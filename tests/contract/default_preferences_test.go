package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssueCreateConfiguredDefaultsAreCredentialFree(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "defaults.toml")
	if err := os.WriteFile(path, []byte("default_project='DEFAULT'\ndefault_issue_type='Task'\nsecret_backend='1password'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "issue", "create", "--config", path, "--summary=Default preview", "--dry-run", "--output=json", "--no-input")
	if exit != 0 || stderr != "" {
		t.Fatalf("configured default preview failed: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
	}
	var frame struct {
		OK   bool `json:"ok"`
		Data struct {
			DryRun  bool `json:"dry_run"`
			Payload struct {
				Fields struct {
					Project struct {
						Key string `json:"key"`
					} `json:"project"`
					IssueType struct {
						Name string `json:"name"`
					} `json:"issuetype"`
				} `json:"fields"`
			} `json:"payload"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || !frame.Data.DryRun || frame.Data.Payload.Fields.Project.Key != "DEFAULT" || frame.Data.Payload.Fields.IssueType.Name != "Task" {
		t.Fatal("configured create defaults were not composed")
	}
}

func TestIssueListDefaultsRespectRawInputAndLocalBuilderGrammar(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "defaults.toml")
	if err := os.WriteFile(path, []byte("default_project='DEFAULT'\ndefault_issue_type='Bug'\nsecret_backend='1password'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "ordinary list", args: []string{"issue", "list", "--as-jql"}, want: `project in ("DEFAULT")`},
		{name: "mine", args: []string{"issue", "mine", "--as-jql"}, want: `project in ("DEFAULT") AND assignee = currentUser() ORDER BY updated ASC`},
		{name: "explicit project", args: []string{"issue", "list", "--as-jql", "--project=FLAGGED"}, want: `project in ("FLAGGED")`},
		{name: "explicit raw query", args: []string{"issue", "list", "--as-jql", "--jql=project = RAW"}, want: "project = RAW"},
		{name: "independent local builder", args: []string{"jql", "build"}, want: "updated >= -365d ORDER BY updated DESC"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			args := append(test.args, "--config", path, "--output=json", "--no-input")
			stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, args...)
			var frame struct {
				OK   bool `json:"ok"`
				Data struct {
					JQL string `json:"jql"`
				} `json:"data"`
			}
			if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || frame.Data.JQL != test.want {
				t.Fatalf("default query precedence changed: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
			}
		})
	}
}

func TestCreateDefaultsNeverReplaceNativeNullOperationsOrExplicitClears(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "defaults.toml")
	if err := os.WriteFile(path, []byte("default_project='DEFAULT'\ndefault_issue_type='Task'\nsecret_backend='1password'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, native := range []string{
		`{"fields":{"summary":"Draft","project":null,"issuetype":{},"description":{"type":"doc","version":1,"content":[],"future":"\u003c&>"}},"future":{"n":9007199254740993}}`,
		`{"fields":{"summary":"Draft"},"update":{"project":[],"issuetype":[{"set":null}]},"future":null}`,
	} {
		t.Run(native, func(t *testing.T) {
			t.Parallel()
			stdout, stderr, exit := runBinaryEnvInputStarted(t, []string{"JIRA_KEYRING=false"}, native, nil, "issue", "create", "--json-input=-", "--config", path, "--dry-run", "--output=json", "--no-input")
			if exit != 0 || stderr != "" {
				t.Fatalf("native default preservation failed: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
			}
			var frame struct {
				OK   bool `json:"ok"`
				Data struct {
					Payload json.RawMessage `json:"payload"`
				} `json:"data"`
			}
			var before, after map[string]json.RawMessage
			if json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || json.Unmarshal([]byte(native), &before) != nil || json.Unmarshal(frame.Data.Payload, &after) != nil || len(before) != len(after) {
				t.Fatal("native request members changed")
			}
			for key, want := range before {
				got := after[key]
				if key == "fields" || key == "update" {
					var wantedMembers, gotMembers map[string]json.RawMessage
					if json.Unmarshal(want, &wantedMembers) != nil || json.Unmarshal(got, &gotMembers) != nil || len(wantedMembers) != len(gotMembers) {
						t.Fatal("native field/operation membership changed", key)
					}
					for field, value := range wantedMembers {
						assertDefaultNativeValue(t, value, gotMembers[field])
					}
					continue
				}
				assertDefaultNativeValue(t, want, got)
			}
		})
	}
	for _, flag := range []string{"--project=", "--type="} {
		stdout, _, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "issue", "create", "--summary=Draft", flag, "--config", path, "--dry-run", "--output=json", "--no-input")
		var frame struct {
			OK     bool `json:"ok"`
			Errors []struct {
				Code string `json:"code"`
			} `json:"errors"`
		}
		if exit != 3 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_required" {
			t.Fatal("explicit clear revived configured create context", stdout)
		}
	}
}

// Compare authored values without treating envelope indentation or top-level
// field sorting as payload changes. Compact preserves escapes and number text.
func assertDefaultNativeValue(t *testing.T, want, got json.RawMessage) {
	t.Helper()
	var expected, actual bytes.Buffer
	if json.Compact(&expected, want) != nil || json.Compact(&actual, got) != nil || !bytes.Equal(expected.Bytes(), actual.Bytes()) {
		t.Fatal("default processing changed an authored native value")
	}
}

func TestWorkingDefaultHelpDescribesEveryAffectedCommand(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		path  []string
		terms []string
	}{
		{[]string{"issue", "create"}, []string{"default_project", "default_issue_type", "absent", "Explicit empty"}},
		{[]string{"issue", "list"}, []string{"default_project", "default_board", "--board=", "cached_at"}},
		{[]string{"issue", "mine"}, []string{"default_project", "default_board", "--board=", "cached_at"}},
		{[]string{"jql", "build"}, []string{"default_board", "default_project", "default_issue_type", "--board="}},
	} {
		stdout, stderr, exit := runBinary(t, append(test.path, "--help")...)
		if exit != 0 || stderr != "" {
			t.Fatal("working-default help did not run")
		}
		for _, term := range test.terms {
			if !strings.Contains(stdout, term) {
				t.Fatal("command help omitted working-default contract", test.path, term)
			}
		}
	}
}
