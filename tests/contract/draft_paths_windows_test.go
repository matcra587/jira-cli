//go:build windows && nativefilesystem

package contract

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/testfixture"
)

func TestNativePublicWindowsDraftPaths(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human"} {
		for _, selection := range []string{"junction", "extended", "dot", "space"} {
			t.Run(mode+"/"+selection, func(t *testing.T) {
				parent := t.TempDir()
				selected := filepath.Join(parent, "draft 古い ~$")
				canonical := selected
				verify := func() {}
				switch selection {
				case "junction":
					actual, link := filepath.Join(parent, "actual '古い"), filepath.Join(parent, "selected 'é")
					if err := os.MkdirAll(filepath.Join(actual, "nested"), 0o700); err != nil {
						t.Fatal(err)
					}
					before := testfixture.Junction(t, link, actual)
					selected, canonical = filepath.Join(link, "nested", "draft"), filepath.Join(actual, "nested", "draft")
					verify = func() { testfixture.SameJunction(t, link, actual, before) }
				case "extended":
					selected = `\\?\` + selected
				case "dot", "space":
					ordinary := selected
					if err := os.Mkdir(ordinary, 0o700); err != nil {
						t.Fatal(err)
					}
					suffix := "."
					if selection == "space" {
						suffix = " "
					}
					selected = `\\?\` + selected + suffix
					canonical = selected
					t.Cleanup(func() {
						if err := os.RemoveAll(canonical); err != nil {
							t.Error(err)
						}
					})
					verify = func() {
						entries, err := os.ReadDir(ordinary)
						if err != nil || len(entries) != 0 {
							t.Fatalf("draft changed its ordinary sibling: %v %v", entries, err)
						}
					}
				}
				server, reads, writes, _ := binaryDraftServer(t)
				var requests atomic.Int32
				observed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					server.Config.Handler.ServeHTTP(w, r)
				}))
				t.Cleanup(observed.Close)
				env := binaryDraftEnv(t, observed.URL)
				invoke := func(args ...string) string {
					t.Helper()
					stdout, stderr, exit := runBinaryEnv(t, env, append(args, "--output="+mode, "--no-input")...)
					if exit != 0 || stderr != "" || stdout == "" {
						t.Fatalf("native draft command failed: exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
					}
					return stdout
				}
				for _, preview := range []bool{true, false} {
					args := []string{"issue", "edit", "draft", "KEY-1", "--directory", selected}
					if preview {
						args = append(args, "--dry-run")
					}
					stdout := invoke(args...)
					if mode == "human" {
						if !strings.Contains(stdout, canonical) || !strings.Contains(stdout, "Native backup") {
							t.Fatalf("native draft lost human recovery paths: %q", stdout)
						}
					} else {
						var got envelope.IssueEditDraftOutput
						decodeWindowsJunctionOutput(t, mode, stdout, "issue.edit.draft", &got)
						if got.Draft.Directory != canonical || got.DryRun != preview || got.Draft.Ready == preview || got.Draft.DirectoryCreated == preview {
							t.Fatalf("native draft lost canonical path or publication state: %+v", got)
						}
						if !preview && (got.Source == nil || string(got.Source.DescriptionBytes) != binaryDraftNative || got.Changed == nil || *got.Changed || got.Draft.Durability != "platform_unconfirmed") {
							t.Fatalf("native draft lost exact source or acknowledged outcome: %+v", got)
						}
					}
					if preview {
						if _, err := os.Lstat(canonical); !os.IsNotExist(err) || reads.Load() != 0 || writes.Load() != 0 || requests.Load() != 0 {
							t.Fatalf("native preview published or contacted Jira: %v", err)
						}
					} else if reads.Load() != 1 || writes.Load() != 0 || requests.Load() != 1 {
						t.Fatalf("draft preparation made unexpected requests: reads=%d writes=%d total=%d", reads.Load(), writes.Load(), requests.Load())
					}
				}
				sourcePath, markdownPath := filepath.Join(canonical, "source.json"), filepath.Join(canonical, "description.md")
				source, err := os.ReadFile(sourcePath)
				if err != nil {
					t.Fatal(err)
				}
				markdown, err := os.ReadFile(markdownPath)
				if err != nil {
					t.Fatal(err)
				}
				authored := bytes.Replace(markdown, []byte("alpha"), []byte("authored"), 1)
				if bytes.Equal(authored, markdown) {
					t.Fatal("fixture did not find editable Markdown")
				}
				if err := os.WriteFile(markdownPath, authored, 0o600); err != nil {
					t.Fatal(err)
				}
				env = append(env, "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_SECRET_BACKEND=1password", "OP_SERVICE_ACCOUNT_TOKEN=", "JIRA_EDITOR="+shell.Quote(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")))
				stdout := invoke("issue", "edit", "inspect", "--directory", selected)
				if requests.Load() != 1 {
					t.Fatalf("local inspection contacted Jira: total=%d", requests.Load())
				}
				if mode == "human" {
					if !strings.Contains(stdout, canonical) || !strings.Contains(stdout, "Native backup") {
						t.Fatalf("native local inspection lost recovery paths: %q", stdout)
					}
				} else {
					var got envelope.IssueEditInspectOutput
					decodeWindowsJunctionOutput(t, mode, stdout, "issue.edit.inspect", &got)
					if got.Draft.Directory != canonical || got.Refreshed || got.Changed == nil || !*got.Changed || got.Source == nil || string(got.Source.DescriptionBytes) != binaryDraftNative || !bytes.Contains(got.Description, []byte(`"text":"authored"`)) || !bytes.Contains(got.Description, []byte(`"nativeCase":"retained"`)) || !bytes.Contains(got.Description, []byte(`9007199254740993`)) {
						t.Fatalf("native local inspection changed scope or lost protected data: %+v", got)
					}
				}
				stdout = invoke("issue", "edit", "resume", "--directory", selected, "--dry-run")
				if mode == "human" {
					if !strings.Contains(stdout, canonical) || !strings.Contains(stdout, "Native backup") {
						t.Fatalf("native resume preview lost recovery paths: %q", stdout)
					}
				} else {
					var got envelope.IssueEditDraftOutput
					decodeWindowsJunctionOutput(t, mode, stdout, "issue.edit.resume", &got)
					if !got.DryRun || got.Draft.Directory != canonical || got.Draft.EditorAttempted || got.Draft.EditorSucceeded || got.Source == nil || string(got.Source.DescriptionBytes) != binaryDraftNative {
						t.Fatalf("native resume preview attempted editing or lost source: %+v", got)
					}
				}
				after, err := os.ReadFile(sourcePath)
				saved, savedErr := os.ReadFile(markdownPath)
				if err != nil || savedErr != nil || !bytes.Equal(after, source) || !bytes.Equal(saved, authored) || reads.Load() != 1 || writes.Load() != 0 || requests.Load() != 1 {
					t.Fatalf("local workflow changed saved work or contacted Jira: source=%v saved=%v reads=%d writes=%d total=%d", err, savedErr, reads.Load(), writes.Load(), requests.Load())
				}
				verify()
			})
		}
	}
}
