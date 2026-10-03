package envelope

// IssueLinkListOutput is the `issue.link.list` operation's payload: the
// issue links of one or more issues, one keyed result per requested key.
//
// This is the FIRST keyed command in the CLI, so it also sets the
// keyed-result precedent every later `KEY…` command follows: the
// operation object that a single-key command would put at `data` lives
// at `data.results[].data` here, and nothing else about it changes (see
// KeyedResults, keyed.go, for the placement rule and the ordering
// guarantee).
//
// It carries no pagination of its own, and that is a property of the
// resource rather than an omission: Jira has no list-an-issue's-links
// endpoint at all, so links arrive whole inside the issue's own
// `fields.issuelinks` (internal/jira.IssueFields) — there is no page to
// be on.
//
// This was expected to be the unusual case, with `issue attachment
// list` — the next keyed read — settling how pagination folds into a
// keyed result. It turned out to be the pattern: attachments have no
// list endpoint either, arrive the same way, and so publish no page
// state either (see IssueAttachmentListOutput, which records the whole
// argument including why 0.15's `--limit`/`--all` were not carried
// forward). Keyed pagination is therefore still unsettled, and the
// first keyed read whose endpoint genuinely paginates is where it gets
// designed.
type IssueLinkListOutput struct {
	// Joined outcomes also survive total failure, cancellation and timeout.
	// No result claims that a failed read observed a link's current state.
	// KeyedResults is the shared embed, not a hand-declared Results
	// field: `results` must be the same key with the same ordering rule
	// on every keyed command, and declaring it per command is how those
	// drift.
	KeyedResults[IssueLinkListData]
}

// IssueLinkListData is one issue's links — the operation object that
// rides at data.results[].data.
type IssueLinkListData struct {
	// Links is every link on this issue, in the order Jira returned
	// them. An issue with no links carries an empty array, never null: a
	// caller iterating it must not have to special-case the common case.
	Links []IssueLinkView `json:"links"`
}

// IssueLinkView is one link, shaped for the CLI's contract rather than
// the wire's.
//
// The wire (internal/jira.IssueLink) describes a link from OUTSIDE it:
// an inwardIssue and an outwardIssue, exactly one of which is the issue
// you asked about, and a type carrying both directional phrasings. That
// is the right shape for the resource and the wrong one for a caller,
// who has to compare keys to work out which side they are on before the
// link means anything. This view answers it once, at the boundary:
// Direction says which side the OTHER issue sits on, Relation is the
// phrase for reading the link from the asked-about issue toward it, and
// Issue is that other issue. `KEY-123 is blocked by KEY-124` is then a
// direct read of relation + issue.key.
type IssueLinkView struct {
	// ID is the link's own id — the handle `issue link delete` takes.
	ID string `json:"id"`
	// Direction is "inward" or "outward": which side of the link the
	// OTHER issue is on. Not a bool named IsInward, because a link has
	// two named ends in Jira's own vocabulary and a boolean would force
	// every reader to remember which end true meant.
	Direction string `json:"direction"`
	// Relation is the type's phrasing for THIS direction — "blocks" for
	// an outward link, "is blocked by" for an inward one — so a caller
	// renders a link without re-deriving which of the type's two phrases
	// applies. It is Jira-authored text (an admin names link types), so
	// every human surface runs it through a sanitizing sink.
	Relation string `json:"relation"`
	// Type is the link type itself, both phrasings included, for a
	// caller that needs the type rather than this link's reading of it.
	Type IssueLinkTypeView `json:"type"`
	// Issue is the issue at the other end.
	Issue LinkedIssueView `json:"issue"`
}

// IssueLinkTypeView is a link type: its name and both directional
// phrasings, exactly as Jira defines them.
type IssueLinkTypeView struct {
	// ID is the link type's id, omitted when the response did not carry
	// one — absence means unresolved, never a type with an empty id.
	ID string `json:"id,omitempty"`
	// Name is the type's own name ("Blocks", "Relates").
	Name string `json:"name"`
	// Inward and Outward are the phrasings for reading the link from
	// each end ("is blocked by" / "blocks").
	Inward  string `json:"inward"`
	Outward string `json:"outward"`
}

// LinkedIssueView is the issue at the other end of a link: the shared
// identity object plus the handful of fields Jira sends alongside a
// link.
type LinkedIssueView struct {
	IssueRef
	// Fields is the linked issue's own summary/status/type/priority.
	Fields LinkedIssueFields `json:"fields"`
}

// LinkedIssueFields is the closed set of fields a link carries about the
// issue at its other end.
//
// Closed, and typed rather than a passthrough: unlike an issue's own
// `fields` container — tenant-dynamic, custom fields and all, which is
// why IssueViewOutput registers it as a passthrough boundary — the
// linked-issue fields object is fixed by the platform schema itself (the
// spec's "Fields", modeled at internal/jira.Fields). There is nothing
// tenant-defined to preserve here, so these are CLI-shaped snake_case
// fields, not wire idioms riding through.
//
// The wire also carries `assignee` and `timetracking`; both are left out
// deliberately. A link answers what an issue is related to and how, and
// `jira issue view` is the command for the linked issue's own detail —
// adding fields here would grow a second, worse issue view that nothing
// asked for.
type LinkedIssueFields struct {
	// Summary is the linked issue's summary line.
	Summary string `json:"summary"`
	// Status is its status, with the category that decides its color.
	Status LinkedIssueStatus `json:"status"`
	// Type is its issue type.
	Type LinkedIssueType `json:"type"`
	// Priority is its priority.
	Priority LinkedIssuePriority `json:"priority"`
}

// LinkedIssueStatus is a linked issue's status: the name a person reads
// and the category a renderer colors by.
type LinkedIssueStatus struct {
	// Name is the status name, which a project's workflow can spell
	// anything at all ("Ready for Review").
	Name string `json:"name"`
	// Category is Jira's own statusCategory key — new, indeterminate,
	// done, or undefined. Published because it is the only stable signal
	// about a status a caller (or a renderer choosing a color) can branch
	// on: the NAME is per-workflow and unbounded. Empty when the response
	// carried no category.
	Category string `json:"category"`
}

// LinkedIssueType is a linked issue's type.
type LinkedIssueType struct {
	// Name is the type name ("Task", "Bug", a tenant's own).
	Name string `json:"name"`
	// HierarchyLevel is Jira's own level: below 0 for a subtask, 0 for a
	// standard issue, above 0 for an epic and anything a tenant put over
	// it. Published alongside the name for the same reason Status
	// publishes its category — the name is tenant-defined, the level is
	// not.
	HierarchyLevel int64 `json:"hierarchy_level"`
}

// LinkedIssuePriority is a linked issue's priority.
type LinkedIssuePriority struct {
	// Name is the priority name ("Highest" … "Lowest"), empty when the
	// response carried none.
	Name string `json:"name"`
}

// IssueLinkCreateOutput is the `issue.link.create` operation's payload:
// the link made from each named issue to one other.
//
// **The composed request is a SENTENCE, and publishing it that way is
// the point.** Jira's link endpoint takes two ends named inward and
// outward, which is a vocabulary a caller should never have to hold (see
// jira.LinkIssueRequest for what it actually means). This envelope
// publishes what was asked for instead — a relation phrase, a target,
// and the resolved type — so `data.relation` plus `data.to.key` reads
// back as the link that was made, exactly as IssueLinkView.Relation
// reads on the list side.
//
// Everything except Results survives BOTH paths, with one documented
// hole: Type and Relation come from the site's own link-type list, and a
// bare `--dry-run` never reads it. See TypeResolved, which is how a
// preview says so rather than leaving a caller to guess whether an empty
// type meant "unknown" or "none".
type IssueLinkCreateOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// To is the issue at the other end of every link this run makes —
	// one target for N sources, which is why it sits here rather than
	// inside each result.
	To IssueRef `json:"to"`
	// TypeInput is what the caller actually typed for --type, verbatim.
	// Published alongside the resolved Type below rather than replaced by
	// it, because on a preview that could not resolve it this is the only
	// thing there is to report — and on a live run, seeing both is how a
	// caller confirms that "blocks" resolved to the type they meant.
	TypeInput string `json:"type_input"`
	// TypeResolved reports whether the site's link-type list was actually
	// read. False on a bare preview, which is local-only and cannot ask;
	// true on a live run and on `--dry-run --validate-remote`.
	//
	// Serialized unconditionally, because it is what makes an absent Type
	// unambiguous: without it, a caller cannot tell "the type could not
	// be checked" from "the type does not exist".
	TypeResolved bool `json:"type_resolved"`
	// Type is the link type as the site defines it, absent when
	// TypeResolved is false. Reused from the list side deliberately: it
	// is the same object describing the same resource, so an agent that
	// learned it once reads it in both places.
	Type *IssueLinkTypeView `json:"type,omitempty"`
	// Relation is the phrase the link reads with, from each named issue
	// toward To — "blocks", "is blocked by". Absent until the type is
	// resolved, for Type's reason.
	//
	// It is the SAME field IssueLinkView.Relation is on the read side,
	// and that is the contract worth noticing: what this command
	// publishes as the link it made is what `link list` will publish as
	// the link that exists.
	Relation string `json:"relation,omitempty"`
	// Results is one entry per key, in argv order. Joined acknowledgements
	// and failures survive total failure, cancellation and timeout. A failed
	// write can still have stored a link; inspect before retrying.
	KeyedResults[IssueLinkCreateData]
}

// IssueLinkCreateData is one key's outcome, and it is EMPTY on every
// path — the only operation object in this contract that is.
//
// That is the endpoint's doing rather than a gap here: POST
// /rest/api/3/issueLink answers 201 with no body at all, so there is no
// created link, no id, and nothing per key to report beyond the `ok`
// and `issue` the keyed frame already carries. Publishing an empty
// object is the honest shape — the key WAS processed, and what it
// produced is nothing the server was willing to describe.
//
// The consequence worth stating: a caller who needs the new link's id
// has to read it back with `jira issue link list`, because this command
// cannot know it. That is also why `link delete` takes an id rather than
// a sentence.
type IssueLinkCreateData struct{}

// WarningLinkTypeUnresolved is what a LOCAL `issue link create` preview
// publishes in place of the type lookup it deliberately did not perform.
//
// One of three constants that exist for the same standing ruling (design
// §8 item 0), alongside command.MeUnresolvedWarning's own type and
// WarningFilenameUnresolved: a value only the site can resolve is
// published UNRESOLVED by a local preview, with a warning saying so,
// rather than invented or left silent.
const WarningLinkTypeUnresolved = "link_type_unresolved"

// IssueLinkDeleteOutput is the `issue.link.delete` operation's payload:
// which link was removed.
//
// **It carries no issue at all**, and that absence is the contract
// rather than an oversight. A link id is unique across the SITE — DELETE
// /rest/api/3/issueLink/{linkId} takes nothing else — so there is no
// issue this operation is scoped to, and an Identity embed here would
// publish a `data.issue` the command never resolved. Compare
// IssueWeblinkDeleteOutput, which does carry one, because a REMOTE
// link's id is scoped to its issue and the endpoint takes both.
//
// It is the only issue-domain envelope in the CLI with no issue in it,
// which is worth a reader's second look and is exactly why it is spelled
// out here.
type IssueLinkDeleteOutput struct {
	// MutationState carries dry_run on both paths.
	MutationState
	// LinkID is the link that was removed, echoed from the argument — the
	// whole request, since the endpoint takes nothing else.
	LinkID string `json:"link_id"`
}

// The overrides carry prose only; the shape derives from the struct.
func init() {
	Register("issue.link.create", IssueLinkCreateOutput{}, map[string]any{
		"description": "The link made from each named issue to one other, with the relationship it reads as.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. A bare preview never contacts Jira, so it cannot resolve the link type; add --validate-remote to have it check.",
			},
			"to": map[string]any{
				"description": "The issue at the other end of every link this run makes.",
			},
			"type_input": map[string]any{
				"description": "What was typed for --type, verbatim — a type name or either of its directional phrases.",
			},
			"type_resolved": map[string]any{
				"description": "Whether the site's link-type list was read. False on a bare preview, which is what makes an absent type mean 'not checked' rather than 'not found'.",
			},
			"type": map[string]any{
				"description": "The link type as the site defines it, with both directional phrasings. Absent until type_resolved is true.",
			},
			"relation": map[string]any{
				"description": "The phrase each link reads with, from the named issue toward `to` — the same field issue.link.list publishes per link.",
			},
			"results": map[string]any{
				"description": "One entry per key, in the order given. Each successful entry's data is empty: Jira's link endpoint answers 201 with no body, so there is no created link to report. A failed key does not fail the others.",
			},
		},
	})

	Register("issue.link.delete", IssueLinkDeleteOutput{}, map[string]any{
		"description": "Which issue link was removed. A link id is unique across the site, so this operation names no issue.",
		"properties": map[string]any{
			"dry_run": map[string]any{
				"description": "True when this was a preview. Nothing was deleted, and it never contacts Jira, so it cannot report whether the link exists.",
			},
			"link_id": map[string]any{
				"description": "The link that was removed, echoed from the argument. Present on both paths.",
			},
		},
	})

	Register("issue.link.list", IssueLinkListOutput{}, map[string]any{
		"description": "The issue links of one or more issues, one keyed result per requested key.",
		"properties": map[string]any{
			"results": map[string]any{
				"description": "One entry per requested key, in the order requested. Each entry carries ok, the key's identity, and either its links (data) or why it failed (error). A failed key does not fail the command; a command whose keys ALL failed reports the most severe failure and retains this results array in the full failure frame. Cancellation and timeout also retain joined outcomes.",
			},
		},
	})
}

// IssueLinkTypesOutput is the `issue.link.types` operation's payload:
// every link type this site has configured, and where the answer came
// from.
//
// **The only operation in this CLI whose data says whether it contacted
// Jira**, and the reason is that the list is CACHED across invocations
// (see internal/cli/issue/linktypes.go). A caller who cannot tell a fresh
// read from a day-old one cannot reason about why a type they just had an
// admin add is missing — so `cached` and `age_seconds` are part of the
// contract rather than a human-only aside.
//
// It carries no identity slot: link types belong to the SITE, not to an
// issue, so there is no issue key to name. That makes it the first
// issue-tree read that is not issue-scoped.
type IssueLinkTypesOutput struct {
	// Types is every configured link type, in the order Jira returned
	// them — which is the site's own configured order, and the order its
	// UI shows.
	Types []IssueLinkTypeView `json:"types"`
	// Cached reports that this answer came from the local cache rather
	// than the site. Serialized unconditionally: a caller branching on
	// freshness must not have to distinguish false from absent.
	Cached bool `json:"cached"`
	// AgeSeconds is how old the cached answer is, omitted on a fresh read
	// where it would be a meaningless zero.
	//
	// Seconds rather than an RFC 3339 timestamp because the question is
	// "how stale?", and an age answers it without the caller doing date
	// arithmetic against a clock that may not match the one that wrote
	// the entry.
	AgeSeconds int64 `json:"age_seconds,omitempty"`
}

func init() {
	Register("issue.link.types", IssueLinkTypesOutput{}, map[string]any{
		"description": "Every issue link type configured on this site, with both directional phrasings.",
		"properties": map[string]any{
			"types": map[string]any{
				"description": "The configured link types, in the site's own order. Each carries the name and both phrasings; --type on `issue link create` accepts any of the three.",
			},
			"cached": map[string]any{
				"description": "True when the list came from the local cache rather than the site. Use --refresh to force a read.",
			},
			"age_seconds": map[string]any{
				"description": "How old the cached list is, in seconds. Absent on a fresh read.",
			},
		},
	})
}
