package issue

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

// What is pinned here is what `issue create` and `issue edit` DECIDE, and
// every one of those decisions is contract for the field-taking writes
// that copy them: that the three input routes compose into ONE request
// rather than layering, that a disagreement between them is refused
// naming both sides, that the collision check reads BOTH halves of a
// native payload, that a repeatable flag ADDS on edit and SETS on create,
// and that every local path returns before a client exists.
//
// The keyed MECHANICS — argv ordering under a concurrent fan-out,
// --parallelism, the severity ladder — are pinned once against `issue link
// list` and `issue comment add`, whose executor `edit` shares. They are
// not re-proved here.

// fakeWriteClient is a core.JiraClient double for the issue-write path.
// It records every request it was handed so a test can assert not only
// what came back but what was SENT — which is the only way to check the
// compose-once property and the add-versus-set decision.
type fakeWriteClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	// editFailures maps a key to the error EditIssue returns for it; any
	// key not listed succeeds.
	editFailures map[string]error
	// readbackErr, when set, is what GetIssue returns — the --verify path.
	readbackErr error
	// emptyAccountID makes CurrentUser answer with no account id, which is
	// the shape the @me resolver must refuse rather than compose.
	emptyAccountID bool

	mu      sync.Mutex
	created []jira.IssueWriteRequest
	edited  []editedIssue
	readds  []string
	// currentUserCalls counts /myself lookups, which is how the tests
	// prove the @me resolution is demand-driven and happens ONCE however
	// many flags or keys the run carries.
	currentUserCalls int
}

// fakeAccountID is what this fake resolves @me to.
const fakeAccountID = "5b10a2844c20165700ede21g"

type editedIssue struct {
	key string
	req jira.IssueWriteRequest
	// notify is the notifyUsers the command asked for, recorded because
	// it rides the QUERY rather than req and so is invisible in the body.
	notify bool
}

func (f *fakeWriteClient) CreateIssue(_ context.Context, req jira.IssueWriteRequest) (core.IssueCreateWrite, error) {
	f.mu.Lock()
	f.created = append(f.created, req)
	f.mu.Unlock()
	return core.IssueCreateWrite{StatusCode: 201, Issue: &jira.CreatedIssue{
		ID: "10101", Key: "KEY-77", Self: "https://example.atlassian.net/rest/api/3/issue/10101",
	}}, nil
}

func (f *fakeWriteClient) EditIssue(_ context.Context, key string, req jira.IssueWriteRequest, notify bool) (core.IssueEditWrite, error) {
	f.mu.Lock()
	f.edited = append(f.edited, editedIssue{key: key, req: req, notify: notify})
	f.mu.Unlock()
	if err, ok := f.editFailures[key]; ok {
		return core.IssueEditWrite{}, err
	}
	return core.IssueEditWrite{}, nil
}

func (f *fakeWriteClient) GetIssue(_ context.Context, key string, _ []string, _ ...string) (core.IssueRead, error) {
	f.mu.Lock()
	f.readds = append(f.readds, key)
	f.mu.Unlock()
	if f.readbackErr != nil {
		return core.IssueRead{}, f.readbackErr
	}
	return core.IssueRead{Issue: &jira.IssueBean{
		Key: jira.NullableOf(key),
		Fields: jira.NullableOf(jira.IssueFields{
			Summary: jira.NullableOf("Rotate the signing key"),
			Status:  jira.NullableOf(jira.StatusDetails{Name: jira.NullableOf("To Do")}),
			Updated: jira.NullableOf("2026-08-13T10:00:00.000+0000"),
		}),
	}}, nil
}

func (f *fakeWriteClient) CurrentUser(context.Context) (core.UserRead, error) {
	f.mu.Lock()
	f.currentUserCalls++
	empty := f.emptyAccountID
	f.mu.Unlock()
	if empty {
		return core.UserRead{User: &jira.UserDetails{}}, nil
	}
	return core.UserRead{User: &jira.UserDetails{
		AccountID:   jira.NullableOf(fakeAccountID),
		DisplayName: jira.NullableOf("Ada Lovelace"),
	}}, nil
}

func writeDeps(fake *fakeWriteClient) Deps {
	return Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}
}

// writeFlagsFor builds an issueWriteFlags bound through the REAL
// registrar and drives it with the flags a caller would type.
//
// Through the registrar and through a real cobra parse, never by setting
// struct fields: the bundle's alias folding, its conflict checks and its
// refusals all read the parsed flag set, and a hand-built struct would
// leave Changed() false everywhere and quietly skip every one of them.
func writeFlagsFor(t *testing.T, verb command.IssueWriteVerb, args ...string) *issueWriteFlags {
	t.Helper()
	var in issueWriteFlags
	cmd := &cobra.Command{Use: "write", RunE: func(*cobra.Command, []string) error { return nil }}
	in.register(cmd, verb)
	cmd.SetArgs(args)
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("parsing %v: %v", args, err)
	}
	return &in
}

// payloadFile writes a native payload fixture and returns its path.
func payloadFile(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	return path
}

// decodePayload reads a published payload back into a comparable map, so
// an assertion names the field it cares about rather than pinning a byte
// layout that key ordering could change.
func decodePayload(t *testing.T, raw json.RawMessage) map[string]map[string]any {
	t.Helper()
	var got map[string]map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("payload is not an object: %v (%s)", err, raw)
	}
	return got
}

// TestCreateDryRunNeverBuildsAClient is the load-bearing guard on "a dry
// run is local-only and never contacts Jira". It checks the property
// where it can be checked STRUCTURALLY: the preview must return before it
// asks Deps for a client, because a preview that never obtains one cannot
// reach the network whatever it does next.
func TestCreateDryRunNeverBuildsAClient(t *testing.T) {
	asked := false
	deps := Deps{JiraClient: func() (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a preview must not build a client")
	}}
	in := writeFlagsFor(t, command.CreateIssueVerb, "--project", "ENG", "--type", "Task", "--summary", "x")

	out, err := runCreate(t.Context(), deps, invocation(true), in, createOptions{})
	if err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if asked {
		t.Error("the preview asked for a Jira client; --dry-run must never reach the transport")
	}
	if out.Issue != nil {
		t.Errorf("preview published an issue identity %+v; no issue exists to name", out.Issue)
	}
	// The preview IS the discovery affordance — there is no
	// --generate-json — so publishing the payload is not a nicety here,
	// it is the only way a caller learns the shape to feed --json-input.
	if len(out.Payload) == 0 {
		t.Error("preview published no payload; a preview that shows nothing answers nothing")
	}
}

// TestEditDryRunNeverBuildsAClient is the keyed sibling of the guard
// above.
func TestEditDryRunNeverBuildsAClient(t *testing.T) {
	asked := false
	deps := Deps{JiraClient: func() (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a dry run must not build a client")
	}}
	in := writeFlagsFor(t, command.EditIssueVerb, "--summary", "x")

	out, err := runEdit(t.Context(), deps, invocation(true, "KEY-1", "KEY-2"), in, editOptions{parallelism: 4})
	if err != nil {
		t.Fatalf("runEdit: %v", err)
	}
	if asked {
		t.Error("the preview asked for a Jira client; --dry-run must never reach the transport")
	}
	if len(out.Results) != 2 {
		t.Fatalf("got %d results, want one per key", len(out.Results))
	}
	// The preview's entry shape must match a live run's, so a caller can
	// diff the two without them disagreeing about their structure.
	for i, r := range out.Results {
		if !r.OK || r.Data == nil {
			t.Errorf("results[%d] = %+v, want ok with an empty data object", i, r)
		}
	}
}

// TestPreviewAndLiveSendTheSameBytes is what makes --dry-run worth
// running: the caller reviews a preview, drops the flag, and gets the
// SAME request sent. A preview built down a separate branch could drift
// from the live one and nothing would notice.
func TestPreviewAndLiveSendTheSameBytes(t *testing.T) {
	args := []string{"--project", "ENG", "--type", "Task", "--summary", "Rotate the signing key", "--label", "ops"}

	preview, err := runCreate(t.Context(), writeDeps(&fakeWriteClient{}), invocation(true),
		writeFlagsFor(t, command.CreateIssueVerb, args...), createOptions{})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	fake := &fakeWriteClient{}
	live, err := runCreate(t.Context(), writeDeps(fake), invocation(false),
		writeFlagsFor(t, command.CreateIssueVerb, args...), createOptions{})
	if err != nil {
		t.Fatalf("live: %v", err)
	}

	if string(preview.Payload) != string(live.Payload) {
		t.Errorf("preview payload %s differs from the live one %s", preview.Payload, live.Payload)
	}
	if len(fake.created) != 1 {
		t.Fatalf("got %d creates, want exactly one", len(fake.created))
	}
	sent, err := fake.created[0].MarshalJSON()
	if err != nil {
		t.Fatalf("re-encoding the sent request: %v", err)
	}
	// The published bytes must be the bytes that went to the transport,
	// not a second encoding of the same struct that could differ in
	// escaping.
	if string(sent) != string(preview.Payload) {
		t.Errorf("the request sent (%s) is not what the preview published (%s)", sent, preview.Payload)
	}
}

// TestCreateSetsListsWhileEditAddsThem pins the add-versus-set decision,
// which is the one thing about these two commands that can surprise.
//
// On create the list IS the field, so it rides `fields`. On edit it ADDS,
// through `update`, because the design pairs --label with a separate
// --remove-label — a removal flag is only coherent alongside an additive
// one — and because replacing is the destructive reading: a caller who
// types one label and silently loses three has lost data the CLI never
// showed them.
func TestCreateSetsListsWhileEditAddsThem(t *testing.T) {
	create, err := runCreate(t.Context(), writeDeps(&fakeWriteClient{}), invocation(true),
		writeFlagsFor(t, command.CreateIssueVerb,
			"--project", "ENG", "--type", "Task", "--summary", "x", "--label", "ops", "--component", "api"), createOptions{})
	if err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	got := decodePayload(t, create.Payload)
	if _, addsInstead := got["update"]; addsInstead {
		t.Errorf("create composed an update half %v; at creation a list is the field's value, not an operation on it", got["update"])
	}
	if labels, ok := got["fields"]["labels"].([]any); !ok || len(labels) != 1 || labels[0] != "ops" {
		t.Errorf(`create fields.labels = %v, want ["ops"] set directly`, got["fields"]["labels"])
	}

	edit, err := runEdit(t.Context(), writeDeps(&fakeWriteClient{}), invocation(true, "KEY-1"),
		writeFlagsFor(t, command.EditIssueVerb,
			"--label", "ops", "--remove-label", "stale", "--component", "api"), editOptions{parallelism: 1})
	if err != nil {
		t.Fatalf("runEdit: %v", err)
	}
	got = decodePayload(t, edit.Payload)
	if _, replaced := got["fields"]["labels"]; replaced {
		t.Error("edit composed fields.labels, which REPLACES the whole list; the repeatable flags must add")
	}
	ops, ok := got["update"]["labels"].([]any)
	if !ok || len(ops) != 2 {
		t.Fatalf("edit update.labels = %v, want one add and one remove", got["update"]["labels"])
	}
	if add, _ := ops[0].(map[string]any); add["add"] != "ops" {
		t.Errorf("first label op = %v, want an add of %q", ops[0], "ops")
	}
	if rm, _ := ops[1].(map[string]any); rm["remove"] != "stale" {
		t.Errorf("second label op = %v, want a remove of %q", ops[1], "stale")
	}
	// Components add too, with the {"name": …} operand the field takes —
	// the asymmetry with a bare label string is Jira's, not ours.
	comps, ok := got["update"]["components"].([]any)
	if !ok || len(comps) != 1 {
		t.Fatalf("edit update.components = %v, want one add", got["update"]["components"])
	}
}

// TestPayloadCollisionIsRefusedInBothHalves is the check the design calls
// out by name, and the one a careless implementation gets half right.
//
// A field set through `update` is every bit as real as one in `fields`, so
// a `fields`-only check would let `update.priority[{"set":…}]` sit beside
// --priority and produce a request whose meaning depends on which half
// Jira applies last.
func TestPayloadCollisionIsRefusedInBothHalves(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{name: "fields half", payload: `{"fields":{"priority":{"name":"High"}}}`},
		{name: "update half", payload: `{"update":{"priority":[{"set":{"name":"High"}}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := writeFlagsFor(t, command.CreateIssueVerb,
				"--json-input", payloadFile(t, tc.payload), "--priority", "Low")

			_, err := runCreate(t.Context(), writeDeps(&fakeWriteClient{}), invocation(true), in, createOptions{})
			assertInputError(t, err, cli.CodeFlagConflict, "priority")
		})
	}
}

// TestPayloadAndFlagsMergeWhenTheyDoNotCollide is the other half of the
// rule, and it matters as much: a field flag beside --json-input is FINE
// until the two set the same field. A check that refused the combination
// outright would make the payload route unusable with any flag at all.
func TestPayloadAndFlagsMergeWhenTheyDoNotCollide(t *testing.T) {
	in := writeFlagsFor(t, command.CreateIssueVerb,
		"--json-input", payloadFile(t, `{"fields":{"project":{"key":"ENG"},"priority":{"name":"High"}}}`),
		"--type", "Task", "--summary", "Rotate the signing key")

	out, err := runCreate(t.Context(), writeDeps(&fakeWriteClient{}), invocation(true), in, createOptions{})
	if err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	fields := decodePayload(t, out.Payload)["fields"]
	for _, want := range []string{"project", "priority", "issuetype", "summary"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("composed payload lost %q; both routes must survive the merge (%s)", want, out.Payload)
		}
	}
	if out.DescriptionRoute != command.RouteADF {
		t.Errorf("description_route = %q, want %q when the payload route was taken", out.DescriptionRoute, command.RouteADF)
	}
}

// TestRouteConflictsAreRefused pins §2.1's blanket refusal: the routes are
// alternatives rather than layers, so two of them naming the body is a
// refusal instead of a merge.
func TestRouteConflictsAreRefused(t *testing.T) {
	payload := payloadFile(t, `{"fields":{"summary":"x"}}`)
	for _, tc := range []struct {
		name  string
		args  []string
		field string
	}{
		{
			name:  "payload beside inline markdown",
			args:  []string{"--json-input", payload, "--markdown", "hello"},
			field: command.FlagJSONInput,
		},
		{
			name:  "payload beside a markdown file",
			args:  []string{"--json-input", payload, "--markdown-file", payload},
			field: command.FlagJSONInput,
		},
		{
			// The alias folds FIRST, so --body reaches the conflict check
			// as --markdown. Without that ordering this combination slips
			// past a check that only sees canonical flags.
			name:  "payload beside the hidden --body alias",
			args:  []string{"--json-input", payload, "--body", "hello"},
			field: command.FlagJSONInput,
		},
		{
			name:  "both markdown routes",
			args:  []string{"--markdown", "hello", "--markdown-file", payload},
			field: command.FlagMarkdown,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := writeFlagsFor(t, command.CreateIssueVerb, tc.args...)
			assertInputErrorField(t, in.validate(), tc.field)
		})
	}
}

// TestEveryRepeatableFieldCanBeRemoved pins the symmetry: a value that can
// be ADDED by flag can be REMOVED by flag. The design authorized only
// --remove-label, and "addable by flag, removable only by hand-written
// JSON" is a hole a user falls into rather than a boundary they can see.
//
// It drives all four pairs in ONE invocation, which is what proves they
// share a code path rather than four copies that drifted — labels compose
// a bare string operand and the other three compose {"name": …}, and that
// asymmetry is Jira's.
func TestEveryRepeatableFieldCanBeRemoved(t *testing.T) {
	out, err := runEdit(t.Context(), writeDeps(&fakeWriteClient{}), invocation(true, "KEY-1"),
		writeFlagsFor(t, command.EditIssueVerb,
			"--remove-label", "stale",
			"--remove-component", "api",
			"--remove-fix-version", "2026.8",
			"--remove-affects-version", "2026.7"),
		editOptions{parallelism: 1})
	if err != nil {
		t.Fatalf("runEdit: %v", err)
	}
	update := decodePayload(t, out.Payload)["update"]

	for _, tc := range []struct {
		field string
		want  any
	}{
		{"labels", "stale"},
		{"components", map[string]any{"name": "api"}},
		{"fixVersions", map[string]any{"name": "2026.8"}},
		{"versions", map[string]any{"name": "2026.7"}},
	} {
		ops, ok := update[tc.field].([]any)
		if !ok || len(ops) != 1 {
			t.Errorf("update.%s = %v, want one remove op", tc.field, update[tc.field])
			continue
		}
		op, _ := ops[0].(map[string]any)
		if _, adding := op["add"]; adding {
			t.Errorf("update.%s composed an add for a --remove-* flag: %v", tc.field, op)
		}
		if got := op["remove"]; !reflect.DeepEqual(got, tc.want) {
			t.Errorf("update.%s remove operand = %#v, want %#v", tc.field, got, tc.want)
		}
	}
}

// TestEveryRepeatableFieldRefusesAValueInBothHalves is the conflict rule
// generalised past labels: the same refusal, for the same reason, on all
// four pairs. Before the three removal flags landed only labels could
// contradict themselves; a rule that stayed label-only would have let the
// other three compose an ambiguous operation list.
func TestEveryRepeatableFieldRefusesAValueInBothHalves(t *testing.T) {
	for _, tc := range []struct{ add, remove, value, field string }{
		{"--label", "--remove-label", "ops", command.FlagLabel},
		{"--component", "--remove-component", "api", command.FlagComponent},
		{"--fix-version", "--remove-fix-version", "2026.8", command.FlagFixVersion},
		{"--affects-version", "--remove-affects-version", "2026.7", command.FlagAffectsVersion},
	} {
		t.Run(tc.field, func(t *testing.T) {
			in := writeFlagsFor(t, command.EditIssueVerb, tc.add, tc.value, tc.remove, tc.value)
			assertInputError(t, in.validate(), cli.CodeFlagConflict, tc.field)
		})
	}
}

// TestCreateHasNoRemovalFlags: there is nothing to remove from an issue
// that does not exist yet, so registering them would offer a caller a flag
// that could only ever be a no-op.
func TestCreateHasNoRemovalFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "create"}
	var in issueWriteFlags
	in.register(cmd, command.CreateIssueVerb)

	for _, name := range []string{
		command.FlagRemoveLabel, command.FlagRemoveComponent,
		command.FlagRemoveFixVersion, command.FlagRemoveAffectsVersion,
		command.FlagUnassign, command.FlagRemainingEstimate,
	} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("create registered --%s, which has nothing to act on before the issue exists", name)
		}
	}
	// The counterpart: create DOES own --project, which edit does not,
	// because on an existing issue that is a move rather than a field
	// write.
	if cmd.Flags().Lookup(command.FlagProject) == nil {
		t.Error("create did not register --project")
	}
}

// TestEditRefusesAnAssigneeContradiction and the label one below pin the
// two refusals internal to the field bundle: flags that would write
// contradictory things into ONE Jira field.
func TestEditRefusesAnAssigneeContradiction(t *testing.T) {
	in := writeFlagsFor(t, command.EditIssueVerb, "--assignee", "acct-1", "--unassign")
	assertInputError(t, in.validate(), cli.CodeFlagConflict, command.FlagAssignee)
}

// TestEditRefusesALabelInBothLists: naming one value as both an add and a
// remove leaves the outcome depending on which verb Jira applies last,
// which the API does not promise.
func TestEditRefusesALabelInBothLists(t *testing.T) {
	in := writeFlagsFor(t, command.EditIssueVerb, "--label", "ops", "--remove-label", "ops")
	assertInputError(t, in.validate(), cli.CodeFlagConflict, command.FlagLabel)
}

// TestUnassignSendsAnExplicitNull: an ABSENT assignee means "do not touch
// it" and a null one means "clear it", so this is the one place the
// difference between absent and null is the whole point of the request.
func TestUnassignSendsAnExplicitNull(t *testing.T) {
	out, err := runEdit(t.Context(), writeDeps(&fakeWriteClient{}), invocation(true, "KEY-1"),
		writeFlagsFor(t, command.EditIssueVerb, "--unassign"), editOptions{parallelism: 1})
	if err != nil {
		t.Fatalf("runEdit: %v", err)
	}
	fields, ok := decodePayload(t, out.Payload)["fields"]
	if !ok {
		t.Fatalf("payload has no fields half: %s", out.Payload)
	}
	value, present := fields["assignee"]
	if !present {
		t.Fatalf("payload omits assignee entirely; an absent assignee tells Jira to leave it alone (%s)", out.Payload)
	}
	if value != nil {
		t.Errorf("assignee = %v, want an explicit null", value)
	}
}

// TestEstimatesShareOneKey: there is no top-level fields.originalEstimate,
// so both estimate flags nest inside `timetracking` — and composing them
// in one call is what stops the second from overwriting the first's whole
// object.
func TestEstimatesShareOneKey(t *testing.T) {
	out, err := runEdit(t.Context(), writeDeps(&fakeWriteClient{}), invocation(true, "KEY-1"),
		writeFlagsFor(t, command.EditIssueVerb, "--original-estimate", "2d", "--remaining-estimate", "4h"),
		editOptions{parallelism: 1})
	if err != nil {
		t.Fatalf("runEdit: %v", err)
	}
	tracking, ok := decodePayload(t, out.Payload)["fields"]["timetracking"].(map[string]any)
	if !ok {
		t.Fatalf("payload has no timetracking object: %s", out.Payload)
	}
	if tracking["originalEstimate"] != "2d" || tracking["remainingEstimate"] != "4h" {
		t.Errorf("timetracking = %v, want both estimates in one object", tracking)
	}
}

// TestCreateRequiresTheThreeJiraDemands: refusing locally turns a round
// trip and a server error about a half-written payload into an immediate
// message naming the flag to add. It reads the COMPOSED request, which is
// what lets --json-input satisfy the requirement on its own.
func TestCreateRequiresTheThreeJiraDemands(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		field string
	}{
		{name: "no project", args: []string{"--type", "Task", "--summary", "x"}, field: command.FlagProject},
		{name: "no type", args: []string{"--project", "ENG", "--summary", "x"}, field: command.FlagIssueType},
		{name: "no summary", args: []string{"--project", "ENG", "--type", "Task"}, field: command.FlagSummary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := writeFlagsFor(t, command.CreateIssueVerb, tc.args...)
			_, err := runCreate(t.Context(), writeDeps(&fakeWriteClient{}), invocation(true), in, createOptions{})
			assertInputError(t, err, cli.CodeFlagRequired, tc.field)
		})
	}

	t.Run("satisfied entirely by the payload", func(t *testing.T) {
		in := writeFlagsFor(t, command.CreateIssueVerb, "--json-input",
			payloadFile(t, `{"fields":{"project":{"key":"ENG"},"issuetype":{"name":"Task"},"summary":"x"}}`))
		if _, err := runCreate(t.Context(), writeDeps(&fakeWriteClient{}), invocation(true), in, createOptions{}); err != nil {
			t.Errorf("runCreate refused a payload carrying all three required fields: %v", err)
		}
	})

	t.Run("the preview is checked too", func(t *testing.T) {
		// A preview that reported a payload Jira is certain to reject
		// would defeat the point of previewing, so the check runs on both
		// paths rather than only on the live one.
		in := writeFlagsFor(t, command.CreateIssueVerb, "--type", "Task", "--summary", "x")
		_, err := runCreate(t.Context(), writeDeps(&fakeWriteClient{}), invocation(true), in, createOptions{})
		assertInputError(t, err, cli.CodeFlagRequired, command.FlagProject)
	})
}

// TestEditRefusesAnEmptyChange: an edit that changes nothing is a caller
// mistake, not a no-op worth a round trip per key.
func TestEditRefusesAnEmptyChange(t *testing.T) {
	in := writeFlagsFor(t, command.EditIssueVerb)
	_, err := runEdit(t.Context(), writeDeps(&fakeWriteClient{}), invocation(true, "KEY-1"), in, editOptions{parallelism: 1})
	assertInputError(t, err, cli.CodeFlagRequired, command.FlagSummary)
}

// TestCreatePublishesTheIdentityJiraAssigned: create is the one command
// where a caller reads data.issue to LEARN the key rather than to confirm
// the one they typed, so all three members have to survive.
func TestCreatePublishesTheIdentityJiraAssigned(t *testing.T) {
	fake := &fakeWriteClient{}
	out, err := runCreate(t.Context(), writeDeps(fake), invocation(false),
		writeFlagsFor(t, command.CreateIssueVerb, "--project", "ENG", "--type", "Task", "--summary", "x"), createOptions{})
	if err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if out.Issue == nil || out.Issue.Key != "KEY-77" || out.Issue.ID != "10101" {
		t.Errorf("published identity = %+v, want the id and key Jira assigned", out.Issue)
	}
	if len(fake.readds) != 0 {
		t.Errorf("create read the issue back without --verify (%v); the readback is a second request a caller opts into", fake.readds)
	}
}

// TestVerifyReadsBackAndAFailedReadbackDoesNotFailTheWrite pins both
// halves of --verify, and the second half is the one that matters: the
// issue EXISTS once Jira said 201, so a failed confirmation must never
// read as a failed create. Returning the error would discard the key and
// leave a caller who retries with two issues.
func TestVerifyReadsBackAndAFailedReadbackDoesNotFailTheWrite(t *testing.T) {
	t.Run("readback succeeds", func(t *testing.T) {
		fake := &fakeWriteClient{}
		out, err := runCreate(t.Context(), writeDeps(fake), invocation(false),
			writeFlagsFor(t, command.CreateIssueVerb, "--project", "ENG", "--type", "Task", "--summary", "x"), createOptions{verify: true})
		if err != nil {
			t.Fatalf("runCreate: %v", err)
		}
		if out.Verified == nil || out.Verified.Status != "To Do" {
			t.Fatalf("verified = %+v, want the stored status", out.Verified)
		}
		if out.VerifyFailed {
			t.Error("verify_failed is true on a readback that answered")
		}
		if len(fake.readds) != 1 {
			t.Errorf("got %d readbacks, want exactly one", len(fake.readds))
		}
	})

	t.Run("readback fails", func(t *testing.T) {
		fake := &fakeWriteClient{readbackErr: errors.New("boom")}
		out, err := runCreate(t.Context(), writeDeps(fake), invocation(false),
			writeFlagsFor(t, command.CreateIssueVerb, "--project", "ENG", "--type", "Task", "--summary", "x"), createOptions{verify: true})
		if err != nil {
			t.Fatalf("a failed readback failed the whole create: %v", err)
		}
		if out.Issue == nil || out.Issue.Key != "KEY-77" {
			t.Error("a failed readback discarded the created key, which is the one thing a caller cannot recover")
		}
		if !out.VerifyFailed || out.Verified != nil {
			t.Errorf("verified=%+v verify_failed=%v, want the failure published", out.Verified, out.VerifyFailed)
		}
	})
}

// TestEditHalfFailureIsAPartialResult: the edits that already applied are
// real and cannot be taken back, so the only safe way to retry is to know
// exactly which keys succeeded — which is what data.results[] is. A run
// where some keys applied is ok, exit 0, with the failures inline.
func TestEditHalfFailureIsAPartialResult(t *testing.T) {
	fake := &fakeWriteClient{editFailures: map[string]error{"KEY-2": errors.New("nope")}}
	out, err := runEdit(t.Context(), writeDeps(fake), invocation(false, "KEY-1", "KEY-2", "KEY-3"),
		writeFlagsFor(t, command.EditIssueVerb, "--summary", "x"), editOptions{parallelism: 4})
	if err != nil {
		t.Fatalf("a half-failed run failed the command; it must be a partial result: %v", err)
	}
	if len(out.Results) != 3 {
		t.Fatalf("got %d results, want one per key", len(out.Results))
	}
	// argv order, never completion order.
	for i, wantKey := range []string{"KEY-1", "KEY-2", "KEY-3"} {
		if out.Results[i].Issue.Key != wantKey {
			t.Errorf("results[%d] is %q, want %q — entries must follow argv", i, out.Results[i].Issue.Key, wantKey)
		}
	}
	if out.Results[1].OK || out.Results[1].Err == nil {
		t.Errorf("results[1] = %+v, want the failed key carrying an error", out.Results[1])
	}
	for _, i := range []int{0, 2} {
		if !out.Results[i].OK {
			t.Errorf("results[%d] failed; one key's failure must not affect the others", i)
		}
	}
}

// TestEditTotalFailureFailsTheCommand: when NO key succeeded there is no
// partial result to hand back, and an ok envelope carrying nothing but
// failures would report success for a command that achieved nothing.
func TestEditTotalFailureFailsTheCommand(t *testing.T) {
	fake := &fakeWriteClient{editFailures: map[string]error{
		"KEY-1": errors.New("nope"), "KEY-2": errors.New("nope"),
	}}
	_, err := runEdit(t.Context(), writeDeps(fake), invocation(false, "KEY-1", "KEY-2"),
		writeFlagsFor(t, command.EditIssueVerb, "--summary", "x"), editOptions{parallelism: 4})
	if err == nil {
		t.Fatal("a run where every key failed reported success")
	}
}

// TestOnePayloadReachesEveryKey is the compose-once property on the keyed
// path: all N issues get a byte-identical request, so a per-key failure
// can never be a per-key difference in what was sent.
func TestOnePayloadReachesEveryKey(t *testing.T) {
	fake := &fakeWriteClient{}
	out, err := runEdit(t.Context(), writeDeps(fake), invocation(false, "KEY-1", "KEY-2", "KEY-3"),
		writeFlagsFor(t, command.EditIssueVerb, "--summary", "x", "--label", "ops"), editOptions{parallelism: 4})
	if err != nil {
		t.Fatalf("runEdit: %v", err)
	}
	if len(fake.edited) != 3 {
		t.Fatalf("got %d edits, want one per key", len(fake.edited))
	}
	for _, sent := range fake.edited {
		got, err := sent.req.MarshalJSON()
		if err != nil {
			t.Fatalf("re-encoding %s's request: %v", sent.key, err)
		}
		if string(got) != string(out.Payload) {
			t.Errorf("%s was sent %s, want the one composed payload %s", sent.key, got, out.Payload)
		}
	}
}

// TestMalformedPayloadIsTheCallersError: a bad document must fail as the
// input it is — with the flag named and a validation exit — rather than
// traveling to Jira and returning a 400 about a payload the caller never
// wrote.
func TestMalformedPayloadIsTheCallersError(t *testing.T) {
	in := writeFlagsFor(t, command.CreateIssueVerb, "--json-input", payloadFile(t, `{"fields":`))
	_, err := runCreate(t.Context(), writeDeps(&fakeWriteClient{}), invocation(true), in, createOptions{})
	assertInputError(t, err, cli.CodeFlagValueInvalid, command.FlagJSONInput)
}

// TestHiddenAliasesFoldIntoTheirCanonicalFlag: an acli- or jcli-trained
// invocation must WORK rather than error at someone who typed a
// reasonable thing (design §5).
func TestHiddenAliasesFoldIntoTheirCanonicalFlag(t *testing.T) {
	t.Run("--labels means --label", func(t *testing.T) {
		in := writeFlagsFor(t, command.EditIssueVerb, "--labels", "ops")
		if err := in.validate(); err != nil {
			t.Fatalf("validate: %v", err)
		}
		if got := in.fields.Labels; len(got) != 1 || got[0] != "ops" {
			t.Errorf("labels = %v, want the alias folded into --label", got)
		}
	})

	t.Run("--remove-assignee means --unassign", func(t *testing.T) {
		in := writeFlagsFor(t, command.EditIssueVerb, "--remove-assignee=true")
		if err := in.validate(); err != nil {
			t.Fatalf("validate: %v", err)
		}
		if !in.fields.Unassign {
			t.Error("--remove-assignee did not fold into --unassign")
		}
	})

	t.Run("--from-json means --json-input", func(t *testing.T) {
		path := payloadFile(t, `{"fields":{"summary":"x"}}`)
		in := writeFlagsFor(t, command.EditIssueVerb, "--from-json", path)
		if err := in.validate(); err != nil {
			t.Fatalf("validate: %v", err)
		}
		if in.jsonInput != path {
			t.Errorf("json-input = %q, want the alias folded in (%q)", in.jsonInput, path)
		}
	})
}

// The `@me` contract, decided once for every account-id flag and pinned
// here rather than per command: a live run resolves it, a bare preview
// leaves it and warns, and `--validate-remote` is the opt-in that makes a
// preview resolve it for real.

// TestMeResolvesOnTheLivePath: the sentinel must never reach the wire, and
// one lookup must serve however many flags asked for it.
func TestMeResolvesOnTheLivePath(t *testing.T) {
	fake := &fakeWriteClient{}
	out, err := runCreate(t.Context(), writeDeps(fake), invocation(false),
		writeFlagsFor(t, command.CreateIssueVerb,
			"--project", "ENG", "--type", "Task", "--summary", "x",
			"--assignee", command.MeSentinel, "--reporter", command.MeSentinel),
		createOptions{})
	if err != nil {
		t.Fatalf("runCreate: %v", err)
	}

	fields := decodePayload(t, out.Payload)["fields"]
	for _, name := range []string{"assignee", "reporter"} {
		ref, ok := fields[name].(map[string]any)
		if !ok {
			t.Fatalf("payload %s = %v, want an accountId object", name, fields[name])
		}
		if ref["accountId"] != fakeAccountID {
			t.Errorf("%s.accountId = %v, want the resolved id %q", name, ref["accountId"], fakeAccountID)
		}
	}
	// ONE lookup for two sentinels. Two identical round trips for one
	// question is the regression this asserts against.
	if fake.currentUserCalls != 1 {
		t.Errorf("CurrentUser called %d times, want exactly 1 however many flags hold the sentinel", fake.currentUserCalls)
	}
}

// TestMeIsLeftUnresolvedInABarePreview is the load-bearing half: --dry-run
// is local-only, so it cannot know the account id and must not invent one.
// It publishes the literal sentinel and a warning saying what a live run
// will do instead.
func TestMeIsLeftUnresolvedInABarePreview(t *testing.T) {
	cli.TakeWarnings() // drain anything a prior test left uncollected
	t.Cleanup(func() { cli.TakeWarnings() })

	asked := false
	deps := Deps{JiraClient: func() (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a bare preview must not build a client")
	}}

	out, err := runCreate(t.Context(), deps, invocation(true),
		writeFlagsFor(t, command.CreateIssueVerb,
			"--project", "ENG", "--type", "Task", "--summary", "x", "--assignee", command.MeSentinel),
		createOptions{})
	if err != nil {
		t.Fatalf("runCreate: %v", err)
	}
	if asked {
		t.Error("a bare preview built a client to resolve @me; --dry-run must never contact Jira")
	}

	ref, _ := decodePayload(t, out.Payload)["fields"]["assignee"].(map[string]any)
	if ref["accountId"] != command.MeSentinel {
		t.Errorf("preview assignee = %v, want the literal %q left in place", ref["accountId"], command.MeSentinel)
	}

	// Silently leaving the sentinel would be worse than either
	// alternative: the payload shown is not the payload that would be
	// sent, and nothing would point at the difference.
	warnings := cli.TakeWarnings()
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want exactly one naming the unresolved flag: %+v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0].Message, "--"+command.FlagAssignee) {
		t.Errorf("warning does not name the flag it is about: %q", warnings[0].Message)
	}
	if warnings[0].Lossy {
		t.Error("warning is marked lossy; nothing was dropped, the value resolves when sent")
	}
}

// TestValidateRemoteResolvesMeInAPreview: the opt-in that buys a real
// lookup on a preview, reusing transition's split rather than inventing a
// second one. Nothing is written either way — only the payload changes.
func TestValidateRemoteResolvesMeInAPreview(t *testing.T) {
	cli.TakeWarnings()
	t.Cleanup(func() { cli.TakeWarnings() })

	fake := &fakeWriteClient{}
	out, err := runCreate(t.Context(), writeDeps(fake), invocation(true),
		writeFlagsFor(t, command.CreateIssueVerb,
			"--project", "ENG", "--type", "Task", "--summary", "x", "--assignee", command.MeSentinel),
		createOptions{validateRemote: true})
	if err != nil {
		t.Fatalf("runCreate: %v", err)
	}

	ref, _ := decodePayload(t, out.Payload)["fields"]["assignee"].(map[string]any)
	if ref["accountId"] != fakeAccountID {
		t.Errorf("assignee = %v, want the resolved id under --validate-remote", ref["accountId"])
	}
	if fake.currentUserCalls != 1 {
		t.Errorf("CurrentUser called %d times, want 1", fake.currentUserCalls)
	}
	// A resolved preview has nothing to warn about.
	if w := cli.TakeWarnings(); len(w) != 0 {
		t.Errorf("resolved preview still warned: %+v", w)
	}
	// Still a preview: it resolved a value, it did not create anything.
	if len(fake.created) != 0 {
		t.Errorf("--validate-remote created %d issues; it buys a READ, never a write", len(fake.created))
	}
	if out.Issue != nil {
		t.Errorf("preview published an issue identity %+v", out.Issue)
	}
}

// TestNoSentinelCostsNoLookup: a command that never mentions @me must not
// pay for a /myself round trip, on either path.
func TestNoSentinelCostsNoLookup(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts createOptions
		dry  bool
	}{
		{name: "live", opts: createOptions{}},
		{name: "preview with validate-remote", opts: createOptions{validateRemote: true}, dry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeWriteClient{}
			if _, err := runCreate(t.Context(), writeDeps(fake), invocation(tc.dry),
				writeFlagsFor(t, command.CreateIssueVerb,
					"--project", "ENG", "--type", "Task", "--summary", "x", "--assignee", "acct-1"),
				tc.opts); err != nil {
				t.Fatalf("runCreate: %v", err)
			}
			if fake.currentUserCalls != 0 {
				t.Errorf("CurrentUser called %d times with no sentinel present; the lookup must be demand-driven", fake.currentUserCalls)
			}
		})
	}
}

// TestMeResolvesOnceForEveryKeyOnEdit: edit composes ONE payload for N
// keys, so the sentinel resolves once and every key gets the same id — not
// one lookup per key.
func TestMeResolvesOnceForEveryKeyOnEdit(t *testing.T) {
	fake := &fakeWriteClient{}
	_, err := runEdit(t.Context(), writeDeps(fake), invocation(false, "KEY-1", "KEY-2", "KEY-3"),
		writeFlagsFor(t, command.EditIssueVerb, "--assignee", command.MeSentinel),
		editOptions{parallelism: 4})
	if err != nil {
		t.Fatalf("runEdit: %v", err)
	}
	if fake.currentUserCalls != 1 {
		t.Errorf("CurrentUser called %d times for 3 keys, want 1 — the payload is composed once", fake.currentUserCalls)
	}
	if len(fake.edited) != 3 {
		t.Fatalf("got %d edits, want one per key", len(fake.edited))
	}
	for _, sent := range fake.edited {
		var body struct {
			Fields struct {
				Assignee struct {
					AccountID string `json:"accountId"`
				} `json:"assignee"`
			} `json:"fields"`
		}
		raw, err := sent.req.MarshalJSON()
		if err != nil {
			t.Fatalf("re-encoding %s: %v", sent.key, err)
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decoding %s's request: %v", sent.key, err)
		}
		if body.Fields.Assignee.AccountID != fakeAccountID {
			t.Errorf("%s was sent assignee %q, want the resolved id", sent.key, body.Fields.Assignee.AccountID)
		}
	}
}

// TestResolveMeFailsLoudlyOnAnEmptyAccountID: an empty id would compose
// `{"accountId": ""}`, which some Jira fields accept as "unassigned" — so
// a silent miss would not error, it would quietly assign the issue to
// nobody. That is why the guard is a hard failure rather than a fallback.
func TestResolveMeFailsLoudlyOnAnEmptyAccountID(t *testing.T) {
	fake := &fakeWriteClient{emptyAccountID: true}
	_, err := runCreate(t.Context(), writeDeps(fake), invocation(false),
		writeFlagsFor(t, command.CreateIssueVerb,
			"--project", "ENG", "--type", "Task", "--summary", "x", "--assignee", command.MeSentinel),
		createOptions{})
	if err == nil {
		t.Fatal("an empty account id was accepted; it would silently unassign the issue")
	}
	if len(fake.created) != 0 {
		t.Error("the issue was created despite an unresolved assignee")
	}
}

// assertInputError checks an error is the CLI's own typed input refusal
// with the expected taxonomy code and field. Both matter: the code is what
// an agent branches on, and the field is what tells a person which flag to
// change.
func assertInputError(t *testing.T, err error, wantCode, wantField string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want %s naming --%s", wantCode, wantField)
	}
	var input *cli.InputError
	if !errors.As(err, &input) {
		t.Fatalf("error %v is not a *cli.InputError; a bad flag must fail as the caller's input", err)
	}
	if input.Code != wantCode {
		t.Errorf("code = %q, want %q (%v)", input.Code, wantCode, err)
	}
	if input.Field != wantField {
		t.Errorf("field = %q, want %q (%v)", input.Field, wantField, err)
	}
}

// assertInputErrorField is assertInputError for the cases where the code
// is not the interesting part — the route refusals, which differ in code
// by design (a two-route clash is a bad value, a two-flag clash is a
// conflict) while always naming the flag to drop.
func assertInputErrorField(t *testing.T, err error, wantField string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want a refusal naming --%s", wantField)
	}
	var input *cli.InputError
	if !errors.As(err, &input) {
		t.Fatalf("error %v is not a *cli.InputError", err)
	}
	if input.Field != wantField {
		t.Errorf("field = %q, want %q (%v)", input.Field, wantField, err)
	}
}
