package envelope

// OffsetPagination and CursorPagination are the two honest per-response
// pagination shapes this CLI emits. They are not variants of one
// permissive struct: Jira's own endpoints do not agree on how they
// paginate, and a single struct wide enough to cover both mechanics
// would also be wide enough to represent states no endpoint can ever
// actually produce — a total AND a cursor token on the same page, an
// is_last flag on an endpoint that has no notion of "last". A field
// nobody's response ever fills is worse than no field: it invites a
// consumer to depend on something that will forever read as its zero
// value, and it weakens every command's derived JSON schema by
// widening it past what the wire can send. So each type documents
// itself, matches exactly one wire shape, and is used only by the
// endpoints whose shape it is.
//
// Neither is the OTHER pagination-shaped type this package declares —
// PageState (envelope.go) — which is a third, unrelated concept: the
// summary of a multi-page WALK an operation already performed
// internally (is it done, did it stop early at a cap), not the shape
// of one page a single API response carries. Do not reach for
// PageState when what is needed is "what page did this one response
// return" — that is what OffsetPagination and CursorPagination are for.

// OffsetPagination is the shared embed for a Jira-style offset-paginated
// read (startAt/maxResults/total) — the comment, worklog, and search-v2
// endpoints all report their page this way, echoing back a known total a
// caller can slice against. It lives INSIDE the operation's data, never
// in meta, and that placement is binding rather than a style choice:
// --output=compact prints bare data on success, and --jq filters exactly
// the document the chosen mode would print, so a consumer of either can
// see this block only if it rides inside data. issue.comment.list is the
// FIRST list command in the CLI, and it sets the precedent every later
// list command reuses — putting pagination in meta here would work for
// this one operation and then break the very first --jq drain of a
// future list, with nothing about this type warning it was coming.
type OffsetPagination struct {
	// StartAt is the zero-based offset of the first item in this page —
	// Jira's own query parameter, echoed back so a caller can confirm
	// which page it got.
	StartAt int `json:"start_at"`
	// MaxResults is the page size that was requested.
	MaxResults int `json:"max_results"`
	// Total is the full result count across every page.
	Total int `json:"total"`
	// Returned is how many items THIS page actually carries — distinct
	// from MaxResults because Jira's last page is usually shorter than
	// the requested size. "There is more" is answered by comparing
	// StartAt+Returned against Total, NOT Returned against Total alone:
	// on any page past the first, Returned by itself only says whether
	// THIS page was short of a full load, not whether the thread is
	// exhausted — a genuinely last page (--start 40 --limit 50 of a
	// 45-total) has Returned(5) well under Total(45) despite 40+5
	// already being the end.
	Returned int `json:"returned"`
}

// CursorPagination is the shared embed for a Jira cursor-paginated read —
// the shape `POST /rest/api/3/search/jql` uses, verified against the
// vendored spec (internal/jira/spec/platform.json): its
// SearchAndReconcileResults response carries exactly isLast, issues,
// names, nextPageToken, schema and warnings. There is no total anywhere
// in that shape — not omitted, not zero, genuinely absent from the wire —
// because a cursor walk over JQL cannot cheaply know how many issues
// match before it has paged through them all. OffsetPagination's Total
// and StartAt fields would have nothing to report for this endpoint;
// giving them a struct here would either fake a value (Total: 0
// indistinguishable from "a query that matched nothing") or force
// omitempty onto fields OffsetPagination deliberately keeps unconditional,
// so this is a separate type rather than an optional corner of that one.
//
// issue.list aggregates multiple pages. Its Pagination retains the last
// fetched page's cursor and IsLast, while Returned counts all collected issues.
// IssueListOutput.Truncated reports incomplete walks independently of these
// server facts.
type CursorPagination struct {
	// NextPageToken fetches the following page — Jira's own token,
	// echoed back verbatim. Omitted (not emitted as "") on the last page:
	// an empty token is meaningless to send back to the search endpoint,
	// so its absence prevents another request. It does not establish that
	// the search is exhausted; IsLast is Jira's completion signal. A missing
	// cursor on a nonfinal page makes an incomplete walk explicit.
	NextPageToken string `json:"next_page_token,omitempty"`
	// IsLast reports whether this is the final page — Jira's own signal
	// for a cursor walk, since there is no total to compare a running
	// offset against. Serialized even when false, unconditionally like
	// OffsetPagination's own fields: a consumer must be able to branch on
	// the value, not on its absence — an omitted false and a build that
	// predates this field would otherwise be indistinguishable.
	IsLast bool `json:"is_last"`
	// Returned is how many items this page actually carries. Jira's own
	// response does not report this directly (issues is the only source),
	// so unlike NextPageToken and IsLast this is a value THIS CLI
	// computes from len(issues) rather than echoes from the wire — kept
	// here, rather than left for a caller to derive from the sibling
	// list's length, so every cursor-paginated command reports it the
	// same way OffsetPagination's own Returned already does.
	Returned int `json:"returned"`
}
