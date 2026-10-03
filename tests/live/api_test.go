//go:build live

package live

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// TestLiveRawAPIReads verifies platform/Agile reads and the shared search POST
// classification. Read-only remains on; this suite owns no mutable fixtures.
func TestLiveRawAPIReads(t *testing.T) {
	r := newLiveRun(t)
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	if !regexp.MustCompile("^" + regexp.QuoteMeta(r.manifest.Project) + "-[0-9]+$").MatchString(key) {
		t.Fatal("raw read fixture must be an explicit issue key in the dedicated project")
	}
	boardID, err := strconv.ParseInt(requiredEnv(t, "JIRA_LIVE_BOARD_ID"), 10, 64)
	if err != nil || boardID <= 0 {
		t.Fatal("raw board read needs a positive board ID")
	}
	issue := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=summary")
	var stored struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(issue.Response.JSON, &stored); err != nil || stored.Key != key {
		t.Fatal("raw platform read lost the designated fixture identity")
	}
	board := rawRead(t, r, "api", "GET", "/rest/agile/1.0/board/"+strconv.FormatInt(boardID, 10))
	var selected struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(board.Response.JSON, &selected); err != nil || selected.ID != boardID {
		t.Fatal("raw Agile read did not address the configured board")
	}
	body, err := wire.MarshalNoEscape(struct {
		JQL        string   `json:"jql"`
		Fields     []string `json:"fields"`
		MaxResults int      `json:"maxResults"`
	}{"key = " + key, []string{"summary"}, 1})
	if err != nil {
		t.Fatal("cannot compose the explicit read-only search")
	}
	path := filepath.Join(r.dir, "raw-search.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal("cannot save the local raw search input")
	}
	search := rawRead(t, r, "api", "post", "/rest/api/3/search/jql", "--input", path)
	if search.Request.BodyBase64 == nil || *search.Request.BodyBase64 != base64.StdEncoding.EncodeToString(body) {
		t.Fatal("live raw search changed its native input bytes")
	}
	var page struct {
		IsLast *bool `json:"isLast"`
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Summary *string `json:"summary"`
			} `json:"fields"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(search.Response.JSON, &page); err != nil || page.IsLast == nil || !*page.IsLast || len(page.Issues) != 1 || page.Issues[0].Key != key || page.Issues[0].Fields.Summary == nil {
		t.Fatal("raw search POST lost the requested read fixture")
	}
	frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "list", "--jql", "key = "+key, "--fields=summary", "--limit=1")
	var domain struct {
		Data envelope.IssueListOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &domain) != nil || domain.Data.Truncated || !domain.Data.Pagination.IsLast || domain.Data.Pagination.Returned != 1 || len(domain.Data.Issues) != 1 || domain.Data.Issues[0].Key != page.Issues[0].Key || !domain.Data.Issues[0].Fields.Summary.Set || domain.Data.Issues[0].Fields.Summary.Null || domain.Data.Issues[0].Fields.Summary.Value != *page.Issues[0].Fields.Summary {
		t.Fatal("typed search differs from independently decoded raw fixture search")
	}
}

func rawRead(t *testing.T, r *liveRun, args ...string) envelope.APIOutput {
	t.Helper()
	frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, args...)
	if exit != 0 || !frame.OK {
		t.Fatalf("raw read step %d failed with exit %d; inspect local receipts", r.sequence, exit)
	}
	var decoded struct {
		Data envelope.APIOutput `json:"data"`
	}
	if err := json.Unmarshal(stdout, &decoded); err != nil || !decoded.Data.Attempted || decoded.Data.Response == nil || decoded.Data.Response.StatusCode != 200 || decoded.Data.Response.Truncated || len(decoded.Data.Response.JSON) == 0 {
		t.Fatal("live raw read has no complete JSON HTTP receipt")
	}
	return decoded.Data
}
