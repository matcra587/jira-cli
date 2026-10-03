package envelope

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

// IssueWatchersListData is one issue's watchers — the operation object
// that rides at data.results[].data.
//
// Three members where the two keyed reads before it published one
// array and nothing else, because here the array alone can lie.
//
// Jira returns the `watchers` array only to a caller holding the "View
// voters and watchers" project permission. Everyone else gets the same
// 200, with `watchCount` populated and the array empty or absent — so
// "nobody is watching" and "you may not see who is watching" arrive as
// the same array and are not the same fact. Publishing the count beside
// it makes them distinguishable: watch_count 7 with an empty watchers
// array says precisely what happened, and a caller (or the human
// renderer below it) can say so instead of reporting an empty room.
type IssueWatchersListData struct {
	// Watchers is everyone watching this issue, in the order Jira
	// returned them. An issue with none carries an empty array, never
	// null, exactly as IssueAttachmentListData.Attachments does: a caller
	// iterating data.results[].data.watchers must not have to
	// special-case the common case.
	//
	// It may also be empty because the caller cannot see the list at all
	// — compare WatchCount, which is populated either way. `length` on
	// this array is therefore the number of watchers VISIBLE to the
	// caller, which is not always the number of watchers.
	Watchers []WatcherView `json:"watchers"`
	// WatchCount is how many accounts watch this issue, as Jira reports
	// it — authoritative even when Watchers is empty, which is the whole
	// reason it is published rather than left for a caller to compute
	// with `watchers | length`.
	WatchCount int64 `json:"watch_count"`
	// IsWatching reports whether the ACCOUNT THIS CLI AUTHENTICATED AS
	// watches the issue — not a property of the issue, but of the caller's
	// relationship to it, which is why it sits beside the list rather
	// than inside a WatcherView.
	//
	// Published because it is the one field here a verb acts on: it is
	// exactly the state `issue watchers add` and `issue watchers remove`
	// toggle for the current user, so an agent deciding whether to call
	// either reads this rather than scanning the array for its own
	// account id — which it would first have to know.
	IsWatching bool `json:"is_watching"`
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
// most accounts on most tenants; a field that is usually absent invites
// a caller to depend on the times it is not, and this CLI has no verb
// that takes an email.
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
	// account keeps watching the issues it watched.
	Active bool `json:"active"`
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
	// --user. Request context, so it survives the preview.
	AccountID string `json:"account_id"`
	// Results is one entry per key the caller named, in argv order.
	KeyedResults[IssueWatchersWriteData]
}

// IssueWatchersRemoveOutput is `issue.watchers.remove`'s payload. See
// IssueWatchersAddOutput for why the two are separate types.
type IssueWatchersRemoveOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// AccountID is the Atlassian account being removed, echoed from
	// --user.
	AccountID string `json:"account_id"`
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
				"description": "True when this was a preview. A preview composes the request and stops; it never contacts Jira, so it cannot report whether the issues exist or whether the account is already watching.",
			},
			"account_id": map[string]any{
				"description": "The Atlassian account id being added, echoed from --user. One account for the whole run, which is why it sits here rather than inside each result.",
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
				"description": "True when this was a preview. A preview composes the request and stops; it never contacts Jira, so it cannot report whether the issues exist or whether the account was watching.",
			},
			"account_id": map[string]any{
				"description": "The Atlassian account id being removed, echoed from --user. One account for the whole run, which is why it sits here rather than inside each result.",
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
