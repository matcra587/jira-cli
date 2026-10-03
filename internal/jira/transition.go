package jira

import "encoding/json"

// TransitionRequest is the request body for POST
// /rest/api/3/issue/{key}/transitions.
//
// A dedicated request type rather than a reuse of the generated
// IssueTransition (wire_gen.go), for the reason AddCommentRequest is its
// own type rather than a reuse of Comment: that model describes what
// Jira SENDS — name, to, hasScreen, isAvailable, every one of them
// `readOnly` in the spec — and marshaling one as a request would offer
// the server a dozen fields it must not be told. This type declares
// the typed target and flag inputs, with a native payload for other members.
//
// The pinned operation uses IssueUpdateDetails, including historyMetadata,
// properties and native extensions. Payload preserves those members through
// the shared issue-write owner. The typed fields below compose flag inputs.
//
// **What `fields` and `update` can carry is decided by the WORKFLOW, not
// by this type.** Jira applies both only when the transition has a
// screen; a screenless transition can acknowledge the status change while
// silently discarding fields, operations and properties. History metadata is
// independent of that screen restriction and remains a native request member.
// Nothing in this package can detect that — the caller must read
// IssueTransition.HasScreen first and decide (design §3.1, and
// internal/cli/issue/transition.go, which is the one caller that does).
type TransitionRequest struct {
	// Payload holds native fields, operations, container presence and extensions.
	// The typed target and flag inputs override it during serialization; callers
	// must reject conflicting user inputs before composing the request.
	Payload *IssueWriteRequest `json:"-"`
	// Transition names which transition to perform. Required, and by ID
	// only: the endpoint takes no name, which is the whole reason a
	// caller who typed a status name has to read the transition list
	// before it can send this.
	Transition TransitionRef `json:"transition"`
	// Fields sets issue fields as part of the transition — resolution and
	// assignee, the two the CLI exposes. Omitted entirely when neither
	// was asked for: an empty object is a field update setting nothing,
	// which is a different request from one that updates no fields.
	Fields *TransitionFields `json:"fields,omitempty"`
	// Update carries the operation-shaped half of the payload, which
	// today is the comment. Separate from Fields because Jira separates
	// them: `fields` SETS a value, `update` applies an operation
	// (add/remove/set) to one, and a comment can only be added.
	Update *TransitionUpdate `json:"update,omitempty"`
}

// MarshalJSON routes the request through MarshalNoEscape so a comment's
// ADF body reaches the wire with its <, >, & and U+2028 intact — the
// same requirement AddCommentRequest documents, for the same bytes. The
// alias type is what stops this from finding itself again.
func (v TransitionRequest) MarshalJSON() ([]byte, error) {
	type alias TransitionRequest
	raw, err := MarshalNoEscape(alias(v))
	if err != nil || v.Payload == nil {
		return raw, err
	}
	var overrides IssueWriteRequest
	if err := json.Unmarshal(raw, &overrides); err != nil {
		return nil, err
	}
	payload := v.Payload.Clone()
	payload.ApplyOverrides(overrides)
	return payload.MarshalJSON()
}

// TransitionRef identifies the transition to perform. ID only, because
// the endpoint accepts nothing else.
type TransitionRef struct {
	ID    string                     `json:"id"`
	Extra map[string]json.RawMessage `json:"-"`
}

// MarshalJSON preserves writable and unknown target extensions while the
// composed ID remains authoritative. Name lookup belongs to the CLI owner.
func (v TransitionRef) MarshalJSON() ([]byte, error) {
	type alias TransitionRef
	return marshalMergeExtra(alias(v), v.Extra, []string{"id"})
}

// TransitionFields is the `fields` half of a transition request: values
// SET as the issue moves.
//
// Both members are pointers so an unset one is genuinely absent rather
// than an empty object. That distinction is not cosmetic here —
// `"resolution": {}` is a resolution naming nothing, which Jira rejects,
// and `"assignee": null` is the documented way to UNASSIGN, which is a
// deliberate act this command does not offer and must not perform by
// accident.
type TransitionFields struct {
	Resolution *ResolutionRef `json:"resolution,omitempty"`
	Assignee   *AccountIDRef  `json:"assignee,omitempty"`
}

// ResolutionRef names a resolution. By name rather than id, because a
// resolution's name is what a person types and what Jira's own UI shows;
// the endpoint accepts either.
type ResolutionRef struct {
	Name string `json:"name"`
}

// AccountIDRef names a user by Atlassian account ID.
//
// Account ID only, with no `name` member: `name` is Jira Server's
// spelling and Jira Cloud rejects it outright, so offering the field
// would only make a wrong request expressible.
type AccountIDRef struct {
	AccountID string `json:"accountId"`
}

// TransitionUpdate is the `update` half of a transition request:
// operations applied as the issue moves.
type TransitionUpdate struct {
	// Comment is the comment operations to apply. A slice because that
	// is the endpoint's shape (a field can take several operations), even
	// though this CLI ever sends exactly one.
	Comment []CommentOp `json:"comment,omitempty"`
}

// CommentOp is one operation on the comment field. Add is the only one
// modeled: Jira's comment field also accepts edit and remove, and
// neither belongs on a status change.
type CommentOp struct {
	Add CommentAdd `json:"add"`
}

// CommentAdd is the comment to attach to the transition.
type CommentAdd struct {
	// Body is the ADF document, raw for the reason every other ADF body
	// in this package is raw: internal/adf owns the typed model and this
	// tier only has to preserve bytes.
	Body json.RawMessage `json:"body"`
}
