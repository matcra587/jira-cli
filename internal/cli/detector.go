package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/gechr/x/agent"
	"github.com/gechr/x/terminal"
	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/output"
)

// Flag names for the output selector. FlagOutput is the one DISCOVERABLE
// selector — it alone appears in help, schema, and completion. The alias
// names are hidden, functional sugar for agents arriving with other-CLI
// muscle memory; they resolve into the canonical value before the single
// resolution point (ResolveSelector), so no independent boolean state
// ever exists. command registers them; ResolveSelector reads them.
const (
	FlagOutput = "output"
	// FlagJQ names the jq filter applied to machine output.
	FlagJQ = "jq"
	// FlagDryRun previews a mutation without performing it.
	FlagDryRun = "dry-run"
	// FlagJQArg and FlagJQArgJSON bind values into that filter as $name,
	// so a caller never has to splice content into the program text. jq
	// spells these `--arg name value`; pflag cannot take a two-token
	// value, hence the name=value form.
	FlagJQArg     = "jq-arg"
	FlagJQArgJSON = "jq-argjson"
	// FlagConfig names an explicit config file, FlagTimeout bounds each
	// HTTP request, and FlagNoInput is the non-interactive guarantee:
	// anything that would prompt refuses instead. All three are global,
	// documented, and resolved from the raw argv before the command tree
	// runs — see root's own seeding.
	FlagConfig       = "config"
	FlagProfile      = "profile"
	FlagTimeout      = "timeout"
	FlagNoInput      = "no-input"
	FlagAliasJSON    = "json"
	FlagAliasPlain   = "plain"
	FlagAliasHuman   = "human"
	FlagAliasCompact = "compact"
)

// OutputMode is the value of the canonical --output flag — the requested
// mode, before auto-detection resolves it to a concrete output.Format.
type OutputMode string

const (
	// OutputAuto defers to detection: agent env → compact, non-TTY →
	// json, interactive terminal → human.
	OutputAuto OutputMode = "auto"
	// OutputHuman forces the gated human surface.
	OutputHuman OutputMode = "human"
	// OutputJSON forces the full envelope frame.
	OutputJSON OutputMode = "json"
	// OutputCompact forces bare data on success (full frame on failure).
	OutputCompact OutputMode = "compact"
)

// OutputModeValues lists the accepted --output values for help text and
// shell completion.
var OutputModeValues = []string{"auto", "human", "json", "compact"}

// OutputModeTerse describes each mode, parallel to OutputModeValues —
// the one vocabulary help rendering, validation errors, and completion
// candidates all quote, so the three surfaces cannot drift.
var OutputModeTerse = []string{
	"Detect the mode from the environment",
	"Human-readable output for terminals",
	"Full envelope frame, one line",
	"Bare data on success, full frame on failure",
}

// ParseOutputMode validates a raw --output value. An empty string
// resolves to auto (the flag's default); anything unrecognized is
// rejected loudly, naming the canonical flag.
func ParseOutputMode(v string) (OutputMode, error) {
	switch v {
	case "", "auto":
		return OutputAuto, nil
	case "human":
		return OutputHuman, nil
	case "json":
		return OutputJSON, nil
	case "compact":
		return OutputCompact, nil
	default:
		return "", fmt.Errorf("invalid --output value %q: valid values are %s", v, strings.Join(OutputModeValues, ", "))
	}
}

// OutputAlias is one hidden selector alias: the boolean flag and the
// mode it pre-sets. --plain and --human are two names for the same
// surface.
type OutputAlias struct {
	Flag string
	Mode OutputMode
}

// OutputAliases is the one alias table. command registers exactly these
// flags and ResolveSelector reads exactly these — either half drifting
// from the other would fail silently (a registered flag nobody resolves,
// or a resolution probing a flag that doesn't exist), so the two ends
// share the table instead of each keeping a list.
var OutputAliases = []OutputAlias{
	{Flag: FlagAliasJSON, Mode: OutputJSON},
	{Flag: FlagAliasPlain, Mode: OutputHuman},
	{Flag: FlagAliasHuman, Mode: OutputHuman},
	{Flag: FlagAliasCompact, Mode: OutputCompact},
}

// ResolveSelector folds the canonical --output flag and the hidden
// aliases into one OutputMode — the single resolution point. Agreeing
// signals are fine (--output=json --json); disagreeing signals are a
// loud validation error that names only mode VALUES and the canonical
// --output, never a hidden alias — an introspecting agent was never
// shown the aliases, so an error naming one would reference a flag
// absent from its schema.
//
// An alias set explicitly false (--json=false) is treated as no signal:
// the aliases are sugar for requesting a mode, not tri-state selectors.
func ResolveSelector(fs *pflag.FlagSet) (OutputMode, error) {
	var signals []OutputMode
	if fs.Changed(FlagOutput) {
		raw, err := fs.GetString(FlagOutput)
		if err != nil {
			return "", err
		}
		mode, err := ParseOutputMode(raw)
		if err != nil {
			return "", err
		}
		signals = append(signals, mode)
	}
	for _, a := range OutputAliases {
		if !fs.Changed(a.Flag) {
			continue
		}
		on, err := fs.GetBool(a.Flag)
		if err != nil {
			return "", err
		}
		if on {
			signals = append(signals, a.Mode)
		}
	}
	if len(signals) == 0 {
		return OutputAuto, nil
	}
	first := signals[0]
	for _, s := range signals[1:] {
		if s != first {
			return "", fmt.Errorf("conflicting output modes requested (%s and %s): pick one with --output=<%s>",
				first, s, strings.Join(OutputModeValues, "|"))
		}
	}
	return first, nil
}

// Detection is the resolved output environment for one invocation: the
// format auto-detection chose plus the signals it derived it from.
type Detection struct {
	Format    output.Format
	IsTTY     bool
	Agent     bool
	AgentName string
}

// Detect resolves the output environment from stdout: a detected agent
// yields compact, a non-TTY yields json, and an interactive terminal
// yields human. Agent detection is x/agent's: the cross-tool AGENT /
// AI_AGENT convention first (a falsy value is an explicit opt-out),
// then per-vendor marker variables. AgentName can be empty even when
// Agent is true — a bare AGENT=1 opt-in names no vendor.
func Detect(stdout *os.File) Detection {
	d := Detection{
		IsTTY:     terminal.Is(stdout),
		Agent:     agent.Is(),
		AgentName: agent.Detect(),
	}
	d.Format = decideFormat(d.Agent, d.IsTTY)
	return d
}

// decideFormat is the auto-resolution table, separated from the probes so
// the headline rule — agent beats TTY beats neither — is directly
// testable without faking a terminal.
func decideFormat(agentDetected, isTTY bool) output.Format {
	switch {
	case agentDetected:
		return output.FormatCompact
	case !isTTY:
		return output.FormatJSON
	default:
		return output.FormatHuman
	}
}

// ResolveOutputMode turns the selected OutputMode into the concrete
// Format. detect is called only for auto — an explicit --output=json
// must not probe the environment it is overriding.
func ResolveOutputMode(mode OutputMode, detect func() Detection) output.Format {
	switch mode {
	case OutputHuman:
		return output.FormatHuman
	case OutputJSON:
		return output.FormatJSON
	case OutputCompact:
		return output.FormatCompact
	case OutputAuto:
		return detect().Format
	default:
		// Only ParseOutputMode produces OutputMode values; anything else
		// is a programming bug, and this layer's posture is loud, never a
		// silent detect-and-guess.
		panic(fmt.Sprintf("cli: unknown output mode %q", mode))
	}
}

// formatKey is the context key for the invocation's resolved Format,
// seeded by root's PersistentPreRunE.
type formatKey struct{}

// WithFormat returns ctx carrying the resolved output format.
func WithFormat(ctx context.Context, f output.Format) context.Context {
	return context.WithValue(ctx, formatKey{}, f)
}

// FormatFromContext returns the invocation's resolved format. ok is
// false when root's resolution never ran — a wiring bug the caller must
// surface, never paper over with a default.
func FormatFromContext(ctx context.Context) (output.Format, bool) {
	f, ok := ctx.Value(formatKey{}).(output.Format)
	return f, ok
}

// filterKey is the context key for the invocation's compiled --jq
// expression, kept beside the resolved format so both travel the same
// way: seeded once in the root's PersistentPreRunE, read once where the
// envelope is written. Injected rather than held in a package global —
// a global is what forced 0.15 to "disarm" a filter before it could eat
// its own error envelope.
type filterKey struct{}

// WithFilter attaches a compiled jq filter to ctx.
func WithFilter(ctx context.Context, f *output.Filter) context.Context {
	return context.WithValue(ctx, filterKey{}, f)
}

// FilterFromContext returns the compiled filter, or nil when --jq was
// not given. A nil filter is the normal case, not an error.
func FilterFromContext(ctx context.Context) *output.Filter {
	f, _ := ctx.Value(filterKey{}).(*output.Filter)
	return f
}
