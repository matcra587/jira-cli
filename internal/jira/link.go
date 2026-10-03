package jira

// LinkIssueRequest is the request body for POST /rest/api/3/issueLink.
//
// A hand-written request type rather than a reuse of the generated
// IssueLink, for AddCommentRequest's reason: that type models what Jira
// SENDS, including an id the server owns, and marshaling one as a
// request would offer the server fields it must not be told.
//
// **The two issue slots are not source and target.** They are the two
// ENDS of the link, named for the direction the type's own phrasing
// reads in: the link reads `inwardIssue <outward-phrase> outwardIssue`.
// So "A blocks B" with the Blocks type (outward "blocks") is
// InwardIssue=A, OutwardIssue=B — which looks backwards until you read
// it as the sentence.
//
// That mapping is derived from the READ side rather than guessed:
// internal/cli/issue/link.go's linkViews resolves a link found inside
// issue X's own payload, and when the far end Y arrives under
// `outwardIssue` it publishes the relation as the type's OUTWARD phrase
// read from X toward Y — "X blocks Y". Since Jira omits the near end
// when embedding a link in an issue, X is the inward side of that same
// link. The CLI never asks a caller to know any of this: `issue link
// create` takes a sentence and does the mapping (see linkDirection in
// internal/cli/issue/link_create.go).
type LinkIssueRequest struct {
	// Type names the link type. Only the name is sent — the endpoint
	// accepts a name or an id, and the CLI has resolved a name by the
	// time it builds this.
	Type LinkTypeRef `json:"type"`
	// InwardIssue is the end the outward phrase reads FROM.
	InwardIssue LinkIssueRef `json:"inwardIssue"`
	// OutwardIssue is the end the outward phrase reads TO.
	OutwardIssue LinkIssueRef `json:"outwardIssue"`
	// Comment is posted on the INWARD issue as the link is made, in one
	// request. Omitted entirely when absent, which is the ordinary case.
	//
	// AddCommentRequest rather than a second type declared here: the
	// endpoint's `comment` member is a comment body with an optional
	// visibility restriction, which is exactly that type and exactly what
	// `issue comment add` already composes. Declaring a near-identical
	// LinkComment would give the two a way to drift over the one member
	// that matters — the ADF body's bytes.
	//
	// A pointer so the zero case is genuinely absent from the payload: an
	// embedded empty object is a comment with no body, which Jira rejects.
	Comment *AddCommentRequest `json:"comment,omitempty"`
}

// MarshalJSON routes the request through MarshalNoEscape so a link
// comment's ADF body reaches the wire with its <, >, & and U+2028 intact —
// the same requirement AddCommentRequest documents, for the same bytes.
// Without it the comment would be escaped by the package-level encoder on
// its way through this outer struct, undoing what that type guarantees.
func (v LinkIssueRequest) MarshalJSON() ([]byte, error) {
	type alias LinkIssueRequest
	return MarshalNoEscape(alias(v))
}

// LinkTypeRef names a link type in a request. Name only: the CLI
// resolves whatever the caller typed against the live type list before
// it gets here, so it always has the canonical name and never needs the
// id form the endpoint also accepts.
type LinkTypeRef struct {
	Name string `json:"name"`
}

// LinkIssueRef names one end of a link by issue key.
type LinkIssueRef struct {
	Key string `json:"key"`
}

// RemoteLinkRequest is the request body for POST
// /rest/api/3/issue/{key}/remotelink — a REMOTE link (an arbitrary URL
// attached to an issue), which is a different resource from the issue
// links above and reached through a different endpoint. The CLI keeps
// the two apart as `weblink` and `link`, and so does this file.
//
// The endpoint is create-OR-update, keyed on GlobalId: a POST carrying a
// GlobalId that already exists on the issue REPLACES that link and
// answers 200, where a new one answers 201. The CLI publishes which
// happened rather than hiding it (see envelope.IssueWeblinkAddOutput).
type RemoteLinkRequest struct {
	// Object is the item being linked to. Required by the endpoint, and
	// the only member the CLI's own flags fill.
	Object RemoteLinkObject `json:"object"`
	// GlobalId is the caller's own identifier for the remote item,
	// omitted when they gave none. Spelled GlobalId rather than GlobalID
	// to match the wire key it marshals to; the CLI-facing field on the
	// envelope uses the Go-idiomatic spelling.
	GlobalId string `json:"globalId,omitempty"` //nolint:revive // the wire spells it globalId; see the field comment
	// Relationship describes how the issue relates to the item ("causes",
	// "mentioned in"). Free text on this endpoint, unlike an issue link's
	// type, which must exist on the site.
	Relationship string `json:"relationship,omitempty"`
}

// RemoteLinkObject is the linked item itself. Url and Title are the
// endpoint's only required members, which is exactly the pair `weblink
// add` takes as flags.
type RemoteLinkObject struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	// Summary is optional prose shown under the title.
	Summary string `json:"summary,omitempty"`
}
