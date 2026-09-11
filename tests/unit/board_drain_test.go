package unit

// BoardService.ListAll drains paginated boards with default (100
// pages / 10 000 boards) bounds and an Unbounded escape. Returned
// BoardDrainResult exposes Truncated/TruncatedReason so the cache-
// prime path can write `truncated: true, truncated_reason: "max_pages"`
// when the bound fired.
//
// The unit test exercises a fake httptest server; full contract tests
// for the cache prime command live in tests/contract/cache_boards_test.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

// pagedBoardServer fakes /rest/agile/1.0/board returning `total` boards,
// `pageSize` per page. Each board's /board/{id}/project responds with one
// project key derived from the board id.
func pagedBoardServer(total, pageSize int) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/agile/1.0/board", func(w http.ResponseWriter, r *http.Request) {
		startAt, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		end := min(startAt+pageSize, total)
		values := []map[string]any{}
		for i := startAt; i < end; i++ {
			values = append(values, map[string]any{
				"id":   i + 1,
				"self": fmt.Sprintf("http://x/board/%d", i+1),
				"name": fmt.Sprintf("Board %d", i+1),
				"type": "scrum",
			})
		}
		body := map[string]any{
			"maxResults": pageSize,
			"startAt":    startAt,
			"isLast":     end >= total,
			"values":     values,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	return httptest.NewServer(mux)
}

func TestBoardServiceListAllUnderBounds(t *testing.T) {
	srv := pagedBoardServer(120, 50)
	defer srv.Close()
	client := jira.NewClient(jira.WithBaseURL(srv.URL + "/"))
	svc := jira.NewBoardService(client)

	res, err := svc.ListAll(context.Background(), jira.BoardDrainOptions{PageSize: 50})
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(res.Boards) != 120 {
		t.Fatalf("Boards len = %d; want 120", len(res.Boards))
	}
	if res.Truncated {
		t.Fatalf("Truncated = true; want false (instance under bounds)")
	}
	if res.PagesFetched != 3 {
		t.Fatalf("PagesFetched = %d; want 3 (50+50+20)", res.PagesFetched)
	}
}

func TestBoardServiceListAllMaxPagesBoundFires(t *testing.T) {
	// 10 boards/page × 200 pages = 2 000 total; default MaxPages=100 caps
	// at 1 000 boards before isLast.
	srv := pagedBoardServer(2000, 10)
	defer srv.Close()
	client := jira.NewClient(jira.WithBaseURL(srv.URL + "/"))
	svc := jira.NewBoardService(client)

	res, err := svc.ListAll(context.Background(), jira.BoardDrainOptions{PageSize: 10})
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if !res.Truncated {
		t.Fatalf("Truncated = false; want true")
	}
	if res.TruncatedReason != "max_pages" {
		t.Fatalf("TruncatedReason = %q; want max_pages", res.TruncatedReason)
	}
	if len(res.Boards) != 1000 {
		t.Fatalf("Boards len = %d; want 1000 (100 pages × 10 per page)", len(res.Boards))
	}
}

func TestBoardServiceListAllMaxResultsBoundFires(t *testing.T) {
	// 50/page × 250 pages with MaxResults=200 caps before MaxPages.
	srv := pagedBoardServer(12500, 50)
	defer srv.Close()
	client := jira.NewClient(jira.WithBaseURL(srv.URL + "/"))
	svc := jira.NewBoardService(client)

	res, err := svc.ListAll(context.Background(), jira.BoardDrainOptions{PageSize: 50, MaxResults: 200})
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if !res.Truncated {
		t.Fatalf("Truncated = false; want true")
	}
	if res.TruncatedReason != "max_results" {
		t.Fatalf("TruncatedReason = %q; want max_results", res.TruncatedReason)
	}
	if len(res.Boards) > 200 {
		t.Fatalf("Boards len = %d; should be capped at 200", len(res.Boards))
	}
}

func TestBoardServiceListAllExactMaxResultsOnLastPageNotTruncated(t *testing.T) {
	srv := pagedBoardServer(100, 50)
	defer srv.Close()
	client := jira.NewClient(jira.WithBaseURL(srv.URL + "/"))
	svc := jira.NewBoardService(client)

	res, err := svc.ListAll(context.Background(), jira.BoardDrainOptions{PageSize: 50, MaxResults: 100})
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if res.Truncated {
		t.Fatalf("Truncated = true with exact final-page max-results; reason=%q", res.TruncatedReason)
	}
	if len(res.Boards) != 100 {
		t.Fatalf("Boards len = %d; want 100", len(res.Boards))
	}
}

func TestBoardServiceListAllUnboundedEscape(t *testing.T) {
	// Walks past default 100-page / 10K-result bounds when Unbounded=true.
	srv := pagedBoardServer(10500, 50)
	defer srv.Close()
	client := jira.NewClient(jira.WithBaseURL(srv.URL + "/"))
	svc := jira.NewBoardService(client)

	res, err := svc.ListAll(context.Background(), jira.BoardDrainOptions{PageSize: 50, Unbounded: true})
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if res.Truncated {
		t.Fatalf("Truncated = true under Unbounded; want false")
	}
	if len(res.Boards) != 10500 {
		t.Fatalf("Boards len = %d; want 10500 (Unbounded should walk full set)", len(res.Boards))
	}
}

func TestBoardServiceProjectsForBoardDrainsAllPages(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/agile/1.0/board/42/project", func(w http.ResponseWriter, r *http.Request) {
		startAt, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		switch startAt {
		case 0:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"maxResults": 1,
				"startAt":    0,
				"isLast":     false,
				"values":     []map[string]any{{"key": "PLAT"}},
			})
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"maxResults": 1,
				"startAt":    1,
				"isLast":     true,
				"values":     []map[string]any{{"key": "ENG"}},
			})
		default:
			t.Fatalf("unexpected startAt %d", startAt)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := jira.NewClient(jira.WithBaseURL(srv.URL + "/"))
	svc := jira.NewBoardService(client)
	keys, _, err := svc.ProjectsForBoard(context.Background(), 42)
	if err != nil {
		t.Fatalf("ProjectsForBoard: %v", err)
	}
	if got, want := fmt.Sprint(keys), "[ENG PLAT]"; got != want {
		t.Fatalf("ProjectsForBoard keys = %s, want %s", got, want)
	}
}

func TestBoardServiceProjectsForBoardContinuesPastEmptyNonFinalPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/agile/1.0/board/42/project", func(w http.ResponseWriter, r *http.Request) {
		startAt, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
		switch startAt {
		case 0:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"maxResults": 1,
				"startAt":    0,
				"isLast":     false,
				"values":     []map[string]any{{"key": "PLAT"}},
			})
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"maxResults": 1,
				"startAt":    1,
				"isLast":     false,
				"values":     []map[string]any{},
			})
		case 2:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"maxResults": 1,
				"startAt":    2,
				"isLast":     true,
				"values":     []map[string]any{{"key": "ENG"}},
			})
		default:
			t.Fatalf("unexpected startAt %d", startAt)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := jira.NewClient(jira.WithBaseURL(srv.URL + "/"))
	svc := jira.NewBoardService(client)
	keys, _, err := svc.ProjectsForBoard(context.Background(), 42)
	if err != nil {
		t.Fatalf("ProjectsForBoard: %v", err)
	}
	if got, want := fmt.Sprint(keys), "[ENG PLAT]"; got != want {
		t.Fatalf("ProjectsForBoard keys = %s, want %s", got, want)
	}
}
