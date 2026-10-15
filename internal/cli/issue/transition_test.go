package issue

import (
	"context"
	"errors"
	"net/http"
	"slices"
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

// What is pinned here is what this command DECIDES, on the same
// principle comment_add_test.go states: the keyed MECHANICS (argv
// ordering under a concurrent fan-out, --parallelism, the severity
// ladder) are pinned once against `issue link list`, whose executor this
// shares. Everything below is about the one thing no earlier command had
// to do — choose a request shape from a live read, and report honestly
// which shape it chose.

// fakeTransitionClient is a core.JiraClient double for the transition
// path. It records every call it was handed, because the assertions that
// matter here are about what was SENT and what was NOT: an atomic key
// must produce exactly one request, and a screenless key must produce
// two whose FIRST one carries no comment.
type fakeTransitionClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	// screens maps a key to whether its "Done" transition has a screen.
	// A key absent from the map 404s on the LIST, which is how a
	// nonexistent issue behaves.
	screens map[string]bool
	// transitionErr and commentErr force a failure on the write paths,
	// keyed by issue key.
	transitionErr map[string]error
	commentErr    map[string]error

	mu       sync.Mutex
	listed   []string
	moved    []movedIssue
	comments []string
}

// movedIssue is one recorded DoTransition call.
type movedIssue struct {
	key string
	req jira.TransitionRequest
}

func (f *fakeTransitionClient) GetTransitions(_ context.Context, key string) (core.TransitionRead, error) {
	f.mu.Lock()
	f.listed = append(f.listed, key)
	f.mu.Unlock()

	screened, ok := f.screens[key]
	if !ok {
		return core.TransitionRead{}, &adapterjira.APIError{
			StatusCode: http.StatusNotFound,
			Messages:   []string{"Issue does not exist"},
		}
	}
	return core.TransitionRead{Transitions: &jira.Transitions{
		Transitions: jira.NullableOf([]jira.IssueTransition{
			{
				ID:        jira.NullableOf("11"),
				Name:      jira.NullableOf("Start Progress"),
				HasScreen: jira.NullableOf(false),
				To:        jira.NullableOf(jira.StatusDetails{Name: jira.NullableOf("In Progress")}),
			},
			{
				ID:        jira.NullableOf("31"),
				Name:      jira.NullableOf("Finish"),
				HasScreen: jira.NullableOf(screened),
				// The transition's NAME and the status it leads to differ
				// on purpose: "Finish" → "Done" is what proves the
				// resolver matches on both, which a workflow where the two
				// agree could never show.
				To: jira.NullableOf(jira.StatusDetails{Name: jira.NullableOf("Done")}),
			},
		}),
	}}, nil
}

func (f *fakeTransitionClient) DoTransition(_ context.Context, key string, req jira.TransitionRequest) (core.TransitionWrite, error) {
	f.mu.Lock()
	f.moved = append(f.moved, movedIssue{key: key, req: req})
	f.mu.Unlock()
	if err, ok := f.transitionErr[key]; ok {
		return core.TransitionWrite{}, err
	}
	return core.TransitionWrite{StatusCode: http.StatusNoContent}, nil
}

func (f *fakeTransitionClient) AddComment(_ context.Context, key string, _ jira.AddCommentRequest) (core.CommentWrite, error) {
	f.mu.Lock()
	f.comments = append(f.comments, key)
	f.mu.Unlock()
	if err, ok := f.commentErr[key]; ok {
		return core.CommentWrite{}, err
	}
	return core.CommentWrite{Comment: &jira.Comment{ID: jira.NullableOf("c-" + key)}}, nil
}

// movedKeys and the two siblings below return the recorded keys sorted,
// so an assertion about WHICH keys were reached does not depend on the
// order a concurrent fan-out happened to finish in.
func (f *fakeTransitionClient) movedKeys() []string {
	return sortedCopy(f.mu.Lock, f.mu.Unlock, &f.moved, movedKey)
}
func (f *fakeTransitionClient) listedKeys() []string { return sortedStrings(&f.mu, f.listed) }
func (f *fakeTransitionClient) commentedKeys() []string {
	return sortedStrings(&f.mu, f.comments)
}

func movedKey(m movedIssue) string { return m.key }

// sortedStrings copies a recorded key slice under the lock and sorts it.
func sortedStrings(mu *sync.Mutex, keys []string) []string {
	mu.Lock()
	defer mu.Unlock()
	return slices.Sorted(slices.Values(slices.Clone(keys)))
}

// sortedCopy is sortedStrings for a slice of records rather than of
// strings. It takes the lock's two halves as functions because the
// recorded slice and the mutex live on the same struct and Go has no way
// to express "this field is guarded by that one" in a signature.
func sortedCopy[T any](lock, unlock func(), items *[]T, key func(T) string) []string {
	lock()
	defer unlock()
	out := make([]string, 0, len(*items))
	for _, it := range *items {
		out = append(out, key(it))
	}
	return slices.Sorted(slices.Values(out))
}

func transitionDeps(fake *fakeTransitionClient) Deps {
	return Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}
}

// drainWarnings takes the warnings this run raised.
func drainWarnings(t *testing.T) []envelope.Warning {
	t.Helper()
	return cli.TakeWarnings()
}

// isolateWarnings clears the process-wide warning slice before and after
// one test.
//
// Mandatory rather than tidy, and this was PROVEN rather than assumed: a
// first draft omitted it, and the very next test in the file failed
// holding two identical warnings — one of them raised by its
// predecessor. cli.AddWarning appends to package state that
// outlives a test (its contract is "ride the NEXT envelope written"), so
// a run that raises one and does not clear it hands that warning to
// whoever writes an envelope next, in this process or in production's
// single command run.
//
// It lives inside transitionOpts because every test here calls that and
// the isolation must not be something a new test can forget.
func isolateWarnings(t *testing.T) {
	t.Helper()
	cli.TakeWarnings()
	t.Cleanup(func() { cli.TakeWarnings() })
}

// transitionOpts builds the handler options for a run, with the comment
// supplied through the real body registrar (bodyFromADF, comment_add_test.go)
// or omitted entirely.
func transitionOpts(t *testing.T, withComment bool) transitionOptions {
	t.Helper()
	isolateWarnings(t)
	opts := transitionOptions{parallelism: 4}
	if withComment {
		opts.body = bodyFromADF(t, adfBody)
	} else {
		opts.body = &command.BodyFlags{}
	}
	return opts
}

// TestTransitionRoutesTheCommentByScreenState is the command's reason
// for existing, and the one test that would catch the naive
// implementation.
//
// Two keys, one workflow apiece: KEY-123's Finish transition has a
// screen and KEY-124's does not. The correct behavior is not "always
// atomic" or "always separate" — it is one of each, decided per key —
// and both halves of that are asserted against what was actually SENT,
// because the envelope could say anything.
func TestTransitionRoutesTheCommentByScreenState(t *testing.T) {
	fake := &fakeTransitionClient{screens: map[string]bool{"KEY-123": true, "KEY-124": false}}

	out, err := runTransition(t.Context(), transitionDeps(fake),
		invocation(false, "KEY-123", "KEY-124", "Done"), transitionOpts(t, true))
	if err != nil {
		t.Fatalf("runTransition: %v", err)
	}

	if got, want := len(out.Results), 2; got != want {
		t.Fatalf("got %d results, want %d", got, want)
	}
	if got := out.Results[0].Data.CommentPosted; got != envelope.CommentPostedAtomic {
		t.Errorf("KEY-123 comment_posted = %q, want %q — its transition has a screen", got, envelope.CommentPostedAtomic)
	}
	if got := out.Results[1].Data.CommentPosted; got != envelope.CommentPostedSeparate {
		t.Errorf("KEY-124 comment_posted = %q, want %q — its transition has none", got, envelope.CommentPostedSeparate)
	}

	// What was SENT, which is the half the envelope cannot fake.
	if got, want := len(fake.moved), 2; got != want {
		t.Fatalf("sent %d transition requests, want %d", got, want)
	}
	for _, m := range fake.moved {
		switch m.key {
		case "KEY-123":
			if m.req.Update == nil || len(m.req.Update.Comment) != 1 {
				t.Errorf("KEY-123's transition carried no comment; a screened transition must take it atomically")
			}
		case "KEY-124":
			if m.req.Update != nil {
				t.Errorf("KEY-124's transition carried a comment Jira would have silently discarded — this is the whole bug the command exists to avoid")
			}
		}
	}
	if got, want := fake.commentedKeys(), []string{"KEY-124"}; !slices.Equal(got, want) {
		t.Errorf("separate comments posted for %v, want %v — the screened key must not pay a second request", got, want)
	}
	// The separate path's comment is a resource of its own, so its id is
	// available; the atomic one's is not, because a 204 carries no body.
	if got := out.Results[1].Data.CommentID; got != "c-KEY-124" {
		t.Errorf("KEY-124 comment_id = %q, want the id its own POST returned", got)
	}
	if got := out.Results[0].Data.CommentID; got != "" {
		t.Errorf("KEY-123 comment_id = %q, want empty — the transition POST returns no body to learn one from", got)
	}
}

// TestTransitionWarnsWhenTheCommentIsNotAtomic pins the second half of
// design §3.1's requirement: the envelope reports the route in data AND
// the run says so out loud. Reporting only in data would leave a person
// at a terminal with no idea their comment did not ride the transition.
func TestTransitionWarnsWhenTheCommentIsNotAtomic(t *testing.T) {
	fake := &fakeTransitionClient{screens: map[string]bool{"KEY-123": true, "KEY-124": false}}

	if _, err := runTransition(t.Context(), transitionDeps(fake),
		invocation(false, "KEY-123", "KEY-124", "Done"), transitionOpts(t, true)); err != nil {
		t.Fatalf("runTransition: %v", err)
	}

	warnings := drainWarnings(t)
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want exactly one — only KEY-124 took the separate route: %+v", len(warnings), warnings)
	}
	if warnings[0].Type != envelope.WarningCommentNotAtomic {
		t.Errorf("warning type = %q, want %q", warnings[0].Type, envelope.WarningCommentNotAtomic)
	}
	if !strings.Contains(warnings[0].Message, "KEY-124") {
		t.Errorf("warning does not name the key it is about: %q", warnings[0].Message)
	}
}

// TestTransitionAtomicOnlyRefusesBeforeWriting pins the flag that keeps
// 0.15's behavior reachable — and pins WHEN it refuses. Refusing after
// the transition landed would leave the caller with a moved issue and an
// error, so the assertion that nothing was sent is the important one.
func TestTransitionAtomicOnlyRefusesBeforeWriting(t *testing.T) {
	fake := &fakeTransitionClient{screens: map[string]bool{"KEY-123": true, "KEY-124": false}}
	opts := transitionOpts(t, true)
	opts.atomicOnly = true

	out, err := runTransition(t.Context(), transitionDeps(fake),
		invocation(false, "KEY-123", "KEY-124", "Done"), opts)
	if err != nil {
		t.Fatalf("runTransition: %v — one key succeeded, so this is a partial result, not a failure", err)
	}

	if !out.Results[0].OK {
		t.Errorf("KEY-123 failed; its transition has a screen, so --atomic-only is satisfiable there")
	}
	if out.Results[1].OK {
		t.Fatal("KEY-124 succeeded; --atomic-only must refuse a key whose comment cannot ride the transition")
	}
	if got := out.Results[1].Err.Code; got != cli.CodeFlagNotApplicable {
		t.Errorf("KEY-124 error code = %q, want %q", got, cli.CodeFlagNotApplicable)
	}
	if got, want := fake.movedKeys(), []string{"KEY-123"}; !slices.Equal(got, want) {
		t.Errorf("transitioned %v, want %v — a refused key must not have been moved first", got, want)
	}
	if got := fake.commentedKeys(); len(got) != 0 {
		t.Errorf("posted comments for %v; --atomic-only refuses rather than falling back", got)
	}
}

// TestTransitionFieldsRefuseWithoutAScreen: unlike a comment, a
// resolution has no second endpoint that can carry it, so the choice is
// between refusing and moving the issue with the fields silently
// dropped. Dropping them would be the same defect as the comment bug,
// relocated to another flag.
func TestTransitionFieldsRefuseWithoutAScreen(t *testing.T) {
	fake := &fakeTransitionClient{screens: map[string]bool{"KEY-124": false}}
	opts := transitionOpts(t, false)
	opts.resolution = "Won't Do"

	_, err := runTransition(t.Context(), transitionDeps(fake),
		invocation(false, "KEY-124", "Done"), opts)
	if err == nil {
		t.Fatal("runTransition succeeded; the only key was refused, so the whole command fails")
	}
	if got := cli.MapError(err).Code; got != cli.CodeFlagNotApplicable {
		t.Errorf("error code = %q, want %q", got, cli.CodeFlagNotApplicable)
	}
	if got := fake.movedKeys(); len(got) != 0 {
		t.Errorf("transitioned %v; a refused key must not be moved with its fields dropped", got)
	}
}

// TestTransitionFieldsRideTheRequestWhereAScreenExists is the other half
// of the rule above: where the workflow can honor them, the fields ride
// the SAME request as the comment, so "close as Won't Do, reassign, and
// explain" costs one call.
func TestTransitionFieldsRideTheRequestWhereAScreenExists(t *testing.T) {
	fake := &fakeTransitionClient{screens: map[string]bool{"KEY-123": true}}
	opts := transitionOpts(t, true)
	opts.resolution = "Won't Do"
	opts.assignee = "5b10a2844c20165700ede21g"

	if _, err := runTransition(t.Context(), transitionDeps(fake),
		invocation(false, "KEY-123", "Done"), opts); err != nil {
		t.Fatalf("runTransition: %v", err)
	}

	if got, want := len(fake.moved), 1; got != want {
		t.Fatalf("sent %d requests, want %d — the fields and the comment share one", got, want)
	}
	req := fake.moved[0].req
	switch {
	case req.Fields == nil:
		t.Fatal("the request carried no fields")
	case req.Fields.Resolution == nil || req.Fields.Resolution.Name != "Won't Do":
		t.Errorf("resolution = %+v, want the composed value", req.Fields.Resolution)
	case req.Fields.Assignee == nil || req.Fields.Assignee.AccountID != "5b10a2844c20165700ede21g":
		t.Errorf("assignee = %+v, want the composed account id", req.Fields.Assignee)
	case req.Update == nil || len(req.Update.Comment) != 1:
		t.Errorf("the request carried no comment alongside its fields")
	}
}

// TestTransitionCommentFailureIsASuccessfulKey is the partial-failure
// shape the design settles, and the one an implementer is most likely to
// get wrong. The status DID change; reporting the key as failed would
// invite a retry that transitions an already-moved issue, possibly down
// a different edge of the workflow.
func TestTransitionCommentFailureIsASuccessfulKey(t *testing.T) {
	fake := &fakeTransitionClient{
		screens: map[string]bool{"KEY-124": false},
		commentErr: map[string]error{
			"KEY-124": &adapterjira.APIError{StatusCode: http.StatusForbidden, Messages: []string{"You do not have permission to comment"}},
		},
	}

	out, err := runTransition(t.Context(), transitionDeps(fake),
		invocation(false, "KEY-124", "Done"), transitionOpts(t, true))
	if err != nil {
		t.Fatalf("runTransition: %v — the transition landed, so the command succeeded", err)
	}

	r := out.Results[0]
	if !r.OK {
		t.Fatal("results[0].ok = false; the status changed, which is what the command was asked to do")
	}
	if got := r.Data.CommentPosted; got != envelope.CommentPostedFailed {
		t.Errorf("comment_posted = %q, want %q", got, envelope.CommentPostedFailed)
	}
	if r.Data.CommentError == "" {
		t.Error("comment_error is empty; a caller needs to know WHY the comment did not land")
	}
	if r.Data.CommentErrorCode != "jira_forbidden" || strings.Contains(r.Data.CommentError, "acknowledged the write") {
		t.Error("a refused comment lost its permission classification or fabricated a comment acknowledgement")
	}
	warnings := drainWarnings(t)
	if len(warnings) != 1 || warnings[0].Type != envelope.WarningCommentFailed {
		t.Fatalf("warnings = %+v, want exactly one %q", warnings, envelope.WarningCommentFailed)
	}
	// The message must steer away from a re-run, because a re-run would
	// transition again rather than only retry the comment.
	if !strings.Contains(warnings[0].Message, "comment add") {
		t.Errorf("warning does not point at the recovery that is actually safe: %q", warnings[0].Message)
	}
}

// TestTransitionBareDryRunNeverBuildsAClient is the load-bearing guard
// on "a preview is local-only". Checked structurally, at the only place
// it can be: the preview returns before it asks Deps for a client, so it
// cannot reach the network whatever it does next.
func TestTransitionBareDryRunNeverBuildsAClient(t *testing.T) {
	asked := false
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a bare dry run must not build a client")
	}}

	out, err := runTransition(t.Context(), deps, invocation(true, "KEY-123", "Done"), transitionOpts(t, true))
	if err != nil {
		t.Fatalf("runTransition: %v", err)
	}
	if asked {
		t.Error("the preview asked for a Jira client; a bare --dry-run must never reach the transport")
	}
	if !out.DryRun {
		t.Error("dry_run = false on a preview")
	}
	if got := out.Results[0].Data.CommentPlan; got != "" {
		t.Errorf("comment_plan = %q on a bare preview; the route needs a live read this run never made", got)
	}
	if out.Results[0].Data.Transition != nil {
		t.Error("the preview resolved a transition; resolving one needs the workflow, which it never read")
	}
	if string(out.Comment) != adfBody {
		t.Errorf("preview comment = %s, want the composed document — a preview must still say what it WOULD attach", out.Comment)
	}
}

// TestTransitionRemotePreviewReportsTheRouteAndWritesNothing pins the
// combination the design names: --dry-run --validate-remote reads the
// workflow, reports which route each key would take, and still sends no
// write. It is the branch the audit corpus cannot cover, because the
// corpus allows one recipe per op × category and the bare preview owns
// that slot.
func TestTransitionRemotePreviewReportsTheRouteAndWritesNothing(t *testing.T) {
	fake := &fakeTransitionClient{screens: map[string]bool{"KEY-123": true, "KEY-124": false}}
	opts := transitionOpts(t, true)
	opts.validateRemote = true

	out, err := runTransition(t.Context(), transitionDeps(fake),
		invocation(true, "KEY-123", "KEY-124", "Done"), opts)
	if err != nil {
		t.Fatalf("runTransition: %v", err)
	}

	if got := out.Results[0].Data.CommentPlan; got != envelope.CommentPlanAtomic {
		t.Errorf("KEY-123 comment_plan = %q, want %q", got, envelope.CommentPlanAtomic)
	}
	if got := out.Results[1].Data.CommentPlan; got != envelope.CommentPlanSeparate {
		t.Errorf("KEY-124 comment_plan = %q, want %q", got, envelope.CommentPlanSeparate)
	}
	for i, r := range out.Results {
		if r.Data.CommentPosted != "" {
			t.Errorf("results[%d].comment_posted = %q on a preview; nothing was posted", i, r.Data.CommentPosted)
		}
	}
	if got := fake.movedKeys(); len(got) != 0 {
		t.Errorf("the remote preview transitioned %v; it must write nothing", got)
	}
	if got := fake.commentedKeys(); len(got) != 0 {
		t.Errorf("the remote preview commented on %v; it must write nothing", got)
	}
	if got, want := fake.listedKeys(), []string{"KEY-123", "KEY-124"}; !slices.Equal(got, want) {
		t.Errorf("read workflows for %v, want %v — reporting the route is the whole point of --validate-remote", got, want)
	}
}

// TestTransitionRemotePreviewForeseesAnAtomicOnlyRefusal: a preview that
// reported a route while staying silent about a key --atomic-only would
// refuse outright would be answering the smaller question.
func TestTransitionRemotePreviewForeseesAnAtomicOnlyRefusal(t *testing.T) {
	fake := &fakeTransitionClient{screens: map[string]bool{"KEY-124": false}}
	opts := transitionOpts(t, true)
	opts.validateRemote = true
	opts.atomicOnly = true

	out, err := runTransition(t.Context(), transitionDeps(fake),
		invocation(true, "KEY-124", "Done"), opts)
	if err != nil {
		t.Fatalf("runTransition: %v", err)
	}
	if got := out.Results[0].Data.CommentPlan; got != envelope.CommentPlanRefused {
		t.Errorf("comment_plan = %q, want %q", got, envelope.CommentPlanRefused)
	}
}

// TestTransitionSkipsTheWorkflowReadForABareID pins the design's
// no-extra-round-trip rule at the only invocation where the read is
// genuinely optional: a transition id, and nothing asked for that
// depends on a screen.
func TestTransitionSkipsTheWorkflowReadForABareID(t *testing.T) {
	fake := &fakeTransitionClient{screens: map[string]bool{"KEY-123": true}}

	if _, err := runTransition(t.Context(), transitionDeps(fake),
		invocation(false, "KEY-123", "31"), transitionOpts(t, false)); err != nil {
		t.Fatalf("runTransition: %v", err)
	}
	if got := fake.listedKeys(); len(got) != 0 {
		t.Errorf("read the workflow for %v; a bare id needs no lookup and a bare transition must not pay for one", got)
	}
	if got, want := len(fake.moved), 1; got != want {
		t.Fatalf("sent %d requests, want %d", got, want)
	}
	if got := fake.moved[0].req.Transition.ID; got != "31" {
		t.Errorf("transition id = %q, want the one given verbatim", got)
	}
}

// TestTransitionReadsTheWorkflowWhenSomethingDependsOnAScreen is the
// other side of the rule: the same bare id DOES read the list once a
// comment is attached, because the id says nothing about screens.
func TestTransitionReadsTheWorkflowWhenSomethingDependsOnAScreen(t *testing.T) {
	fake := &fakeTransitionClient{screens: map[string]bool{"KEY-123": true}}

	if _, err := runTransition(t.Context(), transitionDeps(fake),
		invocation(false, "KEY-123", "31"), transitionOpts(t, true)); err != nil {
		t.Fatalf("runTransition: %v", err)
	}
	if got, want := fake.listedKeys(), []string{"KEY-123"}; !slices.Equal(got, want) {
		t.Errorf("read workflows for %v, want %v — screen state is unknowable from an id alone", got, want)
	}
}

// TestTransitionResolvesNameStatusAndID pins all three accepted
// spellings against ONE workflow whose transition name ("Finish") and
// target status ("Done") deliberately differ — a fixture where they
// agreed could not tell the two lookups apart.
func TestTransitionResolvesNameStatusAndID(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "transition name", want: "Finish"},
		{name: "target status name", want: "Done"},
		{name: "transition id", want: "31"},
		{name: "case-insensitive name", want: "fInIsH"},
		{name: "case-insensitive status", want: "done"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeTransitionClient{screens: map[string]bool{"KEY-123": true}}
			// A comment forces the workflow read even for the id case, so
			// every row resolves through the same lookup.
			out, err := runTransition(t.Context(), transitionDeps(fake),
				invocation(false, "KEY-123", tc.want), transitionOpts(t, true))
			if err != nil {
				t.Fatalf("runTransition(%q): %v", tc.want, err)
			}
			if got := out.Results[0].Data.Transition.ID; got != "31" {
				t.Errorf("resolved transition id = %q, want %q", got, "31")
			}
			if got := out.Results[0].Data.Transition.Status; got != "Done" {
				t.Errorf("resolved status = %q, want %q", got, "Done")
			}
		})
	}
}

// TestTransitionUnknownStatusNamesTheAvailableOnes: what a caller can
// move to depends on the issue's CURRENT status and their permissions,
// so the list is the answer to "then what can I do?" and it rides
// Suggestions where an agent can read it without parsing prose.
func TestTransitionUnknownStatusNamesTheAvailableOnes(t *testing.T) {
	fake := &fakeTransitionClient{screens: map[string]bool{"KEY-123": true}}

	_, err := runTransition(t.Context(), transitionDeps(fake),
		invocation(false, "KEY-123", "Wontfix"), transitionOpts(t, true))
	if err == nil {
		t.Fatal("runTransition succeeded on a status the workflow does not offer")
	}
	class := cli.MapError(err)
	if class.Code != cli.CodeArgValueInvalid {
		t.Errorf("code = %q, want %q", class.Code, cli.CodeArgValueInvalid)
	}
	if !slices.Contains(class.Suggestions, "Finish") {
		t.Errorf("suggestions = %v, want the transitions the issue actually offers", class.Suggestions)
	}
	if got := fake.movedKeys(); len(got) != 0 {
		t.Errorf("transitioned %v after failing to resolve the target", got)
	}
}

// TestTransitionKeyedResultsFollowArgvOrder: results[i] is the i-th key
// GIVEN, never the i-th to finish. The fan-out is concurrent, so an
// implementation that appended as calls completed would hand back one
// key's outcome under another's name — and it would do so
// intermittently, which is the worst way to find out.
func TestTransitionKeyedResultsFollowArgvOrder(t *testing.T) {
	keys := []string{"KEY-125", "KEY-123", "KEY-124"}
	fake := &fakeTransitionClient{screens: map[string]bool{
		"KEY-123": true, "KEY-124": false, "KEY-125": true,
	}}

	out, err := runTransition(t.Context(), transitionDeps(fake),
		invocation(false, append(slices.Clone(keys), "Done")...), transitionOpts(t, true))
	if err != nil {
		t.Fatalf("runTransition: %v", err)
	}
	drainWarnings(t)

	for i, key := range keys {
		if got := out.Results[i].Issue.Key; got != key {
			t.Errorf("results[%d].issue.key = %q, want %q", i, got, key)
		}
	}
}

// TestTransitionTotalFailureFailsTheCommand: a run where no key
// succeeded has no partial result to hand back, so it fails with the
// per-key error's own class rather than reporting an ok envelope that
// achieved nothing.
func TestTransitionTotalFailureFailsTheCommand(t *testing.T) {
	fake := &fakeTransitionClient{}

	_, err := runTransition(t.Context(), transitionDeps(fake),
		invocation(false, "KEY-404", "Done"), transitionOpts(t, false))
	if err == nil {
		t.Fatal("runTransition succeeded though its only key 404'd")
	}
	if got := cli.MapError(err).Code; got != "jira_not_found" {
		t.Errorf("code = %q, want %q", got, "jira_not_found")
	}
}

// TestTransitionPartialFailureKeepsTheSuccesses: a key that failed does
// not fail the others, and that matters more on a write than on a read —
// the issues that DID move are real, and results[] is the only list a
// caller can safely retry from.
func TestTransitionPartialFailureKeepsTheSuccesses(t *testing.T) {
	fake := &fakeTransitionClient{screens: map[string]bool{"KEY-123": true}}

	out, err := runTransition(t.Context(), transitionDeps(fake),
		invocation(false, "KEY-123", "KEY-404", "Done"), transitionOpts(t, false))
	if err != nil {
		t.Fatalf("runTransition: %v — one key moved, so this is a partial result", err)
	}
	if !out.Results[0].OK {
		t.Error("KEY-123 failed though its workflow was readable")
	}
	if out.Results[1].OK {
		t.Error("KEY-404 succeeded though it does not exist")
	}
	if got, want := fake.movedKeys(), []string{"KEY-123"}; !slices.Equal(got, want) {
		t.Errorf("transitioned %v, want %v", got, want)
	}
}

// TestSplitPositionals pins the grammar that resolves `KEY… [STATUS]`.
// The rule is the presence of --transition, never the shape of the last
// argument — a pattern-matcher would read `transition KEY-123 KEY-124`
// as a move to a status called KEY-124.
func TestSplitPositionals(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		flag       string
		wantKeys   []string
		wantStatus string
		wantCode   string
	}{
		{
			name:       "last positional is the status",
			args:       []string{"KEY-123", "Done"},
			wantKeys:   []string{"KEY-123"},
			wantStatus: "Done",
		},
		{
			name:       "several keys then the status",
			args:       []string{"KEY-123", "KEY-124", "In Progress"},
			wantKeys:   []string{"KEY-123", "KEY-124"},
			wantStatus: "In Progress",
		},
		{
			name:       "with the flag every positional is a key",
			args:       []string{"KEY-123", "KEY-124"},
			flag:       "Done",
			wantKeys:   []string{"KEY-123", "KEY-124"},
			wantStatus: "Done",
		},
		{
			name:     "one positional and no flag is a key with no status",
			args:     []string{"KEY-123"},
			wantCode: cli.CodeArgCountInvalid,
		},
		{
			name:     "the flag with no positional has no key",
			args:     nil,
			flag:     "Done",
			wantCode: cli.CodeArgCountInvalid,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// selectorGiven false: this table is the no-selector grammar. The
			// selector-aware half is
			// TestSplitPositionalsWithASelectorReadsALoneWordAsTheStatus.
			keys, status, err := splitPositionals(tc.args, tc.flag, false)
			if tc.wantCode != "" {
				if err == nil {
					t.Fatalf("splitPositionals(%v, %q) succeeded, want %s", tc.args, tc.flag, tc.wantCode)
				}
				if got := cli.MapError(err).Code; got != tc.wantCode {
					t.Errorf("code = %q, want %q", got, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitPositionals(%v, %q): %v", tc.args, tc.flag, err)
			}
			if !slices.Equal(keys, tc.wantKeys) {
				t.Errorf("keys = %v, want %v", keys, tc.wantKeys)
			}
			if status != tc.wantStatus {
				t.Errorf("status = %q, want %q", status, tc.wantStatus)
			}
		})
	}
}

// TestAtomicOnlyNeedsComment: a flag that does nothing is worse than an
// absent one, because an agent reading the schema will pass it and
// believe it bought a guarantee.
func TestAtomicOnlyNeedsComment(t *testing.T) {
	if err := atomicOnlyNeedsComment(true, &command.BodyFlags{}); err == nil {
		t.Error("--atomic-only with no comment was accepted; it would silently do nothing")
	} else if got := cli.MapError(err).Code; got != cli.CodeFlagNotApplicable {
		t.Errorf("code = %q, want %q", got, cli.CodeFlagNotApplicable)
	}
	if err := atomicOnlyNeedsComment(false, &command.BodyFlags{}); err != nil {
		t.Errorf("--atomic-only unset was refused: %v", err)
	}
}

// TestIsTransitionID pins the id-versus-name test that decides whether
// the workflow read can be skipped. It is total rather than a heuristic:
// Jira transition ids are decimal strings and a status named entirely in
// digits is not something a workflow can express.
func TestIsTransitionID(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"31", true},
		{"0", true},
		{"Done", false},
		{"In Progress", false},
		{"31a", false},
		{"", false},
		{"-1", false},
	} {
		if got := isTransitionID(tc.in); got != tc.want {
			t.Errorf("isTransitionID(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
