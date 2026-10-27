package issue

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gechr/clog"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// The keyed mechanics — argv ordering under a concurrent fan-out,
// --parallelism, partial vs total failure, the severity ladder — are
// pinned once against `issue link list` (link_test.go), which shares
// this command's executor, its outcome/KeyedFailure split and its
// fake-client shape. Repeating them here would test command.KeyedFailure
// and x/sync.Parallel a second time rather than anything this command
// decides. What IS this command's own is below: the narrowed read, the
// wire-to-view narrowing, the empty-array contract, and the render.
//
// fakeLinkClient (link_test.go) is reused as the core.JiraClient double —
// its GetIssue answers from a per-key table and records the fields each
// call asked for, which is exactly what these need.

// attachmentBean builds the issue bean the fake serves for key.
func attachmentBean(key string, attachments ...jira.Attachment) *jira.IssueBean {
	if attachments == nil {
		attachments = []jira.Attachment{}
	}
	return &jira.IssueBean{
		ID:   jira.NullableOf("1" + key),
		Key:  jira.NullableOf(key),
		Self: jira.NullableOf("https://example.atlassian.net/rest/api/3/issue/1" + key),
		Fields: jira.NullableOf(jira.IssueFields{
			Attachment: jira.NullableOf(attachments),
		}),
	}
}

// wireAttachment is one fixture attachment, every field populated so a
// narrowing that dropped one is visible.
func wireAttachment(id, filename, mime string, size int64) jira.Attachment {
	return jira.Attachment{
		ID:       jira.NullableOf(id),
		Filename: jira.NullableOf(filename),
		MimeType: jira.NullableOf(mime),
		Size:     jira.NullableOf(size),
		Created:  jira.NullableOf("2024-02-20T14:45:00.000-0500"),
		Content:  jira.NullableOf("https://example.atlassian.net/rest/api/3/attachment/content/" + id),
		Author: jira.NullableOf(jira.UserDetails{
			AccountID:   jira.NullableOf("acct-1"),
			DisplayName: jira.NullableOf("Ada Uploader"),
		}),
	}
}

// TestRunAttachmentListNarrowsTheReadToAttachments pins that the read
// asks Jira for the one field it needs. Requesting the server's default
// field set would pull every issue whole — description, every custom
// field — once per key, and this command displays none of it.
func TestRunAttachmentListNarrowsTheReadToAttachments(t *testing.T) {
	fake := &fakeLinkClient{issues: map[string]*jira.IssueBean{"ABC-1": attachmentBean("ABC-1")}}

	if _, err := runAttachmentList(t.Context(), linkDeps(fake), []string{"ABC-1"}, 1); err != nil {
		t.Fatalf("runAttachmentList: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.gotField) != 1 || len(fake.gotField[0]) != 1 || fake.gotField[0][0] != "attachment" {
		t.Errorf("requested fields = %v, want exactly [attachment]", fake.gotField)
	}
}

// TestRunAttachmentListReturnsEveryAttachment is the pagination decision
// expressed as a test, and the reason it is worth one: 0.15 shipped a
// default `--limit 50` that cut this array in memory AFTER the whole set
// had already crossed the wire, so an issue with more than fifty files
// reported fifty and said nothing about the rest. There is no window to
// apply here and no flag to apply one with — a caller counting
// `attachments` counts them all.
func TestRunAttachmentListReturnsEveryAttachment(t *testing.T) {
	const count = 60
	attachments := make([]jira.Attachment, count)
	for i := range attachments {
		attachments[i] = wireAttachment(
			"1"+strings.Repeat("0", i%3)+string(rune('a'+i%26)), "file.txt", "text/plain", int64(i+1),
		)
	}
	fake := &fakeLinkClient{issues: map[string]*jira.IssueBean{
		"ABC-1": attachmentBean("ABC-1", attachments...),
	}}

	out, err := runAttachmentList(t.Context(), linkDeps(fake), []string{"ABC-1"}, 1)
	if err != nil {
		t.Fatalf("runAttachmentList: %v", err)
	}
	if len(out.Results) != 1 || out.Results[0].Data == nil {
		t.Fatalf("results = %+v, want one successful entry", out.Results)
	}
	if got := len(out.Results[0].Data.Attachments); got != count {
		t.Errorf("attachments = %d, want all %d — this command applies no window, so a short answer means one crept back in", got, count)
	}
}

// TestAttachmentViewsNarrowEveryPublishedField pins the wire-to-view
// mapping field by field. A narrowing that silently dropped one (or
// crossed two of the several string fields over) would still produce a
// well-formed envelope and a plausible table.
func TestAttachmentViewsNarrowEveryPublishedField(t *testing.T) {
	got := attachmentViews([]jira.Attachment{
		wireAttachment("10500", "crash log.txt", "text/plain", 812),
	})
	if len(got) != 1 {
		t.Fatalf("views = %d, want 1", len(got))
	}
	a := got[0]
	for _, c := range []struct{ field, got, want string }{
		{"id", a.ID, "10500"},
		{"filename", a.Filename, "crash log.txt"},
		{"mime_type", a.MimeType, "text/plain"},
		{"created", a.Created, "2024-02-20T14:45:00.000-0500"},
		{"content", a.Content, "https://example.atlassian.net/rest/api/3/attachment/content/10500"},
		{"author.account_id", a.Author.AccountID, "acct-1"},
		{"author.display_name", a.Author.DisplayName, "Ada Uploader"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
	// The size stays an integer count of bytes all the way through the
	// envelope; formatting it is the human table's job alone.
	if a.Size != jira.NullableOf(int64(812)) {
		t.Errorf("size = %d, want 812 — the envelope publishes raw bytes, never a formatted string", a.Size.Value)
	}
}

// TestAttachmentViewsAreNeverNil pins the empty-array contract: a caller
// iterating data.results[].data.attachments must not have to nil-check
// the common case of an issue with nothing attached.
func TestAttachmentViewsAreNeverNil(t *testing.T) {
	if got := attachmentViews(nil); got == nil {
		t.Error("attachmentViews(nil) = nil, want an empty array — the envelope publishes [] for an issue with no attachments")
	}
}

// TestRunAttachmentListFailsLoudlyOnNilIssue mirrors runView's,
// runCommentList's and runLinkList's own guards: GetIssue's contract is
// a non-nil Issue on every nil-error return, and a zero-valued payload
// would read as an issue with no attachments rather than the broken
// response it is.
func TestRunAttachmentListFailsLoudlyOnNilIssue(t *testing.T) {
	fake := &fakeLinkClient{issues: map[string]*jira.IssueBean{"ABC-1": nil}}

	_, err := runAttachmentList(t.Context(), linkDeps(fake), []string{"ABC-1"}, 1)
	if err == nil {
		t.Fatal("runAttachmentList succeeded on a nil Issue — an empty result would have been reported as success")
	}
	if !strings.Contains(err.Error(), "ABC-1") {
		t.Errorf("error = %v, want it to name the key", err)
	}
}

// TestRunAttachmentListKeepsSuccessesWhenOneKeyFails pins that this
// command is wired into the shared partial-result rule, rather than
// merely that the rule works — command.KeyedFailure's own behavior is
// link_test.go's to prove. One bad key must not cost the caller the good
// ones.
func TestRunAttachmentListKeepsSuccessesWhenOneKeyFails(t *testing.T) {
	fake := &fakeLinkClient{
		issues: map[string]*jira.IssueBean{
			"ABC-1": attachmentBean("ABC-1", wireAttachment("10500", "log.txt", "text/plain", 812)),
		},
		failures: map[string]error{
			"ABC-404": &adapterjira.APIError{StatusCode: 404, Messages: []string{"Issue does not exist"}},
		},
	}

	out, err := runAttachmentList(t.Context(), linkDeps(fake), []string{"ABC-1", "ABC-404"}, 2)
	if err != nil {
		t.Fatalf("runAttachmentList returned an error for a partial result: %v", err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}
	if first := out.Results[0]; !first.OK || first.Data == nil || len(first.Data.Attachments) != 1 {
		t.Fatalf("results[0] = %+v, want the successful key with its one attachment", first)
	}
	second := out.Results[1]
	switch {
	case second.OK:
		t.Fatal("results[1].ok is true for a key that failed")
	case second.Data != nil:
		t.Error("results[1] carries data for a key that failed")
	case second.Err == nil:
		t.Fatal("results[1] failed but says nothing about why")
	case second.Err.Code != "jira_not_found":
		t.Errorf("results[1].error.code = %q, want jira_not_found", second.Err.Code)
	}
}

// renderAttachments runs renderAttachmentList and returns what it wrote.
func renderAttachments(t *testing.T, out envelope.IssueAttachmentListOutput, mode clog.ColorMode) string {
	t.Helper()
	withStyle(t, mode == clog.ColorAlways)

	var buf bytes.Buffer
	v := output.NewView(&buf, mode)
	if err := renderAttachmentList(v, envelope.Envelope{OK: true, Data: out}); err != nil {
		t.Fatalf("renderAttachmentList: %v", err)
	}
	return buf.String()
}

// attachmentResult is a small builder for a successful keyed result.
func attachmentResult(key string, attachments ...envelope.AttachmentView) envelope.KeyedResult[envelope.IssueAttachmentListData] {
	data := envelope.IssueAttachmentListData{Attachments: attachments}
	return envelope.KeyedResult[envelope.IssueAttachmentListData]{
		Issue: envelope.IssueRef{Key: key},
		OK:    true,
		Data:  &data,
	}
}

func TestRenderAttachmentListDrawsOneSectionPerKey(t *testing.T) {
	out := envelope.IssueAttachmentListOutput{}
	out.Results = []envelope.KeyedResult[envelope.IssueAttachmentListData]{
		attachmentResult("ABC-1", envelope.AttachmentView{
			ID: "10500", Filename: "crash-log.txt", MimeType: "text/plain", Size: jira.NullableOf(int64(812)),
			Created: "2024-02-20T14:45:00.000-0500",
			Author:  envelope.AuthorRef{DisplayName: "Ada Uploader"},
		}),
		attachmentResult("ABC-2"),
	}

	got := renderAttachments(t, out, clog.ColorNever)
	for _, want := range []string{"ABC-1", "ABC-2", "crash-log.txt", "text/plain", "Ada Uploader", "812 B", "No attachments."} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want it to contain %q", got, want)
		}
	}
	// The empty key's own line belongs to THAT key's section, so the
	// order matters: a "No attachments." printed before ABC-2's header
	// would attribute it to the wrong issue.
	if strings.Index(got, "ABC-2") > strings.Index(got, "No attachments.") {
		t.Errorf("output = %q, want the empty key's header before its own no-attachments line", got)
	}
}

// TestRenderAttachmentListShowsSizesAsHumanUnits pins the one place the
// raw byte count must NOT appear verbatim: a column of "3355443" is
// unreadable, and it is also what a renderer that forgot the sink would
// produce — a failure invisible to every schema check, since the
// envelope's own number is correct either way.
func TestRenderAttachmentListShowsSizesAsHumanUnits(t *testing.T) {
	out := envelope.IssueAttachmentListOutput{}
	out.Results = []envelope.KeyedResult[envelope.IssueAttachmentListData]{
		attachmentResult("ABC-1", envelope.AttachmentView{
			ID: "1", Filename: "heap.hprof", Size: jira.NullableOf(int64(3_355_443)),
			Author: envelope.AuthorRef{DisplayName: "Ada Uploader"},
		}),
	}

	got := renderAttachments(t, out, clog.ColorNever)
	if !strings.Contains(got, "MiB") {
		t.Errorf("output = %q, want the size in human units", got)
	}
	if strings.Contains(got, "3355443") {
		t.Errorf("output = %q, want no raw byte count — the table renders through cli.Bytes", got)
	}
}

// TestRenderAttachmentListShowsAFailedKeyInPlace pins the half of the
// partial-result contract a person sees: the command succeeded, so
// nothing else would ever tell them one of their keys did not answer.
func TestRenderAttachmentListShowsAFailedKeyInPlace(t *testing.T) {
	out := envelope.IssueAttachmentListOutput{}
	out.Results = []envelope.KeyedResult[envelope.IssueAttachmentListData]{
		attachmentResult("ABC-1"),
		{
			Issue: envelope.IssueRef{Key: "ABC-404"},
			OK:    false,
			Err: &envelope.KeyError{
				Code:    "jira_not_found",
				Message: "issue ABC-404 was not found",
				Hint:    "Check the identifier.",
			},
		},
	}

	got := renderAttachments(t, out, clog.ColorNever)
	for _, want := range []string{"ABC-404", "was not found", "Check the identifier."} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want it to contain %q", got, want)
		}
	}
}

// TestRenderAttachmentListRejectsAForeignPayload pins the Human
// closure's type guard: the renderer is reached through an interface,
// and a mis-registered command would otherwise panic on the assertion
// rather than returning the error the contract expects.
func TestRenderAttachmentListRejectsAForeignPayload(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)
	err := renderAttachmentList(v, envelope.Envelope{OK: true, Data: envelope.IssueLinkListOutput{}})
	if err == nil {
		t.Fatal("renderAttachmentList accepted a foreign payload")
	}
	if !strings.Contains(err.Error(), "IssueAttachmentListOutput") {
		t.Errorf("error = %v, want it to name the expected type", err)
	}
}

// TestAttachmentSpinnerLabelCountsRatherThanNames pins the label rule a
// fan-out spinner depends on: one animation covers every key, so its
// text must not imply it is tracking a particular one.
func TestAttachmentSpinnerLabelCountsRatherThanNames(t *testing.T) {
	if got := attachmentSpinnerLabel([]string{"ABC-1"}); !strings.Contains(got, "ABC-1") {
		t.Errorf("single-key label = %q, want it to name the key", got)
	}
	got := attachmentSpinnerLabel([]string{"ABC-1", "ABC-2", "ABC-3"})
	if !strings.Contains(got, "3") {
		t.Errorf("multi-key label = %q, want it to count the keys", got)
	}
	if strings.Contains(got, "ABC-1") {
		t.Errorf("multi-key label = %q, want no single key named — the label is fixed while the reads are not", got)
	}
}
