//go:build outputaudit

package audit

import (
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

// representativeMetas spans every class and capability-flag posture the
// map branches on; the partition test closes over them so a new axis or
// class cannot be half-mapped silently.
var representativeMetas = []struct {
	name string
	meta command.Meta
}{
	{name: "plain read", meta: command.Meta{Op: "x"}},
	{name: "full-featured read", meta: command.Meta{Op: "x", Identity: true, Keyed: true, Passthrough: true}},
	{name: "mutation", meta: command.Meta{Op: "x", WritesJira: true, Identity: true}},
	{name: "local-state mutation", meta: command.Meta{Op: "x", MutatesLocalState: true}},
	{name: "envelope-free surface", meta: command.Meta{Op: "root", NoSuccessEnvelope: "reason"}},
}

// TestApplicabilityPartitionsEveryAxis pins the map's totality: for
// every command shape, each of the sixteen axes lands in exactly one of
// applies / assert-absent / not-applicable. A forgotten axis cannot
// hide in a derived remainder because there is none.
func TestApplicabilityPartitionsEveryAxis(t *testing.T) {
	t.Parallel()
	for _, tt := range representativeMetas {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			app := applicabilityFor(tt.meta)
			seen := map[Axis]int{}
			for _, a := range app.Applies {
				seen[a]++
			}
			for _, a := range app.AssertAbsent {
				seen[a]++
			}
			for _, a := range app.NotApplicable {
				seen[a]++
			}
			for _, axis := range allAxes {
				if seen[axis] != 1 {
					t.Errorf("axis %s appears %d times in the partition, want exactly 1", axis, seen[axis])
				}
			}
			if len(seen) != len(allAxes) {
				t.Errorf("partition covers %d axes, want %d", len(seen), len(allAxes))
			}
		})
	}
}

// TestApplicabilityEncodesTheDesignRules pins the load-bearing rows of
// the design's axis table (§8.1) rather than re-deriving them: reads
// assert mutation semantics ABSENT, capability flags gate their axes,
// and the envelope-free surface keeps only the mode and failure
// contracts.
func TestApplicabilityEncodesTheDesignRules(t *testing.T) {
	t.Parallel()
	read := applicabilityFor(command.Meta{Op: "x"})
	for _, axis := range []Axis{AxisM2, AxisM7} {
		if !slices.Contains(read.AssertAbsent, axis) {
			t.Errorf("a read must assert %s absent, got %v", axis, read.AssertAbsent)
		}
	}
	if slices.Contains(read.Applies, AxisM1) {
		t.Error("M1 applied to a read without an Identity declaration")
	}

	identity := applicabilityFor(command.Meta{Op: "x", Identity: true})
	if !slices.Contains(identity.Applies, AxisM1) {
		t.Error("Identity declaration did not gate M1 into applies")
	}

	mutation := applicabilityFor(command.Meta{Op: "x", WritesJira: true})
	for _, axis := range []Axis{AxisM2, AxisM7, AxisH2} {
		if !slices.Contains(mutation.Applies, axis) {
			t.Errorf("a mutation must prove %s, got %v", axis, mutation.Applies)
		}
	}

	free := applicabilityFor(command.Meta{Op: "root", NoSuccessEnvelope: "reason"})
	want := []Axis{AxisM6, AxisM11}
	if !slices.Equal(free.Applies, want) {
		t.Errorf("envelope-free applies = %v, want %v", free.Applies, want)
	}
}

// TestCategoriesAndModesDeriveFromMetadata pins the category floor and
// the mode wiring the expected matrix multiplies over.
func TestCategoriesAndModesDeriveFromMetadata(t *testing.T) {
	t.Parallel()
	read := categoriesFor(command.Meta{Op: "x"})
	if slices.Contains(read, CategoryDryRun) {
		t.Errorf("a read demands no dry_run category, got %v", read)
	}
	mutation := categoriesFor(command.Meta{Op: "x", WritesJira: true})
	if !slices.Contains(mutation, CategoryDryRun) {
		t.Errorf("a mutation must demand the dry_run category, got %v", mutation)
	}
	if got := modesFor(CategoryHelp); !slices.Equal(got, []Mode{ModeHuman}) {
		t.Errorf("help modes = %v, want human only", got)
	}
	// Success and dry-run both carry ModeHumanColor — the two categories
	// whose human capture is ever reachable (cellAxes's own ModeHuman
	// branches), so H5 rides along on exactly those, not on failure.
	if got := modesFor(CategorySuccess); !slices.Equal(got, []Mode{ModeJSON, ModeCompact, ModeHuman, ModeHumanColor}) {
		t.Errorf("success modes = %v, want both machine modes plus human and human_color", got)
	}
	if got := modesFor(CategoryDryRun); !slices.Equal(got, []Mode{ModeJSON, ModeCompact, ModeHuman, ModeHumanColor}) {
		t.Errorf("dry_run modes = %v, want both machine modes plus human and human_color", got)
	}
	if got := modesFor(CategoryFailure); !slices.Equal(got, []Mode{ModeJSON, ModeCompact, ModeHuman}) {
		t.Errorf("failure modes = %v, want both machine modes plus human, WITHOUT human_color — failure text is CLI-authored, not a styled render", got)
	}
}

// TestCellAxesForTheRootSurface pins today's concrete derivation: the
// envelope-free root yields exactly the M6 success cells and the M11
// failure cell on the machine modes, nothing on its human capture (H3/H4
// are NotApplicable for a NoSuccessEnvelope surface — applicabilityFor
// keeps only M6/M11 in Applies for it), nothing on its human_color
// capture (H5, same reasoning), and nothing on help.
func TestCellAxesForTheRootSurface(t *testing.T) {
	t.Parallel()
	meta := command.Meta{Op: "root", NoSuccessEnvelope: "reason"}
	if got := cellAxes(meta, CategorySuccess, ModeJSON); !slices.Equal(got, []Axis{AxisM6}) {
		t.Errorf("success axes = %v, want [M6]", got)
	}
	if got := cellAxes(meta, CategorySuccess, ModeHuman); got != nil {
		t.Errorf("success human axes = %v, want none — H3/H4 are not applicable to a NoSuccessEnvelope surface", got)
	}
	if got := cellAxes(meta, CategorySuccess, ModeHumanColor); got != nil {
		t.Errorf("success human_color axes = %v, want none — H5 is not applicable to a NoSuccessEnvelope surface either, it never renders through View", got)
	}
	if got := cellAxes(meta, CategoryFailure, ModeJSON); !slices.Equal(got, []Axis{AxisM11}) {
		t.Errorf("failure axes = %v, want [M11]", got)
	}
	if got := cellAxes(meta, CategoryHelp, ModeHuman); got != nil {
		t.Errorf("help axes = %v, want none (the help surface feeds agent-dx)", got)
	}
}
