package root

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	clib "github.com/gechr/clib/cli/cobra"
	"github.com/gechr/clib/complete"
	"github.com/gechr/clive/notify"
	"github.com/gechr/clog"
	"github.com/gechr/x/shell"
	xstrings "github.com/gechr/x/strings"
	"github.com/gechr/x/terminal"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/alias"
	"github.com/matcra587/jira-cli/internal/cli/cmdutil"
	"github.com/matcra587/jira-cli/internal/cli/completion"
	"github.com/matcra587/jira-cli/internal/cli/runtime"
	"github.com/matcra587/jira-cli/internal/cli/startup"
	"github.com/matcra587/jira-cli/internal/cli/tui"
	"github.com/matcra587/jira-cli/internal/selfupdate"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ErrCompletionHandled is returned by Execute when a shell-completion
// preflight request was fully serviced. main translates it into a
// zero-exit termination; no command runs. Keeping this as a returned
// sentinel — rather than an os.Exit deep in construction — leaves main
// the sole owner of process exit.
var ErrCompletionHandled = errors.New("completion request handled")

// newRootCommand builds the bare root *cobra.Command: its metadata,
// persistent flags, groups, help renderer, and PersistentPreRunE/RunE.
// It does NOT attach subcommands or the completion command — that is
// New's job, so the bare root can also seed the completion
// generator before its own completion subcommand exists.
//
// Each call returns an independent command with its own persistent flag
// set: there is no shared process-global command object.
func newRootCommand(rt *runtime.Runtime) *cobra.Command {
	root := &cobra.Command{
		Use:   "jira",
		Short: "Run Jira developer workflows",
		Long: "Run Jira developer workflows from a terminal. Use it to read issues, build JQL, " +
			"update work, manage authentication, and open the persistent dashboard.\n\n" +
			"With stdout attached to a terminal, bare `jira` prints help. In non-TTY or " +
			"agent-detected output, bare `jira` emits the command schema as JSON; use " +
			"`jira agent schema` when you want explicit discovery.",
		Example: `# Use the global interactive flag to open the dashboard
$ jira -i

# Structured output for scripts and agents
$ jira issue list --output=json

$ jira search saved my-open-bugs`,
		SilenceErrors: true,
		SilenceUsage:  true,
		// Enable Cobra's command-name typo suggestions. The field defaults
		// to 0 (exact match only); 2 is Cobra's documented sweet spot and
		// the threshold the unknown-command path reads via SuggestionsFor.
		SuggestionsMinimumDistance: 2,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return rootPersistentPreRun(cmd, rt)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return rootRun(cmd, rt)
		},
	}

	// Route command IO through the runtime streams so output capture is
	// per-instance: a test (or an embedding caller) supplying buffers via
	// runtime options sees every command write land in those buffers, and
	// two roots in one process never share an output destination.
	root.SetOut(rt.Stdout())
	root.SetErr(rt.Stderr())
	root.SetIn(rt.Stdin())

	// Convert pflag parse failures into typed *cli.CLIInputError values.
	// Set on root, the function is inherited by every subcommand (Cobra
	// walks the parent chain for FlagErrorFunc). clib's cobra helper
	// registers none of its own, so there is nothing to chain through.
	root.SetFlagErrorFunc(newFlagError)

	configureRootFlags(root)
	configureRootGroups(root)
	configureRootHelp(root)

	return root
}

// rootPersistentPreRun resolves logging, color, and output-mode detection
// for an invocation, then seeds cmd.Context() with the detector and a
// fresh credential-warning sink. Output detection reads the runtime
// stdout writer so a redirected stream is detected consistently.
func rootPersistentPreRun(cmd *cobra.Command, rt *runtime.Runtime) error {
	if cmd.Name() == "completion" || (cmd.Parent() != nil && cmd.Parent().Name() == "completion") {
		return nil
	}
	if err := validateNumericFlags(cmd); err != nil {
		return err
	}

	pf := cmd.Root().PersistentFlags()
	debug := cmdutil.BoolValue(pf, "debug")
	clog.SetEnvPrefix("JIRA")
	clog.SetVerbose(debug)

	// Resolve --color once and apply it to every color surface. clog.ColorMode
	// implements TextUnmarshaler, so the flag string parses directly; an empty
	// flag keeps the ColorAuto zero value. The mode reaches three places: the
	// clog.Default() logger (stderr human diagnostics); the package-cli stdout
	// surfaces, which build their own loggers clog.Default() does not govern; and
	// the process-wide hyperlink switch the string-level OSC 8 helpers read, so
	// `never` suppresses hyperlinks on every human surface — plain renderer, ADF
	// issue view, exported link helpers — in one place. SetHyperlinkEnabled runs
	// after SetOutput so it pushes the switch onto the new output.
	colorMode := clog.ColorAuto
	if raw := cmdutil.StringValue(pf, "color"); raw != "" {
		if err := colorMode.UnmarshalText([]byte(raw)); err != nil {
			return fmt.Errorf("invalid color mode %q: must be \"auto\", \"always\" or \"never\"", raw)
		}
	}
	clog.SetOutput(clog.NewOutput(cmd.ErrOrStderr(), colorMode))
	clog.SetHyperlinkEnabled(colorMode != clog.ColorNever)
	// Presentation policy for stderr diagnostics, mirroring the stdout plain
	// renderer (newPlainLogger): content-adaptive quote delimiters and digit
	// grouping on large numbers. See the newPlainLogger comment for why
	// omit-zero is NOT part of this policy.
	clog.SetSmartQuotes(true)
	clog.SetNumberFormat(clog.NumberGrouped)
	cli.SetResolvedColorMode(colorMode)
	logger := clog.With().Logger()

	outputRaw := cmdutil.StringValue(pf, "output")
	outputMode, err := cli.ParseOutputMode(outputRaw)
	if err != nil {
		return err
	}
	det := detectOutput(rt)
	det.StdinPiped = !runtimeStdinIsTTY(rt)
	det.Mode = cli.ResolveOutputMode(outputMode, det)
	// --tsv is a script-output format. Off a TTY auto-detection would resolve
	// to JSON, so when the user has not pinned --output explicitly, honor
	// --tsv by rendering the plain (human) TSV table.
	if outputMode == cli.OutputAuto && cmd.Flags().Lookup("tsv") != nil {
		if cmdutil.BoolValue(cmd.Flags(), "tsv") {
			det.Mode = cli.ModePlain
		}
	}
	interactive := cmdutil.BoolValue(pf, "interactive")
	if interactive {
		det.Mode = cli.ModeTUI
	}
	// --jq runs over the machine output. With the mode unset (or auto) it
	// implies --output=json; an explicit --output=human contradicts it and
	// fails validation; an explicit compact keeps compact (the filter then
	// runs over the data document compact emits). The program compiles here
	// so a bad expression fails fast, before any network work.
	jqExpr := cmdutil.StringValue(pf, "jq")
	cli.ClearJQProgram()
	if jqExpr != "" {
		// Every human surface conflicts the same way: the filter runs over
		// JSON output, so an explicit human mode, the TUI, and the TSV table
		// all contradict it and fail validation rather than being silently
		// overridden.
		conflict := ""
		switch {
		case outputMode == cli.OutputHuman:
			conflict = "--output=human"
		case interactive:
			conflict = "--interactive"
		}
		if conflict == "" && cmd.Flags().Lookup("tsv") != nil {
			if cmdutil.BoolValue(cmd.Flags(), "tsv") {
				conflict = "--tsv"
			}
		}
		if conflict != "" {
			ie := cli.NewCLIInputError(cli.InputJQOutputConflict, "--jq filters JSON output and cannot combine with "+conflict)
			ie.Flag = "jq"
			return ie
		}
		prog, err := cli.CompileJQ(jqExpr)
		if err != nil {
			return err
		}
		if outputMode == cli.OutputAuto {
			det.Mode = cli.ModeJSON
		}
		cli.SetJQProgram(cmd.Context(), prog)
	}
	// cmd.Context() is always populated: Execute drives the tree through
	// ExecuteContextC, which seeds every command's context from main's
	// signal-aware root context.
	ctx := cmd.Context()
	ctx = cmdutil.WithDetector(ctx, det)
	// Install a fresh credential-warning sink for this command invocation
	// so a legacy-keyring-fallback warning is scoped to the command that
	// produced it and cannot bleed into another.
	ctx = cmdutil.WithCredentialWarnSink(ctx)
	// And a fresh rate-limit-warning sink, scoped the same way, so a
	// near-limit notice belongs to the command that triggered it.
	ctx = cmdutil.WithRateWarnSink(ctx)
	// And the elapsed-time sink the completion line reads: blocking helpers
	// (Spin, the fanout executor) record their wall time here so API-backed
	// commands can show how long the slow part took.
	ctx = cmdutil.WithAPIElapsedSink(ctx)
	ctx = logger.WithContext(ctx)
	cmd.SetContext(ctx)
	event := logger.Debug().Str("mode", string(det.Mode))
	if det.AgentName != "" {
		event.Str("agent", det.AgentName)
	} else {
		event.Str("agent", "null")
	}
	event.Msg("output detection")

	// Check required flags here, before Cobra's own ValidateRequiredFlags
	// runs (PersistentPreRunE precedes it in the command lifecycle), so a
	// missing required flag leaves the command layer as a typed error
	// rather than Cobra's untyped "required flag(s) ... not set" string.
	if missing := missingRequiredFlags(cmd); len(missing) > 0 {
		return requiredFlagError(missing)
	}
	return nil
}

// validateNumericFlags rejects negative values on the tree-wide numeric
// flags. A negative deadline or page size is always operator error, and
// silently degrading it into the default is the false-confidence failure
// mode (compare `--timeout banana`, which already fails at parse). Zero is
// not rejected: it stays each flag's documented disabled/default sentinel.
func validateNumericFlags(cmd *cobra.Command) error {
	pf := cmd.Root().PersistentFlags()
	for _, name := range []string{"timeout", "max-retry-wait"} {
		if d, err := pf.GetDuration(name); err == nil && d < 0 {
			return fmt.Errorf("validation: --%s must not be negative (got %s); 0 disables it", name, d)
		}
	}
	if cmd.Flags().Lookup("limit") != nil {
		if limit, err := cmd.Flags().GetInt("limit"); err == nil && limit < 0 {
			return fmt.Errorf("validation: --limit must not be negative (got %d); 0 uses the default page size", limit)
		}
	}
	return nil
}

// rootRun is the bare `jira` behavior: launch the dashboard when
// interactive, print help for a human TTY, or emit JSON discovery for a
// pipe/agent.
func rootRun(cmd *cobra.Command, rt *runtime.Runtime) error {
	det := cmdutil.DetectorFromContext(cmd)
	interactive := cmdutil.BoolValue(cmd.Root().PersistentFlags(), "interactive")
	if interactive && !runtimeStdoutIsTTY(rt) {
		return fmt.Errorf("tui requires an interactive terminal")
	}
	if interactive && det.Mode == cli.ModeTUI {
		return tui.Run(cmd)
	}
	// Bare `jira` behavior:
	//   - TTY (human): print help so users get an immediate command list
	//     instead of a wall of JSON.
	//   - non-TTY / agent: emit JSON discovery so pipes and AI agents
	//     keep working (locked contract).
	// Agents wanting structured discovery in TTY can opt in via
	// `jira agent schema [--compact]`.
	if det.IsTTY && det.Mode == cli.ModePlain {
		return cmd.Help()
	}
	return writeDiscoverySchema(cmd)
}

// detectOutput resolves the output detection for the runtime's stdout
// writer. When stdout is an *os.File (the production path) detection
// inspects that descriptor; otherwise (a buffer in tests or an embedding
// caller) it falls back to the process stdout so env-driven agent
// detection still applies.
func detectOutput(rt *runtime.Runtime) cli.Detection {
	if f, ok := rt.Stdout().(*os.File); ok {
		return cli.Detect(f)
	}
	return cli.Detect(os.Stdout)
}

// runtimeStdoutIsTTY reports whether the runtime stdout writer is an
// interactive terminal. A non-*os.File writer (test buffer) is never a
// TTY.
func runtimeStdoutIsTTY(rt *runtime.Runtime) bool {
	f, ok := rt.Stdout().(*os.File)
	return ok && terminal.Is(f)
}

// runtimeStdinIsTTY reports whether the runtime stdin reader is an
// interactive terminal. A non-*os.File reader (test buffer, pipe) is never a
// TTY, so a piped or redirected stdin reads as non-interactive.
func runtimeStdinIsTTY(rt *runtime.Runtime) bool {
	f, ok := rt.Stdin().(*os.File)
	return ok && terminal.Is(f)
}

// New builds a fully assembled root command for the given runtime — the
// importable entry point used by both cmd/jira (to run) and cmd/gen-docs
// (to generate reference docs). The bare root plus every command family
// and the shell completion command. Each call yields an independent
// command tree with its own flag set and IO wiring — no process-global
// command state.
func New(rt *runtime.Runtime) *cobra.Command {
	root := newRootCommand(rt)
	completionCmd := clib.CompletionCommand(root, func() *complete.Generator {
		return completionGenerator(root)
	})
	trackCompletionCommandOutput(root, completionCmd)
	root.AddCommand(completionCmd)
	registerCommands(root)
	// The agent surface mounts last on purpose: its schema is a walk of
	// the live tree, so every command must already be registered.
	mountAgentSurface(root)
	// Wrap every command's positional-argument validator so a count
	// failure surfaces as a typed *cli.CLIInputError. Done after the tree
	// is fully assembled so it reaches every command.
	retypeArgValidators(root)
	return root
}

// trackCompletionCommandOutput preserves Clib's completion command while
// routing its script write through the same destination-error boundary as
// command renderers. Clib's RunE closes over root.OutOrStdout, so the parent
// writer is replaced only for the duration of each shell subcommand.
func trackCompletionCommandOutput(root, completionCmd *cobra.Command) {
	for _, shellCmd := range completionCmd.Commands() {
		runE := shellCmd.RunE
		shellCmd.RunE = func(cmd *cobra.Command, args []string) error {
			original := root.OutOrStdout()
			return cli.TrackWrites(original, func(out io.Writer) error {
				root.SetOut(out)
				defer root.SetOut(original)
				return runE(cmd, args)
			})
		}
	}
}

// completionGenerator builds the clib completion generator for a root
// command. Shared by the `completion` subcommand and the preflight path.
func completionGenerator(root *cobra.Command) *complete.Generator {
	gen := complete.NewGenerator("jira", complete.WithOrder(complete.OrderKeep)).FromFlags(rootCompletionFlagMeta(root))
	gen.Subs = completionSubcommands(root)
	return gen
}

// completionSubcommands keeps the agent surface out of human help while still
// making its flags usable from an interactive shell. clib excludes hidden
// commands before building its completion tree, so temporarily exposing the
// direct child is the only point where the two visibility policies diverge.
func completionSubcommands(root *cobra.Command) []complete.SubSpec {
	for _, child := range root.Commands() {
		if child.Name() != "agent" || !child.Hidden {
			continue
		}

		child.Hidden = false
		defer func() {
			child.Hidden = true
		}()

		break
	}

	return clib.Subcommands(root)
}

func rootCompletionFlagMeta(root *cobra.Command) []complete.FlagMeta {
	meta := clib.FlagMeta(root)
	for i := range meta {
		switch meta[i].Name {
		case "config", "profile":
			meta[i].Forward = true
		}
	}
	return meta
}

// configureRootFlags declares the global persistent flags on root. Each
// call installs a fresh, independent flag set.
func configureRootFlags(root *cobra.Command) {
	pf := root.PersistentFlags()
	cmdutil.AddStringP(pf, "profile", "P", "", "Jira profile name", clib.FlagExtra{
		Group:       "Configuration",
		Placeholder: "NAME",
		Complete:    "predictor=profile",
		Terse:       "profile name",
	})
	cmdutil.AddStringP(pf, "config", "c", "", "Config file path", clib.FlagExtra{
		Group:       "Configuration",
		Placeholder: "PATH",
		Hint:        "file",
		Terse:       "config file path",
	})
	cmdutil.AddStringP(pf, "output", "o", "auto",
		"Output mode; `compact` is the JSON data without the envelope (drops ok/meta/warnings/errors)",
		clib.FlagExtra{
			Group:       "Output",
			Placeholder: "MODE",
			Enum:        cli.OutputModeValues,
			EnumTerse:   []string{"detect terminal", "rich text", "JSON envelope", "JSON data only"},
			EnumDefault: "auto",
			Terse:       "output mode",
		})
	cmdutil.AddString(pf, "jq", "",
		"Filter the JSON output with a jq expression; string results print raw, other values print as JSON per line",
		clib.FlagExtra{
			Group:       "Output",
			Placeholder: "EXPR",
			Terse:       "jq filter",
		})
	cmdutil.AddBoolP(pf, "interactive", "i", false, "Launch persistent dashboard from root command",
		clib.FlagExtra{Group: "Dashboard", Terse: "launch dashboard"})
	cmdutil.AddBoolP(pf, "debug", "d", false, "Enable debug output",
		clib.FlagExtra{Group: "Output", Terse: "debug output"})
	cmdutil.AddBool(pf, "no-input", false,
		"Disable interactive prompts (implied off a TTY or in an agent; pass `--no-input=false` to force prompts)",
		clib.FlagExtra{Group: "Runtime", Terse: "disable prompts"})
	cmdutil.AddDuration(pf, "timeout", 0, "Whole-invocation deadline (e.g. 30s, 2m); 0 disables it",
		clib.FlagExtra{Group: "Configuration", Placeholder: "DURATION", Terse: "invocation deadline"})
	cmdutil.AddDuration(pf, "max-retry-wait", cmdutil.DefaultMaxRetryWait,
		"Longest a request will sleep out Jira rate limits (429/503) before giving up; 0 disables auto-retry. Always capped by --timeout",
		clib.FlagExtra{Group: "Runtime", Placeholder: "DURATION", Terse: "rate-limit retry budget"})
	cmdutil.AddString(pf, "color", "auto", "Color mode; `auto` emits color only to a terminal and honors NO_COLOR, `always` overrides both", clib.FlagExtra{
		Group:       "Output",
		Placeholder: "MODE",
		Enum:        []string{"auto", "always", "never"},
		EnumTerse:   []string{"detect terminal", "force color", "no color"},
		EnumDefault: "auto",
		Terse:       "color mode",
	})
	// ADF strict/best-effort selection — mutually exclusive;
	// internal/cli/adfmode reads them ahead of env/profile/default.
	cmdutil.AddBool(pf, "adf-strict", false, "Treat lossy ADF conversions as errors",
		clib.FlagExtra{Group: "ADF", Terse: "reject lossy ADF"})
	cmdutil.AddBool(pf, "adf-best-effort", false, "Allow lossy ADF conversions with structured warnings",
		clib.FlagExtra{Group: "ADF", Terse: "allow lossy ADF"})

	root.MarkFlagsMutuallyExclusive("adf-strict", "adf-best-effort")
}

// configureRootGroups declares the help command groups on root.
func configureRootGroups(root *cobra.Command) {
	root.AddGroup(
		&cobra.Group{ID: "dashboard", Title: "Dashboard"},
		&cobra.Group{ID: "resources", Title: "Jira Resources"},
		&cobra.Group{ID: "configuration", Title: "Configuration"},
		&cobra.Group{ID: "agent", Title: "Agent Discovery"},
	)
}

// configureRootHelp installs the themed clib help renderer on root.
func configureRootHelp(root *cobra.Command) {
	root.SetHelpFunc(clib.HelpFunc(cmdutil.NewHelpRenderer(), cmdutil.StandardHelpSections))
}

// Execute builds the runtime and root command for a process invocation,
// services any shell-completion preflight, applies the optional
// whole-invocation timeout, and runs the command tree against ctx.
//
// ctx is the root context main owns (signal-aware via signal.NotifyContext).
// Execute never calls os.Exit: a completion preflight that was fully
// handled is reported back to main as ErrCompletionHandled.
func Execute(ctx context.Context) (resultErr error) {
	rt, err := runtime.New()
	if err != nil {
		return err
	}
	// New builds a static command tree; it deliberately takes no context.
	// The per-command PersistentPreRunE derives its context from
	// cmd.Context() at run time — cobra seeds that from the ctx passed to
	// ExecuteContextC below. contextcheck cannot see that deferred handoff
	// and flags the construction call as a missing context thread.
	root := New(rt) //nolint:contextcheck // context flows via ExecuteContextC, not construction
	finishOutputTracking := trackRootCommandOutput(root)
	defer func() {
		resultErr = finishOutputTracking(resultErr)
	}()

	if handled, err := handleCompletionPreflight(root); err != nil {
		return preserveCommandError(err, writeCommandError(ctx, root, err))
	} else if handled {
		return ErrCompletionHandled
	}

	args, err := alias.ExpandAliasArgs(root, os.Args[1:])
	if err != nil {
		return preserveCommandError(err, writeCommandError(ctx, root, err))
	}
	if args != nil {
		root.SetArgs(args)
	}
	// Reject an unknown command before execution so it carries the stable
	// command_unknown code. Cobra's own unknown-command error is an untyped
	// string; here it becomes a typed *cli.CLIInputError with "did you
	// mean" candidates from Cobra's suggestion logic.
	effectiveArgs := args
	if effectiveArgs == nil {
		effectiveArgs = os.Args[1:]
	}
	if cmd, cargs, ferr := root.Find(effectiveArgs); ferr != nil {
		cerr := unknownCommandError(root, firstPositional(root, effectiveArgs))
		return preserveCommandError(cerr, writeCommandError(ctx, root, cerr))
	} else if !cmd.Runnable() && cmd.HasSubCommands() {
		// Cobra only errors for unknown commands at the root; on a group
		// parent it accepts stray positionals and renders help, so a typo'd
		// subcommand either exited 0 with usage or — with a flag trailing —
		// died on the parent's flag parse as "unknown flag", masking the
		// real mistake. A pure group reached with a leftover positional can
		// only mean an unknown subcommand: say so, with the group's own
		// suggestions.
		if tok := firstPositional(cmd, cargs); tok != "" {
			cerr := unknownCommandError(cmd, tok)
			return preserveCommandError(cerr, writeCommandError(ctx, root, cerr))
		}
	}
	// Passive update notify wraps command dispatch: Check schedules a
	// background cache refresh (never blocking, never on the calling
	// path) and the deferred flush prints the throttled one-line hint on
	// a TTY stderr after the command's own output.
	flush := startUpdateNotify(root, effectiveArgs)
	defer flush()
	// Optional whole-invocation deadline. Derive it here, where Execute
	// already owns the root context, with a local defer cancel(): no
	// cancel func is parked in a package var, and the context is never
	// stored on a runtime struct. The deadline spans command execution
	// (alias expansion above is flag/file work with no network I/O; the
	// timeout's purpose is bounding the request).
	if timeout := timeoutFromArgs(args); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	// ExecuteContext seeds cmd.Context() with the (optionally
	// deadline-bounded) root context so every RunE that calls
	// cmd.Context() inherits cancellation.
	cmd, err := root.ExecuteContextC(ctx)
	if err != nil {
		if cmd != nil {
			renderErr := writeCommandError(cmd.Context(), cmd, err) //nolint:contextcheck // use command context seeded by PersistentPreRunE
			return preserveCommandError(err, renderErr)
		}
		return preserveCommandError(err, writeCommandError(ctx, root, err))
	}
	return nil
}

// trackRootCommandOutput closes the two Cobra output seams whose callback
// contracts cannot return errors: Clog's void event finalisers on stderr and
// Cobra's void help function on stdout. The returned finaliser folds either
// destination failure into the command result while preserving an existing
// command error as primary.
func trackRootCommandOutput(root *cobra.Command) func(error) error {
	stderr := cli.NewWriteTracker(root.ErrOrStderr())
	trackedStderr := &commandStderrWriter{Writer: stderr.Writer()}
	if _, ok := stderr.Writer().(interface{ Fd() uintptr }); ok {
		root.SetErr(fdCommandStderrWriter{commandStderrWriter: trackedStderr})
	} else {
		root.SetErr(trackedStderr)
	}

	helpFunc := root.HelpFunc()
	var helpErr error
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		renderErr := cli.TrackWrites(cmd.OutOrStdout(), func(out io.Writer) error {
			trackedCmd := *cmd
			trackedCmd.SetOut(out)
			helpFunc(&trackedCmd, args)
			return nil
		})
		helpErr = errors.Join(helpErr, renderErr)
	})

	return func(commandErr error) error {
		stderrErr := stderr.Err()
		if _, ok := errors.AsType[*commandStderrWriteError](commandErr); ok {
			// A failed command diagnostic is already attached by
			// writeCommandError through its render-local tracker. Do not add
			// the same persistent stderr failure a second time.
			stderrErr = nil
		}
		return preserveCommandError(commandErr, errors.Join(helpErr, stderrErr))
	}
}

type commandStderrWriter struct {
	io.Writer
}

func (w *commandStderrWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err != nil {
		return n, &commandStderrWriteError{Err: err}
	}
	return n, nil
}

type fdCommandStderrWriter struct {
	*commandStderrWriter
}

func (w fdCommandStderrWriter) Fd() uintptr {
	return w.Writer.(interface{ Fd() uintptr }).Fd()
}

type commandStderrWriteError struct {
	Err error
}

func (e *commandStderrWriteError) Error() string {
	return e.Err.Error()
}

func (e *commandStderrWriteError) Unwrap() error {
	return e.Err
}

// startUpdateNotify wires clive's passive "you're behind" check around one
// invocation and returns the flush that prints the hint. The verdict is
// served from a cache file (no network on the calling path); notify itself
// suppresses printing on a non-TTY stderr, throttles hints and refreshes to
// once per 24h, and honors the JIRA_NO_UPDATE_CHECK kill switch.
//
// Two callers are excluded here rather than in notify: detected agent
// context is disabled entirely — regardless of TTY — so agent runs schedule
// no background work and machine envelopes stay byte-clean; and the
// completion and update commands, because completion code paths must stay
// silent and `jira update` already reports update state as its result.
func startUpdateNotify(root *cobra.Command, args []string) func() {
	if args == nil {
		args = os.Args[1:]
	}
	switch firstPositional(root, args) {
	case "completion", "update", "up", "__complete":
		return func() {}
	}
	if cli.Detect(os.Stdout).Agent {
		return func() {}
	}
	return notify.Check(selfupdate.NotifyTool())
}

// handleCompletionPreflight services a clib `--@complete` preflight
// request against root. It returns handled=true when the request was a
// completion request that was fully serviced; the caller must then
// terminate without running a command. Returning the handled signal
// instead of calling os.Exit keeps process termination with main.
func handleCompletionPreflight(root *cobra.Command) (bool, error) {
	flags, positional, ok := clib.Preflight()
	if !ok {
		return false, nil
	}
	gen := completionGenerator(root)
	globals := startup.GlobalsFromArgs(os.Args[1:])
	if forwarded := startup.GlobalsFromArgs(positional); xstrings.AnyNonEmpty(forwarded.ConfigPath, forwarded.Profile) {
		if forwarded.ConfigPath != "" {
			globals.ConfigPath = forwarded.ConfigPath
		}
		if forwarded.Profile != "" {
			globals.Profile = forwarded.Profile
		}
	}
	handler := completion.NewHandler(root.OutOrStdout(), globals)
	handled, err := flags.Handle(gen, handler.Complete, complete.WithArgs(positional))
	if err != nil {
		return false, classifyCompletionPreflightError(flags, gen, err)
	}
	if err := handler.Err(); err != nil {
		return handled, err
	}
	return handled, nil
}

// classifyCompletionPreflightError distinguishes Clib's script-generation
// errors from a failed process-stdout write. The approved upstream
// --print-completion path continues writing to os.Stdout inside Clib; a
// successful discard render proves a later error from that path is the
// destination and gives it the stable local-output taxonomy.
func classifyCompletionPreflightError(
	flags complete.CompletionFlags,
	gen *complete.Generator,
	err error,
) error {
	if err == nil || !flags.PrintCompletion {
		return err
	}
	if flags.Complete != "" || flags.InstallCompletion || flags.UninstallCompletion {
		return err
	}
	sh := flags.Shell
	if sh == "" {
		sh = shell.Detect()
	}
	if generationErr := gen.Print(io.Discard, sh); generationErr != nil {
		return err
	}
	return cli.NewOutputError(err)
}

// timeoutFromArgs extracts the --timeout duration from the resolved
// argv. It parses a throwaway flag set that tolerates every other
// (unknown) flag, so the value is available before cobra runs. args nil
// means "use os.Args[1:]" — the same fallback cobra applies when SetArgs
// was not called.
func timeoutFromArgs(args []string) time.Duration {
	if args == nil {
		args = os.Args[1:]
	}
	fs := pflag.NewFlagSet("timeout-probe", pflag.ContinueOnError)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	fs.Usage = func() {}
	fs.SetOutput(io.Discard)
	timeout := fs.Duration("timeout", 0, "")
	if err := fs.Parse(args); err != nil {
		return 0
	}
	return *timeout
}

func writeCommandError(ctx context.Context, cmd *cobra.Command, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[cmdutil.DiagnosticWrittenError](err); ok {
		return nil
	}
	if _, ok := errors.AsType[*cli.OutputError](err); ok {
		// The command already failed while writing its result. Re-rendering
		// that failure would write to the same failed destination again.
		return nil
	}
	if jsonEnvelopeRequested(cmd) {
		// Machine mode (json/compact): a failure is a parseable JSON envelope on
		// stdout — the same stream as success, with ok:false and the exit code
		// signaling the failure. stdout therefore stays pure JSON and no human
		// diagnostic line is written, so `cmd --output=json | jq` works on the
		// error path too. Some RunEs (e.g. multi-key view partial failure, or
		// watcher add ambiguous-resolution) already wrote their own richer,
		// data-bearing envelope to stdout and signal that with an
		// EnvelopeWritten wrapper; don't write a second one over it.
		if _, ok := errors.AsType[cmdutil.EnvelopeWrittenError](err); !ok {
			return writeErrorEnvelopeToStdout(cmd, err) //nolint:contextcheck // the jq filter runs under the command context captured at resolve time
		}
		return nil
	}
	// Human mode: a single clog diagnostic on stderr, no JSON. A RunE that
	// already rendered its own failure (EnvelopeWritten) still gets this concise
	// summary line, matching prior behavior.
	logger := clog.Ctx(ctx)
	if logger == clog.Default() {
		logger = clog.New(clog.NewOutput(cmd.ErrOrStderr(), cli.ResolvedColorMode()))
	}
	return cli.TrackWrites(cmd.ErrOrStderr(), func(out io.Writer) error {
		renderLogger := logger.With().Logger()
		renderLogger.SetOutput(clog.NewOutput(out, cli.ResolvedColorMode()))
		cliErr := outputErrorFor(err)
		// Human render boundary: the message (which may embed Jira's upstream
		// text), the hint, and the suggestions are written through the terminal
		// sanitizer so server-controlled bytes can never smuggle ANSI/control
		// sequences onto the user's terminal. Machine modes are protected by
		// the JSON encoder; this is the human-mode counterpart.
		event := renderLogger.Error().Err(errors.New(cli.SanitizeTerminalText(err.Error())))
		if clog.IsVerbose() {
			// Under --debug the ERR line also carries the classification an
			// agent would read from the envelope. OmitZero keeps the line
			// lean: retryable renders only when true. The HTTP status is
			// deliberately absent — the message and the --debug traffic trace
			// both already carry it.
			event = event.OmitZero(true).Str("code", cliErr.Code).Str("type", cliErr.Type).Bool("retryable", cliErr.Retryable)
		}
		event.Send()
		// Surface the taxonomy's next-action hint to humans too. In machine mode
		// the hint rides in the JSON envelope; here it renders as clog's dedicated
		// Hint (💡) line so a human sees the same remediation an agent gets.
		if cliErr.Hint != "" {
			renderLogger.Hint().Msg(cli.SanitizeTerminalText(cliErr.Hint))
		}
		// A rate-limited call knows exactly how long to wait: render the
		// per-instance figure on its own line under the static hint (only a
		// 429 carries Retry-After, so a positive value scopes the line).
		if cliErr.RetryAfterSeconds > 0 {
			renderLogger.Hint().Msgf("retry in %ds", cliErr.RetryAfterSeconds)
		}
		if len(cliErr.Suggestions) > 0 {
			renderLogger.Hint().Msgf("Did you mean %s?", cli.SanitizeTerminalText(strings.Join(cliErr.Suggestions, " or ")))
		}
		return nil
	})
}

func preserveCommandError(commandErr, outputErr error) error {
	return cli.PreservePrimaryError(commandErr, outputErr)
}

// jsonEnvelopeRequested returns true when the resolved output mode is a
// machine mode (json or compact), so a command failure still emits a
// parseable error envelope on stdout.
//
// It inspects the --output flag and the env-detector directly so it works
// even before PersistentPreRunE has run (e.g. when cobra fires a flag
// validation error). Detect() reads env vars and the stdout fd directly —
// no cmd.Context() setup is required.
func jsonEnvelopeRequested(cmd *cobra.Command) bool {
	pf := cmd.Root().PersistentFlags()
	outputRaw := cmdutil.StringValue(pf, "output")
	mode, err := cli.ParseOutputMode(outputRaw)
	if err != nil {
		// An invalid --output value is itself a failure; emit the error
		// envelope so the machine consumer sees a parseable result.
		return true
	}
	det := cli.Detect(os.Stdout)
	resolved := cli.ResolveOutputMode(mode, det)
	return resolved == cli.ModeJSON || resolved == cli.ModeCompact
}

// writeErrorEnvelopeToStdout emits a lean failure envelope carrying the
// error in the errors[] array to cmd's stdout. Used only on the
// json/compact/agent-detected path so machine consumers always have
// parseable output on the same stream as success even when the command fails
// before or after RunE.
//
// This helper deliberately bypasses the compact branching used by the
// success path: errors must stay a parseable JSON envelope regardless of
// the output mode, so a failed command is never invisible to an agent.
func writeErrorEnvelopeToStdout(cmd *cobra.Command, err error) error {
	// Use cobra's CommandPath() so nested sub-commands get the full dotted name
	// (e.g. "issue.create") instead of the hand-walked single-level fallback.
	command := strings.TrimPrefix(cmd.CommandPath(), "jira ")
	command = strings.ReplaceAll(command, " ", ".")
	if command == "" || command == "jira" {
		command = "error"
	}
	env := cli.ErrorEnvelope(command, err)
	return cli.WriteEnvelope(cmd.OutOrStdout(), env)
}

// ExitCode maps err to its process exit code. It delegates to the
// central cli.ExitCode mapper so every error type (credential, Jira API,
// rate-limit, validation, command-local) carries the correct stable code.
func ExitCode(err error) int {
	return cli.ExitCode(outputErrorFor(err))
}

// outputErrorFor maps any command error onto the structured cli.Error
// shape. It delegates entirely to the central cli.MapError mapper, which
// uses errors.As for every typed error — credential, Jira API,
// rate-limit, and the command-local board-validation wrapper (via the
// errtax.Coded interface). There is no command-local
// special case: every error envelope is built one way.
func outputErrorFor(err error) cli.Error {
	return cli.MapError(err)
}
