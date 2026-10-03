//go:build live

package live

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestLiveIssueLifecycle(t *testing.T) {
	r := newLiveRun(t)
	readKey := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	boardID := requiredEnv(t, "JIRA_LIVE_BOARD_ID")
	fixture := r.success(t, "issue", "view", readKey)
	if fixture.Data.Issue.Key != readKey {
		t.Fatal("designated read fixture identity changed")
	}
	page := r.success(t, "issue", "list", "--project", r.manifest.Project, "--limit", "1")
	if len(page.Data.Issues) != 1 {
		t.Fatal("dedicated project's first page must contain one issue")
	}
	if !page.Data.Pagination.IsLast {
		if page.Data.Pagination.NextPageToken == "" {
			t.Fatal("nonfinal page omitted its cursor")
		}
		next := r.success(t, "issue", "list", "--project", r.manifest.Project, "--limit", "1",
			"--next-page-token", page.Data.Pagination.NextPageToken)
		if len(next.Data.Issues) != 1 || next.Data.Issues[0].Key == page.Data.Issues[0].Key {
			t.Fatal("cursor did not advance to a distinct issue")
		}
	}
	r.success(t, "issue", "list", "--board-id", boardID, "--limit", "1")
	first := r.create(t, "first")
	second := r.create(t, "second")
	deleted := r.create(t, "partial-failure")
	r.mutate(t, []string{deleted}, "issue", "delete", deleted, "--force")
	missing, _, missingExit := r.invoke(t, t.Context(), nil, "issue", "view", deleted)
	if missingExit != 2 || missing.OK || len(missing.Errors) != 1 || missing.Errors[0].Code != "jira_not_found" {
		t.Fatal("partial-failure fixture deletion was not confirmed")
	}
	for i := range r.manifest.Issues {
		if r.manifest.Issues[i].Key == deleted {
			r.manifest.Issues[i].Deleted = true
		}
	}
	r.save(t)
	// The failed target was created and deleted by this run. No fabricated key
	// or pre-existing tenant issue is used to induce partial failure.
	partial := r.mutate(t, []string{first, deleted}, "issue", "edit", first, deleted,
		"--summary", r.manifest.RunID+" edited", "--parallelism", "2")
	if len(partial.Data.Results) != 2 || !partial.Data.Results[0].OK || partial.Data.Results[1].OK ||
		partial.Data.Results[0].Issue.Key != first || partial.Data.Results[1].Issue.Key != deleted {
		t.Fatal("edit did not preserve ordered partial outcomes")
	}
	stored := r.success(t, "issue", "view", first, "--fields", "summary,description,status")
	if !strings.Contains(string(stored.Data.Issue.Fields), r.manifest.RunID+" edited") {
		t.Fatal("successful edit was not stored")
	}
	var fields struct {
		Description json.RawMessage `json:"description"`
	}
	if err := json.Unmarshal(stored.Data.Issue.Fields, &fields); err != nil {
		t.Fatal(err)
	}
	if adfText(t, fields.Description) != "Acceptance bold <&> \u2028 end" {
		t.Fatal("creation did not preserve the expected Markdown body text")
	}
	before := r.success(t, "issue", "view", first, "--fields", "summary")
	blocked, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "edit", first,
		"--summary", r.manifest.RunID+" must-not-land")
	if exit != 3 || blocked.OK || len(blocked.Errors) != 1 || blocked.Errors[0].Code != "read_only_blocked" {
		t.Fatal("read-only mutation was not refused")
	}
	after := r.success(t, "issue", "view", first, "--fields", "summary")
	if string(before.Data.Issue.Fields) != string(after.Data.Issue.Fields) {
		t.Fatal("read-only attempt changed stored fields")
	}
	preview := r.mutate(t, []string{first}, "issue", "edit", first,
		"--summary", r.manifest.RunID+" preview-only", "--dry-run")
	if !preview.Data.DryRun {
		t.Fatal("preview did not identify itself")
	}
	after = r.success(t, "issue", "view", first, "--fields", "summary")
	if string(before.Data.Issue.Fields) != string(after.Data.Issue.Fields) {
		t.Fatal("dry-run changed stored fields")
	}
	comment := r.mutate(t, []string{first}, "issue", "comment", "add", first,
		"--markdown", r.manifest.RunID+" comment")
	if len(comment.Data.Results) != 1 || !comment.Data.Results[0].OK {
		t.Fatal("comment add did not identify the successful target")
	}
	commentID := postedADFCommentID(t, comment)
	r.registerComment(t, first, commentID)
	commentPage := r.success(t, "issue", "comment", "list", first, "--limit", "1")
	if len(commentPage.Data.Comments) != 1 {
		t.Fatal("comment page did not honor its one-comment limit")
	}
	workflow := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(first)+"/transitions")
	var available struct {
		Transitions []struct {
			ID        string
			HasScreen *bool `json:"hasScreen"`
			To        struct{ ID, Name string }
		}
	}
	if json.Unmarshal(workflow.Response.JSON, &available) != nil || available.Transitions == nil {
		t.Fatal("independent transition discovery returned no explicit workflow list")
	}
	expectedID, expectedStatusID := "", ""
	expectedScreen := false
	for _, item := range available.Transitions {
		if item.To.Name == "Done" {
			if expectedID != "" || item.ID == "" || item.To.ID == "" || item.HasScreen == nil {
				t.Fatal("independent destination discovery is ambiguous or incomplete")
			}
			expectedID, expectedStatusID, expectedScreen = item.ID, item.To.ID, *item.HasScreen
		}
	}
	if expectedID == "" {
		t.Fatal("owned issue has no independently discovered Done destination")
	}
	transition := r.mutate(t, []string{first}, "issue", "transition", first, "Done",
		"--comment", r.manifest.RunID+" transition comment")
	if len(transition.Data.Results) != 1 || !transition.Data.Results[0].OK {
		t.Fatal("compound transition did not succeed")
	}
	var moved struct {
		Attempted, Applied bool
		Transition         struct {
			ID        string
			HasScreen bool `json:"has_screen"`
		}
		CommentPosted string `json:"comment_posted"`
		CommentID     string `json:"comment_id"`
	}
	if err := json.Unmarshal(transition.Data.Results[0].Data, &moved); err != nil {
		t.Fatal(err)
	}
	if moved.CommentPosted != "atomic" && moved.CommentPosted != "separate" {
		t.Fatal("compound transition did not report a completed comment route")
	}
	if !moved.Attempted || !moved.Applied || moved.Transition.ID != expectedID || moved.Transition.HasScreen != expectedScreen || (expectedScreen && moved.CommentPosted != "atomic") || (!expectedScreen && (moved.CommentPosted != "separate" || moved.CommentID == "")) {
		t.Fatal("transition receipt differs from independent native destination and screen metadata")
	}
	if moved.CommentID != "" {
		r.registerComment(t, first, moved.CommentID)
		nativeComment := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(first)+"/comment/"+url.PathEscape(moved.CommentID))
		var posted struct {
			ID   string
			Body json.RawMessage
		}
		if json.Unmarshal(nativeComment.Response.JSON, &posted) != nil || posted.ID != moved.CommentID || adfText(t, posted.Body) != r.manifest.RunID+" transition comment" {
			t.Fatal("separate transition comment identity or stored body differs from its receipt")
		}
	}
	comments := r.success(t, "issue", "comment", "list", first, "--all")
	nativeComments := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(first)+"/comment", "--query=startAt=0", "--query=maxResults=100")
	var nativePage struct {
		StartAt, MaxResults, Total int
		Comments                   []struct {
			ID   string
			Body json.RawMessage
		}
	}
	if json.Unmarshal(nativeComments.Response.JSON, &nativePage) != nil || nativePage.StartAt != 0 || nativePage.MaxResults < 2 || nativePage.MaxResults > 100 || nativePage.Total != 2 || len(nativePage.Comments) != nativePage.Total || len(comments.Data.Comments) != nativePage.Total {
		t.Fatal("native comment inventory did not completely enumerate both owned comments")
	}
	nativeBodies := make(map[string]string, len(nativePage.Comments))
	for _, item := range nativePage.Comments {
		if strings.TrimSpace(item.ID) == "" {
			t.Fatal("native comment inventory has no resource identity")
		}
		if _, exists := nativeBodies[item.ID]; exists {
			t.Fatal("native comment inventory repeats a resource identity")
		}
		nativeBodies[item.ID] = adfText(t, item.Body)
		if moved.CommentPosted == "atomic" && item.ID != commentID && nativeBodies[item.ID] == r.manifest.RunID+" transition comment" {
			r.registerComment(t, first, item.ID)
		}
	}
	foundComment, foundTransition := false, false
	for _, comment := range comments.Data.Comments {
		text := adfText(t, comment.Body)
		if nativeText, exists := nativeBodies[comment.ID]; !exists || nativeText != text {
			t.Fatal("domain comment identity or body differs from independent native storage")
		}
		foundComment = foundComment || text == r.manifest.RunID+" comment"
		foundTransition = foundTransition || text == r.manifest.RunID+" transition comment"
	}
	if !foundComment || !foundTransition {
		t.Fatal("ordinary or transition comment was not stored")
	}
	status := r.success(t, "issue", "view", first, "--fields", "status")
	var statusFields struct {
		Status struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"status"`
	}
	if err := json.Unmarshal(status.Data.Issue.Fields, &statusFields); err != nil {
		t.Fatal(err)
	}
	if statusFields.Status.Name != "Done" || statusFields.Status.ID != expectedStatusID {
		t.Fatal("transition did not store its destination status")
	}
	nativeStatus := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(first), "--query=fields=status")
	var nativeStored struct {
		Fields struct {
			Status struct{ ID, Name string }
		}
	}
	if json.Unmarshal(nativeStatus.Response.JSON, &nativeStored) != nil || nativeStored.Fields.Status.ID != expectedStatusID || nativeStored.Fields.Status.Name != "Done" || nativeStored.Fields.Status.ID != statusFields.Status.ID {
		t.Fatal("transition destination differs from independent native stored status")
	}
	// Configure only this run's source. Select an actual writable date field
	// rather than assuming a tenant-specific custom field identifier.
	identity := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(second), "--query=fields=issuetype")
	var sourceType struct {
		Fields struct {
			IssueType struct{ ID string } `json:"issuetype"`
		} `json:"fields"`
	}
	if json.Unmarshal(identity.Response.JSON, &sourceType) != nil || sourceType.Fields.IssueType.ID == "" {
		t.Fatal("owned clone source has no issue type for create metadata")
	}
	endpoint := "/rest/api/3/issue/createmeta/" + url.PathEscape(r.manifest.Project) + "/issuetypes/" + url.PathEscape(sourceType.Fields.IssueType.ID)
	copyDateField, err := findCloneDateField(func(start int) json.RawMessage {
		metadata := rawRead(t, r, "api", "get", endpoint, "--query=startAt="+strconv.Itoa(start), "--query=maxResults=50")
		return metadata.Response.JSON
	})
	if err != nil {
		t.Fatal(err)
	}
	inputPath := r.nativeCloneDateInput(t, second, copyDateField)
	r.mutate(t, []string{second}, "issue", "edit", second, "--json-input", inputPath)
	r.mutate(t, []string{second}, "issue", "edit", second, "--assignee", "@me")
	cloneSummary := r.manifest.RunID + " clone"
	r.manifest.Attempts = append(r.manifest.Attempts, cloneSummary)
	r.save(t)
	cloned := r.mutate(t, []string{second}, "issue", "clone", second, "--summary", cloneSummary)
	if len(cloned.Data.Results) != 1 || !cloned.Data.Results[0].OK {
		t.Fatal("clone did not identify its successful source")
	}
	var clone struct {
		Clone   liveIssue       `json:"clone"`
		Applied bool            `json:"applied"`
		Receipt json.RawMessage `json:"receipt"`
	}
	if err := json.Unmarshal(cloned.Data.Results[0].Data, &clone); err != nil {
		t.Fatal(err)
	}
	r.register(t, clone.Clone)
	var receipt liveIssue
	if !clone.Applied || clone.Clone.Key == second || json.Unmarshal(clone.Receipt, &receipt) != nil || receipt.Key != clone.Clone.Key || receipt.ID != clone.Clone.ID {
		t.Fatal("clone lost its native creation acknowledgement or returned the source identity")
	}
	// Native responses independently establish stored body and copy behavior,
	// rather than comparing two CLI views that could drop the same fields.
	fieldsQuery := "--query=fields=summary,description,issuetype,project,priority,labels,components,fixVersions,versions,parent,duedate,assignee," + copyDateField
	source := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(second), fieldsQuery)
	copy := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+url.PathEscape(clone.Clone.Key), fieldsQuery)
	var original, copied struct {
		ID, Key string
		Fields  map[string]json.RawMessage `json:"fields"`
	}
	if json.Unmarshal(source.Response.JSON, &original) != nil || json.Unmarshal(copy.Response.JSON, &copied) != nil || original.Key != second || copied.Key != clone.Clone.Key || copied.ID != clone.Clone.ID || original.ID == copied.ID || original.Fields == nil || copied.Fields == nil {
		t.Fatal("native clone inspection lost an independently observed identity or fields")
	}
	var summary string
	if json.Unmarshal(copied.Fields["summary"], &summary) != nil || summary != cloneSummary || adfText(t, copied.Fields["description"]) != "Acceptance bold <&> \u2028 end" {
		t.Fatal("native clone inspection lost its summary override or body")
	}
	var storedDate string
	var storedAssignee struct {
		AccountID string `json:"accountId"`
	}
	if json.Unmarshal(original.Fields[copyDateField], &storedDate) != nil || storedDate != "2026-01-03" || json.Unmarshal(original.Fields["assignee"], &storedAssignee) != nil || storedAssignee.AccountID == "" {
		t.Fatal("owned source did not independently store the custom date or assignee stimulus")
	}
	for _, field := range append([]string{"description", "issuetype", "project", "priority", "labels", "components", "fixVersions", "versions", "parent", "duedate", "assignee"}, copyDateField) {
		value, exists := original.Fields[field]
		if !exists || string(value) == "null" {
			continue
		}
		var want, got any
		if json.Unmarshal(value, &want) != nil || json.Unmarshal(copied.Fields[field], &got) != nil || !reflect.DeepEqual(want, got) {
			t.Fatalf("native clone field %s differs from its source", field)
		}
	}
	r.loadLinkTypes(t)
	r.mutate(t, []string{second, clone.Clone.Key}, "issue", "link", "create", second,
		"--to", clone.Clone.Key, "--type", "Cloners")
	r.registerPairLink(t, second, clone.Clone.Key, r.linkTypes["Cloners"], false)
	links := r.success(t, "issue", "link", "list", second)
	if len(links.Data.Results) != 1 || !strings.Contains(string(links.Data.Results[0].Data), clone.Clone.Key) {
		t.Fatal("clone follow-up link is missing")
	}
}

func adfText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	type node struct {
		Text    string            `json:"text"`
		Content []json.RawMessage `json:"content"`
	}
	var value node
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	text.WriteString(value.Text)
	for _, child := range value.Content {
		text.WriteString(adfText(t, child))
	}
	return text.String()
}
