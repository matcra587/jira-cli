//go:build flagmatrix

package matrix

// Phase-2 cases for `jira issue view`'s two owned flags. The harness
// (cw* helpers) lives in comment_test.go.

import (
	"net/http"
	"testing"
)

func TestViewFieldsNarrowsRequest(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "view", "KEY-123",
		"--fields", "summary,status", "--output=json")
	cwRequireOK(t, res)
	var gets []cwReq
	for _, r := range stub.requests() {
		if r.Method == http.MethodGet && r.Path == "/rest/api/3/issue/KEY-123" {
			gets = append(gets, r)
		}
	}
	if len(gets) != 1 || gets[0].Query.Get("fields") != "summary,status" {
		t.Fatalf("GET /issue/KEY-123 does not carry ?fields=summary,status: %d gets, query %v", len(gets), putsQuery(gets))
	}
	Prove(t, "jira issue view", "fields",
		"stub saw GET /issue/KEY-123 carry ?fields=summary,status; without the flag no fields parameter is sent")
}

// TestViewCommentsDrivesPreviewFetch proves --comments by the request it
// drives. The flag feeds the HUMAN view's inline preview only (machine
// modes skip the fetch by design), so the case runs --output=human and
// asserts the dedicated comment fetch carried the flag's N: the stub's
// issue response embeds no comment field, which forces the fallback
// GetComments(key, 0, N) fetch.
func TestViewCommentsDrivesPreviewFetch(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{
		comments: []string{cwComment("1", "one"), cwComment("2", "two")},
	})
	res := cwRun(t, stub, t.TempDir(), "issue", "view", "KEY-123",
		"--comments", "3", "--output=human")
	if res.Exit != 0 {
		t.Fatalf("human view failed: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	gets := stub.matching(http.MethodGet, "/issue/KEY-123/comment")
	if len(gets) != 1 || gets[0].Query.Get("maxResults") != "3" {
		t.Fatalf("comment preview fetch does not carry maxResults=3: %d gets, query %v", len(gets), putsQuery(gets))
	}
	Prove(t, "jira issue view", "comments",
		"in human mode --comments 3 drove a dedicated GET .../comment with ?maxResults=3 (the issue response embedded no comment field)")
}
