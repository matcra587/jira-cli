package core

import (
	"context"
	"errors"
	"time"

	"github.com/matcra587/jira-cli/internal/jira"
)

// The interfaces below are consumer-side seams: core declares only what its
// operations need, adapters implement them, and every seam carries a
// compile-time assertion at its implementation. They stay at 1–3 methods
// and grow per command, never ahead of one.

// JiraClient is the seam to the Jira REST API.
//
// Read-path only until AddComment below, which is the first write. The
// seam does not split into a separate writer interface: a command that
// writes almost always reads first (resolving a key, echoing a result),
// so two interfaces would mean every such command taking both, and the
// read-only enforcement lives at the transport (adapters/jira's
// newRequest) rather than in the shape of this type — a narrower
// interface would not make a write any harder to reach.
type JiraClient interface {
	// ParseJQL validates explicit queries in the current account's context.
	// Query diagnostics are result data; transport failures remain errors.
	ParseJQL(ctx context.Context, queries []string, mode JQLValidationMode) (JQLParseRead, error)

	// GetJQLReference reads the site's visible field/function metadata. It
	// does not assert that the account can see every configured field.
	GetJQLReference(ctx context.Context) (JQLReferenceRead, error)

	// SubmitIssueMove submits one explicit native bulk move. Acknowledgement
	// retains Jira's 201 even when its task receipt is unusable; it does not
	// establish completion or authorize replaying the write.
	SubmitIssueMove(ctx context.Context, request jira.IssueBulkMovePayload) (IssueMoveSubmission, error)

	// GetBulkOperationProgress reads one task observation. Commands own any
	// bounded polling; a completed task can include failed/inaccessible issues.
	GetBulkOperationProgress(ctx context.Context, taskID string) (BulkOperationRead, error)

	// GetWorklogs fetches exactly one offset page. Callers own the walk;
	// missing paging facts or invalid worklog identities are errors.
	GetWorklogs(ctx context.Context, key string, startAt, maxResults int) (WorklogRead, error)

	// AddWorklog posts once and returns Jira's created record. Acknowledged
	// remains true if Jira returned 201 but its response was malformed;
	// inspect existing worklogs before retrying any uncertain write.
	AddWorklog(ctx context.Context, key string, req jira.AddWorklogRequest, notifyUsers bool) (WorklogWrite, error)

	// DoAPI sends one explicit platform or Agile request through the same auth,
	// scope, read-only, timeout and safe-retry owners as domain operations. A
	// non-nil response alongside an error is an inspectable HTTP receipt.
	DoAPI(ctx context.Context, request APIRequest) (*APIResponse, error)

	// BaseURL returns the site root this client was constructed with
	// (already validated: scheme+host, no embedded credentials — see
	// adapters/jira.New). Not an API operation like the methods below —
	// it is metadata about how the client itself was configured — but it
	// grows the seam for the same reason those did: a genuine caller
	// need. The human render layer (internal/cli.IssueKey) uses it to
	// build a Jira browse link for an already-resolved issue key WITHOUT
	// a second config/keyring resolution: the client this seam wraps has
	// already paid that cost once, so exposing what it resolved to is
	// cheaper and more honest than asking config a second time from a
	// renderer that has no business reaching config directly.
	BaseURL() string

	// GetIssue fetches one issue by key. fields narrows the response to
	// the named field IDs; nil requests the server's default set. Optional
	// expand names request native metadata through the same read.
	GetIssue(ctx context.Context, key string, fields []string, expand ...string) (IssueRead, error)

	// GetComments fetches one page of an issue's comments. startAt and
	// maxResults are the server's own offset-pagination pair — the
	// caller decides the page, this seam never walks every page itself,
	// so a huge thread cannot exhaust memory the way fetch-then-truncate
	// would.
	//
	// order picks the direction; its zero value is newest-first, which
	// is what every caller wanted before the parameter existed and still
	// is the default.
	GetComments(ctx context.Context, key string, startAt, maxResults int, order CommentOrder) (CommentRead, error)

	// GetWatchers fetches everyone watching one issue.
	//
	// It takes no paging pair, and that is the endpoint's own shape
	// rather than a simplification made here: GET
	// /rest/api/3/issue/{issueIdOrKey}/watchers declares exactly one
	// parameter, the key itself, and its response carries no startAt,
	// maxResults, total or cursor to resume from. The whole set arrives
	// at once, so — unlike GetComments and SearchIssues, whose doc
	// comments above explain at length why the CALLER must own the page
	// — there is no page for a caller to own.
	//
	// A separate seam rather than a field folded onto IssueRead,
	// because it is a separate round trip: an issue's `watches` field
	// carries the COUNT and whether the caller watches, but never the
	// list of who, so a command that wants names has to pay for this
	// call and should be able to see that it is paying.
	GetWatchers(ctx context.Context, key string) (WatcherRead, error)

	// CurrentUser returns the account the credentials authenticate as.
	// It is the cheapest call that proves a credential actually works —
	// presence of a stored token proves nothing once one is revoked.
	CurrentUser(ctx context.Context) (UserRead, error)

	// GetUser validates one exact active native account by its ID.
	// It does not establish issue existence, watcher permission or assignability.
	GetUser(ctx context.Context, accountID string) (UserRead, error)

	// SearchUsers returns one offset page of filtered user matches. Jira's
	// response is a bare array, with no total or final-page flag, and this
	// endpoint exposes at most the first 1000 matches. The caller owns paging.
	SearchUsers(ctx context.Context, query string, startAt, maxResults int) (UserSearchRead, error)

	// SearchIssues runs one JQL query and returns one page of matching
	// issues. fields narrows each issue's fields exactly like GetIssue;
	// nextPageToken continues a prior call's page, and "" asks for the
	// first page. Optional expand names apply to every requested page.
	//
	// This seam never walks every page itself, for the same reason
	// GetComments never walks a whole thread: the caller decides the
	// page, so a JQL match running into the tens of thousands cannot
	// exhaust memory the way fetch-then-truncate would. It matters more
	// here than for GetComments, not less — this endpoint rides CURSOR
	// pagination (a nextPageToken, not an offset/total pair; see
	// jira.SearchResults), and a cursor cannot be resumed from a
	// remembered position the way an offset can. An internal "walk
	// everything" loop would have to hold every page's issues in memory
	// at once to hand back anything, which is precisely the failure
	// mode a page-at-a-time contract exists to avoid.
	SearchIssues(ctx context.Context, jql string, fields []string, nextPageToken string, maxResults int, expand ...string) (IssueSearchRead, error)

	// CountIssues returns an ESTIMATED count of jql's matches, from the
	// dedicated /rest/api/3/search/approximate-count endpoint — the ONLY
	// place a count can come from at all: SearchIssues' own response
	// (jira.SearchResults) carries no total, cursor pagination having
	// nothing to compute one from (see that type's own doc comment). It
	// is a SEPARATE seam, not a field folded onto IssueSearchRead,
	// because it is a SEPARATE network round trip with its own cost — a
	// caller must decide when that cost is worth paying (issue list's
	// own gating rule fires it only for a human render with more results
	// still to fetch) rather than getting it bundled into every search
	// whether asked for or not.
	//
	// The estimate qualifier is load-bearing, not decoration: the
	// endpoint's own documentation says so verbatim ("Provide an
	// estimated count ... Recent updates might not be immediately
	// visible in the returned output"), so a caller rendering this to a
	// human MUST mark it as approximate (the `~` prefix issue list's own
	// hint uses) rather than presenting it as an exact total. jql must
	// be BOUNDED (a search restriction) — the endpoint's own
	// requirement, refused by Jira itself rather than validated here.
	CountIssues(ctx context.Context, jql string) (IssueCountRead, error)

	// GetFilter reads one saved filter by id, for the JQL it holds.
	//
	// It exists for the `--filter` bulk selector and reads exactly one
	// property of the response (jira.Filter.Jql). A saved filter is
	// server-side state the CLI never caches: it can be edited by anyone
	// it is shared with, so the query a filter meant yesterday is not the
	// query it means now, and the write commands that take this selector
	// publish the resolved JQL for exactly that reason.
	//
	// id is a filter ID and never a NAME. Jira does not require filter
	// names to be unique — two people can each own and share one called
	// "My bugs" — so a name cannot identify a filter unambiguously, and
	// the commands that take this selector are the ones where an
	// ambiguous target set is least affordable.
	//
	// **A filter the caller cannot see and a filter that does not exist
	// answer identically**, both 404, and that is Jira's own deliberate
	// behavior rather than a gap here: distinguishing them would let
	// anyone enumerate the existence of private filters. A caller-facing
	// message must therefore not claim the filter is absent.
	GetFilter(ctx context.Context, id string) (FilterRead, error)

	// GetBoards returns one page of Agile boards, optionally narrowed by
	// name.
	//
	// **The name filter is a PARTIAL match, in the endpoint's own words:
	// "boards that match or partially match the specified name".** So
	// asking for "Sprint" can come back with "Sprint Board", "Old Sprint
	// Board" and "Sprint 2024" — and board names are not unique across a
	// site in the first place. A caller resolving a name to a single
	// board must therefore decide what to do about several matches
	// rather than taking the first; the command layer refuses and lists
	// them.
	//
	// One page, never a drain, for SearchIssues' reason. maxResults
	// bounds it and the response reports isLast, so a caller can tell
	// "these are all the matches" from "there are more" — which is the
	// difference between a safe pick and a wrong one.
	GetBoards(ctx context.Context, name string, startAt, maxResults int) (BoardRead, error)

	// GetBoardProjects returns one offset page of projects associated with the
	// board's filter or issues. Empty means no visible associated projects;
	// callers must not substitute a board's location project for this relation.
	GetBoardProjects(ctx context.Context, boardID int64, startAt, maxResults int) (BoardProjectRead, error)

	// GetBoardConfiguration returns the board's configuration, for the id
	// of the saved filter behind it.
	//
	// A board is that filter's query plus a column layout, so this is how
	// `--board` becomes JQL: board -> configuration -> filter id ->
	// GetFilter -> query. Three round trips on a cold cache, which is why
	// the result is cached.
	GetBoardConfiguration(ctx context.Context, boardID int64) (BoardConfigurationRead, error)

	// AddComment posts one comment to an issue and returns the comment
	// Jira created.
	//
	// THE FIRST WRITE ON THIS SEAM. Three properties of it are contract
	// rather than implementation detail, because the writes that follow
	// will copy them:
	//
	// It returns the created object. Jira's 201 carries the whole
	// comment — id, author, timestamps, stored body — so the caller gets
	// the readback for free, with no second round trip to gate behind a
	// flag. A write whose response carries the created object should
	// publish it; one that does not is where a --verify flag earns its
	// place.
	//
	// It is never retried. The transport's safeToRetry and its
	// read-only check are the same function (adapters/jira's isRead), so
	// this POST is automatically both blocked under read-only mode and
	// excluded from automatic replay. That is not a nicety: replaying a
	// comment POST after a timeout would post it twice, and the caller
	// would have no way to tell.
	//
	// It takes the composed request, not the pieces. The body arrives as
	// a fully-formed jira.AddCommentRequest so that composing the payload
	// and sending it are separate steps — which is what lets --dry-run
	// compose exactly what a live run would send and then stop, rather
	// than previewing a reconstruction of it.
	AddComment(ctx context.Context, key string, req jira.AddCommentRequest) (CommentWrite, error)

	// UpdateComment replaces one comment's body and returns the comment
	// as Jira stored it.
	//
	// It REPLACES rather than patches, which is the endpoint's own
	// semantics and the reason the request type carries a body on every
	// call: a PUT omitting the body would store a comment with none.
	// UpdateCommentRequest's own doc comment records the third state that
	// falls out of the same fact — an absent visibility leaves the
	// current restriction alone, so removing one needs an explicit null.
	//
	// Like AddComment it returns the stored object, so the readback is
	// free and needs no --verify to gate it. Unlike AddComment it answers
	// 200 rather than 201: nothing was created.
	UpdateComment(ctx context.Context, key, commentID string, req jira.UpdateCommentRequest, notifyUsers bool) (CommentWrite, error)

	// DeleteComment removes one comment from an issue.
	//
	// THE FIRST DESTRUCTIVE OPERATION ON THIS SEAM, and the only thing
	// that makes it different from the writes above is what a caller can
	// do about a mistake: nothing. Jira keeps no trash for comments, so a
	// deleted one is not recoverable through this API or any other. The
	// confirmation that guards it therefore lives in the COMMAND (see
	// internal/cli/issue/comment_delete.go) rather than here — a seam
	// that prompted would be unusable from a test and from an agent
	// alike.
	//
	// It is never retried, for AddComment's reason turned around: the
	// transport classifies a DELETE as a write, so it is both blocked
	// under read-only mode and excluded from replay. Replay would be
	// harmless here in one sense — deleting an already-deleted comment is
	// a 404, not a second deletion — but a 404 arriving from a replayed
	// success would be reported as a failure of a call that worked.
	DeleteComment(ctx context.Context, key, commentID string) (CommentDeleteWrite, error)

	// AddWatcher makes one account watch an issue.
	//
	// The account id rides as the whole request BODY — a bare JSON
	// string, which is genuinely what POST
	// /rest/api/3/issue/{key}/watchers declares (its requestBody schema is
	// `{"type": "string"}`, not an object). Passing an object with an
	// accountId field, the shape every neighboring endpoint takes, is
	// rejected.
	//
	// It answers 204 with no body, so there is no resulting watcher set
	// to publish for free: a command that wants to report one pays for a
	// GetWatchers of its own, which is exactly the extra round trip
	// --no-readback exists to decline.
	//
	// Adding an account that already watches the issue succeeds. That is
	// Jira's own behavior and the right one to pass through — the verb
	// describes an END STATE, so a caller re-running it gets the state
	// they asked for rather than an error about how it got there.
	AddWatcher(ctx context.Context, key, accountID string) (WatcherWrite, error)

	// RemoveWatcher stops one account watching an issue.
	//
	// The account id rides as a QUERY parameter here, not as the body —
	// DELETE /rest/api/3/issue/{key}/watchers?accountId=… — which is an
	// asymmetry in Jira's own API rather than a choice made here, and the
	// reason these two are separate methods rather than one taking a
	// boolean.
	//
	// Removing an account that was not watching succeeds, for AddWatcher's
	// reason: the verb names an end state.
	RemoveWatcher(ctx context.Context, key, accountID string) (WatcherWrite, error)

	// GetIssueLinkTypes lists every issue link type configured on the
	// site.
	//
	// A READ that a write depends on, like GetTransitions below, and for a
	// closely related reason: the link endpoint takes a type plus a
	// DIRECTION, and neither is knowable from what a caller typed. A type
	// carries two phrasings — "blocks" one way, "is blocked by" the other
	// — and only this list says which phrase belongs to which side, so a
	// CLI that lets somebody write the relationship as a sentence has to
	// read it first.
	//
	// SITE-WIDE, not per issue, which is what makes it cheap in a fan-out:
	// one read answers for every key, so a keyed `link create` pays for it
	// once rather than once per key.
	GetIssueLinkTypes(ctx context.Context) (LinkTypeRead, error)

	// LinkIssues joins two issues with a typed, directional link.
	//
	// It answers 201 with an EMPTY body — it creates a resource and
	// declines to identify it — so unlike AddComment there is no created
	// object to publish and no free readback. The new link's id is
	// therefore not knowable from this call at all; `issue link list` is
	// where an id comes from.
	//
	// The request's two issue slots are ENDS, not source and target: the
	// link reads inwardIssue <outward-phrase> outwardIssue. See
	// jira.LinkIssueRequest, which owns that explanation and records how
	// it was derived.
	LinkIssues(ctx context.Context, req jira.LinkIssueRequest) (LinkWrite, error)

	// DeleteIssueLink removes one issue link by id.
	//
	// It takes NO issue key, and that is the endpoint's own shape rather
	// than a simplification: DELETE /rest/api/3/issueLink/{linkId}
	// declares exactly one parameter, and a link id is unique across the
	// site rather than scoped to an issue. The command above it therefore
	// takes an id alone — asking for a key it could not send would be a
	// positional the CLI ignores.
	DeleteIssueLink(ctx context.Context, linkID string) (LinkDeleteWrite, error)

	// GetRemoteLinks lists one issue's REMOTE links — arbitrary URLs
	// attached to an issue, which are a different resource from the issue
	// links above and reached through a different endpoint. The CLI keeps
	// them apart as `weblink` and `link`.
	//
	// It takes no paging pair, for GetWatchers' reason: the operation
	// declares one optional filter (globalId) and no startAt/maxResults,
	// and answers with the whole set.
	GetRemoteLinks(ctx context.Context, key string) (RemoteLinkRead, error)

	// AddRemoteLink attaches a URL to an issue, or REPLACES the one
	// already carrying the same globalId.
	//
	// Create-or-update is the endpoint's own semantics and cannot be
	// opted out of: a POST whose globalId matches an existing link on
	// that issue replaces it and answers 200, where a new one answers
	// 201. The result reports which happened (RemoteLinkWrite.Created),
	// because "added" and "replaced" are different things to have done
	// and the status code is the only place Jira says so.
	AddRemoteLink(ctx context.Context, key string, req jira.RemoteLinkRequest) (RemoteLinkWrite, error)

	// DeleteRemoteLink removes one remote link from an issue.
	//
	// It takes the issue key AND the link id, which is the opposite of
	// DeleteIssueLink above and worth the contrast: a remote link's id is
	// scoped to the issue it hangs off, so both are genuinely needed here
	// while only the id is there.
	DeleteRemoteLink(ctx context.Context, key, linkID string) (RemoteLinkDeleteWrite, error)

	// AddAttachment uploads one or more files to an issue and returns the
	// attachments Jira created.
	//
	// THE ONLY MULTIPART REQUEST ON THIS SEAM, and the only one that
	// streams its body from disk rather than composing it in memory: an
	// attachment can be tens of megabytes, and buffering one to build a
	// request would put the whole file in the process's heap for no gain.
	// files are opened by the ADAPTER, not here, so the caller hands over
	// paths and never holds a descriptor across a network call.
	//
	// It is never retried, and here that is load-bearing rather than
	// incidental: the endpoint has no idempotency key, so a replayed
	// upload after a lost response attaches the file TWICE and Jira keeps
	// both — two attachments with the same name and different ids.
	AddAttachment(ctx context.Context, key string, paths []string) (AttachmentWrite, error)

	// GetAttachment reads one attachment's metadata — filename, size, mime
	// type — without its content.
	//
	// It takes NO issue key, which is the endpoint's own shape: GET
	// /rest/api/3/attachment/{id} declares one parameter, because an
	// attachment id is unique across the site rather than scoped to an
	// issue. DeleteIssueLink above has the same property and the same
	// consequence for the command over it.
	//
	// A caller downloading a file needs this BEFORE the content, because
	// the filename lives here and nowhere else — the content endpoint
	// answers with bytes and a Content-Type, not a name.
	GetAttachment(ctx context.Context, id string) (AttachmentRead, error)

	// GetAttachmentContent streams one attachment's bytes.
	//
	// THE ONLY SEAM METHOD THAT HANDS BACK AN OPEN STREAM. The caller must
	// close AttachmentContent.Body, and the reason it is not decoded here
	// like every other response is size: an attachment is a file, and the
	// whole point of streaming it is that neither this client nor its
	// caller ever holds all of it.
	//
	// Jira answers this endpoint with a 303 to a signed URL on its media
	// host rather than the bytes themselves. The transport follows it —
	// the redirect is on a GET, which its own policy allows — and Go's
	// http.Client drops the Authorization header crossing to another host,
	// which is correct here: the signed URL carries its own credential and
	// must not be handed this site's.
	GetAttachmentContent(ctx context.Context, id string) (AttachmentContent, error)

	// DeleteAttachment removes one attachment.
	//
	// Destructive and unrecoverable, like DeleteComment: Jira keeps no
	// trash for attachments, so the file is gone from the API once this
	// returns. The confirmation that guards it lives in the COMMAND
	// (internal/cli/issue/attachment_delete.go) for DeleteComment's reason
	// — a seam that prompted would be unusable from a test and from an
	// agent alike.
	//
	// It takes only the id, for GetAttachment's reason.
	DeleteAttachment(ctx context.Context, id string) (AttachmentDeleteWrite, error)

	// DeleteIssue removes one issue, and optionally its subtasks.
	//
	// THE MOST DESTRUCTIVE OPERATION ON THIS SEAM. An issue carries its
	// comments, attachments, worklog and history with it, and Jira keeps
	// no trash — so this destroys strictly more than DeleteComment or
	// DeleteAttachment do, and none of it comes back.
	//
	// deleteSubtasks widens that further: Jira REFUSES to delete an issue
	// that has subtasks unless it is told to take them too, so the flag is
	// not an optimisation but the difference between a refusal and a
	// cascade. The command above it spells that out rather than passing
	// the flag through silently.
	DeleteIssue(ctx context.Context, key string, deleteSubtasks bool) (IssueDeleteWrite, error)

	// RankIssues moves issues before or after another issue in the
	// backlog's ordering.
	//
	// PUT /rest/agile/1.0/issue/rank is a different product surface with
	// its own version — and it reaches the same site because this client's
	// base URL is the SITE ROOT rather than an api prefix, which is worth
	// stating because it is the reason no second client was needed.
	//
	// It takes at most fifty distinct issues in one request. There is no fan-out here
	// and no per-issue concurrency to bound: the ordering the caller asked
	// for is the array's own order, and sending it whole is what makes
	// that ordering mean anything. An anchored move applied issue by issue
	// would interleave with itself and land somewhere nobody asked for.
	//
	// It answers 204 when every issue moved and 207 with a per-issue
	// breakdown when some did not — see RankWrite, which carries both.
	RankIssues(ctx context.Context, req jira.RankRequest) (RankWrite, error)

	// GetTransitions lists the transitions available on one issue right
	// now, as the workflow and the caller's own permissions see it.
	//
	// It is a READ that a write depends on, and it is on this seam for
	// two independent reasons rather than one.
	//
	// The endpoint is the only place a transition ID comes from, and the
	// POST below takes nothing else — no name, no target status. So a
	// caller who typed "Done" has to come here first to learn which
	// transition that is.
	//
	// It is also the only place `hasScreen` comes from, and that flag
	// decides whether a transition may carry a comment at all. Jira
	// ACCEPTS a comment on a screenless transition, performs the status
	// change, and silently discards the comment — verified against a live
	// tenant, and surfaced by no other endpoint and by no part of the
	// OpenAPI document. A command that attaches a comment without reading
	// this first cannot know whether it was delivered (design §3.1).
	//
	// Both facts ride ONE response, so a caller that needs either pays
	// for this once and never twice.
	GetTransitions(ctx context.Context, key string) (TransitionRead, error)

	// DoTransition moves one issue through its workflow, optionally
	// setting fields and adding a comment in the same request.
	//
	// THE FIRST WRITE ON THIS SEAM THAT RETURNS NOTHING. Jira answers 204
	// with no body, so unlike AddComment there is no created object to
	// publish and no free readback — a caller who wants to report the
	// resulting status must take it from the transition's own `to` in the
	// list above rather than from this call's answer.
	//
	// It carries no retry and no read-only carve-out of its own, for
	// AddComment's reasons: the transport's isRead classifies this POST
	// as a write, which both blocks it under read-only mode and excludes
	// it from automatic replay. Replay matters more here than it does for
	// a comment — a workflow can have several transitions out of one
	// status, so replaying a lost 204 could move an issue somewhere the
	// caller never asked for.
	//
	// It takes the composed request for the reason AddComment does: so
	// that composing the payload and sending it are separate steps, and
	// --dry-run can compose exactly what a live run would send.
	DoTransition(ctx context.Context, key string, req jira.TransitionRequest) (TransitionWrite, error)

	// CreateIssue creates one issue and returns its identity.
	//
	// THE FIRST WRITE ON THIS SEAM WITH NO SUBJECT TO NAME. Every write
	// above takes a key, because the thing it changes already exists;
	// this one MAKES the thing, so the key travels in the other
	// direction — out, in the response — and the whole reason a caller
	// runs it is to learn that key.
	//
	// It returns an identity, not an issue. Jira's 201 carries id, key
	// and self and nothing else, so unlike AddComment there is no free
	// readback: reporting what was actually stored costs a second GET,
	// which is precisely the extra round trip `--verify` exists to let a
	// caller opt into rather than pay for unasked.
	//
	// Read-only blocking and no-replay both come free from the transport
	// exactly as they do for AddComment, and replay matters more here
	// than anywhere: replaying a create whose 201 was lost to a timeout
	// would make a second issue, and nothing downstream could tell the
	// duplicate from the original.
	//
	// It takes the composed request for AddComment's reason — composing
	// and sending stay separate steps, so `--dry-run` previews exactly
	// what a live run sends rather than a reconstruction of it.
	CreateIssue(ctx context.Context, req jira.IssueWriteRequest) (IssueCreateWrite, error)

	// EditIssue applies field changes to one existing issue.
	//
	// It answers 204 with no body, like DoTransition and for the same
	// reason: the endpoint reports that the edit applied, never what the
	// issue now looks like. A caller who needs the stored values reads
	// them back, and `--verify` is the flag that asks for it.
	//
	// It shares CreateIssue's request type because the endpoints share a
	// payload in meaning and not merely in shape — see
	// jira.IssueWriteRequest, which argues the case for one type rather
	// than two identical ones.
	// notifyUsers asks Jira to email the issue's watchers about the
	// change. It rides the request as a query parameter, and true — the
	// default — is Jira's own, so it is sent only when false.
	//
	// **A false is a REQUEST, not a guarantee, and this seam cannot tell
	// the caller which it got.** Jira requires "administer Jira" or
	// "administer project" permission to suppress the notification and,
	// in its own words, "if the user doesn't have the necessary
	// permission the request is ignored" — ignored, not refused, with
	// nothing in the 204 that distinguishes the two outcomes. The command
	// layer publishes what was asked for and says so in those terms;
	// there is nothing here that could publish what happened.
	EditIssue(ctx context.Context, key string, req jira.IssueWriteRequest, notifyUsers bool) (IssueEditWrite, error)
}

// ErrNoCredential reports that no credential is stored for an account.
// A distinct sentinel because "not logged in" is an ordinary state with
// its own remedy, not a failure of the credential store.
var ErrNoCredential = errors.New("no stored credential")

// CredentialStore keeps the API token outside the config file.
//
// A seam rather than a direct dependency for the usual reason plus a
// sharper one: tests must never touch the developer's real keyring, and
// a headless CI box has no credential store at all, so the ability to
// substitute an implementation is a requirement of the design and not a
// convenience.
type CredentialStore interface {
	// Get returns the stored secret for account, or ErrNoCredential when
	// nothing is stored.
	Get(ctx context.Context, account string) (string, error)
	// Set stores secret for account, replacing any existing entry.
	Set(ctx context.Context, account, secret string) error
	// Delete removes the account's credential. Deleting an absent entry
	// succeeds — logout's contract is the end state, not the transition.
	Delete(ctx context.Context, account string) error
}

// NoCredentialStore is the store used when the credential store is
// turned off. Reads report nothing stored; writes fail loudly, because
// "I saved your token" is the one thing a disabled store must never say.
type NoCredentialStore struct{}

func (NoCredentialStore) Get(ctx context.Context, _ string) (string, error) {
	return "", ErrNoCredential
}

func (NoCredentialStore) Set(ctx context.Context, _, _ string) error {
	return &CredentialStoreError{Backend: CredentialBackendKeyring, Operation: "save", Cause: errors.New("the credential store is turned off (keyring = false), so there is nowhere to save the token")}
}

func (NoCredentialStore) Delete(ctx context.Context, _ string) error { return nil }

// Cache stores metadata bytes between reads so repeated calls stay snappy
// (cache-first, network on miss).
//
// **Get reports the WRITE TIME, and no method here takes a TTL.** How long
// a cached answer stays good is a property of the DATA, not of the
// storage: a site's link types change when an admin edits configuration
// and a project's issue types barely change at all, so the reader decides
// what counts as stale. A cache that expired entries on its own schedule
// would force every consumer to accept one answer, and would make the
// decision invisible at the place it matters.
type Cache interface {
	// Get returns the cached bytes for key and when they were written. Any
	// read failure is a miss: cache-first reads fall back to the network,
	// they never fail on a broken cache.
	Get(key string) (value []byte, written time.Time, ok bool)
	// Set stores value under key, stamped with the current time. Failures
	// surface so the caller decides — usually log-and-continue, because a
	// cold cache is not an error.
	Set(key string, value []byte) error
	// Forget drops one entry, which is what an explicit --refresh does
	// before re-reading. A missing entry is success.
	Forget(key string) error
}

// Clock is the time seam, so retry deadlines and backoff arithmetic run
// against an injectable clock in tests instead of the wall.
type Clock interface {
	Now() time.Time
}
