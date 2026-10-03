//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/envelope"
	wire "github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// parentInput authors only the fixed parent relation between two active issues
// owned by this run. The native input guard rechecks both identities and bytes.
func (r *liveRun) parentInput(t *testing.T, child, parent string) string {
	t.Helper()
	if !r.verified || r.ownedLiveIssue(child) == nil || r.ownedLiveIssue(parent) == nil || child == parent {
		t.Fatal("parent input requires two distinct active owned issues")
	}
	data, err := json.Marshal(map[string]any{"fields": map[string]any{"parent": map[string]string{"key": parent}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.dir, "owned-parent-"+output.NewRequestID()+".json")
	if err := xos.AtomicWrite(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.issueInputs == nil {
		r.issueInputs = make(map[string]nativeIssueInput)
	}
	r.issueInputs[path] = nativeIssueInput{key: child, parent: parent, digest: sha256.Sum256(data)}
	return path
}

func createLiveEpic(t *testing.T, r *liveRun, suffix string) string {
	t.Helper()
	// Recheck current project metadata before creating an Epic fixture. Neither
	// an old numeric type ID nor an unavailable issue type is assumed.
	metadata := rawRead(t, r, "api", "get", "/rest/api/3/issue/createmeta/"+url.PathEscape(r.manifest.Project)+"/issuetypes", "--query=maxResults=100")
	var types struct {
		Total      int `json:"total"`
		StartAt    int `json:"startAt"`
		IssueTypes []struct {
			ID, Name string
			Subtask  bool
		} `json:"issueTypes"`
	}
	if json.Unmarshal(metadata.Response.JSON, &types) != nil || types.IssueTypes == nil || types.StartAt != 0 || types.Total != len(types.IssueTypes) {
		t.Fatal("Epic fixture metadata is incomplete")
	}
	found := false
	for _, typ := range types.IssueTypes {
		if typ.Name == "Epic" && typ.ID != "" && !typ.Subtask {
			found = true
		}
	}
	if !found {
		t.Fatal("dedicated project has no available Epic fixture type")
	}
	previous := r.issueType
	r.issueType = "Epic"
	epic := r.create(t, suffix)
	r.issueType = previous
	return epic
}

func TestLiveEpicReads(t *testing.T) {
	r := newLiveRun(t)
	epic := createLiveEpic(t, r, "epic-count-parent")
	child := r.create(t, "epic-count-child")
	path := r.parentInput(t, child, epic)
	r.mutate(t, []string{child}, "issue", "edit", child, "--json-input", path)
	stored := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(child), "--query=fields=parent,status")
	var relation struct {
		Fields struct {
			Parent struct {
				Key string `json:"key"`
			} `json:"parent"`
			Status struct {
				Name string `json:"name"`
			} `json:"status"`
		} `json:"fields"`
	}
	if json.Unmarshal(stored.Response.JSON, &relation) != nil || relation.Fields.Parent.Key != epic || relation.Fields.Status.Name == "" {
		t.Fatal("owned child parent/status was not independently stored")
	}
	waitForEpicChildIndex(t, r, epic, child)
	query := "issuetype = Epic AND project in (" + strconv.Quote(r.manifest.Project) + ")"
	expected := rawEpicSearch(t, r, query)
	found := false
	for _, row := range expected {
		if row.Key == epic {
			found = true
		}
	}
	if !found {
		t.Fatal("owned Epic absent from search; nonempty listing acceptance is unproven")
	}
	frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "epic", "list", "--project", r.manifest.Project, "--all")
	var list struct {
		Data envelope.EpicListOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &list) != nil || list.Data.JQL != query || list.Data.Truncated || !list.Data.Pagination.IsLast || list.Data.Pagination.Returned != len(expected) || len(list.Data.Epics) != len(expected) {
		t.Fatal("live epic listing lost scope, results or completion")
	}
	wantEpics := make(map[string]wire.IssueBean, len(expected))
	for _, row := range expected {
		wantEpics[row.Key] = row.Bean
	}
	for _, row := range list.Data.Epics {
		bean, ok := wantEpics[row.Key]
		if !ok || row.Fields.Summary.Value != bean.Fields.Value.Summary.Value || row.Fields.Status.Value.Name.Value != bean.Fields.Value.Status.Value.Name.Value {
			t.Fatal("typed epic list differs from independent raw search")
		}
	}
	frame, stdout, exit = r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "epic", "board", "--project", r.manifest.Project, "--all")
	var board struct {
		Data envelope.EpicBoardOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &board) != nil || !board.Data.CountsComplete || board.Data.Truncated || !board.Data.Pagination.IsLast || board.Data.Pagination.Returned != len(expected) || len(board.Data.Epics) != len(expected) || board.Data.UnknownStatus != 0 {
		t.Fatal("live epic board failed to report complete visible counts")
	}
	totals := map[string]int{"To Do": 0, "In Progress": 0, "Done": 0}
	found = false
	for _, row := range board.Data.Epics {
		if _, ok := wantEpics[row.Key]; !ok {
			t.Fatal("board returned a foreign epic")
		}
		children := rawEpicSearch(t, r, "parent = "+strconv.Quote(row.Key))
		counts := map[string]int{"To Do": 0, "In Progress": 0, "Done": 0}
		for _, candidate := range children {
			name := candidate.Bean.Fields.Value.Status.Value.Name.Value
			if name == "" {
				t.Fatal("raw child status is unknown; complete count acceptance is unproven")
			}
			counts[name]++
			totals[name]++
			if row.Key == epic && candidate.Key == child && name == relation.Fields.Status.Name {
				found = true
			}
		}
		if !row.Children.Attempted || !row.Children.Complete || row.Children.Truncated || row.Children.Error != nil || !row.Children.Pagination.IsLast || row.Children.Pagination.Returned != len(children) || !reflect.DeepEqual(row.Counts, counts) {
			t.Fatal("per-epic counts differ from independently drained child search")
		}
	}
	if !found || !reflect.DeepEqual(board.Data.Totals, totals) {
		t.Fatal("board lost the owned child or aggregate counts")
	}
}

type rawEpicRow struct {
	Key  string
	Bean wire.IssueBean
}

// Decode completion independently of the domain search validator and walker.
// Raw reads remain read-only and are bounded for acceptance resource control.
func rawEpicSearch(t *testing.T, r *liveRun, query string, fields ...string) []rawEpicRow {
	t.Helper()
	if len(fields) == 0 {
		fields = []string{"summary", "status"}
	}
	rows := []rawEpicRow{}
	token := ""
	seen := map[string]bool{}
	for range 50 {
		body, err := json.Marshal(map[string]any{"jql": query, "fields": fields, "maxResults": 100, "nextPageToken": token})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(r.dir, "epic-raw-search-"+output.NewRequestID()+".json")
		if err := xos.AtomicWrite(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		raw := rawRead(t, r, "api", "post", "/rest/api/3/search/jql", "--input", path)
		var page struct {
			IsLast *bool            `json:"isLast"`
			Next   string           `json:"nextPageToken"`
			Issues []wire.IssueBean `json:"issues"`
		}
		if json.Unmarshal(raw.Response.JSON, &page) != nil || page.IsLast == nil || page.Issues == nil || len(page.Issues) > 100 {
			t.Fatal("raw epic search lacks valid completion/results")
		}
		for _, bean := range page.Issues {
			if bean.Key.Value == "" || seen[bean.Key.Value] {
				t.Fatal("raw epic search repeats or omits identity")
			}
			seen[bean.Key.Value] = true
			rows = append(rows, rawEpicRow{Key: bean.Key.Value, Bean: bean})
		}
		if *page.IsLast {
			return rows
		}
		if page.Next == "" || page.Next == token {
			t.Fatal("raw epic search lacks progress")
		}
		token = page.Next
	}
	t.Fatal("raw epic search reached its acceptance bound")
	return nil
}

// A confirmed parent edit can precede enhanced-search index visibility. Require
// two consecutive matching observations through raw and domain reads before
// comparing separately timed aggregate snapshots. Only reads are repeated.
func waitForEpicChildIndex(t *testing.T, r *liveRun, epic, child string) {
	t.Helper()
	query := "parent = " + strconv.Quote(epic)
	stable := 0
	for attempt := range 10 {
		raw := rawEpicSearch(t, r, query, "status")
		frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "list", "--jql", query, "--fields=status", "--all")
		var domain struct {
			Data envelope.IssueListOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &domain) != nil || domain.Data.Truncated || !domain.Data.Pagination.IsLast {
			t.Fatal("owned child index probe failed")
		}
		visible := len(raw) == 1 && raw[0].Key == child && len(domain.Data.Issues) == 1 && domain.Data.Issues[0].Key == child
		t.Logf("owned child index observation %d: raw=%d domain=%d matched=%t", attempt+1, len(raw), len(domain.Data.Issues), visible)
		if visible {
			stable++
		} else {
			stable = 0
		}
		if stable == 2 {
			return
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal("owned child index probe canceled")
		case <-timer.C:
		}
	}
	t.Fatal("owned child has not reached a stable search index; aggregate acceptance remains unproven")
}
