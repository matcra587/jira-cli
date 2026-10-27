package issue

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gechr/clog"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// fakeLinkClient is a core.JiraClient double whose GetIssue answers from
// a per-key table: issues carries the bean to return, failures the error
// to return instead. A key in neither is a test-authoring mistake and
// fails loudly rather than returning a zero-valued success, which would
// look exactly like an issue with no links.
//
// It records every key it was asked for, and — for the parallelism
// tests — how many calls were in flight at once. Both are touched from
// several goroutines at a time, so both are guarded.
type fakeLinkClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	issues   map[string]*jira.IssueBean
	failures map[string]error
	baseURL  string
	// block, when non-nil, is waited on inside every call, so a test can
	// hold N calls open at once and observe the concurrency width.
	block func()

	mu       sync.Mutex
	gotKeys  []string
	gotField [][]string

	inFlight, peakInFlight atomic.Int64
}

func (f *fakeLinkClient) BaseURL() string { return f.baseURL }

func (f *fakeLinkClient) GetIssue(_ context.Context, key string, fields []string, _ ...string) (core.IssueRead, error) {
	now := f.inFlight.Add(1)
	for {
		peak := f.peakInFlight.Load()
		if now <= peak || f.peakInFlight.CompareAndSwap(peak, now) {
			break
		}
	}
	defer f.inFlight.Add(-1)

	f.mu.Lock()
	f.gotKeys = append(f.gotKeys, key)
	f.gotField = append(f.gotField, fields)
	f.mu.Unlock()

	if f.block != nil {
		f.block()
	}
	if err, ok := f.failures[key]; ok {
		return core.IssueRead{}, err
	}
	if bean, ok := f.issues[key]; ok {
		return core.IssueRead{Issue: bean}, nil
	}
	return core.IssueRead{}, errors.New("fakeLinkClient: no fixture for " + key)
}

func (f *fakeLinkClient) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.gotKeys...)
}

// linkedIssueBean builds one issue at the far end of a link.
func linkedIssueBean(key, summary, status, category, issueType, priority string) jira.LinkedIssue {
	return jira.LinkedIssue{
		Key: jira.NullableOf(key),
		ID:  jira.NullableOf("2" + key),
		Fields: jira.NullableOf(jira.Fields{
			Summary: jira.NullableOf(summary),
			Status: jira.NullableOf(jira.StatusDetails{
				Name: jira.NullableOf(status),
				StatusCategory: jira.NullableOf(jira.StatusCategory{
					Key: jira.NullableOf(category),
				}),
			}),
			Issuetype: jira.NullableOf(jira.IssueTypeDetails{Name: jira.NullableOf(issueType)}),
			Priority:  jira.NullableOf(jira.Priority{Name: jira.NullableOf(priority)}),
		}),
	}
}

// blockingLinkType is the fixture link type every test below links with:
// two distinct directional phrasings, so a test can tell which one the
// view picked.
func blockingLinkType() jira.Nullable[jira.IssueLinkType] {
	return jira.NullableOf(jira.IssueLinkType{
		ID:      jira.NullableOf("10001"),
		Name:    jira.NullableOf("Blocks"),
		Inward:  jira.NullableOf("is blocked by"),
		Outward: jira.NullableOf("blocks"),
	})
}

// issueWithLinks builds the issue bean the fake serves for key.
func issueWithLinks(key string, links ...jira.IssueLink) *jira.IssueBean {
	return &jira.IssueBean{
		ID:   jira.NullableOf("1" + key),
		Key:  jira.NullableOf(key),
		Self: jira.NullableOf("https://example.atlassian.net/rest/api/3/issue/1" + key),
		Fields: jira.NullableOf(jira.IssueFields{
			Issuelinks: jira.NullableOf(links),
		}),
	}
}

func linkDeps(fake *fakeLinkClient) Deps {
	return Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}
}

// TestRunLinkListNarrowsTheReadToLinks pins that a link read asks Jira
// for the one field it needs. A link list that requested the server's
// default field set would pull every issue whole — description, every
// custom field — once per key.
func TestRunLinkListNarrowsTheReadToLinks(t *testing.T) {
	fake := &fakeLinkClient{issues: map[string]*jira.IssueBean{"ABC-1": issueWithLinks("ABC-1")}}

	if _, _, err := runLinkList(t.Context(), linkDeps(fake), []string{"ABC-1"}, 1); err != nil {
		t.Fatalf("runLinkList: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.gotField) != 1 || len(fake.gotField[0]) != 1 || fake.gotField[0][0] != "issuelinks" {
		t.Errorf("requested fields = %v, want exactly [issuelinks]", fake.gotField)
	}
}

// TestRunLinkListOrdersResultsByArgvNotCompletion is the keyed
// contract's central property, and the one a concurrent fan-out can
// silently break: results[i] must answer for the i-th key given, however
// the reads interleaved. The fixture makes the LAST key the fastest by
// making every earlier one wait, so an implementation that appended on
// completion would come back reversed.
func TestRunLinkListOrdersResultsByArgvNotCompletion(t *testing.T) {
	keys := []string{"ABC-1", "ABC-2", "ABC-3", "ABC-4"}
	issues := map[string]*jira.IssueBean{}
	for _, k := range keys {
		issues[k] = issueWithLinks(k)
	}
	// Every call waits until the last one has started, so completion
	// order cannot match argv order by luck.
	var started sync.WaitGroup
	started.Add(len(keys))
	fake := &fakeLinkClient{
		issues: issues,
		block: func() {
			started.Done()
			started.Wait()
		},
	}

	out, _, err := runLinkList(t.Context(), linkDeps(fake), keys, len(keys))
	if err != nil {
		t.Fatalf("runLinkList: %v", err)
	}
	if len(out.Results) != len(keys) {
		t.Fatalf("results = %d, want one per key (%d)", len(out.Results), len(keys))
	}
	for i, key := range keys {
		if got := out.Results[i].Issue.Key; got != key {
			t.Errorf("results[%d].issue.key = %q, want %q — results must be ordered by argv", i, got, key)
		}
	}
}

// TestRunLinkListHonorsParallelism proves --parallelism is wired to a
// real executor rather than registered and ignored: with a width of two
// and four keys that all block until released, at most two calls are
// ever in flight.
func TestRunLinkListHonorsParallelism(t *testing.T) {
	keys := []string{"ABC-1", "ABC-2", "ABC-3", "ABC-4"}
	issues := map[string]*jira.IssueBean{}
	for _, k := range keys {
		issues[k] = issueWithLinks(k)
	}
	const width = 2
	// Every call parks until the first `width` of them are in flight at
	// once, then all calls run free. That makes the peak deterministic
	// rather than a race against the scheduler — the fan-out cannot get
	// past the barrier without genuinely holding `width` calls open —
	// and it cannot deadlock: releasing is one-way, so the calls that
	// arrive after the barrier opens never park at all.
	var (
		fake    *fakeLinkClient
		once    sync.Once
		release = make(chan struct{})
	)
	fake = &fakeLinkClient{
		issues: issues,
		block: func() {
			if fake.inFlight.Load() >= width {
				once.Do(func() { close(release) })
			}
			<-release
		},
	}

	if _, _, err := runLinkList(t.Context(), linkDeps(fake), keys, width); err != nil {
		t.Fatalf("runLinkList: %v", err)
	}
	if peak := fake.peakInFlight.Load(); peak > width {
		t.Errorf("peak concurrent reads = %d, want at most --parallelism (%d)", peak, width)
	}
	if peak := fake.peakInFlight.Load(); peak < width {
		t.Errorf("peak concurrent reads = %d, want %d — the fan-out ran narrower than asked", peak, width)
	}
}

// TestRunLinkListParallelismOneIsSequential is the other half: a caller
// who asks for one at a time gets one at a time. `issue rank` will opt
// out of the flag entirely for a correctness reason, and this is the
// property that makes that opt-out mean something.
func TestRunLinkListParallelismOneIsSequential(t *testing.T) {
	keys := []string{"ABC-1", "ABC-2", "ABC-3"}
	issues := map[string]*jira.IssueBean{}
	for _, k := range keys {
		issues[k] = issueWithLinks(k)
	}
	fake := &fakeLinkClient{issues: issues}

	if _, _, err := runLinkList(t.Context(), linkDeps(fake), keys, 1); err != nil {
		t.Fatalf("runLinkList: %v", err)
	}
	if peak := fake.peakInFlight.Load(); peak != 1 {
		t.Errorf("peak concurrent reads = %d, want 1", peak)
	}
	if got := fake.keys(); strings.Join(got, ",") != strings.Join(keys, ",") {
		t.Errorf("read order = %v, want %v", got, keys)
	}
}

// TestRunLinkListKeepsSuccessesWhenOneKeyFails is the partial-result
// rule: one bad key must not cost the caller the good ones. The command
// succeeds, and the failed key's own entry says what happened.
func TestRunLinkListKeepsSuccessesWhenOneKeyFails(t *testing.T) {
	fake := &fakeLinkClient{
		issues: map[string]*jira.IssueBean{
			"ABC-1": issueWithLinks("ABC-1", jira.IssueLink{
				ID:          jira.NullableOf("10100"),
				Type:        blockingLinkType(),
				InwardIssue: jira.NullableOf(linkedIssueBean("ABC-9", "Blocker", "In Progress", "indeterminate", "Bug", "High")),
			}),
		},
		failures: map[string]error{"ABC-404": &adapterjira.APIError{StatusCode: 404, Messages: []string{"Issue does not exist"}}},
	}

	out, _, err := runLinkList(t.Context(), linkDeps(fake), []string{"ABC-1", "ABC-404"}, 2)
	if err != nil {
		t.Fatalf("runLinkList returned an error for a partial result: %v", err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}

	first := out.Results[0]
	if !first.OK || first.Data == nil || len(first.Data.Links.Value) != 1 {
		t.Fatalf("results[0] = %+v, want the successful key with its one link", first)
	}
	if first.Err != nil {
		t.Errorf("results[0].error = %+v, want none on a successful key", first.Err)
	}

	second := out.Results[1]
	switch {
	case second.OK:
		t.Fatal("results[1].ok is true for a key that failed")
	case second.Data != nil:
		t.Error("results[1] carries data for a key that failed")
	case second.Err == nil:
		t.Fatal("results[1] failed but says nothing about why")
	}
	if second.Err.Code != "jira_not_found" {
		t.Errorf("results[1].error.code = %q, want jira_not_found", second.Err.Code)
	}
	if second.Err.Hint == "" {
		t.Error("results[1].error.hint is empty — every taxonomy code carries one")
	}
	if second.Issue.Key != "ABC-404" {
		t.Errorf("results[1].issue.key = %q, want the key that failed", second.Issue.Key)
	}
	if second.Issue.ID != "" || second.Issue.Self != "" {
		t.Errorf("results[1].issue = %+v, want key only — a key that could not be read resolved nothing else", second.Issue)
	}
}

// TestRunLinkListFailsWhenEveryKeyFails is the other side of the same
// rule: with no successful key there is no partial result to hand back,
// so the command fails like any single-key read rather than reporting ok
// with nothing in it.
func TestRunLinkListFailsWhenEveryKeyFails(t *testing.T) {
	notFound := &adapterjira.APIError{StatusCode: 404, Messages: []string{"Issue does not exist"}}
	fake := &fakeLinkClient{failures: map[string]error{"ABC-1": notFound, "ABC-2": notFound}}

	_, _, err := runLinkList(t.Context(), linkDeps(fake), []string{"ABC-1", "ABC-2"}, 2)
	if err == nil {
		t.Fatal("runLinkList succeeded with no successful key — an ok envelope carrying only failures reads as done")
	}
	// The ORIGINAL error, not a rebuild: everything the taxonomy reads
	// off a typed error (its class, its retry facts, the field it names)
	// survives only if the error itself is what comes back.
	var apiErr *adapterjira.APIError
	if !errors.As(err, &apiErr) {
		t.Errorf("error = %T (%v), want the original typed API error", err, err)
	}
}

// TestKeyedFailureReportsTheMostSevereClass pins the ladder: a run that
// hit both a missing issue and a bad credential exits on the credential,
// because that is the one the caller has to fix first — not on whichever
// key happened to come first in argv.
func TestKeyedFailureReportsTheMostSevereClass(t *testing.T) {
	notFound := &adapterjira.APIError{StatusCode: 404, Messages: []string{"Issue does not exist"}}
	unauthorized := &adapterjira.APIError{StatusCode: 401, Messages: []string{"Unauthorized"}}
	fake := &fakeLinkClient{failures: map[string]error{"ABC-1": notFound, "ABC-2": unauthorized}}

	_, _, err := runLinkList(t.Context(), linkDeps(fake), []string{"ABC-1", "ABC-2"}, 2)
	if err == nil {
		t.Fatal("runLinkList succeeded with no successful key")
	}
	if !errors.Is(err, error(unauthorized)) {
		t.Errorf("error = %v, want the 401 — an auth failure outranks a missing issue whatever order they arrived in", err)
	}
}

// TestRunLinkListFailsLoudlyOnNilIssue mirrors runView's and
// runCommentList's own guards: GetIssue's contract is a non-nil Issue on
// every nil-error return, and a zero-valued payload would read as an
// issue with no links rather than the broken response it is.
func TestRunLinkListFailsLoudlyOnNilIssue(t *testing.T) {
	fake := &fakeLinkClient{issues: map[string]*jira.IssueBean{"ABC-1": nil}}

	_, _, err := runLinkList(t.Context(), linkDeps(fake), []string{"ABC-1"}, 1)
	if err == nil {
		t.Fatal("runLinkList succeeded on a nil Issue — an empty result would have been reported as success")
	}
	if !strings.Contains(err.Error(), "ABC-1") {
		t.Errorf("error = %v, want it to name the key", err)
	}
}

// TestLinkViewsReadEachDirectionFromTheAskedAboutIssue pins the
// narrowing this command exists for: the wire says inwardIssue/
// outwardIssue, and the view says which way the link reads FROM the
// issue that carried it.
func TestLinkViewsReadEachDirectionFromTheAskedAboutIssue(t *testing.T) {
	links := []jira.IssueLink{
		{
			ID:          jira.NullableOf("10100"),
			Type:        blockingLinkType(),
			InwardIssue: jira.NullableOf(linkedIssueBean("ABC-9", "Blocker", "In Progress", "indeterminate", "Bug", "High")),
		},
		{
			ID:           jira.NullableOf("10101"),
			Type:         blockingLinkType(),
			OutwardIssue: jira.NullableOf(linkedIssueBean("ABC-8", "Blocked", "To Do", "new", "Task", "Low")),
		},
	}

	got := linkViews(links)
	if len(got) != 2 {
		t.Fatalf("links = %d, want 2", len(got))
	}
	inward, outward := got[0], got[1]
	if inward.Direction != directionInward || inward.Relation != "is blocked by" {
		t.Errorf("inward link = {%q, %q}, want {inward, is blocked by}", inward.Direction, inward.Relation)
	}
	if inward.Issue.Key != "ABC-9" {
		t.Errorf("inward link's issue = %q, want the far end ABC-9", inward.Issue.Key)
	}
	if outward.Direction != directionOutward || outward.Relation != "blocks" {
		t.Errorf("outward link = {%q, %q}, want {outward, blocks}", outward.Direction, outward.Relation)
	}
	if outward.Issue.Key != "ABC-8" {
		t.Errorf("outward link's issue = %q, want the far end ABC-8", outward.Issue.Key)
	}
	// Both phrasings survive on the type itself, for a caller that wants
	// the type rather than this link's reading of it.
	if inward.Type.Inward != "is blocked by" || inward.Type.Outward != "blocks" {
		t.Errorf("link type = %+v, want both phrasings", inward.Type)
	}
	// The far issue's own fields, narrowed and CLI-shaped.
	if inward.Issue.Fields.Status.Category != "indeterminate" {
		t.Errorf("status category = %q, want the wire's statusCategory.key", inward.Issue.Fields.Status.Category)
	}
	if inward.Issue.Fields.Priority.Name != "High" {
		t.Errorf("priority = %q, want High", inward.Issue.Fields.Priority.Name)
	}
}

// TestLinkViewsAreNeverNil pins the empty-array contract: a caller
// iterating data.results[].data.links must not have to nil-check the
// common case.
func TestLinkViewsAreNeverNil(t *testing.T) {
	if got := linkViews(nil); got == nil {
		t.Error("linkViews(nil) = nil, want an empty array — the envelope publishes [] for an issue with no links")
	}
}

// renderLinks runs renderLinkList and returns what it wrote.
func renderLinks(t *testing.T, out envelope.IssueLinkListOutput, mode clog.ColorMode) string {
	t.Helper()
	withStyle(t, mode == clog.ColorAlways)

	var buf bytes.Buffer
	v := output.NewView(&buf, mode)
	if err := renderLinkList(v, envelope.Envelope{OK: true, Data: out}, ""); err != nil {
		t.Fatalf("renderLinkList: %v", err)
	}
	return buf.String()
}

// linkResult is a small builder for a successful keyed result.
func linkResult(key string, links ...envelope.IssueLinkView) envelope.KeyedResult[envelope.IssueLinkListData] {
	data := envelope.IssueLinkListData{Links: jira.NullableOf(append([]envelope.IssueLinkView{}, links...))}
	return envelope.KeyedResult[envelope.IssueLinkListData]{
		Issue: envelope.IssueRef{Key: key},
		OK:    true,
		Data:  &data,
	}
}

func TestRenderLinkListDrawsOneSectionPerKey(t *testing.T) {
	out := envelope.IssueLinkListOutput{}
	out.Results = []envelope.KeyedResult[envelope.IssueLinkListData]{
		linkResult("ABC-1", envelope.IssueLinkView{
			ID: "1", Direction: directionInward, Relation: "is blocked by",
			Issue: envelope.LinkedIssueView{
				IssueRef: envelope.IssueRef{Key: "ABC-9"},
				Fields: envelope.LinkedIssueFields{
					Summary: "The blocker",
					Status:  envelope.LinkedIssueStatus{Name: "In Progress", Category: "indeterminate"},
					Type:    envelope.LinkedIssueType{Name: "Bug"},
				},
			},
		}),
		linkResult("ABC-2"),
	}

	got := renderLinks(t, out, clog.ColorNever)
	for _, want := range []string{"ABC-1", "ABC-2", "ABC-9", "is blocked by", "The blocker", "In Progress", "No links."} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want it to contain %q", got, want)
		}
	}
	// The empty key's own line belongs to THAT key's section, so the
	// order matters: a "No links." printed before ABC-2's header would
	// attribute it to the wrong issue.
	if strings.Index(got, "ABC-2") > strings.Index(got, "No links.") {
		t.Errorf("output = %q, want the empty key's header before its own no-links line", got)
	}
}

// TestRenderLinkListShowsAFailedKeyInPlace pins the half of the
// partial-result contract a person sees: the command succeeded, so
// nothing else would ever tell them one of their keys did not answer.
func TestRenderLinkListShowsAFailedKeyInPlace(t *testing.T) {
	out := envelope.IssueLinkListOutput{}
	out.Results = []envelope.KeyedResult[envelope.IssueLinkListData]{
		linkResult("ABC-1"),
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

	got := renderLinks(t, out, clog.ColorNever)
	for _, want := range []string{"ABC-404", "was not found", "Check the identifier."} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want it to contain %q", got, want)
		}
	}
}
