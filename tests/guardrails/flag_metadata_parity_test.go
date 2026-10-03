package guardrails

import (
	"testing"

	clibcobra "github.com/gechr/clib/cli/cobra"
	"github.com/gechr/clib/complete"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// clib pairs Enum and EnumTerse BY INDEX. On a length mismatch it
// silently builds no ValueDescs, so every enum value's completion
// description falls back to the flag's whole usage paragraph — nothing
// errors, nothing warns. clib ships the check (FlagMeta.Validate), but
// only its kong path calls it; the cobra path this CLI uses never does.
// This walk closes that gap: every flag declared anywhere in the real
// tree must satisfy clib's own metadata validation.

// TestFlagMetadataSatisfiesClibValidation walks the assembled tree and
// runs clib's Validate over the metadata of every flag at the command
// that declares it, failing with the command path and clib's own message
// (which names the flag and both lengths). The two enum pairs the tree
// declares today are asserted as seen, so the walk cannot silently
// visit nothing.
func TestFlagMetadataSatisfiesClibValidation(t *testing.T) {
	program := guardProgram(t)
	anchors := map[string]bool{"output": false, "color": false}
	walkAuthoredFlagMeta(program.Root, func(path string, meta complete.FlagMeta) {
		if len(meta.EnumTerse) > 0 {
			if _, ok := anchors[meta.Name]; ok {
				anchors[meta.Name] = true
			}
		}
		if err := meta.Validate(); err != nil {
			t.Errorf("%s: %v — a length mismatch silently drops every terse description, so completion shows the full usage paragraph per value", path, err)
		}
	})
	for name, seen := range anchors {
		if !seen {
			t.Errorf("walk never saw --%s carrying EnumTerse — the known enum pairs moved; update the anchors so this guard stays live", name)
		}
	}
}

// TestFlagMetadataWalkDetectsMismatch plants the defect in a fixture and
// requires the walk to catch it, so the detection channel itself cannot
// rot: if a clib bump moved the metadata or loosened Validate, this
// fails before the main guard goes vacuously green.
func TestFlagMetadataWalkDetectsMismatch(t *testing.T) {
	fixture := &cobra.Command{Use: "fixture"}
	fixture.Flags().String("mode", "", "fixture flag")
	clibcobra.Extend(fixture.Flags().Lookup("mode"), clibcobra.FlagExtra{
		Enum:      []string{"fast", "slow"},
		EnumTerse: []string{"only one description"},
	})
	violations := 0
	walkAuthoredFlagMeta(fixture, func(_ string, meta complete.FlagMeta) {
		if meta.Validate() != nil {
			violations++
		}
	})
	if violations != 1 {
		t.Errorf("walk reported %d violations for a planted Enum/EnumTerse mismatch, want exactly 1", violations)
	}
}

// walkAuthoredFlagMeta visits the clib metadata of every flag at the
// command that DECLARES it. Inherited flags are skipped: they are the
// parent's declaration, and re-checking them per child would report one
// defect at every node below it instead of at its source.
func walkAuthoredFlagMeta(cmd *cobra.Command, visit func(path string, meta complete.FlagMeta)) {
	inherited := make(map[string]bool)
	cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		inherited[f.Name] = true
	})
	for _, meta := range clibcobra.FlagMeta(cmd) {
		if inherited[meta.Name] {
			continue
		}
		visit(cmd.CommandPath(), meta)
	}
	for _, sub := range cmd.Commands() {
		walkAuthoredFlagMeta(sub, visit)
	}
}
