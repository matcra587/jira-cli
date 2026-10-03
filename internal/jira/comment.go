package jira

import "encoding/json"

// Comment is the hand-curated model of one Jira comment. gen-wire does
// not generate this one for the same reason IssueFields is hand-curated
// (fields.go): a comment's body carries the same tenant-editable shape
// concerns as an issue's fields, and the CLI only ever reads the members
// it actually surfaces (id, author, body, created, updated) — everything
// else a tenant's comment payload carries (visibility, jsdPublic,
// renderedBody, properties, …) rides in Extra untouched.
//
// Body stays raw ADF, exactly like IssueFields.Description: internal/adf
// owns its typed model, and this wire tier only has to preserve bytes.
//
// Byte fidelity: this type's MarshalJSON goes through marshalMergeExtra,
// which itself goes through marshalNoEscape, so a preserved ADF body's
// <, >, & and U+2028 survive the wire tier intact. Callers must not
// re-marshal this struct with package-level json.Marshal.
type Comment struct {
	ID      Nullable[string]          `json:"id,omitzero"`
	Author  Nullable[UserDetails]     `json:"author,omitzero"`
	Body    Nullable[json.RawMessage] `json:"body,omitzero"`
	Created Nullable[string]          `json:"created,omitzero"`
	Updated Nullable[string]          `json:"updated,omitzero"`

	// Extra carries every property of the payload this model does not
	// declare, verbatim, so unmodeled data survives a round-trip.
	Extra map[string]json.RawMessage `json:"-"`
}

type commentAlias Comment

// commentKeys must list exactly the JSON tags declared above; a unit
// test enforces the pairing so the two cannot drift apart (see
// fields_test.go's TestIssueFieldsKeysMatchStructTags for the pattern
// this mirrors).
var commentKeys = []string{
	"id",
	"author",
	"body",
	"created",
	"updated",
}

func (v Comment) MarshalJSON() ([]byte, error) {
	return marshalMergeExtra(commentAlias(v), v.Extra, commentKeys)
}

func (v *Comment) UnmarshalJSON(data []byte) error {
	return unmarshalKeepExtra(data, (*commentAlias)(v), &v.Extra, commentKeys)
}

// AddCommentRequest is the request body for POST
// /rest/api/3/issue/{key}/comment.
//
// A dedicated request type rather than reusing Comment above, for the
// reason SearchRequest is its own type: Comment models what Jira SENDS,
// and every one of its members (id, author, created, updated) is
// server-owned. Marshaling one as a request would offer the server
// fields it must not be told, and Extra would carry back whatever a read
// happened to leave in it. This type declares exactly the two things a
// caller may set.
//
// Plain omitempty rather than Nullable, matching SearchRequest: Nullable
// exists for values this package RECEIVES and must distinguish absent
// from null, and neither applies to a body this package originates.
type AddCommentRequest struct {
	// Body is the comment's ADF document. json.RawMessage, not a typed
	// node tree, for the same reason Comment.Body is: internal/adf owns
	// the typed model, and this wire tier only has to preserve bytes.
	//
	// It rides through marshalNoEscape below, so a body containing <, >,
	// & or U+2028 reaches Jira exactly as composed — the property that
	// makes an ADF document survive the round trip intact.
	Body json.RawMessage `json:"body,omitempty"`
	// Visibility restricts who can read the comment; omitted entirely
	// when unset, which is Jira's own "visible to everyone who can see
	// the issue" default. A pointer rather than a value so the zero case
	// is genuinely absent from the payload: an embedded empty object
	// would be a visibility restriction naming no group, which Jira
	// rejects.
	Visibility *CommentVisibility `json:"visibility,omitempty"`
}

// MarshalJSON routes the request through MarshalNoEscape so an ADF body's
// <, >, & and U+2028 reach the wire unescaped.
//
// This is not cosmetic. encoding/json's default HTML escaping would
// rewrite those bytes inside the RawMessage, so the document Jira stores
// would differ from the one the caller composed and the one --dry-run
// previewed. Comment above documents the same requirement for the read
// direction; this is the write half of it.
//
// The alias type is what stops this from recursing: a plain
// MarshalNoEscape(v) would find this very method again.
func (v AddCommentRequest) MarshalJSON() ([]byte, error) {
	type alias AddCommentRequest
	return MarshalNoEscape(alias(v))
}

// UpdateCommentRequest is the request body for PUT
// /rest/api/3/issue/{key}/comment/{id}.
//
// A dedicated type rather than a reuse of AddCommentRequest, for one
// reason that is entirely about the PUT: this endpoint has to be able to
// say "no visibility" out loud, and AddCommentRequest cannot. There, an
// absent Visibility means "post it unrestricted", because a comment
// being created has no prior restriction to keep. Here, absence means
// "leave whatever is there alone" — so removing a restriction needs a
// THIRD state, an explicit JSON null, which a `*CommentVisibility` with
// omitempty has no way to spell.
//
// Hence the flag beside the pointer, and MarshalJSON below rather than
// struct tags. The two are mutually exclusive and the CLI refuses the
// pair before it reaches here (--visibility-group/--visibility-role
// against --clear-visibility), so this type never has to decide which
// wins.
//
// Body carries the same requirement AddCommentRequest.Body does — raw
// ADF, marshaled without HTML escaping — and for the same reason: the
// document Jira stores must be the one the caller composed, byte for
// byte, or --dry-run has previewed something else.
type UpdateCommentRequest struct {
	// Body is the comment's new ADF document. Always sent: a PUT
	// REPLACES the comment, so a payload omitting the body would be
	// asking Jira to store a comment with none.
	Body json.RawMessage
	// Visibility is the restriction to apply, or nil to leave the
	// comment's current one untouched.
	Visibility *CommentVisibility
	// ClearVisibility sends an explicit null, removing whatever
	// restriction the comment currently carries. Distinct from a nil
	// Visibility, which changes nothing — see the type's doc comment.
	ClearVisibility bool
}

// MarshalJSON emits the three-state visibility the struct tags cannot.
//
// json.RawMessage for the visibility slot rather than the typed pointer,
// because that is the only field type that can hold "absent", "an
// object" AND the literal `null` — omitempty on []byte tests length, and
// the four bytes of `null` are not empty.
//
// Everything goes through MarshalNoEscape for AddCommentRequest's
// reason: encoding/json would rewrite <, > and & inside the ADF body,
// so the stored document would differ from the previewed one. The
// payload alias is a local type rather than an alias of this one, since
// the two do not share a field shape.
func (v UpdateCommentRequest) MarshalJSON() ([]byte, error) {
	type payload struct {
		Body       json.RawMessage `json:"body,omitempty"`
		Visibility json.RawMessage `json:"visibility,omitempty"`
	}
	p := payload{Body: v.Body}
	switch {
	case v.ClearVisibility:
		p.Visibility = json.RawMessage("null")
	case v.Visibility != nil:
		raw, err := MarshalNoEscape(v.Visibility)
		if err != nil {
			return nil, err
		}
		p.Visibility = raw
	}
	return MarshalNoEscape(p)
}

// CommentVisibility restricts a comment to one group or project role.
//
// Type is "group" or "role" and Value names the group or role. The pair
// is Jira's own shape, and it holds exactly one restriction — which is
// why the two flags that set it (--visibility-group, --visibility-role)
// cannot both be given.
type CommentVisibility struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// CommentPage is the response envelope for GET
// /rest/api/3/issue/{key}/comment: a plain offset page, not a
// Nullable-wrapped model, because every field is always present in a
// well-formed response and there is no absent-vs-null distinction
// worth preserving for a count or an offset.
type CommentPage struct {
	StartAt    int       `json:"startAt"`
	MaxResults int       `json:"maxResults"`
	Total      int       `json:"total"`
	Comments   []Comment `json:"comments"`
}
