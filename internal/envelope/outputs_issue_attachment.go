package envelope

// IssueAttachmentListOutput is the `issue.attachment.list` operation's
// payload: the attachments of one or more issues, one keyed result per
// requested key.
//
// It carries NO pagination, and — unlike IssueLinkListOutput, which is
// unpaginated because nobody expected it to be — that is a decision
// taken against a predecessor that shipped paging flags here. The
// reasoning, recorded because the next four keyed reads will reach for
// it:
//
// Jira exposes no list-an-issue's-attachments operation. The whole
// /rest/api/3/attachment surface is per-id — {id}, {id}/content,
// {id}/thumbnail, plus a /meta capability probe — so an issue's
// attachments arrive whole inside its own `fields.attachment`
// (internal/jira.IssueFields), exactly as its links do. There is no
// startAt to send, no maxResults to honor, and no page to be on.
//
// 0.15 nonetheless gave this command `--limit` (default 50) and `--all`.
// They were not pagination: the whole array had already crossed the wire
// by the time either was consulted, and the code sliced it in memory —
// its own comment conceded "a truncated window is not resumable
// page-by-page". So the flags saved no bytes, no time and no round
// trips; `--limit` bought nothing and cost the caller attachment 51
// onward, by default, silently. An agent reading
// `.data.results[].data.attachments | length` on a 60-attachment issue
// got 50 and nothing in the document said otherwise.
//
// Publishing a pagination block to describe that would have been worse
// still: a page object whose `total` the server never paginated against
// asserts a page the resource does not have, and the contract's whole
// point is that a pagination block means the same thing everywhere it
// appears.
//
// So the flags are gone and the set arrives whole. A caller who wants
// fewer rows has `--jq '.data.results[].data.attachments[:10]'`, which slices
// the same array at the same place, without the CLI pretending the cut
// happened at the server. The keyed pagination question the build order
// posed here is therefore answered, but negatively — the FIRST keyed
// read that genuinely paginates (a `comment list` conversion, whose
// endpoint really does take startAt/maxResults) is where per-key page
// state has to be designed, and the rule it inherits is already written
// down: the block folds inside the per-key operation object at
// `data.results[].data`, never in meta (.claude/rules/output.md, and
// tests/guardrails/pagination_placement_test.go).
type IssueAttachmentListOutput struct {
	// KeyedResults is the shared embed, not a hand-declared Results
	// field: `results` must be the same key with the same ordering rule
	// on every keyed command, and declaring it per command is how those
	// drift.
	KeyedResults[IssueAttachmentListData]
}

// IssueAttachmentListData is one issue's attachments — the operation
// object that rides at data.results[].data.
type IssueAttachmentListData struct {
	// Attachments is every attachment on this issue, in the order Jira
	// returned them. An issue with none carries an empty
	// array, never null: a caller iterating it must not have to
	// special-case the common case.
	//
	// Every attachment, with no window applied — see the type doc above
	// for why this command publishes no page state and takes no
	// `--limit`. `length` on this array is therefore the issue's true
	// attachment count, which is the property a truncating default would
	// have quietly broken.
	Attachments []AttachmentView `json:"attachments"`
}

// AttachmentView is one attachment, shaped for the CLI's contract.
//
// The wire (internal/jira.Attachment) also carries `thumbnail`, a URL
// that exists only for images and that no command here can act on; it is
// left out for the reason LinkedIssueFields leaves out `assignee` —
// publishing a field the CLI has no verb for grows the contract without
// growing what a caller can do.
type AttachmentView struct {
	// ID is the attachment's own id — the handle `issue attachment
	// download` and `issue attachment delete` both take. First, because
	// finding it is the reason this command exists.
	ID string `json:"id"`
	// Filename is the name the file was uploaded under. Author-supplied,
	// so it is Jira-authored text in the sense that matters: every human
	// surface runs it through a sanitizing sink, and a filename is a
	// likelier carrier of hostile bytes than most — it comes from a
	// filesystem, not a form.
	Filename string `json:"filename"`
	// MimeType is the content type Jira recorded at upload
	// ("image/png"). Empty when the response carried none.
	MimeType string `json:"mime_type"`
	// Size is the attachment's size in BYTES, unformatted. The human
	// table renders it as "1.4 MiB"; the envelope publishes the integer,
	// because a machine caller summing or thresholding sizes must not
	// have to parse a display string back into a number.
	Size int64 `json:"size"`
	// Created is the upload timestamp, as the wire spells it — Jira's own
	// colonless-offset format, passed through rather than normalized,
	// exactly as CommentView.Created is.
	Created string `json:"created"`
	// Author is who uploaded it.
	Author AuthorRef `json:"author"`
	// Content is the URL the bytes are fetched from — the same URL
	// `issue attachment download` will resolve. Published rather than
	// hidden because it is the only way a caller outside this CLI can
	// reach the file at all, and Jira gives no way to construct it from
	// the id.
	Content string `json:"content"`
}

// The overrides carry prose only; the shape derives from the struct.
func init() {
	Register("issue.attachment.list", IssueAttachmentListOutput{}, map[string]any{
		"description": "The attachments of one or more issues, one keyed result per requested key.",
		"properties": map[string]any{
			"results": map[string]any{
				"description": "One entry per requested key, in input order. Total failure and interruption retain joined outcomes in the full failure frame. A failed entry can carry both a validated attachment observation and its original read error; inspect ok and error before treating the read as complete. Missing or unusable native arrays are protocol failures, not empty issues.",
			},
		},
	})
}
