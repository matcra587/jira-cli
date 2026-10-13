package root

import (
	"io"

	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
)

// globalFlagLayer reads --config and --timeout out of the RAW argv,
// before cobra exists.
//
// **Why these two are resolved twice.** Both change things that happen
// BEFORE the command tree can be parsed. The config file has to be located
// before the theme resolves — theme resolution loads config during
// conductor setup, so that a terminal never gets probed over OSC 11 — and
// the adapters read the resolved configuration from closures that were
// built at construction time. A value only available after
// PersistentPreRunE is too late for the first and invisible to the second.
//
// Cobra still registers and parses them (command.AddGlobalFlags), and that
// half is not redundant: it is what puts them in help, the schema and
// completion, and what turns `--timeout wednesday` into a typed flag error
// instead of a value this probe silently drops. The probe is the EARLY
// read; cobra is the CONTRACT. Nothing reads the parsed values back.
//
// Mirrors failureSink.seed exactly — same throwaway flag set, same
// allowlist for the flags it does not know, same "a parse error here is
// about to be reported properly by the real parse" posture.
func globalFlagLayer(args []string) config.FlagLayer {
	probe := pflag.NewFlagSet("global-probe", pflag.ContinueOnError)
	probe.ParseErrorsAllowlist.UnknownFlags = true
	probe.Usage = func() {}
	probe.SetOutput(io.Discard)

	path := probe.String(cli.FlagConfig, "", "")
	profile := probe.StringP(cli.FlagProfile, "P", "", "") //nolint:gocritic // parse-only probe; public flags use command metadata
	timeout := probe.Duration(cli.FlagTimeout, 0, "")
	// Ignored for seed's reason: pflag records what it scanned before
	// failing, and whatever broke the parse is reported by the real one.
	probe.Parse(args) //nolint:errcheck // partial parsing still records the global flags

	return config.FlagLayer{
		Path:       *path,
		Profile:    *profile,
		ProfileSet: probe.Changed(cli.FlagProfile),
		Timeout:    *timeout,
		// Changed, not non-zero: --timeout=0 is a caller explicitly asking
		// for no deadline, which must still beat a timeout set in the
		// config file. Reading it as "absent" would make the flag unable to
		// turn one off.
		TimeoutSet: probe.Changed(cli.FlagTimeout),
	}
}

// applyGlobalFlags seeds the process's flag layer from argv. Called once,
// at the top of Run, before anything loads configuration.
func applyGlobalFlags(args []string) {
	config.SetFlagLayer(globalFlagLayer(args))
}
