package issue

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// The keyed MECHANICS — argv ordering under a concurrent fan-out,
// --parallelism, the severity ladder — are pinned once against `issue
// link list` (link_test.go), and the keyed WRITE semantics once against
// `issue comment add`. Both executors are the same shape as this one, so
// what is pinned below is what these two commands DECIDE: that the
// readback is a second request a caller can decline, that a readback
// which fails does not fail a write that already landed, and that the
// two verbs reach different seam methods while sharing everything else.
//
// Both verbs are exercised, not just one. They share an executor, which
// is exactly why: a shared function is where a divergence would hide,
// and a test covering only `add` would not notice `remove` calling the
// wrong seam method.

// fakeWatcherWriteClient is a core.JiraClient double for both watcher
// verbs. It records each call by name so a test can assert not only that
// the write happened but that the READBACK did or did not — which is the
// only way to check what --no-readback actually saves.
type fakeWatcherWriteClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	// writeFailures maps a key to the error the write returns for it;
	// readFailures does the same for the readback. A key in neither
	// succeeds.
	writeFailures map[string]error
	readFailures  map[string]error

	mu        sync.Mutex
	added     []watcherCall
	removed   []watcherCall
	readbacks []string
	// currentUserCalls counts /myself lookups, which is how the @me tests
	// prove the resolution is demand-driven and happens once per run.
	currentUserCalls int
}

// watcherCall is one recorded write.
type watcherCall struct {
	key       string
	accountID string
}

func (f *fakeWatcherWriteClient) AddWatcher(_ context.Context, key, accountID string) (core.WatcherWrite, error) {
	f.mu.Lock()
	f.added = append(f.added, watcherCall{key: key, accountID: accountID})
	f.mu.Unlock()
	if err, ok := f.writeFailures[key]; ok {
		return core.WatcherWrite{}, err
	}
	return core.WatcherWrite{}, nil
}

func (f *fakeWatcherWriteClient) RemoveWatcher(_ context.Context, key, accountID string) (core.WatcherWrite, error) {
	f.mu.Lock()
	f.removed = append(f.removed, watcherCall{key: key, accountID: accountID})
	f.mu.Unlock()
	if err, ok := f.writeFailures[key]; ok {
		return core.WatcherWrite{}, err
	}
	return core.WatcherWrite{}, nil
}

func (f *fakeWatcherWriteClient) GetWatchers(_ context.Context, key string) (core.WatcherRead, error) {
	f.mu.Lock()
	f.readbacks = append(f.readbacks, key)
	f.mu.Unlock()
	if err, ok := f.readFailures[key]; ok {
		return core.WatcherRead{}, err
	}
	return core.WatcherRead{Watchers: &jira.Watchers{
		WatchCount: jira.NullableOf(int64(2)),
		IsWatching: jira.NullableOf(true),
		Watchers: jira.NullableOf([]jira.UserDetails{{
			AccountID:   jira.NullableOf("5b10a2844c20165700ede21g"),
			DisplayName: jira.NullableOf("Ada Lovelace"),
			Active:      jira.NullableOf(true),
			AccountType: jira.NullableOf("atlassian"),
		}}),
	}}, nil
}

// readbackKeys returns the recorded readback keys, so an assertion about
// WHICH keys were read does not depend on completion order.
func (f *fakeWatcherWriteClient) readbackKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.readbacks))
	copy(out, f.readbacks)
	return out
}

func (f *fakeWatcherWriteClient) CurrentUser(context.Context) (core.UserRead, error) {
	f.mu.Lock()
	f.currentUserCalls++
	f.mu.Unlock()
	return core.UserRead{User: &jira.UserDetails{
		AccountID:   jira.NullableOf(resolvedAccountID),
		DisplayName: jira.NullableOf("Ada Lovelace"),
	}}, nil
}

// resolvedAccountID is what this fake resolves @me to. Distinct from
// testAccountID so a capture showing it proves resolution HAPPENED rather
// than echoing a value the invocation already carried.
const resolvedAccountID = "aaaa1111bbbb2222cccc3333"

func watcherWriteDeps(fake *fakeWatcherWriteClient) Deps {
	return Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}
}

const testAccountID = "5b10a2844c20165700ede21g"

func writeOptions(noReadback bool) watcherWriteOptions {
	return watcherWriteOptions{accountID: testAccountID, noReadback: noReadback, parallelism: 4}
}

// TestWatcherWriteDryRunNeverBuildsAClient is the load-bearing guard on
// "a dry run is local-only and never contacts Jira", checked
// structurally: the preview returns before it ever asks Deps for a
// client, so it cannot reach the network no matter what it does next.
// Both verbs are checked, because they take the branch through the same
// function and a divergence there would be silent.
func TestWatcherWriteDryRunNeverBuildsAClient(t *testing.T) {
	for name, verb := range map[string]watcherVerb{"add": watcherAdd, "remove": watcherRemove} {
		t.Run(name, func(t *testing.T) {
			asked := false
			deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
				asked = true
				return nil, errors.New("a dry run must not build a client")
			}}

			results, err := runWatcherWrite(t.Context(), deps, invocation(true, "KEY-123", "KEY-124"), verb, writeOptions(false))
			if err != nil {
				t.Fatalf("runWatcherWrite: %v", err)
			}
			if asked {
				t.Error("the preview asked for a Jira client; --dry-run must never reach the transport")
			}
			if len(results) != 2 {
				t.Fatalf("got %d results, want one per key", len(results))
			}
			for i, r := range results {
				if !r.OK {
					t.Errorf("results[%d].ok = false; a composed preview key succeeded", i)
				}
				if r.Data == nil {
					t.Fatalf("results[%d].data is nil; a successful key must carry data", i)
				}
				if r.Data.Readback != nil {
					t.Errorf("results[%d] carries a readback on a preview; reading the watcher set needs a live GET", i)
				}
			}
		})
	}
}

// TestWatcherWriteReachesTheVerbsOwnSeamMethod is the property a shared
// executor puts at risk: one function, two verbs, and nothing in the
// fan-out itself says which direction it is going. A miswired verb value
// would add a watcher when asked to remove one, which no other test here
// would notice.
func TestWatcherWriteReachesTheVerbsOwnSeamMethod(t *testing.T) {
	keys := []string{"KEY-123", "KEY-124"}

	added := &fakeWatcherWriteClient{}
	if _, err := runWatcherWrite(t.Context(), watcherWriteDeps(added), invocation(false, keys...), watcherAdd, writeOptions(true)); err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(added.added) != 2 || len(added.removed) != 0 {
		t.Errorf("add reached AddWatcher %d times and RemoveWatcher %d times, want 2 and 0", len(added.added), len(added.removed))
	}
	for _, call := range added.added {
		if call.accountID != testAccountID {
			t.Errorf("%s was sent account %q, want %q", call.key, call.accountID, testAccountID)
		}
	}

	removed := &fakeWatcherWriteClient{}
	if _, err := runWatcherWrite(t.Context(), watcherWriteDeps(removed), invocation(false, keys...), watcherRemove, writeOptions(true)); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(removed.removed) != 2 || len(removed.added) != 0 {
		t.Errorf("remove reached RemoveWatcher %d times and AddWatcher %d times, want 2 and 0", len(removed.removed), len(removed.added))
	}
}

// TestWatcherWriteReadsBackByDefault pins the two-requests-per-key cost
// and what it buys: the resulting watcher set, published at
// data.results[].data.readback. Jira answers the write with an empty
// 204, so without this second request the envelope could report nothing
// but the absence of an error.
func TestWatcherWriteReadsBackByDefault(t *testing.T) {
	fake := &fakeWatcherWriteClient{}
	keys := []string{"KEY-123", "KEY-124"}

	results, err := runWatcherWrite(t.Context(), watcherWriteDeps(fake), invocation(false, keys...), watcherAdd, writeOptions(false))
	if err != nil {
		t.Fatalf("runWatcherWrite: %v", err)
	}
	if got := len(fake.readbackKeys()); got != len(keys) {
		t.Errorf("read back %d times, want one per key (%d)", got, len(keys))
	}
	for i, r := range results {
		if r.Data.Readback == nil || r.Data.ReadbackError != nil {
			t.Fatalf("results[%d] carries no readback; the default is to read the set back", i)
		}
		if r.Data.Readback.WatchCount != 2 {
			t.Errorf("results[%d] watch_count = %d, want the server's own count", i, r.Data.Readback.WatchCount)
		}
		if len(r.Data.Readback.Watchers) != 1 {
			t.Errorf("results[%d] carries %d watchers, want the set the readback returned", i, len(r.Data.Readback.Watchers))
		}
	}
}

// TestWatcherWriteNoReadbackSkipsTheSecondRequest is what --no-readback
// is FOR: the flag exists to decline a cost, so a run that still paid it
// would make the flag a lie. The write must still happen.
func TestWatcherWriteNoReadbackSkipsTheSecondRequest(t *testing.T) {
	fake := &fakeWatcherWriteClient{}

	results, err := runWatcherWrite(t.Context(), watcherWriteDeps(fake), invocation(false, "KEY-123"), watcherAdd, writeOptions(true))
	if err != nil {
		t.Fatalf("runWatcherWrite: %v", err)
	}
	if got := fake.readbackKeys(); len(got) != 0 {
		t.Errorf("--no-readback still read back %v; the flag's whole job is to skip that request", got)
	}
	if len(fake.added) != 1 {
		t.Errorf("the write did not happen; --no-readback declines the READBACK, not the write")
	}
	if results[0].Data.Readback != nil {
		t.Error("results[0] carries a readback that was never read")
	}
	if results[0].Data.ReadbackError != nil {
		t.Error("--no-readback invented a read failure")
	}
}

// TestWatcherWriteFailedReadbackKeepsTheKeySuccessful is this command's
// own partial-success decision. The write landed; a second GET falling
// over afterwards means the CLI cannot DESCRIBE the outcome, not that
// the outcome did not happen. Reporting the key as failed would invite a
// retry of a write that already succeeded.
//
// The classified read error distinguishes this from a deliberately skipped
// readback without erasing the write's acknowledgement.
func TestWatcherWriteFailedReadbackKeepsTheKeySuccessful(t *testing.T) {
	fake := &fakeWatcherWriteClient{readFailures: map[string]error{
		"KEY-123": &adapterjira.APIError{StatusCode: http.StatusForbidden, Messages: []string{"You do not have permission to view watchers"}},
	}}

	results, err := runWatcherWrite(t.Context(), watcherWriteDeps(fake), invocation(false, "KEY-123"), watcherAdd, writeOptions(false))
	if err != nil {
		t.Fatalf("a failed readback must not fail the command: %v", err)
	}
	if !results[0].OK {
		t.Fatal("the key reports failure; the write landed, and only the description of it did not")
	}
	if results[0].Data == nil || results[0].Data.Readback != nil {
		t.Errorf("results[0].data = %+v, want a successful entry carrying no readback", results[0].Data)
	}
	if results[0].Data.ReadbackError == nil || results[0].Data.ReadbackError.Code != "jira_forbidden" {
		t.Fatal("acknowledged watcher write hid its failed readback")
	}
	if len(fake.added) != 1 {
		t.Error("the write did not happen at all, so this test proved nothing about the readback")
	}
}

type lateWatcherReceiptClient struct{ unusedJiraClient }

type watcherReadbackBoundaryClient struct {
	*fakeWatcherWriteClient
	waitForCancellation bool
}

func (c watcherReadbackBoundaryClient) GetWatchers(ctx context.Context, _ string) (core.WatcherRead, error) {
	if c.waitForCancellation {
		<-ctx.Done()
		return core.WatcherRead{}, ctx.Err()
	}
	return core.WatcherRead{}, nil
}

func TestWatcherReadbackBoundaryRetainsAcknowledgedWrite(t *testing.T) {
	t.Parallel()
	for _, selected := range []watcherVerb{watcherAdd, watcherRemove} {
		for _, canceled := range []bool{false, true} {
			t.Run(selected.bare+"/"+map[bool]string{false: "nil-response", true: "deadline"}[canceled], func(t *testing.T) {
				t.Parallel()
				ctx := t.Context()
				if canceled {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, time.Millisecond)
					defer cancel()
				}
				fake := &fakeWatcherWriteClient{}
				client := watcherReadbackBoundaryClient{fakeWatcherWriteClient: fake, waitForCancellation: canceled}
				deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}
				results, err := runWatcherWrite(ctx, deps, invocation(false, "KEY-1"), selected, writeOptions(false))
				if (err != nil) != canceled || (canceled && !errors.Is(err, context.DeadlineExceeded)) || len(results) != 1 || !results[0].OK || results[0].Data == nil || results[0].Data.Readback != nil || results[0].Data.ReadbackError == nil || len(fake.added)+len(fake.removed) != 1 {
					t.Fatal("readback boundary discarded acknowledgement, invented state or lost cancellation")
				}
				if canceled && results[0].Data.ReadbackError.Code != "timeout" {
					t.Fatal("readback boundary discarded its deadline classification")
				}
				if !canceled && !strings.Contains(results[0].Data.ReadbackError.Message, "empty watchers response") {
					t.Fatal("nil post-write observation was silently accepted")
				}
			})
		}
	}
}

func (lateWatcherReceiptClient) AddWatcher(ctx context.Context, _, _ string) (core.WatcherWrite, error) {
	<-ctx.Done()
	return core.WatcherWrite{}, nil
}

func (lateWatcherReceiptClient) RemoveWatcher(ctx context.Context, _, _ string) (core.WatcherWrite, error) {
	<-ctx.Done()
	return core.WatcherWrite{}, nil
}

func (lateWatcherReceiptClient) GetWatchers(ctx context.Context, _ string) (core.WatcherRead, error) {
	<-ctx.Done()
	return core.WatcherRead{Watchers: &jira.Watchers{WatchCount: jira.NullableOf(int64(0)), IsWatching: jira.NullableOf(false)}}, nil
}

func TestWatcherDeadlineRetainsLateJoinedOutcomes(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"list", "add", "remove"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
			defer cancel()
			client := lateWatcherReceiptClient{}
			deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return client, nil }}
			if verb == "list" {
				out, err := runWatchersList(ctx, deps, []string{"KEY-1"}, 1)
				if !errors.Is(err, context.DeadlineExceeded) || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Data == nil || out.Results[0].Issue.Key != "KEY-1" || out.Results[0].Data.WatchCount != 0 || out.Results[0].Data.IsWatching {
					t.Fatal("deadline discarded a late joined watcher read or changed cancellation")
				}
				return
			}
			selected := watcherAdd
			if verb == "remove" {
				selected = watcherRemove
			}
			results, err := runWatcherWrite(ctx, deps, invocation(false, "KEY-1"), selected, watcherWriteOptions{accountID: "account-fixture", noReadback: true, parallelism: 1})
			if !errors.Is(err, context.DeadlineExceeded) || len(results) != 1 || !results[0].OK || results[0].Data == nil || results[0].Issue.Key != "KEY-1" || results[0].Data.Readback != nil || results[0].Data.ReadbackError != nil {
				t.Fatal("deadline discarded a late acknowledged watcher write or invented a readback")
			}
		})
	}
}

func TestWatcherTotalFailurePreservesOriginalTransportCause(t *testing.T) {
	t.Parallel()
	for _, selected := range []watcherVerb{watcherAdd, watcherRemove} {
		t.Run(selected.bare, func(t *testing.T) {
			t.Parallel()
			cause := &adapterjira.APIError{StatusCode: http.StatusTooManyRequests, RetryAfterSeconds: 7, Retry: core.RetryInfo{Attempts: 1, Waited: time.Millisecond}}
			client := &fakeWatcherWriteClient{writeFailures: map[string]error{"KEY-1": cause}}
			results, err := runWatcherWrite(t.Context(), watcherWriteDeps(client), invocation(false, "KEY-1"), selected, writeOptions(true))
			original, ok := errors.AsType[*adapterjira.APIError](err)
			mapped := cli.MapError(err)
			if !ok || original != cause || len(results) != 1 || results[0].OK || results[0].Err == nil || results[0].Err.Code != "jira_rate_limited" || mapped.Exit != cli.ExitRateLimit || mapped.RetryAfterSeconds != 7 || mapped.Attempts != 1 || mapped.Waited != time.Millisecond {
				t.Fatal("retaining watcher outcomes erased the original transport cause or retry facts")
			}
		})
	}
}

// TestWatcherWritePartialFailureIsAPartialResult is the keyed WRITE
// rule, and the argument for it is comment add's: the issues that DID
// take the change really did, so the envelope must name exactly which so
// a caller can retry only the rest.
func TestWatcherWritePartialFailureIsAPartialResult(t *testing.T) {
	keys := []string{"KEY-123", "KEY-404", "KEY-125"}
	fake := &fakeWatcherWriteClient{writeFailures: map[string]error{
		"KEY-404": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Issue does not exist"}},
	}}

	results, err := runWatcherWrite(t.Context(), watcherWriteDeps(fake), invocation(false, keys...), watcherAdd, writeOptions(true))
	if err != nil {
		t.Fatalf("a partly-successful write must not fail the command: %v", err)
	}
	for i, want := range keys {
		if results[i].Issue.Key != want {
			t.Fatalf("results[%d].issue.key = %q, want %q (argv order, not completion order)", i, results[i].Issue.Key, want)
		}
	}
	if !results[0].OK || !results[2].OK {
		t.Error("the keys that succeeded should report ok")
	}
	bad := results[1]
	if bad.OK {
		t.Fatal("the failed key reports ok")
	}
	if bad.Data != nil {
		t.Error("a failed key must carry error in data's place, never a half-filled operation object")
	}
	if bad.Err == nil || bad.Err.Code != "jira_not_found" {
		t.Errorf("failed key error = %+v, want the jira_not_found taxonomy code", bad.Err)
	}
	if bad.Err.Hint == "" {
		t.Error("a published key error must carry a hint, like every other taxonomy entry")
	}
}

// TestWatcherWriteTotalFailureFailsTheCommand: with no partial result to
// hand back, an ok envelope carrying only failures would report success
// for a command that achieved nothing.
func TestWatcherWriteTotalFailureFailsTheCommand(t *testing.T) {
	fake := &fakeWatcherWriteClient{writeFailures: map[string]error{
		"KEY-404": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Issue does not exist"}},
		"KEY-405": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Issue does not exist"}},
	}}

	_, err := runWatcherWrite(t.Context(), watcherWriteDeps(fake), invocation(false, "KEY-404", "KEY-405"), watcherRemove, writeOptions(true))
	if err == nil {
		t.Fatal("a run where every key failed must fail the command")
	}
	if got := cli.MapError(err); got.Code != "jira_not_found" {
		t.Errorf("code = %q, want jira_not_found preserved from the per-key error", got.Code)
	}
}

// TestWatcherWriteReadOnlyModeFailsEveryKey: read-only is enforced at
// the transport, so a keyed write surfaces it per key — every key fails,
// no key succeeds, and KeyedFailure hands back the original error with
// its own code.
func TestWatcherWriteReadOnlyModeFailsEveryKey(t *testing.T) {
	fake := &fakeWatcherWriteClient{writeFailures: map[string]error{
		"KEY-123": adapterjira.ErrReadOnly,
		"KEY-124": adapterjira.ErrReadOnly,
	}}

	_, err := runWatcherWrite(t.Context(), watcherWriteDeps(fake), invocation(false, "KEY-123", "KEY-124"), watcherAdd, writeOptions(true))
	if err == nil {
		t.Fatal("read-only mode must fail the command, not report a successful no-op")
	}
	if got := cli.MapError(err); got.Code != cli.CodeReadOnlyBlocked {
		t.Errorf("code = %q, want %q", got.Code, cli.CodeReadOnlyBlocked)
	}
}

// TestRequireUserRefusesAnUnnamedAccount pins the refusal that keeps a
// pointless request off the wire, and pins WHERE it points: an agent
// recovering from this reads e.Field, not the prose.
func TestRequireUserRefusesAnUnnamedAccount(t *testing.T) {
	err := requireUser("")
	if err == nil {
		t.Fatal("a watcher verb naming no account must be refused before any I/O")
	}
	var e *cli.InputError
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not a *cli.InputError", err)
	}
	if e.Code != cli.CodeFlagRequired {
		t.Errorf("code = %q, want %q", e.Code, cli.CodeFlagRequired)
	}
	if e.Field != flagUser {
		t.Errorf("field = %q, want %q", e.Field, flagUser)
	}
	if !strings.Contains(e.Message, "--"+flagUser) {
		t.Errorf("message %q does not name --%s", e.Message, flagUser)
	}
	if err := requireUser(testAccountID); err != nil {
		t.Errorf("requireUser(%q) = %v, want nil", testAccountID, err)
	}
}

// TestWatcherWriteRegistersItsFlags pins the surface both verbs share.
// --no-readback is the one that has to be there — it is the only way to
// decline the second request per key — and --force is the one that must
// not be: neither verb destroys anything a sibling cannot put back.
//
// --validate-remote MOVED from the forbidden list to the required one, and
// the reason it was forbidden was sound when written: an account id is
// opaque, so there was nothing a preview could check against the site and
// the flag would have been hollow. `@me` changed that. It is a sentinel
// this CLI resolves through GET /myself, so the flag now buys a real read
// — the one thing a local preview cannot otherwise know — and refusing to
// register it would leave `--user @me --dry-run` with no way to see the
// resolved value.
func TestWatcherWriteRegistersItsFlags(t *testing.T) {
	for name, cmd := range map[string]*cobra.Command{
		"add":    newWatchersAdd(Deps{}),
		"remove": newWatchersRemove(Deps{}),
	} {
		t.Run(name, func(t *testing.T) {
			fs := cmd.Flags()
			for _, want := range []string{
				cli.FlagDryRun, command.FlagNoReadback, command.FlagParallelism, flagUser,
				command.FlagValidateRemote,
			} {
				if fs.Lookup(want) == nil {
					t.Errorf("--%s is not registered", want)
				}
			}
			for _, absent := range []string{command.FlagForce, command.FlagVerify} {
				if fs.Lookup(absent) != nil {
					t.Errorf("--%s is registered; this command needs none of it", absent)
				}
			}
		})
	}
}

// TestWatcherWritesAreKeyedMutations guards the declarations
// command.New cross-checks — Keyed against the shared keyed surface in
// both directions, and WritesJira against the dry-run requirement.
func TestWatcherWritesAreKeyedMutations(t *testing.T) {
	for op, cmd := range map[string]*cobra.Command{
		"issue.watchers.add":    newWatchersAdd(Deps{}),
		"issue.watchers.remove": newWatchersRemove(Deps{}),
	} {
		t.Run(op, func(t *testing.T) {
			metas, ok := command.MetaFor(cmd)
			if !ok || len(metas) == 0 {
				t.Fatalf("no metadata recorded for %s", op)
			}
			m := metas[0]
			if m.Op != op {
				t.Errorf("op = %q, want %q", m.Op, op)
			}
			if !m.WritesJira {
				t.Error("WritesJira is false; this command writes to Jira")
			}
			if !m.Keyed {
				t.Error("Keyed is false; this command returns results[]")
			}
			if m.Class() != command.ClassMutation {
				t.Errorf("class = %v, want a mutation", m.Class())
			}
		})
	}
}

func TestWatcherWriteEnvelopeRegistrationsMatch(t *testing.T) {
	if reg, ok := envelope.Registered("issue.watchers.add"); !ok {
		t.Error("issue.watchers.add has no envelope registration")
	} else if _, isType := reg.Sample.(envelope.IssueWatchersAddOutput); !isType {
		t.Errorf("registered sample is %T, want envelope.IssueWatchersAddOutput", reg.Sample)
	}
	if reg, ok := envelope.Registered("issue.watchers.remove"); !ok {
		t.Error("issue.watchers.remove has no envelope registration")
	} else if _, isType := reg.Sample.(envelope.IssueWatchersRemoveOutput); !isType {
		t.Errorf("registered sample is %T, want envelope.IssueWatchersRemoveOutput", reg.Sample)
	}
}

// The @me contract on `--user`, which is the watch/unwatch case design §6
// promised: `watchers add KEY --user @me` is why this CLI ships no
// separate `watch` verb.
//
// The POLICY (live resolves, a bare preview warns and leaves the
// sentinel, --validate-remote resolves in a preview) is
// command.ResolveAccounts' and is pinned once against create/edit. What
// is pinned HERE is that this command adopts it rather than
// reimplementing it, and that the value reaching the wire is the resolved
// one.

// TestResolveWatcherUserResolvesOnTheLivePath: the sentinel must never
// reach the wire, and the caller's variable is rewritten in place so the
// envelope publishes what was SENT rather than what was typed.
func TestResolveWatcherUserResolvesOnTheLivePath(t *testing.T) {
	fake := &fakeWatcherWriteClient{}
	user := command.MeSentinel

	client, err := resolveWatcherUser(t.Context(), watcherWriteDeps(fake), false, false, &user)
	if err != nil {
		t.Fatalf("resolveWatcherUser: %v", err)
	}
	if user != resolvedAccountID {
		t.Errorf("user = %q, want the resolved id %q", user, resolvedAccountID)
	}
	if client == nil {
		t.Error("no client returned; the caller would build a second one and pay twice for credentials")
	}
	if fake.currentUserCalls != 1 {
		t.Errorf("CurrentUser called %d times, want 1", fake.currentUserCalls)
	}
}

// TestResolveWatcherUserLeavesMeInABarePreview: --dry-run is local-only,
// so it cannot know the id and must not invent one — it leaves the
// sentinel and warns.
func TestResolveWatcherUserLeavesMeInABarePreview(t *testing.T) {
	cli.TakeWarnings()
	t.Cleanup(func() { cli.TakeWarnings() })

	asked := false
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a bare preview must not build a client")
	}}
	user := command.MeSentinel

	client, err := resolveWatcherUser(t.Context(), deps, true, false, &user)
	if err != nil {
		t.Fatalf("resolveWatcherUser: %v", err)
	}
	if asked || client != nil {
		t.Error("a bare preview built a client to resolve @me")
	}
	if user != command.MeSentinel {
		t.Errorf("user = %q, want the literal %q left in place", user, command.MeSentinel)
	}
	warnings := cli.TakeWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "--"+flagUser) {
		t.Errorf("want one warning naming --%s, got %+v", flagUser, warnings)
	}
}

// TestResolveWatcherUserUnderValidateRemote: the opt-in that buys the
// lookup on a preview. Nothing is written — the flag buys a READ.
func TestResolveWatcherUserUnderValidateRemote(t *testing.T) {
	cli.TakeWarnings()
	t.Cleanup(func() { cli.TakeWarnings() })

	fake := &fakeWatcherWriteClient{}
	user := command.MeSentinel

	if _, err := resolveWatcherUser(t.Context(), watcherWriteDeps(fake), true, true, &user); err != nil {
		t.Fatalf("resolveWatcherUser: %v", err)
	}
	if user != resolvedAccountID {
		t.Errorf("user = %q, want the resolved id under --validate-remote", user)
	}
	if len(fake.added) != 0 || len(fake.removed) != 0 {
		t.Error("--validate-remote wrote something; it buys a READ, never a write")
	}
	if w := cli.TakeWarnings(); len(w) != 0 {
		t.Errorf("resolved preview still warned: %+v", w)
	}
}

// TestResolveWatcherUserWithoutSentinelCostsNoLookup: a plain account id
// must not pay for a /myself round trip on either path.
func TestResolveWatcherUserWithoutSentinelCostsNoLookup(t *testing.T) {
	fake := &fakeWatcherWriteClient{}
	user := testAccountID

	if _, err := resolveWatcherUser(t.Context(), watcherWriteDeps(fake), false, false, &user); err != nil {
		t.Fatalf("resolveWatcherUser: %v", err)
	}
	if fake.currentUserCalls != 0 {
		t.Errorf("CurrentUser called %d times with no sentinel; the lookup must be demand-driven", fake.currentUserCalls)
	}
	if user != testAccountID {
		t.Errorf("user = %q, want it untouched", user)
	}
}
