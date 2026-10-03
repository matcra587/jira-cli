package core

import (
	"encoding/json"
	"io"
	"time"

	"github.com/matcra587/jira-cli/internal/jira"
)

// RetryPolicy bounds the transport's automatic retry of safe requests. The
// zero value disables retry entirely (MaxWait 0 means no budget), so an
// unconfigured client never sleeps behind the caller's back.
type RetryPolicy struct {
	// MaxAttempts caps total tries, first attempt included.
	MaxAttempts int
	// BaseDelay seeds the exponential backoff window; MaxDelay caps it.
	BaseDelay time.Duration
	MaxDelay  time.Duration
	// MaxWait is the whole-request backoff budget: once a further wait
	// would push past it — or past the context deadline, whichever comes
	// first — the transport gives up and returns the last response.
	MaxWait time.Duration
}

// DefaultRetryPolicy is what production clients use: retry safe requests
// through transient failures rather than surfacing the first blip.
//
// The design makes this behavior the DEFAULT, with a fail-fast escape
// for callers that would rather see the 429 — the opposite of leaving it
// off until asked, because the common case is a human or agent who wants
// the command to survive a rate limit, not to learn about it.
//
// The numbers are deliberately modest: three attempts covers a single
// throttle or blip without turning a broken site into a long hang, and
// the 30s ceiling bounds total wall-clock even when Jira asks for more
// via Retry-After. Only safe requests ever reach this — the transport
// refuses to replay a write regardless of policy.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    8 * time.Second,
		MaxWait:     30 * time.Second,
	}
}

// RetryInfo reports what the retry loop actually did. Backoff is hidden
// wall-clock — a one-shot call can silently sleep — so the transport
// surfaces it and the output layer can say why a command was slow.
type RetryInfo struct {
	// Attempts is the number of HTTP attempts performed; 1 means no retry.
	Attempts int
	// Waited is the total time slept in backoff between attempts.
	Waited time.Duration
}

// IssueRead is the issue-read result: the wire-shaped issue plus the
// transport facts the output layer reports alongside it.
type IssueRead struct {
	Issue *jira.IssueBean
	Retry RetryInfo
}

// UserRead is the result of reading the authenticated account.
type UserRead struct {
	User  *jira.UserDetails
	Retry RetryInfo
}

// UserSearchRead retains the endpoint's array and safe-read retry facts.
// User is the schema of /user/search; CurrentUser uses UserDetails instead.
type UserSearchRead struct {
	Users []*jira.User
	Retry RetryInfo
}

// CommentRead is the comment-page read result: one server page of an
// issue's comments plus the transport facts the output layer reports
// alongside it.
type CommentRead struct {
	Page  *jira.CommentPage
	Retry RetryInfo
}

// CommentOrder is the direction JiraClient.GetComments reads a thread in.
//
// A named string rather than a bare one so the accepted set is a closed,
// documented three rather than whatever a caller happens to pass — the
// endpoint rejects anything else, and finding that out from a 400 is a
// worse way to learn it than not being able to write it.
//
// The values ARE the wire strings rather than names the adapter has to
// translate, because the enum is the API's own (GET
// /rest/api/3/issue/{issueIdOrKey}/comment, `orderBy`, verified against
// the vendored spec in internal/jira/spec/platform.json) and this type
// exists to stop a caller inventing a value outside it, not to invent a
// vocabulary of its own. The flag surface keeps its own words
// (`--order newest|oldest`, internal/cli/issue/comment.go): those are for
// a person, and a person should not have to know that a leading minus
// means newest.
//
// Only ONE field is sortable — `created`. The spec's enum is
// created/-created/+created, of which the bare form is documented as the
// same thing as `+created`, so there are two distinct orders here and no
// more. That is why this is a direction and not a field name, and why the
// flag above is not called --order-by like the JQL one: there is nothing
// to choose BY.
type CommentOrder string

const (
	// CommentOrderDefault expresses no preference, which is what every
	// caller that predates this parameter passes and what the adapter
	// resolves to newest-first. Distinct from CommentOrderNewestFirst
	// only in intent — both reach the same query — so that "did not ask"
	// and "asked for newest" stay separable at the call site.
	CommentOrderDefault CommentOrder = ""
	// CommentOrderNewestFirst is the latest activity first, which is what
	// a human reading a long thread almost always wants.
	CommentOrderNewestFirst CommentOrder = "-created"
	// CommentOrderOldestFirst reads the thread from its beginning, which
	// is what someone reconstructing how an issue unfolded wants.
	CommentOrderOldestFirst CommentOrder = "+created"
)

// WatcherRead is the watcher-read result: one issue's watchers plus the
// transport facts the output layer reports alongside them.
//
// No Page in the name and no page state in the type, unlike CommentRead
// above: the watchers endpoint returns the whole set in one response
// (see JiraClient.GetWatchers), so there is no server page for this
// struct to represent.
type WatcherRead struct {
	Watchers *jira.Watchers
	Retry    RetryInfo
}

// CommentWrite is the comment-post result: the comment Jira created plus
// the transport facts the output layer reports alongside it.
//
// Write, not Read, and a single Comment rather than CommentRead's Page:
// POST /rest/api/3/issue/{key}/comment answers with the one comment it
// created, not with a page of the thread. Reusing CommentRead here would
// have meant a Page holding exactly one element and a Total that means
// nothing.
type CommentWrite struct {
	Comment *jira.Comment
	Retry   RetryInfo
}

// CommentDeleteWrite carries the explicit native acknowledgement and transport
// facts. DELETE answers 204 without a body; a zero value is not evidence that
// deletion completed. A joined 204 remains usable after interruption.
type CommentDeleteWrite struct {
	StatusCode int
	Retry      RetryInfo
}

// WatcherWrite is the result of adding or removing one watcher: the
// transport facts and nothing else. Both verbs answer 204 with no body
// (POST and DELETE on /rest/api/3/issue/{key}/watchers), so one type
// serves both — unlike the read/write pairs above, there is no payload
// for the two directions to differ about.
type WatcherWrite struct {
	Retry RetryInfo
}

// LinkTypeRead is the link-type-list result: every issue link type
// configured on the site, plus the transport facts.
//
// Site-wide, not per issue, and that is the whole reason it is a
// separate seam: link types are an admin's configuration, so one read
// answers for every key in a fan-out rather than one read per key.
type LinkTypeRead struct {
	Types *jira.IssueLinkTypes
	Retry RetryInfo
}

// LinkWrite is the issue-link-create result: the transport facts and
// nothing else, because POST /rest/api/3/issueLink answers 201 with an
// EMPTY body — it creates a resource and declines to describe it, which
// is unusual enough to be worth stating here.
//
// The consequence a caller feels: the new link's id is not knowable from
// the create call, so `link create` cannot publish one and `link delete`
// takes an id that only `link list` can supply.
type LinkWrite struct {
	Retry RetryInfo
}

// LinkDeleteWrite is the issue-link-delete result: the transport facts
// and nothing else (204).
type LinkDeleteWrite struct {
	Retry RetryInfo
}

// RemoteLinkRead is the remote-link-list result: one issue's remote
// links plus the transport facts.
//
// A slice rather than a page type, like WatcherRead and unlike
// CommentRead: GET /rest/api/3/issue/{key}/remotelink declares one
// optional filter (globalId) and no paging pair at all, and answers with
// the whole set.
type RemoteLinkRead struct {
	Links []jira.RemoteIssueLink
	Retry RetryInfo
}

// RemoteLinkWrite is the remote-link-create result: the link Jira
// identifies plus whether it was CREATED or updated.
//
// Created is the member that earns this type over a bare
// acknowledgement. The endpoint is create-or-update keyed on globalId
// and says which it did through its status code alone — 201 or 200 —
// so a caller who cannot see that cannot tell "I added a link" from "I
// replaced one that was already there".
type RemoteLinkWrite struct {
	// Link is the validated identifier Jira answers with. A successful
	// adapter write requires a positive native numeric identity; malformed
	// acknowledgements report uncertainty instead of inventing identity zero.
	Link *jira.RemoteIssueLink
	// Created is true for a 201 (a new link) and false for a 200 (an
	// existing one replaced).
	Created bool
	Retry   RetryInfo
}

// RemoteLinkDeleteWrite is the remote-link-delete result: the transport
// facts and nothing else (204).
type RemoteLinkDeleteWrite struct {
	Retry RetryInfo
}

// AttachmentWrite is the attachment-upload result: the attachments Jira
// created, one per file sent.
//
// A SLICE, not a single object, because the endpoint takes a multipart
// body carrying any number of files and answers with an array — one
// upload request can create several attachments, which is unlike every
// other write on this seam.
type AttachmentWrite struct {
	Attachments []jira.Attachment
	Retry       RetryInfo
}

// AttachmentRead is one attachment's METADATA: its filename, size and
// mime type, without its bytes.
//
// Separate from the content read below because they are separate
// requests against separate endpoints, and because the filename lives
// here — a caller downloading a file has to read this first to learn
// what to call it (see JiraClient.GetAttachmentContent).
type AttachmentRead struct {
	Attachment *jira.AttachmentMetadata
	Retry      RetryInfo
}

// AttachmentContent is an attachment's BYTES, as an open stream the
// caller must close.
//
// THE ONLY RESULT TYPE ON THIS SEAM THAT CARRIES AN OPEN HANDLE, and the
// only one whose caller owes it a Close. Everything else here is decoded
// and finished by the time it is returned; an attachment can be
// arbitrarily large, so buffering it to keep that property would mean
// holding a whole file in memory to avoid a defer.
//
// The contract, stated because it is unusual: Body is non-nil whenever
// the error is nil, and the caller MUST close it.
type AttachmentContent struct {
	// Body streams the file. Never nil alongside a nil error.
	Body io.ReadCloser
	// Size is the length the transport reported, or -1 when it did not.
	// Advisory only — it is the server's claim about a stream the caller
	// is about to read, not a promise.
	Size  int64
	Retry RetryInfo
}

// AttachmentDeleteWrite is the attachment-delete result: the transport
// facts and nothing else (204).
type AttachmentDeleteWrite struct {
	// StatusCode is 204 after a native acknowledgement. Zero carries no
	// acknowledgement, including when interruption joins a failed write.
	StatusCode int
	Retry      RetryInfo
}

// IssueDeleteWrite is the issue-delete result: the transport facts and
// nothing else (204).
type IssueDeleteWrite struct {
	// StatusCode is 204 after Jira acknowledges deletion. Zero carries no
	// acknowledgement, including when interruption joins a failed write.
	StatusCode int
	Retry      RetryInfo
}

// RankWrite retains the native acknowledgement of one ordered ranking request.
// A 204 acknowledges every issue; a 207 supplies complete per-issue outcomes.
type RankWrite struct {
	// StatusCode is 204 or 207 after a usable response. Zero carries no
	// acknowledgement, including when interruption joins a failed write.
	StatusCode int
	Outcomes   []RankOutcome
	Retry      RetryInfo
}

// RankOutcome is one native 207 entry, before request-order correlation.
type RankOutcome struct {
	IssueKey string
	IssueID  int64
	Status   int
	Errors   []string
}

// TransitionRead is the transition-list result: every transition
// available on one issue right now, plus the transport facts the output
// layer reports alongside them.
//
// Like WatcherRead and unlike CommentRead, it carries no page state: GET
// /rest/api/3/issue/{issueIdOrKey}/transitions answers with the whole
// set and declares no startAt/maxResults pair, because the set is
// bounded by the workflow rather than by the issue's history.
type TransitionRead struct {
	Transitions *jira.Transitions
	Retry       RetryInfo
}

// TransitionWrite carries the native acknowledgement and transport facts.
//
// POST /rest/api/3/issue/{key}/transitions answers 204 without a body.
// Preserve that acknowledgement separately from errors so callers can retain
// a joined move receipt after interruption without inventing a status readback.
type TransitionWrite struct {
	// StatusCode is 204 only after Jira acknowledged the move. A zero value
	// cannot establish success; a joined acknowledgement survives interruption.
	StatusCode int
	Retry      RetryInfo
}

// IssueCreateWrite is the issue-create result: the identity Jira assigned
// to the new issue, plus the transport facts the output layer reports
// alongside it.
//
// Issue is a *jira.CreatedIssue and not a *jira.IssueBean, because an
// identity is a narrow projection of the create endpoint response, rather
// than stored issue fields. Receipt preserves optional native followup outcomes
// and future members that projection does not model.
type IssueCreateWrite struct {
	// StatusCode is 201 only when Jira acknowledged creating the issue.
	// It survives a malformed response body; zero carries no acknowledgement.
	StatusCode int
	Issue      *jira.CreatedIssue
	Receipt    json.RawMessage
	Retry      RetryInfo
}

// IssueEditWrite is the issue-edit result: the transport facts and nothing
// else, for TransitionWrite's reason — the endpoint answers 204 with no
// body, so there is no created or updated object to carry.
type IssueEditWrite struct {
	Retry RetryInfo
}

// IssueSearchRead is the JQL-search result: one server page of matching
// issues plus the transport facts the output layer reports alongside
// it. Results carries its own cursor (NextPageToken) and no total —
// see jira.SearchResults for why a total does not exist to report.
type IssueSearchRead struct {
	Results *jira.SearchResults
	Retry   RetryInfo
}

// IssueCountRead is the JQL-count result: Jira's own ESTIMATED match
// count (see JiraClient.CountIssues's own doc comment for why it can
// only ever be an estimate) plus the transport facts the output layer
// reports alongside it. Count is a plain int64, not a pointer or a
// Nullable — jira.SearchCountResults is itself a plain struct (its own
// doc comment explains why), and a decode that succeeds with Count==0
// is not an ambiguous "empty response" the way a nil IssueRead.Issue
// would be: zero genuinely is a valid answer ("no issues match"), so
// there is no absent-vs-zero distinction for this type to lose.
type IssueCountRead struct {
	Count int64
	Retry RetryInfo
}

// FilterRead is the saved-filter result: the whole filter as Jira
// described it, plus the transport facts.
//
// Filter is a POINTER, following LinkTypeRead and IssueRead rather than
// IssueCountRead's plain value, and the distinction is the same one those
// types draw: a filter has no meaningful zero value, so nil is the
// unambiguous signal that a decode produced nothing — where a zero Count
// is a real answer. The caller checks it: a filter whose JQL is empty
// would otherwise resolve to an empty query, which Jira reads as "every
// issue on the site".
type FilterRead struct {
	Filter *jira.Filter
	Retry  RetryInfo
}

// BoardRead is one page of Agile boards, plus the transport facts.
//
// Page is a POINTER for FilterRead's reason: a page has no meaningful
// zero value, so nil is the unambiguous signal that a decode produced
// nothing — where an empty Values slice is a real answer ("no board
// matches that name") the command layer has its own words for.
type BoardRead struct {
	Page  *jira.BoardPage
	Retry RetryInfo
}

// BoardProjectRead is one associated-project page plus transport facts.
type BoardProjectRead struct {
	Page  *jira.BoardProjectPage
	Retry RetryInfo
}

// BoardConfigurationRead is one board's configuration, plus the
// transport facts.
type BoardConfigurationRead struct {
	Configuration *jira.BoardConfiguration
	Retry         RetryInfo
}
