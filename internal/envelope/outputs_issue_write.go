package envelope

import "encoding/json"

// IssueCreateOutput is the `issue.create` operation's payload: the request
// that was composed, and the issue Jira made from it.
//
// THE FIRST MUTATION IN THIS CLI WITH NO SUBJECT AT INVOCATION TIME, and
// that is the one way its layout departs from IssueCommentAddOutput, whose
// doc comment sets the pattern the writes copy. Every other issue-scoped
// operation is ABOUT an issue that already exists, so it carries the
// identity slot unconditionally. This one MAKES the issue, so the identity
// is a server-created outcome and follows the rule such outcomes follow
// everywhere: conditional, absent until the server produced it.
//
// The three placement decisions from the comment-add exemplar all hold.
// The composed request lives at the top rather than being reconstructed
// per path; it survives BOTH the preview and the live write, so a caller
// can diff what a dry run said against what a real run sent; and the
// server-created object is the only conditional member.
type IssueCreateOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// Attempted means the client creation operation was invoked. It does not
	// prove the server received the request; cancellation can stop transport.
	Attempted bool `json:"attempted"`
	// Applied records Jira's HTTP 201 acknowledgement independently of whether
	// its response supplied a usable identity or the command was interrupted.
	Applied bool `json:"applied"`
	// Receipt preserves the complete native creation object, including optional
	// transition/watchers outcomes and unknown response members. It is absent
	// when decoding did not establish one complete object.
	Receipt json.RawMessage `json:"receipt,omitempty"`
	// Payload is the request body composed from the three input routes,
	// exactly as it would be — or was — sent.
	//
	// Raw JSON rather than a typed struct, for jira.IssueWriteRequest's
	// own reason: `--json-input` may carry custom fields no model here can
	// name, and re-typing the document to publish it would drop precisely
	// the members a caller reached for that route to send. It is also what
	// makes `--dry-run` the discovery affordance this command needs: the
	// bytes published here are bytes a caller can feed straight back in
	// through `--json-input`, which is why there is no separate
	// `--generate-json` flag (see internal/cli/issue/write.go).
	Payload json.RawMessage `json:"payload"`
	// DescriptionRoute names which of the three input routes produced the
	// description: "adf" when the whole payload came from --json-input,
	// "markdown" for --markdown/--markdown-file, and empty when the issue
	// is being created without one. Published for the reason
	// IssueCommentAddOutput.BodyRoute is — the routes are not
	// interchangeable, and a caller reviewing a preview needs to know
	// which one they are looking at.
	DescriptionRoute string `json:"description_route"`
	// Issue is the identity Jira assigned, absent on every local-only path
	// (a preview, a generated payload) because there is nothing to name
	// until the site has made it.
	//
	// An IssueRef and not a bare key string: `.data.issue.key` reads the
	// same here as it does on every other issue-scoped operation, which is
	// the whole point of the shared ref type. Create is the one command
	// where a caller is reading that field to LEARN the key rather than to
	// confirm the one they typed.
	Issue *IssueRef `json:"issue,omitempty"`
	// Verified is the readback `--verify` asked for, absent otherwise.
	Verified *VerifiedIssue `json:"verified,omitempty"`
	// VerifyFailed reports that `--verify` was asked for, was attempted,
	// and did not answer.
	//
	// It exists because absence alone cannot say that. A missing Verified
	// means either "nobody asked" or "the readback failed", and those call
	// for opposite responses from a caller — the first is nothing, the
	// second is "your issue exists, go and look at it". A tri-state needs
	// two fields, and this is the second.
	//
	// It is an OUTCOME and not an invocation fact, which is why it belongs
	// in the contract at all: it reports that a request was made and did
	// not come back. The issue was still created — a failed readback never
	// fails the command, because failing it would discard the key and
	// leave a caller who retries with two issues.
	VerifyFailed bool `json:"verify_failed,omitempty"`
}

// IssueEditOutput is the `issue.edit` operation's payload: the changes that
// were composed, and what happened to each named issue.
//
// Keyed, so it takes the exemplar's layout unchanged — the composed
// request at the top because ONE request goes to every key, and only what
// genuinely differs per key inside results[]. What differs on an edit is
// very little, which is itself the honest answer: the endpoint answers 204
// with no body, so absent `--verify` there is nothing per key to report
// beyond whether it worked.
type IssueEditOutput struct {
	MutationState
	// Notify reports whether the request ASKED Jira to email the issue's
	// watchers. Serialized unconditionally, for DeleteSubtasks' reason: a
	// caller auditing what a script did has to be able to see which
	// request was made.
	//
	// **Requested, not achieved, and the distinction is real here.** Jira
	// requires administer-Jira or administer-project permission to
	// suppress the notification and IGNORES the request when the caller
	// lacks it — its own documented behavior, with nothing in the
	// response saying which happened. So a false here means the CLI asked
	// for silence, never that silence was granted. Publishing what was
	// asked is the honest half; there is no field that could publish the
	// other half, because the site does not report it.
	Notify bool `json:"notify"`
	// SelectorState records a --jql/--filter/--from-file run's own query,
	// absent when the caller named the keys.
	SelectorState
	// Payload is the composed request, identical for every key — see
	// IssueCreateOutput.Payload for why it is raw JSON and why it rides
	// both paths.
	Payload json.RawMessage `json:"payload"`
	// DescriptionRoute names which route produced the description, exactly
	// as it does on create.
	DescriptionRoute string `json:"description_route"`
	// Results is one entry per key the caller named, in the order they
	// named them.
	KeyedResults[IssueEditData]
}

// IssueEditData is one key's outcome.
//
// It holds only what is genuinely per key, and on an edit that is the
// readback alone. An empty object is the honest answer on every other
// path: the key WAS edited, and the endpoint told us nothing further about
// it. Inventing a field to fill the space — an echo of the payload, say —
// would republish one fact once per key, which is the exact duplication
// the top-level placement above exists to avoid.
type IssueEditData struct {
	// Verified is this key's readback, present only under `--verify`.
	Verified *VerifiedIssue `json:"verified,omitempty"`
	// VerifyFailed reports that this key's readback was attempted and did
	// not answer — see IssueCreateOutput.VerifyFailed for why absence
	// alone cannot say so. Per key, because the readbacks are per key: one
	// issue's edit can be confirmed while another's confirmation times
	// out, and a single top-level flag could not tell a caller which.
	VerifyFailed bool `json:"verify_failed,omitempty"`
}

// VerifiedIssue is what a `--verify` readback found stored after the write.
//
// Deliberately NARROW — three fields, not the whole issue. The flag's
// question is "did the write land?", and the cheapest honest answer is the
// issue as the site now reports it in the members a create or an edit is
// most likely to have touched. A caller who wants everything runs `jira
// issue view`, which exists, returns the full field set, and does not have
// to be reimplemented inside a write's envelope.
//
// It is also what makes the flag worth gating. The readback is a SECOND
// round trip — the create endpoint answers with an identity and the edit
// endpoint with nothing at all — so unlike `comment add`, whose POST hands
// back the whole created comment for free, there is a real cost here for a
// caller to opt into.
type VerifiedIssue struct {
	// Summary is the stored summary, empty when the issue has none.
	Summary string `json:"summary"`
	// Status is the stored status name.
	Status string `json:"status"`
	// Updated is Jira's own RFC 3339 timestamp, verbatim — wire timestamps
	// are exempt from the CLI's UTC-RFC-3339 rule by design (M5), like
	// every other wire-sourced time in this contract.
	Updated string `json:"updated"`
}

func init() {
	Register("issue.move.fields", IssueEditOutput{}, map[string]any{
		"description": "Confirmed fields/update issue-edit PUTs with the exact native payload and ordered per-key acknowledgements. No bulk migration task is submitted.",
	})
	Register("issue.create", IssueCreateOutput{}, map[string]any{
		"description": "The issue created from the composed request, with the request that produced it.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. It composes the request without creating anything. --validate-remote permits account resolution reads; a local preview cannot establish server field validity.",
			},
			"attempted": map[string]any{"description": "True when the client creation operation was invoked. This does not prove Jira received the request; cancellation can stop transport."},
			"applied":   map[string]any{"description": "True only when Jira acknowledged creation with HTTP 201, independently of usable identity or command failure. Inspect before repeating create or clone."},
			"receipt":   map[string]any{"description": "The complete native creation response object, preserving optional transition/watchers outcomes and future members. A failed followup retains the created issue identity. Absent when no complete object was decoded."},
			"payload": map[string]any{
				"description": "The request body composed from --json-input, the Markdown route and the field flags. Identical on the preview and the live path, and valid input for --json-input.",
			},
			"description_route": map[string]any{
				"description": "Which input route produced the description: adf (--json-input), markdown (--markdown/--markdown-file), or empty for none.",
			},
			"issue": map[string]any{
				"description": "The usable identity returned with native HTTP 201. It survives command failure, but is absent on previews and unusable responses; applied can still be true without an identity.",
			},
			"verified": map[string]any{
				"description": "The issue as the site reports it after the write. Present only under --verify, which pays for a second request.",
			},
			"verify_failed": map[string]any{
				"description": "True when --verify was asked for and the readback did not answer. The issue was still created; only the confirmation is missing.",
			},
		},
	})

	Register("issue.edit", IssueEditOutput{}, map[string]any{
		"description": "The field changes applied to each named issue, with the request that produced them.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. A preview composes the request and stops; it never contacts Jira, so it cannot report whether the issues exist or whether the values are valid.",
			},
			"notify": map[string]any{
				"description": "Whether the request asked Jira to email the issue's watchers. This is what was REQUESTED, not what happened: suppressing the notification needs the administer Jira or administer project permission, and Jira ignores the request from a caller without it while reporting no difference. It rides the query string, so it does not appear in payload.",
			},
			"payload": map[string]any{
				"description": "The request body sent to every key, composed from --json-input, the Markdown route and the field flags. Identical on the preview and the live path.",
			},
			"description_route": map[string]any{
				"description": "Which input route produced the description: adf (--json-input), markdown (--markdown/--markdown-file), or empty for none.",
			},
			"results": map[string]any{
				"description": "One entry per key, in the order given. A key that failed carries error in place of data and does not fail the others.",
			},
		},
	})
}
