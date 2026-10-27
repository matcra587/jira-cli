package envelope

import "github.com/matcra587/jira-cli/internal/jira"

// IssueWatchersListOutput is the `issue.watchers.list` operation's
// payload: the watchers of one or more issues, one keyed result per
// requested key.
//
// It carries no pagination. That now makes three keyed reads in a row
// that publish none, and the three arrived there by three DIFFERENT
// routes — which is the thing to read off this comment, because the
// pattern is about to look like a rule and is not one:
//
//   - `issue link list` — no endpoint exists. An issue's links arrive
//     folded into the issue read, so the question never came up.
//   - `issue attachment list` — no endpoint exists either, but here a
//     predecessor HAD shipped `--limit`/`--all`. They were not
//     pagination: the whole array had already crossed the wire, and the
//     flags sliced it in memory, hiding attachment 51 onward by
//     default. Removing them was a decision, not an omission (see
//     IssueAttachmentListOutput for the full argument).
//   - `issue watchers list` — an endpoint DOES exist: GET
//     /rest/api/3/issue/{issueIdOrKey}/watchers, `getIssueWatchers`, a
//     real request of its own rather than a field of the issue read. It
//     simply does not paginate. The operation declares exactly one
//     parameter, the issue key, and the `Watchers` schema it answers
//     with carries no startAt, maxResults, total, isLast or cursor. The
//     whole set arrives at once.
//
// So do NOT generalize this into "keyed reads do not paginate". Nothing
// about being keyed says anything about paging; these three commands
// each read a resource that happens to have no page, and the next keyed
// read may well have one. A `comment list` conversion is the standing
// example — GET .../issue/{key}/comment takes a real startAt/maxResults
// pair — and the rule it inherits is already written down: the page
// block folds inside the per-key operation object at
// `data.results[].data`, never in meta (.claude/rules/output.md, and
// tests/guardrails/pagination_placement_test.go).
//
// The value of stating the route rather than just the outcome: 0.15's
// `watchers list` also took no paging flags, and it happened to be
// right for a reason nobody had checked. A page block here would assert
// a page the resource does not have — the same objection
// IssueAttachmentListOutput raises against a `--limit` that only slices
// an already-complete array.
type IssueWatchersListOutput struct {
	// KeyedResults is the shared embed, not a hand-declared Results
	// field: `results` must be the same key with the same ordering rule
	// on every keyed command, and declaring it per command is how those
	// drift.
	KeyedResults[IssueWatchersListData]
}

// IssueWatchersListData preserves unavailable, null and observed watcher facts.
// A positive count can accompany an unavailable list when visibility is restricted.
type IssueWatchersListData struct {
	// Watchers retains the returned array. Absence and null do not mean empty.
	Watchers jira.Nullable[[]WatcherView] `json:"watchers,omitzero"`
	// WatchCount is the returned total, which may exceed the visible list.
	WatchCount jira.Nullable[int64] `json:"watch_count,omitzero"`
	// IsWatching is the authenticated account's returned membership state.
	IsWatching jira.Nullable[bool] `json:"is_watching,omitzero"`
}

// WatcherView is one watcher, shaped for the CLI's contract.
//
// Not a reuse of AuthorRef, which carries the same first two members.
// AuthorRef is deliberately narrow — its own doc comment says it drops
// `active` and `accountType` because they "have nothing to do with
// authorship" — and that argument inverts here. The question this
// command answers is who is watching, and for that:
//
//   - Active separates a live watcher from a deactivated account that
//     still sits on the issue. A notification list quietly padded with
//     departed colleagues is the ordinary reason somebody runs this
//     command, and it cannot be seen from a display name.
//   - AccountType ('atlassian', 'app', 'customer') separates people from
//     the integrations that add themselves as watchers. A caller asking
//     "who will this notify" means humans; an app in the list is noise
//     they can now filter rather than mistake for a colleague.
//
// Both are cheap — they ride in the same UserDetails the array already
// carries — and both change what a caller does with a row, which is the
// test AttachmentView's dropped `thumbnail` fails.
//
// EmailAddress is NOT published, though the wire may carry it. Jira
// gates it behind the watcher's own privacy settings, so it is null for
// most accounts on most tenants. Explicit user-query lookup can match an
// email but does not verify its equality; watcher identity remains the ID.
type WatcherView struct {
	// AccountID is the account's stable Atlassian id — the handle `issue
	// watchers add` and `issue watchers remove` both take. First, for the
	// same reason AttachmentView.ID leads: finding the handle is a
	// reason to run the command.
	AccountID string `json:"account_id"`
	// DisplayName is the name Jira shows for the account, subject to the
	// watcher's own privacy settings. Jira-authored text, so every human
	// surface runs it through a sanitizing sink.
	DisplayName string `json:"display_name"`
	// Active reports whether the account is enabled. A deactivated
	// account keeps watching the issues it watched. Absence is not false.
	Active jira.Nullable[bool] `json:"active,omitzero"`
	// AccountType is Jira's own classification of the account —
	// 'atlassian' for an ordinary user, 'app' for an integration,
	// 'customer' for a Service Desk customer. Empty when the response
	// carried none, which the privacy-restricted user records described
	// in the spec's own UserDetails notes can produce.
	AccountType string `json:"account_type"`
}

// IssueWatchersAddOutput is the `issue.watchers.add` operation's
// payload, and IssueWatchersRemoveOutput below is its mirror.
//
// Two types rather than one shared "watchers write", even though the
// fields are identical. The envelope registry is keyed by operation and
// publishes each operation's own schema, so a shared type would give the
// two verbs one schema entry to describe two different acts — and the
// prose that makes a schema worth reading ("who now watches", "who no
// longer watches") is exactly what could not then be said. The cost is a
// duplicated field list; the alternative costs the description.
//
// AccountID sits at the TOP, not per key, and that placement follows
// IssueCommentAddOutput's rule precisely: one account is resolved once
// and applied to every key, so putting it inside each of N results would
// be N copies of one fact. What genuinely differs per key is the watcher
// set afterwards, and that is the only thing in results[].
type IssueWatchersAddOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// AccountID is the Atlassian account being added, echoed from
	// --user or resolved once from --user-query. Local query previews leave it
	// empty; authored query intent is retained separately.
	AccountID string `json:"account_id"`
	// UserQuery preserves an authored lookup query; an unresolved local preview has an empty AccountID.
	UserQuery string `json:"user_query,omitempty"`
	// Results is one entry per key the caller named, in argv order.
	KeyedResults[IssueWatchersWriteData]
}

// IssueWatchersRemoveOutput is `issue.watchers.remove`'s payload. See
// IssueWatchersAddOutput for why the two are separate types.
type IssueWatchersRemoveOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// AccountID is the Atlassian account being removed, echoed from
	// --user or resolved once from --user-query. Local query previews leave it empty.
	AccountID string `json:"account_id"`
	// UserQuery preserves an authored lookup query; an unresolved local preview has an empty AccountID.
	UserQuery string `json:"user_query,omitempty"`
	// Results is one entry per key the caller named, in argv order.
	KeyedResults[IssueWatchersWriteData]
}

// IssueWatchersWriteData is one key's outcome for either watcher verb —
// the operation object at data.results[].data.
//
// ONE type for both verbs, where the Output structs above are two, and
// the asymmetry is deliberate rather than an inconsistency. Those are
// two because the SCHEMA describes two different acts; this is one
// because the per-key payload genuinely is the same object — the issue's
// watcher set as it stands after the write — and a second copy of it
// would be a second thing to keep in agreement for no reader's benefit.
type IssueWatchersWriteData struct {
	// Readback is the issue's whole watcher set after the write, or
	// absent when there is none to report.
	//
	// It is IssueWatchersListData — the SAME object `issue watchers list`
	// publishes per key — and reusing it is the point rather than a
	// shortcut. It is the same resource read from the same endpoint, so
	// an agent that learned `{watchers, watch_count, is_watching}` once
	// reads it here without learning a second spelling of it, and the
	// permission subtlety that type documents at length (an empty array
	// beside a non-zero count means the caller may not see WHO) is true
	// here for the identical reason.
	//
	// Absent when no state was observed:
	//
	//   - a preview, which contacted nobody and so knows nothing about
	//     the watcher set either before or after;
	//   - --no-readback, where the write DID happen and the caller
	//     declined to pay for the extra GET that would describe it.
	//   - a failed post-write read, reported by ReadbackError.
	//
	// dry_run and ReadbackError distinguish these cases. The write itself
	// answers 204 with no body (see core.JiraClient.AddWatcher), so this
	// is never free — unlike `comment add`, where the created object
	// rides the write's own response and is therefore published
	// unconditionally.
	Readback *IssueWatchersListData `json:"readback,omitempty"`
	// ReadbackError reports a failed post-write read without changing the
	// acknowledged write's success. A preview or --no-readback omits both.
	ReadbackError *KeyError `json:"readback_error,omitempty"`
}

// The overrides carry prose only; the shape derives from the struct.
func init() {
	Register("issue.watchers.add", IssueWatchersAddOutput{}, map[string]any{
		"description": "The account added as a watcher on each named issue, with the resulting watcher set where one was read back.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. Bare previews perform no Jira lookup; --validate-remote permits @me/query resolution and exact active literal-account validation without writing. A preview cannot report issue existence or watcher state.",
			},
			"account_id": map[string]any{
				"description": "The account being added, literal from --user or resolved once from --user-query/@me. Empty for an unresolved local query preview; unresolved @me retains its sentinel or unverified cached identity. Explicit --validate-remote checks literal IDs against one exact active native account; no lookup proves issue existence or watcher permission. One account applies to all results.",
			},
			"user_query": map[string]any{
				"description": "Authored --user-query spelling, retained independently of the resolved account ID. A unique visible active query match does not verify exact name/email equality or watcher permission. Omitted for literal --user.",
			},
			"results": map[string]any{
				"description": "One entry per key in input order. Total failure, cancellation and timeout retain joined outcomes and account context. Acknowledged writes stay successful when a post-write read fails; data.readback_error reports that failure without inventing watcher state. Previews and --no-readback omit both readback fields.",
			},
		},
	})

	Register("issue.watchers.remove", IssueWatchersRemoveOutput{}, map[string]any{
		"description": "The account removed as a watcher from each named issue, with the resulting watcher set where one was read back.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. Bare previews perform no Jira lookup; --validate-remote permits @me/query resolution and exact active literal-account validation without writing. A preview cannot report issue existence or watcher state.",
			},
			"account_id": map[string]any{
				"description": "The account being removed, literal from --user or resolved once from --user-query/@me. Empty for an unresolved local query preview; unresolved @me retains its sentinel or unverified cached identity. Explicit --validate-remote checks literal IDs against one exact active native account; no lookup proves issue existence or watcher permission. One account applies to all results.",
			},
			"user_query": map[string]any{
				"description": "Authored --user-query spelling, retained independently of the resolved account ID. A unique visible active query match does not verify exact name/email equality or watcher permission. Omitted for literal --user.",
			},
			"results": map[string]any{
				"description": "One entry per key in input order. Total failure, cancellation and timeout retain joined outcomes and account context. Acknowledged writes stay successful when a post-write read fails; data.readback_error reports that failure without inventing watcher state. Previews and --no-readback omit both readback fields.",
			},
		},
	})

	Register("issue.watchers.list", IssueWatchersListOutput{}, map[string]any{
		"description": "The watchers of one or more issues, one keyed result per requested key.",
		"properties": map[string]any{
			"results": map[string]any{
				"description": "One entry per requested key in input order, with its identity and either watcher data or a classified error. Total failure retains the array with the most severe original error. Cancellation and timeout retain every joined outcome, including completed reads, in the failure frame.",
			},
		},
	})
}
