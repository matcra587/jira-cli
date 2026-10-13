package issue

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// The keyed mechanics are pinned once against `issue link list` and the
// keyed write semantics once against `issue comment add`. What is pinned
// here is what the weblink group DECIDES:
//
//   - the wire's `object` wrapper is flattened, and the integer id
//     becomes the string every other id in this contract is;
//   - a live add reports whether it ADDED or REPLACED, which Jira says
//     only through a status code;
//   - a relative URL is refused before it reaches a server that would
//     happily store it;
//   - delete takes a key AND an id, where `link delete` takes an id
//     alone — the two resources differ in exactly that way.

// fakeWeblinkClient is a core.JiraClient double for the whole weblink
// group.
type fakeWeblinkClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	// links is what GetRemoteLinks returns per key; failures maps a key
	// to an error returned by whichever verb is under test.
	links    map[string][]jira.RemoteIssueLink
	failures map[string]error
	// createdFor names the keys whose add answers 201 (a new link);
	// every other key answers 200 (an existing link replaced).
	createdFor map[string]bool

	mu      sync.Mutex
	added   []weblinkAddCall
	deleted []weblinkDeleteCall
}

type weblinkAddCall struct {
	key string
	req jira.RemoteLinkRequest
}

type weblinkDeleteCall struct {
	key    string
	linkID string
}

func (f *fakeWeblinkClient) GetRemoteLinks(_ context.Context, key string) (core.RemoteLinkRead, error) {
	if err, ok := f.failures[key]; ok {
		return core.RemoteLinkRead{}, err
	}
	return core.RemoteLinkRead{Links: f.links[key]}, nil
}

func (f *fakeWeblinkClient) AddRemoteLink(_ context.Context, key string, req jira.RemoteLinkRequest) (core.RemoteLinkWrite, error) {
	f.mu.Lock()
	f.added = append(f.added, weblinkAddCall{key: key, req: req})
	f.mu.Unlock()
	if err, ok := f.failures[key]; ok {
		return core.RemoteLinkWrite{}, err
	}
	return core.RemoteLinkWrite{
		Created: f.createdFor[key],
		Link:    &jira.RemoteIssueLink{ID: jira.NullableOf(int64(90001))},
	}, nil
}

func (f *fakeWeblinkClient) DeleteRemoteLink(_ context.Context, key, linkID string) (core.RemoteLinkDeleteWrite, error) {
	f.mu.Lock()
	f.deleted = append(f.deleted, weblinkDeleteCall{key: key, linkID: linkID})
	f.mu.Unlock()
	if err, ok := f.failures[key]; ok {
		return core.RemoteLinkDeleteWrite{}, err
	}
	return core.RemoteLinkDeleteWrite{}, nil
}

func weblinkDeps(fake *fakeWeblinkClient) Deps {
	return Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}
}

func testWeblink() weblinkFlags {
	return weblinkFlags{url: "https://example.com/runbook", title: "Runbook"}
}

// TestWeblinkListFlattensTheWireObject pins the one shape decision on
// the read side: url and title live inside an `object` wrapper on the
// wire and come up a level here, and the integer id becomes the string
// every other id in this contract is.
func TestWeblinkListFlattensTheWireObject(t *testing.T) {
	fake := &fakeWeblinkClient{links: map[string][]jira.RemoteIssueLink{
		"KEY-123": {{
			ID:           jira.NullableOf(int64(10042)),
			GlobalID:     jira.NullableOf("ci-build-7"),
			Relationship: jira.NullableOf("is implemented by"),
			Application:  jira.NullableOf(jira.Application{Name: jira.NullableOf("GitHub")}),
			Object: jira.NullableOf(jira.RemoteObject{
				URL:     jira.NullableOf("https://example.com/pr/9"),
				Title:   jira.NullableOf("PR 9"),
				Summary: jira.NullableOf("Adds the retry loop."),
			}),
		}},
	}}

	out, err := runWeblinkList(t.Context(), weblinkDeps(fake), []string{"KEY-123"}, 4)
	if err != nil {
		t.Fatalf("runWeblinkList: %v", err)
	}
	got := out.Results[0].Data.Weblinks
	if len(got) != 1 {
		t.Fatalf("got %d weblinks, want 1", len(got))
	}
	w := got[0]
	if w.ID != "10042" {
		t.Errorf("id = %q, want the wire's integer id as a string", w.ID)
	}
	if w.URL != "https://example.com/pr/9" || w.Title != "PR 9" {
		t.Errorf("url/title = %q/%q, want them lifted out of the wire's object wrapper", w.URL, w.Title)
	}
	if w.Summary != "Adds the retry loop." || w.Relationship != "is implemented by" {
		t.Errorf("summary/relationship = %q/%q", w.Summary, w.Relationship)
	}
	if w.GlobalID != "ci-build-7" {
		t.Errorf("global_id = %q, want it published — it is the key add de-duplicates on", w.GlobalID)
	}
	if w.Application != "GitHub" {
		t.Errorf("application = %q", w.Application)
	}
}

// TestWeblinkListEmptyIsAnEmptyArray: an issue with no web links must
// publish [] rather than null, so a caller iterating needs no null
// check. Also the case that has no loud-failure guard, because an empty
// SLICE is a real answer rather than the ambiguous nil payload the
// watcher read guards against.
func TestWeblinkListEmptyIsAnEmptyArray(t *testing.T) {
	fake := &fakeWeblinkClient{links: map[string][]jira.RemoteIssueLink{"KEY-123": nil}}

	out, err := runWeblinkList(t.Context(), weblinkDeps(fake), []string{"KEY-123"}, 4)
	if err != nil {
		t.Fatalf("runWeblinkList: %v", err)
	}
	if !out.Results[0].OK {
		t.Fatal("an issue with no web links is a successful read, not a failure")
	}
	if out.Results[0].Data.Weblinks == nil {
		t.Error("weblinks is nil; an issue with none publishes an empty array")
	}
}

// TestWeblinkAddReportsAddedVersusReplaced is the decision --global-id
// exists for. Jira says which happened only through its status code, so
// if the CLI collapses the two a caller cannot tell a re-run that
// de-duplicated from one that quietly doubled up.
func TestWeblinkAddReportsAddedVersusReplaced(t *testing.T) {
	fake := &fakeWeblinkClient{createdFor: map[string]bool{"KEY-123": true}}
	link := testWeblink()
	link.globalID = "ci-build-7"

	out, err := runWeblinkAdd(t.Context(), weblinkDeps(fake), invocation(false, "KEY-123", "KEY-124"), link, 4)
	if err != nil {
		t.Fatalf("runWeblinkAdd: %v", err)
	}
	created := out.Results[0].Data.Created
	if created == nil || !*created {
		t.Errorf("results[0].created = %v, want true — this key's link was new", created)
	}
	replaced := out.Results[1].Data.Created
	if replaced == nil || *replaced {
		t.Errorf("results[1].created = %v, want false — this key replaced an existing link", replaced)
	}
	if out.GlobalID != "ci-build-7" {
		t.Errorf("global_id = %q, want it published as request context", out.GlobalID)
	}
	for _, call := range fake.added {
		native, err := call.req.Native()
		if err != nil {
			t.Fatal(err)
		}
		if native.GlobalID.Value != "ci-build-7" {
			t.Errorf("%s was sent globalId %q, want the caller's own", call.key, native.GlobalID.Value)
		}
		if native.Object.Value.URL.Value != link.url || native.Object.Value.Title.Value != link.title {
			t.Errorf("%s got object %+v, want the composed link", call.key, native.Object.Value)
		}
	}
}

// TestWeblinkAddPreviewLeavesCreatedAbsent: false would claim Jira
// REPLACED something, which is a specific thing to have happened and did
// not. Absence says the question was never asked — which is why the
// field is a pointer.
func TestWeblinkAddPreviewLeavesCreatedAbsent(t *testing.T) {
	asked := false
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a dry run must not build a client")
	}}

	out, err := runWeblinkAdd(t.Context(), deps, invocation(true, "KEY-123"), testWeblink(), 4)
	if err != nil {
		t.Fatalf("runWeblinkAdd: %v", err)
	}
	if asked {
		t.Error("the preview asked for a Jira client; --dry-run must never reach the transport")
	}
	if !out.DryRun || out.URL != "https://example.com/runbook" {
		t.Errorf("preview = %+v, want dry_run true and the composed request published", out)
	}
	if out.Results[0].Data.Created != nil {
		t.Error("created is present on a preview; nothing was added and nothing was replaced")
	}
	if out.Results[0].Data.Weblink != nil {
		t.Error("the preview carries a weblink identifier; Jira issued none")
	}
}

// TestWeblinkAddRefusesARelativeURL: Jira stores whatever it is given
// here without validating it, so a relative URL becomes a dead link the
// caller discovers by clicking it weeks later. This refusal is the only
// place that mistake can still be caught.
func TestWeblinkAddRefusesARelativeURL(t *testing.T) {
	tests := []struct {
		name  string
		link  weblinkFlags
		field string
	}{
		{"no url", weblinkFlags{title: "Runbook"}, flagURL},
		{"relative url", weblinkFlags{url: "example.com/runbook", title: "Runbook"}, flagURL},
		{"scheme but no host", weblinkFlags{url: "https://", title: "Runbook"}, flagURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.link.validate()
			if err == nil {
				t.Fatal("want a refusal before any client is built")
			}
			var e *cli.InputError
			if !errors.As(err, &e) {
				t.Fatalf("error %v is not a *cli.InputError", err)
			}
			if e.Field != tt.field {
				t.Errorf("field = %q, want %q", e.Field, tt.field)
			}
		})
	}
	if err := testWeblink().validate(); err != nil {
		t.Errorf("a well-formed link was refused: %v", err)
	}
}

// TestWeblinkAddPartialFailureIsAPartialResult is the keyed WRITE rule.
// It matters here for linkCreateFanOut's reason: without a --global-id,
// re-running a key that already succeeded adds a SECOND copy.
func TestWeblinkAddPartialFailureIsAPartialResult(t *testing.T) {
	keys := []string{"KEY-1", "KEY-404", "KEY-3"}
	fake := &fakeWeblinkClient{failures: map[string]error{
		"KEY-404": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Issue does not exist"}},
	}}

	out, err := runWeblinkAdd(t.Context(), weblinkDeps(fake), invocation(false, keys...), testWeblink(), 4)
	if err != nil {
		t.Fatalf("a partly-successful write must not fail the command: %v", err)
	}
	for i, want := range keys {
		if out.Results[i].Issue.Key != want {
			t.Fatalf("results[%d].issue.key = %q, want %q (argv order)", i, out.Results[i].Issue.Key, want)
		}
	}
	if !out.Results[0].OK || !out.Results[2].OK {
		t.Error("the keys that succeeded should report ok")
	}
	if bad := out.Results[1]; bad.OK || bad.Err == nil || bad.Err.Code != "jira_not_found" {
		t.Errorf("failed key = %+v, want a jira_not_found entry carrying no data", bad)
	}
}

func TestWeblinkAddTotalFailureFailsTheCommand(t *testing.T) {
	fake := &fakeWeblinkClient{failures: map[string]error{
		"KEY-404": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Issue does not exist"}},
	}}

	_, err := runWeblinkAdd(t.Context(), weblinkDeps(fake), invocation(false, "KEY-404"), testWeblink(), 4)
	if err == nil {
		t.Fatal("a run where every key failed must fail the command")
	}
	if got := cli.MapError(err); got.Code != "jira_not_found" {
		t.Errorf("code = %q, want jira_not_found", got.Code)
	}
}

// TestWeblinkDeleteTakesBothKeyAndID is the contrast with `issue link
// delete`, and it is a real API difference rather than an inconsistency:
// a remote link's id is scoped to its issue, so both are genuinely
// needed here while only the id is there.
func TestWeblinkDeleteTakesBothKeyAndID(t *testing.T) {
	fake := &fakeWeblinkClient{}

	out, err := runWeblinkDelete(t.Context(), weblinkDeps(fake), invocation(false, "KEY-123", "10042"))
	if err != nil {
		t.Fatalf("runWeblinkDelete: %v", err)
	}
	if out.Issue.Key != "KEY-123" || out.WeblinkID != "10042" {
		t.Errorf("published (%q, %q), want (KEY-123, 10042)", out.Issue.Key, out.WeblinkID)
	}
	if len(fake.deleted) != 1 || fake.deleted[0] != (weblinkDeleteCall{key: "KEY-123", linkID: "10042"}) {
		t.Errorf("deleted %v, want the key and id together", fake.deleted)
	}
}

func TestWeblinkDeleteDryRunNeverBuildsAClient(t *testing.T) {
	asked := false
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a dry run must not build a client")
	}}

	out, err := runWeblinkDelete(t.Context(), deps, invocation(true, "KEY-123", "10042"))
	if err != nil {
		t.Fatalf("runWeblinkDelete: %v", err)
	}
	if asked {
		t.Error("the preview asked for a Jira client")
	}
	if !out.DryRun {
		t.Error("dry_run = false on a preview")
	}
}

// TestWeblinkDeleteTakesNoForce pins the contrast with `comment delete`:
// the test for a confirmation is whether there is an undo, and `weblink
// add` is one.
func TestWeblinkDeleteTakesNoForce(t *testing.T) {
	fs := newWeblinkDelete(Deps{}).Flags()
	if fs.Lookup(cli.FlagDryRun) == nil {
		t.Error("--dry-run is not registered; every mutation carries it")
	}
	for _, absent := range []string{command.FlagForce, command.FlagYes, command.FlagParallelism} {
		if fs.Lookup(absent) != nil {
			t.Errorf("--%s is registered; a web link is reversible with `weblink add`", absent)
		}
	}
}

// TestWeblinkMetadataAndRegistrations covers all three operations at
// once: the declarations command.New cross-checks, and the envelope
// registration join it performs at construction.
//
// The three differ in every one of these flags, which is the point of
// checking them together — `list` reads and is keyed, `add` writes and
// is keyed, `delete` writes and is not.
func TestWeblinkMetadataAndRegistrations(t *testing.T) {
	tests := []struct {
		op       string
		cmd      *cobra.Command
		keyed    bool
		writes   bool
		identity bool
		sample   string
	}{
		{"issue.weblink.list", newWeblinkList(Deps{}), true, false, true, "envelope.IssueWeblinkListOutput"},
		{"issue.weblink.add", newWeblinkAdd(Deps{}), true, true, true, "envelope.IssueWeblinkAddOutput"},
		{"issue.weblink.delete", newWeblinkDelete(Deps{}), false, true, true, "envelope.IssueWeblinkDeleteOutput"},
	}
	for _, tt := range tests {
		t.Run(tt.op, func(t *testing.T) {
			metas, ok := command.MetaFor(tt.cmd)
			if !ok || len(metas) == 0 {
				t.Fatalf("no metadata recorded for %s", tt.op)
			}
			m := metas[0]
			if m.Op != tt.op {
				t.Errorf("op = %q, want %q", m.Op, tt.op)
			}
			if m.Keyed != tt.keyed {
				t.Errorf("Keyed = %v, want %v", m.Keyed, tt.keyed)
			}
			if m.WritesJira != tt.writes {
				t.Errorf("WritesJira = %v, want %v", m.WritesJira, tt.writes)
			}
			if m.Identity != tt.identity {
				t.Errorf("Identity = %v, want %v", m.Identity, tt.identity)
			}
			reg, found := envelope.Registered(tt.op)
			if !found {
				t.Fatalf("%s has no envelope registration", tt.op)
			}
			if got := fmt.Sprintf("%T", reg.Sample); got != tt.sample {
				t.Errorf("registered sample is %s, want %s", got, tt.sample)
			}
		})
	}
}

// TestWeblinkURLTruncationMarksTheCut: the machine envelope carries the
// whole URL, so the human table's shortening has to be visible or a
// reader copies a truncated link and wonders why it 404s.
func TestWeblinkURLTruncationMarksTheCut(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("a", 100)
	got := truncateURL(long, 20)
	if len([]rune(got)) != 20 {
		t.Errorf("truncated to %d runes, want 20", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated URL %q does not end in an ellipsis; the cut has to be visible", got)
	}
	if short := truncateURL("https://example.com", 60); short != "https://example.com" {
		t.Errorf("a URL inside the width was altered: %q", short)
	}
}
