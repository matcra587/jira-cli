package envelope

import "encoding/json"

// Comment-delivery values, published because a caller branches on them
// and a typo in one would be a silent contract break.
//
// Two vocabularies, not one, and the split is deliberate. A LIVE run
// reports what happened (CommentPosted*); a PREVIEW reports what would
// happen (CommentPlan*). They share atomic and separate because those
// name the same two routes, and they differ at the end because the ways
// a route can go wrong differ by tense: a live comment can FAIL after
// the transition already landed, and a preview can only foresee a
// REFUSAL that --atomic-only would make before anything is sent.
//
// Folding the two into one field was the obvious alternative and is
// wrong: it would put a past-tense value on a preview that posted
// nothing, which is the exact claim `--dry-run` must never make.
const (
	// CommentPostedAtomic means the comment rode the transition request
	// itself — one request, and the comment cannot exist without the
	// status change having happened.
	CommentPostedAtomic = "atomic"
	// CommentPostedSeparate means the transition landed and the comment
	// was posted afterwards, as a second request. The two are not
	// atomic: a reader of the issue could have seen the new status
	// before the comment appeared.
	CommentPostedSeparate = "separate"
	// CommentPostedFailed means the transition was acknowledged but comment
	// delivery could not be confirmed. Inspect stored comments before retrying.
	CommentPostedFailed = "failed"

	// CommentPlanAtomic means a live run would send one request.
	CommentPlanAtomic = "atomic"
	// CommentPlanSeparate means a live run would send two.
	CommentPlanSeparate = "separate"
	// CommentPlanRefused means a live run would refuse this key:
	// --atomic-only was given and the transition has no screen, so the
	// guarantee the caller asked for cannot be met.
	CommentPlanRefused = "refused"
)

// IssueTransitionOutput is the `issue.transition` operation's payload:
// what each named issue was moved to, and what happened to the comment.
//
// It copies `issue.comment.add`'s layout exactly — the composed request
// at the top because it is one fact for the whole run, the per-key
// outcome in results[] — and adds the one thing that command did not
// have to express: a request whose SHAPE is not known until Jira is
// asked. Which route a comment takes depends on the workflow, so the
// route is per key, in results[], while the comment itself is one
// document composed once and sits up here.
type IssueTransitionOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// SelectorState records a --jql/--filter/--from-file run's own query,
	// absent when the caller named the keys.
	SelectorState
	// Transition is what the caller asked to move to, verbatim as they
	// typed it — a status name, a transition name, or a transition id.
	// Published unresolved on purpose: what each KEY resolved it to can
	// differ (two issues can be on different workflows), so the resolved
	// transition is per key and this is the question that produced them.
	Transition string `json:"transition"`
	// Payload is the locally composed native request. A named target remains
	// unresolved here; each prepared per-key payload carries its workflow ID.
	Payload json.RawMessage `json:"payload,omitempty"`
	// Comment is the ADF document to attach, absent when no comment flag
	// was given. Byte-for-byte as composed, identical on the preview and
	// the live path — `issue.comment.add`'s data.body, under the name
	// this command's flag gives it.
	Comment json.RawMessage `json:"comment,omitempty"`
	// CommentRoute names which INPUT route produced Comment — "adf" for
	// --json-input, "markdown" for --comment/--comment-file. Absent when
	// there is no comment.
	//
	// Not to be confused with results[].data.comment_posted, which is
	// about DELIVERY. This one answers "how did the caller write it", the
	// other answers "how did it reach Jira", and they are independent:
	// an ADF-routed comment can still be posted separately.
	CommentRoute string `json:"comment_route,omitempty"`
	// AtomicOnly reports whether the caller demanded the single-request
	// guarantee. Published on both paths and unconditionally, because it
	// is the flag that turns a fallback into a refusal — a caller reading
	// a `refused` key needs the envelope to say why, without having to
	// remember their own argv.
	AtomicOnly bool `json:"atomic_only"`
	// Resolution and Assignee are the field values to set as part of the
	// transition, absent when unset. Assignee is the resolved account ID,
	// not what the caller typed: `--assignee me` resolves through the
	// authenticated account, and publishing the resolved value is what
	// makes the preview worth reading.
	Resolution string `json:"resolution,omitempty"`
	Assignee   string `json:"assignee,omitempty"`
	// Results is one entry per key, in the order the caller named them.
	KeyedResults[IssueTransitionData]
}

// IssueTransitionData is one key's outcome.
//
// **A key whose COMMENT failed is a successful key.** The status changed
// — that is what the command was asked to do and it happened — so
// results[].ok stays true and comment_posted carries "failed". Reporting
// the key as failed would tell a caller to retry a transition that
// already landed, and on a workflow with several transitions out of one
// status that retry could move the issue somewhere else entirely. The
// failure is not hidden: a warning names the key, and this field says
// which half could not be confirmed.
type IssueTransitionData struct {
	// Payload is the exact prepared native request, retained on write failure.
	// A separate fallback comment is published at the operation's top level.
	Payload json.RawMessage `json:"payload,omitempty"`
	// Attempted means the transition write seam was invoked. It does not prove
	// the transport sent bytes, or that an unacknowledged request was rejected.
	Attempted bool `json:"attempted"`
	// Applied records a native HTTP 204 acknowledgement, independent of a
	// later interruption or comment outcome. It is not a status readback.
	Applied bool `json:"applied"`
	// Transition is the transition resolved for this key, whether or not applied,
	// from what the caller typed. Absent on a preview that could not
	// resolve it — a bare `--dry-run` never asks Jira, so it has no
	// transition list to resolve against.
	Transition *TransitionView `json:"transition,omitempty"`
	// CommentPosted reports how the comment reached Jira: "atomic",
	// "separate" or "failed". Absent when no comment was asked for, and
	// absent on every preview — nothing was posted, so there is nothing
	// for a past-tense field to report.
	CommentPosted string `json:"comment_posted,omitempty"`
	// CommentPlan reports which route a live run WOULD take: "atomic",
	// "separate" or "refused". Present only on a preview that could see
	// the workflow — that is, `--dry-run --validate-remote`. A bare
	// `--dry-run` is local-only and never contacts Jira, so it cannot
	// know, and an absent field is the honest answer rather than a guess.
	CommentPlan string `json:"comment_plan,omitempty"`
	// CommentID is a validated comment identity returned on the SEPARATE path,
	// where the comment is a resource of its own that `comment edit` and
	// `comment delete` can address. It is retained alongside an error and does
	// not prove the stored body. Absent on the atomic path: Jira's 204
	// carries no body, so the comment it created inside the transition
	// has no id this command ever learns.
	CommentID string `json:"comment_id,omitempty"`
	// CommentError is why comment delivery was not confirmed, present when
	// comment_posted is "failed". A plain message rather than a KeyError:
	// the transition itself was acknowledged. Its classification and retry
	// advice are carried separately so callers can inspect uncertain delivery.
	CommentError string `json:"comment_error,omitempty"`
	// CommentErrorCode and CommentErrorHint preserve the shared classification
	// and inspection advice when comment delivery could not be confirmed.
	CommentErrorCode string `json:"comment_error_code,omitempty"`
	CommentErrorHint string `json:"comment_error_hint,omitempty"`
}

// TransitionView is one workflow transition, narrowed to what a caller
// needs to understand what just happened or what would.
//
// HasScreen is published rather than kept as an implementation detail
// because it is the fact that explains everything else in the envelope:
// a caller looking at `comment_posted: "separate"` and wondering why
// gets the answer in the same object.
type TransitionView struct {
	// ID is the transition's own id — the value the POST takes, and the
	// value `--transition` accepts to skip the name lookup entirely.
	ID string `json:"id"`
	// Name is the transition's name, which is often but NOT always the
	// name of the status it leads to.
	Name string `json:"name"`
	// Status is the status the issue lands in, from the transition's own
	// `to`. This is the field a caller usually means by "what did it
	// become".
	Status string `json:"status"`
	// HasScreen reports whether this transition has a screen, and so
	// whether Jira will honor fields and comments sent with it.
	HasScreen bool `json:"has_screen"`
}

// WarningCommentNotAtomic is the warning type raised for every key whose
// comment could not ride the transition request.
//
// It exists as a WARNING rather than as data alone because the envelope's
// data is not the only surface: a person running this at a terminal sees
// stderr, and "your comment was posted, but not atomically" is something
// they should be told without having to ask for --output=json. The
// machine-readable form is still in data.results[].data.comment_posted;
// this is the same fact routed to the reader who did not opt in.
const WarningCommentNotAtomic = "comment_not_atomic"

// WarningCommentFailed is the warning type raised for a key whose
// transition was acknowledged and whose comment was not confirmed. Distinct from
// WarningCommentNotAtomic because the caller's next action differs: a
// non-atomic comment needs nothing done about it, while an uncertain comment
// needs inspection before another write.
const WarningCommentFailed = "comment_failed"

func init() {
	Register("issue.transition", IssueTransitionOutput{}, map[string]any{
		"description": "The workflow move applied to each named issue, and how the comment (if any) reached Jira.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. A bare preview never contacts Jira; --dry-run --validate-remote reads the workflow so it can report which route a comment would take.",
			},
			"transition": map[string]any{
				"description": "What the caller asked to move to, verbatim. What each key resolved it to is in results[].data.transition.",
			},
			"comment": map[string]any{
				"description": "The ADF comment to attach, absent when none was given. Identical on the preview and the live path.",
			},
			"comment_route": map[string]any{
				"description": "Which input route produced the comment: adf (--json-input) or markdown (--comment/--comment-file). About how it was WRITTEN, not how it was delivered.",
			},
			"atomic_only": map[string]any{
				"description": "True when --atomic-only was given, which turns the separate-request fallback into a per-key refusal.",
			},
			"resolution": map[string]any{
				"description": "The resolution set as part of the transition, absent when unset. Honored only where the transition has a screen.",
			},
			"assignee": map[string]any{
				"description": "The resolved account ID assigned as part of the transition, absent when unset. Honored only where the transition has a screen.",
			},
			"results": map[string]any{
				"description": "One ordered entry per key, retained after failure or interruption. applied records a native HTTP 204 acknowledgement; attempted means the write seam was invoked, not that bytes reached Jira. A comment failure preserves the acknowledged move and carries comment_posted=failed with its own classification and inspection advice.",
			},
		},
	})
}
