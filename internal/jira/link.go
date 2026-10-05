package jira

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
)

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

// RemoteLinkRequest retains authored request values for POST
// /rest/api/3/issue/{key}/remotelink. The generated RemoteIssueLinkRequest
// describes their meaning; raw members preserve known string escapes, explicit
// nulls, and future extensions. Values are immutable after composition so every
// fan-out target receives the same request. A globalId makes this create or
// replace, and the write receipt distinguishes those outcomes.
type RemoteLinkRequest map[string]json.RawMessage

func (v RemoteLinkRequest) MarshalJSON() ([]byte, error) {
	return MarshalNoEscape(map[string]json.RawMessage(v))
}

// UnmarshalJSON rejects ambiguous authoring input and retains all value bytes.
// Required-field and URL checks run after convenience flags have been merged.
func (v *RemoteLinkRequest) UnmarshalJSON(data []byte) error {
	if !jsontext.Value(data).IsValid() {
		return fmt.Errorf("jira: remote-link payload must contain valid JSON with unique member names")
	}
	members, err := DecodeNativeObject(data, remoteIssueLinkRequestKeys)
	if err != nil {
		return err
	}
	decodeOptional := func(raw json.RawMessage, keys []string) (map[string]json.RawMessage, error) {
		if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, nil
		}
		return DecodeNativeObject(raw, keys)
	}
	if _, err := decodeOptional(members["application"], applicationKeys); err != nil {
		return fmt.Errorf("jira: remote-link application: %w", err)
	}
	if raw, present := members["object"]; present {
		object, err := DecodeNativeObject(raw, remoteObjectKeys)
		if err != nil {
			return fmt.Errorf("jira: remote-link object: %w", err)
		}
		if _, err := decodeOptional(object["icon"], iconKeys); err != nil {
			return fmt.Errorf("jira: remote-link object.icon: %w", err)
		}
		status, err := decodeOptional(object["status"], statusKeys)
		if err != nil {
			return fmt.Errorf("jira: remote-link object.status: %w", err)
		}
		if _, err := decodeOptional(status["icon"], iconKeys); err != nil {
			return fmt.Errorf("jira: remote-link object.status.icon: %w", err)
		}
	}
	*v = members
	return nil
}

// Native inspects the request through the selected schema without replacing
// its authored bytes. Validation also covers requests built by alternate clients.
func (v RemoteLinkRequest) Native() (RemoteIssueLinkRequest, error) {
	var native RemoteIssueLinkRequest
	raw, err := v.MarshalJSON()
	if err != nil {
		return native, err
	}
	var checked RemoteLinkRequest
	if err := checked.UnmarshalJSON(raw); err != nil {
		return native, err
	}
	err = json.Unmarshal(raw, &native)
	return native, err
}
