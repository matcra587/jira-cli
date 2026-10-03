package issue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/gechr/clog"
	"github.com/gechr/x/ansi"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// humanContext returns a context carrying a resolved FormatHuman, the
// same way root's PersistentPreRunE seeds it before Run is ever called
// (cli.WithFormat) — runView's --comments N fetch (item 4's gate) only
// fires when this resolves to human, so any test exercising that fetch
// must seed it explicitly; t.Context() alone carries no such value.
func humanContext(t *testing.T) context.Context {
	t.Helper()
	return cli.WithFormat(t.Context(), output.FormatHuman)
}

// withStyle sets cli.StyleEnabled for the duration of a test and
// restores the safe "no color" default afterward — mirroring
// internal/cli's own sinks_test.go helper, unexported there and so
// re-declared here rather than shared across packages.
func withStyle(t *testing.T, enabled bool) {
	t.Helper()
	cli.SetStyleEnabled(enabled)
	t.Cleanup(func() { cli.SetStyleEnabled(false) })
}

// renderIssue runs renderView against a minimal issue carrying only a
// description, returning what it wrote. mode governs both the View's own
// clog.Output color state (gates Link's OSC 8 hyperlinks) and — via
// withStyle — cli.StyleEnabled (gates the Markup* mark sinks), the same
// pairing root resolves once per real invocation (internal/cli/color.go).
func renderIssue(t *testing.T, description jira.Nullable[json.RawMessage], mode clog.ColorMode) string {
	t.Helper()
	withStyle(t, mode == clog.ColorAlways)

	var buf bytes.Buffer
	v := output.NewView(&buf, mode)
	env := envelope.Envelope{
		OK: true,
		Data: envelope.IssueViewOutput{
			Issue: envelope.IssueView{
				IssueRef: envelope.IssueRef{Key: "KEY-1"},
				Fields: jira.IssueFields{
					Summary:     jira.NullableOf("Test issue"),
					Description: description,
				},
			},
		},
	}
	if err := renderView(v, env, commentsPreview{}, ""); err != nil {
		t.Fatalf("renderView: %v", err)
	}
	return buf.String()
}

func TestRenderViewShowsDescriptionWhenPresent(t *testing.T) {
	desc := jira.NullableOf[json.RawMessage]([]byte(`{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[{"type":"text","text":"release notes go here"}]}
	]}`))

	got := renderIssue(t, desc, clog.ColorNever)

	if !strings.Contains(got, "Description") {
		t.Errorf("output has no Description section: %q", got)
	}
	if !strings.Contains(got, "release notes go here") {
		t.Errorf("output does not contain the rendered description body: %q", got)
	}
}

func TestRenderViewSkipsDescriptionWhenAbsent(t *testing.T) {
	// The zero value: Set is false, matching a field Jira never sent.
	got := renderIssue(t, jira.Nullable[json.RawMessage]{}, clog.ColorNever)

	if strings.Contains(got, "Description") {
		t.Errorf("output has a Description section for an absent field: %q", got)
	}
}

func TestRenderViewSkipsDescriptionWhenExplicitlyNull(t *testing.T) {
	got := renderIssue(t, jira.NullableNull[json.RawMessage](), clog.ColorNever)

	if strings.Contains(got, "Description") {
		t.Errorf("output has a Description section for an explicit null: %q", got)
	}
}

// TestRenderViewDescriptionParseFailureFallsBackToPlaceholder pins that
// malformed ADF (not this command's fault — the wire is not trusted
// blindly) neither panics nor silently drops the section, per the
// design's "on parse failure don't panic — skip or emit a single
// placeholder line" instruction.
func TestRenderViewDescriptionParseFailureFallsBackToPlaceholder(t *testing.T) {
	desc := jira.NullableOf[json.RawMessage]([]byte(`not json at all`))

	got := renderIssue(t, desc, clog.ColorNever)

	if !strings.Contains(got, "Description") {
		t.Errorf("output has no Description section for a malformed body: %q", got)
	}
	if !strings.Contains(got, "--output=json") {
		t.Errorf("output does not point at the machine escape hatch: %q", got)
	}
}

// TestRenderViewDescriptionProducesZeroANSIOffTTY is the end-to-end form
// of the same contract internal/cli/sinks_test.go and internal/adf's own
// tests pin individually: with color disabled on both axes root actually
// resolves together (clog.ColorNever for the View, cli.StyleEnabled
// false), a styled description — strong, code, a link — renders with NOT
// ONE escape byte.
func TestRenderViewDescriptionProducesZeroANSIOffTTY(t *testing.T) {
	desc := jira.NullableOf[json.RawMessage]([]byte(`{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[
			{"type":"text","text":"bold","marks":[{"type":"strong"}]},
			{"type":"text","text":" and "},
			{"type":"text","text":"a link","marks":[{"type":"link","attrs":{"href":"https://example.com"}}]}
		]}
	]}`))

	got := renderIssue(t, desc, clog.ColorNever)

	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("output carries an escape sequence with color disabled: %q", got)
	}
	if !strings.Contains(got, "bold") || !strings.Contains(got, "a link") {
		t.Errorf("output lost content along with the styling: %q", got)
	}
}

// TestRenderViewDescriptionStylesWhenColorEnabled is the positive
// counterpart: proves the wiring actually reaches real ANSI when color
// IS on, so the zero-ANSI test above is not passing merely because
// nothing was ever styled to begin with.
func TestRenderViewDescriptionStylesWhenColorEnabled(t *testing.T) {
	desc := jira.NullableOf[json.RawMessage]([]byte(`{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[{"type":"text","text":"bold","marks":[{"type":"strong"}]}]}
	]}`))

	got := renderIssue(t, desc, clog.ColorAlways)

	if !strings.ContainsRune(got, 0x1b) {
		t.Errorf("output has no escape sequence with color enabled: %q", got)
	}
}

// TestRenderViewSanitizesGlanceKey pins that the issue key reaching the
// glance line is sanitized exactly like every other Jira-authored field
// on it (Summary, Status, ...) — a plain identifier is not exempt from
// the H4 policy just because it usually looks like ABC-123; nothing
// stops a malformed or hostile response from putting control bytes
// there instead. Only the DISPLAY copy is sanitized; buildOutput's own
// envelope.IssueRef.Key is untouched by this — a separate concern this
// test does not exercise.
func TestRenderViewSanitizesGlanceKey(t *testing.T) {
	hostile := "ABC-123\x1b[2K\rSUDO PASSWORD:\x07"

	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)
	env := envelope.Envelope{
		OK: true,
		Data: envelope.IssueViewOutput{
			Issue: envelope.IssueView{
				IssueRef: envelope.IssueRef{Key: hostile},
				Fields:   jira.IssueFields{Summary: jira.NullableOf("Test issue")},
			},
		},
	}
	if err := renderView(v, env, commentsPreview{}, ""); err != nil {
		t.Fatalf("renderView: %v", err)
	}
	got := buf.String()
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("output = %q, an escape sequence from the issue key reached the terminal", got)
	}
	if strings.ContainsRune(got, '\r') {
		t.Errorf("output = %q, a carriage return from the issue key reached the terminal", got)
	}
	if strings.ContainsRune(got, '\x07') {
		t.Errorf("output = %q, a BEL from the issue key reached the terminal", got)
	}
}

// TestRenderViewLinksGlanceKeyWhenStyledAndSiteSet proves the glance
// line's key becomes a clickable browse-URL hyperlink exactly when both
// conditions IssueKey requires are met (styling on AND a real site), and
// stays the plain key otherwise — the render-layer half of the wiring
// view.go's Run closure feeds via its own site variable (see newView's
// doc comment on it). The comment-preview hint text (renderCommentsPreview,
// fed plainKey) is deliberately left unstyled either way — cli.IssueKey's
// own scope excludes prose — so it is not exercised by the styled case
// here.
func TestRenderViewLinksGlanceKeyWhenStyledAndSiteSet(t *testing.T) {
	const site = "https://example.atlassian.net"
	buildEnv := func() envelope.Envelope {
		return envelope.Envelope{
			OK: true,
			Data: envelope.IssueViewOutput{
				Issue: envelope.IssueView{
					IssueRef: envelope.IssueRef{Key: "ABC-123"},
					Fields:   jira.IssueFields{Summary: jira.NullableOf("Test issue")},
				},
			},
		}
	}

	t.Run("styled and site set: link", func(t *testing.T) {
		withStyle(t, true)
		var buf bytes.Buffer
		v := output.NewView(&buf, clog.ColorAlways)
		if err := renderView(v, buildEnv(), commentsPreview{}, site); err != nil {
			t.Fatalf("renderView: %v", err)
		}
		got := buf.String()
		wantURL := site + "/browse/ABC-123"
		if !strings.Contains(got, wantURL) {
			t.Errorf("output = %q, want the glance key linked to %q", got, wantURL)
		}
	})

	t.Run("styled but no site: plain key, no partial URL", func(t *testing.T) {
		withStyle(t, true)
		var buf bytes.Buffer
		v := output.NewView(&buf, clog.ColorAlways)
		if err := renderView(v, buildEnv(), commentsPreview{}, ""); err != nil {
			t.Fatalf("renderView: %v", err)
		}
		got := buf.String()
		if strings.Contains(got, "/browse/") || strings.Contains(got, "]8;;") {
			t.Errorf("output = %q, built a link with no site to link to", got)
		}
		if !strings.Contains(got, "ABC-123") {
			t.Errorf("output = %q, lost the key along with the link", got)
		}
	})

	t.Run("site set but not styled: plain key, no link", func(t *testing.T) {
		withStyle(t, false)
		var buf bytes.Buffer
		v := output.NewView(&buf, clog.ColorNever)
		if err := renderView(v, buildEnv(), commentsPreview{}, site); err != nil {
			t.Fatalf("renderView: %v", err)
		}
		got := buf.String()
		if strings.Contains(got, "]8;;") {
			t.Errorf("output = %q, carries a hyperlink escape sequence with styling disabled", got)
		}
		if !strings.Contains(got, "ABC-123") {
			t.Errorf("output = %q, lost the key", got)
		}
	})
}

func TestFitGlanceSummary(t *testing.T) {
	const (
		key    = "ABC-123"
		status = "To Do"
		long   = "a summary long enough that a narrow terminal must truncate it to fit"
	)
	// Width 0 (non-TTY) never truncates — the full summary stays.
	if got := fitGlanceSummary(long, key, status, 0); got != long {
		t.Errorf("width 0 truncated: %q", got)
	}
	// Ample width fits, unchanged.
	if got := fitGlanceSummary(long, key, status, 200); got != long {
		t.Errorf("wide truncated: %q", got)
	}
	// Narrow: truncated with the ellipsis, within the budget left after
	// the key, the status and the two gutters.
	const width = 40
	got := fitGlanceSummary(long, key, status, width)
	if got == long {
		t.Fatalf("narrow did not truncate: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated value does not end in the ellipsis: %q", got)
	}
	if budget := width - ansi.StringWidth(key) - ansi.StringWidth(status) - 4; ansi.StringWidth(got) > budget {
		t.Errorf("truncated width %d exceeds budget %d: %q", ansi.StringWidth(got), budget, got)
	}
	// Too narrow to fit anything (budget <= 0) leaves the summary alone
	// rather than truncating to nonsense.
	if got := fitGlanceSummary(long, key, status, 6); got != long {
		t.Errorf("sub-budget width truncated: %q", got)
	}
}

// fakeIssueClient is a minimal core.JiraClient double: GetIssue returns
// whatever read/err a test seeds and records the fields it was called
// with (gotFields), so a test can assert item 3's plural-normalization
// rewrite reached the wire request; GetComments likewise records the
// startAt/maxResults it was called with, mirroring comment_test.go's own
// fakeCommentClient so a test can assert --comments N mapped to
// GetComments(0,N). commentsCalled distinguishes "never called" from
// "called with (0,0)" — both look the same through
// gotCommentsStartAt/gotCommentsMaxResults alone, which the uncapped
// --fields comment path (that must make NO GetComments call at all)
// needs told apart. getIssueCalled is the identical distinction for
// GetIssue: gotFields alone cannot tell "never called" from "called with
// a nil fields slice" (--web's own no-fetch tests need exactly that told
// apart). baseURL feeds BaseURL() — the site newView's Run
// closure captures for the glance line's hyperlinked key (view.go) —
// defaulting to "" for every test that does not care, the same "no
// site" degrade cli.IssueKey already covers on its own.
type fakeIssueClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	read           core.IssueRead
	err            error
	gotFields      []string
	getIssueCalled bool
	baseURL        string

	commentsRead core.CommentRead
	commentsErr  error

	commentsCalled                            bool
	gotCommentsStartAt, gotCommentsMaxResults int
}

func (f *fakeIssueClient) BaseURL() string { return f.baseURL }

func (f *fakeIssueClient) GetIssue(_ context.Context, _ string, fields []string, _ ...string) (core.IssueRead, error) {
	f.getIssueCalled = true
	f.gotFields = fields
	return f.read, f.err
}

func (f *fakeIssueClient) GetComments(_ context.Context, _ string, startAt, maxResults int, _ core.CommentOrder) (core.CommentRead, error) {
	f.commentsCalled = true
	f.gotCommentsStartAt, f.gotCommentsMaxResults = startAt, maxResults
	return f.commentsRead, f.commentsErr
}

// TestRunViewWebBuildsURLWithoutFetching pins --web's own contract for
// `issue view`: the browse URL is built from key and the client's
// resolved site ALONE — GetIssue is never called (fake.gotFields stays
// nil, its own zero value, since GetIssue is the only thing that sets
// it), and the returned Output carries the URL at WebURL plus the key
// alone, no fields.
func TestRunViewWebBuildsURLWithoutFetching(t *testing.T) {
	fake := &fakeIssueClient{baseURL: "https://example.atlassian.net"}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	var calls []recordedOpen
	out, err := runViewWeb(t.Context(), deps, "ABC-123", fakeOpener(&calls, nil))
	if err != nil {
		t.Fatalf("runViewWeb: %v", err)
	}

	if fake.getIssueCalled {
		t.Errorf("GetIssue was called (fields = %v) — --web must not fetch the issue", fake.gotFields)
	}
	if fake.commentsCalled {
		t.Error("GetComments was called — --web must not fetch anything")
	}
	const want = "https://example.atlassian.net/browse/ABC-123"
	if out.WebURL != want {
		t.Errorf("WebURL = %q, want %q", out.WebURL, want)
	}
	if out.Issue.Key != "ABC-123" {
		t.Errorf("Issue.Key = %q, want %q", out.Issue.Key, "ABC-123")
	}
}

// TestRunViewWebRejectsMalformedKey pins the fail-loud posture --web
// takes for a key that cannot become a browse URL (cli.IssueBrowseURL's
// own shape gate): producing the URL is this invocation's entire point,
// so this must not silently degrade to no link the way a passive render
// would.
func TestRunViewWebRejectsMalformedKey(t *testing.T) {
	fake := &fakeIssueClient{baseURL: "https://example.atlassian.net"}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	var calls []recordedOpen
	_, err := runViewWeb(t.Context(), deps, "not a valid key", fakeOpener(&calls, nil))
	if err == nil {
		t.Fatal("runViewWeb(malformed key) returned nil error, want a classified rejection")
	}
	if got := cli.MapError(err).Code; got != cli.CodePathInvalid {
		t.Errorf("classified code = %q, want %q", got, cli.CodePathInvalid)
	}
	if len(calls) != 0 {
		t.Errorf("opener called %d times, want 0 — a rejected key must never reach the opener", len(calls))
	}
}

// TestRunViewWebOpensOnlyForHumanOutput closes the loop between --web's
// machine/human split (openForHuman, web.go) and this command's own
// wiring: a non-human resolved format still gets the URL back, but never
// calls the opener.
func TestRunViewWebOpensOnlyForHumanOutput(t *testing.T) {
	fake := &fakeIssueClient{baseURL: "https://example.atlassian.net"}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	var calls []recordedOpen
	out, err := runViewWeb(cli.WithFormat(t.Context(), output.FormatJSON), deps, "ABC-123", fakeOpener(&calls, nil))
	if err != nil {
		t.Fatalf("runViewWeb: %v", err)
	}
	if out.WebURL == "" {
		t.Error("WebURL is empty for json output — the URL must always ride the envelope")
	}
	if len(calls) != 0 {
		t.Errorf("opener called %d times for json output, want 0", len(calls))
	}
}

// TestRunViewFailsLoudlyOnNilIssue pins the guard against GetIssue's own
// contract (internal/adapters/jira/client.go: a non-nil Issue on every
// nil-error return) being violated: runView must return an error, not
// silently hand buildOutput a nil Issue and let it produce an
// envelope.OK, all-fields-zero IssueViewOutput that would read as a
// real (if empty) issue rather than the failure it actually is.
func TestRunViewFailsLoudlyOnNilIssue(t *testing.T) {
	deps := Deps{
		JiraClient: func() (core.JiraClient, error) {
			return &fakeIssueClient{read: core.IssueRead{Issue: nil}}, nil
		},
	}

	_, _, err := runView(t.Context(), deps, "ABC-123", nil, viewOpts{})
	if err == nil {
		t.Fatal("runView returned nil error for a nil Issue — an empty issue would have been reported as success")
	}
	if !strings.Contains(err.Error(), "ABC-123") {
		t.Errorf("error = %v, want it to name the issue key", err)
	}
}

// TestBuildOutputOnNilIssueStaysZeroValue pins buildOutput's own,
// narrower contract now that runView is the one refusing to call it
// with a nil Issue (TestRunViewFailsLoudlyOnNilIssue): buildOutput
// itself keeps returning the zero value for a nil Issue, exactly as
// documented, in case some future caller besides runView ever reaches
// it directly.
func TestBuildOutputOnNilIssueStaysZeroValue(t *testing.T) {
	got := buildOutput(core.IssueRead{Issue: nil})
	want := envelope.IssueViewOutput{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildOutput(nil Issue) = %+v, want the zero value %+v", got, want)
	}
}

// renderPreview runs renderCommentsPreview against a built View,
// returning what it wrote — this file's sibling of comment_test.go's
// renderComments helper, but for --comments N's inline preview rather
// than the dedicated comment-list command.
func renderPreview(t *testing.T, key string, preview commentsPreview, mode clog.ColorMode) string {
	t.Helper()
	withStyle(t, mode == clog.ColorAlways)

	var buf bytes.Buffer
	v := output.NewView(&buf, mode)
	renderCommentsPreview(v, key, preview)
	return buf.String()
}

// TestRenderCommentsPreviewDefaultHint pins the N==0 default: a known,
// nonzero total gets one hint line naming the count (singular at
// exactly 1) and the full-thread verb.
func TestRenderCommentsPreviewDefaultHint(t *testing.T) {
	tests := []struct {
		name  string
		total int
		want  string
	}{
		{name: "plural", total: 3, want: "3 comments — jira issue comment list ABC-1"},
		{name: "singular", total: 1, want: "1 comment — jira issue comment list ABC-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderPreview(t, "ABC-1", commentsPreview{total: tt.total, totalKnown: true}, clog.ColorNever)
			if !strings.Contains(got, tt.want) {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRenderCommentsPreviewDefaultHintColorsCountWhenStyled proves
// commentCountHint's total goes through cli.Number: styled output must
// carry an SGR sequence, and — since commentCountHint no longer wraps
// its assembled string in cli.Text (plainKey arrives already sanitized
// from renderView; see commentCountHint's own doc comment) — those
// bytes must actually reach the caller.
func TestRenderCommentsPreviewDefaultHintColorsCountWhenStyled(t *testing.T) {
	got := renderPreview(t, "ABC-1", commentsPreview{total: 3, totalKnown: true}, clog.ColorAlways)

	if !strings.ContainsRune(got, 0x1b) {
		t.Fatalf("output = %q, want the comment count styled", got)
	}
	// The count is colored in its OWN self-closed span (cli.Number), so
	// "3 comments — ..." is no longer one contiguous substring once
	// styled — checked as separate pieces rather than the plain phrase.
	if !strings.Contains(got, "3") {
		t.Errorf("output = %q, lost the comment count", got)
	}
	if !strings.Contains(got, "comments — jira issue comment list ABC-1") {
		t.Errorf("output = %q, lost the hint's surrounding text once styled", got)
	}
}

// TestRenderCommentsPreviewDefaultSilentWhenNoneOrUnknown pins the two
// suppression cases the default hint must stay silent for: a known-zero
// total (the issue genuinely has no comments) and an unknown total (the
// caller's own --fields narrowed fields.comment away) — neither is an
// error, and neither prints anything.
func TestRenderCommentsPreviewDefaultSilentWhenNoneOrUnknown(t *testing.T) {
	if got := renderPreview(t, "ABC-1", commentsPreview{total: 0, totalKnown: true}, clog.ColorNever); got != "" {
		t.Errorf("output = %q, want nothing for a known-zero total", got)
	}
	if got := renderPreview(t, "ABC-1", commentsPreview{}, clog.ColorNever); got != "" {
		t.Errorf("output = %q, want nothing when the total could not be learned", got)
	}
}

// commentBody builds a minimal ADF paragraph document carrying text —
// shared by this file's --comments N tests so each one states only the
// text it cares about rather than repeating the document shape.
func commentBody(t *testing.T, text string) json.RawMessage {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"type":    "doc",
		"version": 1,
		"content": []any{
			map[string]any{
				"type":    "paragraph",
				"content": []any{map[string]any{"type": "text", "text": text}},
			},
		},
	})
	if err != nil {
		t.Fatalf("build fixture body: %v", err)
	}
	return body
}

// TestRenderCommentsPreviewShowsLatestAndBannerWhenMore pins --comments
// N>0's positive case: the latest N render, each with its own
// author/body, and the "Showing N of TOTAL" banner appears when the
// fetched page is short of the issue's real total.
func TestRenderCommentsPreviewShowsLatestAndBannerWhenMore(t *testing.T) {
	preview := commentsPreview{
		requested:  2,
		total:      5,
		totalKnown: true,
		latest: []envelope.CommentView{
			{Author: envelope.AuthorRef{DisplayName: "Ada"}, Created: "2024-01-01T00:00:00Z", Body: commentBody(t, "first")},
			{Author: envelope.AuthorRef{DisplayName: "Grace"}, Created: "2024-01-02T00:00:00Z", Body: commentBody(t, "second")},
		},
	}

	got := renderPreview(t, "ABC-1", preview, clog.ColorNever)

	if !strings.Contains(got, "Ada") || !strings.Contains(got, "Grace") {
		t.Errorf("output = %q, want both latest comments' authors", got)
	}
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Errorf("output = %q, want both latest comments' bodies", got)
	}
	if !strings.Contains(got, "Showing 2 of 5") {
		t.Errorf("output = %q, want the Showing 2 of 5 banner", got)
	}
	if !strings.Contains(got, "jira issue comment list ABC-1") {
		t.Errorf("output = %q, want the banner to name the full-thread verb", got)
	}
}

// TestRenderCommentsPreviewBannerColorsCountsWhenStyled proves the
// "Showing N of TOTAL" banner's two counts go through cli.Number, the
// same way TestRenderCommentsPreviewDefaultHintColorsCountWhenStyled
// pins it for commentCountHint's single count — this banner is built by
// renderCommentsPreview directly rather than through commentCountHint.
func TestRenderCommentsPreviewBannerColorsCountsWhenStyled(t *testing.T) {
	preview := commentsPreview{
		requested:  2,
		total:      5000,
		totalKnown: true,
		latest: []envelope.CommentView{
			{Author: envelope.AuthorRef{DisplayName: "Ada"}, Created: "2024-01-01T00:00:00Z", Body: commentBody(t, "first")},
			{Author: envelope.AuthorRef{DisplayName: "Grace"}, Created: "2024-01-02T00:00:00Z", Body: commentBody(t, "second")},
		},
	}

	got := renderPreview(t, "ABC-1", preview, clog.ColorAlways)

	if !strings.ContainsRune(got, 0x1b) {
		t.Fatalf("output = %q, want the banner styled", got)
	}
	// Each count is colored in its OWN self-closed span (cli.Number), so
	// "2 of 5,000" is no longer one contiguous substring once styled —
	// checked as separate pieces rather than the plain-mode phrase.
	if !strings.Contains(got, "Showing ") || !strings.Contains(got, " of ") {
		t.Errorf("output = %q, lost the banner's surrounding prose", got)
	}
	if !strings.Contains(got, "5,000") {
		t.Errorf("output = %q, want the total thousands-grouped", got)
	}
}

// TestRenderCommentsPreviewOmitsBannerWhenComplete is the negative
// counterpart: a fetch that already covers the whole total must not
// claim there is more to see.
func TestRenderCommentsPreviewOmitsBannerWhenComplete(t *testing.T) {
	preview := commentsPreview{
		requested:  2,
		total:      2,
		totalKnown: true,
		latest: []envelope.CommentView{
			{Author: envelope.AuthorRef{DisplayName: "Ada"}, Body: commentBody(t, "first")},
			{Author: envelope.AuthorRef{DisplayName: "Grace"}, Body: commentBody(t, "second")},
		},
	}

	got := renderPreview(t, "ABC-1", preview, clog.ColorNever)

	if strings.Contains(got, "Showing") {
		t.Errorf("output = %q, want no Showing banner when the fetch already covers the total", got)
	}
}

// TestRenderCommentsPreviewKnownZeroTotalIsSilent pins the quiet, honest
// outcome for a known-zero total: no "Comments" header, no "No
// comments." line, no banner — regardless of requested, since --comments
// N>0 (default or explicit) against an issue that genuinely has no
// comments must not print noise just because N asked for more than
// exists. This SUPERSEDES the previous contract (an older revision of
// this test, back when --comments defaulted to 0, asserted the OPPOSITE:
// that an explicit --comments N>0 against zero comments printed "No
// comments." as its own explicit answer) — with the default now 1,
// "issue with zero comments" is the common bare `issue view` case, not
// an unusual explicit ask, so silence is the outcome that reads right
// for both.
func TestRenderCommentsPreviewKnownZeroTotalIsSilent(t *testing.T) {
	// asked false: the DEFAULT preview, which nobody requested. An issue
	// with no comments must not grow a section saying so.
	got := renderPreview(t, "ABC-1", commentsPreview{requested: 1, total: 0, totalKnown: true}, clog.ColorNever)
	if got != "" {
		t.Errorf("output = %q, want nothing for a known-zero total — no noisy empty Comments section", got)
	}
}

// TestRenderCommentsPreviewKnownZeroTotalAnswersAnExplicitAsk is the
// other half of the rule above: silence is right only when nobody asked.
// An explicit --comments (or --fields naming the comment field) that
// finds an empty thread must SAY so, because a typed question answered
// with nothing at all is indistinguishable from the flag being ignored,
// the fetch failing, or the build not supporting it.
func TestRenderCommentsPreviewKnownZeroTotalAnswersAnExplicitAsk(t *testing.T) {
	for _, tt := range []struct {
		name    string
		preview commentsPreview
	}{
		{"explicit --comments", commentsPreview{requested: 5, total: 0, totalKnown: true, asked: true}},
		{"--fields comment", commentsPreview{uncapped: true, total: 0, totalKnown: true, asked: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := renderPreview(t, "ABC-1", tt.preview, clog.ColorNever)
			if !strings.Contains(got, "No comments.") {
				t.Errorf("output = %q, want an explicit no-comments answer to an explicit ask", got)
			}
		})
	}
}

// TestRenderCommentsPreviewExactlyOneCommentNoBanner pins the default
// N==1 edge this feature's whole existence turns on: an issue with
// EXACTLY one comment renders that comment inline and shows no "Showing"
// banner (1 is not less than its own total of 1) — the shape a bare
// `jira issue view KEY` now produces for the single most common case an
// issue has any comments at all.
func TestRenderCommentsPreviewExactlyOneCommentNoBanner(t *testing.T) {
	preview := commentsPreview{
		requested:  1,
		total:      1,
		totalKnown: true,
		latest: []envelope.CommentView{
			{Author: envelope.AuthorRef{DisplayName: "Ada"}, Created: "2024-01-01T00:00:00Z", Body: commentBody(t, "only one")},
		},
	}

	got := renderPreview(t, "ABC-1", preview, clog.ColorNever)

	if !strings.Contains(got, "Ada") || !strings.Contains(got, "only one") {
		t.Errorf("output = %q, want the single comment rendered", got)
	}
	if strings.Contains(got, "Showing") {
		t.Errorf("output = %q, want no Showing banner — one comment out of a total of one is everything", got)
	}
}

// TestRenderCommentsPreviewFallsBackToCountHintWhenTotalPositiveButNothingRendered
// pins item 6: a known-POSITIVE total with nothing in preview.latest — a
// decode failure, or an empty embedded array Jira sent alongside a
// positive total, are both indistinguishable from this struct's own
// shape — must NOT print "No comments.", since the total itself
// contradicts that statement of fact. It falls back to the same
// count-hint line the ordinary default (case 1) uses instead.
func TestRenderCommentsPreviewFallsBackToCountHintWhenTotalPositiveButNothingRendered(t *testing.T) {
	got := renderPreview(t, "ABC-1", commentsPreview{requested: 2, total: 3, totalKnown: true}, clog.ColorNever)
	if strings.Contains(got, "No comments.") {
		t.Errorf("output = %q, fabricated \"No comments.\" despite a known total of 3", got)
	}
	if !strings.Contains(got, "3 comments — jira issue comment list ABC-1") {
		t.Errorf("output = %q, want the count-hint fallback naming the total", got)
	}
}

// TestRenderCommentsPreviewSanitizesHostileAuthorAndBody is the
// --comments N end-to-end H4 proof, mirroring
// TestRenderCommentListSanitizesHostileAuthorAndBody: with color
// disabled, a hostile author display name AND a hostile ADF body must
// both render with not one escape byte.
func TestRenderCommentsPreviewSanitizesHostileAuthorAndBody(t *testing.T) {
	hostileAuthor := "Eve\x1b[2K\rSUDO PASSWORD:\x07"
	preview := commentsPreview{
		requested:  1,
		total:      1,
		totalKnown: true,
		latest: []envelope.CommentView{
			{
				Author:  envelope.AuthorRef{DisplayName: hostileAuthor},
				Created: "2024-01-01T00:00:00Z",
				Body:    commentBody(t, "hostile\x1b[2K\rSUDO PASSWORD:\x07"),
			},
		},
	}

	got := renderPreview(t, "ABC-1", preview, clog.ColorNever)

	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("output = %q, an escape sequence reached the terminal", got)
	}
	if strings.ContainsRune(got, '\r') || strings.ContainsRune(got, '\x07') {
		t.Errorf("output = %q, a control byte reached the terminal", got)
	}
}

// TestRunViewCommentsMapsToGetCommentsWithZeroStart pins --comments N's
// fetch call: it always asks GetComments for startAt 0 (the latest N,
// never an offset page) with maxResults set to N, and the returned
// page's own Total becomes the preview's total. Run against
// humanContext, since item 4's gate skips this fetch entirely outside a
// human render — see TestRunViewSkipsCommentsFetchWhenOutputIsNotHuman.
func TestRunViewCommentsMapsToGetCommentsWithZeroStart(t *testing.T) {
	fake := &fakeIssueClient{
		read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1")}},
		commentsRead: core.CommentRead{Page: &jira.CommentPage{
			Total: 5,
			Comments: []jira.Comment{
				{ID: jira.NullableOf("1"), Author: jira.NullableOf(jira.UserDetails{DisplayName: jira.NullableOf("Ada")})},
				{ID: jira.NullableOf("2"), Author: jira.NullableOf(jira.UserDetails{DisplayName: jira.NullableOf("Grace")})},
			},
		}},
	}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, preview, err := runView(humanContext(t), deps, "ABC-1", nil, viewOpts{n: 2})
	if err != nil {
		t.Fatalf("runView: %v", err)
	}
	if fake.gotCommentsStartAt != 0 || fake.gotCommentsMaxResults != 2 {
		t.Errorf("GetComments called with (startAt=%d, maxResults=%d), want (0, 2)", fake.gotCommentsStartAt, fake.gotCommentsMaxResults)
	}
	if !preview.totalKnown || preview.total != 5 {
		t.Errorf("preview total = %+v, want total 5, known", preview)
	}
	if len(preview.latest) != 2 {
		t.Fatalf("preview latest = %+v, want 2 comments", preview.latest)
	}
	if preview.latest[0].Author.DisplayName != "Ada" || preview.latest[1].Author.DisplayName != "Grace" {
		t.Errorf("preview latest = %+v, want Ada then Grace in the order GetComments returned them", preview.latest)
	}
}

// TestRunViewSkipsCommentsFetchWhenOutputIsNotHuman pins item 4's gate:
// --comments N's own GetComments call exists only to feed the human
// render's inline preview, so it must never fire when the resolved
// output mode is not human — an --output=json/compact caller must not
// pay for a network call whose result they can never see, and must not
// have their whole request die if that unwatched fetch fails. Run
// against a FormatJSON context (rather than a bare t.Context(), which
// TestRunViewCommentsMapsToGetCommentsWithZeroStart's own doc comment
// notes ALSO skips the fetch, since FormatFromContext's ok is false
// there too) to prove this is a deliberate mode check, not just "no
// context resolved".
func TestRunViewSkipsCommentsFetchWhenOutputIsNotHuman(t *testing.T) {
	fake := &fakeIssueClient{
		read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1")}},
		commentsRead: core.CommentRead{Page: &jira.CommentPage{
			Total:    5,
			Comments: []jira.Comment{{ID: jira.NullableOf("1")}},
		}},
	}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	ctx := cli.WithFormat(t.Context(), output.FormatJSON)
	_, preview, err := runView(ctx, deps, "ABC-1", nil, viewOpts{n: 2})
	if err != nil {
		t.Fatalf("runView: %v", err)
	}
	if fake.commentsCalled {
		t.Error("GetComments was called for a non-human output mode — --comments N's fetch must only feed a render that will actually run")
	}
	if preview.totalKnown {
		t.Errorf("preview = %+v, want totalKnown false — no fetch happened and the fixture's issue carries no embedded fields.comment either", preview)
	}
}

// TestRunViewExplicitZeroCommentsReadsTotalFromEmbeddedFieldWithoutFetching
// pins the no-double-fetch requirement for the count-hint-only mode: an
// explicit --comments 0 (the only way to reach n==0 now that the flag's
// own default is 1 — see TestRunViewDefaultReadsLatestFromEmbeddedPageWithoutFetching
// for the actual default), when the issue response already carries
// fields.comment (Jira's normal default-fetch behavior), must read the
// total from it rather than making a second GetComments call.
func TestRunViewExplicitZeroCommentsReadsTotalFromEmbeddedFieldWithoutFetching(t *testing.T) {
	fields := jira.IssueFields{
		Extra: map[string]json.RawMessage{
			"comment": json.RawMessage(`{"startAt":0,"maxResults":20,"total":4,"comments":[]}`),
		},
	}
	fake := &fakeIssueClient{
		read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}},
	}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, preview, err := runView(t.Context(), deps, "ABC-1", nil, viewOpts{n: 0, explicit: true})
	if err != nil {
		t.Fatalf("runView: %v", err)
	}
	if !preview.totalKnown || preview.total != 4 {
		t.Errorf("preview = %+v, want total 4, known — read from the issue's own embedded fields.comment", preview)
	}
	// commentsCalled directly, not gotCommentsStartAt/gotCommentsMaxResults
	// alone: a call recorded as (0, 0) is indistinguishable from no call at
	// all through those two fields by themselves.
	if fake.commentsCalled {
		t.Error("GetComments was called — --comments 0 must not double-fetch when fields.comment is already present")
	}
}

// TestRunViewExplicitZeroCommentsOmitsTotalWhenFieldNarrowedAway pins the
// --fields-narrowed edge case for --comments 0: when the caller's own
// --fields left fields.comment out of the response, runView must not
// error — the preview's total simply stays unknown, and
// renderCommentsPreview's own hint branch already proves that renders
// nothing.
func TestRunViewExplicitZeroCommentsOmitsTotalWhenFieldNarrowedAway(t *testing.T) {
	fake := &fakeIssueClient{
		read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(jira.IssueFields{Summary: jira.NullableOf("no comment field here")})}},
	}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, preview, err := runView(t.Context(), deps, "ABC-1", []string{"summary"}, viewOpts{n: 0, explicit: true})
	if err != nil {
		t.Fatalf("runView: %v", err)
	}
	if preview.totalKnown {
		t.Errorf("preview = %+v, want totalKnown false when fields.comment was never fetched", preview)
	}
}

// commentAt builds a jira.Comment carrying an id, author, created
// timestamp and body — shared by this file's embedded-page ordering
// tests so each one states only what differs (id and created) between
// its fixture's comments.
func commentAt(t *testing.T, id, author, created string) jira.Comment {
	t.Helper()
	return jira.Comment{
		ID:      jira.NullableOf(id),
		Author:  jira.NullableOf(jira.UserDetails{DisplayName: jira.NullableOf(author)}),
		Created: jira.NullableOf(created),
		Body:    jira.NullableOf(commentBody(t, author)),
	}
}

// TestRunViewDefaultReadsLatestFromEmbeddedPageWithoutFetching is the
// primary regression pin for the new default: a bare `issue view` (the
// flag's own default, opts.n == 1, not explicit) against an issue whose
// embedded fields.comment page already carries the WHOLE thread (its own
// comment count equals its own total) must resolve the latest comment
// locally — no second GetComments call — which is what keeps the most
// common human invocation down to one round trip.
func TestRunViewDefaultReadsLatestFromEmbeddedPageWithoutFetching(t *testing.T) {
	fields := embeddedCommentFields(t, 2, []jira.Comment{
		commentAt(t, "1", "Ada", "2024-01-01T00:00:00.000+0000"),
		commentAt(t, "2", "Grace", "2024-01-02T00:00:00.000+0000"),
	})
	fake := &fakeIssueClient{read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}}}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, preview, err := runView(humanContext(t), deps, "ABC-1", nil, viewOpts{n: 1})
	if err != nil {
		t.Fatalf("runView: %v", err)
	}
	if fake.commentsCalled {
		t.Error("GetComments was called — the default N==1 must resolve locally when the embedded page carries the whole thread")
	}
	if !preview.totalKnown || preview.total != 2 {
		t.Errorf("preview = %+v, want total 2, known", preview)
	}
	if len(preview.latest) != 1 {
		t.Fatalf("preview.latest = %+v, want exactly 1 comment (the default N)", preview.latest)
	}
}

// TestRunViewDefaultLatestIsNewestNotFirst is the ordering regression
// this brief exists to prevent: the embedded page is seeded OLDEST
// first (Ada's comment before Grace's newer one), matching what a live
// tenant actually returns — if runView trusted that delivery order and
// simply took the page's own first N, it would render Ada (the
// OLDEST) as "the latest". It must render Grace instead.
func TestRunViewDefaultLatestIsNewestNotFirst(t *testing.T) {
	fields := embeddedCommentFields(t, 2, []jira.Comment{
		commentAt(t, "1", "Ada", "2024-01-01T00:00:00.000+0000"),   // oldest
		commentAt(t, "2", "Grace", "2024-01-02T00:00:00.000+0000"), // newest
	})
	fake := &fakeIssueClient{read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}}}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, preview, err := runView(humanContext(t), deps, "ABC-1", nil, viewOpts{n: 1})
	if err != nil {
		t.Fatalf("runView: %v", err)
	}
	if len(preview.latest) != 1 {
		t.Fatalf("preview.latest = %+v, want exactly 1 comment", preview.latest)
	}
	if got := preview.latest[0].Author.DisplayName; got != "Grace" {
		t.Errorf("preview.latest[0].Author.DisplayName = %q, want %q (the newest comment, despite the embedded page listing it second/oldest-first)", got, "Grace")
	}
}

// TestRunViewFallsBackToFetchWhenEmbeddedPageTruncated pins the other
// half of the embedded-vs-fetch decision: when the embedded page's own
// comment count falls short of its own total (Jira truncated it — a
// large thread, or a caller's own maxResults elsewhere), runView must
// NOT trust that partial page for "the latest N" and must fall back to
// a dedicated GetComments(0, N) fetch instead, exactly as it always has.
func TestRunViewFallsBackToFetchWhenEmbeddedPageTruncated(t *testing.T) {
	fields := embeddedCommentFields(t, 10, []jira.Comment{
		commentAt(t, "1", "Ada", "2024-01-01T00:00:00.000+0000"),
	}) // total 10, but only 1 comment embedded — truncated
	fake := &fakeIssueClient{
		read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}},
		commentsRead: core.CommentRead{Page: &jira.CommentPage{
			Total:    10,
			Comments: []jira.Comment{commentAt(t, "9", "Grace", "2024-06-01T00:00:00.000+0000")},
		}},
	}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, preview, err := runView(humanContext(t), deps, "ABC-1", nil, viewOpts{n: 1})
	if err != nil {
		t.Fatalf("runView: %v", err)
	}
	if !fake.commentsCalled || fake.gotCommentsStartAt != 0 || fake.gotCommentsMaxResults != 1 {
		t.Errorf("GetComments called=%v (startAt=%d, maxResults=%d), want called with (0, 1) — the embedded page was truncated short of its own total", fake.commentsCalled, fake.gotCommentsStartAt, fake.gotCommentsMaxResults)
	}
	if len(preview.latest) != 1 || preview.latest[0].Author.DisplayName != "Grace" {
		t.Errorf("preview.latest = %+v, want the freshly-fetched comment (Grace), not the truncated embedded one (Ada)", preview.latest)
	}
}

// TestSortCommentsDescendingOrdersNewestFirst pins the core sort in
// isolation from runView's own embedded-vs-fetch decision: three
// comments seeded out of order (middle, oldest, newest) come back
// newest, middle, oldest.
func TestSortCommentsDescendingOrdersNewestFirst(t *testing.T) {
	comments := []jira.Comment{
		commentAt(t, "1", "Middle", "2024-01-02T00:00:00.000+0000"),
		commentAt(t, "2", "Oldest", "2024-01-01T00:00:00.000+0000"),
		commentAt(t, "3", "Newest", "2024-01-03T00:00:00.000+0000"),
	}

	got := sortCommentsDescending(comments)

	want := []string{"Newest", "Middle", "Oldest"}
	if len(got) != len(want) {
		t.Fatalf("sortCommentsDescending(...) = %+v, want %d comments", got, len(want))
	}
	for i, name := range want {
		if got[i].Author.DisplayName != name {
			t.Errorf("sortCommentsDescending(...)[%d].Author.DisplayName = %q, want %q", i, got[i].Author.DisplayName, name)
		}
	}
}

// TestSortCommentsDescendingUnparseableCreatedSortsLastInOriginalOrder
// pins the documented tie-break for a `created` value that fails to
// parse: it is neither dropped nor reordered ahead of a parseable one —
// every unparseable comment sorts AFTER every parseable one, and among
// themselves keeps their original relative order (sort.SliceStable, not
// an arbitrary reshuffle). Two unparseable comments ("Bogus1" before
// "Bogus2" in the input) prove the stability half; a parseable comment
// seeded LAST in the input but chronologically newest ("Newest") proves
// unparseable values do not simply stay wherever they were typed.
func TestSortCommentsDescendingUnparseableCreatedSortsLastInOriginalOrder(t *testing.T) {
	comments := []jira.Comment{
		commentAt(t, "1", "Bogus1", "not-a-timestamp"),
		commentAt(t, "2", "Bogus2", ""),
		commentAt(t, "3", "Newest", "2024-01-01T00:00:00.000+0000"),
	}

	got := sortCommentsDescending(comments)

	want := []string{"Newest", "Bogus1", "Bogus2"}
	if len(got) != len(want) {
		t.Fatalf("sortCommentsDescending(...) = %+v, want %d comments", got, len(want))
	}
	for i, name := range want {
		if got[i].Author.DisplayName != name {
			t.Errorf("sortCommentsDescending(...)[%d].Author.DisplayName = %q, want %q — unparseable created values must sort last, in original relative order, never dropped", i, got[i].Author.DisplayName, name)
		}
	}
}

// embeddedCommentFields builds a jira.IssueFields carrying total/comments
// as its own embedded fields.comment — reused by this file's --fields
// comment precedence tests so each one states only the comments it cares
// about rather than hand-writing the wire shape. Marshaling
// jira.CommentPage is safe here (unlike re-marshaling an already-encoded
// jira.Comment's OWN bytes, which comment.go's byte-fidelity note warns
// against): CommentPage itself declares no custom MarshalJSON, so
// encoding/json's reflection-based walk is what invokes each element's
// own Comment.MarshalJSON exactly once.
func embeddedCommentFields(t *testing.T, total int, comments []jira.Comment) jira.IssueFields {
	t.Helper()
	raw, err := json.Marshal(jira.CommentPage{Total: total, Comments: comments})
	if err != nil {
		t.Fatalf("marshal embedded comment page: %v", err)
	}
	return jira.IssueFields{Extra: map[string]json.RawMessage{"comment": raw}}
}

// TestRunViewFieldsCommentRendersAllEmbeddedCommentsUncapped pins case 1
// of the --fields/--comments precedence — and, now that --comments
// defaults to 1 rather than 0, the precedence trap the default change
// introduces: opts here carries n:1 (the flag's own non-explicit
// default), NOT the zero value, specifically to prove that default does
// NOT cap the uncapped read at 1 — a bare `issue view KEY --fields
// comment` must still render every embedded comment, not just the
// latest one. viewOpts{} (n:0, explicit:false) would not have
// exercised this at all, since 0 is capped-to-nothing regardless of
// uncapped's own logic; n:1 is the one value that could plausibly leak
// through as an accidental cap.
//
// This also proves the uncapped read is newest-first: the fixture's
// embedded page lists Ada before Grace (oldest first, matching a live
// tenant), so a naive "just use the page's own order" implementation
// would render them in that order too — this asserts Grace (the
// newest) comes first instead.
func TestRunViewFieldsCommentRendersAllEmbeddedCommentsUncapped(t *testing.T) {
	fields := embeddedCommentFields(t, 3, []jira.Comment{
		commentAt(t, "1", "Ada", "2024-01-01T00:00:00.000+0000"),
		commentAt(t, "2", "Grace", "2024-01-02T00:00:00.000+0000"),
		commentAt(t, "3", "Linus", "2024-01-03T00:00:00.000+0000"),
	})
	fake := &fakeIssueClient{read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}}}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, preview, err := runView(humanContext(t), deps, "ABC-1", []string{"comment"}, viewOpts{n: 1})
	if err != nil {
		t.Fatalf("runView: %v", err)
	}
	if !preview.uncapped {
		t.Errorf("preview = %+v, want uncapped true when --fields names comment", preview)
	}
	if len(preview.latest) != 3 {
		t.Fatalf("preview.latest = %+v, want all 3 embedded comments — the default --comments 1 must not cap the uncapped read", preview.latest)
	}
	if got := preview.latest[0].Author.DisplayName; got != "Linus" {
		t.Errorf("preview.latest[0].Author.DisplayName = %q, want %q (newest first)", got, "Linus")
	}
	if fake.commentsCalled {
		t.Error("GetComments was called — the uncapped path must source comments from the response already in hand")
	}
}

// TestRunViewFieldsCommentCaseAndWhitespaceInsensitive pins that the
// comment-field match is case-insensitive and tolerant of surrounding
// whitespace, alongside other named fields.
func TestRunViewFieldsCommentCaseAndWhitespaceInsensitive(t *testing.T) {
	fields := embeddedCommentFields(t, 1, []jira.Comment{
		{ID: jira.NullableOf("1"), Author: jira.NullableOf(jira.UserDetails{DisplayName: jira.NullableOf("Ada")}), Body: jira.NullableOf(commentBody(t, "first"))},
	})
	fake := &fakeIssueClient{read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}}}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, preview, err := runView(humanContext(t), deps, "ABC-1", []string{"summary", "  COMMENT  "}, viewOpts{})
	if err != nil {
		t.Fatalf("runView: %v", err)
	}
	if !preview.uncapped {
		t.Errorf("preview = %+v, want uncapped true for a mixed-case, whitespace-padded 'comment' field", preview)
	}
}

// TestRunViewFieldsSummaryAloneDoesNotTriggerUncapped is the negative
// counterpart: a --fields list that never names comment stays on the
// ordinary hint path.
func TestRunViewFieldsSummaryAloneDoesNotTriggerUncapped(t *testing.T) {
	fake := &fakeIssueClient{read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(jira.IssueFields{Summary: jira.NullableOf("no comment field here")})}}}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, preview, err := runView(t.Context(), deps, "ABC-1", []string{"summary"}, viewOpts{})
	if err != nil {
		t.Fatalf("runView: %v", err)
	}
	if preview.uncapped {
		t.Errorf("preview = %+v, want uncapped false when --fields never named comment", preview)
	}
}

// TestFieldsRequestComment is fieldsRequestComment's own table, covering
// every token shape item 2/3 defined rather than only the ones runView's
// own precedence tests happen to exercise: the plural alias replaces the
// prior design (a bare "comments" used to be rejected on purpose,
// because fields rode to Jira verbatim and a plural value fetched
// nothing — see requestFields, which is what makes accepting it honest
// now) — so this table is the ONE place that old "rejected" expectation
// would have lived, and it asserts the opposite on purpose.
func TestFieldsRequestComment(t *testing.T) {
	tests := []struct {
		name   string
		fields []string
		want   bool
	}{
		{name: "singular", fields: []string{"comment"}, want: true},
		{name: "plural alias", fields: []string{"comments"}, want: true},
		{name: "wildcard all", fields: []string{"*all"}, want: true},
		{name: "singular mixed case and whitespace", fields: []string{"  Comment  "}, want: true},
		{name: "plural mixed case and whitespace", fields: []string{"  COMMENTS  "}, want: true},
		{name: "alongside other fields", fields: []string{"summary", "comment"}, want: true},
		{name: "wildcard navigable does not count", fields: []string{"*navigable"}, want: false},
		{name: "unrelated field alone", fields: []string{"summary"}, want: false},
		{name: "no fields at all", fields: nil, want: false},
		{name: "substring is not a match", fields: []string{"commentary"}, want: false},
		{name: "singular exclusion suppresses", fields: []string{"comment", "-comment"}, want: false},
		{name: "plural exclusion suppresses", fields: []string{"comment", "-comments"}, want: false},
		{name: "wildcard plus exclusion suppresses", fields: []string{"*all", "-comment"}, want: false},
		{name: "exclusion before inclusion still suppresses", fields: []string{"-comment", "*all"}, want: false},
		{name: "exclusion alone with no inclusion", fields: []string{"-comment"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fieldsRequestComment(tt.fields); got != tt.want {
				t.Errorf("fieldsRequestComment(%q) = %v, want %v", tt.fields, got, tt.want)
			}
		})
	}
}

// TestRequestFieldsNormalizesPluralToSingular pins requestFields' own
// rewrite in isolation: the plural alias becomes Jira's real "comment"
// token, case-insensitively and whitespace-tolerantly like
// fieldsRequestComment's own match, and every other token — including an
// exclusion like "-comments", which only ever matters to
// fieldsRequestComment's local suppression, never to Jira — passes
// through byte-for-byte.
func TestRequestFieldsNormalizesPluralToSingular(t *testing.T) {
	got := requestFields([]string{"summary", "  Comments  ", "-comments", "status"})
	want := []string{"summary", "comment", "-comments", "status"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requestFields(...) = %q, want %q", got, want)
	}
}

// TestRequestFieldsLeavesNilAlone pins the empty-input edge: nil fields
// (no --fields given at all) must stay nil rather than becoming an
// allocated empty slice, matching GetIssue's own "narrow only when
// fields was actually given" contract.
func TestRequestFieldsLeavesNilAlone(t *testing.T) {
	if got := requestFields(nil); got != nil {
		t.Errorf("requestFields(nil) = %#v, want nil", got)
	}
}

// TestRunViewNormalizesPluralCommentsInWireRequest is item 3's
// end-to-end proof: --fields comments must reach GetIssue as "comment",
// not the literal plural Jira does not recognize — asserted against the
// fake's own recorded gotFields, not just the rendered preview, so a
// regression that broke the wire request but left the LOCAL uncapped
// read working (fieldsRequestComment alone would still return true)
// could not hide behind that coincidence.
func TestRunViewNormalizesPluralCommentsInWireRequest(t *testing.T) {
	fields := embeddedCommentFields(t, 1, []jira.Comment{
		{ID: jira.NullableOf("1"), Author: jira.NullableOf(jira.UserDetails{DisplayName: jira.NullableOf("Ada")}), Body: jira.NullableOf(commentBody(t, "first"))},
	})
	fake := &fakeIssueClient{read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}}}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, preview, err := runView(humanContext(t), deps, "ABC-1", []string{"summary", "comments"}, viewOpts{})
	if err != nil {
		t.Fatalf("runView: %v", err)
	}
	if want := []string{"summary", "comment"}; !reflect.DeepEqual(fake.gotFields, want) {
		t.Errorf("GetIssue called with fields %q, want %q — the plural must be rewritten before the request goes out", fake.gotFields, want)
	}
	if !preview.uncapped {
		t.Errorf("preview = %+v, want uncapped true for a plural 'comments' ask", preview)
	}
}

// TestRunViewExplicitCommentsWinsOverFieldsComment pins case 3 of the
// precedence rule: an explicit --comments N overrides --fields naming
// comment, falling through to the ordinary capped-fetch behavior.
func TestRunViewExplicitCommentsWinsOverFieldsComment(t *testing.T) {
	fields := embeddedCommentFields(t, 5, nil)
	fake := &fakeIssueClient{
		read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}},
		commentsRead: core.CommentRead{Page: &jira.CommentPage{
			Total:    5,
			Comments: []jira.Comment{{ID: jira.NullableOf("1"), Author: jira.NullableOf(jira.UserDetails{DisplayName: jira.NullableOf("Ada")})}},
		}},
	}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, preview, err := runView(humanContext(t), deps, "ABC-1", []string{"comment"}, viewOpts{n: 1, explicit: true})
	if err != nil {
		t.Fatalf("runView: %v", err)
	}
	if preview.uncapped {
		t.Errorf("preview = %+v, want uncapped false — explicit --comments must win over --fields comment", preview)
	}
	if !fake.commentsCalled || fake.gotCommentsStartAt != 0 || fake.gotCommentsMaxResults != 1 {
		t.Errorf("GetComments called=%v (startAt=%d, maxResults=%d), want called with (0, 1)", fake.commentsCalled, fake.gotCommentsStartAt, fake.gotCommentsMaxResults)
	}
	if preview.requested != 1 {
		t.Errorf("preview.requested = %d, want 1", preview.requested)
	}
}

// TestRunViewExplicitZeroCommentsWithFieldsCommentStaysHintOnly pins the
// subtlest edge of the precedence rule: --comments 0 typed EXPLICITLY
// alongside --fields comment still means hint-only, not uncapped —
// distinguishing this from "never asked" is exactly why explicit rides
// its own bit rather than being inferred from n > 0.
func TestRunViewExplicitZeroCommentsWithFieldsCommentStaysHintOnly(t *testing.T) {
	fields := embeddedCommentFields(t, 3, nil)
	fake := &fakeIssueClient{read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}}}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, preview, err := runView(t.Context(), deps, "ABC-1", []string{"comment"}, viewOpts{n: 0, explicit: true})
	if err != nil {
		t.Fatalf("runView: %v", err)
	}
	if preview.uncapped {
		t.Errorf("preview = %+v, want uncapped false — an explicit --comments 0 must still win over --fields comment", preview)
	}
	if fake.commentsCalled {
		t.Error("GetComments was called — --comments 0 must stay on the cheap embedded-total read")
	}
	if !preview.totalKnown || preview.total != 3 {
		t.Errorf("preview = %+v, want total 3, known, read from the embedded field", preview)
	}
}

// TestRenderCommentsPreviewUncappedShowsBannerWhenPageFellShortOfTotal
// pins item 5's guard: the uncapped path renders every comment its
// OWN embedded page carried, but still shows the same "Showing N of
// TOTAL" banner as the capped path when that page's own total says
// there was more than it carried — Jira's embedded page can itself be
// capped for a large thread, and rendering a subset while implying it
// is everything would be exactly the silent-swallow bug this feature
// exists to kill. The banner names the paginated verb rather than
// triggering a second fetch to "complete" the set.
func TestRenderCommentsPreviewUncappedShowsBannerWhenPageFellShortOfTotal(t *testing.T) {
	preview := commentsPreview{
		uncapped:   true,
		total:      5,
		totalKnown: true,
		latest: []envelope.CommentView{
			{Author: envelope.AuthorRef{DisplayName: "Ada"}, Created: "2024-01-01T00:00:00Z", Body: commentBody(t, "first")},
			{Author: envelope.AuthorRef{DisplayName: "Grace"}, Created: "2024-01-02T00:00:00Z", Body: commentBody(t, "second")},
		},
	}

	got := renderPreview(t, "ABC-1", preview, clog.ColorNever)

	if !strings.Contains(got, "Ada") || !strings.Contains(got, "Grace") {
		t.Errorf("output = %q, want both comments' authors", got)
	}
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Errorf("output = %q, want both comments' bodies", got)
	}
	if !strings.Contains(got, "Showing 2 of 5") {
		t.Errorf("output = %q, want the Showing 2 of 5 banner — the embedded page fell short of the issue's real total", got)
	}
	if !strings.Contains(got, "jira issue comment list ABC-1") {
		t.Errorf("output = %q, want the banner to name the full-thread verb", got)
	}
}

// TestRenderCommentsPreviewUncappedOmitsBannerWhenComplete is the
// negative counterpart: when the embedded page's own total says it
// carried everything, no banner claims there is more to see.
func TestRenderCommentsPreviewUncappedOmitsBannerWhenComplete(t *testing.T) {
	preview := commentsPreview{
		uncapped:   true,
		total:      2,
		totalKnown: true,
		latest: []envelope.CommentView{
			{Author: envelope.AuthorRef{DisplayName: "Ada"}, Body: commentBody(t, "first")},
			{Author: envelope.AuthorRef{DisplayName: "Grace"}, Body: commentBody(t, "second")},
		},
	}

	got := renderPreview(t, "ABC-1", preview, clog.ColorNever)

	if strings.Contains(got, "Showing") {
		t.Errorf("output = %q, want no Showing banner when the embedded page already covers the total", got)
	}
}

// TestRenderCommentsPreviewUncappedNoCommentsPrintsExplicit mirrors
// TestRenderCommentsPreviewKnownZeroTotalAnswersAnExplicitAsk for the
// uncapped shape: zero comments still gets one plain line, not an empty
// section.
func TestRenderCommentsPreviewUncappedNoCommentsPrintsExplicit(t *testing.T) {
	got := renderPreview(t, "ABC-1", commentsPreview{uncapped: true}, clog.ColorNever)
	if !strings.Contains(got, "No comments.") {
		t.Errorf("output = %q, want the explicit no-comments line", got)
	}
}

// TestRenderCommentsPreviewUncappedSanitizesHostileAuthorAndBody is the
// uncapped path's own H4 proof, mirroring
// TestRenderCommentsPreviewSanitizesHostileAuthorAndBody.
func TestRenderCommentsPreviewUncappedSanitizesHostileAuthorAndBody(t *testing.T) {
	hostileAuthor := "Eve\x1b[2K\rSUDO PASSWORD:\x07"
	preview := commentsPreview{
		uncapped: true,
		latest: []envelope.CommentView{
			{
				Author:  envelope.AuthorRef{DisplayName: hostileAuthor},
				Created: "2024-01-01T00:00:00Z",
				Body:    commentBody(t, "hostile\x1b[2K\rSUDO PASSWORD:\x07"),
			},
		},
	}

	got := renderPreview(t, "ABC-1", preview, clog.ColorNever)

	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("output = %q, an escape sequence reached the terminal", got)
	}
	if strings.ContainsRune(got, '\r') || strings.ContainsRune(got, '\x07') {
		t.Errorf("output = %q, a control byte reached the terminal", got)
	}
}

// TestRenderCommentsPreviewSanitizesHostileAuthorAndBodyWithColorEnabled
// is item 8's H4-holds-on-the-colored-path proof for the inline
// preview, mirroring comment_test.go's own
// TestRenderCommentListSanitizesHostileAuthorAndBodyWithColorEnabled:
// every existing sanitization test for this preview forces
// clog.ColorNever, which cannot tell "sanitized" apart from "styling
// was simply off the whole time". renderCommentsPreview builds its
// header with v.Line, not v.Section, so it never hits the divider-title
// nesting bug item 1 fixes — but it still owes the same H4 guarantee
// with color on: the hostile payload's own control/escape bytes must
// never reach the terminal, even though legitimate styling escape bytes
// are now expected.
func TestRenderCommentsPreviewSanitizesHostileAuthorAndBodyWithColorEnabled(t *testing.T) {
	hostileAuthor := "Eve\x1b[2K\rSUDO PASSWORD:\x07"
	preview := commentsPreview{
		requested: 1,
		latest: []envelope.CommentView{
			{
				Author:  envelope.AuthorRef{DisplayName: hostileAuthor},
				Created: "2024-01-01T00:00:00Z",
				Body:    commentBody(t, "hostile\x1b[2K\rSUDO PASSWORD:\x07"),
			},
		},
	}

	got := renderPreview(t, "ABC-1", preview, clog.ColorAlways)

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

// TestRunViewNegativeCommentsIsRejectedNotPanicked is the regression
// guard for the bug this brief exists to fix: --comments=-1 used to
// reach `sorted[:opts.n]` in the local (no-second-fetch) path unchecked
// and panic with a raw Go stack trace — reproduced live against a real
// tenant. Run against that SAME local path (humanContext, an embedded
// page that demonstrably carries the whole thread) so this pins the
// exact shape that panicked, not runView in the abstract. Rejecting
// with a classified *cli.InputError — flag_value_invalid, naming the
// flag and the offending value — is what "fail loudly" looks like for a
// value that is a user mistake, not one to silently clamp.
func TestRunViewNegativeCommentsIsRejectedNotPanicked(t *testing.T) {
	fields := embeddedCommentFields(t, 2, []jira.Comment{
		commentAt(t, "1", "Ada", "2024-01-01T00:00:00.000+0000"),
		commentAt(t, "2", "Grace", "2024-01-02T00:00:00.000+0000"),
	})
	fake := &fakeIssueClient{read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}}}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, _, err := runView(humanContext(t), deps, "ABC-1", nil, viewOpts{n: -1, explicit: true})
	if err == nil {
		t.Fatal("runView(--comments=-1) returned nil error, want a classified rejection")
	}
	var input *cli.InputError
	if !errors.As(err, &input) {
		t.Fatalf("err = %v (%T), want a *cli.InputError", err, err)
	}
	if input.Code != cli.CodeFlagValueInvalid {
		t.Errorf("code = %q, want %q", input.Code, cli.CodeFlagValueInvalid)
	}
	if input.Field != "comments" {
		t.Errorf("field = %q, want %q", input.Field, "comments")
	}
	if !strings.Contains(err.Error(), "comments") || !strings.Contains(err.Error(), "-1") {
		t.Errorf("error = %q, want it to name the flag and the offending value", err.Error())
	}
}

// TestRunViewNegativeCommentsNeverReachesGetComments is the second half
// of item 1's guard: a negative --comments must be rejected before it
// can reach GetComments as maxResults at all — not just before the
// local slice truncation, since --comments N's fallback fetch path
// takes the exact same opts.n as its maxResults argument.
func TestRunViewNegativeCommentsNeverReachesGetComments(t *testing.T) {
	fake := &fakeIssueClient{read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1")}}}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	if _, _, err := runView(humanContext(t), deps, "ABC-1", nil, viewOpts{n: -1, explicit: true}); err == nil {
		t.Fatal("runView(--comments=-1) returned nil error")
	}
	if fake.commentsCalled {
		t.Error("GetComments was called — a negative --comments must be rejected before it can reach maxResults")
	}
}

// TestRunViewCommentFetchFailureDefaultDegradesInsteadOfFailing pins
// item 2: a GetComments failure reached only because nobody explicitly
// asked for comments (the bare `issue view` default, whose embedded
// page fell short of the issue's own total) must not fail the whole
// command — the issue itself already fetched successfully, and a
// hiccup in a display aid must not destroy it. The command succeeds
// with the real issue and a preview that degrades to the count-hint
// shape (a known total, since the embedded page's own total decoded
// even though its comment array itself was truncated) rather than an
// error.
func TestRunViewCommentFetchFailureDefaultDegradesInsteadOfFailing(t *testing.T) {
	fields := embeddedCommentFields(t, 10, []jira.Comment{
		commentAt(t, "1", "Ada", "2024-01-01T00:00:00.000+0000"),
	}) // total 10, only 1 embedded — truncated, forces the fallback fetch
	fake := &fakeIssueClient{
		read:        core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}},
		commentsErr: errors.New("jira: comment endpoint unavailable"),
	}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	out, preview, err := runView(humanContext(t), deps, "ABC-1", nil, viewOpts{n: 1})
	if err != nil {
		t.Fatalf("runView: %v, want the issue returned successfully despite the comment-fetch failure", err)
	}
	if out.Issue.Key != "ABC-1" {
		t.Errorf("out.Issue.Key = %q, want the real issue, not the zero value", out.Issue.Key)
	}
	if len(preview.latest) != 0 {
		t.Errorf("preview.latest = %+v, want no comments rendered from a failed fetch", preview.latest)
	}
	if !preview.totalKnown || preview.total != 10 {
		t.Errorf("preview = %+v, want total 10 known — read from the embedded field before the failed fetch", preview)
	}
}

// TestRunViewCommentFetchFailureExplicitStillFails is the other half of
// item 2's rule: the SAME failure, reached because the caller
// EXPLICITLY typed --comments N, still fails the whole command — they
// asked, so they get the truth, exactly as before this degrade existed.
func TestRunViewCommentFetchFailureExplicitStillFails(t *testing.T) {
	fields := embeddedCommentFields(t, 10, []jira.Comment{
		commentAt(t, "1", "Ada", "2024-01-01T00:00:00.000+0000"),
	})
	fake := &fakeIssueClient{
		read:        core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}},
		commentsErr: errors.New("jira: comment endpoint unavailable"),
	}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, _, err := runView(humanContext(t), deps, "ABC-1", nil, viewOpts{n: 1, explicit: true})
	if err == nil {
		t.Fatal("runView returned nil error for an explicit --comments whose fetch failed — an explicit ask must still fail loudly")
	}
}

// TestRunViewCommentFetchNilPageDefaultDegradesInsteadOfFailing mirrors
// the pair above for the nil-Page guard (mirrors runCommentList's own):
// a nil Page with nobody asking degrades the same way a transport error
// does.
func TestRunViewCommentFetchNilPageDefaultDegradesInsteadOfFailing(t *testing.T) {
	fields := embeddedCommentFields(t, 10, []jira.Comment{
		commentAt(t, "1", "Ada", "2024-01-01T00:00:00.000+0000"),
	})
	fake := &fakeIssueClient{
		read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}},
		// commentsRead left at its zero value: Page is nil.
	}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	out, preview, err := runView(humanContext(t), deps, "ABC-1", nil, viewOpts{n: 1})
	if err != nil {
		t.Fatalf("runView: %v, want the issue returned successfully despite the nil comment page", err)
	}
	if out.Issue.Key != "ABC-1" {
		t.Errorf("out.Issue.Key = %q, want the real issue", out.Issue.Key)
	}
	if len(preview.latest) != 0 {
		t.Errorf("preview.latest = %+v, want no comments rendered", preview.latest)
	}
}

// TestRunViewCommentFetchNilPageExplicitStillFails is the nil-Page
// sibling of TestRunViewCommentFetchFailureExplicitStillFails.
func TestRunViewCommentFetchNilPageExplicitStillFails(t *testing.T) {
	fields := embeddedCommentFields(t, 10, []jira.Comment{
		commentAt(t, "1", "Ada", "2024-01-01T00:00:00.000+0000"),
	})
	fake := &fakeIssueClient{
		read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}},
	}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	_, _, err := runView(humanContext(t), deps, "ABC-1", nil, viewOpts{n: 1, explicit: true})
	if err == nil {
		t.Fatal("runView returned nil error for an explicit --comments against a nil comment page")
	}
}

// TestRunViewFieldsCommentSkipsDecodeWhenOutputIsNotHuman pins item 4:
// --fields comment's uncapped read must not decode or sort the embedded
// comment page for a resolved non-human output mode — the doc comment
// above runView claims this is skipped entirely, and before this fix it
// was not: --output=json --fields comment silently did the work anyway
// and threw it away. Observed the same way
// TestRunViewSkipsCommentsFetchWhenOutputIsNotHuman observes its own
// gate: the returned preview itself, since nothing else about this path
// is exposed to a test — an uncapped preview with comments in it is
// exactly what a caller would get if the decode ran; the zero value is
// exactly what "skipped" looks like.
func TestRunViewFieldsCommentSkipsDecodeWhenOutputIsNotHuman(t *testing.T) {
	fields := embeddedCommentFields(t, 3, []jira.Comment{
		commentAt(t, "1", "Ada", "2024-01-01T00:00:00.000+0000"),
		commentAt(t, "2", "Grace", "2024-01-02T00:00:00.000+0000"),
		commentAt(t, "3", "Linus", "2024-01-03T00:00:00.000+0000"),
	})
	fake := &fakeIssueClient{read: core.IssueRead{Issue: &jira.IssueBean{Key: jira.NullableOf("ABC-1"), Fields: jira.NullableOf(fields)}}}
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}

	ctx := cli.WithFormat(t.Context(), output.FormatJSON)
	_, preview, err := runView(ctx, deps, "ABC-1", []string{"comment"}, viewOpts{n: 1})
	if err != nil {
		t.Fatalf("runView: %v", err)
	}
	if preview.uncapped {
		t.Error("preview.uncapped = true for a non-human resolved format — the uncapped read must not run at all")
	}
	if len(preview.latest) != 0 {
		t.Errorf("preview.latest = %+v, want nothing decoded for a non-human resolved format", preview.latest)
	}
	if fake.commentsCalled {
		t.Error("GetComments was called for a non-human output mode")
	}
}
