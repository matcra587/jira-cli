package command

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
)

// newAliasCommand builds a command carrying one canonical string flag
// plus the aliases under test, parses args against it, and hands back the
// command — the shape every test here needs, so none of them repeats the
// three lines of cobra setup.
func newAliasCommand(t *testing.T, canonical string, aliases []HiddenAlias, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "probe"}
	var value string
	AddStringVar(cmd, &value, canonical, "", "canonical", FlagOpts{Terse: "Canonical"})
	AddHiddenAliases(cmd, aliases)
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return cmd
}

// TestHiddenAliasIsAcceptedButNotAdvertised pins both halves of the
// design's §2 policy in one place: the parser knows the alias, and every
// surface that describes the command does not.
func TestHiddenAliasIsAcceptedButNotAdvertised(t *testing.T) {
	aliases := []HiddenAlias{{Flag: "status", Canonical: "transition"}}
	cmd := newAliasCommand(t, "transition", aliases, "--status", "Done")

	alias := cmd.Flags().Lookup("status")
	if alias == nil {
		t.Fatal("--status was not registered — an acli-trained invocation would die with unknown flag")
	}
	if !alias.Hidden {
		t.Error("--status is visible in help, the schema and completions — a hidden alias must widen no contract")
	}
}

// TestResolveHiddenAliasesFoldsAliasIntoCanonical is the whole point of
// the mechanism: after resolution the CANONICAL flag holds the value,
// because that is the only variable a command's handler reads.
func TestResolveHiddenAliasesFoldsAliasIntoCanonical(t *testing.T) {
	aliases := []HiddenAlias{{Flag: "status", Canonical: "transition"}}
	cmd := newAliasCommand(t, "transition", aliases, "--status", "Done")

	if err := ResolveHiddenAliases(cmd.Flags(), aliases); err != nil {
		t.Fatalf("ResolveHiddenAliases: %v", err)
	}
	if got := cmd.Flags().Lookup("transition").Value.String(); got != "Done" {
		t.Errorf("--transition = %q after folding --status, want %q", got, "Done")
	}
}

// TestResolveHiddenAliasesLeavesCanonicalAlone pins the other direction:
// an invocation that never touched an alias must come out exactly as it
// went in.
func TestResolveHiddenAliasesLeavesCanonicalAlone(t *testing.T) {
	aliases := []HiddenAlias{{Flag: "status", Canonical: "transition"}}
	cmd := newAliasCommand(t, "transition", aliases, "--transition", "Done")

	if err := ResolveHiddenAliases(cmd.Flags(), aliases); err != nil {
		t.Fatalf("ResolveHiddenAliases: %v", err)
	}
	if got := cmd.Flags().Lookup("transition").Value.String(); got != "Done" {
		t.Errorf("--transition = %q, want %q", got, "Done")
	}
}

// TestResolveHiddenAliasesAcceptsAgreeingSignals pins the permissive half
// of cli.ResolveSelector's rule: the same value twice is not a conflict,
// it is a caller being redundant.
func TestResolveHiddenAliasesAcceptsAgreeingSignals(t *testing.T) {
	aliases := []HiddenAlias{{Flag: "status", Canonical: "transition"}}
	cmd := newAliasCommand(t, "transition", aliases, "--transition", "Done", "--status", "Done")

	if err := ResolveHiddenAliases(cmd.Flags(), aliases); err != nil {
		t.Fatalf("agreeing signals were refused: %v", err)
	}
}

// TestResolveHiddenAliasesRefusesDisagreementWithoutNamingTheAlias pins
// §2's "errors never name an alias" rule, which is the one property that
// would be easy to lose and hard to notice: an introspecting agent was
// shown only --transition, so a message mentioning --status would send it
// hunting a flag it was never told about. Both VALUES must still appear,
// or the refusal is not actionable.
func TestResolveHiddenAliasesRefusesDisagreementWithoutNamingTheAlias(t *testing.T) {
	aliases := []HiddenAlias{{Flag: "status", Canonical: "transition"}}
	cmd := newAliasCommand(t, "transition", aliases, "--transition", "Done", "--status", "In Progress")

	err := ResolveHiddenAliases(cmd.Flags(), aliases)
	if err == nil {
		t.Fatal("two different values were accepted — one of them was silently discarded")
	}
	msg := err.Error()
	if strings.Contains(msg, "--status") {
		t.Errorf("refusal names the hidden alias: %q", msg)
	}
	if !strings.Contains(msg, "--transition") {
		t.Errorf("refusal does not name the canonical flag: %q", msg)
	}
	for _, want := range []string{"Done", "In Progress"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal omits the value %q, so a caller cannot tell which to drop: %q", want, msg)
		}
	}
}

// TestResolveHiddenAliasesRefusesTwoAliasesDisagreeing is the case a
// per-alias loop gets wrong: neither value is the canonical flag's, so a
// resolver comparing each alias only against the canonical would find no
// conflict, fold the first, and let the second silently overwrite or
// vanish. Grouping by canonical is what makes this fail.
func TestResolveHiddenAliasesRefusesTwoAliasesDisagreeing(t *testing.T) {
	aliases := []HiddenAlias{
		{Flag: "body", Canonical: "markdown"},
		{Flag: "description", Canonical: "markdown"},
	}
	cmd := newAliasCommand(t, "markdown", aliases, "--body", "one", "--description", "two")

	if err := ResolveHiddenAliases(cmd.Flags(), aliases); err == nil {
		t.Fatal("two aliases of one flag disagreed and were accepted — one value was silently dropped")
	}
}

// TestResolveHiddenAliasesWalksCanonicalsInFirstSeenOrder pins the walk
// order across a table with MORE THAN ONE canonical, which is the only
// shape that can observe it.
//
// The order decides which canonical's refusal a caller sees when two of
// them are each disagreeing, and an error that reordered itself between
// runs of the same command reads as a different error. The resolver's own
// comment called that load-bearing while nothing tested it: the whole
// suite stayed green with the walk switched to sorted order, and with
// Go's randomized map iteration it would have stayed green intermittently
// with no order at all.
//
// Sorted order is the specific wrong answer this catches — "json-input"
// sorts before "markdown" but is declared after it.
func TestResolveHiddenAliasesWalksCanonicalsInFirstSeenOrder(t *testing.T) {
	t.Parallel()
	aliases := []HiddenAlias{
		{Flag: "body", Canonical: "markdown"},
		{Flag: "description", Canonical: "markdown"},
		{Flag: "from-json", Canonical: "json-input"},
		{Flag: "body-adf", Canonical: "json-input"},
	}

	cmd := &cobra.Command{Use: "probe"}
	var markdown, jsonInput string
	AddStringVar(cmd, &markdown, "markdown", "", "markdown", FlagOpts{Terse: "Markdown"})
	AddStringVar(cmd, &jsonInput, "json-input", "", "json input", FlagOpts{Terse: "JSON input"})
	AddHiddenAliases(cmd, aliases)

	// Both canonicals disagree, so whichever is walked first produces the
	// refusal and the other is never reached.
	if err := cmd.Flags().Parse([]string{
		"--body", "one", "--description", "two",
		"--from-json", "a.json", "--body-adf", "b.json",
	}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	err := ResolveHiddenAliases(cmd.Flags(), aliases)
	if err == nil {
		t.Fatal("two canonicals each disagreed and were accepted")
	}
	var inputErr *cli.InputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("error = %v (%T), want *cli.InputError", err, err)
	}
	if inputErr.Field != "markdown" {
		t.Errorf("field = %q, want %q — the table declares markdown first, so its refusal is the one a caller sees",
			inputErr.Field, "markdown")
	}
}

// TestAddHiddenAliasesPanicsOnUnregisteredCanonical and its sibling below
// pin the startup-wiring refusals. Both are mistakes in code rather than
// input, so they must stop the tree being built rather than surface later
// as a baffling parse error at a caller.
func TestAddHiddenAliasesPanicsOnUnregisteredCanonical(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("aliasing to an unregistered flag did not panic — resolution would fail on a nil lookup")
		}
		if msg, _ := r.(string); !strings.Contains(msg, "not registered") {
			t.Errorf("panic = %v, want it to say the canonical flag is not registered", r)
		}
	}()
	cmd := &cobra.Command{Use: "probe"}
	AddHiddenAliases(cmd, []HiddenAlias{{Flag: "status", Canonical: "nope"}})
}

func TestAddHiddenAliasesPanicsOnUnmirrorableCanonical(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("aliasing a type with no alias form did not panic — the alias would have parsed as something else")
		}
		if msg, _ := r.(string); !strings.Contains(msg, "no alias form") {
			t.Errorf("panic = %v, want it to say the canonical flag's type has no alias form", r)
		}
	}()
	cmd := &cobra.Command{Use: "probe"}
	cmd.Flags().Int("depth", 0, "depth")
	AddHiddenAliases(cmd, []HiddenAlias{{Flag: "levels", Canonical: "depth"}})
}

// TestAddHiddenAliasesMirrorsCanonicalType is the guard for the two bugs
// that consolidating the three registrars fixed, and it asserts the
// PARSE rather than the registration: a bool alias must be settable with
// no value at all (`--remove-assignee --dry-run` consumed --dry-run as
// its value when the alias was registered as a string), and a slice alias
// must accumulate across repeats (`--labels x --labels y` kept only y).
func TestAddHiddenAliasesMirrorsCanonicalType(t *testing.T) {
	t.Parallel()

	t.Run("bool alias takes no value", func(t *testing.T) {
		t.Parallel()
		cmd := &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return nil }}
		var unassign, dryRun bool
		AddBoolVar(cmd, &unassign, "unassign", false, "unassign", FlagOpts{Terse: "Unassign"})
		AddBoolVar(cmd, &dryRun, "dry-run", false, "dry run", FlagOpts{Terse: "Dry run"})
		AddHiddenAliases(cmd, []HiddenAlias{{Flag: "remove-assignee", Canonical: "unassign"}})

		cmd.SetArgs([]string{"--remove-assignee", "--dry-run"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v — a bool alias must not swallow the next argument", err)
		}
		if err := ResolveHiddenAliases(cmd.Flags(),
			[]HiddenAlias{{Flag: "remove-assignee", Canonical: "unassign"}}); err != nil {
			t.Fatalf("ResolveHiddenAliases: %v", err)
		}
		if !unassign {
			t.Error("unassign = false, want the alias folded in")
		}
		if !dryRun {
			t.Error("dry-run = false — the alias consumed it as its own value")
		}
	})

	t.Run("slice alias keeps every occurrence", func(t *testing.T) {
		t.Parallel()
		cmd := &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return nil }}
		var labels []string
		AddStringSliceVar(cmd, &labels, "label", nil, "label", FlagOpts{Terse: "Label", Placeholder: "name"})
		aliases := []HiddenAlias{{Flag: "labels", Canonical: "label"}}
		AddHiddenAliases(cmd, aliases)

		cmd.SetArgs([]string{"--labels", "x", "--labels", "y"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if err := ResolveHiddenAliases(cmd.Flags(), aliases); err != nil {
			t.Fatalf("ResolveHiddenAliases: %v", err)
		}
		if want := []string{"x", "y"}; !slices.Equal(labels, want) {
			t.Errorf("labels = %q, want %q — a repeated slice alias must not drop earlier values", labels, want)
		}
	})
}
