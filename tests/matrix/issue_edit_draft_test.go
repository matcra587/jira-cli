//go:build flagmatrix

package matrix

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
)

func TestIssueDraftFlagsControlPublicationAndEditorAdmission(t *testing.T) {
	t.Parallel()
	reads := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issue/KEY-1" || r.URL.Query().Get("fields") != "description,updated" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reads.Add(1)
		_, _ = w.Write([]byte(`{"id":"123456","key":"KEY-1","fields":{"updated":null,"description":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"source"}]}]}}}`))
	}))
	defer server.Close()
	options := mx4Opts{Env: map[string]string{"JIRA_BASE_URL": server.URL, "JIRA_EMAIL": mx4Email, "JIRA_API_TOKEN": mx4Token}}
	preview := mx4Run(t, options, "issue", "edit", "draft", "KEY-1", "--directory=planned", "--dry-run", "--output=json")
	var planned envelope.IssueEditDraftOutput
	mx4Data(t, mx4OK(t, preview), &planned)
	if !planned.DryRun || planned.Source != nil || planned.Draft.Directory != filepath.Join(preview.Workdir, "planned") || planned.Draft.DirectoryCreated || reads.Load() != 0 {
		t.Fatal("preview did not select an absent destination without Jira")
	}
	if _, err := os.Stat(planned.Draft.Directory); !os.IsNotExist(err) {
		t.Fatal("preview created a destination", err)
	}
	editorRefused := mx4Run(t, options, "issue", "edit", "draft", "KEY-1", "--directory=editor", "--edit", "--no-input", "--output=json")
	frame := mx4Frame(t, editorRefused.Stdout)
	if editorRefused.Exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "terminal_required" || reads.Load() != 0 {
		t.Fatal("edit did not enforce terminal admission before Jira", editorRefused)
	}
	live := mx4Run(t, options, "issue", "edit", "draft", "KEY-1", "--directory=published", "--output=json")
	var published envelope.IssueEditDraftOutput
	mx4Data(t, mx4OK(t, live), &published)
	if published.DryRun || !published.Draft.Ready || !published.Draft.DirectoryCreated || published.Draft.Directory != filepath.Join(live.Workdir, "published") || reads.Load() != 1 {
		t.Fatal("live flag branch did not publish the chosen directory")
	}
	for _, name := range []string{"source.json", "description.md", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(published.Draft.Directory, name)); err != nil {
			t.Fatal("live publication omitted a member", name, err)
		}
	}
	Prove(t, "jira issue edit draft", "directory", "the chosen literal child contains all three published members and is named in the typed receipt")
	Prove(t, "jira issue edit draft", "dry-run", "preview creates no directory and performs no Jira read; live execution publishes all three members")
	Prove(t, "jira issue edit draft", "edit", "headless editor selection refuses before Jira reads, while preparation without edit succeeds")

	inspect := mx4Run(t, mx4Opts{}, "issue", "edit", "inspect", "--directory", published.Draft.Directory, "--output=json")
	var local envelope.IssueEditInspectOutput
	mx4Data(t, mx4OK(t, inspect), &local)
	if local.Draft.Directory != published.Draft.Directory || local.Source == nil || local.Source.IssueID != "123456" || local.Refreshed || !bytes.Contains(local.Description, []byte("source")) || reads.Load() != 1 {
		t.Fatal("local inspection did not read the selected draft without credentials")
	}
	wrongOptions := mx4Opts{Env: map[string]string{"JIRA_BASE_URL": server.URL, "JIRA_EMAIL": "other@example.test", "JIRA_API_TOKEN": mx4Token}}
	wrongScope := mx4Run(t, wrongOptions, "issue", "edit", "inspect", "--directory", published.Draft.Directory, "--refresh", "--output=json")
	frame = mx4Frame(t, wrongScope.Stdout)
	if wrongScope.Exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "issue_description_scope_changed" || reads.Load() != 1 {
		t.Fatal("refresh ignored bound scope admission", wrongScope)
	}
	Prove(t, "jira issue edit inspect", "directory", "the selected published directory supplies the original native ID and description without credentials")
	Prove(t, "jira issue edit inspect", "refresh", "ordinary local inspection succeeds without scope; refresh refuses a changed account before another Jira read")

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	options = mx4Opts{Env: map[string]string{"JIRA_EDITOR": shell.Quote(executable) + " --wait"}}
	resumed := mx4Run(t, options, "issue", "edit", "resume", "--directory", published.Draft.Directory, "--dry-run", "--output=json")
	var resume envelope.IssueEditDraftOutput
	mx4Data(t, mx4OK(t, resumed), &resume)
	if !resume.DryRun || !resume.Draft.Ready || resume.Draft.EditorAttempted || resume.Draft.Directory != published.Draft.Directory || resume.Source == nil || resume.Source.IssueID != "123456" || len(resume.Editor) != 2 || resume.Editor[1] != "--wait" || len(resume.Description) != 0 {
		t.Fatal("resume preview did not validate saved recovery without editor launch")
	}
	refused := mx4Run(t, options, "issue", "edit", "resume", "--directory", published.Draft.Directory, "--no-input", "--output=json")
	frame = mx4Frame(t, refused.Stdout)
	if refused.Exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "terminal_required" || strings.Contains(refused.Stdout, "description_bytes") {
		t.Fatal("live resume bypassed terminal admission", refused)
	}
	Prove(t, "jira issue edit resume", "directory", "preview reopens the selected recovery directory and reports its native source ID")
	Prove(t, "jira issue edit resume", "dry-run", "preview validates the saved draft and literal editor argv without launching it; live headless resume refuses")
}
