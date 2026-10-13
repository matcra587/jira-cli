package envelope

import (
	"time"

	"github.com/matcra587/jira-cli/internal/jira"
)

// IssueListOutput is the `issue.list` and `issue.mine` payload: matching issues
// collected across a JQL search's requested cursor walk.
//
// This is the first CURSOR-paginated list command (issue.comment.list,
// the first list command overall, is offset-paginated — see
// pagination.go). Pagination lives inside data, never in meta, for the
// same reason issue.comment.list's own doc comment gives: --output=compact
// prints bare data and --jq filters exactly that document, so a consumer
// of either could not otherwise see it.
//
// Issues reuses IssueView verbatim rather than inventing a second issue
// shape: each entry carries the same identity object (data.issues[].key,
// with id/self when the search response carried them) and the same
// fields passthrough boundary `issue view` already registers, so a
// caller narrowing `--fields` or reading a tenant's custom field does
// not need to learn a second contract for the list form.
type IssueListOutput struct {
	// Issues is every issue this invocation collected, in the order
	// Jira returned them across however many pages the shared search
	// walker fetched (internal/cli/command/search_walk.go's WalkIssues) — `--limit` issues fetched when bounded, every
	// issue up to `--all`'s own cap when draining. Each carries the
	// same identity shape `issue view` uses (IssueRef) plus its fields —
	// narrowed to whatever `--fields` asked for, or the command's own
	// table-shaped default when it did not.
	Issues []IssueView `json:"issues"`
	// SearchPages retains each accepted native page's completion, cursor and
	// metadata (names, schema, warnings and future members). Issues appear
	// only in Issues above. Failed/unvalidated pages never enter this receipt.
	SearchPages []jira.SearchResults `json:"search_pages,omitempty"`
	// Pagination describes where the LAST page this invocation fetched
	// leaves the cursor — never the first: a caller resuming with
	// next_page_token must never see a page already folded into Issues
	// above, or a resume would hand back duplicates rather than new
	// results. The search endpoint sends no total — see
	// CursorPagination's own doc comment for why one is never invented
	// here.
	Pagination CursorPagination `json:"pagination"`
	// Truncated reports that a safety cap or unusable cursor stopped the
	// search before Jira's last page. It is always
	// serialized so compact and full-JSON consumers can detect incomplete
	// searches. Satisfying --limit alone is not truncation, but a broken
	// resume cursor is still reported even when that count was satisfied.
	// Empty-page guards can truncate bounded searches too.
	Truncated bool `json:"truncated"`
	// TruncatedReason names the cap or cursor fault that stopped the walk.
	// It is empty whenever Truncated is false; the constants below define
	// its values. Pagination retains the server's reported resume facts.
	TruncatedReason string `json:"truncated_reason,omitempty"`
	// Count carries `--count`'s own result: how many issues the composed
	// query matches, WITHOUT ever fetching them. Nil whenever --count was
	// not given — the one signal a machine consumer needs to tell "this
	// is issue list's count-only answer" from "this is a search page
	// that happens to have collected zero issues". Issues and Pagination
	// both sit at their ordinary zero value alongside it: --count takes a
	// single-round-trip path through core.JiraClient.CountIssues, never
	// runList's own auto-paginating search, so neither field has
	// anything real to report.
	Count *IssueCountEstimate `json:"count,omitempty"`
	// JQL carries `--as-jql`'s own answer: the query the convenience
	// filters composed, printed INSTEAD of running it. Absent on every
	// other path — and absence is unambiguous rather than a convention
	// to remember, because a composed query is never the empty string
	// (ComposeJQL, internal/cli/command/jql_builder.go, always yields at least one
	// clause or the caller's own --jql). So this key's presence IS the
	// "this is the preview, not a search result" signal, the same job
	// Count above does for --count, without needing a pointer to
	// distinguish absent from zero.
	//
	// Issues stays an empty, non-nil slice and Pagination at its zero
	// value alongside it: --as-jql contacts nothing at all, so neither
	// field has anything real to report — the same "nothing real to
	// report" posture Count and WebLink already document for their own
	// single-purpose paths.
	JQL string `json:"jql,omitempty"`
	// Board is the board `--board`/`--board-id` resolved to, absent when
	// neither was given.
	//
	// It publishes the board's own query as well as its identity, because
	// the name alone cannot reproduce a result: a board IS a saved
	// filter, that filter is editable by anyone it is shared with, and
	// two boards on one site may share a name. A caller auditing "why
	// these issues" needs the query that actually ran, and `jql` above is
	// the COMPOSED one — board scope plus whatever filters narrowed it —
	// so the board's own half would otherwise be unrecoverable.
	Board *IssueBoardView `json:"board,omitempty"`
	// SavedQuery records the selected local file's name, metadata and original
	// JQL. It survives partial reads; Project is descriptive, never a scope.
	SavedQuery *SavedQueryView `json:"saved_query,omitempty"`
	// WebLink carries `--web`'s own resolved search URL at data.web_url
	// — see its own doc comment (envelope.go). `--web` skips the search
	// entirely (internal/cli/issue/list.go's runListWeb), so Issues stays
	// empty and Pagination at its zero value whenever WebURL is set, the
	// same "nothing real to report" posture Count above already
	// documents for --count's own single-purpose path.
	WebLink
}

// IssueCountEstimate is `--count`'s own result. Value is Jira's
// approximate-count endpoint's own number (core.JiraClient.CountIssues).
// Approximate rides the wire as ALWAYS true, rather than being left for a
// consumer to infer from Value's mere presence: the endpoint's own
// documentation calls the number an estimate ("recent updates might not
// be immediately visible in the returned output" —
// core.JiraClient.CountIssues's own doc comment), so a machine consumer
// gets that caveat structurally, the same honesty the human render's own
// `~` prefix (nextPageHint, internal/cli/issue/list.go) gives a person,
// rather than a bare integer that reads as an exact total.
type IssueCountEstimate struct {
	Value       int64 `json:"value"`
	Approximate bool  `json:"approximate"`
}

// IssueListOutput.TruncatedReason distinguishes safety caps from server cursor
// faults. The latter cannot be repaired by lifting a cap or raising --limit.
const (
	TruncatedReasonMaxPages       = "max_pages"
	TruncatedReasonMaxIssues      = "max_issues"
	TruncatedReasonMaxEmptyPages  = "max_empty_pages"
	TruncatedReasonMissingCursor  = "missing_cursor"
	TruncatedReasonRepeatedCursor = "repeated_cursor"
)

// TruncatedReasonMaxComments is IssueCommentListOutput.TruncatedReason's
// second possible value, beside TruncatedReasonMaxPages above, which the
// two commands share: `issue comment list --all` counts COMMENTS toward
// its own cap where `issue list --all` counts issues, but "the page cap
// stopped it" means exactly the same thing on both, so that reason is
// reused rather than respelled.
//
// It lives beside the search reasons so consumers can see which values apply
// to each operation. Only max_pages is shared with comment pagination.
const TruncatedReasonMaxComments = "max_comments"

// Comment reads retain observed pages when a later request fails or the
// server's next page is unusable. These reasons are failures, not safety caps.
const (
	TruncatedReasonReadFailed      = "read_failed"
	TruncatedReasonResponseInvalid = "response_invalid"
)

func init() {
	overrides := map[string]any{
		"description": "Every issue this invocation collected, auto-paginated across as many requests as --limit or --all needed.",
		"properties": map[string]any{
			"issues": map[string]any{
				"description": "The collected issues, in Jira's own order; each carries its identity plus its fields at .issues[].fields.",
			},
			"pagination": map[string]any{
				"description": "The cursor the LAST page fetched leaves at (next_page_token/is_last/returned) — there is no total.",
			},
			"truncated": map[string]any{
				"description": "True when a safety cap or unusable cursor stopped the search before Jira's last page. This can occur with --limit as well as --all; unusable resume cursors are reported even when the requested count was satisfied.",
			},
			"truncated_reason": map[string]any{
				"description": "The safety cap or cursor fault that stopped the walk when truncated is true. Cursor faults require inspection rather than lifting a cap.",
				"enum":        []string{TruncatedReasonMaxPages, TruncatedReasonMaxIssues, TruncatedReasonMaxEmptyPages, TruncatedReasonMissingCursor, TruncatedReasonRepeatedCursor},
			},
			"count": map[string]any{
				"description": "--count's own result: how many issues match, without fetching them. Absent unless --count was given.",
			},
			"board": map[string]any{
				"description": "The board --board or --board-id resolved to: its id, name, and its own query. Absent unless a board was named. The board's query is published alongside the composed jql because neither can be derived from the other, and a board's filter is editable by anyone it is shared with.",
			},
			"jql": map[string]any{
				"description": "--as-jql's own result: the composed query, printed instead of run. Absent unless --as-jql was given; issues stays empty.",
			},
			"saved_query": map[string]any{
				"description": "The local .jql file selected by --saved, including its literal filename stem, frontmatter and original query. Project is descriptive metadata, not an implicit filter. Retained when later search pages fail.",
			},
			"web_url": map[string]any{
				"description": "The composed search's issue-navigator URL. Present only when --web was given; --web skips the search, so issues stays empty.",
			},
		},
	}
	for _, op := range []string{"issue.list", "issue.mine"} {
		Register(op, IssueListOutput{}, overrides)
	}
}

// SavedQueryView identifies the local query that supplied a search's JQL.
type SavedQueryView struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Project     string `json:"project"`
	JQL         string `json:"jql"`
}

// IssueBoardView is the board behind a `--board` listing.
type IssueBoardView struct {
	// ID is the board's numeric id — the value to pass to --board-id to
	// skip name resolution, and the only thing that tells two
	// same-named boards apart.
	ID int64 `json:"id"`
	// Name is the board's display name.
	Name string `json:"name"`
	// JQL is the board's OWN query, before any convenience filter
	// narrowed it. Distinct from the envelope's composed `jql`, and both
	// are published because neither can be derived from the other.
	JQL string `json:"jql"`
	// Precedence identifies configured scope; omitted for existing flag views.
	Precedence string `json:"precedence,omitempty"`
	// CachedAt is the configured scope snapshot, without a live freshness claim.
	CachedAt *time.Time `json:"cached_at,omitempty"`
}
