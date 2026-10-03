//go:build outputaudit

package audit

import (
	"slices"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

// Axis is one of the seventeen per-command scorecard axes (design §8.1
// plus the H5 addendum, 2026-08-06): M1–M12 machine contract, H1–H5
// human surface.
type Axis string

const (
	AxisM1  Axis = "M1"  // identity objects at data.<noun>, never bare scalars
	AxisM2  Axis = "M2"  // dry_run on both mutation paths; absent from reads
	AxisM3  Axis = "M3"  // keyed results[] ordering, per-key ok, counts
	AxisM4  Axis = "M4"  // snake_case CLI keys; wire idioms only inside passthrough
	AxisM5  Axis = "M5"  // CLI timestamps UTC RFC 3339; wire timestamps verbatim
	AxisM6  Axis = "M6"  // mode contract: json full frame, compact bare data, errors framed everywhere
	AxisM7  Axis = "M7"  // preview object on mutation dry-runs; absent from reads
	AxisM8  Axis = "M8"  // schema truth: emitted output conforms, fields exercised
	AxisM9  Axis = "M9"  // verb-registry membership
	AxisM10 Axis = "M10" // passthrough fidelity: registered boundary round-trips verbatim
	AxisM11 Axis = "M11" // failure surface: typed error envelope / documented exit contract
	AxisM12 Axis = "M12" // determinism: null/absent/zero stable across scenarios
	AxisH1  Axis = "H1"  // reads render bespoke layouts
	AxisH2  Axis = "H2"  // dry-run speaks preview tense on every human surface
	AxisH3  Axis = "H3"  // consistent humanized formats, stable field order
	AxisH4  Axis = "H4"  // sanitization of Jira-sourced text per sink
	AxisH5  Axis = "H5"  // color-forced human output nests SGR spans correctly (the divider-title bug, generalized)
)

// allAxes is the complete axis universe the partition test closes over.
var allAxes = []Axis{
	AxisM1, AxisM2, AxisM3, AxisM4, AxisM5, AxisM6, AxisM7, AxisM8,
	AxisM9, AxisM10, AxisM11, AxisM12, AxisH1, AxisH2, AxisH3, AxisH4, AxisH5,
}

// Category is a mandatory scenario class (design §10): the expected
// matrix demands a recorded result per applicable category, so a
// category cannot be skipped by simply not writing its recipe. The set
// is the harness floor — capability-gated categories (stdin/`-`,
// pagination end, broken pipe, partial fan-out, permission errors, rate
// limits) join it alongside the metadata flags that make them decidable
// when the commands carrying those capabilities land.
type Category string

const (
	CategorySuccess Category = "success"
	CategoryDryRun  Category = "dry_run"
	CategoryFailure Category = "failure"
	CategoryHelp    Category = "help"
)

// Mode is one output mode a capture runs under.
type Mode string

const (
	ModeJSON    Mode = "json"
	ModeCompact Mode = "compact"
	ModeHuman   Mode = "human"
	// ModeHumanColor captures the SAME human render ModeHuman does, with
	// color FORCED on rather than the NO_COLOR=1 every other mode pins
	// (scenarioEnv). It exists for exactly one axis: H5 needs real SGR
	// bytes on the wire to check their nesting, which NO_COLOR's own
	// determinism guarantee makes structurally impossible to see — the
	// exact reason the divider-title bug shipped invisible to every test
	// that existed before this axis. human_color is an audit-only
	// concept, never a value the CLI's own --output selector accepts;
	// runScenario maps it back to the real "--output=human" plus
	// "--color=always".
	ModeHumanColor Mode = "human_color"
)

// Applicability partitions the seventeen axes for one operation: Applies
// are proven, AssertAbsent are proven NOT to appear, NotApplicable are
// explicitly out of scope. All three are spelled out — a derived
// remainder would let a forgotten axis vanish silently, which is the
// exact gap class this suite exists to kill. The partition test closes
// the loop.
type Applicability struct {
	Applies       []Axis
	AssertAbsent  []Axis
	NotApplicable []Axis
}

// applicabilityFor is the T030 axis-applicability map: command class →
// axis partition, refined by the metadata capability flags (Identity
// gates M1, Keyed gates M3, Passthrough gates M10). An envelope-free
// surface keeps just the mode contract and failure surface: there is no data
// to hold to data axes.
func applicabilityFor(m command.Meta) Applicability {
	if m.NoSuccessEnvelope != "" {
		// H5 joins H1–H4 here: envelope-free operations such as auth.token
		// and completion print raw bytes rather than a View render —
		// never a View render — so there is no styling primitive whose
		// SGR spans H5 could even check the nesting of, honestly the same
		// "no data axes" reasoning this branch already applies to M1–M12.
		return Applicability{
			Applies: []Axis{AxisM6, AxisM11},
			NotApplicable: []Axis{
				AxisM1, AxisM2, AxisM3, AxisM4, AxisM5, AxisM7, AxisM8,
				AxisM9, AxisM10, AxisM12, AxisH1, AxisH2, AxisH3, AxisH4, AxisH5,
			},
		}
	}

	var a Applicability
	conditional := func(axis Axis, on bool) {
		if on {
			a.Applies = append(a.Applies, axis)
			return
		}
		a.NotApplicable = append(a.NotApplicable, axis)
	}

	switch m.Class() {
	case command.ClassRead:
		a.Applies = []Axis{AxisM4, AxisM5, AxisM6, AxisM8, AxisM9, AxisM11, AxisM12, AxisH1, AxisH3, AxisH4, AxisH5}
		// A read must never claim mutation semantics: dry_run and
		// preview are asserted absent, not merely unchecked.
		a.AssertAbsent = []Axis{AxisM2, AxisM7}
		a.NotApplicable = []Axis{AxisH2}
	case command.ClassMutation:
		// H5 Applies here too: a mutation's dry-run preview renders
		// through the same View primitives a read does (a resolved
		// account, a status name), so it can carry the identical class of
		// styling defect — H5 is about SGR structure, not about which
		// class emitted it.
		a.Applies = []Axis{AxisM2, AxisM4, AxisM5, AxisM6, AxisM7, AxisM8, AxisM9, AxisM11, AxisM12, AxisH2, AxisH3, AxisH4, AxisH5}
		// H1 is the read-side bespoke-layout rule; mutations render the
		// shared completion shape instead.
		a.NotApplicable = []Axis{AxisH1}
	case command.ClassLocalState:
		a.Applies = []Axis{AxisM2, AxisM4, AxisM5, AxisM6, AxisM7, AxisM8, AxisM9, AxisM11, AxisM12, AxisH2, AxisH3, AxisH4, AxisH5}
		a.NotApplicable = []Axis{AxisH1}
	}
	conditional(AxisM1, m.Identity)
	conditional(AxisM3, m.Keyed)
	conditional(AxisM10, m.Passthrough)
	return a
}

// categoriesFor lists the scenario categories the expected matrix
// demands for one operation. Derived from metadata, never from which
// recipes exist — that direction is the R10 rule.
func categoriesFor(m command.Meta) []Category {
	if m.NoSuccessEnvelope != "" || m.Class() == command.ClassRead {
		return []Category{CategorySuccess, CategoryFailure, CategoryHelp}
	}
	return []Category{CategorySuccess, CategoryDryRun, CategoryFailure, CategoryHelp}
}

// modesFor lists the output modes a category's captures run under.
// Machine categories run both machine modes (per-mode isolation is the
// harness's job) plus ModeHuman, so every data-bearing category also
// feeds the human-surface axes (H3/H4 below); the help surface stays
// human-only, being human by definition. A category returning ModeHuman
// here does not by itself put an H axis on the matrix — cellAxes still
// decides which axis a given (category, mode) pair actually evaluates,
// exactly as it already narrows which M axes a dry-run capture is
// checked against.
//
// ModeHumanColor rides ONLY alongside CategorySuccess and CategoryDryRun
// — the same two categories whose human capture cellAxes already reads
// for H3/H4 (a read's success, a mutation's or local-state op's dry-run
// preview; see cellAxes's own ModeHuman branches). That is deliberately
// not every category: CategoryFailure's human text is CLI-authored, not
// a View render, so forcing color on it would buy H5 nothing but a
// third binary invocation per failure recipe. "One extra capture per
// command, not per scenario" is the design doc's own answer to its own
// open question about human_color's cost.
func modesFor(c Category) []Mode {
	switch c {
	case CategoryHelp:
		return []Mode{ModeHuman}
	case CategorySuccess, CategoryDryRun:
		return []Mode{ModeJSON, ModeCompact, ModeHuman, ModeHumanColor}
	default:
		return []Mode{ModeJSON, ModeCompact, ModeHuman}
	}
}

// cellAxes lists the axes evaluated as matrix cells for one operation ×
// category × mode. Not every applicable axis is a corpus cell: M9 is a
// registry join proven by the inventory reconciliation, and the help
// surface feeds the agent-dx checklist instead. Absence assertions (a
// read's M2/M7) are evaluated on the success capture — an assertion is
// a check, not a skip.
//
// mode now matters: an M axis only makes sense against a machine
// capture (checkSuccessSnakeCase, say, decodes cap.Stdout as JSON — a
// human capture is prose, not JSON, and would fail every M check for a
// reason that has nothing to do with the axis), and H1/H2/H3/H4 only
// make sense against the human capture that actually renders (H1's own
// evidence is source code, not this capture at all — see
// checkH1ViewPrimitives — but it still only makes sense to ask on the
// coordinate a human render exists for). Splitting the switch on mode
// keeps that pairing structural instead of trusting every check
// function to self-filter by cap.Mode.
func cellAxes(m command.Meta, c Category, mode Mode) []Axis {
	app := applicabilityFor(m)
	keep := func(from []Axis, want ...Axis) []Axis {
		var out []Axis
		for _, a := range from {
			if slices.Contains(want, a) {
				out = append(out, a)
			}
		}
		return out
	}
	switch c {
	case CategorySuccess:
		switch mode {
		case ModeHuman:
			// H1 joins H3/H4 here: applicabilityFor keeps H1 in Applies
			// ONLY for the read class (mutations/local-state carry it in
			// NotApplicable — their only human capture is the dry-run
			// preview below, which is CategoryDryRun, not this branch), so
			// keep() already narrows this to the operations H1 is even
			// meant to cover.
			return keep(app.Applies, AxisH1, AxisH3, AxisH4)
		case ModeHumanColor:
			return keep(app.Applies, AxisH5)
		}
		axes := keep(app.Applies, AxisM1, AxisM3, AxisM4, AxisM5, AxisM6, AxisM8, AxisM10, AxisM12)
		return append(axes, keep(app.AssertAbsent, AxisM2, AxisM7)...)
	case CategoryDryRun:
		switch mode {
		case ModeHuman:
			// A mutation's ONLY reachable human capture — auth.login/logout
			// and config.edit have no captured human SUCCESS (their live
			// paths need a real keyring or a PTY the harness cannot give
			// them; see the exemption table), so this is where H2/H3/H4
			// actually get proven for the mutation/local-state classes. H2
			// joins them the same way H1 joins the CategorySuccess branch
			// above — applicabilityFor keeps H2 in Applies only for
			// mutation/local-state, never for a read (a read never
			// dry-runs at all), so keep() again does the narrowing.
			return keep(app.Applies, AxisH2, AxisH3, AxisH4)
		case ModeHumanColor:
			// Same reasoning as ModeHuman just above: this IS the
			// mutation/local-state class's only reachable human capture,
			// so it is where H5 gets proven for them too.
			return keep(app.Applies, AxisH5)
		}
		return keep(app.Applies, AxisM2, AxisM7)
	case CategoryFailure:
		if mode == ModeHuman {
			// Failure text is CLI-authored ("issue TST-999 not found"), not
			// rendered Jira response content — out of scope for this
			// slice; left for whichever later slice extends H3/H4 (or a
			// dedicated axis) to error prose.
			return nil
		}
		return keep(app.Applies, AxisM11)
	case CategoryHelp:
		return nil
	}
	return nil
}
