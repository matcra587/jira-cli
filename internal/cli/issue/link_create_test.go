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

// The keyed mechanics are pinned once against `issue link list` and the
// keyed WRITE semantics once against `issue comment add`, both of which
// share this command's executor shape. What is pinned below is what this
// command DECIDES, and the first of them is the one that could silently
// create backwards links:
//
//   - a sentence ("A blocks B") maps onto Jira's inward/outward ends the
//     right way round, and the reverse phrasing reverses it;
//   - a type the site does not have is refused with its own list, before
//     any write;
//   - a bare preview says it could not resolve the type rather than
//     guessing, and --validate-remote resolves it without writing.

// corpusLinkTypes is the site configuration every test here resolves
// against: one asymmetric type whose two phrasings differ, and one
// symmetric type whose phrasings are identical. The symmetric one is not
// padding — it is the case where outward and inward both match and the
// resolution has to be stable anyway.
var corpusLinkTypes = []jira.IssueLinkType{
	{
		ID:      jira.NullableOf("10000"),
		Name:    jira.NullableOf("Blocks"),
		Inward:  jira.NullableOf("is blocked by"),
		Outward: jira.NullableOf("blocks"),
	},
	{
		ID:      jira.NullableOf("10001"),
		Name:    jira.NullableOf("Relates"),
		Inward:  jira.NullableOf("relates to"),
		Outward: jira.NullableOf("relates to"),
	},
}

// fakeLinkWriteClient is a core.JiraClient double for the link write
// path. It records every request so a test can assert which END each
// issue landed on — the only way to catch a reversed direction.
type fakeLinkWriteClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	// types is the site's link-type list; typesErr fails the read.
	types    []jira.IssueLinkType
	typesErr error
	// failures maps a SOURCE key to the error LinkIssues returns.
	failures map[string]error

	mu      sync.Mutex
	links   []jira.LinkIssueRequest
	deleted []string
	// typeReads counts how many times the site's type list was fetched,
	// which is how "resolve once" is checked.
	typeReads int
}

func (f *fakeLinkWriteClient) GetIssueLinkTypes(context.Context) (core.LinkTypeRead, error) {
	f.mu.Lock()
	f.typeReads++
	f.mu.Unlock()
	if f.typesErr != nil {
		return core.LinkTypeRead{}, f.typesErr
	}
	return core.LinkTypeRead{Types: &jira.IssueLinkTypes{
		IssueLinkTypes: jira.NullableOf(f.types),
	}}, nil
}

func (f *fakeLinkWriteClient) LinkIssues(_ context.Context, req jira.LinkIssueRequest) (core.LinkWrite, error) {
	f.mu.Lock()
	f.links = append(f.links, req)
	f.mu.Unlock()
	// Keyed by whichever end is NOT the shared target, so a test can fail
	// one SOURCE regardless of which side the direction put it on.
	for _, end := range []string{req.InwardIssue.Key, req.OutwardIssue.Key} {
		if err, ok := f.failures[end]; ok {
			return core.LinkWrite{}, err
		}
	}
	return core.LinkWrite{}, nil
}

func (f *fakeLinkWriteClient) DeleteIssueLink(_ context.Context, linkID string) (core.LinkDeleteWrite, error) {
	f.mu.Lock()
	f.deleted = append(f.deleted, linkID)
	f.mu.Unlock()
	if err, ok := f.failures[linkID]; ok {
		return core.LinkDeleteWrite{}, err
	}
	return core.LinkDeleteWrite{}, nil
}

func linkWriteDeps(fake *fakeLinkWriteClient) Deps {
	return Deps{JiraClient: func() (core.JiraClient, error) { return fake, nil }}
}

func linkCreateOpts(typeInput string) linkCreateOptions {
	return linkCreateOptions{to: "KEY-456", typeInput: typeInput, parallelism: 4}
}

// TestLinkCreateSentenceMapsOntoJirasEnds is THE test in this file, and
// the one thing here that a review cannot check by reading: it pins
// which Jira end each issue in the caller's sentence lands on.
//
// A link reads `inwardIssue <outward-phrase> outwardIssue`, so "SOURCE
// blocks TARGET" — the caller's forward phrasing — puts SOURCE on the
// INWARD side. Typing the reverse phrase with the same type has to
// produce the mirror request, or `--type 'is blocked by'` would silently
// mean the same thing as `--type blocks` and every such link would point
// the wrong way.
func TestLinkCreateSentenceMapsOntoJirasEnds(t *testing.T) {
	tests := []struct {
		name         string
		typeInput    string
		wantInward   string
		wantOutward  string
		wantRelation string
	}{
		{
			// The forward phrasing: the sentence and the wire agree that
			// SOURCE is the inward end.
			name: "outward phrase reads forward", typeInput: "blocks",
			wantInward: "KEY-123", wantOutward: "KEY-456", wantRelation: "blocks",
		},
		{
			// The reverse phrasing: the same type, the ends swapped. This
			// is the assertion that makes the two phrasings mean different
			// things.
			name: "inward phrase reverses the ends", typeInput: "is blocked by",
			wantInward: "KEY-456", wantOutward: "KEY-123", wantRelation: "is blocked by",
		},
		{
			// The bare NAME reads in the type's forward direction, which is
			// the one Jira's own UI lists first.
			name: "type name reads forward", typeInput: "Blocks",
			wantInward: "KEY-123", wantOutward: "KEY-456", wantRelation: "blocks",
		},
		{
			// Case is not significant: Jira title-cases what an admin
			// typed, and nobody should have to guess which casing was
			// stored.
			name: "matching ignores case", typeInput: "BLOCKS",
			wantInward: "KEY-123", wantOutward: "KEY-456", wantRelation: "blocks",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeLinkWriteClient{types: corpusLinkTypes}

			out, err := runLinkCreate(t.Context(), linkWriteDeps(fake), invocation(false, "KEY-123"), linkCreateOpts(tt.typeInput))
			if err != nil {
				t.Fatalf("runLinkCreate: %v", err)
			}
			if len(fake.links) != 1 {
				t.Fatalf("sent %d requests, want one", len(fake.links))
			}
			got := fake.links[0]
			if got.InwardIssue.Key != tt.wantInward {
				t.Errorf("inwardIssue = %q, want %q — a reversed end creates a backwards link", got.InwardIssue.Key, tt.wantInward)
			}
			if got.OutwardIssue.Key != tt.wantOutward {
				t.Errorf("outwardIssue = %q, want %q", got.OutwardIssue.Key, tt.wantOutward)
			}
			if got.Type.Name != "Blocks" {
				t.Errorf("type = %q, want the site's canonical name", got.Type.Name)
			}
			if out.Relation != tt.wantRelation {
				t.Errorf("published relation = %q, want %q — this is the sentence the caller reads back", out.Relation, tt.wantRelation)
			}
		})
	}
}

// TestLinkCreateResolvesTheTypeOnce pins resolve-once. The type list is
// a SITE read, so resolving per key would pay N times for one answer and
// could, on a site reconfigured mid-run, give two keys different
// relationships.
func TestLinkCreateResolvesTheTypeOnce(t *testing.T) {
	fake := &fakeLinkWriteClient{types: corpusLinkTypes}

	if _, err := runLinkCreate(t.Context(), linkWriteDeps(fake), invocation(false, "KEY-1", "KEY-2", "KEY-3"), linkCreateOpts("blocks")); err != nil {
		t.Fatalf("runLinkCreate: %v", err)
	}
	if fake.typeReads != 1 {
		t.Errorf("read the type list %d times, want exactly once for the whole run", fake.typeReads)
	}
	if len(fake.links) != 3 {
		t.Fatalf("sent %d requests, want one per key", len(fake.links))
	}
	for _, req := range fake.links {
		if req.OutwardIssue.Key != "KEY-456" {
			t.Errorf("a key linked to %q, want every key pointing at the one --to target", req.OutwardIssue.Key)
		}
	}
}

// TestLinkCreateSymmetricTypeResolvesStably: "Relates" phrases both
// directions identically, so both branches of matchLinkType match. The
// answer has to be the forward one every time rather than depending on
// evaluation order.
func TestLinkCreateSymmetricTypeResolvesStably(t *testing.T) {
	fake := &fakeLinkWriteClient{types: corpusLinkTypes}

	out, err := runLinkCreate(t.Context(), linkWriteDeps(fake), invocation(false, "KEY-123"), linkCreateOpts("relates to"))
	if err != nil {
		t.Fatalf("runLinkCreate: %v", err)
	}
	if got := fake.links[0]; got.InwardIssue.Key != "KEY-123" || got.OutwardIssue.Key != "KEY-456" {
		t.Errorf("ends = (%q, %q), want the forward reading for a symmetric type", got.InwardIssue.Key, got.OutwardIssue.Key)
	}
	if out.Relation != "relates to" {
		t.Errorf("relation = %q", out.Relation)
	}
}

// TestLinkCreateRefusesAnUnknownTypeWithTheSiteList: link types are the
// site's own configuration, so the CLI cannot know them ahead of time —
// which makes listing what IS available the only useful thing a refusal
// can do. Both phrasings are offered, because the phrasing is what picks
// the direction.
func TestLinkCreateRefusesAnUnknownTypeWithTheSiteList(t *testing.T) {
	fake := &fakeLinkWriteClient{types: corpusLinkTypes}

	_, err := runLinkCreate(t.Context(), linkWriteDeps(fake), invocation(false, "KEY-123"), linkCreateOpts("obsoletes"))
	if err == nil {
		t.Fatal("a type the site does not have must be refused, not sent")
	}
	var e *cli.InputError
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not a *cli.InputError", err)
	}
	if e.Code != cli.CodeArgValueInvalid {
		t.Errorf("code = %q, want %q", e.Code, cli.CodeArgValueInvalid)
	}
	if e.Field != flagType {
		t.Errorf("field = %q, want %q", e.Field, flagType)
	}
	for _, want := range []string{"blocks", "is blocked by", "relates to"} {
		if !contains(e.Suggestions, want) {
			t.Errorf("suggestions %v do not offer %q — a caller cannot discover the direction without both phrasings", e.Suggestions, want)
		}
	}
	if len(fake.links) != 0 {
		t.Errorf("the refused run still sent %d requests", len(fake.links))
	}
}

// TestLinkCreateRefusesAnAmbiguousPhrase: two custom types can share a
// phrase, and picking whichever Jira listed first would make the link's
// DIRECTION depend on the order a configuration endpoint happened to
// return. Refusing names both.
func TestLinkCreateRefusesAnAmbiguousPhrase(t *testing.T) {
	fake := &fakeLinkWriteClient{types: append(append([]jira.IssueLinkType{}, corpusLinkTypes...), jira.IssueLinkType{
		ID:      jira.NullableOf("10002"),
		Name:    jira.NullableOf("Gating"),
		Inward:  jira.NullableOf("is gated by"),
		Outward: jira.NullableOf("blocks"),
	})}

	_, err := runLinkCreate(t.Context(), linkWriteDeps(fake), invocation(false, "KEY-123"), linkCreateOpts("blocks"))
	if err == nil {
		t.Fatal("a phrase two types answer to must be refused; the direction it means is not decidable")
	}
	var e *cli.InputError
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not a *cli.InputError", err)
	}
	for _, want := range []string{"Blocks", "Gating"} {
		if !strings.Contains(e.Message, want) && !contains(e.Suggestions, want) {
			t.Errorf("refusal names neither the message nor the suggestions for %q: %v / %v", want, e.Message, e.Suggestions)
		}
	}
	if len(fake.links) != 0 {
		t.Errorf("the ambiguous run still sent %d requests", len(fake.links))
	}
}

// TestLinkCreateBarePreviewResolvesNothing is this command's version of
// "a dry run is local-only". It must not build a client at all — so it
// cannot read the type list — and it must SAY that the type is
// unresolved rather than leaving an absent type ambiguous.
func TestLinkCreateBarePreviewResolvesNothing(t *testing.T) {
	asked := false
	deps := Deps{JiraClient: func() (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a bare dry run must not build a client")
	}}

	out, err := runLinkCreate(t.Context(), deps, invocation(true, "KEY-123", "KEY-124"), linkCreateOpts("blocks"))
	if err != nil {
		t.Fatalf("runLinkCreate: %v", err)
	}
	if asked {
		t.Error("the preview asked for a Jira client; a bare --dry-run must never reach the transport")
	}
	if !out.DryRun {
		t.Error("dry_run = false on a preview")
	}
	if out.TypeResolved {
		t.Error("type_resolved = true on a run that contacted nobody")
	}
	if out.Type != nil || out.Relation != "" {
		t.Errorf("preview published type %+v / relation %q, having resolved neither", out.Type, out.Relation)
	}
	if out.TypeInput != "blocks" {
		t.Errorf("type_input = %q, want what the caller typed — it is all this preview knows", out.TypeInput)
	}
	// The machine-readable flag AND the warning, because they serve
	// different consumers: a caller branches on type_resolved, while one
	// watching warnings[] would otherwise see nothing at all. Design §8
	// item 0 asks for the warning specifically.
	if !hasWarning(envelope.WarningLinkTypeUnresolved) {
		t.Errorf("no %q warning was raised; a preview that resolved nothing has to say so in warnings[] too",
			envelope.WarningLinkTypeUnresolved)
	}
	if len(out.Results) != 2 {
		t.Fatalf("got %d results, want one per key", len(out.Results))
	}
}

// TestLinkCreateRemotePreviewResolvesButWritesNothing pins the
// --validate-remote split: the combination exists to answer a question
// only the site can answer, and it answers it without writing.
func TestLinkCreateRemotePreviewResolvesButWritesNothing(t *testing.T) {
	fake := &fakeLinkWriteClient{types: corpusLinkTypes}
	opts := linkCreateOpts("is blocked by")
	opts.validateRemote = true

	out, err := runLinkCreate(t.Context(), linkWriteDeps(fake), invocation(true, "KEY-123"), opts)
	if err != nil {
		t.Fatalf("runLinkCreate: %v", err)
	}
	if !out.TypeResolved {
		t.Error("type_resolved = false; --validate-remote is what makes the preview resolve it")
	}
	if out.Relation != "is blocked by" {
		t.Errorf("relation = %q, want the resolved reverse phrasing", out.Relation)
	}
	if fake.typeReads != 1 {
		t.Errorf("read the type list %d times, want once", fake.typeReads)
	}
	if len(fake.links) != 0 {
		t.Errorf("the remote preview sent %d link requests; it must write nothing", len(fake.links))
	}
}

// TestLinkCreateRemotePreviewRefusesAnUnknownType: the preview's whole
// job is to catch this before a live run does, so a bad type has to fail
// here too rather than being reported as a fine-looking plan.
func TestLinkCreateRemotePreviewRefusesAnUnknownType(t *testing.T) {
	fake := &fakeLinkWriteClient{types: corpusLinkTypes}
	opts := linkCreateOpts("obsoletes")
	opts.validateRemote = true

	if _, err := runLinkCreate(t.Context(), linkWriteDeps(fake), invocation(true, "KEY-123"), opts); err == nil {
		t.Fatal("a remote preview must refuse a type the site does not have")
	}
}

// TestLinkCreatePartialFailureIsAPartialResult is the keyed WRITE rule,
// and it matters more here than for a comment: Jira permits two
// identical links between the same pair and has no idempotency key, so
// results[] is the only thing standing between a retry and a duplicate.
func TestLinkCreatePartialFailureIsAPartialResult(t *testing.T) {
	keys := []string{"KEY-1", "KEY-404", "KEY-3"}
	fake := &fakeLinkWriteClient{types: corpusLinkTypes, failures: map[string]error{
		"KEY-404": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Issue does not exist"}},
	}}

	out, err := runLinkCreate(t.Context(), linkWriteDeps(fake), invocation(false, keys...), linkCreateOpts("blocks"))
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
	bad := out.Results[1]
	if bad.OK || bad.Data != nil {
		t.Error("the failed key must carry error in data's place")
	}
	if bad.Err == nil || bad.Err.Code != "jira_not_found" {
		t.Errorf("failed key error = %+v, want the jira_not_found taxonomy code", bad.Err)
	}
}

// TestLinkCreateTotalFailureFailsTheCommand: with no partial result to
// hand back, an ok envelope carrying only failures would report success
// for a command that achieved nothing.
func TestLinkCreateTotalFailureFailsTheCommand(t *testing.T) {
	fake := &fakeLinkWriteClient{types: corpusLinkTypes, failures: map[string]error{
		"KEY-404": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Issue does not exist"}},
		"KEY-405": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Issue does not exist"}},
	}}

	_, err := runLinkCreate(t.Context(), linkWriteDeps(fake), invocation(false, "KEY-404", "KEY-405"), linkCreateOpts("blocks"))
	if err == nil {
		t.Fatal("a run where every key failed must fail the command")
	}
	if got := cli.MapError(err); got.Code != "jira_not_found" {
		t.Errorf("code = %q, want jira_not_found", got.Code)
	}
}

// TestLinkCreateEmptyPerKeyDataIsDeliberate: Jira's link endpoint
// answers 201 with no body, so a successful key carries an empty object
// rather than a created link. Pinned because the obvious "improvement" —
// inventing an id, or omitting data entirely — would both be wrong.
func TestLinkCreateEmptyPerKeyDataIsDeliberate(t *testing.T) {
	fake := &fakeLinkWriteClient{types: corpusLinkTypes}

	out, err := runLinkCreate(t.Context(), linkWriteDeps(fake), invocation(false, "KEY-123"), linkCreateOpts("blocks"))
	if err != nil {
		t.Fatalf("runLinkCreate: %v", err)
	}
	if !out.Results[0].OK {
		t.Fatal("the key failed")
	}
	if out.Results[0].Data == nil {
		t.Error("a successful key carries no data; the contract says it does, even when the object is empty")
	}
}

func TestRequireLinkTargetAndType(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		field string
	}{
		{"no target", requireLinkTarget(""), flagTo},
		{"no type", requireLinkType(""), flagType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Fatal("want a refusal before any I/O")
			}
			var e *cli.InputError
			if !errors.As(tt.err, &e) {
				t.Fatalf("error %v is not a *cli.InputError", tt.err)
			}
			if e.Code != cli.CodeFlagRequired {
				t.Errorf("code = %q, want %q", e.Code, cli.CodeFlagRequired)
			}
			if e.Field != tt.field {
				t.Errorf("field = %q, want %q", e.Field, tt.field)
			}
		})
	}
	if err := requireLinkTarget("KEY-1"); err != nil {
		t.Errorf("requireLinkTarget with a target = %v, want nil", err)
	}
	if err := requireLinkType("blocks"); err != nil {
		t.Errorf("requireLinkType with a type = %v, want nil", err)
	}
}

// TestLinkCreateIsAKeyedMutation guards the declarations command.New
// cross-checks, and one absence: this command declares no Identity,
// because its identity is per key and the `to` issue at the top is the
// other end rather than the subject.
func TestLinkCreateIsAKeyedMutation(t *testing.T) {
	cmd := newLinkCreate(Deps{})
	metas, ok := command.MetaFor(cmd)
	if !ok || len(metas) == 0 {
		t.Fatal("no metadata recorded for issue.link.create")
	}
	m := metas[0]
	if m.Op != "issue.link.create" {
		t.Errorf("op = %q", m.Op)
	}
	if !m.WritesJira || !m.Keyed {
		t.Errorf("WritesJira=%v Keyed=%v, want both true", m.WritesJira, m.Keyed)
	}
	if !m.Identity {
		t.Error("Identity is false; a keyed command's identity slot is results[].issue, which the audit's M1 axis checks there")
	}
	fs := cmd.Flags()
	for _, want := range []string{cli.FlagDryRun, command.FlagValidateRemote, command.FlagParallelism, flagTo, flagType} {
		if fs.Lookup(want) == nil {
			t.Errorf("--%s is not registered", want)
		}
	}
	for _, absent := range []string{command.FlagForce, command.FlagVerify, command.FlagNoReadback} {
		if fs.Lookup(absent) != nil {
			t.Errorf("--%s is registered; linking is additive and reversible", absent)
		}
	}
}

func TestLinkCreateEnvelopeRegistrationMatches(t *testing.T) {
	reg, ok := envelope.Registered("issue.link.create")
	if !ok {
		t.Fatal("issue.link.create has no envelope registration")
	}
	if _, isType := reg.Sample.(envelope.IssueLinkCreateOutput); !isType {
		t.Errorf("registered sample is %T, want envelope.IssueLinkCreateOutput", reg.Sample)
	}
}

// --- issue link delete ---

// TestLinkDeleteTakesAnIdAndNoKey pins the departure from the layout
// design's `KEY LINK_ID`: the endpoint takes only an id, so a key
// positional would be an argument the command reads and cannot send.
func TestLinkDeleteTakesAnIdAndNoKey(t *testing.T) {
	cmd := newLinkDelete(Deps{})
	// Two positionals must be refused. cobra's own arity check is what
	// enforces it, and asserting through Execute is what proves the
	// ArgSpec actually says one rather than the doc comment merely
	// claiming so.
	cmd.SetArgs([]string{"KEY-123", "10042"})
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	if err := cmd.Execute(); err == nil {
		t.Fatal("`link delete KEY ID` must be refused; this command takes an id alone")
	}
}

func TestLinkDeleteRemovesTheIdItWasGiven(t *testing.T) {
	fake := &fakeLinkWriteClient{}

	out, err := runLinkDelete(t.Context(), linkWriteDeps(fake), invocation(false, "10042"))
	if err != nil {
		t.Fatalf("runLinkDelete: %v", err)
	}
	if out.LinkID != "10042" {
		t.Errorf("link_id = %q", out.LinkID)
	}
	if len(fake.deleted) != 1 || fake.deleted[0] != "10042" {
		t.Errorf("deleted %v, want [10042]", fake.deleted)
	}
}

func TestLinkDeleteDryRunNeverBuildsAClient(t *testing.T) {
	asked := false
	deps := Deps{JiraClient: func() (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a dry run must not build a client")
	}}

	out, err := runLinkDelete(t.Context(), deps, invocation(true, "10042"))
	if err != nil {
		t.Fatalf("runLinkDelete: %v", err)
	}
	if asked {
		t.Error("the preview asked for a Jira client")
	}
	if !out.DryRun || out.LinkID != "10042" {
		t.Errorf("preview = %+v, want dry_run true and the id echoed", out)
	}
}

// TestLinkDeleteTakesNoForce pins the contrast with `comment delete`:
// the test for a confirmation is whether there is an undo, and `link
// create` is one.
func TestLinkDeleteTakesNoForce(t *testing.T) {
	fs := newLinkDelete(Deps{}).Flags()
	if fs.Lookup(cli.FlagDryRun) == nil {
		t.Error("--dry-run is not registered; every mutation carries it")
	}
	for _, absent := range []string{command.FlagForce, command.FlagYes, command.FlagParallelism} {
		if fs.Lookup(absent) != nil {
			t.Errorf("--%s is registered; a link is reversible with `link create`, unlike a deleted comment", absent)
		}
	}
}

func TestLinkDeleteEnvelopeRegistrationMatches(t *testing.T) {
	reg, ok := envelope.Registered("issue.link.delete")
	if !ok {
		t.Fatal("issue.link.delete has no envelope registration")
	}
	if _, isType := reg.Sample.(envelope.IssueLinkDeleteOutput); !isType {
		t.Errorf("registered sample is %T, want envelope.IssueLinkDeleteOutput", reg.Sample)
	}
}

// contains reports slice membership. A local helper rather than
// slices.Contains so the failure messages above can stay about the
// suggestion list rather than about a generic comparison.
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// hasWarning reports whether a warning of the given type is pending on
// the process-wide sink cli.AddWarning appends to.
//
// It DRAINS the sink, which is what makes it usable from a test at all:
// the warnings ride the next envelope written, and nothing in a unit
// test writes one — so without taking them here they would leak into
// whichever test ran next and make a passing assertion depend on order.
func hasWarning(want string) bool {
	for _, w := range cli.TakeWarnings() {
		if w.Type == want {
			return true
		}
	}
	return false
}
