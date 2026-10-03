package envelope

// A REMOTE link is not an issue link, and this file is not
// outputs_issue_link.go for that reason rather than for tidiness.
//
// An issue link joins two Jira issues with a type an admin configured,
// through /rest/api/3/issueLink. A remote link attaches an arbitrary URL
// — a runbook, a pull request, a dashboard — to ONE issue, through
// /rest/api/3/issue/{key}/remotelink. Different endpoint, different
// resource, different id space, and nothing in one is addressable from
// the other. The CLI spells them `link` and `weblink`, and the two
// vocabularies stay apart everywhere including here.

// IssueWeblinkListOutput is the `issue.weblink.list` operation's
// payload: the remote links of one or more issues, one keyed result per
// requested key.
//
// No pagination, and here the reason is the endpoint's own shape rather
// than the absence of one: GET /rest/api/3/issue/{key}/remotelink
// declares a single optional filter (globalId, which fetches ONE known
// link) and no startAt/maxResults pair, and answers with the whole set.
// That is IssueWatchersListOutput's situation exactly, and its own doc
// comment's warning applies here too — this is a fourth keyed read
// publishing no page, and it is still not a rule about being keyed.
type IssueWeblinkListOutput struct {
	// KeyedResults is the shared embed, so `results` means the same thing
	// and orders the same way as on every other keyed command.
	KeyedResults[IssueWeblinkListData]
}

// IssueWeblinkListData is one issue's remote links — the operation
// object at data.results[].data.
type IssueWeblinkListData struct {
	// Weblinks is every remote link on this issue, in the order Jira
	// returned them. An issue with none carries an empty array, never
	// null, exactly as IssueLinkListData.Links does.
	Weblinks []WeblinkView `json:"weblinks"`
}

// WeblinkView is one remote link, shaped for the CLI's contract.
//
// The wire nests the interesting part one level down — url and title
// live inside an `object` member, alongside icon and status decoration
// this CLI does not render — so this view FLATTENS it. That is a
// deliberate departure from the usual narrowing: `object.url` is the
// whole point of a remote link, and making a caller write
// `.weblinks[].object.url` to reach it would publish the wire's own
// grouping as though it meant something to them.
//
// The icon and status subtrees are dropped rather than flattened
// alongside. They exist so a remote system can decorate its row in
// Jira's web UI — a favicon, a strikethrough for a resolved item — and
// this CLI renders neither, so publishing them would be shape a caller
// has to skip past. AttachmentView drops its `thumbnail` on the same
// test.
type WeblinkView struct {
	// ID is the remote link's own id — the handle `weblink delete` takes.
	// First, for AttachmentView.ID's reason: finding the handle is a
	// reason to run the command.
	//
	// A string here, though the wire types it as an integer, because
	// every other id in this contract is a string and a caller should not
	// have to remember that this one alone needs quoting.
	ID string `json:"id"`
	// URL is the item being linked to.
	URL string `json:"url"`
	// Title is the item's display text, as whoever created the link wrote
	// it. Author-supplied and therefore Jira-authored text — every human
	// surface runs it through a sanitizing sink.
	Title string `json:"title"`
	// Summary is optional prose under the title, empty when the link
	// carries none.
	Summary string `json:"summary,omitempty"`
	// Relationship describes how the issue relates to the item ("causes",
	// "mentioned in"). Free text on this resource, unlike an issue link's
	// type — which is why there is no direction to resolve here and no
	// equivalent of IssueLinkView.Direction.
	Relationship string `json:"relationship,omitempty"`
	// GlobalID is the caller's own identifier for the remote item, when
	// one was set. Published because it is the key the create endpoint
	// de-duplicates on: a caller who wants to REPLACE this link rather
	// than add a second one has to send this back.
	GlobalID string `json:"global_id,omitempty"`
	// Application names the remote system the item lives in, when the
	// link declared one. Empty for the ordinary case of a bare URL.
	Application string `json:"application,omitempty"`
}

// IssueWeblinkAddOutput is the `issue.weblink.add` operation's payload:
// the remote link attached to each named issue.
//
// The composed request lives at the top and the per-key outcome in
// results[], which is IssueCommentAddOutput's split applied unchanged:
// one URL and one title are resolved once and sent to every key, so
// duplicating them into each of N results would be N copies of one fact.
type IssueWeblinkAddOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// URL, Title, Summary and Relationship are the composed request,
	// present on both paths so a preview can be diffed against a live
	// run.
	URL          string `json:"url"`
	Title        string `json:"title"`
	Summary      string `json:"summary,omitempty"`
	Relationship string `json:"relationship,omitempty"`
	// GlobalID is the de-duplication key the caller supplied, absent when
	// they gave none. It is request context rather than an outcome, so it
	// survives the preview — and it is the field that decides whether a
	// live run adds a link or replaces one.
	GlobalID string `json:"global_id,omitempty"`
	// Results is one entry per key, in argv order, retained with the
	// composed request after total failure or interruption.
	KeyedResults[IssueWeblinkAddData]
}

// IssueWeblinkAddData is one key's outcome.
type IssueWeblinkAddData struct {
	// Weblink identifies the link Jira created or replaced, absent on a
	// preview. Unusable acknowledgements fail without inventing an identity.
	//
	// It is NOT a WeblinkView: the create response carries an id and a
	// self URL and nothing else — no title, no url echoed back — so
	// publishing the full view shape would promise fields this position
	// can never fill. A caller who wants the whole link reads it back
	// with `weblink list`.
	Weblink *WeblinkRef `json:"weblink,omitempty"`
	// Created reports whether a NEW link was added (true) or an existing
	// one with the same global_id was replaced (false).
	//
	// Serialized unconditionally on a live key, because "added" and
	// "replaced" are different things to have done and a caller
	// scripting against this must be able to tell them apart. Jira says
	// which only through its status code — 201 against 200 — so this
	// field is the only place the distinction survives into the envelope.
	// Absent on a preview, where nothing happened to report.
	Created *bool `json:"created,omitempty"`
}

// WeblinkRef identifies a remote link without describing it: what the
// create response actually carries.
type WeblinkRef struct {
	ID   string `json:"id"`
	Self string `json:"self,omitempty"`
}

// IssueWeblinkDeleteOutput is the `issue.weblink.delete` operation's
// payload: which remote link was removed from which issue.
//
// Shaped exactly like IssueCommentDeleteOutput and for its reasons: no
// `deleted` boolean (dry_run already answers it), no removed object (the
// 204 carries none), and the request context survives both paths.
//
// Unlike a deleted comment, this one is genuinely recoverable — a remote
// link is a URL and a title, and `weblink add` puts it back — which is
// why this command takes no --force where `comment delete` does.
type IssueWeblinkDeleteOutput struct {
	// Identity places the issue at data.issue.
	Identity
	// MutationState carries dry_run on both paths.
	MutationState
	// WeblinkID is the remote link that was removed, echoed from the
	// argument.
	WeblinkID string `json:"weblink_id"`
}

// The overrides carry prose only; the shape derives from the struct.
func init() {
	Register("issue.weblink.list", IssueWeblinkListOutput{}, map[string]any{
		"description": "The remote links (arbitrary URLs attached to an issue) of one or more issues, one keyed result per requested key. Not issue links — see issue.link.list for those.",
		"properties": map[string]any{
			"results": map[string]any{
				"description": "One entry per requested key, in the order requested. Each entry carries ok, the key's identity, and either its weblinks (data) or why it failed (error). Partial success succeeds; total failure returns an error. Total failure and interruption retain ordered outcomes. A valid native list has distinct positive numeric identities; unusable responses report weblink_response_invalid.",
			},
		},
	})

	Register("issue.weblink.add", IssueWeblinkAddOutput{}, map[string]any{
		"description": "The remote link attached to each named issue, with the request that produced it.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. A preview composes the request and stops; it never contacts Jira, so it cannot report whether the issues exist.",
			},
			"url": map[string]any{
				"description": "The absolute HTTP or HTTPS URL being linked to. Identical on both paths; local validation does not fetch it.",
			},
			"title": map[string]any{
				"description": "The effective display text, defaulting to the URL when --title is omitted or empty. Identical on preview and live paths.",
			},
			"global_id": map[string]any{
				"description": "The caller's own de-duplication key, when given. A link whose global_id already exists on the issue is replaced; results[].data.created reports which happened. Replacement clears omitted summary and relationship fields.",
			},
			"results": map[string]any{
				"description": "One entry per key, in the order given. A key that failed carries error in place of data and does not fail the others. Partial success succeeds; total failure returns an error. Total failure and interruption retain the composed request and ordered outcomes. Unusable acknowledgements report weblink_response_invalid without inventing an identity; inspect stored links before retrying because Jira may have applied the write.",
			},
		},
	})

	Register("issue.weblink.delete", IssueWeblinkDeleteOutput{}, map[string]any{
		"description": "Which remote link was removed from which issue. Reversible with `jira issue weblink add`, unlike a deleted comment.",
		"properties": map[string]any{
			"issue": map[string]any{
				"description": "The issue the link was removed from.",
			},
			"dry_run": map[string]any{
				"description": "True when this was a preview. Nothing was deleted, and it never contacts Jira, so it cannot report whether the link exists.",
			},
			"weblink_id": map[string]any{
				"description": "The remote link that was removed, echoed from the argument. Present on both paths.",
			},
		},
	})
}
