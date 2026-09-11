package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The pagination contract: one camelCase shape in one place
// (meta.pagination; results[].data.pagination for keyed multi-key output),
// isLast/nextCursor authoritative, total only when the endpoint reports
// one, a returned nextCursor accepted back via --cursor, and no
// pagination block at all on mutation envelopes.

type paginationEnvelope struct {
	OK   bool `json:"ok"`
	Meta struct {
		Pagination *struct {
			StartAt    int    `json:"startAt"`
			MaxResults int    `json:"maxResults"`
			Total      *int   `json:"total"`
			IsLast     bool   `json:"isLast"`
			NextCursor string `json:"nextCursor"`
		} `json:"pagination"`
	} `json:"meta"`
	Data map[string]any `json:"data"`
}

func decodePaginationEnvelope(t *testing.T, raw []byte) paginationEnvelope {
	t.Helper()
	var env paginationEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, raw)
	}
	return env
}

// enhancedSearchServer fakes POST /rest/api/3/search/jql with two pages
// keyed on the request body's nextPageToken.
func enhancedSearchServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /rest/api/3/search/jql", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			NextPageToken string `json:"nextPageToken"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		switch req.NextPageToken {
		case "":
			_, _ = w.Write([]byte(`{"issues":[{"key":"PROJ-1","fields":{"summary":"one"}}],"isLast":false,"nextPageToken":"PAGE2"}`))
		case "PAGE2":
			_, _ = w.Write([]byte(`{"issues":[{"key":"PROJ-2","fields":{"summary":"two"}}],"isLast":true}`))
		default:
			t.Errorf("unexpected nextPageToken %q", req.NextPageToken)
			http.Error(w, "bad token", http.StatusBadRequest)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// Enhanced search reports no total, so the envelope must not fabricate
// total:0 — isLast and nextCursor are the walk signals.
func TestSearchJQLOmitsFabricatedTotal(t *testing.T) {
	srv := enhancedSearchServer(t)
	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "search", "jql", "project = PROJ", "--output=json")
	if code != 0 {
		t.Fatalf("exit=%d\nstderr=%s\nstdout=%s", code, stderr, stdout)
	}
	env := decodePaginationEnvelope(t, stdout)
	p := env.Meta.Pagination
	if p == nil {
		t.Fatalf("meta.pagination missing: %s", stdout)
	}
	if p.Total != nil {
		t.Fatalf("token-paged endpoint must omit total, got %d: %s", *p.Total, stdout)
	}
	if p.IsLast {
		t.Fatalf("first of two pages must report isLast:false: %s", stdout)
	}
	if p.NextCursor != "PAGE2" {
		t.Fatalf("nextCursor = %q; want the server token PAGE2", p.NextCursor)
	}
}

// A returned nextCursor passed back via --cursor fetches the next page
// deterministically, and the terminal page reports isLast:true.
func TestSearchJQLCursorResumesWalk(t *testing.T) {
	srv := enhancedSearchServer(t)
	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "search", "jql", "project = PROJ", "--cursor", "PAGE2", "--output=json")
	if code != 0 {
		t.Fatalf("exit=%d\nstderr=%s\nstdout=%s", code, stderr, stdout)
	}
	env := decodePaginationEnvelope(t, stdout)
	issues, _ := env.Data["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("resumed page issues = %d; want 1: %s", len(issues), stdout)
	}
	issue, _ := issues[0].(map[string]any)
	if issue["key"] != "PROJ-2" {
		t.Fatalf("resumed page must carry the second page's issue: %s", stdout)
	}
	p := env.Meta.Pagination
	if p == nil || !p.IsLast || p.NextCursor != "" {
		t.Fatalf("terminal page must report isLast:true with no cursor: %s", stdout)
	}
}

// issue list honors the general pagination contract: --limit sets the
// requested page size, --cursor resumes, and --all drains to a known
// total.
func TestIssueListPaginationFlags(t *testing.T) {
	var capturedMaxResults requestCapture[float64]
	mux := http.NewServeMux()
	mux.HandleFunc("POST /rest/api/3/search/jql", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			MaxResults    float64 `json:"maxResults"`
			NextPageToken string  `json:"nextPageToken"`
		}
		_ = json.Unmarshal(body, &req)
		capturedMaxResults.Add(req.MaxResults)
		w.Header().Set("Content-Type", "application/json")
		if req.NextPageToken == "" {
			_, _ = w.Write([]byte(`{"issues":[{"key":"PROJ-1","fields":{"summary":"one"}}],"isLast":false,"nextPageToken":"PAGE2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"issues":[{"key":"PROJ-2","fields":{"summary":"two"}}],"isLast":true}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := jiraConfig(t, srv.URL)

	stdout, stderr, code := runJira(t, "--config", cfg, "issue", "list", "--jql", "project = PROJ", "--limit", "7", "--output=json")
	if code != 0 {
		t.Fatalf("--limit exit=%d\nstderr=%s\nstdout=%s", code, stderr, stdout)
	}
	sawMaxResults := capturedMaxResults.Values()
	if len(sawMaxResults) != 1 || sawMaxResults[0] != 7 {
		t.Fatalf("--limit 7 must reach the wire as maxResults, saw %v", sawMaxResults)
	}
	env := decodePaginationEnvelope(t, stdout)
	if env.Meta.Pagination == nil || env.Meta.Pagination.NextCursor != "PAGE2" {
		t.Fatalf("issue list must surface the resume cursor: %s", stdout)
	}

	stdout, stderr, code = runJira(t, "--config", cfg, "issue", "list", "--jql", "project = PROJ", "--all", "--output=json")
	if code != 0 {
		t.Fatalf("--all exit=%d\nstderr=%s\nstdout=%s", code, stderr, stdout)
	}
	env = decodePaginationEnvelope(t, stdout)
	issues, _ := env.Data["issues"].([]any)
	if len(issues) != 2 {
		t.Fatalf("--all must drain both pages, got %d: %s", len(issues), stdout)
	}
	p := env.Meta.Pagination
	if p == nil || !p.IsLast || p.Total == nil || *p.Total != 2 {
		t.Fatalf("--all must report isLast:true with the drained total: %s", stdout)
	}

	stdout, stderr, code = runJira(t, "--config", cfg, "issue", "list", "--jql", "project = PROJ", "--cursor", "PAGE2", "--output=json")
	if code != 0 {
		t.Fatalf("--cursor exit=%d\nstderr=%s\nstdout=%s", code, stderr, stdout)
	}
	env = decodePaginationEnvelope(t, stdout)
	issues, _ = env.Data["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("--cursor must fetch the second page, got %d issues: %s", len(issues), stdout)
	}
}

// A key listing is a set of exact lookups; pagination flags are refused
// loudly instead of silently ignored.
func TestIssueListKeysRejectPaginationFlags(t *testing.T) {
	srv := enhancedSearchServer(t)
	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "issue", "list", "--key", "PROJ-1..PROJ-5", "--all", "--output=json")
	if code == 0 {
		t.Fatalf("--all with --key must be refused:\nstdout=%s", stdout)
	}
	combined := string(stdout) + string(stderr)
	if !strings.Contains(combined, "pagination") {
		t.Fatalf("refusal must explain the pagination/keys conflict: %s", combined)
	}
}

// Mutation envelopes carry no pagination block at all — a zero-value
// block would read as "empty last page".
func TestMutationEnvelopeOmitsPagination(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rest/api/3/issue/PROJ-1/transitions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transitions":[{"id":"31","name":"Done","hasScreen":true}]}`))
	})
	mux.HandleFunc("POST /rest/api/3/issue/PROJ-1/transitions", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "issue", "transition", "PROJ-1", "Done", "--no-input", "--output=json")
	if code != 0 {
		t.Fatalf("exit=%d\nstderr=%s\nstdout=%s", code, stderr, stdout)
	}
	env := decodePaginationEnvelope(t, stdout)
	if env.Meta.Pagination != nil {
		t.Fatalf("mutation envelope must omit meta.pagination: %s", stdout)
	}
}

// worklog list decodes the endpoint's own startAt/maxResults/total, so
// the envelope reports real numbers instead of zeros.
func TestWorklogListPaginationFromWire(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rest/api/3/issue/PROJ-1/worklog", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"startAt":0,"maxResults":20,"total":1,"worklogs":[{"id":"9000","timeSpent":"1h","timeSpentSeconds":3600,"started":"2026-06-01T10:00:00.000+0000","author":{"accountId":"u","displayName":"A"}}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "worklog", "list", "PROJ-1", "--output=json")
	if code != 0 {
		t.Fatalf("exit=%d\nstderr=%s\nstdout=%s", code, stderr, stdout)
	}
	env := decodePaginationEnvelope(t, stdout)
	p := env.Meta.Pagination
	if p == nil {
		t.Fatalf("meta.pagination missing: %s", stdout)
	}
	if p.Total == nil || *p.Total != 1 || !p.IsLast || p.MaxResults != 20 {
		t.Fatalf("worklog pagination must mirror the wire (total 1, isLast, maxResults 20): %s", stdout)
	}
}
