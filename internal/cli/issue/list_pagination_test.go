package issue

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestAutoPaginateContinuesThroughEmptyPages(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start string
		walk  listWalk
	}{
		{name: "bounded", walk: listWalk{limit: 2}},
		{name: "resume", start: "resume-anchor", walk: listWalk{limit: 2}},
		{name: "all", walk: listWalk{all: true}},
		{name: "unbounded", walk: listWalk{all: true, unbounded: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeListClient{pages: []core.IssueSearchRead{
				searchPage(false, "cursor-a"),
				searchPage(false, "cursor-b", "ABC-1"),
				searchPage(false, "cursor-c"),
				searchPage(true, "", "ABC-2"),
			}}
			out, err := autoPaginate(t.Context(), fake, "project = ABC", []string{"summary"}, tc.start, tc.walk)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Issues) != 2 || out.Issues[0].Key != "ABC-1" || out.Issues[1].Key != "ABC-2" || out.Pagination.Returned != 2 || !out.Pagination.IsLast || out.Truncated {
				t.Fatalf("sparse search did not return its two ordered issues and final state: issues=%d returned=%d last=%t truncated=%t", len(out.Issues), out.Pagination.Returned, out.Pagination.IsLast, out.Truncated)
			}
			wantTokens := []string{tc.start, "cursor-a", "cursor-b", "cursor-c"}
			if len(fake.calls) != len(wantTokens) {
				t.Fatalf("requests=%d, want four distinct cursor requests", len(fake.calls))
			}
			for i, call := range fake.calls {
				wantSize := 2
				if i > 1 {
					wantSize = 1
				}
				if tc.walk.all {
					wantSize = maxRequestPageSize
				}
				if call.nextPageToken != wantTokens[i] || call.maxResults != wantSize || call.jql != "project = ABC" || !slices.Equal(call.fields, []string{"summary"}) {
					t.Fatalf("request %d lost its cursor, remaining count, query or fields: %+v", i, call)
				}
			}
		})
	}
}

func TestAutoPaginateStopsBeforeRevisitingResumeCursor(t *testing.T) {
	fake := &fakeListClient{pages: []core.IssueSearchRead{
		searchPage(false, "cursor-a", "ABC-1"),
		searchPage(false, "resume-anchor", "ABC-2"),
	}}
	out, err := autoPaginate(t.Context(), fake, "project = ABC", nil, "resume-anchor", listWalk{all: true, unbounded: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 2 || len(out.Issues) != 2 || out.Pagination.IsLast || out.Pagination.NextPageToken != "resume-anchor" || !out.Truncated || out.TruncatedReason != envelope.TruncatedReasonRepeatedCursor {
		t.Fatalf("resume cycle was not stopped with both pages retained: requests=%d output=%+v", len(fake.calls), out)
	}
}

func TestAutoPaginateBoundsEmptyPagesAndUnboundedFinishes(t *testing.T) {
	pages := make([]core.IssueSearchRead, 0, command.MaxSearchEmptyPages+1)
	for i := range command.MaxSearchEmptyPages {
		pages = append(pages, searchPage(false, fmt.Sprintf("page-%d", i+1)))
	}
	pages = append(pages, searchPage(true, "", "ABC-1"))
	for _, tc := range []struct {
		name      string
		walk      listWalk
		requests  int
		truncated bool
	}{
		{name: "bounded", walk: listWalk{limit: 1}, requests: command.MaxSearchEmptyPages, truncated: true},
		{name: "all", walk: listWalk{all: true}, requests: command.MaxSearchEmptyPages, truncated: true},
		{name: "unbounded", walk: listWalk{all: true, unbounded: true}, requests: command.MaxSearchEmptyPages + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeListClient{pages: pages}
			out, err := autoPaginate(t.Context(), fake, "project = ABC", nil, "", tc.walk)
			if err != nil {
				t.Fatal(err)
			}
			if len(fake.calls) != tc.requests || out.Truncated != tc.truncated {
				t.Fatalf("requests=%d truncated=%t, want %d/%t", len(fake.calls), out.Truncated, tc.requests, tc.truncated)
			}
			if tc.truncated {
				if out.TruncatedReason != envelope.TruncatedReasonMaxEmptyPages || out.Pagination.NextPageToken != fmt.Sprintf("page-%d", command.MaxSearchEmptyPages) || out.Pagination.IsLast || len(out.Issues) != 0 {
					t.Fatal("empty-page cap lost its resume or incomplete state")
				}
			} else if !out.Pagination.IsLast || len(out.Issues) != 1 || out.Issues[0].Key != "ABC-1" {
				t.Fatal("unbounded remediation did not reach the final issue")
			}
		})
	}
}

func TestAutoPaginateFinalEmptyPageAtSafetyBoundaryIsComplete(t *testing.T) {
	pages := make([]core.IssueSearchRead, 0, command.MaxSearchEmptyPages)
	for i := range command.MaxSearchEmptyPages - 1 {
		pages = append(pages, searchPage(false, fmt.Sprintf("page-%d", i+1)))
	}
	pages = append(pages, searchPage(true, ""))
	fake := &fakeListClient{pages: pages}
	out, err := autoPaginate(t.Context(), fake, "project = ABC", nil, "", listWalk{limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != command.MaxSearchEmptyPages || out.Truncated || !out.Pagination.IsLast || out.Pagination.Returned != 0 {
		t.Fatal("Jira's final empty page was incorrectly called incomplete")
	}
}

func TestAutoPaginateReportsBrokenResumeEvenWhenLimitSatisfied(t *testing.T) {
	for _, tc := range []struct{ name, next, reason string }{
		{name: "missing cursor", reason: envelope.TruncatedReasonMissingCursor},
		{name: "repeated resume cursor", next: "resume", reason: envelope.TruncatedReasonRepeatedCursor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeListClient{pages: []core.IssueSearchRead{searchPage(false, tc.next, "ABC-1")}}
			out, err := autoPaginate(t.Context(), fake, "project = ABC", nil, "resume", listWalk{limit: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(fake.calls) != 1 || len(out.Issues) != 1 || out.Pagination.Returned != 1 || out.Pagination.IsLast || !out.Truncated || out.TruncatedReason != tc.reason {
				t.Fatal("fulfilled count hid an unusable resume cursor")
			}
		})
	}
}

// searchPage scripts Jira's wire facts without implementing pagination policy.
func searchPage(last bool, next string, keys ...string) core.IssueSearchRead {
	issues := make([]jira.IssueBean, len(keys))
	for i, key := range keys {
		issues[i] = issueBean(key)
	}
	return core.IssueSearchRead{Results: &jira.SearchResults{
		IsLast:        jira.NullableOf(last),
		NextPageToken: jira.NullableOf(next),
		Issues:        jira.NullableOf(issues),
	}}
}

func TestAutoPaginateRejectsRepeatedIssuePagesAtomically(t *testing.T) {
	t.Parallel()
	fake := &fakeListClient{pages: []core.IssueSearchRead{searchPage(false, "next", "KEY-1"), searchPage(true, "", "KEY-2", "KEY-1")}}
	out, err := autoPaginate(t.Context(), fake, "project = KEY", nil, "", listWalk{limit: 3})
	if err == nil || cli.MapError(err).Code != cli.CodeSearchResponseInvalid || len(out.Issues) != 1 || out.Issues[0].Key != "KEY-1" || out.Pagination.Returned != 1 || out.Pagination.NextPageToken != "next" || out.Pagination.IsLast || len(fake.calls) != 2 {
		t.Fatalf("bad page polluted retained prefix: %+v %v", out, err)
	}
}

func TestRunListRetainsPrefixWhenLaterRequestIsCanceled(t *testing.T) {
	t.Parallel()
	fake := &fakeListClient{pages: []core.IssueSearchRead{searchPage(false, "next", "KEY-1")}, pagesErr: context.Canceled}
	out, _, err := runList(t.Context(), Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}, "project = KEY", nil, "", listWalk{limit: 3})
	if !errors.Is(err, context.Canceled) || len(out.Issues) != 1 || out.Issues[0].Key != "KEY-1" || len(fake.calls) != 2 || out.Pagination.IsLast {
		t.Fatalf("canceled read lost prefix or cause: %+v %v", out, err)
	}
}

func TestAutoPaginateRequiresExplicitPageFacts(t *testing.T) {
	t.Parallel()
	read := searchPage(true, "", "KEY-1")
	read.Results.IsLast.Set = false
	fake := &fakeListClient{pages: []core.IssueSearchRead{read}}
	out, err := autoPaginate(t.Context(), fake, "project = KEY", nil, "", listWalk{limit: 3})
	if err == nil || cli.MapError(err).Code != cli.CodeSearchResponseInvalid || len(out.Issues) != 0 || out.Pagination.IsLast || len(fake.calls) != 1 {
		t.Fatalf("page without completion became a result: %+v %v", out, err)
	}
}

// Preserve the historical fixture vocabulary while exercising the shared owner.
func nextRequestSize(walk listWalk, collected int) int {
	return walk.shared().RequestSize(collected)
}

func buildListOutput(read core.IssueSearchRead) envelope.IssueListOutput {
	return command.SearchPageOutput(read)
}

const maxRequestPageSize = command.MaxSearchPageSize
