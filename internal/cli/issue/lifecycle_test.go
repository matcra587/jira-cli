package issue

import (
	"context"
	"encoding/json"
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

// The keyed mechanics are pinned elsewhere. What is pinned here is what
// these three commands decide, and each has one decision worth the test:
//
//   - delete asks ONCE, naming every key, and a bad key fails as its own
//     entry rather than stopping the run;
//   - clone copies the source's fields and lets the caller's flags
//     override them — in that order;
//   - rank sends ONE request whose array order IS the ranking, and turns
//     a single 207 into per-key verdicts.

// fakeLifecycleClient is a core.JiraClient double for delete, clone and
// rank.
type fakeLifecycleClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	// source is what GetIssue returns for any key; failures maps a key to
	// an error from whichever verb is under test.
	source   *jira.IssueBean
	failures map[string]error
	// rank is what RankIssues answers with.
	rank core.RankWrite

	mu       sync.Mutex
	deleted  []deleteCall
	created  []jira.IssueWriteRequest
	rankReqs []jira.RankRequest
}

type deleteCall struct {
	key      string
	subtasks bool
}

func (f *fakeLifecycleClient) DeleteIssue(_ context.Context, key string, subtasks bool) (core.IssueDeleteWrite, error) {
	f.mu.Lock()
	f.deleted = append(f.deleted, deleteCall{key: key, subtasks: subtasks})
	f.mu.Unlock()
	if err, ok := f.failures[key]; ok {
		return core.IssueDeleteWrite{}, err
	}
	return core.IssueDeleteWrite{StatusCode: http.StatusNoContent}, nil
}

func (f *fakeLifecycleClient) GetIssue(_ context.Context, key string, _ []string, _ ...string) (core.IssueRead, error) {
	if err, ok := f.failures[key]; ok {
		return core.IssueRead{}, err
	}
	return core.IssueRead{Issue: f.source}, nil
}

func (f *fakeLifecycleClient) CreateIssue(_ context.Context, req jira.IssueWriteRequest) (core.IssueCreateWrite, error) {
	f.mu.Lock()
	f.created = append(f.created, req)
	n := len(f.created)
	f.mu.Unlock()
	if err, ok := f.failures["create"]; ok {
		return core.IssueCreateWrite{}, err
	}
	return core.IssueCreateWrite{StatusCode: 201, Issue: &jira.CreatedIssue{
		ID: "900" + string(rune('0'+n)), Key: "NEW-" + string(rune('0'+n)),
	}}, nil
}

func (f *fakeLifecycleClient) RankIssues(_ context.Context, req jira.RankRequest) (core.RankWrite, error) {
	f.mu.Lock()
	f.rankReqs = append(f.rankReqs, req)
	f.mu.Unlock()
	if err, ok := f.failures["rank"]; ok {
		return core.RankWrite{}, err
	}
	rank := f.rank
	if rank.StatusCode == 0 {
		rank.StatusCode = 204
	}
	return rank, nil
}

func (*fakeLifecycleClient) CurrentUser(context.Context) (core.UserRead, error) {
	return core.UserRead{User: &jira.UserDetails{
		AccountID: jira.NullableOf("5b10a2844c20165700ede21g"),
	}}, nil
}

func lifecycleDeps(fake core.JiraClient) Deps {
	return Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}
}

// --- issue delete ---

// TestDeleteAsksOnceNamingEveryKey is this command's own confirmation
// decision. One dialog for the whole run, because a prompt per issue is
// unanswerable for a list of ten and turns a partial "no" into a
// half-deleted set — and it NAMES the keys, because a list is the only
// form in which somebody notices the one they did not mean to type.
func TestDeleteAsksOnceNamingEveryKey(t *testing.T) {
	fake := &fakeLifecycleClient{}
	confirm := &recordingConfirm{answer: true}
	deps := lifecycleDeps(fake)
	deps.Confirm = confirm.ask

	if _, err := runDelete(t.Context(), deps, interactiveInvocation(false, "KEY-1", "KEY-2"), deleteOptions{parallelism: 4}); err != nil {
		t.Fatalf("runDelete: %v", err)
	}
	if confirm.asked != 1 {
		t.Fatalf("dialog drawn %d times, want exactly once for the whole run", confirm.asked)
	}
	for _, key := range []string{"KEY-1", "KEY-2"} {
		if !strings.Contains(confirm.last.Question, key) {
			t.Errorf("question %q does not name %s; a list is how a wrong key gets noticed", confirm.last.Question, key)
		}
	}
	if len(fake.deleted) != 2 {
		t.Errorf("deleted %v, want both keys", fake.deleted)
	}
}

// TestDeleteCaveatMentionsSubtasksOnlyWhenCascading: subtasks are issues
// the caller never named, so the dialog has to say when they are coming
// — and must not imply it when they are not.
func TestDeleteCaveatMentionsSubtasksOnlyWhenCascading(t *testing.T) {
	for _, tt := range []struct{ cascade, wantMention bool }{{true, true}, {false, false}} {
		fake := &fakeLifecycleClient{}
		confirm := &recordingConfirm{answer: true}
		deps := lifecycleDeps(fake)
		deps.Confirm = confirm.ask

		if _, err := runDelete(t.Context(), deps, interactiveInvocation(false, "KEY-1"), deleteOptions{deleteSubtasks: tt.cascade, parallelism: 4}); err != nil {
			t.Fatalf("runDelete: %v", err)
		}
		if got := strings.Contains(confirm.last.Caveat, "SUBTASKS"); got != tt.wantMention {
			t.Errorf("cascade=%v: caveat mentions subtasks = %v, want %v (%q)",
				tt.cascade, got, tt.wantMention, confirm.last.Caveat)
		}
		if fake.deleted[0].subtasks != tt.cascade {
			t.Errorf("cascade=%v was not passed to the seam", tt.cascade)
		}
	}
}

// TestDeleteRefusesNonInteractiveWithoutForce: the shared gate, reached
// from the third destructive command.
func TestDeleteRefusesNonInteractiveWithoutForce(t *testing.T) {
	fake := &fakeLifecycleClient{}
	deps := lifecycleDeps(fake)
	deps.Confirm = (&recordingConfirm{answer: true}).ask

	_, err := runDelete(t.Context(), deps, invocation(false, "KEY-1"), deleteOptions{parallelism: 4})
	if err == nil {
		t.Fatal("a non-interactive delete without --force must be refused")
	}
	var e *cli.InputError
	if !errors.As(err, &e) || e.Code != cli.CodeFlagRequired || e.Field != command.FlagForce {
		t.Errorf("error = %v, want a flag_required InputError naming --force", err)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("the refused run deleted %v", fake.deleted)
	}
}

// TestDeleteBadKeyFailsOnlyItsOwnEntry is the keyed rule applied to the
// most destructive command, and the instinct it argues against is
// strong: refusing the whole run on a bad key feels safer, and is not.
// The keys are independent, and the confirmation — which names every key
// before anything is sent — is what actually protects a typo.
func TestDeleteBadKeyFailsOnlyItsOwnEntry(t *testing.T) {
	keys := []string{"KEY-1", "KEY-404", "KEY-3"}
	fake := &fakeLifecycleClient{failures: map[string]error{
		"KEY-404": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Issue does not exist"}},
	}}

	out, err := runDelete(t.Context(), lifecycleDeps(fake), invocation(false, keys...), deleteOptions{force: true, parallelism: 4})
	if err != nil {
		t.Fatalf("a partly-successful delete must not fail the command: %v", err)
	}
	for i, want := range keys {
		if out.Results[i].Issue.Key != want {
			t.Fatalf("results[%d].issue.key = %q, want %q (argv order)", i, out.Results[i].Issue.Key, want)
		}
	}
	if !out.Results[0].OK || !out.Results[2].OK {
		t.Error("the keys that succeeded should report ok — those issues are gone")
	}
	if bad := out.Results[1]; bad.OK || bad.Err == nil || bad.Err.Code != "jira_not_found" {
		t.Errorf("failed key = %+v, want a jira_not_found entry", bad)
	}
}

func TestDeletePreviewNeitherAsksNorDeletes(t *testing.T) {
	fake := &fakeLifecycleClient{}
	confirm := &recordingConfirm{answer: false}
	deps := lifecycleDeps(fake)
	deps.Confirm = confirm.ask

	out, err := runDelete(t.Context(), deps, interactiveInvocation(true, "KEY-1"), deleteOptions{deleteSubtasks: true, parallelism: 4})
	if err != nil {
		t.Fatalf("runDelete: %v", err)
	}
	if confirm.asked != 0 {
		t.Error("the preview asked for confirmation; it destroys nothing")
	}
	if !out.DryRun || !out.DeleteSubtasks {
		t.Errorf("preview = %+v, want dry_run and delete_subtasks published", out)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("the preview deleted %v", fake.deleted)
	}
}

// TestDeleteCascadeAlias pins the hidden --cascade, which must set the
// same variable as the documented flag.
func TestDeleteCascadeAlias(t *testing.T) {
	cmd := newDelete(Deps{})
	alias := cmd.Flags().Lookup(flagCascade)
	if alias == nil {
		t.Fatal("--cascade is not registered; design §5 accepts jcli's spelling")
	}
	if !alias.Hidden {
		t.Error("--cascade is visible; a hidden alias adds no surface")
	}
	if cmd.Flags().Lookup(flagDeleteSubtasks) == nil {
		t.Error("--delete-subtasks is not registered")
	}
}

// --- issue clone ---

// cloneSource is a source issue carrying one of every field the copy
// policy claims to take, plus a server lifecycle field it must not carry.
//
// **The DECLARED fields are set as declared fields**, not stuffed into
// Extra, and that distinction is the point rather than pedantry:
// jira.IssueFields models the common ones as typed members and keeps
// only the rest in Extra. An earlier version of this fixture put
// everything in Extra, which made the copy look correct in a test while
// silently dropping the summary against a real response — the audit
// corpus is what caught it. A fixture that does not match the wire tests
// the author's belief rather than the code.
//
// `components` stays in Extra because that IS where it lives: it is one
// of the fields IssueFields does not declare, so the pair here exercises
// both halves of the merge a clone depends on.
func cloneSource() *jira.IssueBean {
	return &jira.IssueBean{
		Key: jira.NullableOf("KEY-1"),
		Fields: jira.NullableOf(jira.IssueFields{
			Summary:     jira.NullableOf("Original summary"),
			Description: jira.NullableOf(json.RawMessage(`{"type":"doc","version":1,"content":[]}`)),
			Issuetype:   jira.NullableOf(jira.IssueTypeDetails{Name: jira.NullableOf("Bug")}),
			Project:     jira.NullableOf(jira.ProjectDetails{Key: jira.NullableOf("KEY")}),
			Priority:    jira.NullableOf(jira.Priority{Name: jira.NullableOf("High")}),
			Labels:      jira.NullableOf([]string{"one", "two"}),
			// Declared fields include both copied assignment and excluded status.
			Status:   jira.NullableOf(jira.StatusDetails{Name: jira.NullableOf("Done")}),
			Assignee: jira.NullableOf(jira.UserDetails{AccountID: jira.NullableOf("someone-else")}),
			// Undeclared, so it rides Extra — the other half of the merge.
			Extra: map[string]json.RawMessage{
				"components": json.RawMessage(`[{"name":"api"}]`),
			},
		}),
	}
}

// TestCloneCarriesTheWorkAndNotTheHistory is the copy policy, and it is
// the one thing about this command a caller cannot discover without
// running it. Both halves are asserted: what carries, and what must not.
func TestCloneCarriesTheWorkAndNotTheHistory(t *testing.T) {
	fake := &fakeLifecycleClient{source: cloneSource()}
	var fields command.IssueFieldFlags

	out, err := runClone(t.Context(), lifecycleDeps(fake), invocation(false, "KEY-1"), cloneOptions{fields: &fields, parallelism: 4})
	if err != nil {
		t.Fatalf("runClone: %v", err)
	}
	if len(fake.created) != 1 {
		t.Fatalf("created %d issues, want one per source", len(fake.created))
	}
	got := fake.created[0].Fields
	for _, want := range []string{"summary", "description", "issuetype", "project", "priority", "labels", "components", "assignee"} {
		if _, ok := got[want]; !ok {
			t.Errorf("%q did not carry over; it describes the work", want)
		}
	}
	for _, forbidden := range []string{"status"} {
		if _, ok := got[forbidden]; ok {
			t.Errorf("%q carried over; it belongs to the source workflow", forbidden)
		}
	}
	if out.Results[0].Data.Clone == nil || out.Results[0].Data.Clone.Key != "NEW-1" {
		t.Errorf("clone = %+v, want the new issue's own key — the value this command exists to report", out.Results[0].Data.Clone)
	}
}

// TestCloneOverridesBeatTheSource pins the ORDER: copy first, override
// second. Applying them the other way round would make every override a
// no-op for any field the source happened to have — which is every
// field a clone carries.
func TestCloneOverridesBeatTheSource(t *testing.T) {
	fake := &fakeLifecycleClient{source: cloneSource()}
	cmd := &cobra.Command{Use: "clone"}
	var fields command.IssueFieldFlags
	command.AddIssueFieldFlags(cmd, &fields, command.CreateIssueVerb)
	if err := cmd.Flags().Set("summary", "Retry of the migration"); err != nil {
		t.Fatalf("set --summary: %v", err)
	}

	if _, err := runClone(t.Context(), lifecycleDeps(fake), invocation(false, "KEY-1"), cloneOptions{fields: &fields, parallelism: 4}); err != nil {
		t.Fatalf("runClone: %v", err)
	}
	var summary string
	if err := json.Unmarshal(fake.created[0].Fields["summary"], &summary); err != nil {
		t.Fatalf("decode the sent summary: %v", err)
	}
	if summary != "Retry of the migration" {
		t.Errorf("summary = %q, want the caller's override to replace the source's", summary)
	}
}

// TestClonePreviewReadsNothing: the fields a clone carries live on the
// source, so a preview that described them would read N issues. The rule
// holds and the preview says less.
func TestClonePreviewReadsNothing(t *testing.T) {
	asked := false
	deps := Deps{JiraClient: func() (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a bare dry run must not build a client")
	}}
	var fields command.IssueFieldFlags

	out, err := runClone(t.Context(), deps, invocation(true, "KEY-1", "KEY-2"), cloneOptions{fields: &fields, parallelism: 4})
	if err != nil {
		t.Fatalf("runClone: %v", err)
	}
	if asked {
		t.Error("the preview asked for a Jira client")
	}
	if len(out.Results) != 2 {
		t.Fatalf("got %d results, want one per source", len(out.Results))
	}
	for i, r := range out.Results {
		if r.Data == nil || r.Data.Clone != nil {
			t.Errorf("results[%d] reports a clone on a preview; nothing was created", i)
		}
	}
}

// TestCloneNotIdempotentPartialFailure: cloning twice makes two issues,
// so results[] naming which sources produced a clone is what makes a
// retry safe.
func TestCloneNotIdempotentPartialFailure(t *testing.T) {
	fake := &fakeLifecycleClient{source: cloneSource(), failures: map[string]error{
		"KEY-404": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Issue does not exist"}},
	}}
	var fields command.IssueFieldFlags

	out, err := runClone(t.Context(), lifecycleDeps(fake), invocation(false, "KEY-1", "KEY-404"), cloneOptions{fields: &fields, parallelism: 4})
	if err != nil {
		t.Fatalf("a partly-successful clone must not fail the command: %v", err)
	}
	if !out.Results[0].OK {
		t.Error("the source that cloned should report ok")
	}
	if out.Results[1].OK {
		t.Error("the source that could not be read should report failure")
	}
	if len(fake.created) != 1 {
		t.Errorf("created %d issues, want only the one whose source read", len(fake.created))
	}
}

// --- issue rank ---

// TestRankSendsOneRequestInArgvOrder is the property that makes this
// command different from every other keyed write: one request, and the
// array's order IS the ranking.
func TestRankSendsOneRequestInArgvOrder(t *testing.T) {
	fake := &fakeLifecycleClient{}

	out, err := runRank(t.Context(), lifecycleDeps(fake), invocation(false, "KEY-1", "KEY-2", "KEY-3"), "KEY-9", "")
	if err != nil {
		t.Fatalf("runRank: %v", err)
	}
	if len(fake.rankReqs) != 1 {
		t.Fatalf("made %d requests, want exactly one — the array's order is the ranking", len(fake.rankReqs))
	}
	req := fake.rankReqs[0]
	if strings.Join(req.Issues, ",") != "KEY-1,KEY-2,KEY-3" {
		t.Errorf("issues = %v, want argv order preserved", req.Issues)
	}
	if req.RankBeforeIssue != "KEY-9" || req.RankAfterIssue != "" {
		t.Errorf("anchor = before:%q after:%q, want only rankBeforeIssue set", req.RankBeforeIssue, req.RankAfterIssue)
	}
	if out.Position != envelope.RankPositionBefore || out.Anchor != "KEY-9" {
		t.Errorf("published position/anchor = %q/%q", out.Position, out.Anchor)
	}
}

func TestRankAfterUsesTheOtherAnchorField(t *testing.T) {
	fake := &fakeLifecycleClient{}

	out, err := runRank(t.Context(), lifecycleDeps(fake), invocation(false, "KEY-1"), "", "KEY-9")
	if err != nil {
		t.Fatalf("runRank: %v", err)
	}
	req := fake.rankReqs[0]
	if req.RankAfterIssue != "KEY-9" || req.RankBeforeIssue != "" {
		t.Errorf("anchor = before:%q after:%q, want only rankAfterIssue set", req.RankBeforeIssue, req.RankAfterIssue)
	}
	if out.Position != envelope.RankPositionAfter {
		t.Errorf("position = %q, want %q", out.Position, envelope.RankPositionAfter)
	}
}

func TestRequireOneAnchor(t *testing.T) {
	if err := requireOneAnchor("", ""); err == nil {
		t.Error("no anchor must be refused; there is no sensible default direction")
	}
	if err := requireOneAnchor("KEY-9", "KEY-8"); err == nil {
		t.Error("both anchors must be refused; an issue cannot go both sides")
	}
	if err := requireOneAnchor("KEY-9", ""); err != nil {
		t.Errorf("--before alone was refused: %v", err)
	}
	if err := requireOneAnchor("", "KEY-9"); err != nil {
		t.Errorf("--after alone was refused: %v", err)
	}
}

// TestRankTurnsOneResponseIntoPerKeyVerdicts covers all three cases of
// the 204/207 contract in one table, because they are one decision about
// how a single response becomes N results.
func TestRankTurnsOneResponseIntoPerKeyVerdicts(t *testing.T) {
	keys := []string{"KEY-1", "KEY-2"}
	tests := []struct {
		name    string
		write   core.RankWrite
		wantErr bool
		wantOK  []bool
	}{
		{
			name:   "204 moves everything",
			write:  core.RankWrite{StatusCode: 204},
			wantOK: []bool{true, true},
		},
		{
			name:   "207 with an attributed failure is a partial result",
			write:  core.RankWrite{StatusCode: 207, Outcomes: []core.RankOutcome{{IssueKey: "KEY-1", Status: 200}, {IssueKey: "KEY-2", Status: 403, Errors: []string{"not on a ranked board"}}}},
			wantOK: []bool{true, false},
		},
		{
			// The case the tempting shortcut gets wrong: a 207 whose body
			// the CLI could not read means an unknown subset did not move,
			// and reporting every key as successful would be the worst
			// answer available.
			name:    "207 with nothing attributable fails the command",
			write:   core.RankWrite{StatusCode: 207},
			wantErr: true,
		},
		{
			name:    "207 where every key failed fails the command",
			write:   core.RankWrite{StatusCode: 207, Outcomes: []core.RankOutcome{{IssueKey: "KEY-1", Status: 403, Errors: []string{"no"}}, {IssueKey: "KEY-2", Status: 403, Errors: []string{"no"}}}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := rankResults(keys, tt.write)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("rankResults = %+v, want a command failure", results)
				}
				return
			}
			if err != nil {
				t.Fatalf("rankResults: %v", err)
			}
			for i, want := range tt.wantOK {
				if results[i].OK != want {
					t.Errorf("results[%d].ok = %v, want %v", i, results[i].OK, want)
				}
			}
		})
	}
}

func TestRankPreviewSendsNothing(t *testing.T) {
	asked := false
	deps := Deps{JiraClient: func() (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a dry run must not build a client")
	}}

	out, err := runRank(t.Context(), deps, invocation(true, "KEY-1"), "KEY-9", "")
	if err != nil {
		t.Fatalf("runRank: %v", err)
	}
	if asked {
		t.Error("the preview asked for a Jira client")
	}
	if !out.DryRun || out.Anchor != "KEY-9" {
		t.Errorf("preview = %+v", out)
	}
}

// TestRankRegistersNoParallelism: there is no fan-out, so a
// --parallelism here would be a flag with nothing to act on.
func TestRankRegistersNoParallelism(t *testing.T) {
	fs := newRank(Deps{}).Flags()
	if fs.Lookup(command.FlagParallelism) != nil {
		t.Error("--parallelism is registered; rank sends one request, so there is nothing to widen")
	}
	for _, want := range []string{flagBefore, flagAfter, cli.FlagDryRun} {
		if fs.Lookup(want) == nil {
			t.Errorf("--%s is not registered", want)
		}
	}
}

// --- metadata and registration ---

func TestLifecycleMetadata(t *testing.T) {
	tests := []struct {
		op     string
		cmd    *cobra.Command
		sample string
	}{
		{"issue.delete", newDelete(Deps{}), "envelope.IssueDeleteOutput"},
		{"issue.clone", newClone(Deps{}), "envelope.IssueCloneOutput"},
		{"issue.rank", newRank(Deps{}), "envelope.IssueRankOutput"},
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
			if !m.WritesJira || !m.Keyed || !m.Identity {
				t.Errorf("WritesJira=%v Keyed=%v Identity=%v, want all true", m.WritesJira, m.Keyed, m.Identity)
			}
			reg, found := envelope.Registered(tt.op)
			if !found {
				t.Fatalf("%s has no envelope registration", tt.op)
			}
			if got := fmt.Sprintf("%T", reg.Sample); got != tt.sample {
				t.Errorf("registered %s, want %s", got, tt.sample)
			}
		})
	}
}

// TestCloneAcceptsTheAcliProjectSpelling: --to-project is acli's name for
// where the copy goes, and the design's §5 requires that invocation to
// work. It is a HIDDEN alias of --project here (see cloneProjectAliases
// for why that inversion is deliberate), which means nothing in help, the
// schema or completion would reveal it had stopped resolving.
//
// The assertion is on the CREATE REQUEST rather than on the flag, because
// registering the alias and folding it are two different things: an alias
// that parses but never reaches IssueFieldFlags.Project would accept the
// invocation, clone into the source's own project, and report success.
func TestCloneAcceptsTheAcliProjectSpelling(t *testing.T) {
	fake := &fakeLifecycleClient{source: cloneSource()}
	cmd := &cobra.Command{Use: "clone"}
	var fields command.IssueFieldFlags
	command.AddIssueFieldFlags(cmd, &fields, command.CreateIssueVerb)
	command.AddHiddenAliases(cmd, cloneProjectAliases)
	if err := cmd.Flags().Set("to-project", "OTHER"); err != nil {
		t.Fatalf("set --to-project: %v", err)
	}
	if err := command.ResolveHiddenAliases(cmd.Flags(), cloneProjectAliases); err != nil {
		t.Fatalf("resolve the hidden alias: %v", err)
	}

	if _, err := runClone(t.Context(), lifecycleDeps(fake), invocation(false, "KEY-1"), cloneOptions{fields: &fields, parallelism: 4}); err != nil {
		t.Fatalf("runClone: %v", err)
	}
	sent := string(fake.created[0].Fields["project"])
	if !strings.Contains(sent, "OTHER") {
		t.Errorf("project = %s, want the --to-project value to reach the create request", sent)
	}
}

// TestCloneRefusesDisagreeingProjectSpellings: the two spellings set one
// field, so two different values is a refusal rather than a silent pick.
//
// The message must name ONLY --project. --to-project is hidden, so an
// agent recovering from this error was never shown it and would hunt a
// flag absent from its schema (design §2).
func TestCloneRefusesDisagreeingProjectSpellings(t *testing.T) {
	cmd := &cobra.Command{Use: "clone"}
	var fields command.IssueFieldFlags
	command.AddIssueFieldFlags(cmd, &fields, command.CreateIssueVerb)
	command.AddHiddenAliases(cmd, cloneProjectAliases)
	for flag, value := range map[string]string{"project": "A", "to-project": "B"} {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatalf("set --%s: %v", flag, err)
		}
	}

	err := command.ResolveHiddenAliases(cmd.Flags(), cloneProjectAliases)
	if err == nil {
		t.Fatal("two different projects were accepted; one of them was silently discarded")
	}
	if strings.Contains(err.Error(), "to-project") {
		t.Errorf("refusal names the hidden alias: %q", err)
	}
	for _, want := range []string{"project", `"A"`, `"B"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %s", err, want)
		}
	}
}
