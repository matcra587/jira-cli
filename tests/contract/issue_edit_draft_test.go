package contract

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/envelope"
)

const binaryDraftNative = `{"type":"doc","version":1,"futureCamel":{"nativeMember":"native source value","large":9007199254740993},"content":[{"type":"paragraph","content":[{"type":"text","text":"alpha"}]},{"type":"futureNode","attrs":{"nativeCase":"retained"}}]}`

func binaryDraftServer(t *testing.T) (*httptest.Server, *atomic.Int32, *atomic.Int32, *atomic.Value) {
	t.Helper()
	reads, writes := new(atomic.Int32), new(atomic.Int32)
	current := new(atomic.Value)
	current.Store(binaryDraftNative)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/rest/api/3/issue/KEY-1" && r.URL.Path != "/rest/api/3/issue/123456" || r.URL.Query().Get("fields") != "description,updated" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		account, token, ok := r.BasicAuth()
		if !ok || account != "reader@example.test" || token != "synthetic-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		reads.Add(1)
		if _, err := w.Write([]byte(`{"id":"123456","key":"KEY-1","fields":{"updated":null,"description":` + current.Load().(string) + `}}`)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server, reads, writes, current
}

func binaryDraftEnv(t *testing.T, site string) []string {
	t.Helper()
	config := apiInput(t, []byte("read_only=true\n"))
	return []string{"JIRA_BASE_URL=" + site, "JIRA_EMAIL=reader@example.test", "JIRA_API_TOKEN=synthetic-token", "JIRA_KEYRING=false", "JIRA_SECRET_BACKEND=env", "JIRA_READ_ONLY=true", "JIRA_NO_UPDATE_CHECK=1", "JIRA_CONFIG=" + config}
}

func TestIssueDraftActualBinaryPreparesAndInspectsAllOutputModes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			server, reads, writes, _ := binaryDraftServer(t)
			env := binaryDraftEnv(t, server.URL)
			directory := filepath.Join(t.TempDir(), "literal draft~$")
			stdout, stderr, exit := runBinaryEnv(t, env, "issue", "edit", "draft", "KEY-1", "--directory", directory, "--output="+mode, "--no-input")
			if exit != 0 || reads.Load() != 1 || writes.Load() != 0 {
				t.Fatalf("actual read-only draft failed: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
			}
			if mode == "human" && !strings.Contains(stdout, "Native backup") {
				t.Fatal("human output omitted recovery paths")
			}
			if mode != "human" {
				var out envelope.IssueEditDraftOutput
				if mode == "json" {
					var frame struct {
						OK   bool                          `json:"ok"`
						Data envelope.IssueEditDraftOutput `json:"data"`
					}
					if json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK {
						t.Fatal("broken draft envelope", stdout)
					}
					out = frame.Data
				} else if json.Unmarshal([]byte(stdout), &out) != nil {
					t.Fatal("broken compact draft", stdout)
				}
				if !out.Draft.Ready || !out.Draft.DirectoryCreated || out.Source == nil || out.Source.APIBaseURL != server.URL || out.Source.IssueID != "123456" || string(out.Source.DescriptionBytes) != binaryDraftNative || out.Changed == nil || *out.Changed {
					t.Fatal("binary artifact lost native binding, bytes or effects", out)
				}
			}
			sourcePath, markdownPath := filepath.Join(directory, "source.json"), filepath.Join(directory, "description.md")
			source, err := os.ReadFile(sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			markdown, err := os.ReadFile(markdownPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(markdownPath, bytes.Replace(markdown, []byte("alpha"), []byte("authored"), 1), 0o600); err != nil {
				t.Fatal(err)
			}
			localEnv := append(append([]string{}, env...), "JIRA_BASE_URL=https://unreachable.example.invalid", "JIRA_EMAIL=", "JIRA_API_TOKEN=", "JIRA_KEYRING=false")
			stdout, stderr, exit = runBinaryEnv(t, localEnv, "issue", "edit", "inspect", "--directory", directory, "--output=json", "--no-input")
			var frame struct {
				OK   bool                            `json:"ok"`
				Data envelope.IssueEditInspectOutput `json:"data"`
			}
			if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || frame.Data.Refreshed || frame.Data.Changed == nil || !*frame.Data.Changed || !bytes.Contains(frame.Data.Description, []byte(`"text":"authored"`)) || !bytes.Contains(frame.Data.Description, []byte(`"nativeCase":"retained"`)) || reads.Load() != 1 || writes.Load() != 0 {
				t.Fatalf("local actual inspection lost native state or opened Jira: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
			}
			after, err := os.ReadFile(sourcePath)
			if err != nil || !bytes.Equal(after, source) {
				t.Fatal("inspection overwrote original backup", err)
			}
		})
	}
}

func TestIssueDraftActualBinaryRefreshRefusesBadInputScopeAndConflicts(t *testing.T) {
	t.Parallel()
	server, reads, writes, current := binaryDraftServer(t)
	env := binaryDraftEnv(t, server.URL)
	directory := filepath.Join(t.TempDir(), "draft")
	stdout, stderr, exit := runBinaryEnv(t, env, "issue", "edit", "draft", "KEY-1", "--directory", directory, "--output=json")
	if exit != 0 {
		t.Fatal("draft preparation failed", stdout, stderr)
	}
	markdownPath := filepath.Join(directory, "description.md")
	markdown, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markdownPath, bytes.Replace(markdown, []byte("alpha"), []byte("authored"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	current.Store(strings.Replace(binaryDraftNative, `9007199254740993`, `9007199254740995`, 1))
	stdout, stderr, exit = runBinaryEnv(t, env, "issue", "edit", "inspect", "--directory", directory, "--refresh", "--output=json")
	var frame struct {
		OK   bool                            `json:"ok"`
		Data envelope.IssueEditInspectOutput `json:"data"`
	}
	if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || !frame.Data.Refreshed || frame.Data.Current == nil || !bytes.Contains(frame.Data.Description, []byte("9007199254740995")) || !bytes.Contains(frame.Data.Description, []byte("authored")) || reads.Load() != 2 || writes.Load() != 0 {
		t.Fatal("actual fresh merge lost local/current state", stdout, stderr)
	}
	for _, failure := range []string{"scope", "anchors", "conflict"} {
		t.Run(failure, func(t *testing.T) {
			selectedEnv := append([]string{}, env...)
			if failure == "scope" {
				selectedEnv = append(selectedEnv, "JIRA_EMAIL=other@example.test")
			}
			if failure == "anchors" {
				if err := os.WriteFile(markdownPath, bytes.Replace(markdown, []byte("protected"), []byte("editable"), 1), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "conflict" {
				if err := os.WriteFile(markdownPath, bytes.Replace(markdown, []byte("alpha"), []byte("authored"), 1), 0o600); err != nil {
					t.Fatal(err)
				}
				current.Store(strings.Replace(binaryDraftNative, "alpha", "remote", 1))
			}
			before := reads.Load()
			stdout, stderr, exit := runBinaryEnv(t, selectedEnv, "issue", "edit", "inspect", "--directory", directory, "--refresh", "--output=json")
			var refused struct {
				OK   bool                            `json:"ok"`
				Data envelope.IssueEditInspectOutput `json:"data"`
			}
			if exit != 3 || json.Unmarshal([]byte(stdout), &refused) != nil || refused.OK || refused.Data.Description != nil || refused.Data.Current != nil || refused.Data.Changed != nil || writes.Load() != 0 {
				t.Fatal("failed reconcile retained candidate", stdout, stderr)
			}
			if failure != "conflict" && reads.Load() != before {
				t.Fatal("local refusal entered Jira")
			}
		})
	}
}

func TestIssueDraftActualBinaryPureRefusalsAndResumePreview(t *testing.T) {
	t.Parallel()
	server, reads, writes, _ := binaryDraftServer(t)
	env := binaryDraftEnv(t, server.URL)
	for _, args := range [][]string{
		{"issue", "edit", "draft", "KEY-1"},
		{"issue", "edit", "draft", "KEY-1", "--directory="},
		{"issue", "edit", "draft", "KEY-1", "--directory", filepath.Join(t.TempDir(), "unstarted"), "--edit", "--no-input"},
		{"issue", "edit", "inspect", "--directory", "unstarted", "--web"},
		{"issue", "edit", "resume", "--directory", "unstarted", "--no-input"},
	} {
		stdout, stderr, exit := runBinaryEnv(t, env, append(args, "--output=json")...)
		if exit != 3 || !strings.Contains(stdout, `"ok":false`) || reads.Load() != 0 || writes.Load() != 0 {
			t.Fatal("pure refusal opened Jira", args, stdout, stderr)
		}
	}
	directory := filepath.Join(t.TempDir(), "draft")
	stdout, stderr, exit := runBinaryEnv(t, env, "issue", "edit", "draft", "KEY-1", "--directory", directory, "--output=json")
	if exit != 0 {
		t.Fatal(stdout, stderr)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	selectedEnv := append(append([]string{}, env...), "JIRA_EDITOR="+shell.Quote(executable)+" --wait")
	stdout, stderr, exit = runBinaryEnv(t, selectedEnv, "issue", "edit", "resume", "--directory", directory, "--dry-run", "--no-input", "--output=json")
	var frame struct {
		OK   bool                          `json:"ok"`
		Data envelope.IssueEditDraftOutput `json:"data"`
	}
	if exit != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || !frame.Data.DryRun || frame.Data.Draft.EditorAttempted || frame.Data.Source == nil || frame.Data.Description != nil || len(frame.Data.Editor) != 2 || reads.Load() != 1 || writes.Load() != 0 {
		t.Fatal("actual resume preview launched work or lost recovery", stdout, stderr)
	}
	stdout, stderr, exit = runBinaryEnv(t, env, "issue", "edit", "draft", "KEY-1", "--directory", directory, "--output=json")
	if exit != 3 || !strings.Contains(stdout, "issue_edit_draft_changed") || reads.Load() != 1 || writes.Load() != 0 {
		t.Fatal("existing draft was replayed over", stdout, stderr)
	}
}
