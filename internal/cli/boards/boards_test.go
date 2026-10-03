package boards

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters"
	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

type boardFake struct {
	core.JiraClient
	boards   func(context.Context, int, int) (core.BoardRead, error)
	projects func(context.Context, int64, int, int) (core.BoardProjectRead, error)
}

func (f boardFake) GetBoards(ctx context.Context, name string, start, size int) (core.BoardRead, error) {
	if name != "" {
		return core.BoardRead{}, errors.New("unexpected name filter")
	}
	return f.boards(ctx, start, size)
}

func (f boardFake) GetBoardProjects(ctx context.Context, id int64, start, size int) (core.BoardProjectRead, error) {
	return f.projects(ctx, id, start, size)
}

func board(id int64, name string) jira.Board {
	return jira.Board{ID: jira.NullableOf(id), Name: jira.NullableOf(name), Type: jira.NullableOf("scrum")}
}

func boardPage(start int, final bool, rows ...jira.Board) core.BoardRead {
	if rows == nil {
		rows = []jira.Board{}
	}
	return core.BoardRead{Page: &jira.BoardPage{StartAt: jira.NullableOf(start), MaxResults: jira.NullableOf(pageSize), IsLast: jira.NullableOf(final), Values: jira.NullableOf(rows)}}
}

func projectPage(start int, final bool, keys ...string) core.BoardProjectRead {
	rows := make([]jira.BoardProject, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, jira.BoardProject{ID: jira.NullableOf(key), Key: jira.NullableOf(key)})
	}
	return core.BoardProjectRead{Page: &jira.BoardProjectPage{StartAt: jira.NullableOf(start), MaxResults: jira.NullableOf(pageSize), IsLast: jira.NullableOf(final), Values: jira.NullableOf(rows)}}
}

func depsFor(client core.JiraClient, store core.Cache) Deps {
	return Deps{JiraClient: func() (core.JiraClient, error) { return client, nil }, Cache: func() (core.Cache, error) { return store, nil }}
}

func TestBoardListDrainsBeforeWindowAndEnrichesProjects(t *testing.T) {
	t.Parallel()
	var offsets []int
	var projectOffsets []int
	fake := boardFake{boards: func(ctx context.Context, start, size int) (core.BoardRead, error) {
		if ctx != t.Context() || size != 50 {
			t.Error("board context or page size changed")
			return core.BoardRead{}, errors.New("wrong board request")
		}
		offsets = append(offsets, start)
		switch start {
		case 0:
			return boardPage(0, false, board(99, " Later ")), nil
		case 1:
			return boardPage(1, true, board(42, "Earlier")), nil
		default:
			return core.BoardRead{}, errors.New("wrong board continuation")
		}
	}, projects: func(ctx context.Context, id int64, start, size int) (core.BoardProjectRead, error) {
		if ctx != t.Context() || size != 50 {
			t.Error("project context or page size changed")
			return core.BoardProjectRead{}, errors.New("wrong project request")
		}
		if id == 99 {
			return projectPage(0, true, "OPS"), nil
		}
		projectOffsets = append(projectOffsets, start)
		if start == 0 {
			return projectPage(0, false, "KEY10"), nil
		}
		if start == 1 {
			return projectPage(1, true, "KEY2"), nil
		}
		return core.BoardProjectRead{}, errors.New("wrong project continuation")
	}}
	var store adapters.MemoryCache
	out, err := runList(t.Context(), depsFor(fake, &store), options{limit: 1})
	if err != nil || out.Count != 1 || out.Pagination.Total != 2 || out.Pagination.IsLast || !out.Pagination.Complete || out.Truncated || out.FromCache || !out.CacheSaved || out.Boards[0].ID != 42 || !reflect.DeepEqual(out.Boards[0].ProjectKeys, []string{"KEY2", "KEY10"}) || !out.Boards[0].ProjectsComplete || !reflect.DeepEqual(offsets, []int{0, 1}) || !reflect.DeepEqual(projectOffsets, []int{0, 1}) {
		t.Fatalf("board drain/enrichment/window changed: %+v %v offsets=%v projects=%v", out, err, offsets, projectOffsets)
	}
	cached, err := runList(t.Context(), Deps{Cache: func() (core.Cache, error) { return &store, nil }, JiraClient: func() (core.JiraClient, error) {
		t.Error("fresh cache constructed a client")
		return nil, errors.New("unexpected client")
	}}, options{all: true})
	if err != nil || !cached.FromCache || cached.Count != 2 || cached.FetchedAt != out.FetchedAt || cached.Boards[1].Name != "Later" || cached.CacheSourceState != "fresh" {
		t.Fatalf("cache did not retain full enriched snapshot: %+v %v", cached, err)
	}
}

func TestBoardListEmptyIsCachedAndCacheFailureDegrades(t *testing.T) {
	t.Parallel()
	calls := 0
	fake := boardFake{boards: func(context.Context, int, int) (core.BoardRead, error) { calls++; return boardPage(0, true), nil }, projects: func(context.Context, int64, int, int) (core.BoardProjectRead, error) {
		t.Error("empty boards enriched a project")
		return core.BoardProjectRead{}, errors.New("unexpected project")
	}}
	var store adapters.MemoryCache
	for i := 0; i < 2; i++ {
		out, err := runList(t.Context(), depsFor(fake, &store), options{})
		if err != nil || out.Boards == nil || out.ProjectFailures == nil || !out.CacheEmpty || out.CacheState != "empty" || !out.Pagination.Complete || out.FromCache != (i == 1) {
			t.Fatalf("empty snapshot lost provenance: %+v %v", out, err)
		}
	}
	if calls != 1 {
		t.Fatalf("empty cache was not reusable: %d", calls)
	}
	out, err := runList(t.Context(), Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }, Cache: func() (core.Cache, error) { return nil, errors.New("full disk") }}, options{})
	if err != nil || out.CacheSaved || out.CacheSourceState != "disabled" || out.Boards == nil {
		t.Fatalf("cache failure disabled a read or claimed storage: %+v %v", out, err)
	}
}

func TestBoardListRefreshFailureInvalidatesCacheAndRetainsNoSuccess(t *testing.T) {
	t.Parallel()
	var store adapters.MemoryCache
	fake := boardFake{boards: func(context.Context, int, int) (core.BoardRead, error) { return boardPage(0, true), nil }}
	if _, err := runList(t.Context(), depsFor(fake, &store), options{}); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("offline")
	fake.boards = func(context.Context, int, int) (core.BoardRead, error) { return core.BoardRead{}, cause }
	_, err := runList(t.Context(), depsFor(fake, &store), options{cache: metacache.Options{Refresh: true}})
	if !errors.Is(err, cause) {
		t.Fatalf("refresh fabricated success: %v", err)
	}
	if _, _, ok := store.Get(cacheKey); ok {
		t.Fatal("failed refresh retained the old entry")
	}
}

func TestBoardWalkBoundsAndUnbounded(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		pages, results int
		unbounded      bool
		want           int
		reason         string
	}{{"page", 1, 9, false, 2, "max_pages"}, {"rows", 9, 1, false, 1, "max_results"}, {"unbounded", 1, 1, true, 3, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := boardFake{boards: func(_ context.Context, start, _ int) (core.BoardRead, error) {
				if start == 0 {
					return boardPage(0, false, board(1, "One"), board(2, "Two")), nil
				}
				return boardPage(2, true, board(3, "Three")), nil
			}, projects: func(context.Context, int64, int, int) (core.BoardProjectRead, error) {
				return projectPage(0, true), nil
			}}
			value, err := fetchSnapshot(t.Context(), fake, tc.unbounded, tc.pages, tc.results)
			if err != nil || len(value.Boards) != tc.want || value.Reason != tc.reason || value.Complete != tc.unbounded || value.Truncated == tc.unbounded {
				t.Fatalf("bound ignored: %+v %v", value, err)
			}
			if !tc.unbounded && (value.NextStart == nil || *value.NextStart != tc.want) {
				t.Fatal("truncation lost first unread offset")
			}
		})
	}
}

func TestUnboundedRefetchesBoundedCache(t *testing.T) {
	t.Parallel()
	var store adapters.MemoryCache
	value := snapshot{Boards: []envelope.BoardRow{{ID: 1, Name: "One", ProjectKeys: []string{}, ProjectsComplete: true}}, Failures: []envelope.BoardProjectFailure{}, FetchedAt: time.Now(), PagesFetched: 100, Truncated: true, Reason: "max_pages", NextStart: new(1)}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(cacheKey, raw); err != nil {
		t.Fatal(err)
	}
	calls := 0
	fake := boardFake{boards: func(context.Context, int, int) (core.BoardRead, error) { calls++; return boardPage(0, true), nil }}
	out, err := runList(t.Context(), depsFor(fake, &store), options{unbounded: true})
	if err != nil || calls != 1 || out.FromCache || out.Truncated || !out.Pagination.Complete {
		t.Fatalf("unbounded served a capped cache: %+v %v", out, err)
	}
}

func TestBoardWalkFailureRetainsValidatedPagesAtomically(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		status  int
		repeat  bool
		code    string
		success bool
	}{{"throttle", 429, false, "", true}, {"server", 503, false, "jira_server_error", false}, {"permission", 403, false, "jira_forbidden", false}, {"repeat", 0, true, cli.CodeBoardResponseInvalid, false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := boardFake{boards: func(_ context.Context, start, _ int) (core.BoardRead, error) {
				if start == 0 {
					return boardPage(0, false, board(1, "One")), nil
				}
				if tc.repeat {
					return boardPage(1, true, board(2, "Two"), board(1, "Repeated")), nil
				}
				return core.BoardRead{}, &adapterjira.APIError{StatusCode: tc.status, RetryAfterSeconds: 7}
			}, projects: func(context.Context, int64, int, int) (core.BoardProjectRead, error) {
				return projectPage(0, true), nil
			}}
			var store adapters.MemoryCache
			out, err := runList(t.Context(), depsFor(fake, &store), options{all: true})
			if (err == nil) != tc.success || out.Count != 1 || out.Boards[0].ID != 1 || out.NextStart == nil || *out.NextStart != 1 || out.Pagination.Complete || out.CacheSaved != tc.success {
				t.Fatalf("failed page polluted/cached the prefix: %+v %v", out, err)
			}
			if tc.success {
				if out.TruncatedReason != "rate_limit" || out.RetryAfterSeconds != 7 || len(listWarnings(out)) != 1 {
					t.Fatal("partial throttle lost warning/metadata")
				}
			} else if cli.MapError(err).Code != tc.code {
				t.Fatalf("wrong taxonomy: %v", err)
			}
		})
	}
}

func TestProjectFailuresAreExplicitAndKeepPriorKeys(t *testing.T) {
	t.Parallel()
	fake := boardFake{boards: func(context.Context, int, int) (core.BoardRead, error) {
		return boardPage(0, true, board(42, "Sprint")), nil
	}, projects: func(_ context.Context, _ int64, start, _ int) (core.BoardProjectRead, error) {
		if start == 0 {
			return projectPage(0, false, "KEY10", "KEY2"), nil
		}
		return core.BoardProjectRead{}, &adapterjira.APIError{StatusCode: http.StatusForbidden}
	}}
	out, err := runList(t.Context(), depsFor(fake, nil), options{})
	if err != nil || len(out.ProjectFailures) != 1 || out.ProjectFailures[0].BoardID != 42 || out.ProjectFailures[0].Error.Code != "jira_forbidden" || out.Boards[0].ProjectsComplete || !reflect.DeepEqual(out.Boards[0].ProjectKeys, []string{"KEY2", "KEY10"}) || out.Boards[0].ProjectsNextStart == nil || *out.Boards[0].ProjectsNextStart != 2 || len(listWarnings(out)) != 1 {
		t.Fatalf("project failure became empty success: %+v %v", out, err)
	}
}

func TestProjectWalkRejectsRepeatedOrUnsafeRowsAndBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		second         core.BoardProjectRead
		pages, results int
	}{{"repeat", projectPage(1, true, "KEY"), 9, 9}, {"unsafe", projectPage(1, true, "KEY OR OTHER"), 9, 9}, {"pages", projectPage(1, true, "OTHER"), 1, 9}, {"results", projectPage(1, true, "OTHER"), 9, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			fake := boardFake{projects: func(_ context.Context, _ int64, start, _ int) (core.BoardProjectRead, error) {
				calls++
				if start == 0 {
					return projectPage(0, false, "KEY"), nil
				}
				return tc.second, nil
			}}
			keys, next, err := projectKeys(t.Context(), fake, 42, tc.pages, tc.results)
			wantCalls := 2
			if tc.name == "pages" || tc.name == "results" {
				wantCalls = 1
			}
			if err == nil || cli.MapError(err).Code != cli.CodeBoardResponseInvalid || !reflect.DeepEqual(keys, []string{"KEY"}) || next == nil || *next != 1 || calls != wantCalls {
				t.Fatalf("unsafe/unbounded project walk accepted: keys=%v next=%v err=%v", keys, next, err)
			}
		})
	}
}

func TestProjectCancellationRetainsBoardsAndStopsEnrichment(t *testing.T) {
	t.Parallel()
	calls := 0
	fake := boardFake{boards: func(context.Context, int, int) (core.BoardRead, error) {
		return boardPage(0, true, board(1, "One"), board(2, "Two")), nil
	}, projects: func(context.Context, int64, int, int) (core.BoardProjectRead, error) {
		calls++
		return core.BoardProjectRead{}, context.Canceled
	}}
	var store adapters.MemoryCache
	out, err := runList(t.Context(), depsFor(fake, &store), options{all: true})
	if !errors.Is(err, context.Canceled) || calls != 1 || out.Count != 2 || out.CacheSaved || len(out.ProjectFailures) != 1 || out.Boards[0].ProjectsComplete || out.Boards[1].ProjectsComplete {
		t.Fatalf("cancellation was cached or enrichment continued: %+v %v calls=%d", out, err, calls)
	}
}

func TestBoardListMalformedSnapshotsRefetch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		breakSnapshot func(*snapshot)
	}{
		{"missing boards", func(s *snapshot) { s.Boards = nil }},
		{"missing failures", func(s *snapshot) { s.Failures = nil }},
		{"future time", func(s *snapshot) { s.FetchedAt = time.Now().Add(time.Hour) }},
		{"missing pages", func(s *snapshot) { s.PagesFetched = 0 }},
		{"false completion", func(s *snapshot) { s.Truncated = true }},
		{"bad resume", func(s *snapshot) {
			s.Complete = false
			s.Truncated = true
			s.Reason = "max_pages"
			s.NextStart = new(999)
		}},
		{"unknown reason", func(s *snapshot) { s.Complete = false; s.Truncated = true; s.Reason = "unknown"; s.NextStart = new(1) }},
		{"complete resume", func(s *snapshot) { s.NextStart = new(1) }},
		{"unexpected retry", func(s *snapshot) { s.RetryAfterSeconds = 1 }},
		{"duplicate board", func(s *snapshot) { s.Boards = append(s.Boards, s.Boards[0]) }},
		{"unsafe key", func(s *snapshot) { s.Boards[0].ProjectKeys = []string{"KEY OR OTHER"} }},
		{"duplicate key", func(s *snapshot) { s.Boards[0].ProjectKeys = []string{"KEY", "KEY"} }},
		{"missing project array", func(s *snapshot) { s.Boards[0].ProjectKeys = nil }},
		{"unexplained partial projects", func(s *snapshot) { s.Boards[0].ProjectsComplete = false }},
		{"complete projects resume", func(s *snapshot) { s.Boards[0].ProjectsNextStart = new(1) }},
		{"unknown failure board", func(s *snapshot) {
			s.Failures = []envelope.BoardProjectFailure{{BoardID: 2, Error: envelope.Error{Code: "jira_forbidden"}}}
		}},
		{"failure on complete projects", func(s *snapshot) {
			s.Failures = []envelope.BoardProjectFailure{{BoardID: 1, Error: envelope.Error{Code: "jira_forbidden"}}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			value := snapshot{Boards: []envelope.BoardRow{{ID: 1, Name: "One", ProjectKeys: []string{"KEY"}, ProjectsComplete: true}}, Failures: []envelope.BoardProjectFailure{}, FetchedAt: time.Now(), PagesFetched: 1, Complete: true}
			tc.breakSnapshot(&value)
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var store adapters.MemoryCache
			if err := store.Set(cacheKey, raw); err != nil {
				t.Fatal(err)
			}
			calls := 0
			fake := boardFake{boards: func(context.Context, int, int) (core.BoardRead, error) { calls++; return boardPage(0, true), nil }}
			out, err := runList(t.Context(), depsFor(fake, &store), options{})
			if err != nil || calls != 1 || out.FromCache || out.CacheSourceState != "malformed" || out.Count != 0 || !out.Pagination.Complete {
				t.Fatalf("malformed snapshot was served: %+v calls=%d err=%v", out, calls, err)
			}
		})
	}
}

func TestProjectKeysDeduplicateDistinctProjectIdentities(t *testing.T) {
	t.Parallel()
	fake := boardFake{projects: func(context.Context, int64, int, int) (core.BoardProjectRead, error) {
		page := projectPage(0, true, "KEY", "KEY")
		page.Page.Values.Value[1].ID = jira.NullableOf("other-project")
		return page, nil
	}}
	keys, next, err := projectKeys(t.Context(), fake, 42, 100, 10000)
	if err != nil || next != nil || !reflect.DeepEqual(keys, []string{"KEY"}) {
		t.Fatalf("duplicate key leaked or distinct identity refused: %v %v %v", keys, next, err)
	}
}

func TestBoardWalkRefusesIncompletePageFacts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		page core.BoardRead
	}{
		{"nil page", core.BoardRead{}},
		{"missing final", core.BoardRead{Page: &jira.BoardPage{Values: jira.NullableOf([]jira.Board{board(1, "One")})}}},
		{"null values", core.BoardRead{Page: &jira.BoardPage{IsLast: jira.NullableOf(true), Values: jira.NullableNull[[]jira.Board]()}}},
		{"empty nonfinal", boardPage(0, false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			fake := boardFake{boards: func(context.Context, int, int) (core.BoardRead, error) {
				calls++
				if calls > 1 {
					return boardPage(0, true), nil
				}
				return tc.page, nil
			}, projects: func(context.Context, int64, int, int) (core.BoardProjectRead, error) {
				return projectPage(0, true), nil
			}}
			out, err := runList(t.Context(), depsFor(fake, nil), options{})
			if err == nil || cli.MapError(err).Code != cli.CodeBoardResponseInvalid || calls != 1 || out.CacheSaved || out.Pagination.Complete || out.Count != 0 {
				t.Fatalf("page without facts became success: %+v calls=%d err=%v", out, calls, err)
			}
		})
	}
}
