package issue

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
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

// The keyed MECHANICS — argv ordering under a concurrent fan-out,
// --parallelism, the severity ladder — are pinned once against `issue
// link list` (link_test.go), which shares this command's executor and
// its outcome/KeyedFailure split. What is pinned here is what this
// command DECIDES, and every one of those decisions is contract for the
// writes that copy it: that a preview composes without a client, that
// the preview and the live write send byte-identical payloads, that one
// payload reaches every key, and that a half-failed WRITE is a partial
// result rather than a command failure.

// fakeCommentAddClient is a core.JiraClient double for the write path.
// It records every (key, request) pair it was handed so a test can
// assert not only what came back but what was SENT — which is the only
// way to check the compose-once property.
type fakeCommentAddClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	// failures maps a key to the error AddComment returns for it; any
	// key not listed succeeds.
	failures map[string]error

	mu    sync.Mutex
	posts []postedComment
}

// postedComment is one recorded call.
type postedComment struct {
	key string
	req jira.AddCommentRequest
}

func (f *fakeCommentAddClient) AddComment(_ context.Context, key string, req jira.AddCommentRequest) (core.CommentWrite, error) {
	f.mu.Lock()
	f.posts = append(f.posts, postedComment{key: key, req: req})
	f.mu.Unlock()

	if err, ok := f.failures[key]; ok {
		return core.CommentWrite{}, err
	}
	return core.CommentWrite{Comment: &jira.Comment{
		ID:      jira.NullableOf("c-" + key),
		Created: jira.NullableOf("2026-08-13T10:00:00.000+0000"),
		Updated: jira.NullableOf("2026-08-13T10:00:00.000+0000"),
		Body:    jira.NullableOf(req.Body),
		Author: jira.NullableOf(jira.UserDetails{
			AccountID:   jira.NullableOf("acct-1"),
			DisplayName: jira.NullableOf("Ada Lovelace"),
		}),
	}}, nil
}

// postedKeys returns the recorded keys, sorted, so an assertion about
// WHICH keys were posted to does not depend on completion order.
func (f *fakeCommentAddClient) postedKeys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.posts))
	for _, p := range f.posts {
		out = append(out, p.key)
	}
	return out
}

func commentAddDeps(fake *fakeCommentAddClient) Deps {
	return Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}
}

// adfBody is the fixture document every test here sends. The ADF route
// is used rather than Markdown because it is the route that works in
// this build (see command.encodeMarkdown), and because being able to
// assert the sent bytes are IDENTICAL to the source file is exactly the
// property the lossless route promises.
const adfBody = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Deployed."}]}]}`

// bodyFromADF builds a BodyFlags bound through the real registrar and
// pointed at a temp file holding doc.
//
// Through the registrar, not by hand: BodyFlags carries its own flag
// spellings privately, and a hand-built one would leave them empty and
// make every refusal name "--".
func bodyFromADF(t *testing.T, doc string) *command.BodyFlags {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write body: %v", err)
	}
	cmd := &cobra.Command{Use: "add"}
	body := &command.BodyFlags{}
	command.AddBodyFlags(cmd, body)
	if err := cmd.Flags().Set("json-input", path); err != nil {
		t.Fatalf("set --json-input: %v", err)
	}
	return body
}

// invocation builds the Invocation a handler is given. DryRun is the
// only field these tests vary.
func invocation(dryRun bool, keys ...string) command.Invocation {
	return command.Invocation{Args: keys, DryRun: dryRun, Stdin: strings.NewReader("")}
}

// TestCommentAddDryRunNeverBuildsAClient is the load-bearing guard on
// "a dry run is local-only and never contacts Jira". It checks the
// property at the only place it can be checked structurally: the
// preview must return before it ever asks Deps for a client, because a
// preview that cannot obtain a client cannot reach the network no matter
// what it does next.
func TestCommentAddDryRunNeverBuildsAClient(t *testing.T) {
	asked := false
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a dry run must not build a client")
	}}

	out, err := runCommentAdd(t.Context(), deps, invocation(true, "KEY-123"), commentAddOptions{
		body:        bodyFromADF(t, adfBody),
		parallelism: 4,
	})
	if err != nil {
		t.Fatalf("runCommentAdd: %v", err)
	}
	if asked {
		t.Error("the preview asked for a Jira client; --dry-run must never reach the transport")
	}
	if !out.DryRun {
		t.Error("dry_run = false on a preview")
	}
}

// TestCommentAddDryRunPublishesTheComposedRequest: the preview's whole
// job is to answer "what would you send?", so the body, the route and
// the per-key entries all have to be there.
func TestCommentAddDryRunPublishesTheComposedRequest(t *testing.T) {
	keys := []string{"KEY-123", "KEY-124"}
	fake := &fakeCommentAddClient{}

	out, err := runCommentAdd(t.Context(), commentAddDeps(fake), invocation(true, keys...), commentAddOptions{
		body:        bodyFromADF(t, adfBody),
		visibility:  &jira.CommentVisibility{Type: "group", Value: "developers"},
		parallelism: 4,
	})
	if err != nil {
		t.Fatalf("runCommentAdd: %v", err)
	}

	if got := string(out.Body); got != adfBody {
		t.Errorf("preview body = %s, want the composed document %s", got, adfBody)
	}
	if out.BodyRoute != command.RouteADF {
		t.Errorf("body_route = %q, want %q", out.BodyRoute, command.RouteADF)
	}
	if out.Visibility == nil || out.Visibility.Value != "developers" {
		t.Errorf("visibility = %+v, want the composed group restriction", out.Visibility)
	}
	if len(out.Results) != len(keys) {
		t.Fatalf("got %d results, want one per key (%d)", len(out.Results), len(keys))
	}
	for i, r := range out.Results {
		if r.Issue.Key != keys[i] {
			t.Errorf("results[%d].issue.key = %q, want %q (argv order)", i, r.Issue.Key, keys[i])
		}
		if !r.OK {
			t.Errorf("results[%d].ok = false; a composed preview key succeeded", i)
		}
		if r.Data == nil {
			t.Fatalf("results[%d].data is nil; a successful key must carry data", i)
		}
		if r.Data.Comment != nil {
			t.Errorf("results[%d] carries a comment on a preview; nothing was created", i)
		}
	}
	if len(fake.postedKeys()) != 0 {
		t.Errorf("the preview posted to %v; it must send nothing", fake.postedKeys())
	}
}

// TestCommentAddPreviewAndLiveSendTheSameBody is the property that makes
// --dry-run worth running at all. If a preview composed down a separate
// branch it could drift from what a live run sends, and the caller's
// review of the preview would be worthless.
func TestCommentAddPreviewAndLiveSendTheSameBody(t *testing.T) {
	fake := &fakeCommentAddClient{}
	opts := func() commentAddOptions {
		return commentAddOptions{body: bodyFromADF(t, adfBody), parallelism: 4}
	}

	preview, err := runCommentAdd(t.Context(), commentAddDeps(fake), invocation(true, "KEY-123"), opts())
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	live, err := runCommentAdd(t.Context(), commentAddDeps(fake), invocation(false, "KEY-123"), opts())
	if err != nil {
		t.Fatalf("live: %v", err)
	}

	if string(preview.Body) != string(live.Body) {
		t.Errorf("preview body %s differs from live body %s", preview.Body, live.Body)
	}
	sent := fake.posts[0].req.Body
	if string(sent) != string(preview.Body) {
		t.Errorf("what was SENT (%s) differs from what the preview promised (%s)", sent, preview.Body)
	}
}

// TestCommentAddPostsOneIdenticalPayloadToEveryKey pins compose-once. A
// per-key difference in what was sent would make a per-key failure
// impossible to reason about.
func TestCommentAddPostsOneIdenticalPayloadToEveryKey(t *testing.T) {
	keys := []string{"KEY-123", "KEY-124", "KEY-125"}
	fake := &fakeCommentAddClient{}

	if _, err := runCommentAdd(t.Context(), commentAddDeps(fake), invocation(false, keys...), commentAddOptions{
		body:        bodyFromADF(t, adfBody),
		visibility:  &jira.CommentVisibility{Type: "role", Value: "Administrators"},
		parallelism: 4,
	}); err != nil {
		t.Fatalf("runCommentAdd: %v", err)
	}

	if len(fake.posts) != len(keys) {
		t.Fatalf("posted %d times, want %d", len(fake.posts), len(keys))
	}
	for _, p := range fake.posts {
		if string(p.req.Body) != adfBody {
			t.Errorf("%s got body %s, want the one composed document", p.key, p.req.Body)
		}
		if p.req.Visibility == nil || p.req.Visibility.Type != "role" || p.req.Visibility.Value != "Administrators" {
			t.Errorf("%s got visibility %+v, want the composed role restriction", p.key, p.req.Visibility)
		}
	}
}

// TestCommentAddLivePublishesTheCreatedComment: the POST's own response
// carries the created comment, so it is published unconditionally — no
// --verify, no second round trip. The id is the part that matters, since
// `comment edit` and `comment delete` both take it.
func TestCommentAddLivePublishesTheCreatedComment(t *testing.T) {
	fake := &fakeCommentAddClient{}

	out, err := runCommentAdd(t.Context(), commentAddDeps(fake), invocation(false, "KEY-123"), commentAddOptions{
		body:        bodyFromADF(t, adfBody),
		parallelism: 4,
	})
	if err != nil {
		t.Fatalf("runCommentAdd: %v", err)
	}
	if out.DryRun {
		t.Error("dry_run = true on a live write")
	}
	got := out.Results[0].Data.Comment
	if got == nil {
		t.Fatal("the created comment is absent; the POST response carries it, so it costs nothing to publish")
	}
	if got.ID != "c-KEY-123" {
		t.Errorf("comment id = %q, want the server's own id", got.ID)
	}
	if got.Author.DisplayName != "Ada Lovelace" {
		t.Errorf("author = %q, want the server's own author", got.Author.DisplayName)
	}
}

// TestCommentAddPartialFailureIsAPartialResult is THE write-semantics
// decision. The comments that posted are real and cannot be taken back,
// so the envelope must name exactly which keys got one — that list is
// what makes a safe retry possible. Failing the whole command would
// throw it away and leave the caller choosing between duplicates and
// guesswork.
func TestCommentAddPartialFailureIsAPartialResult(t *testing.T) {
	keys := []string{"KEY-123", "KEY-404", "KEY-125"}
	fake := &fakeCommentAddClient{failures: map[string]error{
		"KEY-404": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Issue does not exist"}},
	}}

	out, err := runCommentAdd(t.Context(), commentAddDeps(fake), invocation(false, keys...), commentAddOptions{
		body:        bodyFromADF(t, adfBody),
		parallelism: 4,
	})
	if err != nil {
		t.Fatalf("a partly-successful write must not fail the command: %v", err)
	}

	for i, want := range keys {
		if out.Results[i].Issue.Key != want {
			t.Fatalf("results[%d].issue.key = %q, want %q (argv order, not completion order)",
				i, out.Results[i].Issue.Key, want)
		}
	}
	if !out.Results[0].OK || !out.Results[2].OK {
		t.Error("the keys that succeeded should report ok")
	}
	bad := out.Results[1]
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

// TestCommentAddTotalFailureFailsTheCommand: with no partial result to
// hand back, an ok envelope carrying only failures would report success
// for a command that achieved nothing.
func TestCommentAddTotalFailureFailsTheCommand(t *testing.T) {
	keys := []string{"KEY-404", "KEY-405"}
	fake := &fakeCommentAddClient{failures: map[string]error{
		"KEY-404": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Issue does not exist"}},
		"KEY-405": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Issue does not exist"}},
	}}

	_, err := runCommentAdd(t.Context(), commentAddDeps(fake), invocation(false, keys...), commentAddOptions{
		body:        bodyFromADF(t, adfBody),
		parallelism: 4,
	})
	if err == nil {
		t.Fatal("a run where every key failed must fail the command")
	}
	// The ORIGINAL typed error comes back, so the exit code and
	// transport facts are the same ones a single bad key would produce.
	if got := cli.MapError(err); got.Code != "jira_not_found" {
		t.Errorf("code = %q, want jira_not_found preserved from the per-key error", got.Code)
	}
}

// TestCommentAddReadOnlyModeFailsEveryKey pins the interaction the
// severity ladder's own comment now records: read-only is enforced at
// the transport, so a keyed WRITE surfaces it per key — every key fails,
// no key succeeds, and KeyedFailure hands back the original error with
// its own code.
func TestCommentAddReadOnlyModeFailsEveryKey(t *testing.T) {
	keys := []string{"KEY-123", "KEY-124"}
	fake := &fakeCommentAddClient{failures: map[string]error{
		"KEY-123": adapterjira.ErrReadOnly,
		"KEY-124": adapterjira.ErrReadOnly,
	}}

	_, err := runCommentAdd(t.Context(), commentAddDeps(fake), invocation(false, keys...), commentAddOptions{
		body:        bodyFromADF(t, adfBody),
		parallelism: 4,
	})
	if err == nil {
		t.Fatal("read-only mode must fail the command, not report a successful no-op")
	}
	if got := cli.MapError(err); got.Code != cli.CodeReadOnlyBlocked {
		t.Errorf("code = %q, want %q", got.Code, cli.CodeReadOnlyBlocked)
	}
}

// TestCommentAddOmitsVisibilityWhenUnset: an absent restriction must be
// absent from the payload, not an empty object — Jira rejects a
// visibility naming no group.
func TestCommentAddOmitsVisibilityWhenUnset(t *testing.T) {
	fake := &fakeCommentAddClient{}

	out, err := runCommentAdd(t.Context(), commentAddDeps(fake), invocation(false, "KEY-123"), commentAddOptions{
		body:        bodyFromADF(t, adfBody),
		parallelism: 4,
	})
	if err != nil {
		t.Fatalf("runCommentAdd: %v", err)
	}
	if out.Visibility != nil {
		t.Errorf("visibility = %+v, want it absent", out.Visibility)
	}
	if fake.posts[0].req.Visibility != nil {
		t.Errorf("sent visibility = %+v, want it omitted from the payload", fake.posts[0].req.Visibility)
	}
}

func TestVisibilityConflictRefusesBothFlags(t *testing.T) {
	err := visibilityConflict("developers", "Administrators")
	if err == nil {
		t.Fatal("both visibility flags together must be refused; Jira allows one restriction")
	}
	var e *cli.InputError
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not a *cli.InputError", err)
	}
	if e.Code != cli.CodeFlagConflict {
		t.Errorf("code = %q, want %q", e.Code, cli.CodeFlagConflict)
	}
	for _, name := range []string{flagVisibilityGroup, flagVisibilityRole} {
		if !strings.Contains(e.Message, "--"+name) {
			t.Errorf("message %q does not name --%s", e.Message, name)
		}
	}
}

func TestVisibilityConflictAllowsEitherAlone(t *testing.T) {
	for _, tc := range [][2]string{{"developers", ""}, {"", "Administrators"}, {"", ""}} {
		if err := visibilityConflict(tc[0], tc[1]); err != nil {
			t.Errorf("visibilityConflict(%q, %q) = %v, want nil", tc[0], tc[1], err)
		}
	}
}

func TestVisibilityForBuildsTheWireShape(t *testing.T) {
	tests := []struct {
		name        string
		group, role string
		want        *jira.CommentVisibility
	}{
		{"group", "developers", "", &jira.CommentVisibility{Type: "group", Value: "developers"}},
		{"role", "", "Administrators", &jira.CommentVisibility{Type: "role", Value: "Administrators"}},
		{"neither", "", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := visibilityFor(tt.group, tt.role)
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("got %+v, want nil", got)
			case tt.want != nil && got == nil:
				t.Fatalf("got nil, want %+v", tt.want)
			case tt.want != nil && (*got != *tt.want):
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestCommentAddMutationFlagSurface pins which opt-in mutation flags this
// command carries and which it does not.
//
// --dry-run is mandatory for a mutation (New refuses one without it, so
// its absence would panic at construction).
//
// **--force was deliberately absent and now is not**, which is a change
// worth recording rather than a relaxation. The old reason was sound:
// adding a comment is not destructive, so there was no confirmation to
// skip. The bulk selectors added one — a --jql run does not name the
// issues it comments on, so it asks — and --force is what a script uses
// to answer it. The flag still has nothing to skip when keys are named,
// which TestCommentAddNamedKeysAreNeverConfirmed pins from the other
// side.
//
// --verify and --no-readback stay absent for their original reason,
// untouched by any of this: the POST answers with the whole comment, so
// there is no second round trip to gate or to suppress.
func TestCommentAddMutationFlagSurface(t *testing.T) {
	cmd := newCommentAdd(Deps{})
	fs := cmd.Flags()

	if fs.Lookup(cli.FlagDryRun) == nil {
		t.Error("--dry-run is not registered; every mutation carries it")
	}
	for _, absent := range []string{command.FlagVerify, command.FlagNoReadback} {
		if fs.Lookup(absent) != nil {
			t.Errorf("--%s is registered; this command's POST answers with the comment, so it gates no second round trip", absent)
		}
	}
	for _, want := range []string{
		command.FlagMarkdown, command.FlagMarkdownFile, command.FlagJSONInput,
		flagVisibilityGroup, flagVisibilityRole, command.FlagParallelism,
		command.FlagForce, command.FlagValidateRemote,
		command.FlagJQL, command.FlagFilter, command.FlagFromFile,
	} {
		if fs.Lookup(want) == nil {
			t.Errorf("--%s is not registered", want)
		}
	}
}

// TestCommentAddIsDeclaredAMutation guards the declaration New checks
// against. A command that writes Jira but forgot WritesJira would skip
// the dry-run requirement and the audit's mutation axes entirely.
func TestCommentAddIsDeclaredAMutation(t *testing.T) {
	metas, ok := command.MetaFor(newCommentAdd(Deps{}))
	if !ok || len(metas) == 0 {
		t.Fatal("no metadata recorded for issue.comment.add")
	}
	m := metas[0]
	if m.Op != "issue.comment.add" {
		t.Errorf("op = %q", m.Op)
	}
	if !m.WritesJira {
		t.Error("WritesJira is false; this command posts to Jira")
	}
	if !m.Keyed {
		t.Error("Keyed is false; this command returns results[]")
	}
	if m.Class() != command.ClassMutation {
		t.Errorf("class = %v, want a mutation", m.Class())
	}
}

// TestCommentAddEnvelopeRegistrationMatches pins the registry join
// command.New performs at construction: the registered Output type must
// be the one the handler returns, or the published schema promises a
// shape nothing can produce.
func TestCommentAddEnvelopeRegistrationMatches(t *testing.T) {
	reg, ok := envelope.Registered("issue.comment.add")
	if !ok {
		t.Fatal("issue.comment.add has no envelope registration")
	}
	if _, isType := reg.Sample.(envelope.IssueCommentAddOutput); !isType {
		t.Errorf("registered sample is %T, want envelope.IssueCommentAddOutput", reg.Sample)
	}
}
