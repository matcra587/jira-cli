package issue

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

// What a bulk selector has to get right, and where each half is pinned.
//
// The ORDERING is here, on `issue delete`, because delete is the command
// where getting it wrong is unrecoverable and because the ordering is a
// property of a run rather than of a function — only a whole run can
// show that the confirmation came after the search. The flag-level
// refusals (a selector beside keys, two selectors, the cap) are pure and
// live in internal/cli/command/selector_test.go.

// fakeSelectorClient records the ORDER of everything a run does, which is
// the whole point: no assertion in this file is about a call happening,
// only about when it happened relative to the others.
type fakeSelectorClient struct {
	unusedJiraClient

	// keys is what SearchIssues resolves to; searchErr fails it instead.
	keys      []string
	searchErr error
	// filterJQL is what GetFilter answers with.
	filterJQL string

	mu sync.Mutex
	// events is the run's timeline, appended to by every seam and by the
	// confirmation.
	events []string
	// lastJQL is the query SearchIssues was asked for.
	lastJQL string
	// searchMax is the page size requested for this complete one-page fixture.
	searchMax int
}

func (f *fakeSelectorClient) record(event string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}

func (f *fakeSelectorClient) timeline() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

func (f *fakeSelectorClient) SearchIssues(_ context.Context, jql string, _ []string, _ string, maxResults int, _ ...string) (core.IssueSearchRead, error) {
	f.mu.Lock()
	f.lastJQL = jql
	f.searchMax = maxResults
	f.events = append(f.events, "search")
	f.mu.Unlock()
	if f.searchErr != nil {
		return core.IssueSearchRead{}, f.searchErr
	}
	issues := make([]jira.IssueBean, 0, len(f.keys))
	for _, k := range f.keys {
		issues = append(issues, jira.IssueBean{Key: jira.NullableOf(k)})
	}
	return core.IssueSearchRead{Results: &jira.SearchResults{Issues: jira.NullableOf(issues), IsLast: jira.NullableOf(true)}}, nil
}

func (f *fakeSelectorClient) GetFilter(_ context.Context, id string) (core.FilterRead, error) {
	f.record("filter " + id)
	return core.FilterRead{Filter: &jira.Filter{Jql: jira.NullableOf(f.filterJQL)}}, nil
}

func (f *fakeSelectorClient) DeleteIssue(_ context.Context, key string, _ bool) (core.IssueDeleteWrite, error) {
	f.record("delete " + key)
	return core.IssueDeleteWrite{StatusCode: http.StatusNoContent}, nil
}

// selectorDeps wires the fake as both the client and the dialog, so the
// confirmation lands on the SAME timeline as the requests.
func selectorDeps(fake *fakeSelectorClient, answer bool, seen *prompt.Confirm) Deps {
	return Deps{
		JiraClient: func() (core.JiraClient, error) { return fake, nil },
		Confirm: func(_ context.Context, c prompt.Confirm) (bool, error) {
			fake.record("confirm")
			if seen != nil {
				*seen = c
			}
			return answer, nil
		},
	}
}

// TestConfirmationCannotBeReachedBeforeTheSelectorResolves is the guard
// with teeth, and it is the reason this file exists.
//
// Design §3.5 rule 1: the selector RESOLVES BEFORE the confirmation,
// never after. A confirmation reachable before resolution is one of two
// things, both wrong — a dialog echoing the query back at somebody who
// has already read it and got it wrong, or a dialog naming an empty list
// while the fan-out deletes a hundred issues behind it.
//
// It is pinned as an ORDER over one timeline rather than as "confirm saw
// the right text", because the text can be made to look right by a
// second resolution the run does not use. The search must have HAPPENED
// first.
func TestConfirmationCannotBeReachedBeforeTheSelectorResolves(t *testing.T) {
	fake := &fakeSelectorClient{keys: []string{"KEY-7", "KEY-8"}}
	var asked prompt.Confirm
	deps := selectorDeps(fake, true, &asked)

	inv := interactiveInvocation(false)
	_, err := runDelete(t.Context(), deps, inv, deleteOptions{
		selector:    command.SelectorFlags{JQL: "project = KEY AND status = Done"},
		parallelism: 1,
	})
	if err != nil {
		t.Fatalf("runDelete: %v", err)
	}

	got := fake.timeline()
	want := []string{"search", "confirm", "delete KEY-7", "delete KEY-8"}
	if len(got) != len(want) {
		t.Fatalf("timeline = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("timeline = %v, want %v — the confirmation must come after the search and before every delete", got, want)
		}
	}

	// And the consequence the order exists for: the question names the
	// issues, not the query.
	question := asked.Question
	for _, key := range []string{"KEY-7", "KEY-8"} {
		if !strings.Contains(question, key) {
			t.Errorf("question %q does not name %s; a prompt that cannot list what it will delete is not worth drawing", question, key)
		}
	}
	if strings.Contains(question, "status = Done") {
		t.Errorf("question %q echoes the JQL back; the caller has already read their own query, and the keys are what they cannot derive from it", question)
	}
}

// TestForceSkipsThePromptButNeverTheEnumeration is the other half of
// rule 1. --force answers the question up front, as it does everywhere
// else in the tree; it must NOT turn the run into one that acts on
// issues it never resolved or never published, because a scripted caller
// needs the record of what was affected more than an interactive one
// does.
func TestForceSkipsThePromptButNeverTheEnumeration(t *testing.T) {
	fake := &fakeSelectorClient{keys: []string{"KEY-7", "KEY-8"}}
	deps := selectorDeps(fake, false, nil) // answering false would abort, if it were asked

	out, err := runDelete(t.Context(), deps, invocation(false), deleteOptions{
		selector:    command.SelectorFlags{JQL: "project = KEY"},
		force:       true,
		parallelism: 1,
	})
	if err != nil {
		t.Fatalf("runDelete with --force: %v", err)
	}

	for _, event := range fake.timeline() {
		if event == "confirm" {
			t.Error("--force still opened the dialog")
		}
	}
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2 — --force skips the prompt, never the enumeration", len(out.Results))
	}
	if out.Selector == nil || !out.Selector.Resolved {
		t.Fatal("--force run published no resolved selector; the query that chose these issues is the only record of why they went")
	}
	if out.Selector.Query != "project = KEY" {
		t.Errorf("selector query = %q, want the query as typed", out.Selector.Query)
	}
}

// TestBareDryRunDoesNotResolveAQuery is design §3.5 rule 3, and the
// fourth application of the §8 item 0 rule that governs @me: resolving a
// selector is a live read, --dry-run is local-only, so the preview
// reports the selector unresolved rather than inventing a key list.
//
// The structural half is the one that matters — it must not obtain a
// client at all, because a preview that cannot get one cannot reach the
// network whatever it does next.
func TestBareDryRunDoesNotResolveAQuery(t *testing.T) {
	built := false
	deps := Deps{JiraClient: func() (core.JiraClient, error) {
		built = true
		return nil, errors.New("a preview must not build a client")
	}}

	out, err := runDelete(t.Context(), deps, invocation(true), deleteOptions{
		selector:    command.SelectorFlags{JQL: "project = KEY"},
		parallelism: 1,
	})
	if err != nil {
		t.Fatalf("runDelete --dry-run: %v", err)
	}
	if built {
		t.Error("a bare --dry-run built a client; --dry-run is local-only and must never contact Jira")
	}
	if len(out.Results) != 0 {
		t.Errorf("results = %d, want 0 — a preview that did not resolve cannot list issues", len(out.Results))
	}
	if out.Selector == nil {
		t.Fatal("no selector published; an empty results[] with no explanation reads as a run with no targets")
	}
	if out.Selector.Resolved {
		t.Error("selector reported resolved on a bare preview")
	}
}

// TestValidateRemoteResolvesAPreviewWithoutWriting is rule 3's other
// half. --validate-remote is the opt-in that lets a preview do the read,
// and it is the SAME flag `issue transition` already spends on its
// screen-state read — reused rather than a third pattern invented here.
func TestValidateRemoteResolvesAPreviewWithoutWriting(t *testing.T) {
	fake := &fakeSelectorClient{keys: []string{"KEY-7", "KEY-8"}}
	deps := selectorDeps(fake, true, nil)

	out, err := runDelete(t.Context(), deps, invocation(true), deleteOptions{
		selector:       command.SelectorFlags{JQL: "project = KEY"},
		validateRemote: true,
		parallelism:    1,
	})
	if err != nil {
		t.Fatalf("runDelete --dry-run --validate-remote: %v", err)
	}
	if len(out.Results) != 2 {
		t.Errorf("results = %d, want 2 — the point of this flag is that the preview lists them", len(out.Results))
	}
	if out.Selector == nil || !out.Selector.Resolved {
		t.Error("selector reported unresolved despite --validate-remote")
	}
	for _, event := range fake.timeline() {
		if strings.HasPrefix(event, "delete ") {
			t.Fatalf("a preview deleted %q", event)
		}
		if event == "confirm" {
			t.Error("a preview asked for confirmation; it destroys nothing, so a dialog would be theater")
		}
	}
}

// TestFromFileResolvesInABareDryRun is where rule 3 deliberately does
// NOT bite, and the reasoning is worth pinning because the rule's
// wording ("bare --dry-run cannot enumerate") reads as if it should.
//
// The rule is a CONSEQUENCE of resolution being a live read. A file is
// answered by the filesystem, so a preview can enumerate it truthfully
// and withholding the list would be a restriction with no honest reason
// behind it — the preview would be hiding something it knows.
func TestFromFileResolvesInABareDryRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(path, []byte("KEY-1\n\n# a note\nKEY-2 # trailing\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	built := false
	deps := Deps{JiraClient: func() (core.JiraClient, error) {
		built = true
		return nil, errors.New("a preview must not build a client")
	}}

	out, err := runDelete(t.Context(), deps, invocation(true), deleteOptions{
		selector:    command.SelectorFlags{FromFile: path},
		parallelism: 1,
	})
	if err != nil {
		t.Fatalf("runDelete --from-file --dry-run: %v", err)
	}
	if built {
		t.Error("--from-file built a client; resolving a file needs no site")
	}
	if out.Selector == nil || !out.Selector.Resolved {
		t.Fatal("--from-file reported unresolved in a preview; a local file is knowable without the site")
	}
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2 (blank lines and comments dropped)", len(out.Results))
	}
	for i, want := range []string{"KEY-1", "KEY-2"} {
		if got := out.Results[i].Issue.Key; got != want {
			t.Errorf("results[%d].issue.key = %q, want %q", i, got, want)
		}
	}
}

// TestFilterResolvesToItsQueryAndPublishesIt pins the one fact a
// --filter run cannot otherwise recover. A saved filter is editable by
// anyone it is shared with, so the query behind an id is neither visible
// in the invocation nor stable over time — publishing it is what makes
// the run auditable afterwards.
func TestFilterResolvesToItsQueryAndPublishesIt(t *testing.T) {
	fake := &fakeSelectorClient{keys: []string{"KEY-7"}, filterJQL: "project = KEY AND labels = old"}
	deps := selectorDeps(fake, true, nil)

	out, err := runDelete(t.Context(), deps, invocation(false), deleteOptions{
		selector:    command.SelectorFlags{Filter: "10042"},
		force:       true,
		parallelism: 1,
	})
	if err != nil {
		t.Fatalf("runDelete --filter: %v", err)
	}
	if fake.lastJQL != "project = KEY AND labels = old" {
		t.Errorf("searched %q, want the filter's own query", fake.lastJQL)
	}
	if out.Selector == nil {
		t.Fatal("no selector published")
	}
	if out.Selector.Query != "10042" {
		t.Errorf("query = %q, want the id as typed", out.Selector.Query)
	}
	if out.Selector.JQL != "project = KEY AND labels = old" {
		t.Errorf("jql = %q, want the resolved query — without it nobody can tell what this run acted on", out.Selector.JQL)
	}
}

// TestResolutionAsksForOneOverTheCap pins HOW the cap is enforced, not
// just that it is. One page of MaxSelectorKeys+1 makes "too many"
// answerable from a single request; a drain would make fifty requests
// before refusing, and a page of exactly the cap could not tell a full
// match from an over-full one.
func TestResolutionAsksForOneOverTheCap(t *testing.T) {
	fake := &fakeSelectorClient{keys: []string{"KEY-1"}}
	deps := selectorDeps(fake, true, nil)

	if _, err := runDelete(t.Context(), deps, invocation(false), deleteOptions{
		selector:    command.SelectorFlags{JQL: "project = KEY"},
		force:       true,
		parallelism: 1,
	}); err != nil {
		t.Fatalf("runDelete: %v", err)
	}
	if fake.searchMax != command.MaxSelectorKeys+1 {
		t.Errorf("page size = %d, want %d — one over the cap is what makes an over-full match detectable in one request",
			fake.searchMax, command.MaxSelectorKeys+1)
	}
}

// TestOverTheCapIsRefusedRatherThanTruncated is design §3.5 rule 4.
// Acting on the first N of an unbounded match is the dishonesty `list
// --all`'s visible cap exists to avoid, and here the consequence is
// destructive rather than cosmetic: a run that stopped partway looks
// exactly like one that finished.
func TestOverTheCapIsRefusedRatherThanTruncated(t *testing.T) {
	keys := make([]string, command.MaxSelectorKeys+1)
	for i := range keys {
		keys[i] = fmt.Sprintf("KEY-%d", i+1)
	}
	fake := &fakeSelectorClient{keys: keys}
	deps := selectorDeps(fake, true, nil)

	_, err := runDelete(t.Context(), deps, invocation(false), deleteOptions{
		selector:    command.SelectorFlags{JQL: "project = KEY"},
		force:       true,
		parallelism: 1,
	})
	if err == nil {
		t.Fatal("a match over the cap succeeded; it must be refused, never trimmed")
	}
	for _, event := range fake.timeline() {
		if strings.HasPrefix(event, "delete ") {
			t.Fatalf("a refused run still deleted %q", event)
		}
	}
	var input *cli.InputError
	if !errors.As(err, &input) {
		t.Fatalf("refusal is %T, want a typed *cli.InputError so the envelope carries a code", err)
	}
	if input.Field != command.FlagJQL {
		t.Errorf("field = %q, want %q so an agent reads the fix off a field", input.Field, command.FlagJQL)
	}
}

// TestNamedKeysStillPromptOnlyWhereTheyAlreadyDid pins the boundary of
// what the selectors cost. Four commands gained a confirmation they
// never had, and it must fire ONLY for an unnamed target set — a caller
// who types `issue comment add KEY-1` must see exactly the command they
// saw before.
func TestNamedKeysStillPromptOnlyWhereTheyAlreadyDid(t *testing.T) {
	asked := 0
	ask := func(_ context.Context, _ prompt.Confirm) (bool, error) { asked++; return true, nil }

	// No selector: Summary is nil, so the gate is a no-op whatever else
	// is true of the run.
	if err := confirmSelected(t.Context(), ask, interactiveInvocation(false, "KEY-1"), false,
		selectedWrite{Gerund: "commenting on", Imperative: "Comment"},
		command.Targets{Keys: []string{"KEY-1"}}); err != nil {
		t.Fatalf("confirmSelected with named keys: %v", err)
	}
	if asked != 0 {
		t.Error("a command whose caller named the keys opened a dialog; the gate is on the ambiguity, not on the verb")
	}
}
