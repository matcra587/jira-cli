package issue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gechr/clog"
	"github.com/gechr/x/ansi"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// listCall pins the arguments one SearchIssues call was made with — the
// per-call record fakeListClient keeps so a multi-page test can assert
// not just the final result but the SHAPE of the walk that produced it
// (how many calls, what page size and cursor each one carried).
type listCall struct {
	jql           string
	fields        []string
	nextPageToken string
	maxResults    int
}

// fakeListClient is a minimal core.JiraClient double: SearchIssues
// serves pages one at a time from a scripted sequence and records every
// call it was made with, so a test can drive and assert a genuine
// multi-page auto-pagination walk rather than a single canned response.
// baseURL feeds BaseURL() — the site newList's Run closure captures for
// the KEY column's hyperlink (list.go) — so a test can prove that
// wiring end to end; it defaults to "" for every test that does not
// care, which is the same "no site" degrade cli.IssueKey already covers
// on its own.
type fakeListClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	baseURL string

	// pages is the scripted response sequence: call N (0-indexed) gets
	// pages[N]. A call past the end of pages is a test bug (the walk
	// asked for more pages than the test scripted) and returns pagesErr,
	// or a loud default error when pagesErr is nil.
	pages    []core.IssueSearchRead
	pagesErr error

	calls []listCall

	// countRead/countErr script CountIssues' own response; countCalls
	// records every jql it was called with (in order), so a test can
	// assert not just what fetchCountEstimate resolved to but WHETHER —
	// and how many times — the gate actually fired at all. Both default
	// to their zero value for every test that never expects a call.
	countRead  core.IssueCountRead
	countErr   error
	countCalls []string
}

func (f *fakeListClient) BaseURL() string { return f.baseURL }

func (f *fakeListClient) SearchIssues(_ context.Context, jql string, fields []string, nextPageToken string, maxResults int, _ ...string) (core.IssueSearchRead, error) {
	f.calls = append(f.calls, listCall{jql: jql, fields: fields, nextPageToken: nextPageToken, maxResults: maxResults})
	i := len(f.calls) - 1
	if i >= len(f.pages) {
		if f.pagesErr != nil {
			return core.IssueSearchRead{}, f.pagesErr
		}
		return core.IssueSearchRead{}, errors.New("fakeListClient: SearchIssues called more times than the test scripted a page for")
	}
	return f.pages[i], nil
}

func (f *fakeListClient) CountIssues(_ context.Context, jql string) (core.IssueCountRead, error) {
	f.countCalls = append(f.countCalls, jql)
	return f.countRead, f.countErr
}

// onePage is the shorthand every single-page test below reaches for: a
// scripted response sequence of exactly one page — the test itself sets
// whatever IsLast/NextPageToken/Issues shape it means to pin.
func onePage(results *jira.SearchResults) []core.IssueSearchRead {
	return []core.IssueSearchRead{{Results: results}}
}

// TestRunListWebBuildsURLWithoutSearching pins --web's own contract for
// `issue list`: the search's own issue-navigator URL is built from jql
// and the client's resolved site ALONE — SearchIssues (f.calls) and
// CountIssues (f.countCalls) are both never called.
func TestRunListWebBuildsURLWithoutSearching(t *testing.T) {
	fake := &fakeListClient{baseURL: "https://example.atlassian.net"}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}

	var calls []recordedOpen
	out, err := runListWeb(t.Context(), deps, `project = ABC`, fakeOpener(&calls, nil))
	if err != nil {
		t.Fatalf("runListWeb: %v", err)
	}

	if len(fake.calls) != 0 {
		t.Errorf("SearchIssues called %d times, want 0 — --web must not run the search: %+v", len(fake.calls), fake.calls)
	}
	if len(fake.countCalls) != 0 {
		t.Errorf("CountIssues called %d times, want 0", len(fake.countCalls))
	}
	const want = "https://example.atlassian.net/issues/?jql=project+%3D+ABC"
	if out.WebURL != want {
		t.Errorf("WebURL = %q, want %q", out.WebURL, want)
	}
	if len(out.Issues) != 0 {
		t.Errorf("Issues = %v, want empty — --web never searched", out.Issues)
	}
}

// TestRunListWebRejectsUnparsableSite pins the same fail-loud posture
// runViewWeb's own malformed-key test does, for the other half of
// cli.SearchBrowseURL's ok gate: a site that will not parse as an absolute
// scheme+host URL.
func TestRunListWebRejectsUnparsableSite(t *testing.T) {
	fake := &fakeListClient{baseURL: "not-a-url"}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}

	var calls []recordedOpen
	_, err := runListWeb(t.Context(), deps, "project = ABC", fakeOpener(&calls, nil))
	if err == nil {
		t.Fatal("runListWeb(unparsable site) returned nil error, want a classified rejection")
	}
	if got := cli.MapError(err).Code; got != cli.CodeConfigInvalid {
		t.Errorf("classified code = %q, want %q", got, cli.CodeConfigInvalid)
	}
}

// TestRunListWebOpensOnlyForHumanOutput mirrors
// TestRunViewWebOpensOnlyForHumanOutput (view_test.go): a non-human
// resolved format still gets the URL back, but never calls the opener.
func TestRunListWebOpensOnlyForHumanOutput(t *testing.T) {
	fake := &fakeListClient{baseURL: "https://example.atlassian.net"}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}

	var calls []recordedOpen
	out, err := runListWeb(cli.WithFormat(t.Context(), output.FormatCompact), deps, "project = ABC", fakeOpener(&calls, nil))
	if err != nil {
		t.Fatalf("runListWeb: %v", err)
	}
	if out.WebURL == "" {
		t.Error("WebURL is empty for compact output — the URL must always ride the envelope")
	}
	if len(calls) != 0 {
		t.Errorf("opener called %d times for compact output, want 0", len(calls))
	}
}

func TestRunListPassesComposedJQLThrough(t *testing.T) {
	fake := &fakeListClient{pages: onePage(&jira.SearchResults{IsLast: jira.NullableOf(true), Issues: jira.NullableOf([]jira.IssueBean{})})}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}

	if _, _, err := runList(t.Context(), deps, `project in ("ABC")`, nil, "", listWalk{limit: 25}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].jql != `project in ("ABC")` {
		t.Errorf("calls = %+v, want one call with jql %q", fake.calls, `project in ("ABC")`)
	}
}

// TestRunListMapsLimitAndNextPageToken pins the flag-to-request mapping
// for the FIRST request a walk makes: --limit bounds the first page's
// own maxResults (never more than what is asked for), and
// --next-page-token rides straight through as the starting cursor.
func TestRunListMapsLimitAndNextPageToken(t *testing.T) {
	fake := &fakeListClient{pages: onePage(&jira.SearchResults{IsLast: jira.NullableOf(true), Issues: jira.NullableOf([]jira.IssueBean{})})}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}

	if _, _, err := runList(t.Context(), deps, "", nil, "cursor-abc", listWalk{limit: 10}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("calls = %+v, want exactly one", fake.calls)
	}
	if fake.calls[0].maxResults != 10 {
		t.Errorf("maxResults = %d, want 10", fake.calls[0].maxResults)
	}
	if fake.calls[0].nextPageToken != "cursor-abc" {
		t.Errorf("nextPageToken = %q, want %q", fake.calls[0].nextPageToken, "cursor-abc")
	}
}

// TestRunListUsesDefaultFieldsWhenNoneGiven proves the table-shaped
// default fires when --fields was never passed — the search endpoint's
// own default is "id" only, which would render an empty table.
func TestRunListUsesDefaultFieldsWhenNoneGiven(t *testing.T) {
	fake := &fakeListClient{pages: onePage(&jira.SearchResults{IsLast: jira.NullableOf(true), Issues: jira.NullableOf([]jira.IssueBean{})})}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}

	if _, _, err := runList(t.Context(), deps, "", nil, "", listWalk{limit: 50}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(fake.calls) == 0 || len(fake.calls[0].fields) == 0 {
		t.Fatal("fields = nil, want the table's own default field set")
	}
	for _, want := range []string{"summary", "status"} {
		found := false
		for _, got := range fake.calls[0].fields {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("fields = %v, want it to contain %q", fake.calls[0].fields, want)
		}
	}
}

// TestRunListRespectsExplicitFields proves an explicit --fields
// overrides the table-shaped default rather than being merged with it.
func TestRunListRespectsExplicitFields(t *testing.T) {
	fake := &fakeListClient{pages: onePage(&jira.SearchResults{IsLast: jira.NullableOf(true), Issues: jira.NullableOf([]jira.IssueBean{})})}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}

	if _, _, err := runList(t.Context(), deps, "", []string{"priority"}, "", listWalk{limit: 50}); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if got := fake.calls[0].fields; len(got) != 1 || got[0] != "priority" {
		t.Errorf("fields = %v, want exactly [priority]", got)
	}
}

func TestRunListRejectsNegativeLimit(t *testing.T) {
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		t.Fatal("JiraClient constructed for a request that should have been rejected before any I/O")
		return nil, nil
	}}

	_, _, err := runList(t.Context(), deps, "", nil, "", listWalk{limit: -1})
	if err == nil {
		t.Fatal("runList returned nil error for a negative limit")
	}
}

// TestRunListFailsLoudlyOnNilResults mirrors TestRunViewFailsLoudlyOnNilIssue
// and TestRunCommentListFailsLoudlyOnNilPage: SearchIssues's own contract
// is a non-nil Results on every nil-error return, so runList must refuse
// a violation of it rather than silently produce an empty-but-successful
// page.
func TestRunListFailsLoudlyOnNilResults(t *testing.T) {
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		return &fakeListClient{pages: []core.IssueSearchRead{{Results: nil}}}, nil
	}}

	_, _, err := runList(t.Context(), deps, "", nil, "", listWalk{limit: 50})
	if err == nil {
		t.Fatal("runList returned nil error for a nil Results — an empty page would have been reported as success")
	}
}

// TestRunCountReturnsEstimateWithoutFetchingIssues is --count's own core
// proof: a single CountIssues call, carrying the composed jql, and NO
// SearchIssues call at all — --count's whole point is answering "how
// many" without paying for "which ones".
func TestRunCountReturnsEstimateWithoutFetchingIssues(t *testing.T) {
	fake := &fakeListClient{countRead: core.IssueCountRead{Count: 4321}}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}

	out, err := runCount(t.Context(), deps, `project in ("ABC")`)
	if err != nil {
		t.Fatalf("runCount: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("SearchIssues calls = %d, want 0 — --count must never fetch issues", len(fake.calls))
	}
	if len(fake.countCalls) != 1 || fake.countCalls[0] != `project in ("ABC")` {
		t.Errorf("countCalls = %v, want exactly one call carrying the composed jql", fake.countCalls)
	}
	if out.Count == nil {
		t.Fatal("Count = nil, want a resolved estimate")
	}
	if out.Count.Value != 4321 || !out.Count.Approximate {
		t.Errorf("Count = %+v, want {Value: 4321, Approximate: true}", out.Count)
	}
	if len(out.Issues) != 0 {
		t.Errorf("Issues = %+v, want empty — --count never fetches issues", out.Issues)
	}
}

// TestRunCountPropagatesClientError proves a CountIssues failure fails
// runCount loudly rather than papering over it with a zero estimate —
// unlike fetchCountEstimate's own display-aid degrade, --count IS the
// whole command's answer, so its own failure has nothing else to fall
// back to.
func TestRunCountPropagatesClientError(t *testing.T) {
	fake := &fakeListClient{countErr: errors.New("boom")}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}

	_, err := runCount(t.Context(), deps, "project = ABC")
	if err == nil {
		t.Fatal("runCount returned nil error when CountIssues failed")
	}
}

// morePage builds a one-issue, not-yet-last SearchIssues response — the
// shared shape every fetchCountEstimate gating test below scripts, so
// each one only has to say what differs (the context's resolved format,
// or the fake's own countRead/countErr).
func morePage() []core.IssueSearchRead {
	return onePage(&jira.SearchResults{
		IsLast:        jira.NullableOf(false),
		NextPageToken: jira.NullableOf("cursor-2"),
		Issues:        jira.NullableOf([]jira.IssueBean{issueBean("ABC-1")}),
	})
}

// TestRunListSkipsCountCallWhenIsLast pins fetchCountEstimate's first
// gate: a complete page already knows its own exact count
// (out.Pagination.Returned) without asking anything else, so the
// separate estimate endpoint must never be called for it — even under a
// human render, where every other gate would otherwise allow it.
func TestRunListSkipsCountCallWhenIsLast(t *testing.T) {
	fake := &fakeListClient{pages: onePage(&jira.SearchResults{
		IsLast: jira.NullableOf(true),
		Issues: jira.NullableOf([]jira.IssueBean{issueBean("ABC-1")}),
	})}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}

	_, estimate, err := runList(humanContext(t), deps, "", nil, "", listWalk{limit: 50})
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(fake.countCalls) != 0 {
		t.Errorf("countCalls = %v, want none — the page is already complete", fake.countCalls)
	}
	if estimate.known {
		t.Errorf("estimate = %+v, want unknown when the count endpoint was never asked", estimate)
	}
}

// TestRunListFetchesCountWhenMoreExistsAndHuman pins the gate's positive
// case: more results plus a resolved human format fires exactly one
// count call, carrying the SAME jql the search itself used, and resolves
// the estimate runList hands back.
func TestRunListFetchesCountWhenMoreExistsAndHuman(t *testing.T) {
	fake := &fakeListClient{pages: morePage(), countRead: core.IssueCountRead{Count: 1234}}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}

	_, estimate, err := runList(humanContext(t), deps, `project in ("ABC")`, nil, "", listWalk{limit: 1})
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(fake.countCalls) != 1 || fake.countCalls[0] != `project in ("ABC")` {
		t.Errorf("countCalls = %v, want exactly one call carrying the search's own jql", fake.countCalls)
	}
	if !estimate.known || estimate.value != 1234 {
		t.Errorf("estimate = %+v, want {value: 1234, known: true}", estimate)
	}
}

// TestRunListSkipsCountCallForMachineOutput pins the machine-output
// gate: --output=json/compact already carries is_last and
// next_page_token in the envelope, so the estimate round trip must
// never fire regardless of how many pages remain — asserted directly on
// the fake, the only way to prove a call did NOT happen.
func TestRunListSkipsCountCallForMachineOutput(t *testing.T) {
	fake := &fakeListClient{pages: morePage()}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}

	ctx := cli.WithFormat(t.Context(), output.FormatJSON)
	_, estimate, err := runList(ctx, deps, "", nil, "", listWalk{limit: 1})
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(fake.countCalls) != 0 {
		t.Errorf("countCalls = %v, want none for machine output", fake.countCalls)
	}
	if estimate.known {
		t.Errorf("estimate = %+v, want unknown for machine output", estimate)
	}
}

// TestRunListDegradesWhenCountCallFails is the failure-must-never-
// escalate proof: a count-call failure must not take down the search
// that already succeeded — the issues collected still come back with a
// nil error, and the estimate simply stays unknown so the human render
// falls back to the count-free wording (renderList's own concern, pinned
// separately by the render-level tests above).
func TestRunListDegradesWhenCountCallFails(t *testing.T) {
	fake := &fakeListClient{pages: morePage(), countErr: errors.New("boom")}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}

	out, estimate, err := runList(humanContext(t), deps, "", nil, "", listWalk{limit: 1})
	if err != nil {
		t.Fatalf("runList: %v, want the search to succeed despite the count call failing", err)
	}
	if len(out.Issues) != 1 {
		t.Errorf("issues = %+v, want the search's own result to survive a count-call failure", out.Issues)
	}
	if estimate.known {
		t.Errorf("estimate = %+v, want unknown after a failed count call", estimate)
	}
}

// TestValidateAllLimitRefusesExplicitLimit is the --all/--limit
// conflict proof: an EXPLICIT --limit alongside --all is a classified
// error, not a silently resolved precedence rule.
func TestValidateAllLimitRefusesExplicitLimit(t *testing.T) {
	err := validateAllLimit(true, true)
	if err == nil {
		t.Fatal("validateAllLimit(all=true, limitChanged=true) returned nil, want a classified error")
	}
}

// TestValidateAllLimitAllowsUntouchedDefault proves --all alone, with
// --limit sitting at its own untouched default, is NOT a conflict — the
// caller never actually asked for a competing bounded count.
func TestValidateAllLimitAllowsUntouchedDefault(t *testing.T) {
	if err := validateAllLimit(true, false); err != nil {
		t.Errorf("validateAllLimit(all=true, limitChanged=false) = %v, want nil", err)
	}
}

// TestValidateAllLimitAllowsExplicitLimitWithoutAll proves --limit on
// its own, without --all, is untouched by this rule.
func TestValidateAllLimitAllowsExplicitLimitWithoutAll(t *testing.T) {
	if err := validateAllLimit(false, true); err != nil {
		t.Errorf("validateAllLimit(all=false, limitChanged=true) = %v, want nil", err)
	}
}

// TestValidateCountFlagsRefusesExplicitLimit proves --count alongside an
// EXPLICIT --limit is a classified error naming --limit — --count fetches
// nothing, so a page size has nothing to act on.
func TestValidateCountFlagsRefusesExplicitLimit(t *testing.T) {
	err := validateCountFlags(true, true, false)
	if err == nil {
		t.Fatal("validateCountFlags(count=true, limitChanged=true, all=false) returned nil, want a classified error")
	}
	var inputErr *cli.InputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("error = %v (%T), want *cli.InputError", err, err)
	}
	if inputErr.Code != cli.CodeFlagNotApplicable {
		t.Errorf("code = %q, want %q", inputErr.Code, cli.CodeFlagNotApplicable)
	}
	if inputErr.Field != "limit" {
		t.Errorf("field = %q, want %q", inputErr.Field, "limit")
	}
}

// TestValidateCountFlagsRefusesAll proves --count alongside --all is
// refused the same way, naming --all.
func TestValidateCountFlagsRefusesAll(t *testing.T) {
	err := validateCountFlags(true, false, true)
	if err == nil {
		t.Fatal("validateCountFlags(count=true, limitChanged=false, all=true) returned nil, want a classified error")
	}
	var inputErr *cli.InputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("error = %v (%T), want *cli.InputError", err, err)
	}
	if inputErr.Field != "all" {
		t.Errorf("field = %q, want %q", inputErr.Field, "all")
	}
}

// TestValidateCountFlagsAllowsUntouchedLimitDefault proves --count alone,
// with --limit sitting at its own untouched default, is NOT a conflict —
// the same "Changed, not the value" posture validateAllLimit applies.
func TestValidateCountFlagsAllowsUntouchedLimitDefault(t *testing.T) {
	if err := validateCountFlags(true, false, false); err != nil {
		t.Errorf("validateCountFlags(count=true, limitChanged=false, all=false) = %v, want nil", err)
	}
}

// TestValidateCountFlagsIgnoredWhenCountFalse proves this rule only ever
// fires for --count itself — --limit/--all together (validateAllLimit's
// own concern) are untouched by it.
func TestValidateCountFlagsIgnoredWhenCountFalse(t *testing.T) {
	if err := validateCountFlags(false, true, true); err != nil {
		t.Errorf("validateCountFlags(count=false, ...) = %v, want nil — this rule is --count's own", err)
	}
}

func TestBuildListOutputCarriesPagination(t *testing.T) {
	results := &jira.SearchResults{
		IsLast:        jira.NullableOf(false),
		NextPageToken: jira.NullableOf("abc123"),
		Issues: jira.NullableOf([]jira.IssueBean{
			{
				ID:   jira.NullableOf("10001"),
				Key:  jira.NullableOf("ABC-1"),
				Self: jira.NullableOf("https://example.atlassian.net/rest/api/3/issue/10001"),
				Fields: jira.NullableOf(jira.IssueFields{
					Summary: jira.NullableOf("Fix the thing"),
				}),
			},
		}),
	}

	got := buildListOutput(core.IssueSearchRead{Results: results})

	if got.Pagination != (envelope.CursorPagination{NextPageToken: "abc123", IsLast: false, Returned: 1}) {
		t.Errorf("pagination = %+v, want next_page_token abc123, is_last false, returned 1", got.Pagination)
	}
	if len(got.Issues) != 1 {
		t.Fatalf("issues = %+v, want one entry", got.Issues)
	}
	issue := got.Issues[0]
	if issue.Key != "ABC-1" || issue.ID != "10001" {
		t.Errorf("issue = %+v, want key ABC-1 id 10001", issue)
	}
	if issue.Fields.Summary.Value != "Fix the thing" {
		t.Errorf("summary = %q, want %q", issue.Fields.Summary.Value, "Fix the thing")
	}
}

// TestBuildListOutputIsLastTrueOnFinalPage proves is_last carries
// through unmodified — the authoritative end-of-results signal this
// endpoint sends in place of a total.
func TestBuildListOutputIsLastTrueOnFinalPage(t *testing.T) {
	results := &jira.SearchResults{IsLast: jira.NullableOf(true)}
	got := buildListOutput(core.IssueSearchRead{Results: results})
	if !got.Pagination.IsLast {
		t.Errorf("is_last = false, want true")
	}
	if got.Pagination.NextPageToken != "" {
		t.Errorf("next_page_token = %q, want empty on the last page", got.Pagination.NextPageToken)
	}
}

// issueBean is a small builder for a scripted page's one issue, used
// throughout the auto-pagination tests below where the exact field
// content does not matter — only that a page carries N issues.
func issueBean(key string) jira.IssueBean {
	return jira.IssueBean{
		ID:  jira.NullableOf(key),
		Key: jira.NullableOf(key),
		Fields: jira.NullableOf(jira.IssueFields{
			Summary: jira.NullableOf("summary for " + key),
		}),
	}
}

// TestAutoPaginateCollectsExactlyLimitAcrossPages is the auto-pagination
// core proof: --limit spanning several pages collects exactly N issues
// and stops — asserting both the number of seam calls and the page size
// each one requested.
func TestAutoPaginateCollectsExactlyLimitAcrossPages(t *testing.T) {
	fake := &fakeListClient{pages: []core.IssueSearchRead{
		{Results: &jira.SearchResults{
			IsLast:        jira.NullableOf(false),
			NextPageToken: jira.NullableOf("page-2"),
			Issues:        jira.NullableOf([]jira.IssueBean{issueBean("A-1"), issueBean("A-2")}),
		}},
		{Results: &jira.SearchResults{
			IsLast:        jira.NullableOf(false),
			NextPageToken: jira.NullableOf("page-3"),
			Issues:        jira.NullableOf([]jira.IssueBean{issueBean("A-3")}),
		}},
	}}

	out, err := autoPaginate(t.Context(), fake, "project = A", nil, "", listWalk{limit: 3})
	if err != nil {
		t.Fatalf("autoPaginate: %v", err)
	}
	if len(out.Issues) != 3 {
		t.Fatalf("issues = %d, want exactly 3", len(out.Issues))
	}
	if len(fake.calls) != 2 {
		t.Fatalf("seam calls = %d, want exactly 2", len(fake.calls))
	}
	// First request: remaining need is 3, so it asks for exactly 3, not
	// maxRequestPageSize.
	if fake.calls[0].maxResults != 3 {
		t.Errorf("first call maxResults = %d, want 3 (the full remaining need)", fake.calls[0].maxResults)
	}
	if fake.calls[0].nextPageToken != "" {
		t.Errorf("first call nextPageToken = %q, want empty (first page)", fake.calls[0].nextPageToken)
	}
	// Second request: 2 collected, 1 still needed, so it asks for
	// exactly 1 — never more than the remaining need.
	if fake.calls[1].maxResults != 1 {
		t.Errorf("second call maxResults = %d, want 1 (the remaining need)", fake.calls[1].maxResults)
	}
	if fake.calls[1].nextPageToken != "page-2" {
		t.Errorf("second call nextPageToken = %q, want %q", fake.calls[1].nextPageToken, "page-2")
	}
	if out.Truncated {
		t.Error("truncated = true, want false — limit was satisfied, not capped")
	}
}

// TestAutoPaginateStopsWithoutExtraCallWhenFewerResultsThanLimit is the
// negative counterpart: a search with fewer results than --limit must
// not make a call past the last page, is_last must read true, and the
// reported count must be the honest total actually collected.
func TestAutoPaginateStopsWithoutExtraCallWhenFewerResultsThanLimit(t *testing.T) {
	fake := &fakeListClient{pages: []core.IssueSearchRead{
		{Results: &jira.SearchResults{
			IsLast: jira.NullableOf(true),
			Issues: jira.NullableOf([]jira.IssueBean{issueBean("A-1"), issueBean("A-2")}),
		}},
	}}

	out, err := autoPaginate(t.Context(), fake, "project = A", nil, "", listWalk{limit: 50})
	if err != nil {
		t.Fatalf("autoPaginate: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("seam calls = %d, want exactly 1 — no call belongs past the last page", len(fake.calls))
	}
	if len(out.Issues) != 2 {
		t.Fatalf("issues = %d, want 2", len(out.Issues))
	}
	if !out.Pagination.IsLast {
		t.Error("is_last = false, want true")
	}
	if out.Pagination.Returned != 2 {
		t.Errorf("returned = %d, want the honest count of 2", out.Pagination.Returned)
	}
}

// TestAutoPaginateStopsOnMissingCursor distinguishes an incomplete response
// from an exhausted search. An advancing empty page is valid; this page lacks
// the cursor needed to continue.
func TestAutoPaginateStopsOnMissingCursor(t *testing.T) {
	fake := &fakeListClient{pages: []core.IssueSearchRead{
		{Results: &jira.SearchResults{
			IsLast: jira.NullableOf(false),
			Issues: jira.NullableOf([]jira.IssueBean{}),
		}},
	}}

	out, err := autoPaginate(t.Context(), fake, "project = A", nil, "", listWalk{limit: 50})
	if err != nil {
		t.Fatalf("autoPaginate: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("seam calls = %d, want exactly 1 without a continuation cursor", len(fake.calls))
	}
	if len(out.Issues) != 0 {
		t.Errorf("issues = %d, want 0", len(out.Issues))
	}
	if !out.Truncated || out.TruncatedReason != envelope.TruncatedReasonMissingCursor {
		t.Errorf("missing cursor was not reported as incomplete: truncated=%t reason=%q", out.Truncated, out.TruncatedReason)
	}
}

// TestAutoPaginateStopsOnNonAdvancingCursor guards the other infinite-
// loop shape: a page whose own nextPageToken echoes back the SAME token
// that was just requested must stop the walk rather than re-fetch the
// identical page forever.
func TestAutoPaginateStopsOnNonAdvancingCursor(t *testing.T) {
	fake := &fakeListClient{pages: []core.IssueSearchRead{
		{Results: &jira.SearchResults{
			IsLast:        jira.NullableOf(false),
			NextPageToken: jira.NullableOf("page-2"),
			Issues:        jira.NullableOf([]jira.IssueBean{issueBean("A-1")}),
		}},
		{Results: &jira.SearchResults{
			IsLast: jira.NullableOf(false),
			// Echoes the SAME token this call was just made with —
			// a server bug (or a stub that never advances), not
			// progress.
			NextPageToken: jira.NullableOf("page-2"),
			Issues:        jira.NullableOf([]jira.IssueBean{issueBean("A-2")}),
		}},
	}}

	out, err := autoPaginate(t.Context(), fake, "project = A", nil, "", listWalk{limit: 50})
	if err != nil {
		t.Fatalf("autoPaginate: %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("seam calls = %d, want exactly 2 — the walk must stop the call AFTER it sees the non-advancing cursor, not spin further", len(fake.calls))
	}
	if len(out.Issues) != 2 {
		t.Errorf("issues = %d, want 2 (both fetched pages kept)", len(out.Issues))
	}
	if !out.Truncated || out.TruncatedReason != envelope.TruncatedReasonRepeatedCursor {
		t.Errorf("repeated cursor was not reported as incomplete: truncated=%t reason=%q", out.Truncated, out.TruncatedReason)
	}
}

// TestAutoPaginateAllDrainsUntilLastPage proves --all walks past what a
// bounded --limit would have stopped at, collecting every page up to
// the search's own is_last=true.
func TestAutoPaginateAllDrainsUntilLastPage(t *testing.T) {
	fake := &fakeListClient{pages: []core.IssueSearchRead{
		{Results: &jira.SearchResults{
			IsLast:        jira.NullableOf(false),
			NextPageToken: jira.NullableOf("page-2"),
			Issues:        jira.NullableOf([]jira.IssueBean{issueBean("A-1")}),
		}},
		{Results: &jira.SearchResults{
			IsLast: jira.NullableOf(true),
			Issues: jira.NullableOf([]jira.IssueBean{issueBean("A-2")}),
		}},
	}}

	// limit is deliberately tiny (1) and irrelevant: all==true means
	// limit is ignored entirely.
	out, err := autoPaginate(t.Context(), fake, "project = A", nil, "", listWalk{all: true})
	if err != nil {
		t.Fatalf("autoPaginate: %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("seam calls = %d, want exactly 2 — --all must not stop at limit's own count", len(fake.calls))
	}
	if len(out.Issues) != 2 {
		t.Errorf("issues = %d, want 2", len(out.Issues))
	}
	if out.Truncated {
		t.Error("truncated = true, want false — the drain reached the search's own last page, not a cap")
	}
	if !out.Pagination.IsLast {
		t.Error("is_last = false, want true")
	}
}

// TestAutoPaginateAllReportsTruncationAtPageCap proves --all's own page
// cap stops the walk honestly: Truncated is set with the reason naming
// the cap, and the walk requests no more than maxAllPages pages.
func TestAutoPaginateAllReportsTruncationAtPageCap(t *testing.T) {
	pages := make([]core.IssueSearchRead, 0, maxAllPages+1)
	for i := range maxAllPages + 1 {
		pages = append(pages, core.IssueSearchRead{Results: &jira.SearchResults{
			IsLast:        jira.NullableOf(false),
			NextPageToken: jira.NullableOf(fmt.Sprintf("page-%d", i+2)),
			Issues:        jira.NullableOf([]jira.IssueBean{issueBean(fmt.Sprintf("A-%d", i))}),
		}})
	}
	fake := &fakeListClient{pages: pages}

	out, err := autoPaginate(t.Context(), fake, "project = A", nil, "", listWalk{all: true})
	if err != nil {
		t.Fatalf("autoPaginate: %v", err)
	}
	if len(fake.calls) != maxAllPages {
		t.Fatalf("seam calls = %d, want exactly maxAllPages (%d) — the cap must stop the walk, not overshoot it", len(fake.calls), maxAllPages)
	}
	if !out.Truncated {
		t.Fatal("truncated = false, want true — the page cap stopped the walk before is_last")
	}
	if out.TruncatedReason != envelope.TruncatedReasonMaxPages {
		t.Errorf("truncated_reason = %q, want %q", out.TruncatedReason, envelope.TruncatedReasonMaxPages)
	}
	if out.Pagination.IsLast {
		t.Error("is_last = true, want false — the search was not actually exhausted, only capped")
	}
}

// TestAutoPaginateAllReportsTruncationAtIssueCap uses valid-sized pages to
// reach the issue bound. If both bounds are reached, the item bound is the
// specific reason; a smaller walk still reports the page bound separately.
func TestAutoPaginateAllReportsTruncationAtIssueCap(t *testing.T) {
	pages := make([]core.IssueSearchRead, 0, maxAllPages)
	for start := 0; start < maxAllIssues; start += maxRequestPageSize {
		beans := make([]jira.IssueBean, maxRequestPageSize)
		for i := range beans {
			beans[i] = issueBean(fmt.Sprintf("A-%d", start+i+1))
		}
		pages = append(pages, core.IssueSearchRead{Results: &jira.SearchResults{IsLast: jira.NullableOf(false), NextPageToken: jira.NullableOf(fmt.Sprintf("page-%d", len(pages)+1)), Issues: jira.NullableOf(beans)}})
	}
	fake := &fakeListClient{pages: pages}
	out, err := autoPaginate(t.Context(), fake, "project = A", nil, "", listWalk{all: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != maxAllIssues/maxRequestPageSize || len(out.Issues) != maxAllIssues || !out.Truncated || out.TruncatedReason != envelope.TruncatedReasonMaxIssues || out.Pagination.IsLast {
		t.Fatalf("item cap did not stop with truthful retained rows: %+v calls=%d", out, len(fake.calls))
	}
	for _, call := range fake.calls {
		if call.maxResults > maxRequestPageSize {
			t.Fatalf("oversized request: %d", call.maxResults)
		}
	}
}

// TestAutoPaginateHonorsContextCancellation proves the walk checks
// context cancellation BETWEEN pages and returns promptly rather than
// making a further request once the context is done.
func TestAutoPaginateHonorsContextCancellation(t *testing.T) {
	fake := &fakeListClient{pages: []core.IssueSearchRead{
		{Results: &jira.SearchResults{
			IsLast:        jira.NullableOf(false),
			NextPageToken: jira.NullableOf("page-2"),
			Issues:        jira.NullableOf([]jira.IssueBean{issueBean("A-1")}),
		}},
	}}

	ctx, cancel := context.WithCancel(t.Context())
	// Canceled BEFORE the walk ever starts: the loop's own ctx.Err()
	// check at the top of the FIRST iteration is what this proves — a
	// context canceled between pages is the same code path, exercised
	// here at its simplest.
	cancel()

	_, err := autoPaginate(ctx, fake, "project = A", nil, "", listWalk{limit: 50})
	if err == nil {
		t.Fatal("autoPaginate returned nil error against an already-canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("seam calls = %d, want 0 — a canceled context must be checked before the first request too", len(fake.calls))
	}
}

// renderIssueList runs renderList against a built envelope, returning
// what it wrote — the list sibling of view_test.go's renderIssue and
// comment_test.go's renderComments helpers. site feeds the KEY column's
// hyperlink (cli.IssueKey); every existing caller below passes "" — the
// "no site" degrade cli.IssueKey already covers on its own — and the
// hyperlink-specific tests further down pass a real one. estimate feeds
// the same shape runList's own fetchCountEstimate would carry from Run
// to Human; every existing caller below passes the zero countEstimate{}
// (unknown — no count call ever happened), and the estimate-specific
// test further down passes a resolved one.
func renderIssueList(t *testing.T, out envelope.IssueListOutput, mode clog.ColorMode, site string, estimate countEstimate) string {
	t.Helper()
	withStyle(t, mode == clog.ColorAlways)

	var buf bytes.Buffer
	v := output.NewView(&buf, mode)
	env := envelope.Envelope{OK: true, Data: out}
	if err := renderList(v, env, site, estimate, nil); err != nil {
		t.Fatalf("renderList: %v", err)
	}
	return buf.String()
}

// TestRenderListEmptyIsOneHonestLine proves the empty-result contract:
// one plain line, never an empty table (output.Table itself writes
// nothing at all for zero items, so the empty state has to be this
// command's own).
func TestRenderListEmptyIsOneHonestLine(t *testing.T) {
	got := renderIssueList(t, envelope.IssueListOutput{}, clog.ColorNever, "", countEstimate{})

	if !strings.Contains(got, "No issues found.") {
		t.Errorf("output = %q, want the explicit no-issues line", got)
	}
}

// TestRenderListShowsApproximateCountAndSkipsTable proves --count's own
// render branch: the `~` marker carried through (Approximate must never
// read as an exact total the approximate-count endpoint never promised —
// core.JiraClient.CountIssues's own doc comment), thousands-grouped, and
// no table/empty-state line at all — --count's whole point is reporting
// how many issues match WITHOUT listing them.
func TestRenderListShowsApproximateCountAndSkipsTable(t *testing.T) {
	out := envelope.IssueListOutput{Count: &envelope.IssueCountEstimate{Value: 1234, Approximate: true}}
	got := renderIssueList(t, out, clog.ColorNever, "", countEstimate{})

	if !strings.Contains(got, "~1,234 issues match.") {
		t.Errorf("output = %q, want the approximate count line", got)
	}
	if strings.Contains(got, "No issues found.") {
		t.Errorf("output = %q, --count must not render the empty-search line", got)
	}
	if strings.Contains(got, "KEY") || strings.Contains(got, "SUMMARY") {
		t.Errorf("output = %q, --count must not render a table", got)
	}
}

func TestRenderListShowsTableColumns(t *testing.T) {
	out := envelope.IssueListOutput{
		Issues: []envelope.IssueView{
			{
				IssueRef: envelope.IssueRef{Key: "ABC-1"},
				Fields: jira.IssueFields{
					Summary:   jira.NullableOf("Fix the thing"),
					Status:    jira.NullableOf(jira.StatusDetails{Name: jira.NullableOf("Done")}),
					Issuetype: jira.NullableOf(jira.IssueTypeDetails{Name: jira.NullableOf("Bug")}),
					Assignee:  jira.NullableOf(jira.UserDetails{DisplayName: jira.NullableOf("Ada Lovelace")}),
				},
			},
		},
		Pagination: envelope.CursorPagination{IsLast: true},
	}
	got := renderIssueList(t, out, clog.ColorNever, "", countEstimate{})

	for _, want := range []string{"ABC-1", "Fix the thing", "Done", "Bug", "Ada Lovelace"} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want it to contain %q", got, want)
		}
	}
}

// TestRenderListStatusPillAlignsColumnsAcrossDifferentWidthPills proves
// the STATUS column's own PILL (cli.Status's background + one space of
// padding either side, wider than the bare status name) does not
// misalign the columns after it: two rows carrying very
// differently-sized status names/categories — one short and default
// ("To Do", category new), one long and custom ("Ready for a Very
// Thorough Review", category indeterminate — the exact regression this
// task fixes) — must still start their SUMMARY column at the identical
// visible offset. The sibling of internal/output's own
// TestTableAlignsStyledCellsByPlainWidthNotRawBytes, but exercised
// through this command's real cli.Status pill output rather than a
// hand-wrapped ANSI fake, since the pill's own padding is exactly the
// new width primer has to measure correctly.
func TestRenderListStatusPillAlignsColumnsAcrossDifferentWidthPills(t *testing.T) {
	// Pin the glyph set: this asserts the padded-space pill's own shape,
	// and without pinning it reads the DEVELOPER's `glyphs` key — a
	// machine set to nerd renders capped pills instead and the assertion
	// fails for a reason that has nothing to do with alignment. Exactly
	// how this test first went red.
	withGlyphs(t, config.GlyphSetDefault)
	out := envelope.IssueListOutput{
		Issues: []envelope.IssueView{
			{
				IssueRef: envelope.IssueRef{Key: "ABC-1"},
				Fields: jira.IssueFields{
					Summary: jira.NullableOf("Short row"),
					Status: jira.NullableOf(jira.StatusDetails{
						Name:           jira.NullableOf("To Do"),
						StatusCategory: jira.NullableOf(jira.StatusCategory{Key: jira.NullableOf("new")}),
					}),
				},
			},
			{
				IssueRef: envelope.IssueRef{Key: "ABC-2"},
				Fields: jira.IssueFields{
					Summary: jira.NullableOf("Long row"),
					Status: jira.NullableOf(jira.StatusDetails{
						Name:           jira.NullableOf("Ready for a Very Thorough Review"),
						StatusCategory: jira.NullableOf(jira.StatusCategory{Key: jira.NullableOf("indeterminate")}),
					}),
				},
			},
		},
		Pagination: envelope.CursorPagination{IsLast: true},
	}

	got := renderIssueList(t, out, clog.ColorAlways, "", countEstimate{})

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("got %d lines, want at least 3 (header + 2 rows): %q", len(lines), lines)
	}

	headerCol := strings.Index(ansi.Strip(lines[0]), "SUMMARY")
	row1Col := strings.Index(ansi.Strip(lines[1]), "Short row")
	row2Col := strings.Index(ansi.Strip(lines[2]), "Long row")
	if headerCol < 0 || row1Col < 0 || row2Col < 0 {
		t.Fatalf("missing an expected column value: header=%d row1=%d row2=%d, lines=%q", headerCol, row1Col, row2Col, lines)
	}
	if headerCol != row1Col || row1Col != row2Col {
		t.Errorf("SUMMARY column not aligned despite differently-sized STATUS pills: header at %d, row1 at %d, row2 at %d (%q)", headerCol, row1Col, row2Col, lines)
	}
}

// TestRenderListLinksKeyColumnWhenStyledAndSiteSet is the KEY column's
// sibling of view_test.go's TestRenderViewLinksGlanceKeyWhenStyledAndSiteSet:
// the same three-way IssueKey contract (link only when BOTH styling and
// site are real), proven at this command's own render layer.
func TestRenderListLinksKeyColumnWhenStyledAndSiteSet(t *testing.T) {
	const site = "https://example.atlassian.net"
	out := envelope.IssueListOutput{
		Issues:     []envelope.IssueView{{IssueRef: envelope.IssueRef{Key: "ABC-1"}}},
		Pagination: envelope.CursorPagination{IsLast: true},
	}

	t.Run("styled and site set: link", func(t *testing.T) {
		got := renderIssueList(t, out, clog.ColorAlways, site, countEstimate{})
		wantURL := site + "/browse/ABC-1"
		if !strings.Contains(got, wantURL) {
			t.Errorf("output = %q, want the KEY column linked to %q", got, wantURL)
		}
	})

	t.Run("styled but no site: plain key, no partial URL", func(t *testing.T) {
		got := renderIssueList(t, out, clog.ColorAlways, "", countEstimate{})
		if strings.Contains(got, "/browse/") || strings.Contains(got, "]8;;") {
			t.Errorf("output = %q, built a link with no site to link to", got)
		}
		if !strings.Contains(got, "ABC-1") {
			t.Errorf("output = %q, lost the key along with the link", got)
		}
	})

	t.Run("site set but not styled: plain key, no link", func(t *testing.T) {
		got := renderIssueList(t, out, clog.ColorNever, site, countEstimate{})
		if strings.Contains(got, "]8;;") {
			t.Errorf("output = %q, carries a hyperlink escape sequence with styling disabled", got)
		}
		if !strings.Contains(got, "ABC-1") {
			t.Errorf("output = %q, lost the key", got)
		}
	})
}

// TestRenderListShowsNextPageHintWhenNotLast pins the discoverability
// requirement: a page that is not the last must name --limit as the
// action to take, and must NOT show the raw cursor, a count-of-total,
// or any base64 token — human output never carries the resume cursor.
// estimate is left at its zero value (no count call ever happened), so
// this also pins the count-free wording's own negative: no `~`, since
// nothing here is an estimate — TestRenderListShowsEstimateWithThousandsSeparator
// below is this test's positive counterpart.
func TestRenderListShowsNextPageHintWhenNotLast(t *testing.T) {
	out := envelope.IssueListOutput{
		Issues:     []envelope.IssueView{{IssueRef: envelope.IssueRef{Key: "ABC-1"}}},
		Pagination: envelope.CursorPagination{IsLast: false, NextPageToken: "cursor-xyz", Returned: 1},
	}
	got := renderIssueList(t, out, clog.ColorNever, "", countEstimate{})

	if !strings.Contains(got, "--limit") {
		t.Errorf("output = %q, want a hint naming --limit", got)
	}
	if strings.Contains(got, "cursor-xyz") {
		t.Errorf("output = %q, the raw cursor must never reach human output", got)
	}
	if strings.Contains(got, "--next-page-token") {
		t.Errorf("output = %q, --next-page-token must never be advertised in human output", got)
	}
	if strings.Contains(got, "of ") || strings.Contains(got, "total") {
		t.Errorf("output = %q, must not imply a total this endpoint never sent", got)
	}
	if strings.Contains(got, "~") {
		t.Errorf("output = %q, must not carry the estimate's own `~` when no estimate was ever resolved", got)
	}
	if !strings.Contains(got, "Showing 1") {
		t.Errorf("output = %q, want the honest returned count", got)
	}
}

// TestRenderListShowsEstimateWithThousandsSeparator is
// TestRenderListShowsNextPageHintWhenNotLast's positive counterpart: a
// RESOLVED estimate renders "of ~N" with N thousands-grouped (gechr/x/
// human.FormatNumber) and the `~` prefix present — load-bearing, since
// it is the only thing on this line distinguishing an ESTIMATE from an
// exact total the search endpoint never sent.
func TestRenderListShowsEstimateWithThousandsSeparator(t *testing.T) {
	out := envelope.IssueListOutput{
		Issues:     []envelope.IssueView{{IssueRef: envelope.IssueRef{Key: "ABC-1"}}},
		Pagination: envelope.CursorPagination{IsLast: false, NextPageToken: "cursor-xyz", Returned: 50},
	}
	got := renderIssueList(t, out, clog.ColorNever, "", countEstimate{value: 1234, known: true})

	if !strings.Contains(got, "Showing 50 of ~1,234") {
		t.Errorf("output = %q, want the estimate rendered with a thousands separator and the `~` prefix", got)
	}
}

// spansAreSelfContained is this file's own byte-level H5 check: it walks
// s's SGR codes in encounter order and confirms every styled span is
// self-contained — no fresh "open" code starts before the CURRENTLY
// open span's own reset has closed it, and no reset appears with
// nothing open to close. That is the exact shape a colored number
// landing INSIDE cli.Hint's backtick span would produce: BacktickMode.
// Render would call code.Render on content that already carries cli.
// Number's own open/reset pair, wrapping ANOTHER open around it and
// leaving a reset in the MIDDLE of the outer span rather than only at
// its own end.
//
// This is deliberately NOT comment_test.go's resetsStayTrailing:
// that helper polices a narrower shape — several spans separated by
// nothing but WHITESPACE, as in clog's Divider title (rule, space,
// title, space, rule) — and reusing it on nextPageHint's own output
// produces a false failure, because the real prose between its spans
// ("of ~", "raise ", " for more.") is not whitespace, yet is entirely
// legitimate: independently self-closed spans with ordinary words
// between them are not nested inside each other, which is exactly what
// cli.Number's own composition with cli.Hint is supposed to look like.
// TestSpansAreSelfContainedCatchesNestedNumber below proves this
// function still catches the actual bad shape.
func spansAreSelfContained(s string) bool {
	depth := 0
	for _, m := range sgrCode.FindAllString(s, -1) {
		params := strings.TrimSuffix(strings.TrimPrefix(m, "\x1b["), "m")
		if params == "" || params == "0" {
			if depth == 0 {
				return false // a reset with nothing open to close
			}
			depth--
			continue
		}
		if depth != 0 {
			return false // a fresh open before the current span's own reset
		}
		depth++
	}
	return depth == 0 // every opened span closed; none left dangling
}

// TestRenderListHintColoredNumberNestsWithBacktickSpan is nextPageHint's
// own H5 proof: the estimate/count-known branch is the one shape that
// carries BOTH a cli.Number-colored span AND cli.Hint's own
// backtick-styled `--limit` span in the SAME string — exactly the
// combination cli.Number's own doc comment (sinks.go) warns must never
// nest a styled span inside another. Calling nextPageHint directly,
// rather than through renderIssueList, isolates this one hint line from
// the table's own ANSI (entity colors, hyperlinks) that a full render
// would otherwise mix in.
func TestRenderListHintColoredNumberNestsWithBacktickSpan(t *testing.T) {
	withStyle(t, true)

	out := envelope.IssueListOutput{
		Pagination: envelope.CursorPagination{IsLast: false, Returned: 50},
	}
	got := nextPageHint(out, countEstimate{value: 1234567, known: true})

	if !strings.ContainsRune(got, 0x1b) {
		t.Fatalf("nextPageHint(...) = %q, want it styled — this test would prove nothing unstyled", got)
	}
	if !strings.Contains(got, "1,234,567") {
		t.Errorf("nextPageHint(...) = %q, lost the formatted estimate", got)
	}
	if !strings.Contains(got, "--limit") {
		t.Errorf("nextPageHint(...) = %q, lost the flag mention this test needs alongside the number — BacktickStrip drops the delimiters once styled, so this checks for the bare flag name, not backticks", got)
	}
	if !spansAreSelfContained(got) {
		t.Errorf("nextPageHint(...) = %q, has an SGR span nested inside another (H5)", got)
	}
}

// TestSpansAreSelfContainedCatchesNestedNumber is the mutation-style
// proof spansAreSelfContained's own doc comment promises: constructing
// the ACTUAL H5 trap — a colored number placed INSIDE a backtick span,
// so cli.Hint's BacktickMode.Render wraps a fresh style around content
// that already carries cli.Number's own open/reset pair — and confirming
// the checker reports it false. Without this, a checker that always
// returns true would pass the test above for the wrong reason.
func TestSpansAreSelfContainedCatchesNestedNumber(t *testing.T) {
	withStyle(t, true)

	bad := cli.Hint(fmt.Sprintf("Showing `%s` — raise `--limit` for more.", cli.Number(50)))

	if !strings.ContainsRune(bad, 0x1b) {
		t.Fatalf("bad = %q, want it styled — this test would prove nothing unstyled", bad)
	}
	if spansAreSelfContained(bad) {
		t.Errorf("bad = %q, want spansAreSelfContained to report a nested span, not a well-formed one", bad)
	}
}

// TestRenderListShowsExactCountOnLastPage proves the "everything was
// returned" shape: a complete page states the count PLAINLY — no `~`
// (nothing here came from the estimate endpoint; Returned already IS
// exact — see fetchCountEstimate's own first gate, which skips the
// count call entirely for this shape) and no "--limit" suggestion
// (there is nothing more to raise it for).
func TestRenderListShowsExactCountOnLastPage(t *testing.T) {
	out := envelope.IssueListOutput{
		Issues:     []envelope.IssueView{{IssueRef: envelope.IssueRef{Key: "ABC-1"}}},
		Pagination: envelope.CursorPagination{IsLast: true, Returned: 1},
	}
	got := renderIssueList(t, out, clog.ColorNever, "", countEstimate{})

	if !strings.Contains(got, "Showing 1.") {
		t.Errorf("output = %q, want an exact-count hint on a complete page", got)
	}
	if strings.Contains(got, "--limit") {
		t.Errorf("output = %q, --limit is meaningless once every result is already shown", got)
	}
	if strings.Contains(got, "~") {
		t.Errorf("output = %q, an exact count must never carry the estimate's own `~` prefix", got)
	}
}

// TestRenderListReportsTruncationHonestly proves --all's own capped
// drain says so honestly in the human view, naming the cap rather than
// suggesting --limit (which is meaningless once --all is already
// draining) or showing any cursor.
func TestRenderListReportsTruncationHonestly(t *testing.T) {
	out := envelope.IssueListOutput{
		Issues:          []envelope.IssueView{{IssueRef: envelope.IssueRef{Key: "ABC-1"}}},
		Pagination:      envelope.CursorPagination{IsLast: false, NextPageToken: "cursor-xyz", Returned: 1},
		Truncated:       true,
		TruncatedReason: envelope.TruncatedReasonMaxPages,
	}
	got := renderIssueList(t, out, clog.ColorNever, "", countEstimate{})

	if !strings.Contains(got, "--all") {
		t.Errorf("output = %q, want the hint to name --all", got)
	}
	if strings.Contains(got, "cursor-xyz") {
		t.Errorf("output = %q, the raw cursor must never reach human output", got)
	}
	if strings.Contains(got, "raise `--limit`") || strings.Contains(got, "raise --limit") {
		t.Errorf("output = %q, --limit is meaningless alongside --all and must not be suggested", got)
	}
}

// TestRenderListHintDegradesToPlainUnderNoColor is the H4 proof for the
// styled hint specifically: with color disabled, the backtick-quoted
// flag mention must still carry zero escape bytes — plain backticks,
// not stripped-and-colored, not raw ANSI.
func TestRenderListHintDegradesToPlainUnderNoColor(t *testing.T) {
	out := envelope.IssueListOutput{
		Issues:     []envelope.IssueView{{IssueRef: envelope.IssueRef{Key: "ABC-1"}}},
		Pagination: envelope.CursorPagination{IsLast: false, NextPageToken: "cursor-xyz", Returned: 1},
	}
	got := renderIssueList(t, out, clog.ColorNever, "", countEstimate{})

	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("output = %q, carries an escape byte under NO_COLOR", got)
	}
	if !strings.Contains(got, "`--limit`") {
		t.Errorf("output = %q, want the plain backtick-quoted flag mention when styling is off", got)
	}
}

// TestRenderListSanitizesHostileFields is the H4 proof for this
// command, mirroring TestRenderCommentListSanitizesHostileAuthorAndBody:
// with color disabled, hostile status/summary/assignee/key values must
// render with not one escape byte.
func TestRenderListSanitizesHostileFields(t *testing.T) {
	hostile := "Eve\x1b[2K\rSUDO PASSWORD:\x07"
	out := envelope.IssueListOutput{
		Issues: []envelope.IssueView{
			{
				IssueRef: envelope.IssueRef{Key: "ABC-1" + hostile},
				Fields: jira.IssueFields{
					Summary:  jira.NullableOf("Summary " + hostile),
					Status:   jira.NullableOf(jira.StatusDetails{Name: jira.NullableOf("Status " + hostile)}),
					Assignee: jira.NullableOf(jira.UserDetails{DisplayName: jira.NullableOf("Assignee " + hostile)}),
				},
			},
		},
		Pagination: envelope.CursorPagination{IsLast: false, NextPageToken: "token" + hostile},
	}
	got := renderIssueList(t, out, clog.ColorNever, "", countEstimate{})

	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("output = %q, an escape sequence reached the terminal", got)
	}
	if strings.ContainsRune(got, '\r') || strings.ContainsRune(got, '\x07') {
		t.Errorf("output = %q, a control byte reached the terminal", got)
	}
}

// TestRenderListNoRawISOTimestamp is the H3 proof: the Updated column
// must show a humanized relative time, never the wire's raw ISO-8601
// value.
func TestRenderListNoRawISOTimestamp(t *testing.T) {
	out := envelope.IssueListOutput{
		Issues: []envelope.IssueView{
			{
				IssueRef: envelope.IssueRef{Key: "ABC-1"},
				Fields: jira.IssueFields{
					Updated: jira.NullableOf("2024-01-15T09:30:00.000+0000"),
				},
			},
		},
		Pagination: envelope.CursorPagination{IsLast: true},
	}
	got := renderIssueList(t, out, clog.ColorNever, "", countEstimate{})

	if strings.Contains(got, "2024-01-15T09:30:00") {
		t.Errorf("output = %q, carries a raw ISO-8601 timestamp", got)
	}
}

// withGlyphs pins the glyph vocabulary for one test, the sibling of
// withStyle above: both stop a renderer's shape depending on the machine
// it runs on rather than on what the test set up.
func withGlyphs(t *testing.T, set config.GlyphSet) {
	t.Helper()
	cli.SetGlyphs(&set)
	t.Cleanup(func() { cli.SetGlyphs(nil) })
}

// TestValidateUnboundedRefusedWithoutAll: --unbounded lifts a cap that
// only --all imposes, so on its own it would do nothing — and a caller
// who typed it believing it fetches everything would be left with
// --limit's default 50 and no way to tell the flag was inert.
func TestValidateUnboundedRefusedWithoutAll(t *testing.T) {
	err := validateUnbounded(true, false)
	if err == nil {
		t.Fatal("--unbounded alone was accepted — it would silently do nothing")
	}
	if !strings.Contains(err.Error(), "--all") {
		t.Errorf("refusal does not name the flag that actually drains: %v", err)
	}
}

func TestValidateUnboundedAllowedWithAll(t *testing.T) {
	if err := validateUnbounded(true, true); err != nil {
		t.Errorf("--all --unbounded was refused: %v", err)
	}
}

// TestNextRequestSizeIgnoresTheIssueCapWhenUnbounded pins the mechanism
// --unbounded actually changes: a capped drain shrinks its last request
// to the distance left to maxAllIssues, and an uncapped one has no such
// distance, so it keeps asking for full pages however many it has
// collected.
func TestNextRequestSizeIgnoresTheIssueCapWhenUnbounded(t *testing.T) {
	nearCap := maxAllIssues - 10
	if got := nextRequestSize(listWalk{all: true}, nearCap); got != 10 {
		t.Errorf("capped drain asked for %d at %d collected, want 10 (the distance left to the cap)", got, nearCap)
	}
	if got := nextRequestSize(listWalk{all: true, unbounded: true}, nearCap); got != maxRequestPageSize {
		t.Errorf("uncapped drain asked for %d at %d collected, want a full page of %d",
			got, nearCap, maxRequestPageSize)
	}
}

// TestAutoPaginateUnboundedWalksPastThePageCap is the behavioral half:
// a search longer than maxAllPages stops a capped drain and does not stop
// an uncapped one — and the uncapped result must NOT be marked truncated,
// because nothing of ours cut it short.
func TestAutoPaginateUnboundedWalksPastThePageCap(t *testing.T) {
	// One issue per page and always another cursor, scripted two pages
	// past the cap so an uncapped walk has somewhere to go: the page cap
	// is the only thing that can end the capped walk here.
	script := func() []core.IssueSearchRead {
		pages := make([]core.IssueSearchRead, 0, maxAllPages+2)
		for i := range maxAllPages + 2 {
			pages = append(pages, core.IssueSearchRead{Results: &jira.SearchResults{
				IsLast:        jira.NullableOf(i == maxAllPages+1),
				NextPageToken: jira.NullableOf(fmt.Sprintf("page-%d", i+2)),
				Issues:        jira.NullableOf([]jira.IssueBean{issueBean(fmt.Sprintf("A-%d", i))}),
			}})
		}
		return pages
	}

	capped := &fakeListClient{pages: script()}
	cappedOut, err := autoPaginate(t.Context(), capped, "project = A", nil, "", listWalk{all: true})
	if err != nil {
		t.Fatalf("capped autoPaginate: %v", err)
	}
	if !cappedOut.Truncated || cappedOut.TruncatedReason != envelope.TruncatedReasonMaxPages {
		t.Fatalf("capped drain: truncated=%v reason=%q, want true/%s",
			cappedOut.Truncated, cappedOut.TruncatedReason, envelope.TruncatedReasonMaxPages)
	}
	if len(capped.calls) != maxAllPages {
		t.Fatalf("capped drain made %d calls, want exactly maxAllPages (%d)", len(capped.calls), maxAllPages)
	}

	unbounded := &fakeListClient{pages: script()}
	unboundedOut, err := autoPaginate(t.Context(), unbounded, "project = A", nil, "",
		listWalk{all: true, unbounded: true})
	if err != nil {
		t.Fatalf("unbounded autoPaginate: %v", err)
	}
	if len(unbounded.calls) <= maxAllPages {
		t.Fatalf("unbounded drain made %d calls, still inside the %d-page cap it was meant to lift",
			len(unbounded.calls), maxAllPages)
	}
	if unboundedOut.Truncated {
		t.Errorf("unbounded drain reported truncated=true — nothing of ours cut it short")
	}
	if !unboundedOut.Pagination.IsLast {
		t.Errorf("unbounded drain did not reach the search's own last page")
	}
}

// TestValidateAsJQLRefusesEveryFlagThatNeedsASearch walks the whole set
// in one table, because the property under test is that NONE of them is
// silently ignored — a per-flag test would pass while a fifth flag went
// unchecked.
func TestValidateAsJQLRefusesEveryFlagThatNeedsASearch(t *testing.T) {
	cases := []struct {
		name  string
		field string
		call  func() error
	}{
		{"count", "count", func() error { return validateAsJQL(true, false, false, true, false) }},
		{"web", "web", func() error { return validateAsJQL(true, false, false, false, true) }},
		{"limit", "limit", func() error { return validateAsJQL(true, true, false, false, false) }},
		{"all", "all", func() error { return validateAsJQL(true, false, true, false, false) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err != nil {
				var input *cli.InputError
				if errors.As(err, &input) && input.Field != tc.field {
					t.Errorf("field = %q, want %q", input.Field, tc.field)
				}
				return
			}
			t.Fatalf("--as-jql --%s was accepted — one of the two answers would be silently dropped", tc.name)
		})
	}
}

func TestValidateAsJQLAllowsTheOrdinaryPreview(t *testing.T) {
	if err := validateAsJQL(true, false, false, false, false); err != nil {
		t.Errorf("a bare --as-jql was refused: %v", err)
	}
	if err := validateAsJQL(false, true, true, true, true); err != nil {
		t.Errorf("the rule fired without --as-jql: %v", err)
	}
}

// TestRenderListAsJQLPrintsTheBareQuery pins the property that makes the
// flag useful: the output is the query and nothing else, so it can be
// piped straight back into --jql. A hint, a header or a table would each
// have to be stripped first.
func TestRenderListAsJQLPrintsTheBareQuery(t *testing.T) {
	const query = `project in ("ABC") AND status in ("Open")`
	got := renderIssueList(t, envelope.IssueListOutput{
		Issues: []envelope.IssueView{},
		JQL:    query,
	}, clog.ColorNever, "", countEstimate{})

	if strings.TrimSpace(got) != query {
		t.Errorf("rendered %q, want exactly %q — the line must be paste-ready for --jql", got, query)
	}
}

// TestRenderListAsJQLBeatsTheEmptyResultMessage: --as-jql never ran a
// search, so Issues is empty for a reason that has nothing to do with a
// zero-result query. Reaching the "No issues found." fallback would
// report the preview as a search that matched nothing.
func TestRenderListAsJQLBeatsTheEmptyResultMessage(t *testing.T) {
	got := renderIssueList(t, envelope.IssueListOutput{
		Issues: []envelope.IssueView{},
		JQL:    `project in ("ABC")`,
	}, clog.ColorNever, "", countEstimate{})

	if strings.Contains(got, "No issues found") {
		t.Errorf("the preview rendered as an empty search result: %q", got)
	}
}

// TestNextPageHintNamesTheEscapeHatchWhenCapped: a cap that bites must
// say how to lift it, or a caller reading "more results exist" has been
// told about a wall and not about the door.
func TestNextPageHintNamesTheEscapeHatchWhenCapped(t *testing.T) {
	out := envelope.IssueListOutput{Truncated: true, TruncatedReason: envelope.TruncatedReasonMaxPages}
	out.Pagination.Returned = 5000
	if got := nextPageHint(out, countEstimate{}); !strings.Contains(got, "--unbounded") {
		t.Errorf("capped hint = %q, want it to name --unbounded", got)
	}
}
