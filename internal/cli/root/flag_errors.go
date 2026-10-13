package root

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"

	xslices "github.com/gechr/x/slices"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
)

// newFlagError converts a pflag parse failure into a typed
// *cli.InputError so it leaves the dispatch layer with a stable code, the
// offending flag in Field, and — for an unknown long flag — "did you
// mean" candidates. pflag exposes the four parse failures as typed errors
// with accessors, so the flag identity is read structurally; the message
// is never parsed. Installed once on the root via SetFlagErrorFunc; cobra
// resolves it upward, so every subcommand's parse failure lands here.
func newFlagError(cmd *cobra.Command, err error) error {
	if notExist, ok := errors.AsType[*pflag.NotExistError](err); ok {
		flag := notExist.GetSpecifiedName()
		// A shorthand group (-xyz) names single-character flags; the
		// edit-distance space is too small to suggest into.
		var nearMisses []string
		if notExist.GetSpecifiedShortnames() == "" {
			nearMisses = suggestFlags(cmd, flag)
		}
		return cli.UnknownFlagError(err.Error(), flag, nearMisses)
	}

	if valueRequired, ok := errors.AsType[*pflag.ValueRequiredError](err); ok {
		fe := cli.NewInputError(cli.CodeFlagValueMissing, err.Error())
		fe.Field = flagName(valueRequired.GetFlag())
		return fe
	}

	if invalidValue, ok := errors.AsType[*pflag.InvalidValueError](err); ok {
		fe := cli.NewInputError(cli.CodeFlagValueInvalid, err.Error())
		fe.Field = flagName(invalidValue.GetFlag())
		return fe
	}

	if invalidSyntax, ok := errors.AsType[*pflag.InvalidSyntaxError](err); ok {
		fe := cli.NewInputError(cli.CodeFlagSyntaxInvalid, err.Error())
		fe.Field = normalizedFlagToken(invalidSyntax.GetSpecifiedFlag())
		return fe
	}

	// A pflag variant this switch does not know still leaves typed, so it
	// carries a code instead of falling through to the generic fallback.
	return cli.NewInputError(cli.CodeFlagSyntaxInvalid, err.Error())
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

// normalizedFlagToken strips the leading dashes from a flag token as the
// parser reported it, so Field carries the bare name like every other
// input error.
func normalizedFlagToken(tok string) string {
	for len(tok) > 0 && tok[0] == '-' {
		tok = tok[1:]
	}
	return tok
}

// suggestFlags returns "did you mean" candidates for an unknown long
// flag, scoped to the flags visible on cmd — its own plus the persistent
// flags inherited from parents. Hidden flags (the output aliases) are
// excluded: a suggestion must only name surface an introspecting agent
// was shown.
func suggestFlags(cmd *cobra.Command, unknown string) []string {
	var candidates []string
	collect := func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		candidates = append(candidates, f.Name)
	}
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

// checkCommandPath rejects an unknown command BEFORE cobra parses flags.
// Cobra's own dispatch parses flags first, so `jira verson --foo` would
// blame --foo — masking the real mistake (0-magic: the failure names the
// thing that was actually wrong). Find is cobra's dispatch resolution
// without the parse, so the check and real dispatch cannot disagree.
//
// Find itself only errors for an Args-less root (its legacy-args path);
// this tree declares validators, so the miss shows up as the resolved
// command still holding a positional token. A parent with no declared
// positional input treats that token as an unknown child command. A runnable
// parent can also consume its own explicitly declared arguments; those keep
// their validator and handler semantics, just as leaf arguments do.
func checkCommandPath(root *cobra.Command, args []string) error {
	cmd, cargs, err := root.Find(args)
	if err != nil {
		return unknownCommandError(root, firstPositional(root, args))
	}
	if cmd.HasSubCommands() {
		if metas, ok := command.MetaFor(cmd); ok && cmd.Runnable() && len(metas) > 0 && metas[0].Args != nil && metas[0].Args.Max != 0 {
			return nil
		}
		if tok := firstPositional(cmd, cargs); tok != "" {
			return unknownCommandError(cmd, tok)
		}
	}
	return nil
}

// unknownCommandError builds the typed error for a first positional
// token that names no command under cmd, with candidates from cobra's
// own command-name suggestion logic so command and flag suggestions ride
// one contract. A group parent names itself in the message so a
// subcommand typo reads differently from a top-level one.
func unknownCommandError(cmd *cobra.Command, name string) error {
	msg := fmt.Sprintf("unknown command %q", name)
	if cmd.HasParent() {
		msg = fmt.Sprintf("unknown command %q for %q", name, cmd.CommandPath())
	}
	fe := cli.NewInputError(cli.CodeCommandUnknown, msg)
	fe.Field = name
	// Cobra reads SuggestionsMinimumDistance from the command whose
	// children are being matched, and only the root is configured — a
	// group parent would silently produce no candidates. Inherit before
	// asking.
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = cmd.Root().SuggestionsMinimumDistance
	}
	// Cobra's SuggestionsFor can return the input itself — it matches on
	// prefix as well as edit distance, and a token that names a real
	// command still reaches here when "--" made it a positional. "Did you
	// mean version?" for `version` is noise to a reader and a loop to an
	// agent following the recovery path, so a candidate identical to what
	// was typed is never offered. cli.Suggest already holds this rule for
	// flags; this keeps both suggestion surfaces on one contract.
	fe.Suggestions = slices.DeleteFunc(cmd.SuggestionsFor(name), func(s string) bool {
		return s == name
	})
	return fe
}

// firstPositional returns the first non-flag token in args, resolved
// against cmd's own and inherited persistent flags so a flag that
// consumes a value (--output json) does not have its value mistaken for
// a command. A probe flag set carries each flag's name and value-vs-bool
// arity (NoOptDefVal); unknown flags are tolerated so a bad flag does
// not abort the scan, and a probe parse error is ignored — pflag still
// records the positionals it scanned before failing, which is exactly
// what a caller reaching here needs. Empty means "no command token
// present".
func firstPositional(cmd *cobra.Command, args []string) string {
	name, _ := firstPositionalIndex(cmd, args)
	return name
}

// firstPositionalIndex also returns the original argv position. Stopping the
// probe at the first positional keeps flag values that equal command names
// from being mistaken for the token an alias expansion should replace.
func firstPositionalIndex(cmd *cobra.Command, args []string) (string, int) {
	// Tokens past a bare "--" are declared non-flags by the caller — and
	// declared non-commands just as much: naming one in an
	// unknown-command error would contradict the terminator's meaning.
	if i := slices.Index(args, "--"); i >= 0 {
		args = args[:i]
	}
	probe := pflag.NewFlagSet("command-probe", pflag.ContinueOnError)
	probe.SetInterspersed(false)
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
	// Own persistents first, then the ancestors' — a group parent is
	// probed with everything a real parse would accept at its position.
	cmd.PersistentFlags().VisitAll(register)
	cmd.InheritedFlags().VisitAll(register)
	// --help then has to be asked for by name: cobra hangs it off the LOCAL
	// set, which neither walk above covers. Skipping it is not an option,
	// because pflag special-cases an UNREGISTERED --help and aborts the
	// parse with ErrHelp before the unknown-flag allowlist can tolerate it —
	// a probe that has not been told about --help stops scanning at the
	// first one and reports no command token, turning a typo'd verb into an
	// arity failure. Cobra defines the flag itself (idempotently, and during
	// ExecuteC regardless), so the probe inherits cobra's own spelling and
	// arity instead of a hand-rolled guess.
	cmd.InitDefaultHelpFlag()
	if help := cmd.Flags().Lookup("help"); help != nil {
		register(help)
	}
	probe.Parse(args) //nolint:errcheck // partial parsing still identifies the first positional token
	if probe.NArg() == 0 {
		return "", -1
	}
	return probe.Arg(0), len(args) - probe.NArg()
}

// noopValue is a pflag.Value that discards everything assigned to it.
// The command probe only needs each flag's name and arity to strip flags
// correctly; the values themselves are never read.
type noopValue struct{}

func (noopValue) String() string   { return "" }
func (noopValue) Set(string) error { return nil }
func (noopValue) Type() string     { return "string" }

// typeArgErrors walks the assembled tree and wraps every command's
// positional-argument validator so an arity failure leaves the dispatch
// layer as a typed *cli.InputError (arg_count_invalid) instead of an
// untyped cobra string. A validator that already returns a typed input
// error (completion's shell check) passes through untouched — the
// command knows its own class better than a blanket wrapper. A command
// with no validator keeps cobra's arbitrary-args default: nothing to
// fail, nothing to wrap.
func typeArgErrors(cmd *cobra.Command) {
	if orig := cmd.Args; orig != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			err := orig(c, args)
			if err == nil {
				return nil
			}
			var typed *cli.InputError
			if errors.As(err, &typed) {
				return err
			}
			// A parent whose NoArgs validator rejected a positional is
			// reporting an unknown subcommand, not a miscount — cobra's
			// own NoArgs even words it that way. Stamping the arity code
			// on it left the code and hint saying "wrong number of
			// arguments" while the message said "unknown command", the
			// contradiction the pre-dispatch check exists to avoid. It
			// reaches here because a token after "--" is declared a
			// non-command, so the check skips it and cobra's validator is
			// what finally rejects it.
			//
			// Keyed on the VALIDATOR, not on whether the command has
			// children: a parent using ExactArgs, RangeArgs or
			// OnlyValidArgs fails for its own reasons, and relabelling
			// those as an unknown command would tell the caller to fix a
			// verb that was never wrong.
			noArgs := isNoArgs(orig)
			if metadata, ok := command.SelectedVariantMeta(c); ok && metadata.Args != nil {
				noArgs = metadata.Args.Min == 0 && metadata.Args.Max == 0
			}
			if noArgs && c.HasSubCommands() && len(args) > 0 {
				return unknownCommandError(c, args[0])
			}
			return cli.NewInputError(cli.CodeArgCountInvalid, err.Error())
		}
	}
	for _, child := range cmd.Commands() {
		typeArgErrors(child)
	}
}

// isNoArgs reports whether v is cobra's NoArgs validator.
//
// Compared by function identity because cobra.NoArgs is a plain package
// function value: two references to it share one code pointer, and no
// other validator can collide. A name- or message-based test would tie
// this to cobra's wording, which is exactly the coupling the typed
// taxonomy exists to remove.
func isNoArgs(v cobra.PositionalArgs) bool {
	if v == nil {
		return false
	}
	return reflect.ValueOf(v).Pointer() == reflect.ValueOf(cobra.PositionalArgs(cobra.NoArgs)).Pointer()
}
