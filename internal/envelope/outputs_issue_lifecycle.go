package envelope

import "encoding/json"

// The three lifecycle writes: removing an issue, copying one, and moving
// one in the backlog's order. Grouped because each is about an issue's
// EXISTENCE or its POSITION rather than its content, which is what every
// other write in this contract changes.

// IssueDeleteOutput is the `issue.delete` operation's payload: which
// issue deletion acknowledgements and failures, including interrupted runs.
//
// THE MOST DESTRUCTIVE OPERATION IN THIS CLI, and the envelope's job
// here is mostly to be honest about how little it can say afterwards. A
// deleted issue has no identity left to publish beyond the key the
// caller typed, and Jira's 204 carries nothing — so what survives is the
// request: which keys, and whether subtasks were requested too. A live per-key
// data object records a validated 204, not an independent absence check.
type IssueDeleteOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// SelectorState records a --jql/--filter/--from-file run's own query,
	// absent when the caller named the keys. It matters more on this
	// command than on any other in the tree: results[] says what was
	// deleted, and this says why those issues and not others — which is
	// the only remaining evidence once the issues themselves are gone.
	SelectorState
	// DeleteSubtasks reports whether the request told Jira to take each
	// issue's subtasks with it.
	//
	// Serialized unconditionally, and it is the field in this envelope
	// that most needs to be. Jira REFUSES to delete an issue that has
	// subtasks unless told to cascade, so false and true are not "one
	// issue or several" — they are "this may have been refused" against
	// "this took everything below it". A caller auditing what a script
	// did has to be able to see which request was made.
	DeleteSubtasks bool `json:"delete_subtasks"`
	// Results is one entry per key, in argv order.
	KeyedResults[IssueDeleteData]
}

// IssueDeleteData is an empty object for a preview or a native 204 acknowledgement.
// It can accompany a failed row when a client returns an acknowledgement and an
// error together; the acknowledgement and original error both survive.
//
// The second operation object in this contract with no members, after
// IssueLinkCreateData, and for a related reason: Jira answers 204 with
// no body. Here it goes further — there is nothing that COULD be
// reported, because the subject of the operation no longer exists. The
// key is in the entry's own `issue` slot, which is all a caller needs to
// know which of their arguments this entry answers for.
type IssueDeleteData struct{}

// IssueCloneOutput is the `issue.clone` operation's payload: the new
// issue made from each named one.
//
// **A clone is not an endpoint.** Jira has no copy operation, so this
// command reads each source issue and creates a new one from what it
// found — which is why the envelope publishes the CREATED issue per key
// rather than anything about the copy itself. What carried over and what
// reset is a decision this CLI makes, not one the server records, and it
// is stated in the command's help rather than encoded here: publishing a
// list of carried fields would be a second place for that policy to live
// and drift.
type IssueCloneOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// SelectorState records a --jql/--filter/--from-file run's own query,
	// absent when the caller named the keys.
	SelectorState
	// Results is one entry per source key, in argv order.
	KeyedResults[IssueCloneData]
}

// IssueCloneData is one source issue's outcome: the clone made from it.
type IssueCloneData struct {
	// Applied records the native HTTP 201 acknowledgement. It can survive a
	// failed row whose response did not supply a usable clone identity.
	Applied bool `json:"applied"`
	// Receipt preserves optional native followups and future response members.
	Receipt json.RawMessage `json:"receipt,omitempty"`
	// Clone is the identity Jira assigned to the new issue, absent on a
	// preview. An IssueRef for IssueCreateOutput.Issue's reason —
	// `.clone.key` reads like every other identity in this contract, and
	// this is the field a caller runs the command to LEARN.
	Clone *IssueRef `json:"clone,omitempty"`
}

// WarningCloneReplaceUnmatched is what `issue clone --replace` publishes
// when the search text appeared in no source summary anywhere in the run.
//
// A warning rather than a refusal, and a RUN-level one rather than per key,
// because clone is keyed: a pattern matching six of ten sources is the
// ordinary case that flag exists for, so only "matched nothing at all" is
// evidence of a typo. Shaped after WarningLinkTypeUnresolved so a caller
// who has met one recognizes the next.
const WarningCloneReplaceUnmatched = "clone_replace_unmatched"

// Rank positions, published so a caller can branch on the direction
// rather than parse it out of prose.
const (
	RankPositionBefore = "before"
	RankPositionAfter  = "after"
)

// IssueRankOutput is the `issue.rank` operation's payload: where the
// named issues were moved to.
//
// The Agile rank endpoint takes the whole ordered list at once,
// because the ordering the caller asked for IS the array's order and
// applying it issue by issue would interleave each move with the last.
//
// The keyed envelope is still the right shape — one entry per key, in
// argv order, so `data.results[]` reads the same here as everywhere —
// but the per-key verdicts come from ONE server response rather than
// from N. Jira answers 204 when every issue moved and 207 with a
// breakdown when some did not.
type IssueRankOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// Anchor is the issue the move was relative to.
	Anchor string `json:"anchor"`
	// Position is "before" or "after" — which side of Anchor the issues
	// landed on. A string rather than a boolean named IsBefore, because
	// the relationship has two named directions in the caller's own
	// vocabulary and a boolean would force every reader to remember which
	// end true meant (IssueLinkView.Direction's own reasoning).
	Position string `json:"position"`
	// Results is one entry per key, in argv order — which is also the
	// order the issues were placed in relative to the anchor.
	KeyedResults[IssueRankData]
}

// IssueRankData is one key's outcome, empty for IssueDeleteData's
// reason: a successful rank answers 204 with no body, and there is
// nothing per issue to report beyond the entry's own ok.
type IssueRankData struct{}

// The overrides carry prose only; the shape derives from the struct.
func init() {
	Register("issue.delete", IssueDeleteOutput{}, map[string]any{
		"description": "Which issues were deleted. Deleting an issue cannot be undone: its comments, attachments and history go with it.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. Nothing was deleted. Local previews contact no Jira service; --validate-remote may resolve selectors through read-only requests, without establishing subtask state.",
			},
			"delete_subtasks": map[string]any{
				"description": "Whether the request told Jira to delete each issue's subtasks too. Jira refuses to delete an issue that has subtasks without this, so it changes whether the request can succeed at all.",
			},
			"results": map[string]any{
				"description": "One entry per resolved key, in input order, retained on total failure, cancellation or timeout. Empty data is a preview or validated native HTTP 204 acknowledgement, never an independent absence check. Data may accompany an error when both were observed. Failed keys have no data without an acknowledgement; successful keys do not erase other failures.",
			},
		},
	})

	Register("issue.clone", IssueCloneOutput{}, map[string]any{
		"description": "The new issue created from each named one. Jira has no copy endpoint, so a clone is a read of the source followed by a create.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. It never creates anything or reads source fields. --validate-remote permits selector/account reads, but cannot report copied fields.",
			},
			"results": map[string]any{
				"description": "One entry per source key in input order, retained on total failure or interruption. data.applied records native HTTP 201; data.clone carries its usable identity. A failed row can retain either acknowledgement or identity. Inspect outcomes before retrying to avoid duplicate clones.",
			},
		},
	})

	Register("issue.rank", IssueRankOutput{}, map[string]any{
		"description": "The authored backlog move of at most 50 distinct issues relative to one different anchor, with ordered outcomes retained on total failure or interruption.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. Nothing was moved, and it never contacts Jira, so it cannot report whether the issues or the anchor exist.",
			},
			"anchor": map[string]any{
				"description": "The issue the move was relative to.",
			},
			"position": map[string]any{
				"description": "Which side of the anchor the issues landed on: before or after.",
			},
			"results": map[string]any{
				"description": "One entry per requested key in input order. One ordered request returns 204 for full acknowledgement or 207 with complete native per-issue verdicts. Unusable attribution reports rank_response_invalid without inventing moves; inspect the backlog before retrying an uncertain write.",
			},
		},
	})
}
