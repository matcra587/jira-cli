//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// Native pagination and comment fields are decoded independently of the domain
// mapper. Each comparison performs read-only raw and domain requests.
func compareOwnedCommentPage(t *testing.T, r *liveRun, key string, start, limit int, order string, want []string) {
	t.Helper()
	if r.ownedLiveIssue(key) == nil {
		t.Fatal("comment page comparison requires an active owned issue")
	}
	nativeOrder := "+created"
	if order != "oldest" {
		nativeOrder = "-created"
	}
	native := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key+"/comment",
		"--query=startAt="+strconv.Itoa(start), "--query=maxResults="+strconv.Itoa(limit), "--query=orderBy="+nativeOrder)
	var page struct {
		StartAt    *int `json:"startAt"`
		MaxResults *int `json:"maxResults"`
		Total      *int `json:"total"`
		Comments   []struct {
			ID      string          `json:"id"`
			Body    json.RawMessage `json:"body"`
			Created string          `json:"created"`
			Updated string          `json:"updated"`
			Author  struct {
				AccountID   string `json:"accountId"`
				DisplayName string `json:"displayName"`
			} `json:"author"`
		} `json:"comments"`
	}
	if json.Unmarshal(native.Response.JSON, &page) != nil || page.StartAt == nil || page.MaxResults == nil || page.Total == nil || page.Comments == nil || len(page.Comments) != len(want) {
		t.Fatal("native comment page lost its explicit pagination or authored window")
	}
	args := []string{"issue", "comment", "list", key, "--start=" + strconv.Itoa(start), "--limit=" + strconv.Itoa(limit)}
	if order != "" {
		args = append(args, "--order="+order)
	}
	frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, args...)
	var listed struct {
		Data envelope.IssueCommentListOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &listed) != nil || listed.Data.Issue.Key != key || listed.Data.Truncated || listed.Data.Comments == nil || len(listed.Data.Comments) != len(want) || listed.Data.Pagination.StartAt != *page.StartAt || listed.Data.Pagination.MaxResults != *page.MaxResults || listed.Data.Pagination.Total != *page.Total || listed.Data.Pagination.Returned != len(want) {
		t.Fatal("domain comment page differs from independent native pagination")
	}
	for i, native := range page.Comments {
		comment := listed.Data.Comments[i]
		if native.ID != want[i] || comment.ID != native.ID || !bytes.Equal(comment.Body, native.Body) || comment.Created != native.Created || comment.Updated != native.Updated || comment.Author.AccountID != native.Author.AccountID || comment.Author.DisplayName != native.Author.DisplayName {
			t.Fatal("domain comment page changed native order, identity, body, timestamps or authorship")
		}
	}
}

func requireOwnedCommentWalk(t *testing.T, r *liveRun, key, order string, ids []string, bodies map[string][]byte) {
	t.Helper()
	frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "comment", "list", key, "--all", "--order="+order)
	var listed struct {
		Data envelope.IssueCommentListOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &listed) != nil || listed.Data.Issue.Key != key || listed.Data.Truncated || listed.Data.Comments == nil || len(listed.Data.Comments) != len(ids) || listed.Data.Pagination.StartAt != 0 || listed.Data.Pagination.Returned != len(ids) || listed.Data.Pagination.Total != len(ids) {
		t.Fatal("complete comment walk lost owned identities or honest counts")
	}
	for i, comment := range listed.Data.Comments {
		equal, err := adf.StructuralEqual(comment.Body, bodies[comment.ID])
		if comment.ID != ids[i] || err != nil || !equal {
			t.Fatal("complete comment walk changed the authored order or native body")
		}
	}
}

func deleteOwnedReadComment(t *testing.T, r *liveRun, key, id string) {
	t.Helper()
	args := []string{"issue", "comment", "delete", key, id, "--force"}
	if err := r.checkMutation([]string{key}, args); err != nil {
		t.Fatal(err)
	}
	frame, stdout, exit := r.invoke(t, t.Context(), nil, args...)
	var deleted struct {
		Data envelope.IssueCommentDeleteOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &deleted) != nil || deleted.Data.Issue.Key != key || deleted.Data.CommentID != id || deleted.Data.DryRun {
		t.Fatal("comment deletion lost its exact acknowledged target")
	}
	missing, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "api", "get", "/rest/api/3/issue/"+key+"/comment/"+id)
	if exit != 2 || missing.OK || len(missing.Errors) != 1 || missing.Errors[0].Code != "jira_not_found" {
		t.Fatal("comment deletion was not independently confirmed absent")
	}
	r.ownedLiveComment(r.ownedLiveIssue(key), id).Deleted = true
	r.save(t)
}

func TestLiveCommentReads(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "comment-reads")
	compareOwnedCommentPage(t, r, key, 0, 1, "", nil)
	requireOwnedCommentWalk(t, r, key, "oldest", nil, nil)
	var ids []string
	bodies := make(map[string][]byte)
	for i := range 3 {
		body := []byte(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"ordered native comment ` + strconv.Itoa(i) + ` <&>"}]}]}`)
		created := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--json-input", r.nativeBody(t, body))
		id := postedADFCommentID(t, created)
		r.registerComment(t, key, id)
		ids = append(ids, id)
		bodies[id] = body
		stored := rawOwnedComment(t, r, key, id)
		equal, err := adf.StructuralEqual(stored.Body, body)
		if err != nil || !equal || stored.Visibility != nil {
			t.Fatal("new owned comment changed its complete authored body or visibility")
		}
		requireOwnedCommentWalk(t, r, key, "oldest", ids, bodies)
	}
	newest := slices.Clone(ids)
	slices.Reverse(newest)
	for i := range ids {
		compareOwnedCommentPage(t, r, key, i, 1, "oldest", ids[i:i+1])
		compareOwnedCommentPage(t, r, key, i, 1, "", newest[i:i+1])
	}
	compareOwnedCommentPage(t, r, key, 0, 2, "newest", newest[:2])
	compareOwnedCommentPage(t, r, key, 5, 1, "oldest", nil)
	requireOwnedCommentWalk(t, r, key, "oldest", ids, bodies)
	requireOwnedCommentWalk(t, r, key, "newest", newest, bodies)
	deleteOwnedReadComment(t, r, key, ids[1])
	remaining := []string{ids[0], ids[2]}
	compareOwnedCommentPage(t, r, key, 0, 2, "oldest", remaining)
	requireOwnedCommentWalk(t, r, key, "oldest", remaining, bodies)
	for _, id := range remaining {
		deleteOwnedReadComment(t, r, key, id)
	}
	compareOwnedCommentPage(t, r, key, 0, 1, "", nil)
	requireOwnedCommentWalk(t, r, key, "oldest", nil, bodies)
}
