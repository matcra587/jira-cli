package command

import (
	"context"

	xmath "github.com/gechr/x/math"
	xslices "github.com/gechr/x/slices"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// Search walk bounds apply independently of the endpoint's maximum page size.
// All drains stop after 50 pages or 5,000 issues unless explicitly unbounded.
const (
	MaxSearchPageSize = 100
	MaxSearchPages    = 50
	MaxSearchIssues   = 5000
)

// IssueSearcher reads a single enhanced-search page. Commands own the walk.
type IssueSearcher interface {
	SearchIssues(context.Context, string, []string, string, int, ...string) (core.IssueSearchRead, error)
}

// SearchWalk requests a fixed issue count or a complete search. Unbounded is
// meaningful only with All; commands validate that flag pairing before I/O.
type SearchWalk struct {
	Limit     int
	All       bool
	Unbounded bool
}

// RequestSize bounds the next page by the remaining need and the request cap.
// Zero means no request is needed, including a zero-limit walk.
func (w SearchWalk) RequestSize(collected int) int {
	remaining := w.Limit - collected
	switch {
	case w.Unbounded:
		remaining = MaxSearchPageSize
	case w.All:
		remaining = MaxSearchIssues - collected
	}
	return xmath.Clamp(remaining, 0, MaxSearchPageSize)
}

// WalkIssues retains validated pages on later failures. Pagination describes
// the last accepted page; Returned counts the entire walk. Cursor faults and
// safety caps are explicit truncation, including faults at a satisfied limit.
func WalkIssues(ctx context.Context, client IssueSearcher, jql string, fields []string, startToken string, walk SearchWalk, expand ...string) (envelope.IssueListOutput, error) {
	out := envelope.IssueListOutput{Issues: []envelope.IssueView{}}
	token := startToken
	pages := 0
	progress := NewSearchProgress(startToken, walk.Unbounded)
	seen := map[string]bool{}

	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}

		size := walk.RequestSize(len(out.Issues))
		if size <= 0 {
			// Already have everything this walk was asked for (including
			// the degenerate --limit 0 case, which wants nothing and so
			// makes no request at all).
			return out, nil
		}

		read, err := client.SearchIssues(ctx, jql, fields, token, size, expand...)
		if err != nil {
			return out, err
		}
		if err := read.Results.ValidatePage(size); err != nil {
			return out, cli.NewCodedError(cli.CodeSearchResponseInvalid, err.Error())
		}
		for _, issue := range read.Results.Issues.Value {
			if seen[issue.Key.Value] {
				return out, cli.NewCodedError(cli.CodeSearchResponseInvalid, "search walk repeats an issue key")
			}
		}

		page := SearchPageOutput(read)
		for _, issue := range page.Issues {
			seen[issue.Key] = true
		}
		pages++
		out.Issues = append(out.Issues, page.Issues...)
		out.SearchPages = append(out.SearchPages, page.SearchPages...)
		// Reassigned from THIS page every iteration, so whichever
		// condition below ends the walk, Pagination always describes the
		// most recently fetched page — the resume point a caller's next
		// --next-page-token must start from.
		out.Pagination = page.Pagination
		out.Pagination.Returned = len(out.Issues)

		if page.Pagination.IsLast {
			return out, nil
		}
		if reason := progress.Advance(page.Pagination.NextPageToken, len(page.Issues)); reason != "" {
			out.Truncated = true
			out.TruncatedReason = reason
			return out, nil
		}
		if !walk.All && len(out.Issues) >= walk.Limit {
			// limit's own request is satisfied — not a truncation: the
			// caller asked for exactly this many, and got them.
			return out, nil
		}
		if walk.All && !walk.Unbounded {
			if len(out.Issues) >= MaxSearchIssues {
				out.Truncated = true
				out.TruncatedReason = envelope.TruncatedReasonMaxIssues
				return out, nil
			}
			if pages >= MaxSearchPages {
				out.Truncated = true
				out.TruncatedReason = envelope.TruncatedReasonMaxPages
				return out, nil
			}
		}

		token = page.Pagination.NextPageToken
	}
}

// SearchPageOutput projects one search response into the shared issue shape.
// Wire presence and protocol validation belong to ValidatePage before use.
func SearchPageOutput(read core.IssueSearchRead) envelope.IssueListOutput {
	out := envelope.IssueListOutput{}
	results := read.Results
	if results == nil {
		return out
	}
	metadata := *results
	var absentIssues jira.Nullable[[]jira.IssueBean]
	metadata.Issues = absentIssues
	out.SearchPages = []jira.SearchResults{metadata}
	beans := results.Issues.Value
	out.Issues = xslices.Map(beans, envelope.IssueViewFromBean)
	out.Pagination = envelope.CursorPagination{
		NextPageToken: results.NextPageToken.Value,
		IsLast:        results.IsLast.Value,
		Returned:      len(beans),
	}
	return out
}
