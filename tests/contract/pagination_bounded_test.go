package contract

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

// --all consumption MUST be bounded by default to 100 pages and
// 10,000 results-total. Override flags lift the bounds; only
// --unbounded removes them entirely.
//
// We exercise the iterator/page-stream wrapper directly. The CLI flag
// surface maps onto this helper so command code never manages
// raw cursors.
func TestSearchAllBoundedByDefaults(t *testing.T) {
	var pageCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := pageCount.Add(1)
		// Always return 10 issues per page and pretend more are available.
		body := `{"issues":[`
		for i := range 10 {
			if i > 0 {
				body += ","
			}
			body += `{"key":"JCT-1"}`
		}
		body += `],"isLast":false,"nextPageToken":"page-` + itoaPos(page) + `"}`
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	client := jira.NewClient(jira.WithBaseURL(srv.URL))
	svc := jira.NewSearchService(client)

	collected, info, err := jira.DrainSearch(context.Background(), svc, &jira.SearchRequest{JQL: "project = X"}, jira.DrainOptions{})
	if err != nil {
		t.Fatalf("DrainSearch: %v", err)
	}

	// Default limits: 100 pages OR 10,000 issues — whichever hits first.
	// With 10 issues per page that's 1,000 pages; the 10,000-issue cap
	// hits at page 1,000... wait, 100 pages is the lower cap. So 100
	// pages × 10 issues = 1,000 issues retrieved before truncating.
	if len(collected) > 10_000 {
		t.Fatalf("collected %d > 10000 default cap", len(collected))
	}
	if !info.Truncated {
		t.Fatal("default unbounded-server should truncate; meta.pagination.truncated=true expected")
	}
	if !strings.HasPrefix(info.TruncatedReason, "max_") {
		t.Fatalf("truncated_reason should describe which bound fired (max_pages|max_results); got %q", info.TruncatedReason)
	}
}

func TestSearchAllRespectsExplicitMaxPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"issues":[{"key":"JCT-1"},{"key":"JCT-2"}],"isLast":false,"nextPageToken":"abc"}`))
	}))
	defer srv.Close()

	client := jira.NewClient(jira.WithBaseURL(srv.URL))
	svc := jira.NewSearchService(client)

	collected, info, err := jira.DrainSearch(context.Background(), svc, &jira.SearchRequest{JQL: "x"}, jira.DrainOptions{MaxPages: 3})
	if err != nil {
		t.Fatalf("DrainSearch: %v", err)
	}
	if len(collected) != 6 { // 3 pages × 2 issues
		t.Fatalf("expected 6 issues (3 pages × 2), got %d", len(collected))
	}
	if !info.Truncated || info.TruncatedReason != "max_pages" {
		t.Fatalf("expected truncated by max_pages, got truncated=%v reason=%q", info.Truncated, info.TruncatedReason)
	}
}

func TestDrainSearchExactMaxResultsOnLastPageNotTruncated(t *testing.T) {
	var pageCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := pageCount.Add(1)
		switch page {
		case 1:
			_, _ = w.Write([]byte(`{"issues":[{"key":"JCT-1"}],"isLast":false,"nextPageToken":"next"}`))
		case 2:
			_, _ = w.Write([]byte(`{"issues":[{"key":"JCT-2"}],"isLast":true}`))
		default:
			t.Fatalf("unexpected page %d", page)
		}
	}))
	defer srv.Close()

	client := jira.NewClient(jira.WithBaseURL(srv.URL))
	svc := jira.NewSearchService(client)

	collected, info, err := jira.DrainSearch(context.Background(), svc, &jira.SearchRequest{JQL: "project = X"}, jira.DrainOptions{MaxResults: 2})
	if err != nil {
		t.Fatalf("DrainSearch: %v", err)
	}
	if len(collected) != 2 {
		t.Fatalf("collected len = %d, want 2", len(collected))
	}
	if info.Truncated {
		t.Fatalf("Truncated = true with exact final-page max-results; reason=%q", info.TruncatedReason)
	}
}

func TestDrainSearchExactMaxResultsWithMorePagesIsTruncated(t *testing.T) {
	var pageCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := pageCount.Add(1)
		_, _ = w.Write([]byte(`{"issues":[{"key":"JCT-` + itoaPos(page) + `"}],"isLast":false,"nextPageToken":"next"}`))
	}))
	defer srv.Close()

	client := jira.NewClient(jira.WithBaseURL(srv.URL))
	svc := jira.NewSearchService(client)

	collected, info, err := jira.DrainSearch(context.Background(), svc, &jira.SearchRequest{JQL: "project = X"}, jira.DrainOptions{MaxResults: 2})
	if err != nil {
		t.Fatalf("DrainSearch: %v", err)
	}
	if len(collected) != 2 {
		t.Fatalf("collected len = %d, want 2", len(collected))
	}
	if !info.Truncated || info.TruncatedReason != "max_results" {
		t.Fatalf("truncation = (%v, %q), want max_results", info.Truncated, info.TruncatedReason)
	}
}

// --unbounded MUST remove all bounds. The server side here
// terminates after a few pages so the test doesn't hang.
func TestSearchAllUnboundedClearsAllBounds(t *testing.T) {
	var pages atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := pages.Add(1)
		isLast := page >= 5
		body := `{"issues":[{"key":"X"}],"isLast":` + boolJSON(isLast) + `,"nextPageToken":"p"}`
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	client := jira.NewClient(jira.WithBaseURL(srv.URL))
	svc := jira.NewSearchService(client)

	collected, info, err := jira.DrainSearch(context.Background(), svc, &jira.SearchRequest{JQL: "x"}, jira.DrainOptions{Unbounded: true})
	if err != nil {
		t.Fatalf("DrainSearch unbounded: %v", err)
	}
	if len(collected) != 5 {
		t.Fatalf("expected 5 issues (server stopped after 5 pages), got %d", len(collected))
	}
	if info.Truncated {
		t.Fatalf("--unbounded should not report truncation when server returned isLast")
	}
}

func itoaPos(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
