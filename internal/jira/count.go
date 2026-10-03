package jira

// SearchCountRequest is the wire body for POST
// /rest/api/3/search/approximate-count.
//
// Verified against the vendored OpenAPI spec (internal/jira/spec/
// platform.json: paths./rest/api/3/search/approximate-count.post.
// requestBody refs components.schemas.JQLCountRequestBean, whose only
// declared property is jql). Like SearchRequest above, this type is
// built by the adapter rather than decoded off the wire, so it carries
// no absent-vs-null ambiguity to preserve: a zero-value JQL simply is
// not the caller's, never something this package has to guess at. The
// endpoint's own description requires JQL to be BOUNDED (a search
// restriction) — server-side validation this wire type does not
// duplicate.
type SearchCountRequest struct {
	JQL string `json:"jql,omitempty"`
}

// SearchCountResults is the wire response for POST
// /rest/api/3/search/approximate-count.
//
// Verified against the same spec: components.schemas.JQLCountResultsBean
// declares exactly one property, count (int64), with
// additionalProperties: false — unlike IssueBean, SearchResults or
// Comment, Jira reserves no room here for a tenant to grow this
// response, so there is no unmodeled data for an Extra field to
// preserve. This stays a plain struct for the same reason CommentPage
// does (comment.go's own doc comment): every field is unconditionally
// present, with no absent-vs-null distinction worth tracking.
//
// The endpoint's own description calls this an ESTIMATE ("Provide an
// estimated count of the issues that match the JQL. Recent updates
// might not be immediately visible in the returned output."); nothing
// about the wire shape marks that — Count is an ordinary int64 — so
// every caller must carry the caveat forward itself rather than
// expecting this struct to say so (see core.JiraClient.CountIssues's
// own doc comment, and issue list's `~` prefix on the rendered hint).
type SearchCountResults struct {
	Count int64 `json:"count"`
}
