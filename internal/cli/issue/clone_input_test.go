package issue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// The two input routes `issue clone` gained on top of its field flags,
// and they are tested together because the interesting decisions are
// about ORDER — what beats what, and when a disagreement is refused
// rather than resolved.
//
// The copy policy itself lives in lifecycle_test.go and is not repeated
// here; these tests assume it and check what is layered on top.

// cloneOverridesFile writes a payload to a temp file and returns its path,
// which is what --json-input takes.
func cloneOverridesFile(t *testing.T, payload string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overrides.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write overrides: %v", err)
	}
	return path
}

// runCloneWith runs one clone against a scripted source and returns the
// request that reached CreateIssue.
func runCloneWith(t *testing.T, opts cloneOptions) (*fakeLifecycleClient, error) {
	t.Helper()
	fake := &fakeLifecycleClient{source: cloneSource()}
	if opts.fields == nil {
		opts.fields = &command.IssueFieldFlags{}
	}
	opts.parallelism = 4
	_, err := runClone(t.Context(), lifecycleDeps(fake), invocation(false, "KEY-1"), opts)
	return fake, err
}

// TestCloneJSONInputOverridesTheCopy is --json-input's whole point: it
// reaches a field the flag surface cannot name, and it beats what was
// copied rather than losing to it.
//
// The custom field is the case that motivates the route — no flag exists
// or could exist for `customfield_10016`, because the set is per tenant.
func TestCloneJSONInputOverridesTheCopy(t *testing.T) {
	path := cloneOverridesFile(t, `{"fields":{"priority":{"name":"Low"},"customfield_10016":8}}`)

	fake, err := runCloneWith(t, cloneOptions{jsonInput: path})
	if err != nil {
		t.Fatalf("runClone: %v", err)
	}
	got := fake.created[0].Fields

	if want := `{"name":"Low"}`; string(got["priority"]) != want {
		t.Errorf("priority = %s, want %s — the document must beat the copied value", got["priority"], want)
	}
	if want := "8"; string(got["customfield_10016"]) != want {
		t.Errorf("customfield_10016 = %s, want %s", got["customfield_10016"], want)
	}
	// Layered, not substituted: a document naming one field must leave the
	// rest of the copy alone.
	if _, ok := got["summary"]; !ok {
		t.Error("summary vanished; --json-input overrides the copy rather than replacing the whole request")
	}
	if _, ok := got["labels"]; !ok {
		t.Error("labels vanished; a document naming priority must not drop the other copied fields")
	}
}

// TestCloneJSONInputCarriesTheUpdateHalf pins that `update` survives
// composition. The copy is entirely `fields` SETs, so there is nothing for
// an operation to collide with and the caller's own belongs to them.
func TestCloneJSONInputCarriesTheUpdateHalf(t *testing.T) {
	path := cloneOverridesFile(t, `{"update":{"labels":[{"add":"cloned"}]}}`)

	fake, err := runCloneWith(t, cloneOptions{jsonInput: path})
	if err != nil {
		t.Fatalf("runClone: %v", err)
	}
	got := fake.created[0]
	if want := `[{"add":"cloned"}]`; string(got.Update["labels"]) != want {
		t.Errorf("update.labels = %s, want %s", got.Update["labels"], want)
	}
}

// TestCloneJSONInputCollidingWithAFlagIsRefused is §2.1's conflict rule,
// checked on BOTH halves of the document.
//
// The `update` case is the one worth having: a check reading only `fields`
// would let `update.priority` sit beside --priority and compose a request
// whose meaning depends on which half Jira applies last. That omission is
// the one the design calls out by name.
func TestCloneJSONInputCollidingWithAFlagIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"fields half", `{"fields":{"priority":{"name":"Low"}}}`},
		{"update half", `{"update":{"priority":[{"set":{"name":"Low"}}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := &command.IssueFieldFlags{Priority: "High"}

			fake, err := runCloneWith(t, cloneOptions{
				jsonInput: cloneOverridesFile(t, tc.payload),
				fields:    fields,
			})
			if err == nil {
				t.Fatal("a field named by both the document and a flag must be refused, not silently resolved")
			}
			if !strings.Contains(err.Error(), "priority") {
				t.Errorf("refusal %q does not name the colliding field", err)
			}
			// Refused before anything was written, which is what makes the
			// check worth having on a command that creates issues.
			if len(fake.created) != 0 {
				t.Errorf("created %d issues despite refusing the input", len(fake.created))
			}
		})
	}
}

// TestCloneJSONInputMalformedIsACallerError pins that a bad document fails
// as the INPUT it is rather than traveling to Jira and returning as a 400
// about a payload nobody wrote.
func TestCloneJSONInputMalformedIsACallerError(t *testing.T) {
	fake, err := runCloneWith(t, cloneOptions{jsonInput: cloneOverridesFile(t, `{"fields":`)})
	if err == nil {
		t.Fatal("a malformed document must be refused")
	}
	if !strings.Contains(err.Error(), command.FlagJSONInput) {
		t.Errorf("refusal %q does not name --%s, so the caller cannot tell which input was wrong", err, command.FlagJSONInput)
	}
	if len(fake.created) != 0 {
		t.Error("a malformed document must be refused before anything is created")
	}
}

// TestCloneReplaceRewritesTheSummary is --replace's core case: cloning a
// release ticket for the next version without retyping its summary.
//
// Repeatable and ordered, both asserted, because "applied in order" is the
// documented behavior and a map-backed implementation would lose it.
func TestCloneReplaceRewritesTheSummary(t *testing.T) {
	fake, err := runCloneWith(t, cloneOptions{replacements: []cloneReplacement{
		{old: "Original", new: "Copied"},
		{old: "summary", new: "subject"},
	}})
	if err != nil {
		t.Fatalf("runClone: %v", err)
	}

	var got string
	if err := json.Unmarshal(fake.created[0].Fields["summary"], &got); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if want := "Copied subject"; got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

// TestCloneReplaceRunsAfterTheSummaryOverride pins the documented order:
// --replace transforms whatever the summary SETTLED at, whichever route
// produced it. Here --summary wins the value and --replace rewrites that,
// not the copied original.
func TestCloneReplaceRunsAfterTheSummaryOverride(t *testing.T) {
	fake, err := runCloneWith(t, cloneOptions{
		fields:       &command.IssueFieldFlags{Summary: "Retry of the migration"},
		replacements: []cloneReplacement{{old: "Retry", new: "Second attempt"}},
	})
	if err != nil {
		t.Fatalf("runClone: %v", err)
	}

	var got string
	if err := json.Unmarshal(fake.created[0].Fields["summary"], &got); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if want := "Second attempt of the migration"; got != want {
		t.Errorf("summary = %q, want %q — --replace applies to the settled summary, not the copied one", got, want)
	}
}

// TestCloneReplaceLeavesTheDescriptionAlone is the deliberate narrowing,
// pinned so it cannot be widened by accident.
//
// The description is a structured document; a textual replace across it
// would rewrite link targets and attribute values as readily as prose. The
// source's body contains no matching text here — what is asserted is that
// the document arrives byte-identical to what was copied.
func TestCloneReplaceLeavesTheDescriptionAlone(t *testing.T) {
	fake, err := runCloneWith(t, cloneOptions{
		replacements: []cloneReplacement{{old: "doc", new: "REWRITTEN"}},
	})
	if err != nil {
		t.Fatalf("runClone: %v", err)
	}

	got := string(fake.created[0].Fields["description"])
	if strings.Contains(got, "REWRITTEN") {
		t.Errorf("description = %s; --replace must not reach into the body", got)
	}
	if want := `{"type":"doc","version":1,"content":[]}`; got != want {
		t.Errorf("description = %s, want the copied document unchanged (%s)", got, want)
	}
}

// TestCloneReplaceMatchingNothingWarnsWithoutFailing is the no-match
// ruling, and it is a ruling rather than an oversight.
//
// Clone is keyed: a pattern matching six of ten sources is the ordinary
// case the flag exists for, so a per-key refusal would fail keys that
// cloned correctly. Only "matched nothing anywhere" is evidence of a typo,
// and that earns a warning — silence would let one look like a success.
func TestCloneReplaceMatchingNothingWarnsWithoutFailing(t *testing.T) {
	cli.TakeWarnings()

	fake, err := runCloneWith(t, cloneOptions{
		replacements: []cloneReplacement{{old: "no such text", new: "x"}},
	})
	if err != nil {
		t.Fatalf("a --replace that matched nothing must not fail the run: %v", err)
	}
	if len(fake.created) != 1 {
		t.Fatalf("created %d issues, want the clone to have been made anyway", len(fake.created))
	}

	warnings := cli.TakeWarnings()
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want one naming the unmatched search text", len(warnings))
	}
	if warnings[0].Type != envelope.WarningCloneReplaceUnmatched {
		t.Errorf("warning type = %q, want %q", warnings[0].Type, envelope.WarningCloneReplaceUnmatched)
	}
	if !strings.Contains(warnings[0].Message, "no such text") {
		t.Errorf("warning %q does not quote the search text that matched nothing", warnings[0].Message)
	}
}

// TestCloneReplaceThatMatchedSomethingIsSilent is the other half of the
// ruling: a --replace that did its job says nothing.
func TestCloneReplaceThatMatchedSomethingIsSilent(t *testing.T) {
	cli.TakeWarnings()

	if _, err := runCloneWith(t, cloneOptions{
		replacements: []cloneReplacement{{old: "Original", new: "Copied"}},
	}); err != nil {
		t.Fatalf("runClone: %v", err)
	}

	for _, w := range cli.TakeWarnings() {
		if w.Type == envelope.WarningCloneReplaceUnmatched {
			t.Error("a --replace that rewrote the summary must not warn about matching nothing")
		}
	}
}

// TestParseCloneReplacementsRefusals pins the two malformed shapes, and
// the one that looks malformed and is not.
func TestParseCloneReplacementsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{"no separator", "oldnew", "has no `=`"},
		{"empty search text", "=new", "search text is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseCloneReplacements([]string{tc.input})
			if err == nil {
				t.Fatalf("%q must be refused", tc.input)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal %q does not explain the problem (want %q)", err, tc.want)
			}
		})
	}

	// The separator is the FIRST `=`, so a replacement value containing one
	// is ordinary input rather than an error: `=` is a normal character in
	// an issue summary.
	got, err := parseCloneReplacements([]string{"a=b=c"})
	if err != nil {
		t.Fatalf("a replacement value containing `=` is legitimate: %v", err)
	}
	if len(got) != 1 || got[0].old != "a" || got[0].new != "b=c" {
		t.Errorf("parsed %+v, want old=%q new=%q", got, "a", "b=c")
	}
}
