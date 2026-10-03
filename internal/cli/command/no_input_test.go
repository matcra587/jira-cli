package command

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
)

// interactive() is the ONE seam --no-input acts through: every prompt in
// this CLI is gated on the Invocation.Interactive it produces, so this
// function answering true under the flag would silently restore prompting
// everywhere at once.

// TestNoInputForcesNonInteractive pins the flag's effect at the seam,
// independent of the terminal the test happens to run on. It asserts the
// direction that matters: under the flag the answer is false, whatever the
// environment would otherwise have said.
func TestNoInputForcesNonInteractive(t *testing.T) {
	root := &cobra.Command{Use: "jira"}
	AddGlobalFlags(root)
	leaf := &cobra.Command{Use: "leaf", RunE: func(*cobra.Command, []string) error { return nil }}
	root.AddCommand(leaf)

	if noInput(leaf) {
		t.Fatal("noInput is true before the flag was set")
	}
	if err := root.PersistentFlags().Set(cli.FlagNoInput, "true"); err != nil {
		t.Fatalf("set --%s: %v", cli.FlagNoInput, err)
	}
	if !noInput(leaf) {
		t.Error("--no-input set on root is invisible from a subcommand; the flag would do nothing on every real command")
	}
}

// TestNoInputBeatsAWorkingTerminal asserts the rule against the ONE input
// combination that can distinguish it: a real person at a real terminal,
// no agent detected, who asked not to be prompted. Every other combination
// is already non-interactive for some other reason, which is why this is
// tested through decideInteractive rather than through interactive() —
// the probes cannot be faked in-process, so a test of the wrapper passes
// whether or not the flag is honored at all.
func TestNoInputBeatsAWorkingTerminal(t *testing.T) {
	if !decideInteractive(false, false, true, true) {
		t.Fatal("a person at a terminal with no flag set is not interactive; the fixture no longer isolates the flag")
	}
	if decideInteractive(true, false, true, true) {
		t.Error("--no-input did not beat a working terminal; a harness passing the flag would still be prompted")
	}
}

// TestNoInputIsAbsentOnATreeThatNeverRegisteredIt: a bare command built by
// a unit test has no such flag, and reading one that does not exist must
// answer "not set" rather than panicking or erroring — the same posture
// dryRunFlag takes for a command that registers no --dry-run.
func TestNoInputIsAbsentOnATreeThatNeverRegisteredIt(t *testing.T) {
	bare := &cobra.Command{Use: "bare"}
	if noInput(bare) {
		t.Error("noInput invented a flag the tree never registered")
	}
}
