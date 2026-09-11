package root

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	xslices "github.com/gechr/x/slices"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newFlagError converts a pflag parse failure into a typed
// *cli.CLIInputError so it leaves the command layer with a stable code, a
// remediation hint, and — for an unknown long flag — a "did you mean"
// suggestion. pflag v1.0.10 exposes the four parse failures as typed
// errors with accessors, so the flag identity is read structurally via
// errors.As; the offending flag name never comes from parsing a message.
// An unrecognized pflag variant still becomes a typed error so it carries
// a code rather than reaching the substring classifier.
func newFlagError(cmd *cobra.Command, err error) error {
	if notExist, ok := errors.AsType[*pflag.NotExistError](err); ok {
		fe := cli.NewCLIInputError(cli.InputFlagUnknown, err.Error())
		fe.Flag = notExist.GetSpecifiedName()
		// A shorthand group (-xyz) names single-character flags; the
		// edit-distance space is too small to suggest into.
		if notExist.GetSpecifiedShortnames() == "" {
			fe.Suggestions = suggestFlags(cmd, fe.Flag)
			if len(fe.Suggestions) == 0 {
				// A flag borrowed from another Jira CLI has no near-miss
				// here — offer that CLI's actual equivalents instead.
				fe.Suggestions = cli.ForeignFlagSuggestions(fe.Flag)
			}
		}
		return fe
	}

	if valueRequired, ok := errors.AsType[*pflag.ValueRequiredError](err); ok {
		fe := cli.NewCLIInputError(cli.InputFlagValueMissing, err.Error())
		fe.Flag = flagName(valueRequired.GetFlag())
		return fe
	}

	if invalidValue, ok := errors.AsType[*pflag.InvalidValueError](err); ok {
		fe := cli.NewCLIInputError(cli.InputFlagValueInvalid, err.Error())
		fe.Flag = flagName(invalidValue.GetFlag())
		return fe
	}

	if invalidSyntax, ok := errors.AsType[*pflag.InvalidSyntaxError](err); ok {
		fe := cli.NewCLIInputError(cli.InputFlagSyntaxInvalid, err.Error())
		fe.Flag = strings.TrimLeft(invalidSyntax.GetSpecifiedFlag(), "-")
		return fe
	}

	return cli.NewCLIInputError(cli.InputFlagSyntaxInvalid, err.Error())
}

// flagName returns a pflag flag's canonical long name, tolerating a nil
// flag (pflag populates it for value errors, but the typed accessors
// promise nothing).
func flagName(f *pflag.Flag) string {
	if f == nil {
		return ""
	}
	return f.Name
}

// suggestFlags returns "did you mean" candidates for an unknown long flag,
// scoped to the flags visible on cmd — its own flags plus the persistent
// flags inherited from parents. cli.Suggest applies the single length gate
// that drops names too short to suggest into meaningfully.
func suggestFlags(cmd *cobra.Command, unknown string) []string {
	candidates := make([]string, 0)
	collect := func(f *pflag.Flag) { candidates = append(candidates, f.Name) }
	cmd.Flags().VisitAll(collect)
	cmd.InheritedFlags().VisitAll(collect)

	// Unique keeps first-seen order, so a flag visible both locally and
	// inherited is offered once, in visit order.
	hits := cli.Suggest(unknown, xslices.Unique(candidates))
	for i := range hits {
		hits[i] = "--" + hits[i]
	}
	return hits
}

// retypeArgValidators walks the command tree and wraps every command's
// positional-argument validator so a count failure leaves the command
// layer as a typed *cli.CLIInputError (code arg_count_invalid) instead of
// an untyped Cobra string. A command with no validator keeps Cobra's
// arbitrary-args default — there is nothing to fail.
func retypeArgValidators(cmd *cobra.Command) {
	if orig := cmd.Args; orig != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			if err := orig(c, args); err != nil {
				return cli.NewCLIInputError(cli.InputArgCountInvalid, err.Error())
			}
			return nil
		}
	}
	for _, child := range cmd.Commands() {
		retypeArgValidators(child)
	}
}

// missingRequiredFlags reports the required flags on cmd that were not
// set. It reads the same annotation Cobra's own ValidateRequiredFlags
// reads (cobra.BashCompOneRequiredFlag — an exported constant), so this is
// the public contract for "required", not a private detail. Running this
// from PersistentPreRunE — which executes before ValidateRequiredFlags —
// lets jira-cli return a typed error before Cobra's untyped string fires.
func missingRequiredFlags(cmd *cobra.Command) []string {
	var missing []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		anno, ok := f.Annotations[cobra.BashCompOneRequiredFlag]
		if ok && len(anno) > 0 && anno[0] == "true" && !f.Changed {
			missing = append(missing, f.Name)
		}
	})
	sort.Strings(missing)
	return missing
}

// requiredFlagError builds the typed error for a set of unset required
// flags, scoping Error.Flag to the first missing flag.
func requiredFlagError(missing []string) error {
	quoted := xslices.Map(missing, func(name string) string { return "--" + name })
	fe := cli.NewCLIInputError(
		cli.InputRequiredFlagMissing,
		fmt.Sprintf("required flag(s) %s not set", strings.Join(quoted, ", ")),
	)
	fe.Flag = missing[0]
	return fe
}

// unknownCommandError builds the typed error for a first positional token
// that does not name a command under cmd, with "did you mean" candidates
// drawn from Cobra's own command-name suggestion logic. A group parent
// names itself in the message ("… for \"jira issue\"") so a subcommand typo
// reads differently from a top-level one.
func unknownCommandError(cmd *cobra.Command, name string) error {
	msg := fmt.Sprintf("unknown command %q", name)
	if cmd.HasParent() {
		msg = fmt.Sprintf("unknown command %q for %q", name, cmd.CommandPath())
	}
	fe := cli.NewCLIInputError(cli.InputCommandUnknown, msg)
	// SuggestionsMinimumDistance is configured on the root only; Cobra reads
	// it from the command whose children are being matched, so a group
	// parent would silently produce no candidates. Inherit before asking.
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = cmd.Root().SuggestionsMinimumDistance
	}
	fe.Suggestions = cmd.SuggestionsFor(name)
	return fe
}

// firstPositional returns the first non-flag token in args, resolved
// against cmd's own and inherited persistent flags so a flag that consumes
// a value (--profile NAME) does not have its value mistaken for a command.
// It mirrors the throwaway-flag-set technique timeoutFromArgs uses: a probe
// set carries each persistent flag's name and its value-vs-bool arity
// (NoOptDefVal), and unknown flags are tolerated so a bad flag does not
// abort the scan. A probe parse error is ignored — pflag still records
// the positionals it scanned before the failure, which is exactly what a
// caller reaching here (an unknown command, possibly trailed by a
// value-flag with no value) needs. An empty return means "no command
// token present".
func firstPositional(cmd *cobra.Command, args []string) string {
	// Tokens past a bare "--" are declared non-flags by the caller — and
	// declared non-commands just as much: naming one in an unknown-command
	// error would contradict the terminator's whole meaning. Only what sits
	// before it can be a command candidate.
	if i := slices.Index(args, "--"); i >= 0 {
		args = args[:i]
	}
	probe := pflag.NewFlagSet("command-probe", pflag.ContinueOnError)
	probe.ParseErrorsAllowlist.UnknownFlags = true
	probe.Usage = func() {}
	probe.SetOutput(io.Discard)
	register := func(f *pflag.Flag) {
		if probe.Lookup(f.Name) != nil {
			return
		}
		probed := probe.VarPF(noopValue{}, f.Name, f.Shorthand, "")
		probed.NoOptDefVal = f.NoOptDefVal
	}
	// Own persistents first, then the ancestors' (root's --profile and
	// friends) — a group parent is probed with everything a real parse
	// would accept at its position.
	cmd.PersistentFlags().VisitAll(register)
	cmd.InheritedFlags().VisitAll(register)
	probe.Parse(args) //nolint:errcheck // partial parsing still identifies the first positional token
	if probe.NArg() == 0 {
		return ""
	}
	return probe.Arg(0)
}

// noopValue is a pflag.Value that discards everything assigned to it. The
// command-probe flag set only needs each flag's name and arity to strip
// flags correctly; the values themselves are never read.
type noopValue struct{}

func (noopValue) String() string   { return "" }
func (noopValue) Set(string) error { return nil }
func (noopValue) Type() string     { return "string" }
