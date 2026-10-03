package envelope_test

import (
	"encoding/json"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// TestIssueCommentListOutputJSONShape pins the wire shape the design
// binds: snake_case keys throughout the CLI-minted fields, the identity
// slot at "issue", the comment's body carrying its ADF verbatim, and the
// pagination block riding inside data (never meta) so --output=compact
// and --jq can both see it.
func TestIssueCommentListOutputJSONShape(t *testing.T) {
	out := envelope.IssueCommentListOutput{
		Identity: envelope.Identity{Issue: envelope.IssueRef{Key: "PROJ-1"}},
		Comments: []envelope.CommentView{
			{
				ID:      "10050",
				Author:  envelope.AuthorRef{AccountID: "acc-1", DisplayName: "Ada Lovelace"},
				Created: "2024-03-01T10:00:00Z",
				Updated: "2024-03-01T10:00:00Z",
				Body:    json.RawMessage(`{"type":"doc","version":1,"content":[]}`),
			},
		},
		Pagination: envelope.OffsetPagination{StartAt: 0, MaxResults: 50, Total: 3, Returned: 1},
	}

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	issue, ok := doc["issue"].(map[string]any)
	if !ok || issue["key"] != "PROJ-1" {
		t.Errorf("issue = %#v, want the identity object carrying key PROJ-1", doc["issue"])
	}

	comments, ok := doc["comments"].([]any)
	if !ok || len(comments) != 1 {
		t.Fatalf("comments = %#v, want a one-element array", doc["comments"])
	}
	c, ok := comments[0].(map[string]any)
	if !ok {
		t.Fatalf("comments[0] = %#v, want an object", comments[0])
	}
	for _, key := range []string{"id", "author", "created", "updated", "body"} {
		if _, present := c[key]; !present {
			t.Errorf("comments[0].%s missing", key)
		}
	}
	author, ok := c["author"].(map[string]any)
	if !ok {
		t.Fatalf("comments[0].author = %#v, want an object", c["author"])
	}
	if author["account_id"] != "acc-1" || author["display_name"] != "Ada Lovelace" {
		t.Errorf("author = %#v, want snake_case account_id/display_name", author)
	}
	body, ok := c["body"].(map[string]any)
	if !ok || body["type"] != "doc" {
		t.Errorf("body = %#v, want the raw ADF document verbatim", c["body"])
	}

	pagination, ok := doc["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("pagination = %#v, want an object", doc["pagination"])
	}
	for _, key := range []string{"start_at", "max_results", "total", "returned"} {
		if _, present := pagination[key]; !present {
			t.Errorf("pagination.%s missing", key)
		}
	}
	if pagination["total"] != float64(3) || pagination["returned"] != float64(1) {
		t.Errorf("pagination = %#v, want total=3 returned=1", pagination)
	}
}

// TestOffsetPaginationJSONKeysAreSnakeCase pins the shared type's wire
// shape directly, independent of any one operation.
func TestOffsetPaginationJSONKeysAreSnakeCase(t *testing.T) {
	p := envelope.OffsetPagination{StartAt: 10, MaxResults: 25, Total: 100, Returned: 25}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"start_at":10,"max_results":25,"total":100,"returned":25}`
	if string(b) != want {
		t.Errorf("marshal = %s, want %s", b, want)
	}
}
