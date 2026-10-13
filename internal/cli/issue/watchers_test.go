package issue

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gechr/clog"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// The keyed mechanics — argv ordering under a concurrent fan-out,
// --parallelism, partial vs total failure, the severity ladder — are
// pinned once against `issue link list` (link_test.go), which shares
// this command's executor, its outcome/KeyedFailure split and its
// fan-out. Repeating them here would test command.KeyedFailure and
// x/sync.Parallel a second time rather than anything this command
// decides. What IS this command's own is below: the dedicated read (the
// one structural difference from its two keyed siblings), the
// wire-to-view narrowing, the count-beside-the-array contract, and the
// render — including the permission case only this command has.
//
// A fake of its own rather than fakeLinkClient, which the attachment
// tests reuse: those two commands both ride GetIssue, and this one is
// the first keyed read to call a different seam method at all.

// fakeWatchersClient is a core.JiraClient double whose GetWatchers
// answers from a per-key table: watchers carries the payload to return,
// failures the error to return instead. A key in neither is a
// test-authoring mistake and fails loudly rather than returning a
// zero-valued success, which would look exactly like an issue nobody
// watches.
type fakeWatchersClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	watchers map[string]*jira.Watchers
	failures map[string]error

	mu      sync.Mutex
	gotKeys []string
}

func (f *fakeWatchersClient) GetWatchers(_ context.Context, key string) (core.WatcherRead, error) {
	f.mu.Lock()
	f.gotKeys = append(f.gotKeys, key)
	f.mu.Unlock()

	if err, ok := f.failures[key]; ok {
		return core.WatcherRead{}, err
	}
	if w, ok := f.watchers[key]; ok {
		return core.WatcherRead{Watchers: w}, nil
	}
	return core.WatcherRead{}, errors.New("fakeWatchersClient: no fixture for " + key)
}

func watchersDeps(fake *fakeWatchersClient) Deps {
	return Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}
}

// wireWatcher is one fixture watcher, every published field populated so
// a narrowing that dropped one is visible.
func wireWatcher(id, name, accountType string, active bool) jira.UserDetails {
	return jira.UserDetails{
		AccountID:   jira.NullableOf(id),
		DisplayName: jira.NullableOf(name),
		AccountType: jira.NullableOf(accountType),
		Active:      jira.NullableOf(active),
	}
}

// wireWatchers builds the payload the fake serves for a key.
func wireWatchers(count int64, isWatching bool, watchers ...jira.UserDetails) *jira.Watchers {
	return &jira.Watchers{
		IsWatching: jira.NullableOf(isWatching),
		WatchCount: jira.NullableOf(count),
		Watchers:   jira.NullableOf(watchers),
		Self:       jira.NullableOf("https://example.atlassian.net/rest/api/3/issue/10500/watchers"),
	}
}

// TestRunWatchersListReadsTheWatcherEndpointOncePerKey pins the one
// structural way this command differs from its keyed siblings: link and
// attachment lists ride GetIssue because Jira publishes no list endpoint
// for either, while watchers have one — so this command must reach the
// watcher seam, exactly once per key, and never fall back to reading
// whole issues.
func TestRunWatchersListReadsTheWatcherEndpointOncePerKey(t *testing.T) {
	fake := &fakeWatchersClient{watchers: map[string]*jira.Watchers{
		"ABC-1": wireWatchers(0, false),
		"ABC-2": wireWatchers(0, false),
	}}

	if _, err := runWatchersList(t.Context(), watchersDeps(fake), []string{"ABC-1", "ABC-2"}, 1); err != nil {
		t.Fatalf("runWatchersList: %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.gotKeys) != 2 {
		t.Errorf("GetWatchers calls = %v, want exactly one per key", fake.gotKeys)
	}
}

// TestWatcherViewsNarrowEveryPublishedField pins the wire-to-view
// mapping field by field. A narrowing that silently dropped one (or
// crossed the two string fields over) would still produce a well-formed
// envelope and a plausible table.
func TestWatcherViewsNarrowEveryPublishedField(t *testing.T) {
	got := watcherViews([]jira.UserDetails{
		wireWatcher("5b10a2844c20165700ede21g", "Ada Watcher", "atlassian", true),
	})
	if len(got) != 1 {
		t.Fatalf("views = %d, want 1", len(got))
	}
	w := got[0]
	for _, c := range []struct{ field, got, want string }{
		{"account_id", w.AccountID, "5b10a2844c20165700ede21g"},
		{"display_name", w.DisplayName, "Ada Watcher"},
		{"account_type", w.AccountType, "atlassian"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
	if !w.Active {
		t.Error("active = false, want true — a dropped bool is invisible in every string check above")
	}
}

// TestWatcherViewsCarryDeactivatedAccounts pins that `active: false`
// survives the narrowing rather than being filtered out. A deactivated
// account keeps watching the issues it watched, and a list that quietly
// omitted them would answer "who will this notify" correctly while
// answering "why is this list so long" not at all — which is the reason
// WatcherView publishes the field.
func TestWatcherViewsCarryDeactivatedAccounts(t *testing.T) {
	got := watcherViews([]jira.UserDetails{
		wireWatcher("acct-1", "Ada Watcher", "atlassian", true),
		wireWatcher("acct-2", "Departed Colleague", "atlassian", false),
	})
	if len(got) != 2 {
		t.Fatalf("views = %d, want both watchers — a deactivated account is not filtered out", len(got))
	}
	if got[1].Active {
		t.Error("views[1].active = true, want false")
	}
}

// TestWatcherViewsAreNeverNil pins the empty-array contract: a caller
// iterating data.results[].data.watchers must not have to nil-check the
// common case of an issue nobody watches.
func TestWatcherViewsAreNeverNil(t *testing.T) {
	if got := watcherViews(nil); got == nil {
		t.Error("watcherViews(nil) = nil, want an empty array — the envelope publishes [] for an issue with no watchers")
	}
}

// TestRunWatchersListPublishesTheCountBesideTheArray pins the decision
// this command's payload makes and its two predecessors did not: Jira
// sends `watchCount` to every caller but the `watchers` array only to
// one holding "View voters and watchers", so an empty array with a
// non-zero count is a real response meaning "seven, and you may not see
// them". A payload publishing only the array could not express it.
func TestRunWatchersListPublishesTheCountBesideTheArray(t *testing.T) {
	fake := &fakeWatchersClient{watchers: map[string]*jira.Watchers{
		"ABC-1": wireWatchers(7, false),
	}}

	out, err := runWatchersList(t.Context(), watchersDeps(fake), []string{"ABC-1"}, 1)
	if err != nil {
		t.Fatalf("runWatchersList: %v", err)
	}
	if len(out.Results) != 1 || out.Results[0].Data == nil {
		t.Fatalf("results = %+v, want one successful entry", out.Results)
	}
	data := out.Results[0].Data
	if len(data.Watchers) != 0 {
		t.Errorf("watchers = %d, want none visible", len(data.Watchers))
	}
	if data.WatchCount != 7 {
		t.Errorf("watch_count = %d, want 7 — the count is what distinguishes a hidden list from an empty one", data.WatchCount)
	}
}

// TestRunWatchersListPublishesIsWatching pins the one field here a verb
// acts on: it is the state `issue watchers add` and `issue watchers
// remove` toggle for the authenticated account, so it must survive to
// the envelope rather than being inferable only by scanning the array
// for an account id the caller would first have to know.
func TestRunWatchersListPublishesIsWatching(t *testing.T) {
	fake := &fakeWatchersClient{watchers: map[string]*jira.Watchers{
		"ABC-1": wireWatchers(1, true, wireWatcher("acct-1", "Ada Watcher", "atlassian", true)),
	}}

	out, err := runWatchersList(t.Context(), watchersDeps(fake), []string{"ABC-1"}, 1)
	if err != nil {
		t.Fatalf("runWatchersList: %v", err)
	}
	if len(out.Results) != 1 || out.Results[0].Data == nil {
		t.Fatalf("results = %+v, want one successful entry", out.Results)
	}
	if !out.Results[0].Data.IsWatching {
		t.Error("is_watching = false, want true")
	}
}

// TestRunWatchersListOmitsUnresolvedIdentityMembers pins the one place a
// SUCCESSFUL result here differs from its siblings'. The watchers
// endpoint names no issue id and no issue self — its own `self` is the
// watcher list's URL, not the issue's — so IssueRef carries the key
// alone. Emitting either as an empty string would tell a caller the
// server had answered "" rather than not answered at all.
func TestRunWatchersListOmitsUnresolvedIdentityMembers(t *testing.T) {
	fake := &fakeWatchersClient{watchers: map[string]*jira.Watchers{
		"ABC-1": wireWatchers(0, false),
	}}

	out, err := runWatchersList(t.Context(), watchersDeps(fake), []string{"ABC-1"}, 1)
	if err != nil {
		t.Fatalf("runWatchersList: %v", err)
	}
	got := out.Results[0].Issue
	if got.Key != "ABC-1" {
		t.Errorf("issue.key = %q, want ABC-1", got.Key)
	}
	if got.ID != "" || got.Self != "" {
		t.Errorf("issue = %+v, want id and self absent — this endpoint resolves neither", got)
	}
}

// TestRunWatchersListFailsLoudlyOnNilPayload mirrors readAttachments'
// own guard: GetWatchers' contract is a non-nil Watchers on every
// nil-error return, and a zero-valued payload would read as an issue
// nobody watches rather than the broken response it is.
func TestRunWatchersListFailsLoudlyOnNilPayload(t *testing.T) {
	fake := &fakeWatchersClient{watchers: map[string]*jira.Watchers{"ABC-1": nil}}

	_, err := runWatchersList(t.Context(), watchersDeps(fake), []string{"ABC-1"}, 1)
	if err == nil {
		t.Fatal("runWatchersList succeeded on a nil payload — an empty result would have been reported as success")
	}
	if !strings.Contains(err.Error(), "ABC-1") {
		t.Errorf("error = %v, want it to name the key", err)
	}
}

// TestRunWatchersListKeepsSuccessesWhenOneKeyFails pins that this
// command is wired into the shared partial-result rule, rather than
// merely that the rule works — command.KeyedFailure's own behavior is
// link_test.go's to prove. One bad key must not cost the caller the good
// ones.
func TestRunWatchersListKeepsSuccessesWhenOneKeyFails(t *testing.T) {
	fake := &fakeWatchersClient{
		watchers: map[string]*jira.Watchers{
			"ABC-1": wireWatchers(1, false, wireWatcher("acct-1", "Ada Watcher", "atlassian", true)),
		},
		failures: map[string]error{
			"ABC-404": &adapterjira.APIError{StatusCode: 404, Messages: []string{"Issue does not exist"}},
		},
	}

	out, err := runWatchersList(t.Context(), watchersDeps(fake), []string{"ABC-1", "ABC-404"}, 2)
	if err != nil {
		t.Fatalf("runWatchersList returned an error for a partial result: %v", err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}
	if first := out.Results[0]; !first.OK || first.Data == nil || len(first.Data.Watchers) != 1 {
		t.Fatalf("results[0] = %+v, want the successful key with its one watcher", first)
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

// renderWatchers runs renderWatchersList and returns what it wrote.
func renderWatchers(t *testing.T, out envelope.IssueWatchersListOutput, mode clog.ColorMode) string {
	t.Helper()
	withStyle(t, mode == clog.ColorAlways)

	var buf bytes.Buffer
	v := output.NewView(&buf, mode)
	if err := renderWatchersList(v, envelope.Envelope{OK: true, Data: out}); err != nil {
		t.Fatalf("renderWatchersList: %v", err)
	}
	return buf.String()
}

// watchersResult is a small builder for a successful keyed result.
func watchersResult(key string, count int64, watchers ...envelope.WatcherView) envelope.KeyedResult[envelope.IssueWatchersListData] {
	data := envelope.IssueWatchersListData{Watchers: watchers, WatchCount: count}
	return envelope.KeyedResult[envelope.IssueWatchersListData]{
		Issue: envelope.IssueRef{Key: key},
		OK:    true,
		Data:  &data,
	}
}

func TestRenderWatchersListDrawsOneSectionPerKey(t *testing.T) {
	out := envelope.IssueWatchersListOutput{}
	out.Results = []envelope.KeyedResult[envelope.IssueWatchersListData]{
		watchersResult("ABC-1", 1, envelope.WatcherView{
			AccountID: "acct-1", DisplayName: "Ada Watcher",
			AccountType: "atlassian", Active: true,
		}),
		watchersResult("ABC-2", 0),
	}

	got := renderWatchers(t, out, clog.ColorNever)
	for _, want := range []string{"ABC-1", "ABC-2", "Ada Watcher", "acct-1", "atlassian", "No watchers."} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want it to contain %q", got, want)
		}
	}
	// The empty key's own line belongs to THAT key's section, so the
	// order matters: a "No watchers." printed before ABC-2's header would
	// attribute it to the wrong issue.
	if strings.Index(got, "ABC-2") > strings.Index(got, "No watchers.") {
		t.Errorf("output = %q, want the empty key's header before its own no-watchers line", got)
	}
}

// TestRenderWatchersListDistinguishesHiddenFromEmpty is the render half
// of the count-beside-the-array decision, and the case only this
// command has. Reporting "No watchers." to somebody whose permissions
// hid seven of them would be the CLI stating a falsehood it held the
// evidence to avoid — the count came back in the same response.
func TestRenderWatchersListDistinguishesHiddenFromEmpty(t *testing.T) {
	out := envelope.IssueWatchersListOutput{}
	out.Results = []envelope.KeyedResult[envelope.IssueWatchersListData]{
		watchersResult("ABC-1", 7),
	}

	got := renderWatchers(t, out, clog.ColorNever)
	if strings.Contains(got, "No watchers.") {
		t.Errorf("output = %q, want no empty-list claim — seven people are watching, they are merely not visible", got)
	}
	for _, want := range []string{"7", "cannot see who", "View voters and watchers"} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want it to contain %q", got, want)
		}
	}
}

// TestRenderWatchersListShowsDeactivatedAccounts pins the ACTIVE column
// rendering as a scannable word. A bool formatted straight through would
// read "%!s(bool=false)" or vanish entirely, and the envelope's own
// field would still be correct — so no schema check would catch it.
func TestRenderWatchersListShowsDeactivatedAccounts(t *testing.T) {
	out := envelope.IssueWatchersListOutput{}
	out.Results = []envelope.KeyedResult[envelope.IssueWatchersListData]{
		watchersResult("ABC-1",
			2,
			envelope.WatcherView{AccountID: "acct-1", DisplayName: "Ada Watcher", AccountType: "atlassian", Active: true},
			envelope.WatcherView{AccountID: "acct-2", DisplayName: "Departed Colleague", AccountType: "atlassian"},
		),
	}

	got := renderWatchers(t, out, clog.ColorNever)
	for _, want := range []string{"ACTIVE", "yes", "no", "Departed Colleague"} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want it to contain %q", got, want)
		}
	}
}

// TestRenderWatchersListShowsAFailedKeyInPlace pins the half of the
// partial-result contract a person sees: the command succeeded, so
// nothing else would ever tell them one of their keys did not answer.
func TestRenderWatchersListShowsAFailedKeyInPlace(t *testing.T) {
	out := envelope.IssueWatchersListOutput{}
	out.Results = []envelope.KeyedResult[envelope.IssueWatchersListData]{
		watchersResult("ABC-1", 0),
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

	got := renderWatchers(t, out, clog.ColorNever)
	for _, want := range []string{"ABC-404", "was not found", "Check the identifier."} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want it to contain %q", got, want)
		}
	}
}

// TestRenderWatchersListRejectsAForeignPayload pins the Human closure's
// type guard: the renderer is reached through an interface, and a
// mis-registered command would otherwise panic on the assertion rather
// than returning the error the contract expects.
func TestRenderWatchersListRejectsAForeignPayload(t *testing.T) {
	var buf bytes.Buffer
	v := output.NewView(&buf, clog.ColorNever)
	err := renderWatchersList(v, envelope.Envelope{OK: true, Data: envelope.IssueLinkListOutput{}})
	if err == nil {
		t.Fatal("renderWatchersList accepted a foreign payload")
	}
	if !strings.Contains(err.Error(), "IssueWatchersListOutput") {
		t.Errorf("error = %v, want it to name the expected type", err)
	}
}

// TestWatchersSpinnerLabelCountsRatherThanNames pins the label rule a
// fan-out spinner depends on: one animation covers every key, so its
// text must not imply it is tracking a particular one.
func TestWatchersSpinnerLabelCountsRatherThanNames(t *testing.T) {
	if got := watchersSpinnerLabel([]string{"ABC-1"}); !strings.Contains(got, "ABC-1") {
		t.Errorf("single-key label = %q, want it to name the key", got)
	}
	got := watchersSpinnerLabel([]string{"ABC-1", "ABC-2", "ABC-3"})
	if !strings.Contains(got, "3") {
		t.Errorf("multi-key label = %q, want it to count the keys", got)
	}
	if strings.Contains(got, "ABC-1") {
		t.Errorf("multi-key label = %q, want no single key named — the label is fixed while the reads are not", got)
	}
}
