package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/jql"
)

func TestCommandsUseConfiguredJiraServices(t *testing.T) {
	var seenJQL requestCapture[string]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql":
			var body struct {
				JQL string `json:"jql"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode search body: %v", err)
			}
			seenJQL.Add(body.JQL)
			switch body.JQL {
			case jql.DefaultIssueListJQL:
				_, _ = w.Write([]byte(`{"isLast":true,"issues":[{"key":"PROJ-1","fields":{"summary":"From server","status":{"name":"To Do"},"priority":{"name":"High"},"updated":"2026-05-03T10:00:00Z"}}]}`))
			case "project = CUSTOM ORDER BY updated DESC":
				_, _ = w.Write([]byte(`{"isLast":true,"issues":[{"key":"CUSTOM-1","fields":{"summary":"Custom search"}}]}`))
			case "project = PROJ":
				_, _ = w.Write([]byte(`{"isLast":true,"issues":[{"key":"PROJ-2","fields":{"summary":"Search hit"}}]}`))
			default:
				t.Fatalf("unexpected JQL %q", body.JQL)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/PROJ-1/worklog":
			_, _ = w.Write([]byte(`{"worklogs":[{"id":"1","timeSpentSeconds":60}]}`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	cfg := jiraConfig(t, srv.URL)
	for _, tc := range []struct {
		args []string
		key  string
		val  any
	}{
		{[]string{"issue", "list", "--output=json"}, "key", "PROJ-1"},
		{[]string{"issue", "list", "--jql", "project = CUSTOM", "--output=json"}, "key", "CUSTOM-1"},
		{[]string{"search", "jql", "project = PROJ", "--output=json"}, "key", "PROJ-2"},
		{[]string{"worklog", "list", "PROJ-1", "--output=json"}, "timeSpentSeconds", 60},
	} {
		cmd := exec.Command(buildJiraBinary(t), append([]string{"--config", cfg}, tc.args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("jira %v error = %v\n%s", tc.args, err, out)
		}
		if !envelopeHasKV(t, out, tc.key, tc.val) {
			t.Fatalf("jira %v output missing %s=%v:\n%s", tc.args, tc.key, tc.val, out)
		}
	}
	for _, want := range []string{jql.DefaultIssueListJQL, "project = CUSTOM ORDER BY updated DESC", "project = PROJ"} {
		if !slices.Contains(seenJQL.Values(), want) {
			t.Fatalf("missing JQL %q in %v", want, seenJQL.Values())
		}
	}
}

func TestIssueCommentCommandConvertsMarkdownToADF(t *testing.T) {
	cmd := exec.Command(buildJiraBinary(t), "issue", "comment", "PROJ-1", "--markdown", "hello **world**", "--dry-run", "--no-input", "--output=json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("issue comment error = %v\n%s", err, out)
	}
	var env struct {
		Data struct {
			Comment struct {
				Body map[string]any `json:"body"`
			} `json:"comment"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("comment output is not JSON: %v\n%s", err, out)
	}
	if env.Data.Comment.Body["type"] != "doc" {
		t.Fatalf("comment body is not ADF: %+v", env.Data.Comment.Body)
	}
}

func TestJSONErrorsUseStdoutEnvelopeAndExitCodes(t *testing.T) {
	bin := buildJiraBinary(t)
	cmd := exec.Command(bin, "worklog", "add", "PROJ-1", "--time-spent", "wat", "--output=json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("invalid duration succeeded:\nstdout=%s", stdout.String())
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("exit = %v stdout=%s", err, stdout.String())
	}
	// Machine mode: the JSON envelope on stdout carries the diagnostic in
	// errors[]; stderr stays free of a human clog line.
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty in machine mode", stderr.String())
	}
	var env map[string]any
	decodeErrorEnvelopeFromStdout(t, stdout.Bytes(), stderr.Bytes(), cmd.Args, &env)
	errs, _ := env["errors"].([]any)
	if len(errs) == 0 {
		t.Fatalf("envelope.errors is empty:\nstdout=%s", stdout.String())
	}
	first, _ := errs[0].(map[string]any)
	if msg, _ := first["message"].(string); !strings.Contains(msg, "invalid duration") {
		t.Fatalf("envelope.errors[0].message = %q; want to contain %q", msg, "invalid duration")
	}
}

func TestSchemaIncludesFlagsAndOutputSchemas(t *testing.T) {
	root := loadAgentSchemaShapes(t)
	for _, path := range []string{"jira issue list", "jira issue create", "jira worklog add"} {
		cmd := findSchemaCommand(root, path)
		if cmd == nil {
			t.Fatalf("schema missing path %q", path)
		}
		if len(cmd.Flags) == 0 {
			t.Fatalf("%s schema carries no flags", path)
		}
		if cmd.OutputSchema == nil {
			t.Fatalf("%s schema missing embedded output schema", path)
		}
	}
	if findSchemaFlag(findSchemaCommand(root, "jira issue create").Flags, "dry-run") == nil {
		t.Fatalf("jira issue create schema missing --dry-run flag")
	}
}

func jiraConfig(t *testing.T, baseURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `default_profile = "default"
queries_path = "` + filepath.ToSlash(t.TempDir()) + `/queries"

[[profiles]]
name = "default"
base_url = "` + baseURL + `"
auth_type = "token"
secret_backend = "keyring"
refresh_interval = 30
timeout = 30
workday_seconds = 28800
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
