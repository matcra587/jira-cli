package issue

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// The WRITE decisions this command shares with `issue comment add` — the
// preview builds no client, the preview and the live run send identical
// bytes, the taxonomy resolves a failure — are pinned there
// (comment_add_test.go) against the same body registrar, and repeating
// all of them here would test command.BodyFlags a second time. What is
// pinned below is what this command DECIDES and its sibling does not:
// that it is not keyed, that a body is mandatory because the endpoint
// replaces rather than patches, and that the three visibility states are
// three genuinely different requests.

// fakeCommentEditClient is a core.JiraClient double for the edit path,
// recording every (key, id, request) triple so a test can assert not
// only what came back but what was SENT — which is the only way to check
// the three-state visibility reaches the wire as three different
// payloads.
type fakeCommentEditClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	// failures maps a comment id to the error UpdateComment returns for
	// it; any id not listed succeeds.
	failures map[string]error

	mu    sync.Mutex
	edits []editedComment
}

// editedComment is one recorded call.
type editedComment struct {
	key       string
	commentID string
	req       jira.UpdateCommentRequest
	// notify is the notifyUsers the command asked for, recorded because
	// it rides the QUERY rather than req and so is invisible in the body.
	notify bool
}

func (f *fakeCommentEditClient) UpdateComment(_ context.Context, key, commentID string, req jira.UpdateCommentRequest, notify bool) (core.CommentWrite, error) {
	f.mu.Lock()
	f.edits = append(f.edits, editedComment{key: key, commentID: commentID, req: req, notify: notify})
	f.mu.Unlock()

	if err, ok := f.failures[commentID]; ok {
		return core.CommentWrite{}, err
	}
	return core.CommentWrite{Comment: &jira.Comment{
		ID:      jira.NullableOf(commentID),
		Created: jira.NullableOf("2026-08-13T10:00:00.000+0000"),
		Updated: jira.NullableOf("2026-08-13T11:30:00.000+0000"),
		Body:    jira.NullableOf(req.Body),
		Author: jira.NullableOf(jira.UserDetails{
			AccountID:   jira.NullableOf("acct-1"),
			DisplayName: jira.NullableOf("Ada Lovelace"),
		}),
	}}, nil
}

func commentEditDeps(fake *fakeCommentEditClient) Deps {
	return Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}
}

// TestCommentEditDryRunNeverBuildsAClient is the load-bearing guard on
// "a dry run is local-only and never contacts Jira", checked where it
// can be checked structurally: the preview must return before it ever
// asks Deps for a client, because a preview that cannot obtain one
// cannot reach the network no matter what it does next.
func TestCommentEditDryRunNeverBuildsAClient(t *testing.T) {
	asked := false
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a dry run must not build a client")
	}}

	out, err := runCommentEdit(t.Context(), deps, invocation(true, "KEY-123", "10042"), commentEditOptions{
		body: bodyFromADF(t, adfBody),
	})
	if err != nil {
		t.Fatalf("runCommentEdit: %v", err)
	}
	if asked {
		t.Error("the preview asked for a Jira client; --dry-run must never reach the transport")
	}
	if !out.DryRun {
		t.Error("dry_run = false on a preview")
	}
}

// TestCommentEditDryRunPublishesTheComposedRequest: the preview's whole
// job is to answer "what would you send, and to which comment?". The
// stored comment must be absent — nothing was stored.
func TestCommentEditDryRunPublishesTheComposedRequest(t *testing.T) {
	fake := &fakeCommentEditClient{}

	out, err := runCommentEdit(t.Context(), commentEditDeps(fake), invocation(true, "KEY-123", "10042"), commentEditOptions{
		body:       bodyFromADF(t, adfBody),
		visibility: &jira.CommentVisibility{Type: "group", Value: "developers"},
	})
	if err != nil {
		t.Fatalf("runCommentEdit: %v", err)
	}

	if out.Issue.Key != "KEY-123" {
		t.Errorf("issue.key = %q, want the key given", out.Issue.Key)
	}
	if out.CommentID != "10042" {
		t.Errorf("comment_id = %q, want the id given — a preview naming no comment says almost nothing", out.CommentID)
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
	if out.Comment != nil {
		t.Error("the preview carries a stored comment; nothing was stored")
	}
	if len(fake.edits) != 0 {
		t.Errorf("the preview sent %d requests; it must send none", len(fake.edits))
	}
}

// TestCommentEditPreviewAndLiveSendTheSameBody is the property that
// makes --dry-run worth running: if a preview composed down a separate
// branch it could drift from what a live run sends, and reviewing the
// preview would be worthless.
func TestCommentEditPreviewAndLiveSendTheSameBody(t *testing.T) {
	fake := &fakeCommentEditClient{}
	opts := func() commentEditOptions {
		return commentEditOptions{body: bodyFromADF(t, adfBody)}
	}

	preview, err := runCommentEdit(t.Context(), commentEditDeps(fake), invocation(true, "KEY-123", "10042"), opts())
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	live, err := runCommentEdit(t.Context(), commentEditDeps(fake), invocation(false, "KEY-123", "10042"), opts())
	if err != nil {
		t.Fatalf("live: %v", err)
	}

	if string(preview.Body) != string(live.Body) {
		t.Errorf("preview body %s differs from live body %s", preview.Body, live.Body)
	}
	sent := fake.edits[0].req.Body
	if string(sent) != string(preview.Body) {
		t.Errorf("what was SENT (%s) differs from what the preview promised (%s)", sent, preview.Body)
	}
}

// TestCommentEditLivePublishesTheStoredComment: the PUT's own 200
// response carries the comment, so it is published unconditionally — no
// --verify, no second round trip. Its `updated` timestamp is the one
// fact in the response the caller could not already know.
func TestCommentEditLivePublishesTheStoredComment(t *testing.T) {
	fake := &fakeCommentEditClient{}

	out, err := runCommentEdit(t.Context(), commentEditDeps(fake), invocation(false, "KEY-123", "10042"), commentEditOptions{
		body: bodyFromADF(t, adfBody),
	})
	if err != nil {
		t.Fatalf("runCommentEdit: %v", err)
	}
	if out.DryRun {
		t.Error("dry_run = true on a live write")
	}
	if out.Comment == nil {
		t.Fatal("the stored comment is absent; the PUT response carries it, so it costs nothing to publish")
	}
	if out.Comment.ID != "10042" {
		t.Errorf("comment id = %q, want the edited comment's own id", out.Comment.ID)
	}
	if out.Comment.Updated != "2026-08-13T11:30:00.000+0000" {
		t.Errorf("updated = %q, want the server's own timestamp", out.Comment.Updated)
	}
}

// TestCommentEditAddressesTheCommentItWasGiven pins the pair of
// positionals reaching the seam in the right order. Transposing them
// would edit a comment whose id is an issue key, which Jira answers with
// a 404 the caller would read as "that comment does not exist".
func TestCommentEditAddressesTheCommentItWasGiven(t *testing.T) {
	fake := &fakeCommentEditClient{}

	if _, err := runCommentEdit(t.Context(), commentEditDeps(fake), invocation(false, "KEY-123", "10042"), commentEditOptions{
		body: bodyFromADF(t, adfBody),
	}); err != nil {
		t.Fatalf("runCommentEdit: %v", err)
	}
	if len(fake.edits) != 1 {
		t.Fatalf("sent %d requests, want exactly one — this command is not keyed", len(fake.edits))
	}
	if got := fake.edits[0]; got.key != "KEY-123" || got.commentID != "10042" {
		t.Errorf("edited (key=%q, id=%q), want (KEY-123, 10042)", got.key, got.commentID)
	}
}

// TestCommentEditVisibilityIsThreeDistinctRequests is the decision
// jira.UpdateCommentRequest exists for. An absent visibility leaves the
// comment's current restriction alone, so "leave it" and "remove it"
// have to reach the wire as different payloads — and the envelope has to
// distinguish them too, or a caller cannot tell which request they
// previewed.
func TestCommentEditVisibilityIsThreeDistinctRequests(t *testing.T) {
	tests := []struct {
		name            string
		opts            commentEditOptions
		wantVisibility  bool
		wantClear       bool
		wantPublishedVi bool
	}{
		{
			name:      "leave the current restriction alone",
			opts:      commentEditOptions{},
			wantClear: false,
		},
		{
			name:            "apply a restriction",
			opts:            commentEditOptions{visibility: &jira.CommentVisibility{Type: "role", Value: "Administrators"}},
			wantVisibility:  true,
			wantPublishedVi: true,
		},
		{
			name:      "remove the restriction",
			opts:      commentEditOptions{clearVisibility: true},
			wantClear: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeCommentEditClient{}
			opts := tt.opts
			opts.body = bodyFromADF(t, adfBody)

			out, err := runCommentEdit(t.Context(), commentEditDeps(fake), invocation(false, "KEY-123", "10042"), opts)
			if err != nil {
				t.Fatalf("runCommentEdit: %v", err)
			}
			sent := fake.edits[0].req
			if (sent.Visibility != nil) != tt.wantVisibility {
				t.Errorf("sent visibility = %+v, want present=%v", sent.Visibility, tt.wantVisibility)
			}
			if sent.ClearVisibility != tt.wantClear {
				t.Errorf("sent ClearVisibility = %v, want %v", sent.ClearVisibility, tt.wantClear)
			}
			if (out.Visibility != nil) != tt.wantPublishedVi {
				t.Errorf("published visibility = %+v, want present=%v", out.Visibility, tt.wantPublishedVi)
			}
			if out.VisibilityCleared != tt.wantClear {
				t.Errorf("visibility_cleared = %v, want %v — it is the only field separating 'leave it' from 'remove it'",
					out.VisibilityCleared, tt.wantClear)
			}
		})
	}
}

// TestCommentEditFailureCarriesTheTaxonomy: an unknown comment id is the
// mistake this command's own argument makes possible, so its failure
// must arrive as the ordinary typed error with the shared code rather
// than as something a caller has to parse.
func TestCommentEditFailureCarriesTheTaxonomy(t *testing.T) {
	fake := &fakeCommentEditClient{failures: map[string]error{
		"999999": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Comment does not exist"}},
	}}

	_, err := runCommentEdit(t.Context(), commentEditDeps(fake), invocation(false, "KEY-123", "999999"), commentEditOptions{
		body: bodyFromADF(t, adfBody),
	})
	if err == nil {
		t.Fatal("an unknown comment id must fail the command")
	}
	if got := cli.MapError(err); got.Code != "jira_not_found" {
		t.Errorf("code = %q, want jira_not_found", got.Code)
	}
}

func TestClearVisibilityConflictRefusesEitherSetter(t *testing.T) {
	for _, tc := range []struct{ group, role string }{
		{"developers", ""},
		{"", "Administrators"},
	} {
		err := clearVisibilityConflict(true, tc.group, tc.role)
		if err == nil {
			t.Fatalf("--clear-visibility with group=%q role=%q must be refused; the request carries one answer", tc.group, tc.role)
		}
		var e *cli.InputError
		if !errors.As(err, &e) {
			t.Fatalf("error %v is not a *cli.InputError", err)
		}
		if e.Code != cli.CodeFlagConflict {
			t.Errorf("code = %q, want %q", e.Code, cli.CodeFlagConflict)
		}
		if !strings.Contains(e.Message, "--"+flagClearVisibility) {
			t.Errorf("message %q does not name --%s", e.Message, flagClearVisibility)
		}
	}
}

func TestClearVisibilityConflictAllowsEachAlone(t *testing.T) {
	for _, tc := range []struct {
		clear       bool
		group, role string
	}{
		{true, "", ""},
		{false, "developers", ""},
		{false, "", "Administrators"},
		{false, "", ""},
	} {
		if err := clearVisibilityConflict(tc.clear, tc.group, tc.role); err != nil {
			t.Errorf("clearVisibilityConflict(%v, %q, %q) = %v, want nil", tc.clear, tc.group, tc.role, err)
		}
	}
}

// TestCommentEditRequiresABody pins the consequence of the endpoint
// replacing rather than patching: there is no visibility-only edit,
// because a request with no body would ask Jira to store a comment with
// none. Refused in Validate, before any client is built.
func TestCommentEditRequiresABody(t *testing.T) {
	cmd := newCommentEdit(Deps{})
	cmd.SetArgs([]string{"KEY-123", "10042", "--clear-visibility"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true

	err := cmd.Execute()
	if err == nil {
		t.Fatal("an edit with no body must be refused; a PUT with no body stores a comment with none")
	}
	var e *cli.InputError
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not a *cli.InputError", err)
	}
	if e.Code != cli.CodeFlagRequired {
		t.Errorf("code = %q, want %q", e.Code, cli.CodeFlagRequired)
	}
}

// TestCommentEditRegistersItsOwnFlagsAndNotTheKeyedOnes pins both halves
// of the flag decision. --dry-run is mandatory for a mutation, the body
// trio and the visibility trio are this command's surface, and
// --parallelism is ABSENT because the command is not keyed — its
// presence would be a wiring error command.New refuses outright, so this
// also guards against a copy-paste from the keyed sibling.
func TestCommentEditRegistersItsOwnFlagsAndNotTheKeyedOnes(t *testing.T) {
	fs := newCommentEdit(Deps{}).Flags()

	for _, want := range []string{
		cli.FlagDryRun,
		command.FlagMarkdown, command.FlagMarkdownFile, command.FlagJSONInput,
		flagVisibilityGroup, flagVisibilityRole, flagClearVisibility,
	} {
		if fs.Lookup(want) == nil {
			t.Errorf("--%s is not registered", want)
		}
	}
	for _, absent := range []string{
		command.FlagParallelism,
		command.FlagForce, command.FlagVerify, command.FlagNoReadback, command.FlagValidateRemote,
	} {
		if fs.Lookup(absent) != nil {
			t.Errorf("--%s is registered; this command needs neither the keyed surface nor the opt-in mutation flags", absent)
		}
	}
}

// TestCommentEditIsAMutationAndNotKeyed guards the declaration
// command.New cross-checks. Keyed is the one that matters here: it gates
// the results[] contract and the audit's M3 axis, and a command
// declaring it while addressing one comment would promise a shape it
// never produces.
func TestCommentEditIsAMutationAndNotKeyed(t *testing.T) {
	metas, ok := command.MetaFor(newCommentEdit(Deps{}))
	if !ok || len(metas) == 0 {
		t.Fatal("no metadata recorded for issue.comment.edit")
	}
	m := metas[0]
	if m.Op != "issue.comment.edit" {
		t.Errorf("op = %q", m.Op)
	}
	if !m.WritesJira {
		t.Error("WritesJira is false; this command writes to Jira")
	}
	if m.Keyed {
		t.Error("Keyed is true; this command addresses one comment on one issue and returns a single result")
	}
	if m.Class() != command.ClassMutation {
		t.Errorf("class = %v, want a mutation", m.Class())
	}
}

func TestCommentEditEnvelopeRegistrationMatches(t *testing.T) {
	reg, ok := envelope.Registered("issue.comment.edit")
	if !ok {
		t.Fatal("issue.comment.edit has no envelope registration")
	}
	if _, isType := reg.Sample.(envelope.IssueCommentEditOutput); !isType {
		t.Errorf("registered sample is %T, want envelope.IssueCommentEditOutput", reg.Sample)
	}
}
