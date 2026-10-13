//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type ownedDefaultConfig struct {
	path   string
	board  string
	digest [sha256.Size]byte
}

// This constructor can only select the verified dedicated project/type. No
// caller-authored file or flag can redirect the default-only create workflow.
func (r *liveRun) workingDefaults(t *testing.T, board string) ownedDefaultConfig {
	t.Helper()
	if !r.verified || r.account == "" || r.manifest.Project == "" || r.issueType == "" {
		t.Fatal("working-default acceptance requires verified dedicated context")
	}
	data := []byte("base_url=" + strconv.Quote(r.manifest.BaseURL) + "\nemail=" + strconv.Quote(r.account) + "\ndefault_project=" + strconv.Quote(r.manifest.Project) + "\ndefault_issue_type=" + strconv.Quote(r.issueType) + "\ndefault_board=" + strconv.Quote(board) + "\nsecret_backend='keyring'\n")
	path := filepath.Join(r.dir, "working-defaults.toml")
	if err := xos.AtomicWrite(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return ownedDefaultConfig{path: path, board: board, digest: sha256.Sum256(data)}
}

func (r *liveRun) checkWorkingDefaults(input ownedDefaultConfig) error {
	if !r.verified || input.path != filepath.Join(r.dir, "working-defaults.toml") {
		return errors.New("default-only creation requires the registered owned configuration")
	}
	data, err := os.ReadFile(input.path)
	if err != nil || sha256.Sum256(data) != input.digest {
		return errors.New("owned default configuration changed before creation")
	}
	return nil
}

// Pin public defaults after inherited environment values. The command still
// has no project/type flags, so acceptance exercises the real default path.
func (r *liveRun) workingDefaultsEnvironment(input ownedDefaultConfig) []string {
	return []string{"JIRA_DEFAULT_PROJECT=" + r.manifest.Project, "JIRA_DEFAULT_ISSUE_TYPE=" + r.issueType, "JIRA_DEFAULT_BOARD=" + input.board, "JIRA_EMAIL=" + r.account}
}

func (r *liveRun) checkDefaultCreatePreview(stdout []byte) error {
	var preview struct {
		OK   bool `json:"ok"`
		Data struct {
			DryRun    bool `json:"dry_run"`
			Attempted bool `json:"attempted"`
			Applied   bool `json:"applied"`
			Payload   struct {
				Fields struct {
					Project struct{ Key string }  `json:"project"`
					Type    struct{ Name string } `json:"issuetype"`
				} `json:"fields"`
			} `json:"payload"`
		} `json:"data"`
	}
	if json.Unmarshal(stdout, &preview) != nil || !preview.OK || !preview.Data.DryRun || preview.Data.Attempted || preview.Data.Applied || preview.Data.Payload.Fields.Project.Key != r.manifest.Project || preview.Data.Payload.Fields.Type.Name != r.issueType {
		return errors.New("default-only preview must prove the dedicated project and issue type before creation")
	}
	return nil
}

func TestLiveWorkingDefaults(t *testing.T) {
	r := newLiveRun(t)
	preferences := r.workingDefaults(t, "")
	summary := r.manifest.RunID + " working-defaults"
	args := []string{"issue", "create", "--config", preferences.path, "--summary", summary, "--label", r.manifest.RunID, "--verify"}
	if err := r.checkWorkingDefaults(preferences); err != nil {
		t.Fatal(err)
	}
	_, preview, previewExit := r.invoke(t, t.Context(), r.workingDefaultsEnvironment(preferences), append(args, "--dry-run")...)
	if err := r.checkDefaultCreatePreview(preview); previewExit != 0 || err != nil {
		t.Fatal("default-only preview did not prove owned context", err)
	}
	r.manifest.Attempts = append(r.manifest.Attempts, summary)
	r.save(t)
	if err := r.checkWorkingDefaults(preferences); err != nil {
		t.Fatal(err)
	}
	created, _, createExit := r.invoke(t, t.Context(), r.workingDefaultsEnvironment(preferences), args...)
	if createExit != 0 || !created.OK {
		t.Fatal("default-only creation failed; inspect the retained attempt and never replay it blindly")
	}
	r.register(t, created.Data.Issue)
	var receipt liveIssue
	if !created.Data.Attempted || !created.Data.Applied || json.Unmarshal(created.Data.Receipt, &receipt) != nil || receipt.ID != created.Data.Issue.ID || receipt.Key != created.Data.Issue.Key {
		t.Fatal("default-only create lost the native acknowledgement")
	}
	stored := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+created.Data.Issue.Key, "--query", "fields=project,issuetype,summary,labels")
	var native struct {
		Fields struct {
			Project struct{ Key string }  `json:"project"`
			Type    struct{ Name string } `json:"issuetype"`
			Summary string                `json:"summary"`
			Labels  []string              `json:"labels"`
		} `json:"fields"`
	}
	if json.Unmarshal(stored.Response.JSON, &native) != nil || native.Fields.Project.Key != r.manifest.Project || native.Fields.Type.Name != r.issueType || native.Fields.Summary != summary || len(native.Fields.Labels) != 1 || native.Fields.Labels[0] != r.manifest.RunID {
		t.Fatal("independent read did not retain dedicated default context and ownership")
	}
	frame, stdout, exit := r.invoke(t, t.Context(), append(r.workingDefaultsEnvironment(preferences), "JIRA_READ_ONLY=true"), "issue", "create", "--generate-json", "--config", preferences.path)
	var metadata struct {
		Data envelope.IssueCreateInputOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &metadata) != nil || !metadata.Data.Complete {
		t.Fatal("default-only live metadata discovery did not complete")
	}
	boardID, err := strconv.Atoi(requiredEnv(t, "JIRA_LIVE_BOARD_ID"))
	if err != nil || boardID <= 0 {
		t.Fatal("working-default board acceptance needs a positive board ID")
	}
	configuration := rawRead(t, r, "api", "get", "/rest/agile/1.0/board/"+strconv.Itoa(boardID)+"/configuration")
	var board struct {
		Filter struct{ ID string } `json:"filter"`
	}
	if json.Unmarshal(configuration.Response.JSON, &board) != nil || board.Filter.ID == "" {
		t.Fatal("independent board configuration lacks its filter")
	}
	filter := rawRead(t, r, "api", "get", "/rest/api/3/filter/"+board.Filter.ID)
	var nativeFilter struct{ JQL string }
	if json.Unmarshal(filter.Response.JSON, &nativeFilter) != nil || nativeFilter.JQL == "" {
		t.Fatal("independent board filter lacks JQL")
	}
	name := requiredEnv(t, "JIRA_LIVE_BOARD_NAME")
	cache := "XDG_CACHE_HOME=" + filepath.Join(r.dir, "default-board-cache")
	warm := append(r.workingDefaultsEnvironment(preferences), cache, "JIRA_READ_ONLY=true")
	frame, _, exit = r.invoke(t, t.Context(), warm, "issue", "list", "--board="+name, "--refresh", "--as-jql")
	if exit != 0 || !frame.OK {
		t.Fatal("explicit named-board warming failed")
	}
	preferences = r.workingDefaults(t, strings.ToUpper(name))
	offline := append(r.workingDefaultsEnvironment(preferences), cache, "JIRA_KEYRING=false", "DBUS_SESSION_BUS_ADDRESS=/dev/null")
	conditions, order := command.SplitJQLOrderBy(nativeFilter.JQL)
	want := "(" + conditions + ") AND assignee = currentUser()"
	if order != "" {
		want += " " + order
	}
	frame, stdout, exit = r.invoke(t, t.Context(), offline, "issue", "mine", "--config", preferences.path, "--as-jql")
	var query struct {
		Data envelope.IssueListOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &query) != nil || query.Data.JQL != want || query.Data.Board == nil || query.Data.Board.Precedence != "default_board" || query.Data.Board.CachedAt == nil || query.Data.Board.CachedAt.IsZero() || query.Data.Board.ID != int64(boardID) || query.Data.Board.JQL != nativeFilter.JQL {
		t.Fatal("offline configured board lost independent filter, account scope or provenance")
	}
	frame, countOutput, countExit := r.invoke(t, t.Context(), append(r.workingDefaultsEnvironment(preferences), cache, "JIRA_READ_ONLY=true"), "issue", "mine", "--config", preferences.path, "--count")
	var counted struct {
		Data envelope.IssueListOutput `json:"data"`
	}
	if countExit != 0 || !frame.OK || json.Unmarshal(countOutput, &counted) != nil || counted.Data.Count == nil || counted.Data.Board == nil || counted.Data.Board.ID != int64(boardID) || counted.Data.Board.Precedence != "default_board" || counted.Data.Board.CachedAt == nil || counted.Data.Board.JQL != nativeFilter.JQL {
		t.Fatal("live configured-board count lost the matching account binding or snapshot provenance")
	}
	frame, stdout, exit = r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "jql", "validate", "--mode=strict", "--", query.Data.JQL)
	var validated struct {
		Data envelope.JQLValidateOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &validated) != nil || len(validated.Data.Queries) != 1 || !validated.Data.Queries[0].Valid {
		t.Fatal("Jira strict parser rejected the retained default-board scope")
	}
}

func TestWorkingDefaultsMutationGuardRejectsChangedAndForeignFiles(t *testing.T) {
	r := &liveRun{dir: t.TempDir(), verified: true, account: "public@example.invalid", issueType: "Task", manifest: liveManifest{BaseURL: "https://example.invalid", Project: "OWNED"}}
	input := r.workingDefaults(t, "")
	if err := r.checkWorkingDefaults(input); err != nil {
		t.Fatal(err)
	}
	foreign := input
	foreign.path = filepath.Join(t.TempDir(), "working-defaults.toml")
	if err := r.checkWorkingDefaults(foreign); err == nil {
		t.Fatal("foreign file passed the owned default guard")
	}
	if err := os.WriteFile(input.path, []byte("default_project='FOREIGN'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.checkWorkingDefaults(input); err == nil {
		t.Fatal("changed dedicated project passed the default mutation guard")
	}
	r.verified = false
	if err := r.checkWorkingDefaults(input); err == nil {
		t.Fatal("unverified run passed the default mutation guard")
	}
}

func TestWorkingDefaultsInheritedEnvironmentCannotRedirectOwnedCreate(t *testing.T) {
	r := &liveRun{dir: t.TempDir(), verified: true, account: "public@example.invalid", issueType: "Task", manifest: liveManifest{BaseURL: "https://example.invalid", Project: "OWNED"}}
	input := r.workingDefaults(t, "")
	inherited := []string{"JIRA_DEFAULT_PROJECT=FOREIGN", "JIRA_DEFAULT_ISSUE_TYPE=Foreign type", "JIRA_DEFAULT_BOARD=Foreign board", "JIRA_EMAIL=foreign@example.invalid"}
	child := exec.Command("never-executed")
	child.Env = append(inherited, r.workingDefaultsEnvironment(input)...)
	resolved, err := config.Load(config.Options{Path: input.path, Environ: child.Environ})
	if err != nil || resolved.Config.DefaultProject != "OWNED" || resolved.Config.DefaultIssueType != "Task" || resolved.Config.DefaultBoard != "" || resolved.Config.Email != r.account {
		t.Fatal("inherited environment redirected owned default context", err)
	}
	for _, data := range []string{
		`{"ok":true,"data":{"dry_run":true,"payload":{"fields":{"project":{"key":"FOREIGN"},"issuetype":{"name":"Task"}}}}}`,
		`{"ok":true,"data":{"dry_run":true,"payload":{"fields":{"project":{"key":"OWNED"},"issuetype":{"name":"Other"}}}}}`,
		`{"ok":true,"data":{"dry_run":true,"attempted":true,"payload":{"fields":{"project":{"key":"OWNED"},"issuetype":{"name":"Task"}}}}}`,
	} {
		if err := r.checkDefaultCreatePreview([]byte(data)); err == nil {
			t.Fatal("unproved preview permitted a live create")
		}
	}
	if err := r.checkDefaultCreatePreview([]byte(`{"ok":true,"data":{"dry_run":true,"payload":{"fields":{"project":{"key":"OWNED"},"issuetype":{"name":"Task"}}}}}`)); err != nil {
		t.Fatal(err)
	}
}
