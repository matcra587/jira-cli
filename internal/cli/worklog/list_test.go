package worklog

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

func worklogPage(start, size, total int, ids ...string) core.WorklogRead {
	rows := make([]jira.Worklog, len(ids))
	for i, id := range ids {
		rows[i] = *storedWorklog(id)
	}
	return core.WorklogRead{Page: &jira.PageOfWorklogs{
		StartAt: jira.NullableOf(int64(start)), MaxResults: jira.NullableOf(int64(size)), Total: jira.NullableOf(int64(total)), Worklogs: jira.NullableOf(rows),
	}}
}

func allOptions() listOptions {
	return listOptions{all: true, keyed: command.KeyedFlags{Parallelism: 2}}
}

func TestWorklogListSparseWalkUsesReturnedCount(t *testing.T) {
	t.Parallel()
	var calls [][2]int
	c := &worklogClient{get: func(_ context.Context, key string, start, size int) (core.WorklogRead, error) {
		calls = append(calls, [2]int{start, size})
		if key != "KEY-1" || size != 100 {
			return core.WorklogRead{}, errors.New("unexpected page request")
		}
		return worklogPage(start, 100, 3, fmt.Sprint(start+1)), nil
	}}
	out, err := readWorklogs(t.Context(), c, "KEY-1", allOptions())
	if err != nil || !out.Complete || out.Truncated || out.Pagination.StartAt != 0 || out.Pagination.Returned != 3 || out.Pagination.Total != 3 || !reflect.DeepEqual(calls, [][2]int{{0, 100}, {1, 100}, {2, 100}}) {
		t.Fatalf("sparse walk skipped records or lied about completion: %+v %v calls=%v", out, err, calls)
	}
	for i, w := range out.Worklogs {
		if w.ID != fmt.Sprint(i+1) {
			t.Fatalf("worklog order changed: %+v", out.Worklogs)
		}
	}
}

func TestWorklogListOnePageAndBeyondEnd(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		start, total int
		ids          []string
		complete     bool
	}{
		{"resumable", 2, 5, []string{"3"}, false}, {"terminal", 2, 3, []string{"3"}, true}, {"beyond end", 6, 3, []string{}, true}, {"empty", 0, 0, []string{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			c := &worklogClient{get: func(_ context.Context, _ string, start, size int) (core.WorklogRead, error) {
				calls++
				if start != tc.start || size != 2 {
					return core.WorklogRead{}, errors.New("requested offset/size ignored")
				}
				return worklogPage(start, 2, tc.total, tc.ids...), nil
			}}
			out, err := readWorklogs(t.Context(), c, "KEY-1", listOptions{start: tc.start, limit: 2})
			if err != nil || calls != 1 || out.Complete != tc.complete || out.Truncated || out.Pagination.StartAt != tc.start || out.Pagination.Returned != len(tc.ids) || out.Worklogs == nil {
				t.Fatalf("single page facts lost: %+v %v calls=%d", out, err, calls)
			}
		})
	}
}

func TestWorklogListEmptyBeforeTotalIsNonProgress(t *testing.T) {
	t.Parallel()
	for _, all := range []bool{false, true} {
		calls := 0
		c := &worklogClient{get: func(_ context.Context, _ string, start, size int) (core.WorklogRead, error) {
			calls++
			return worklogPage(start, size, 1), nil
		}}
		out, err := readWorklogs(t.Context(), c, "KEY-1", listOptions{all: all, limit: 50})
		if err != nil || calls != 1 || out.Complete || !out.Truncated || out.TruncatedReason != "non_progress" || len(out.Worklogs) != 0 {
			t.Fatalf("empty advancing offset was accepted as an ordinary page: all=%t %+v %v calls=%d", all, out, err, calls)
		}
	}
}

func TestWorklogListBoundsHaveSpecificReceipts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                  string
		size, total, returned int
		reason                string
	}{
		{"page bound", 1, 51, 50, "max_pages"}, {"record bound", 100, 5001, 5000, "max_worklogs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			c := &worklogClient{get: func(_ context.Context, _ string, start, size int) (core.WorklogRead, error) {
				calls++
				ids := make([]string, tc.size)
				for i := range ids {
					ids[i] = fmt.Sprint(start + i + 1)
				}
				return worklogPage(start, size, tc.total, ids...), nil
			}}
			out, err := readWorklogs(t.Context(), c, "KEY-1", allOptions())
			if err != nil || calls != 50 || out.Complete || !out.Truncated || out.TruncatedReason != tc.reason || out.Pagination.Returned != tc.returned {
				t.Fatalf("bounded walk: complete=%t truncated=%t reason=%q returned=%d err=%v calls=%d", out.Complete, out.Truncated, out.TruncatedReason, out.Pagination.Returned, err, calls)
			}
		})
	}
}

func TestWorklogListLaterFailureRetainsValidatedPrefix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		later   core.WorklogRead
		failure error
	}{
		{"request", core.WorklogRead{}, cli.NewCodedError("jira_server_error", "unavailable")},
		{"nil page", core.WorklogRead{}, nil},
		{"invalid page", worklogPage(0, 100, 3, "2"), nil},
		{"cross-page duplicate", worklogPage(1, 100, 3, "2", "1"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := &worklogClient{get: func(_ context.Context, _ string, start, _ int) (core.WorklogRead, error) {
				if start == 0 {
					return worklogPage(0, 100, 3, "1"), nil
				}
				return tc.later, tc.failure
			}}
			out, err := runList(t.Context(), clientDeps(c), []string{"KEY-1"}, allOptions())
			if err != nil || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Err != nil || out.Results[0].Data == nil {
				t.Fatalf("validated prefix became total failure: %+v %v", out, err)
			}
			data := out.Results[0].Data
			if data.Complete || !data.Truncated || data.TruncatedReason != "read_failed" || data.ReadError == nil || len(data.Worklogs) != 1 || data.Worklogs[0].ID != "1" || data.Pagination.Returned != 1 || data.Pagination.Total != 3 {
				t.Fatalf("later bad page leaked rows or lost prefix facts: %+v", data)
			}
			want := cli.CodeWorklogResponseInvalid
			if tc.failure != nil {
				want = "jira_server_error"
			}
			if data.ReadError.Code != want || len(listWarnings(out)) != 1 {
				t.Fatalf("incomplete read classification lost: %+v", data)
			}
		})
	}
}

func TestWorklogListOrderedDeduplicatedPartialKeys(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	calls := map[string]int{}
	c := &worklogClient{get: func(_ context.Context, key string, start, size int) (core.WorklogRead, error) {
		mu.Lock()
		calls[key]++
		mu.Unlock()
		if key == "KEY-2" {
			return core.WorklogRead{}, cli.NewCodedError("jira_not_found", "missing issue")
		}
		return worklogPage(start, size, 1, "100"), nil
	}}
	o := listOptions{limit: 50, keyed: command.KeyedFlags{Parallelism: 1}}
	out, err := runList(t.Context(), clientDeps(c), []string{"KEY-3,KEY-1:3"}, o)
	if err != nil || len(out.Results) != 3 || !reflect.DeepEqual(calls, map[string]int{"KEY-1": 1, "KEY-2": 1, "KEY-3": 1}) {
		t.Fatalf("read fanout skipped or repeated keys: %+v %v calls=%v", out, err, calls)
	}
	for i, key := range []string{"KEY-3", "KEY-1", "KEY-2"} {
		if out.Results[i].Issue.Key != key || out.Results[i].OK != (i != 2) {
			t.Fatalf("read key ordering changed: %+v", out.Results)
		}
	}
	if out.Results[2].Data != nil || out.Results[2].Err.Code != "jira_not_found" {
		t.Fatalf("read failure lost: %+v", out.Results[2])
	}
}

func TestWorklogListCancellationStopsNextPage(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var calls atomic.Int32
	c := &worklogClient{get: func(context.Context, string, int, int) (core.WorklogRead, error) {
		calls.Add(1)
		cancel()
		return worklogPage(0, 100, 2, "1"), nil
	}}
	out, err := readWorklogs(ctx, c, "KEY-1", allOptions())
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 || len(out.Worklogs) != 1 || out.Complete {
		t.Fatalf("cancellation discarded prefix or requested another page: %+v %v calls=%d", out, err, calls.Load())
	}
}

func TestWorklogListTotalFailureRetainsEveryKey(t *testing.T) {
	t.Parallel()
	c := &worklogClient{get: func(_ context.Context, key string, _, _ int) (core.WorklogRead, error) {
		if key == "KEY-2" {
			return core.WorklogRead{}, cli.NewCodedError("jira_unauthorized", "credential refused")
		}
		return core.WorklogRead{}, cli.NewCodedError("jira_not_found", "missing issue")
	}}
	out, err := runList(t.Context(), clientDeps(c), []string{"KEY-1:2"}, allOptions())
	if cli.MapError(err).Code != "jira_unauthorized" || len(out.Results) != 2 || out.Results[0].Issue.Key != "KEY-1" || out.Results[0].Err.Code != "jira_not_found" || out.Results[1].Issue.Key != "KEY-2" || out.Results[1].Err.Code != "jira_unauthorized" {
		t.Fatalf("total read failure lost ordered receipts or severity: %+v %v", out, err)
	}
}
