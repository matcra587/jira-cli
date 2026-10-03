package issue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/gechr/clog"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// fakeCommentClient is a minimal core.JiraClient double: GetComments
// returns whatever page/err a test seeds and records the startAt/
// maxResults it was called with (commentsCalled distinguishes "never
// called" from "called with (0,0)", the same reason fakeIssueClient's
// own commentsCalled exists — see its doc comment, view_test.go);
// GetIssue is unused by this command and only exists to satisfy the
// interface. baseURL feeds BaseURL() — "" (comment.go's own ordinary
// render needs no site) unless a --web test sets it for
// runCommentListWeb's own browse-URL construction.
type fakeCommentClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	read core.CommentRead
	err  error

	gotStartAt, gotMaxResults int
	commentsCalled            bool

	baseURL string
}

func (f *fakeCommentClient) BaseURL() string { return f.baseURL }

func (f *fakeCommentClient) GetComments(_ context.Context, _ string, startAt, maxResults int, _ core.CommentOrder) (core.CommentRead, error) {
	f.commentsCalled = true
	f.gotStartAt, f.gotMaxResults = startAt, maxResults
	return f.read, f.err
}

func TestRunCommentListMapsStartAndLimitToStartAtAndMaxResults(t *testing.T) {
	fake := &fakeCommentClient{read: core.CommentRead{Page: &jira.CommentPage{StartAt: 25, MaxResults: 10, Total: 25, Comments: []jira.Comment{}}}}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	if _, err := runCommentList(t.Context(), deps, "ABC-1", 25, 10, core.CommentOrderDefault); err != nil {
		t.Fatalf("runCommentList: %v", err)
	}
	if fake.gotStartAt != 25 {
		t.Errorf("startAt = %d, want 25", fake.gotStartAt)
	}
	if fake.gotMaxResults != 10 {
		t.Errorf("maxResults = %d, want 10", fake.gotMaxResults)
	}
}

// TestRunCommentListFailsLoudlyOnNilPage mirrors
// TestRunViewFailsLoudlyOnNilIssue: GetComments's own contract
// (internal/adapters/jira/client.go) is a non-nil Page on every
// nil-error return, so runCommentList must refuse a violation of it
// rather than silently produce an empty-but-successful page.
func TestRunCommentListFailsLoudlyOnNilPage(t *testing.T) {
	deps := Deps{
		JiraClient: func() (core.JiraClient, error) {
			return &fakeCommentClient{read: core.CommentRead{Page: nil}}, nil
		},
	}

	_, err := runCommentList(t.Context(), deps, "ABC-123", 0, 50, core.CommentOrderDefault)
	if err == nil {
		t.Fatal("runCommentList returned nil error for a nil Page — an empty page would have been reported as success")
	}
	if !strings.Contains(err.Error(), "ABC-123") {
		t.Errorf("error = %v, want it to name the issue key", err)
	}
}

// TestRunCommentListWebBuildsIssueURLWithoutFetching pins --web's own
// contract for `issue comment list`: it resolves the COMMENTED ISSUE'S
// browse URL (comments live on that page), never calling GetComments
// (commentsCalled stays false).
func TestRunCommentListWebBuildsIssueURLWithoutFetching(t *testing.T) {
	fake := &fakeCommentClient{baseURL: "https://example.atlassian.net"}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	var calls []recordedOpen
	out, err := runCommentListWeb(t.Context(), deps, "ABC-123", fakeOpener(&calls, nil))
	if err != nil {
		t.Fatalf("runCommentListWeb: %v", err)
	}

	if fake.commentsCalled {
		t.Error("GetComments was called — --web must not fetch the thread")
	}
	const want = "https://example.atlassian.net/browse/ABC-123"
	if out.WebURL != want {
		t.Errorf("WebURL = %q, want %q", out.WebURL, want)
	}
	if out.Issue.Key != "ABC-123" {
		t.Errorf("Issue.Key = %q, want %q", out.Issue.Key, "ABC-123")
	}
	if len(out.Comments) != 0 {
		t.Errorf("Comments = %v, want empty — --web never fetched them", out.Comments)
	}
}

// TestRunCommentListWebRejectsMalformedKey mirrors
// TestRunViewWebRejectsMalformedKey (view_test.go): producing the URL is
// this invocation's entire point, so a key that cannot become one fails
// loudly rather than silently degrading.
func TestRunCommentListWebRejectsMalformedKey(t *testing.T) {
	fake := &fakeCommentClient{baseURL: "https://example.atlassian.net"}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	var calls []recordedOpen
	_, err := runCommentListWeb(t.Context(), deps, "not a valid key", fakeOpener(&calls, nil))
	if err == nil {
		t.Fatal("runCommentListWeb(malformed key) returned nil error, want a classified rejection")
	}
	if got := cli.MapError(err).Code; got != cli.CodePathInvalid {
		t.Errorf("classified code = %q, want %q", got, cli.CodePathInvalid)
	}
}

func TestBuildCommentListOutput(t *testing.T) {
	page := &jira.CommentPage{
		StartAt:    5,
		MaxResults: 10,
		Total:      12,
		Comments: []jira.Comment{
			{
				ID:      jira.NullableOf("100"),
				Author:  jira.NullableOf(jira.UserDetails{AccountID: jira.NullableOf("acc-1"), DisplayName: jira.NullableOf("Ada Lovelace")}),
				Body:    jira.NullableOf[json.RawMessage]([]byte(`{"type":"doc"}`)),
				Created: jira.NullableOf("2024-03-01T10:00:00.000+0000"),
				Updated: jira.NullableOf("2024-03-02T10:00:00.000+0000"),
			},
		},
	}

	got := buildCommentListOutput("ABC-1", core.CommentRead{Page: page})

	if got.Issue.Key != "ABC-1" {
		t.Errorf("issue key = %q, want ABC-1", got.Issue.Key)
	}
	if got.Pagination != (envelope.OffsetPagination{StartAt: 5, MaxResults: 10, Total: 12, Returned: 1}) {
		t.Errorf("pagination = %+v, want {5 10 12 1}", got.Pagination)
	}
	if len(got.Comments) != 1 {
		t.Fatalf("comments = %+v, want one entry", got.Comments)
	}
	c := got.Comments[0]
	if c.ID != "100" || c.Author.AccountID != "acc-1" || c.Author.DisplayName != "Ada Lovelace" {
		t.Errorf("comment = %+v, want id 100 authored by acc-1/Ada Lovelace", c)
	}
	if string(c.Body) != `{"type":"doc"}` {
		t.Errorf("body = %s, want the raw ADF verbatim", c.Body)
	}
}

func TestBuildCommentListOutputReturnedTracksActualPageSize(t *testing.T) {
	// Total can exceed how many this page actually carries — Returned
	// must reflect the ACTUAL slice, not the requested maxResults, so a
	// caller comparing Returned against Total sees the truth on a short
	// final page.
	page := &jira.CommentPage{Total: 100, Comments: []jira.Comment{{}, {}, {}}}
	got := buildCommentListOutput("ABC-1", core.CommentRead{Page: page})
	if got.Pagination.Returned != 3 {
		t.Errorf("returned = %d, want 3", got.Pagination.Returned)
	}
}

// renderComments runs renderCommentList against a built envelope,
// returning what it wrote — the comment-list sibling of view_test.go's
// renderIssue helper.
func renderComments(t *testing.T, out envelope.IssueCommentListOutput, mode clog.ColorMode) string {
	t.Helper()
	withStyle(t, mode == clog.ColorAlways)

	var buf bytes.Buffer
	v := output.NewView(&buf, mode)
	env := envelope.Envelope{OK: true, Data: out}
	if err := renderCommentList(v, env); err != nil {
		t.Fatalf("renderCommentList: %v", err)
	}
	return buf.String()
}

func TestRenderCommentListNoCommentsIsExplicit(t *testing.T) {
	got := renderComments(t, envelope.IssueCommentListOutput{}, clog.ColorNever)

	if !strings.Contains(got, "No comments.") {
		t.Errorf("output = %q, want the explicit no-comments line", got)
	}
	// No "Comments" section header — this command's whole output is
	// comments, so an empty result is just the one honest line, not a
	// titled-but-empty section. ("No comments." is lowercase, so a
	// capital-C "Comments" here could only be the dropped header.)
	if strings.Contains(got, "Comments") {
		t.Errorf("output = %q, want no redundant Comments section header", got)
	}
}

func TestRenderCommentListShowsHeaderAndBody(t *testing.T) {
	out := envelope.IssueCommentListOutput{
		Comments: []envelope.CommentView{
			{
				Author:  envelope.AuthorRef{DisplayName: "Ada Lovelace"},
				Created: "2024-01-01T00:00:00Z",
				Body: json.RawMessage(`{"type":"doc","version":1,"content":[
					{"type":"paragraph","content":[{"type":"text","text":"looks good"}]}
				]}`),
			},
		},
		Pagination: envelope.OffsetPagination{Total: 1, Returned: 1},
	}
	got := renderComments(t, out, clog.ColorNever)

	if !strings.Contains(got, "Ada Lovelace") {
		t.Errorf("output = %q, want the author's display name", got)
	}
	if !strings.Contains(got, "looks good") {
		t.Errorf("output = %q, want the rendered comment body", got)
	}
}

// TestRenderCommentListShowsPaginationHintWhenTruncated pins the
// discoverability banner the design demands: a page that is not the
// whole thread must say so and name the escape hatch.
func TestRenderCommentListShowsPaginationHintWhenTruncated(t *testing.T) {
	out := envelope.IssueCommentListOutput{
		Comments:   []envelope.CommentView{{Author: envelope.AuthorRef{DisplayName: "A"}, Body: json.RawMessage(`{"type":"doc"}`)}},
		Pagination: envelope.OffsetPagination{Total: 5, Returned: 1},
	}
	got := renderComments(t, out, clog.ColorNever)

	if !strings.Contains(got, "1 of 5") {
		t.Errorf("output = %q, want a hint naming 1 of 5", got)
	}
	if !strings.Contains(got, "--start") || !strings.Contains(got, "--limit") {
		t.Errorf("output = %q, want the hint to name --start/--limit", got)
	}
}

// TestRenderCommentListPaginationHintColorsCountsWhenStyled proves the
// "Showing N of TOTAL" banner's two counts go through cli.Number: styled
// output must carry an SGR sequence around the formatted digits, and —
// since this banner no longer wraps the assembled line in cli.Text (see
// renderCommentList's own comment on why: plainKey-free, nothing here is
// Jira-authored) — those bytes must actually survive to the caller
// rather than being stripped back out by a sanitizing wrapper.
func TestRenderCommentListPaginationHintColorsCountsWhenStyled(t *testing.T) {
	out := envelope.IssueCommentListOutput{
		Comments:   []envelope.CommentView{{Author: envelope.AuthorRef{DisplayName: "A"}, Body: json.RawMessage(`{"type":"doc"}`)}},
		Pagination: envelope.OffsetPagination{Total: 5000, Returned: 1},
	}
	got := renderComments(t, out, clog.ColorAlways)

	if !strings.ContainsRune(got, 0x1b) {
		t.Fatalf("output = %q, want the pagination banner styled", got)
	}
	// Each count is colored in its OWN self-closed span (cli.Number), so
	// "1 of 5,000" is no longer one contiguous substring once styled —
	// checked as separate pieces rather than the plain-mode phrase.
	if !strings.Contains(got, "Showing ") || !strings.Contains(got, " of ") {
		t.Errorf("output = %q, lost the banner's surrounding prose", got)
	}
	if !strings.Contains(got, "5,000") {
		t.Errorf("output = %q, want the total thousands-grouped", got)
	}
}

// TestRenderCommentListOmitsPaginationHintWhenComplete proves the hint
// is conditional: a page that IS the whole thread must not claim there
// is more to see.
func TestRenderCommentListOmitsPaginationHintWhenComplete(t *testing.T) {
	out := envelope.IssueCommentListOutput{
		Comments:   []envelope.CommentView{{Author: envelope.AuthorRef{DisplayName: "A"}, Body: json.RawMessage(`{"type":"doc"}`)}},
		Pagination: envelope.OffsetPagination{Total: 1, Returned: 1},
	}
	got := renderComments(t, out, clog.ColorNever)

	if strings.Contains(got, "use --start") {
		t.Errorf("output = %q, want no pagination hint when the page is complete", got)
	}
}

// TestRenderCommentListOmitsPaginationHintOnLastPageWithNonzeroStart is
// the regression pin for the bug 0.15 already fixed once: the hint must
// compare StartAt+Returned against Total, not Returned against Total
// alone. A page fetched with --start 40 --limit 50 of a 45-total thread
// returns only 5 comments (Returned=5, well under Total=45) but IS the
// last page (40+5==45) — the wrong formula would print "Showing 5 of
// 45" on a page that already reached the end. Every other pagination
// test in this file uses StartAt=0, where the two formulas cannot be
// told apart, which is why the bug passed unnoticed.
func TestRenderCommentListOmitsPaginationHintOnLastPageWithNonzeroStart(t *testing.T) {
	out := envelope.IssueCommentListOutput{
		Comments:   []envelope.CommentView{{Author: envelope.AuthorRef{DisplayName: "A"}, Body: json.RawMessage(`{"type":"doc"}`)}},
		Pagination: envelope.OffsetPagination{StartAt: 40, MaxResults: 50, Total: 45, Returned: 5},
	}
	got := renderComments(t, out, clog.ColorNever)

	if strings.Contains(got, "use --start") {
		t.Errorf("output = %q, want no pagination hint — start_at(40)+returned(5) already reaches total(45)", got)
	}
}

// TestRenderCommentListShowsPaginationHintOnNonLastPageWithNonzeroStart
// is the positive counterpart: a page with StartAt>0 that is NOT the
// last one must still show the hint.
func TestRenderCommentListShowsPaginationHintOnNonLastPageWithNonzeroStart(t *testing.T) {
	out := envelope.IssueCommentListOutput{
		Comments:   []envelope.CommentView{{Author: envelope.AuthorRef{DisplayName: "A"}, Body: json.RawMessage(`{"type":"doc"}`)}},
		Pagination: envelope.OffsetPagination{StartAt: 40, MaxResults: 50, Total: 100, Returned: 5},
	}
	got := renderComments(t, out, clog.ColorNever)

	if !strings.Contains(got, "5 of 100") {
		t.Errorf("output = %q, want a hint naming 5 of 100 — start_at(40)+returned(5) is still short of total(100)", got)
	}
}

// TestRenderCommentListSanitizesHostileAuthorAndBody is the end-to-end
// H4 proof for this command, mirroring
// TestRenderViewDescriptionProducesZeroANSIOffTTY: with color disabled a
// hostile author display name AND a hostile ADF body must both render
// with not one escape byte.
func TestRenderCommentListSanitizesHostileAuthorAndBody(t *testing.T) {
	hostileAuthor := "Eve\x1b[2K\rSUDO PASSWORD:\x07"
	// The ADF text's hostile bytes are JSON-escaped (\u001b, \r, \u0007):
	// a literal control byte inside a JSON string is not valid JSON, and
	// building the body through json.Marshal (rather than a hand-typed
	// raw string) guarantees that escaping is correct so this test
	// exercises the real parse-then-render path, not a malformed-document
	// fallback.
	hostileBody, err := json.Marshal(map[string]any{
		"type":    "doc",
		"version": 1,
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{"type": "text", "text": "hostile\x1b[2K\rSUDO PASSWORD:\x07"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("build fixture body: %v", err)
	}
	out := envelope.IssueCommentListOutput{
		Comments: []envelope.CommentView{
			{
				Author:  envelope.AuthorRef{DisplayName: hostileAuthor},
				Created: "2024-01-01T00:00:00Z",
				Body:    hostileBody,
			},
		},
		Pagination: envelope.OffsetPagination{Total: 1, Returned: 1},
	}
	got := renderComments(t, out, clog.ColorNever)

	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("output = %q, an escape sequence reached the terminal", got)
	}
	if strings.ContainsRune(got, '\r') || strings.ContainsRune(got, '\x07') {
		t.Errorf("output = %q, a control byte reached the terminal", got)
	}
}

// TestRenderCommentListSanitizesUnparsableHostileTimestamp pins the one
// gap cli.Time itself leaves open: its unparsable-input fallback returns
// the argument AS GIVEN, so a `created` value that both fails RFC 3339
// parsing AND carries control bytes must be sanitized BEFORE it reaches
// cli.Time, or those bytes would reach the terminal through that
// fallback path untouched.
func TestRenderCommentListSanitizesUnparsableHostileTimestamp(t *testing.T) {
	hostile := "not-a-timestamp\x1b[2K\rSUDO PASSWORD:\x07"
	out := envelope.IssueCommentListOutput{
		Comments: []envelope.CommentView{
			{
				Author:  envelope.AuthorRef{DisplayName: "Author"},
				Created: hostile,
				Body:    json.RawMessage(`{"type":"doc"}`),
			},
		},
		Pagination: envelope.OffsetPagination{Total: 1, Returned: 1},
	}
	got := renderComments(t, out, clog.ColorNever)

	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\r') || strings.ContainsRune(got, '\x07') {
		t.Errorf("output = %q, the hostile created timestamp's control bytes reached the terminal", got)
	}
}

// sgrCode matches one ANSI SGR escape sequence (CSI ... m) — the only
// escape shape lipgloss/clog emit in this test's rendered output.
var sgrCode = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// resetsStayTrailing is item 1/8's byte-level nesting guard, run
// against a full rendered line rather than one isolated span: it walks
// s's alternating escape/visible runs and reports whether every SGR
// "reset" code (empty or "0" parameters) is followed only by MORE
// escape codes or whitespace — never by visible content — until either
// the string ends or a fresh style-open code starts a new span (which
// cancels the "reset" state, since a legitimate rendered line is built
// from several independently self-closed spans: clog's own dim rule
// segments on both sides of the title, plus the title's own styling).
//
// This is the actual byte shape of the regression clog's Divider caused:
// it wraps a title in Bold with exactly ONE trailing reset. Feeding it
// the PRE-FIX title (cli.Entity(name)+" • "+cli.Time(...), whose own
// color span self-closes mid-string) leaves everything after that
// self-close as visible content following a reset with no fresh open in
// between — a violation this reports false for. The fix (cli.EntityAs
// coloring the WHOLE title in one Render call) emits only one reset, at
// the very end, which this reports true for. Verified against real
// captured bytes from both shapes before this function was trusted as a
// test oracle.
func resetsStayTrailing(s string) bool {
	matches := sgrCode.FindAllStringIndex(s, -1)
	seenReset, last := false, 0
	for _, m := range matches {
		if visible := s[last:m[0]]; seenReset && strings.TrimSpace(visible) != "" {
			return false
		}
		code := s[m[0]:m[1]]
		params := strings.TrimSuffix(strings.TrimPrefix(code, "\x1b["), "m")
		seenReset = params == "" || params == "0"
		last = m[1]
	}
	return !seenReset || strings.TrimSpace(s[last:]) == ""
}

// TestRenderCommentListDividerTitleHasNoResetBeforeFinal is item 1's own
// regression pin, run with color ENABLED — every other comment-render
// test in this file forces clog.ColorNever, which is exactly why this
// bug shipped with zero coverage. Before the fix, v.Section received
// cli.Entity(author)+" • "+cli.Time(created): the author's own
// self-closing color span reset mid-title, and clog's Divider (which
// unconditionally bolds whatever title it is given) could not re-open
// bold for everything after it, so "• <time>" silently lost its bold.
func TestRenderCommentListDividerTitleHasNoResetBeforeFinal(t *testing.T) {
	out := envelope.IssueCommentListOutput{
		Comments: []envelope.CommentView{
			{
				Author:  envelope.AuthorRef{DisplayName: "Ada Lovelace"},
				Created: "2024-01-01T00:00:00Z",
				Body:    json.RawMessage(`{"type":"doc"}`),
			},
		},
		Pagination: envelope.OffsetPagination{Total: 1, Returned: 1},
	}
	got := renderComments(t, out, clog.ColorAlways)

	// v.Section writes exactly one line — the divider itself — before
	// renderBody's own output starts; take only that first line, since
	// the placeholder text a still-malformed-enough fixture body renders
	// below it (this one has no "content", so ADF's own placeholder
	// fallback fires) is legitimately unstyled and would otherwise read
	// as "content after the final reset", a false positive unrelated to
	// the divider-title bug this test exists to catch.
	dividerLine, _, found := strings.Cut(got, "\n")
	if !found {
		t.Fatalf("output = %q, has no line break after the divider title", got)
	}
	if !sgrCode.MatchString(dividerLine) {
		t.Fatalf("divider line = %q, has no ANSI at all with color enabled — this test would prove nothing", dividerLine)
	}
	if !strings.Contains(dividerLine, "Ada Lovelace") || !strings.Contains(dividerLine, "ago") {
		t.Fatalf("divider line = %q, lost the header's content", dividerLine)
	}
	if !resetsStayTrailing(dividerLine) {
		t.Errorf("divider line = %q, a reset lands before the title's final one — the divider-title nesting bug is back", dividerLine)
	}
}

// TestRenderCommentListSanitizesHostileAuthorAndBodyWithColorEnabled is
// item 8's H4-holds-on-the-colored-path proof: every existing
// sanitization test in this file forces clog.ColorNever, which cannot
// tell apart "sanitized" from "styling was simply off the whole time".
// With color ON, the hostile payload's own control/escape bytes must
// still never reach the terminal, even though LEGITIMATE styling escape
// bytes are now expected and correct.
func TestRenderCommentListSanitizesHostileAuthorAndBodyWithColorEnabled(t *testing.T) {
	hostileAuthor := "Eve\x1b[2K\rSUDO PASSWORD:\x07"
	hostileBody, err := json.Marshal(map[string]any{
		"type":    "doc",
		"version": 1,
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{"type": "text", "text": "hostile\x1b[2K\rSUDO PASSWORD:\x07"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("build fixture body: %v", err)
	}
	out := envelope.IssueCommentListOutput{
		Comments: []envelope.CommentView{
			{
				Author:  envelope.AuthorRef{DisplayName: hostileAuthor},
				Created: "2024-01-01T00:00:00Z",
				Body:    hostileBody,
			},
		},
		Pagination: envelope.OffsetPagination{Total: 1, Returned: 1},
	}
	got := renderComments(t, out, clog.ColorAlways)

	if !sgrCode.MatchString(got) {
		t.Fatalf("output = %q, has no ANSI at all with color enabled — this test would prove nothing", got)
	}
	if strings.Contains(got, "\x1b[2K") {
		t.Errorf("output = %q, the hostile cursor-clear escape reached the terminal", got)
	}
	if strings.ContainsRune(got, '\r') || strings.ContainsRune(got, '\x07') {
		t.Errorf("output = %q, a hostile control byte reached the terminal", got)
	}
}

// pagingCommentClient serves a synthetic thread one page at a time and
// records every (startAt, maxResults, order) it was asked for — the
// double drainCommentList needs, which fakeCommentClient above cannot be:
// that one returns a single canned page whatever it is asked, which is
// exactly what a walk must not be tested against.
type pagingCommentClient struct {
	unusedJiraClient

	// total is the thread's length as the SERVER reports it; comments are
	// generated on demand so a five-thousand-comment thread costs nothing
	// to script.
	total int
	// short caps how many comments any single page actually returns,
	// independent of the maxResults asked for, so a test can produce the
	// "page came back empty while total claims more" shape.
	short *int

	calls []commentCall
}

type commentCall struct {
	startAt, maxResults int
	order               core.CommentOrder
}

func (f *pagingCommentClient) GetComments(_ context.Context, _ string, startAt, maxResults int, order core.CommentOrder) (core.CommentRead, error) {
	f.calls = append(f.calls, commentCall{startAt: startAt, maxResults: maxResults, order: order})

	size := maxResults
	if f.short != nil {
		size = *f.short
	}
	if remaining := f.total - startAt; size > remaining {
		size = remaining
	}
	if size < 0 {
		size = 0
	}
	comments := make([]jira.Comment, size)
	for i := range comments {
		comments[i] = jira.Comment{ID: jira.NullableOf(fmt.Sprintf("%d", startAt+i))}
	}
	return core.CommentRead{Page: &jira.CommentPage{
		StartAt:    startAt,
		MaxResults: maxResults,
		Total:      f.total,
		Comments:   comments,
	}}, nil
}

func pagingDeps(f *pagingCommentClient) Deps {
	return Deps{JiraClient: func() (core.JiraClient, error) { return f, nil }}
}

// TestDrainCommentListPagesRatherThanFetchingEverythingAtOnce is the
// central proof for --all, and the one that would catch the cheap wrong
// implementation: a walk that asked for the whole thread in one request
// (maxResults=total) or fetched-then-sliced would make ONE call. 0.15's
// attachment list did exactly that and was dropped for it.
func TestDrainCommentListPagesRatherThanFetchingEverythingAtOnce(t *testing.T) {
	fake := &pagingCommentClient{total: 250}

	out, err := drainCommentList(t.Context(), pagingDeps(fake), "ABC-1", core.CommentOrderDefault)
	if err != nil {
		t.Fatalf("drainCommentList: %v", err)
	}
	if len(out.Comments) != 250 {
		t.Fatalf("collected %d comments, want the whole thread of 250", len(out.Comments))
	}
	if len(fake.calls) != 3 {
		t.Fatalf("made %d requests for a 250-comment thread, want 3 pages of %d — "+
			"one request means it asked for everything at once", len(fake.calls), maxCommentRequestPageSize)
	}
	for i, call := range fake.calls {
		if call.maxResults != maxCommentRequestPageSize {
			t.Errorf("call %d asked for maxResults=%d, want the bounded page size %d",
				i, call.maxResults, maxCommentRequestPageSize)
		}
		if want := i * maxCommentRequestPageSize; call.startAt != want {
			t.Errorf("call %d startAt = %d, want %d", i, call.startAt, want)
		}
	}
	if out.Truncated {
		t.Error("truncated = true — the walk reached the thread's own total, not a cap")
	}
}

// TestDrainCommentListReportsTheWholeWalkInPagination: an offset reader
// resumes from start_at + returned, so a drain must report the walk it
// actually did rather than its last page's own offset.
func TestDrainCommentListReportsTheWholeWalkInPagination(t *testing.T) {
	fake := &pagingCommentClient{total: 150}

	out, err := drainCommentList(t.Context(), pagingDeps(fake), "ABC-1", core.CommentOrderDefault)
	if err != nil {
		t.Fatalf("drainCommentList: %v", err)
	}
	if out.Pagination.StartAt != 0 {
		t.Errorf("start_at = %d, want 0 — the walk genuinely began at the start", out.Pagination.StartAt)
	}
	if out.Pagination.Returned != 150 {
		t.Errorf("returned = %d, want 150 (everything collected, not the last page's count)", out.Pagination.Returned)
	}
	if out.Pagination.Total != 150 {
		t.Errorf("total = %d, want the server's own 150", out.Pagination.Total)
	}
}

// TestDrainCommentListPassesTheOrderThrough pins that --order survives
// the walk rather than being applied to the first page and forgotten.
func TestDrainCommentListPassesTheOrderThrough(t *testing.T) {
	fake := &pagingCommentClient{total: 250}

	if _, err := drainCommentList(t.Context(), pagingDeps(fake), "ABC-1", core.CommentOrderOldestFirst); err != nil {
		t.Fatalf("drainCommentList: %v", err)
	}
	for i, call := range fake.calls {
		if call.order != core.CommentOrderOldestFirst {
			t.Errorf("call %d used order %q, want %q — a page mid-walk would be sorted the other way",
				i, call.order, core.CommentOrderOldestFirst)
		}
	}
}

// TestDrainCommentListReportsTruncationAtThePageCap: the cap must stop
// the walk and SAY so, because a capped result reported as complete is
// the failure mode the whole Truncated field exists for.
func TestDrainCommentListReportsTruncationAtThePageCap(t *testing.T) {
	one := 1
	// One comment per page against a huge total: the page cap bites long
	// before the comment cap.
	fake := &pagingCommentClient{total: 1_000_000, short: &one}

	out, err := drainCommentList(t.Context(), pagingDeps(fake), "ABC-1", core.CommentOrderDefault)
	if err != nil {
		t.Fatalf("drainCommentList: %v", err)
	}
	if len(fake.calls) != maxAllCommentPages {
		t.Fatalf("made %d requests, want exactly maxAllCommentPages (%d)", len(fake.calls), maxAllCommentPages)
	}
	if !out.Truncated {
		t.Fatal("truncated = false — the page cap stopped the walk short of the thread")
	}
	if out.TruncatedReason != envelope.TruncatedReasonMaxPages {
		t.Errorf("truncated_reason = %q, want %q", out.TruncatedReason, envelope.TruncatedReasonMaxPages)
	}
}

// TestDrainCommentListStopsOnAnEmptyPage: a server claiming more
// comments than it will hand over would spin the walk forever if
// trusted. The unusable page must be a classified failure, not a successfully
// completed walk or a safety cap.
func TestDrainCommentListStopsOnAnEmptyPage(t *testing.T) {
	none := 0
	fake := &pagingCommentClient{total: 500, short: &none}

	out, err := drainCommentList(t.Context(), pagingDeps(fake), "ABC-1", core.CommentOrderDefault)
	if err == nil || cli.MapError(err).Code != "comment_response_invalid" {
		t.Fatalf("empty page before total did not report a protocol failure: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("made %d requests against a server returning nothing, want 1 — the walk did not stop", len(fake.calls))
	}
	if !out.Truncated || out.TruncatedReason != envelope.TruncatedReasonResponseInvalid || out.Comments != nil {
		t.Error("unusable first page fabricated comments or omitted its incomplete state")
	}
}

func TestCommentOrderFromFlag(t *testing.T) {
	cases := []struct {
		in   string
		want core.CommentOrder
	}{
		{"", core.CommentOrderDefault},
		{orderNewest, core.CommentOrderNewestFirst},
		{orderOldest, core.CommentOrderOldestFirst},
	}
	for _, tc := range cases {
		got, err := commentOrderFromFlag(tc.in)
		if err != nil {
			t.Fatalf("commentOrderFromFlag(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("commentOrderFromFlag(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCommentOrderFromFlagRefusesAForeignValueAndNamesTheSet: an agent
// reading the Jira API docs would reach for `created`, and finding out it
// is wrong from a 400 about a parameter it never typed is a worse way to
// learn than a refusal naming both accepted words.
func TestCommentOrderFromFlagRefusesAForeignValueAndNamesTheSet(t *testing.T) {
	_, err := commentOrderFromFlag("created")
	if err == nil {
		t.Fatal("a wire-spelling was accepted — it would reach Jira as an invalid orderBy")
	}
	for _, want := range []string{orderNewest, orderOldest, "created"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal omits %q: %v", want, err)
		}
	}
}

func TestValidateCommentAllRefusesPagingFlags(t *testing.T) {
	if err := validateCommentAll(true, true, false); err == nil {
		t.Error("--all --start was accepted — one of the two would be silently ignored")
	}
	if err := validateCommentAll(true, false, true); err == nil {
		t.Error("--all --limit was accepted — one of the two would be silently ignored")
	}
	if err := validateCommentAll(true, false, false); err != nil {
		t.Errorf("a bare --all was refused: %v", err)
	}
	if err := validateCommentAll(false, true, true); err != nil {
		t.Errorf("the rule fired without --all: %v", err)
	}
}

// TestCommentPageHintNamesTheCapAndTheWayPast: --all's cap is not the
// caller's own paging choice, so telling them to "use --start/--limit to
// see more" would report a deliberate truncation as though they had asked
// for it. The offset to resume from has to be in the line, since that is
// the entire recovery.
func TestCommentPageHintNamesTheCapAndTheWayPast(t *testing.T) {
	out := envelope.IssueCommentListOutput{
		Truncated:       true,
		TruncatedReason: envelope.TruncatedReasonMaxPages,
		Pagination:      envelope.OffsetPagination{Returned: 50, Total: 900},
	}
	got := commentPageHint(out)
	for _, want := range []string{"--all", "--start 50"} {
		if !strings.Contains(got, want) {
			t.Errorf("capped hint = %q, want it to contain %q", got, want)
		}
	}
}

func TestCommentPageHintKeepsTheOrdinaryPagingLine(t *testing.T) {
	out := envelope.IssueCommentListOutput{
		Pagination: envelope.OffsetPagination{StartAt: 0, Returned: 50, Total: 900},
	}
	got := commentPageHint(out)
	if !strings.Contains(got, "--start/--limit") {
		t.Errorf("uncapped hint = %q, want the ordinary paging advice", got)
	}
	if strings.Contains(got, "--all") {
		t.Errorf("uncapped hint = %q, want no mention of a cap that never bit", got)
	}
}
