package issue

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// Everything below is about the CONFIRMATION, because that is the only
// thing this command has that its siblings do not. The delete itself is
// one call to one seam method and a 204; the decisions worth pinning are
// when the question is asked, when it is refused instead, and what
// happens to the delete in each case — a gate that could be bypassed by
// an invocation nobody tested is the whole risk of putting a gate there
// at all.

// fakeCommentDeleteClient is a core.JiraClient double for the delete
// path. It records every (key, id) pair so a test can assert that a run
// which should NOT have deleted anything did not.
type fakeCommentDeleteClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	// failures maps a comment id to the error DeleteComment returns for
	// it; any id not listed succeeds.
	failures map[string]error

	mu      sync.Mutex
	deleted []deletedComment
}

type deletedComment struct {
	key       string
	commentID string
}

func (f *fakeCommentDeleteClient) DeleteComment(_ context.Context, key, commentID string) (core.CommentDeleteWrite, error) {
	f.mu.Lock()
	f.deleted = append(f.deleted, deletedComment{key: key, commentID: commentID})
	f.mu.Unlock()

	if err, ok := f.failures[commentID]; ok {
		return core.CommentDeleteWrite{}, err
	}
	return core.CommentDeleteWrite{StatusCode: http.StatusNoContent}, nil
}

// recordingConfirm is a Deps.Confirm double: it records that it was
// asked and answers with a fixed verdict. Counting the asks is as
// important as the answer — "the prompt did not appear" is exactly what
// the --force and dry-run cases claim.
type recordingConfirm struct {
	asked  int
	last   prompt.Confirm
	answer bool
	err    error
}

func (r *recordingConfirm) ask(c prompt.Confirm) (bool, error) {
	r.asked++
	r.last = c
	return r.answer, r.err
}

func commentDeleteDeps(fake *fakeCommentDeleteClient, confirm *recordingConfirm) Deps {
	deps := Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}
	if confirm != nil {
		deps.Confirm = confirm.ask
	}
	return deps
}

// interactiveInvocation is the Invocation a person at a terminal
// produces. The shared invocation() helper leaves Interactive false,
// which is the AGENT case and the default every other test here wants —
// so the one case that needs a person says so explicitly.
func interactiveInvocation(dryRun bool, args ...string) command.Invocation {
	inv := invocation(dryRun, args...)
	inv.Interactive = true
	return inv
}

// TestCommentDeleteDryRunNeitherAsksNorBuildsAClient pins both halves of
// the preview's promise at once. A preview destroys nothing, so there is
// nothing to confirm — and it must not reach the transport either. The
// two are checked together because a preview that asked would be
// obnoxious while a preview that deleted would be catastrophic, and the
// same early return prevents both.
func TestCommentDeleteDryRunNeitherAsksNorBuildsAClient(t *testing.T) {
	asked := false
	confirm := &recordingConfirm{answer: false}
	deps := Deps{
		JiraClient: func() (core.JiraClient, error) {
			asked = true
			return nil, errors.New("a dry run must not build a client")
		},
		Confirm: confirm.ask,
	}

	out, err := runCommentDelete(t.Context(), deps, interactiveInvocation(true, "KEY-123", "10042"), false)
	if err != nil {
		t.Fatalf("runCommentDelete: %v", err)
	}
	if asked {
		t.Error("the preview asked for a Jira client; --dry-run must never reach the transport")
	}
	if confirm.asked != 0 {
		t.Error("the preview asked for confirmation; a preview destroys nothing, so there is nothing to confirm")
	}
	if !out.DryRun {
		t.Error("dry_run = false on a preview")
	}
	if out.Issue.Key != "KEY-123" || out.CommentID != "10042" {
		t.Errorf("preview identifies (%q, %q), want (KEY-123, 10042)", out.Issue.Key, out.CommentID)
	}
}

// TestCommentDeleteRefusesNonInteractiveWithoutForce is THE
// confirmation decision, and the one an agent meets. There is nobody to
// prompt, so the choice is between deleting anyway and refusing — and
// refusing is what keeps the destructive default from being "yes". The
// refusal must name --force, because that is the entire fix.
func TestCommentDeleteRefusesNonInteractiveWithoutForce(t *testing.T) {
	fake := &fakeCommentDeleteClient{}
	confirm := &recordingConfirm{answer: true}

	_, err := runCommentDelete(t.Context(), commentDeleteDeps(fake, confirm), invocation(false, "KEY-123", "10042"), false)
	if err == nil {
		t.Fatal("a non-interactive delete without --force must be refused, not performed")
	}
	var e *cli.InputError
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not a *cli.InputError", err)
	}
	if e.Code != cli.CodeFlagRequired {
		t.Errorf("code = %q, want %q — something the caller must add is missing", e.Code, cli.CodeFlagRequired)
	}
	if e.Field != command.FlagForce {
		t.Errorf("field = %q, want %q", e.Field, command.FlagForce)
	}
	if !strings.Contains(e.Message, "--"+command.FlagForce) {
		t.Errorf("message %q does not name --%s, which is the whole fix", e.Message, command.FlagForce)
	}
	if confirm.asked != 0 {
		t.Error("a non-interactive run drew a dialog; there is no terminal to draw it on")
	}
	if len(fake.deleted) != 0 {
		t.Errorf("the refused run deleted %v", fake.deleted)
	}
}

// TestCommentDeleteForceSkipsThePromptAndDeletes is the scriptable path
// — the one an agent, a script and a pipeline all take. --force must
// work with no terminal present, which is what makes the refusal above
// a real answer rather than a dead end.
func TestCommentDeleteForceSkipsThePromptAndDeletes(t *testing.T) {
	fake := &fakeCommentDeleteClient{}
	confirm := &recordingConfirm{answer: false} // would refuse, if it were ever consulted

	out, err := runCommentDelete(t.Context(), commentDeleteDeps(fake, confirm), invocation(false, "KEY-123", "10042"), true)
	if err != nil {
		t.Fatalf("runCommentDelete: %v", err)
	}
	if confirm.asked != 0 {
		t.Error("--force still drew the dialog; the flag's whole job is to skip it")
	}
	if out.DryRun {
		t.Error("dry_run = true on a live delete")
	}
	if len(fake.deleted) != 1 || fake.deleted[0] != (deletedComment{key: "KEY-123", commentID: "10042"}) {
		t.Errorf("deleted %v, want exactly [{KEY-123 10042}]", fake.deleted)
	}
}

// TestCommentDeleteAsksAtATerminalAndProceedsOnYes: with somebody to
// ask, the interrupt happens — and the question names the issue and the
// comment, which is everything this dialog can honestly identify.
func TestCommentDeleteAsksAtATerminalAndProceedsOnYes(t *testing.T) {
	fake := &fakeCommentDeleteClient{}
	confirm := &recordingConfirm{answer: true}

	if _, err := runCommentDelete(t.Context(), commentDeleteDeps(fake, confirm), interactiveInvocation(false, "KEY-123", "10042"), false); err != nil {
		t.Fatalf("runCommentDelete: %v", err)
	}
	if confirm.asked != 1 {
		t.Fatalf("dialog drawn %d times, want exactly once", confirm.asked)
	}
	if !strings.Contains(confirm.last.Question, "10042") || !strings.Contains(confirm.last.Question, "KEY-123") {
		t.Errorf("question %q names neither the comment nor the issue", confirm.last.Question)
	}
	if confirm.last.Caveat == "" {
		t.Error("the dialog carries no caveat; 'this cannot be undone' is the reason the question exists")
	}
	if len(fake.deleted) != 1 {
		t.Errorf("deleted %v, want exactly one comment after a yes", fake.deleted)
	}
}

// TestCommentDeleteAbortsOnNo: saying no is a CHOICE, not a failure of
// the command — it exits as canceled and, crucially, deletes nothing.
func TestCommentDeleteAbortsOnNo(t *testing.T) {
	fake := &fakeCommentDeleteClient{}
	confirm := &recordingConfirm{answer: false}

	_, err := runCommentDelete(t.Context(), commentDeleteDeps(fake, confirm), interactiveInvocation(false, "KEY-123", "10042"), false)
	if err == nil {
		t.Fatal("answering no must not report success")
	}
	if got := cli.MapError(err); got.Code != cli.CodeCanceled {
		t.Errorf("code = %q, want %q — declining is a choice, not a failure", got.Code, cli.CodeCanceled)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("the declined run deleted %v", fake.deleted)
	}
}

// TestCommentDeleteAbortsOnDialogEscape: closing the form is the same
// choice as answering no, which is how huh reports it and how
// `auth logout` already reads it.
func TestCommentDeleteAbortsOnDialogEscape(t *testing.T) {
	fake := &fakeCommentDeleteClient{}
	confirm := &recordingConfirm{err: huh.ErrUserAborted}

	_, err := runCommentDelete(t.Context(), commentDeleteDeps(fake, confirm), interactiveInvocation(false, "KEY-123", "10042"), false)
	if err == nil {
		t.Fatal("escaping the dialog must not report success")
	}
	if got := cli.MapError(err); got.Code != cli.CodeCanceled {
		t.Errorf("code = %q, want %q", got.Code, cli.CodeCanceled)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("the escaped run deleted %v", fake.deleted)
	}
}

// TestCommentDeleteRefusesWithNoDialogWired is the unreachable-from-
// production branch, pinned because its wrong answer is silent: a
// command built without a Confirm would otherwise delete without ever
// asking. The safe answer to "I was built without the thing that gates
// this" is to refuse.
func TestCommentDeleteRefusesWithNoDialogWired(t *testing.T) {
	fake := &fakeCommentDeleteClient{}

	_, err := runCommentDelete(t.Context(), commentDeleteDeps(fake, nil), interactiveInvocation(false, "KEY-123", "10042"), false)
	if err == nil {
		t.Fatal("a delete with no dialog wired must be refused, never performed unasked")
	}
	if got := cli.MapError(err); got.Code != cli.CodeFlagRequired {
		t.Errorf("code = %q, want %q", got.Code, cli.CodeFlagRequired)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("the refused run deleted %v", fake.deleted)
	}
}

// TestCommentDeleteFailureCarriesTheTaxonomy: an unknown comment id is
// the mistake this command's own argument makes possible.
func TestCommentDeleteFailureCarriesTheTaxonomy(t *testing.T) {
	fake := &fakeCommentDeleteClient{failures: map[string]error{
		"999999": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Comment does not exist"}},
	}}

	_, err := runCommentDelete(t.Context(), commentDeleteDeps(fake, nil), invocation(false, "KEY-123", "999999"), true)
	if err == nil {
		t.Fatal("an unknown comment id must fail the command")
	}
	if got := cli.MapError(err); got.Code != "jira_not_found" {
		t.Errorf("code = %q, want jira_not_found", got.Code)
	}
}

// TestCommentDeleteRegistersForceAndItsHiddenAlias pins the destructive
// surface. --force is documented; --yes is the acli spelling accepted
// without widening the contract, so it must be registered AND hidden —
// a visible alias would put two names for one flag in help, the schema
// and completions.
func TestCommentDeleteRegistersForceAndItsHiddenAlias(t *testing.T) {
	fs := newCommentDelete(Deps{}).Flags()

	if fs.Lookup(command.FlagForce) == nil {
		t.Error("--force is not registered; a destructive command needs the confirmation escape")
	}
	if fs.Lookup(cli.FlagDryRun) == nil {
		t.Error("--dry-run is not registered; every mutation carries it")
	}
	alias := fs.Lookup(command.FlagYes)
	if alias == nil {
		t.Fatal("--yes is not registered; it is the acli spelling design §5 accepts")
	}
	if !alias.Hidden {
		t.Error("--yes is visible; a hidden alias adds no surface, a visible one adds a second name for one flag")
	}
	for _, absent := range []string{command.FlagParallelism, command.FlagVerify, command.FlagNoReadback, command.FlagValidateRemote} {
		if fs.Lookup(absent) != nil {
			t.Errorf("--%s is registered; this command needs none of it", absent)
		}
	}
}

// TestCommentDeleteYesAliasSetsForce proves the alias actually does
// something. A hidden flag that parsed but never reached the gate would
// be the worst of both: accepted on the command line, then a
// confirmation refusal the caller cannot explain.
func TestCommentDeleteYesAliasSetsForce(t *testing.T) {
	var mut command.MutationFlags
	// A bare command, not newCommentDelete's: that one has already
	// registered --dry-run, and the registrar would redefine it. The
	// alias is a property of the REGISTRAR, so exercising it against a
	// fresh registration is what this test is about.
	cmd := &cobra.Command{Use: "delete"}
	command.AddMutationFlags(cmd, &mut, command.WithForce())
	if err := cmd.Flags().Set(command.FlagYes, "true"); err != nil {
		t.Fatalf("set --%s: %v", command.FlagYes, err)
	}
	if !mut.Force {
		t.Errorf("--%s did not set Force; the alias parses but does not reach the gate", command.FlagYes)
	}
}

func TestCommentDeleteIsAMutationAndNotKeyed(t *testing.T) {
	metas, ok := command.MetaFor(newCommentDelete(Deps{}))
	if !ok || len(metas) == 0 {
		t.Fatal("no metadata recorded for issue.comment.delete")
	}
	m := metas[0]
	if m.Op != "issue.comment.delete" {
		t.Errorf("op = %q", m.Op)
	}
	if !m.WritesJira {
		t.Error("WritesJira is false; this command writes to Jira")
	}
	if m.Keyed {
		t.Error("Keyed is true; this command addresses one comment on one issue")
	}
	if m.Passthrough {
		t.Error("Passthrough is true; a 204 carries no tenant data for a boundary to preserve")
	}
}

func TestCommentDeleteEnvelopeRegistrationMatches(t *testing.T) {
	reg, ok := envelope.Registered("issue.comment.delete")
	if !ok {
		t.Fatal("issue.comment.delete has no envelope registration")
	}
	if _, isType := reg.Sample.(envelope.IssueCommentDeleteOutput); !isType {
		t.Errorf("registered sample is %T, want envelope.IssueCommentDeleteOutput", reg.Sample)
	}
}
