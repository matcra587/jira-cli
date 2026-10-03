package guardrails

import (
	"fmt"
	"maps"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Cobra resolves persistent and local flags separately: a subcommand
// re-declaring an ancestor's persistent flag under the same name splits
// the value across two flag objects. Which copy a placement updates and
// which copy a handler reads then silently diverge — wrong value, no
// error, no warning. With --output, --color, --verbose, --quiet and
// conductor's completion flags all persistent on root, this trips the
// first time a subcommand wants its own --quiet.

// TestNoLocalFlagShadowsAPersistentFlag walks the assembled tree and
// fails on any flag declared on a descendant whose name collides with a
// persistent flag on an ancestor, naming both sides of the collision.
func TestNoLocalFlagShadowsAPersistentFlag(t *testing.T) {
	program := guardProgram(t)
	for _, finding := range shadowedFlags(program.Root) {
		t.Errorf("%s — flag placement relative to the subcommand would bind different flag objects; reuse the inherited flag or rename", finding)
	}
}

// TestShadowWalkDetectsAPlantedShadow keeps the walk itself honest: a
// fixture tree with the exact defect must produce exactly one finding,
// so a cobra change to flag-set accounting cannot leave the main guard
// scanning for nothing.
func TestShadowWalkDetectsAPlantedShadow(t *testing.T) {
	parent := &cobra.Command{Use: "fixture"}
	parent.PersistentFlags().Bool("quiet", false, "fixture persistent flag")
	child := &cobra.Command{
		Use:  "child",
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	child.Flags().Bool("quiet", false, "fixture shadowing flag")
	parent.AddCommand(child)

	findings := shadowedFlags(parent)
	if len(findings) != 1 {
		t.Fatalf("walk reported %d findings for a planted shadow, want exactly 1: %v", len(findings), findings)
	}
}

// shadowedFlags reports every flag declared on a descendant command that
// collides by name with an ancestor's persistent flag. Both the local
// and persistent sets of the descendant are checked: shadowing through a
// subcommand's own persistent set splits the value just the same, one
// level further down.
func shadowedFlags(root *cobra.Command) []string {
	var findings []string
	var walk func(cmd *cobra.Command, ancestors map[string]string)
	walk = func(cmd *cobra.Command, ancestors map[string]string) {
		if cmd.HasParent() {
			check := func(fs *pflag.FlagSet) {
				fs.VisitAll(func(f *pflag.Flag) {
					owner, shadowing := ancestors[f.Name]
					if !shadowing {
						return
					}
					findings = append(findings, fmt.Sprintf(
						"%s declares --%s, shadowing the persistent --%s on %s",
						cmd.CommandPath(), f.Name, f.Name, owner,
					))
				})
			}
			check(cmd.LocalNonPersistentFlags())
			check(cmd.PersistentFlags())
		}
		merged := make(map[string]string, len(ancestors))
		maps.Copy(merged, ancestors)
		cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
			merged[f.Name] = cmd.CommandPath()
		})
		for _, sub := range cmd.Commands() {
			walk(sub, merged)
		}
	}
	walk(root, map[string]string{})
	return findings
}
