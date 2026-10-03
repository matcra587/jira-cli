package jira

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SearchRequest is the wire body for POST /rest/api/3/search/jql.
//
// Verified against the vendored OpenAPI spec (internal/jira/spec/
// platform.json: paths./rest/api/3/search/jql.post.requestBody refs
// components.schemas.SearchAndReconcileRequestBean). That schema also
// lists fieldsByKeys, properties and reconcileIssues; none of
// them has a caller yet in this slice, so they are left off rather than
// modeled ahead of demand — add a field here when a caller needs one.
//
// Unlike a decoded response, this type carries no absent-vs-null
// ambiguity to preserve: the adapter builds it itself, so a zero
// MaxResults or a nil Fields simply is not sent on the wire, and the
// server falls back to its own default. Plain omitempty fields are
// enough; Nullable exists for values this package receives, not values
// it originates.
type SearchRequest struct {
	// Expand is comma-delimited, unlike Fields, per the enhanced-search schema.
	Expand string `json:"expand,omitempty"`
	// JQL is the query. Jira requires a bounded query (one with a search
	// restriction); that is server-side validation this wire type does
	// not duplicate.
	JQL string `json:"jql,omitempty"`
	// NextPageToken continues a prior page; empty requests the first
	// page.
	NextPageToken string `json:"nextPageToken,omitempty"`
	// MaxResults bounds the page size; 0 lets the server apply its own
	// default (50, per the spec).
	MaxResults int `json:"maxResults,omitempty"`
	// Fields narrows the fields returned per issue, exactly like
	// GetIssue's fields parameter; nil requests the server's default
	// (id only — narrower than GetIssue's own default).
	Fields []string `json:"fields,omitempty"`
}

// SearchResults is the wire response for POST /rest/api/3/search/jql.
//
// Verified against the same vendored spec: paths./rest/api/3/search/
// jql.post.responses.200 refs components.schemas.SearchAndReconcileResults,
// whose declared properties are isLast, issues, names, nextPageToken,
// schema and warnings. There is no total anywhere in that schema —
// cursor pagination has nothing to compute one from — and this type
// must not invent one: a later "Showing N of TOTAL" hint cannot be
// built on a number the server never sent.
//
// This is the cursor-paginated sibling of CommentPage, and deliberately
// NOT shaped like it. CommentPage stays a plain struct because every
// field it carries is unconditionally present, with no absent-vs-null
// distinction worth preserving for a count or an offset. This
// endpoint's own documentation is not that clean: the GET form of this
// same operation says nextPageToken is omitted entirely on the last
// page, while the POST form (quoted above) says the token "will be
// null" on the last page — two different servers-say-so answers for
// the same fact. Nullable lets the wire type carry EITHER without
// picking a winner: Set=false is "the key was never there", Set=true
// with Null=true is "the key was there and null", and a caller checking
// for a next page treats both identically — one comparison, not a
// mis-guessed collapse baked in before anyone reads it.
//
// Extra follows the same rule as IssueBean and Comment: names, schema
// and warnings are real, documented parts of this response — warnings
// in particular can report a truncated result set — and a field this
// slice does not surface must still survive a round-trip rather than
// vanish. Issues reuses IssueBean directly (see fields.go/wire_gen.go)
// so an issue's shape stays defined in exactly one place; nesting it
// inherits IssueBean's own byte-fidelity guarantees for free.
type SearchResults struct {
	IsLast        Nullable[bool]        `json:"isLast,omitzero"`
	Issues        Nullable[[]IssueBean] `json:"issues,omitzero"`
	NextPageToken Nullable[string]      `json:"nextPageToken,omitzero"`

	// Extra carries every property of the payload this model does not
	// declare, verbatim, so unmodeled data (names, schema, warnings)
	// survives a round-trip.
	Extra map[string]json.RawMessage `json:"-"`
}

type searchResultsAlias SearchResults

// searchResultsKeys must list exactly the JSON tags declared above; a
// unit test enforces the pairing so the two cannot drift apart (the
// same fence fields_test.go and comment_test.go keep for their own
// types).
var searchResultsKeys = []string{
	"isLast",
	"issues",
	"nextPageToken",
}

func (v SearchResults) MarshalJSON() ([]byte, error) {
	return marshalMergeExtra(searchResultsAlias(v), v.Extra, searchResultsKeys)
}

func (v *SearchResults) UnmarshalJSON(data []byte) error {
	var next searchResultsAlias
	if err := unmarshalKeepExtra(data, &next, &next.Extra, searchResultsKeys); err != nil {
		return err
	}
	*v = SearchResults(next)
	return nil
}

// ValidatePage requires explicit completion and an issue array before a page
// can contribute identities or counts. Sparse nonfinal pages are valid: their
// cursor progress is checked by the caller across requests. Missing/broken
// cursors remain inspectable walk faults, rather than discarded page data.
func (v *SearchResults) ValidatePage(maxResults int) error {
	if v == nil || !v.IsLast.Set || v.IsLast.Null || !v.Issues.Set || v.Issues.Null || v.Issues.Value == nil {
		return fmt.Errorf("search page lacks explicit completion or an issue array")
	}
	if maxResults <= 0 || len(v.Issues.Value) > maxResults {
		return fmt.Errorf("search page exceeds the requested size")
	}
	seen := make(map[string]bool, len(v.Issues.Value))
	for _, issue := range v.Issues.Value {
		key := issue.Key.Value
		if !issue.Key.Set || issue.Key.Null || strings.TrimSpace(key) == "" || seen[key] {
			return fmt.Errorf("search page lacks or repeats an issue key")
		}
		seen[key] = true
	}
	return nil
}
