package command

import (
	"strings"

	xslices "github.com/gechr/x/slices"
	"github.com/spf13/cobra"
)

// AnyArgs is ArgSpec.Max for a positional list with no upper bound.
//
// Negative rather than zero so the ZERO VALUE of ArgSpec reads as "this
// command takes no arguments" — the safe reading for a command whose
// author said nothing. Cobra's own default is ArbitraryArgs, which
// accepts a mistyped subcommand as a positional and runs anyway.
const AnyArgs = -1

// Arg declares one positional argument: what the usage line calls it,
// and what completes it.
//
// One type rather than two parallel lists because the placeholder and
// the predictor are one decision about one position, and splitting them
// across a Use string and a comma-separated annotation is how the two
// came to disagree.
type Arg struct {
	// Name is the placeholder as the usage line spells it (KEY,
	// ISSUE_KEY, PROFILE). Documentation with a job: a guardrail can key
	// on it to find a positional that clearly wanted a predictor and was
	// given none.
	Name string `json:"name"`
	// Predictor is the completion kind the shell calls back with;
	// empty means this position has no dynamic completion. It must be a
	// key in root's completionKinds — a name with no handler generates a
	// script that calls back, gets nothing, and reports no error.
	Predictor string `json:"predictor,omitempty"`
}

// ArgSpec declares a command's whole positional surface: how many
// arguments it takes, what each is called, and what completes it.
//
// One declaration, two derived things — the cobra validator and the
// clib annotation the generated shell scripts call back with. They were
// separate declarations kept in agreement by nobody, so a command could
// accept two arguments while advertising one predictor and every test
// stayed green while TAB did nothing.
type ArgSpec struct {
	// Min and Max bound the count; Max is AnyArgs for a variadic tail.
	//
	// Ints rather than a cobra.PositionalArgs because the arity is read
	// back OUT of the validator by two consumers that are sensitive to
	// how it was built — see validator.
	Min int `json:"min"`
	Max int `json:"max"`
	// Positions describes each argument in order. When Max is AnyArgs
	// the LAST entry covers every further position, which is clib's own
	// rule for dynamic-args, so arity and completion cannot drift apart
	// even in the variadic case.
	Positions []Arg `json:"positions,omitempty"`
}

// validator returns the cobra validator this spec means.
//
// The no-args case returns cobra.NoArgs ITSELF, never a wrapper, and
// that is load-bearing twice over:
//
//  1. root.isNoArgs compares against cobra.NoArgs by FUNCTION POINTER to
//     tell a mistyped subcommand from an argument miscount. A wrapper —
//     however transparent — fails that comparison, and every runnable
//     parent silently reports a miscount for a bad verb, losing the
//     field that named it.
//  2. clib recovers a command's positional limit by calling the
//     validator with 0..32 dummy arguments and regex-matching cobra's
//     own error wording. A bespoke message loses shell completion's
//     MaxPositionalArgs.
//
// Storing preserves identity where wrapping destroys it: verified
// against cobra, a struct field handed back is still NoArgs, while a
// forwarding closure and ExactArgs(0) are both something else despite
// identical behavior. So every case here hands back one of cobra's own
// values rather than one of ours.
func (s ArgSpec) validator() cobra.PositionalArgs {
	switch {
	case s.Min == 0 && s.Max == 0:
		return cobra.NoArgs
	case s.Max == AnyArgs && s.Min == 0:
		return cobra.ArbitraryArgs
	case s.Max == AnyArgs:
		return cobra.MinimumNArgs(s.Min)
	case s.Min == s.Max:
		return cobra.ExactArgs(s.Min)
	case s.Min == 0:
		return cobra.MaximumNArgs(s.Max)
	default:
		return cobra.RangeArgs(s.Min, s.Max)
	}
}

// NoCompletionPredictor occupies a positional slot without offering values.
// clib repeats the final predictor, so an omitted trailing slot would reuse
// the preceding predictor for an unrelated argument.
const NoCompletionPredictor = "none"

// annotation renders one predictor per declared
// position. Blank positions use the registered empty predictor, preserving
// their order and suppressing accidental completion from an earlier slot.
func (s ArgSpec) annotation() (string, error) {
	if !xslices.AnyFunc(s.Positions, func(position Arg) bool { return position.Predictor != "" }) {
		return "", nil
	}
	names := xslices.Map(s.Positions, func(position Arg) string {
		if position.Predictor == "" {
			return NoCompletionPredictor
		}
		return position.Predictor
	})
	return "dynamic-args='" + strings.Join(names, ",") + "'", nil
}
