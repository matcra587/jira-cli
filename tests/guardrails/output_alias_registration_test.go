package guardrails

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/cli"
)

// cli.OutputAliases is read from BOTH ends: command.AddOutputFlags
// registers exactly these flags, and cli.ResolveSelector reads exactly
// these off the parsed set. The table exists so neither half can drift —
// but sharing a table only prevents drift in the half that actually reads
// it, and the registration half is a loop somebody could delete, narrow to
// a subset, or move onto a flag set the resolver never sees. Nothing would
// fail: ResolveSelector calls fs.Changed on a flag that does not exist,
// which is false, not an error. The alias would simply stop working, in
// silence, on every command.
//
// Silence is the whole hazard. These are HIDDEN aliases, so no help text,
// no schema entry and no completion candidate would go missing to make the
// loss visible — the only symptom is an `unknown flag` at the moment a
// caller who trained on another CLI reaches for --json.
//
// So this guard asserts the property the table is supposed to guarantee,
// against the REAL assembled tree rather than a fixture flag set: every row
// is registered, on root's persistent set so every command inherits it
// identically, as a hidden bool.
//
// **This table was never broken, and the guard is not a scar.** It exists
// because a flag-parity review REPORTED all four aliases failing with
// `unknown flag`, and the report was wrong twice over: the probe passed
// --dry-run to `issue view`, a read command that has no such flag, so the
// string "unknown flag" was in the output whatever the alias did; and the
// hunt for the registration grepped for the FlagAlias* constants, which
// AddOutputFlags never names because it iterates the table and reads
// alias.Flag. Absence of a grep match was read as absence of the code.
//
// Recording that matters for whoever reads this next. The registration was
// correct at the time this was written, so nothing here is a fix, and the
// guard should not be taken as evidence that the two halves once drifted.
// It is here because the drift is UNDETECTABLE by hand: the same two
// mistakes that produced a false positive would equally hide a true one,
// and the doc comment on OutputAliases already predicted this exact failure
// in words. A prediction in a comment is not a guard.

// TestEveryOutputAliasIsRegisteredOnRoot fails when a row in
// cli.OutputAliases is missing from the assembled root's persistent flags,
// or is registered in a shape ResolveSelector cannot read.
func TestEveryOutputAliasIsRegisteredOnRoot(t *testing.T) {
	program := guardProgram(t)
	for _, finding := range unregisteredOutputAliases(program.Root.PersistentFlags()) {
		t.Errorf("%s — cli.OutputAliases drives both registration (command.AddOutputFlags) and resolution (cli.ResolveSelector); a row missing from the parser fails as `unknown flag` with nothing in help or the schema to show it went", finding)
	}
}

// TestEveryOutputAliasReachesASubcommand pins the INHERITANCE half
// separately, because registering the aliases on the wrong flag set is the
// mistake that still passes the check above.
//
// A leaf command is what a caller actually types, and cobra resolves a
// leaf's flags from its own set merged with its ancestors' PERSISTENT
// sets — so an alias registered on root's LOCAL set exists, is hidden, is
// a bool, and is still rejected on `jira issue view --json`. The guard
// above would not notice; this one asks the question from where the caller
// stands.
func TestEveryOutputAliasReachesASubcommand(t *testing.T) {
	program := guardProgram(t)
	leaf := deepestLeaf(program.Root)
	if leaf == program.Root {
		t.Fatal("assembled tree has no subcommand to check alias inheritance against")
	}
	// InheritedFlags, not Flags: cobra composes a command's own local and
	// persistent sets in Flags() and merges the ANCESTORS' persistent sets
	// into InheritedFlags(), which is the half that answers whether an
	// alias registered on root reaches this leaf at all.
	for _, finding := range unregisteredOutputAliases(leaf.InheritedFlags()) {
		t.Errorf("%s on %s — the aliases must live on ROOT's persistent set so every command inherits them identically", finding, leaf.CommandPath())
	}
}

// TestOutputAliasScanDetectsAPlantedAbsence keeps the scan itself honest.
//
// The guard's whole value is that it fails when a row stops being
// registered, and a scan that can no longer detect that is indistinguishable
// from a passing one. A fixture set carrying every row but the first must
// produce exactly one finding, naming the row it dropped.
func TestOutputAliasScanDetectsAPlantedAbsence(t *testing.T) {
	if len(cli.OutputAliases) < 2 {
		t.Skip("needs at least two aliases to drop one and still have a table")
	}
	dropped := cli.OutputAliases[0]

	fs := pflag.NewFlagSet("planted", pflag.ContinueOnError)
	for _, alias := range cli.OutputAliases[1:] {
		fs.Bool(alias.Flag, false, "fixture alias")
		fs.Lookup(alias.Flag).Hidden = true
	}

	findings := unregisteredOutputAliases(fs)
	if len(findings) != 1 {
		t.Fatalf("scan reported %d findings for one planted absence, want exactly 1: %v", len(findings), findings)
	}
	if want := "--" + dropped.Flag; !strings.Contains(findings[0], want) {
		t.Errorf("finding %q does not name the dropped alias %s", findings[0], want)
	}
}

// unregisteredOutputAliases reports every cli.OutputAliases row that fs
// cannot serve, and says which of the three properties failed.
//
// Hidden and the bool type are checked alongside presence rather than in
// guards of their own, because each is a way for the alias to be present
// and still wrong: a visible alias widens the documented contract the
// design deliberately keeps to one selector, and a non-bool one makes
// ResolveSelector's GetBool return an error on a flag the caller did pass.
func unregisteredOutputAliases(fs *pflag.FlagSet) []string {
	var findings []string
	for _, alias := range cli.OutputAliases {
		f := fs.Lookup(alias.Flag)
		switch {
		case f == nil:
			findings = append(findings, fmt.Sprintf(
				"--%s (shorthand for --output=%s) is in cli.OutputAliases but is not registered",
				alias.Flag, alias.Mode))
		case !f.Hidden:
			findings = append(findings, fmt.Sprintf(
				"--%s is registered but VISIBLE; the output aliases are hidden compatibility sugar and must not appear in help, the schema or completions",
				alias.Flag))
		case f.Value.Type() != "bool":
			findings = append(findings, fmt.Sprintf(
				"--%s is registered as %s, but cli.ResolveSelector reads it with GetBool",
				alias.Flag, f.Value.Type()))
		}
	}
	return findings
}

// deepestLeaf returns a runnable command as far from the root as the tree
// goes — the furthest an inherited flag has to travel to still work.
func deepestLeaf(root *cobra.Command) *cobra.Command {
	best, bestDepth := root, 0
	var walk func(cmd *cobra.Command, depth int)
	walk = func(cmd *cobra.Command, depth int) {
		if depth > bestDepth && cmd.Runnable() {
			best, bestDepth = cmd, depth
		}
		for _, sub := range cmd.Commands() {
			walk(sub, depth+1)
		}
	}
	walk(root, 0)
	return best
}
