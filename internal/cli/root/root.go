package root

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	clibcobra "github.com/gechr/clib/cli/cobra"
	"github.com/gechr/clib/complete"
	"github.com/gechr/clib/help"
	"github.com/gechr/clib/theme"
	"github.com/gechr/clive/updater"
	"github.com/gechr/clog"
	"github.com/gechr/clog/fx/spinner"
	"github.com/gechr/conductor"
	concobra "github.com/gechr/conductor/cli/cobra"
	"github.com/matcra587/docent"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/agentguides"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/adfcmd"
	agentcmd "github.com/matcra587/jira-cli/internal/cli/agent"
	aliascmd "github.com/matcra587/jira-cli/internal/cli/alias"
	apicmd "github.com/matcra587/jira-cli/internal/cli/api"
	authcmd "github.com/matcra587/jira-cli/internal/cli/auth"
	boardscmd "github.com/matcra587/jira-cli/internal/cli/boards"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/completion"
	"github.com/matcra587/jira-cli/internal/cli/completion/native"
	configcmd "github.com/matcra587/jira-cli/internal/cli/config"
	"github.com/matcra587/jira-cli/internal/cli/discovery"
	epiccmd "github.com/matcra587/jira-cli/internal/cli/epic"
	issuecmd "github.com/matcra587/jira-cli/internal/cli/issue"
	jqlcmd "github.com/matcra587/jira-cli/internal/cli/jql"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	"github.com/matcra587/jira-cli/internal/cli/releasenotes"
	updatecmd "github.com/matcra587/jira-cli/internal/cli/update"
	usercmd "github.com/matcra587/jira-cli/internal/cli/user"
	"github.com/matcra587/jira-cli/internal/cli/version"
	worklogcmd "github.com/matcra587/jira-cli/internal/cli/worklog"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// description is shared by the conductor App and the cobra root so help and
// runtime identity can never disagree.
const (
	name        = "jira"
	description = "Terminal-first Jira Cloud CLI for developer and agent workflows"
)

// enableRunHookTraversal publishes Cobra's process-wide policy before a program
// can execute. Later constructors must not write while existing commands read it.
var enableRunHookTraversal = sync.OnceFunc(func() {
	cobra.EnableTraverseRunHooks = true
})

// Run builds the runtime and command tree and executes one invocation,
// returning the process exit code. The Conductor runtime must exist before
// anything logs — clog re-reads the environment when the prefix changes —
// so construction happens here, never in command packages.
func Run(ctx context.Context, args []string) int {
	// Assemble the real grammar without resolving the file theme: aliases
	// may select another config, and theme resolution is once per process.
	applyGlobalFlags(args)
	program, sink := newProgramWithTheme(defaultDeps(), os.Stdout, os.Stderr, cli.BootstrapTheme) //nolint:contextcheck // the PersistentPreRunE closure reads cmd.Context(), which cobra derives from the ctx below at Execute time — there is no other context to pass
	// Completion actions are handled BEFORE cobra parses anything, with
	// OUR dynamic handler — conductor's own preflight passes a nil
	// handler, which would make every `predictor=` flag silently
	// complete nothing. When an action was handled the process exits
	// now: falling through would run the real command on every
	// keystroke.
	if handled, code := runCompletionAction(ctx, program, sink, args); handled {
		return code
	}
	sink.seed(program.Root, args)
	expanded, err := prepareAliasArgs(ctx, program.Root, args)
	if err != nil {
		resolveProgramTheme(program)
		return sink.exitCodeContext(ctx, err)
	}
	if !slices.Equal(expanded, args) {
		// Lazy adapters read this final flag layer only when invoked.
		applyGlobalFlags(expanded)
	}
	resolveProgramTheme(program)
	return execute(ctx, program, sink, expanded)
}

// resolveProgramTheme settles the settings theme before execution or early
// output. Conductor 0.1.16 exposes its runtime and clib 0.7.17 its renderer
// theme. Help callbacks retain the renderer pointer, so updating it preserves
// the assembled grammar and the renderer's options without rebuilding either.
func resolveProgramTheme(program *concobra.Program) {
	selected := cli.HelpTheme()
	program.Runtime.App.Theme = selected
	program.Runtime.Theme = selected
	program.Runtime.Renderer.Theme = selected.Init()
}

// execute runs one invocation with the guards that must fire before
// cobra parses anything. The selector seed makes every failure the
// binary can produce — flag parse, arg validation, unknown command —
// reach the machine envelope in machine modes (the uniform-grammar
// rule: --output means the same envelope shape on every path). The
// command-path check runs next, so an unknown subcommand is reported as
// itself rather than as whatever flag cobra would have parsed first.
func execute(ctx context.Context, program *concobra.Program, sink *failureSink, args []string) int {
	sink.seed(program.Root, args)
	if err := checkCommandPath(program.Root, args); err != nil {
		return sink.exitCodeContext(ctx, err)
	}
	return program.Run(ctx, args)
}

// NewProgram assembles the conductor program around the injected deps and
// streams. Exported for the audit inventory, which joins the REAL
// assembled tree against the metadata and schema registries — production
// entry stays Run.
func NewProgram(deps *Deps, stdout, stderr io.Writer) *concobra.Program {
	program, _ := newProgram(deps, stdout, stderr)
	return program
}

// newProgram builds the program and its failure sink. Conductor's
// adapter never calls SetOut/SetErr itself, so the write-ownership chain
// starts here: root owns the streams and commands reach them only
// through cmd.OutOrStdout()/ErrOrStderr(). Honest limit: the injected
// stderr covers cobra's own output — clog messages still go to the
// process stderr, because conductor wires clog's writer at ApplyFlags
// time and re-pointing it belongs to the logger work.
func newProgram(deps *Deps, stdout, stderr io.Writer) (*concobra.Program, *failureSink) {
	return newProgramWithTheme(deps, stdout, stderr, cli.HelpTheme)
}

// newProgramWithTheme retains one construction path while allowing startup to
// defer settings resolution until aliases have selected the invocation config.
func newProgramWithTheme(deps *Deps, stdout, stderr io.Writer, resolveTheme func() *theme.Theme) (*concobra.Program, *failureSink) {
	// Before conductor: its EnvPrefix option makes clog and clib re-read
	// the environment, and an unusable theme name has to be taken out of
	// their way first or they print about it themselves.
	cli.ConfigureTheme()

	sink := &failureSink{stdout: stdout, detect: deps.Detect}
	var notifier updater.Tool
	if deps.NotifyTool != nil {
		notifier = notificationDisplay{Tool: deps.NotifyTool}
	}
	var runtime *conductor.Runtime
	cli.WithLibraryThemeEnvironment(func() {
		runtime = conductor.New(conductor.App{
			Name:        name,
			Module:      "github.com/matcra587/jira-cli",
			Description: description,
			Updater:     notifier,
			// Keep local discovery and configuration free of release I/O. The
			// framework already skips update, version, help and completion.
			NotifySkip:    []string{"", "up", "agent", "guide", "config", "alias", "auth", "me", "adf", "jql", "open", "release-notes", "rn"},
			NotifyOptions: notificationOptions(deps, sink),
			// Supplying a theme is what avoids conductor calling
			// theme.Auto(), which asks the terminal for its background color
			// over OSC 11 and waits for an answer. A terminal that never
			// answers costs termenv's whole timeout: `version` measured at
			// 5108ms this way, and 21ms once a theme is known — on EVERY
			// command, not just the ones that draw something.
			//
			// So HelpTheme never returns nil, even when no theme is named:
			// it reads COLORFGBG, then falls back to the dark variant.
			// Guessing wrong costs colors tuned for the wrong background,
			// which is visible and fixed with JIRA_THEME. Asking costs five
			// seconds of nothing, with no clue why.
			Theme: resolveTheme(),
		})
	})

	// Without this, cobra runs only the NEAREST PersistentPreRunE — a
	// subcommand declaring its own would silently skip output resolution.
	// The hook-chaining note on newRoot stays as documentation; this makes
	// it structural.
	enableRunHookTraversal()

	quietLevelLabels()

	root := newRoot(deps, sink, runtime.Renderer)
	if deps.BindContext != nil {
		deps.BindContext(root.Context)
	}
	root.SetOut(stdout)
	root.SetErr(stderr)

	// The adapter defaults root.Short from App.Description; the renderer
	// only prints that blurb when asked, and a CLI that cannot say what it
	// is fails its own discovery bar. WithExitCode replaces conductor's
	// default mapper, which flattens every failure to exit 1 — the
	// taxonomy returns the published 0–8 contract instead. Inherited
	// flags stay visible on subcommand help: the output selector and the
	// standard trio are contract surface a leaf's reader needs, not
	// noise to hide.
	program := concobra.New(runtime, root,
		concobra.WithGenerator(func(gen *complete.Generator) {
			native.Configure(gen)
			normalizeCompletionHelp(gen)
			command.ForwardCompletion(root, gen)
		}),
		concobra.WithAlwaysShowDescription(),
		concobra.WithSections(clibcobra.WithShowInheritedFlagsOnSubcommands()),
		concobra.WithExitCode(sink.exitCode))
	guardNotifications(program)

	// Conductor's built-ins ship with zero clib metadata; the help
	// standard demands every flag carry it, so the composition root
	// annotates what it did not register itself.
	extendStandardFlags(program.Root)
	sealCompletionFlagValues(program.Root)

	// The completion command derives its script from the assembled
	// program — registered here, after concobra.New, because the
	// generator must see conductor's flags and every subcommand.
	program.Root.AddCommand(completion.New(program.Generator))

	// Settings inspection. Local-state only: it reads the config file and
	// the environment, and constructs no client — a command that reports
	// what is configured must work when nothing is.
	program.Root.AddCommand(configcmd.New(configcmd.Deps{Credentials: deps.Credentials, Resolve: deps.ResolveConfig, MigrationStores: deps.MigrationStores}))
	program.Root.AddCommand(aliascmd.New(aliascmd.Deps{
		ValidateName: func(name string) error { return validateAliasName(program.Root, name) },
		Validate: func(table map[string]string, name string) error {
			return validateAlias(program.Root, table, name)
		},
	}))
	program.Root.AddCommand(apicmd.New(apicmd.Deps{JiraClient: deps.JiraClient}))
	program.Root.AddCommand(adfcmd.New())
	program.Root.AddCommand(usercmd.New(usercmd.Deps{JiraClient: deps.JiraClient}))
	program.Root.AddCommand(jqlcmd.New(jqlcmd.Deps{JiraClient: deps.JiraClient, Preferences: deps.BuildPreferences, CacheFor: deps.CacheFor}))
	program.Root.AddCommand(epiccmd.New(epiccmd.Deps{JiraClient: deps.JiraClient}))
	workday := deps.WorkdaySeconds
	if workday == nil {
		workday = workdaySeconds
	}
	program.Root.AddCommand(worklogcmd.New(worklogcmd.Deps{JiraClient: deps.JiraClient, WorkdaySeconds: workday}))
	program.Root.AddCommand(cachecmd.New(cachecmd.Deps{
		Cache: func(ctx context.Context) (core.Cache, error) { return configuredCache(ctx, deps) },
		Primers: cachecmd.PrimeDeps{
			Scope: func(ctx context.Context, local bool) (cachecmd.MetadataScope, error) {
				return metadataCacheScope(ctx, deps, local)
			},
			JiraClient: deps.JiraClient, FetchBoards: boardscmd.FetchMetadata, DecodeBoards: boardscmd.DecodeMetadata,
		},
	}))
	program.Root.AddCommand(boardscmd.New(boardscmd.Deps{JiraClient: deps.JiraClient, Cache: deps.Cache}))
	program.Root.AddCommand(updatecmd.New(deps.Update))

	// Issues. The first domain handed a Jira client — which is what makes
	// "config list cannot open one" a property the type system holds,
	// not a convention.
	issueDeps := issuecmd.Deps{
		EditorCommand: deps.EditorCommand,
		EditFile:      deps.EditFile,
		Preferences:   deps.BuildPreferences,
		CacheFor:      deps.CacheFor,
		JiraClient:    deps.JiraClient,
		IssueMetadata: func(ctx context.Context) (issuecmd.InputDiscoveryClient, error) {
			if deps.JiraClient == nil {
				return nil, core.ErrIssueMetadataUnavailable
			}
			client, err := deps.JiraClient(ctx)
			if err != nil {
				return nil, err
			}
			reader, ok := client.(issuecmd.InputDiscoveryClient)
			if !ok {
				return nil, core.ErrIssueMetadataUnavailable
			}
			return reader, nil
		},
		Cache: deps.Cache,
		// The real dialog, as `auth logout` already gets below. A test
		// hands the command an answer instead.
		Confirm: prompt.Run,
	}
	if deps.BuildPreferences != nil {
		issueDeps.BrowseSite = func() (string, error) {
			prefs, err := deps.BuildPreferences()
			return prefs.BaseURL, err
		}
	}
	program.Root.AddCommand(issuecmd.New(issueDeps), issuecmd.NewOpen(issueDeps))

	// Credentials. Constructors, not instances: nothing opens the
	// keyring or a network client until a RunE that needs one runs.
	authDeps := authcmd.Deps{
		Credentials:          deps.Credentials,
		ClientFor:            deps.JiraClientFor,
		SiteForSave:          deps.IdentitySaveSite,
		SiteForLogin:         deps.LoginSite,
		CredentialsForLogin:  deps.LoginCredentials,
		PrepareLoginLocation: deps.PrepareLoginLocation,
		ClientForSave:        deps.IdentitySaveClientFor,
		Site:                 func(ctx context.Context) (authcmd.SiteInfo, error) { return siteFromDeps(ctx, deps) },
		SiteWithoutStore:     siteInfoWithoutStore,
		ValidateLoginScope:   validateLoginScope,
		IdentityTarget: func(ctx context.Context, scope core.AccountIdentityScope) (core.AccountIdentityTarget, error) {
			return config.OpenAccountIdentityTarget(ctx, "", scope)
		},
		LoginTarget: func(ctx context.Context, binding core.LoginIdentityBinding) (core.AccountIdentityTarget, error) {
			return config.OpenLoginIdentityTarget(ctx, config.Options{PermitMissingTarget: true}, binding)
		},
		// The real dialog. A test hands the command an answer instead.
		Confirm: prompt.Run,
	}
	if deps.SiteWithoutStore != nil {
		authDeps.SiteWithoutStore = deps.SiteWithoutStore
	}
	if deps.LoginTarget != nil {
		authDeps.LoginTarget = deps.LoginTarget
	}
	if deps.IdentityTarget != nil {
		authDeps.IdentityTarget = deps.IdentityTarget
	}
	if deps.ValidateLoginScope != nil {
		authDeps.ValidateLoginScope = deps.ValidateLoginScope
	}
	program.Root.AddCommand(authcmd.New(authDeps), authcmd.NewMe(authDeps))

	// Cobra adds the help command lazily inside Execute — AFTER the
	// pre-dispatch command-path check walks the tree, which would make
	// `jira help` read as an unknown command. Materialize it now
	// (idempotent; the audit inventory does the same for its walk).
	program.Root.InitDefaultHelpCmd()
	for _, child := range program.Root.Commands() {
		if child.Name() == "help" {
			child.Use = "help [COMMAND...]"
			child.Annotations = map[string]string{"clib": "dynamic-args='helppath'"}
			break
		}
	}
	guides, err := agentguides.Load()
	if err != nil {
		panic(err) // embedded guide violations are construction errors
	}
	agent := agentcmd.New(agentcmd.Deps{Guides: guides, Tree: func() (docent.Command, error) {
		return discovery.Build(program.Root)
	}})
	program.Root.AddCommand(agent, agentcmd.NewGuide(guides))
	agentcmd.SealPaths(agent, program.Root)

	// After the tree is complete: every arity failure leaves typed, so
	// arg validation carries a stable code like every other input error.
	typeArgErrors(program.Root)

	return program, sink
}

// completionFlagUnset is what a bare --@shell/--@complete resolves to.
// It must be non-empty — pflag only skips consuming the next token when
// NoOptDefVal is set — and it must name no real shell and no predictor
// kind, so a bare flag stays inert: the completion handler answers an
// unknown kind with nothing, per the predictor laws.
const completionFlagUnset = "unset"

// sealCompletionFlagValues stops clib's hidden completion flags from
// eating the token after them. Both take a value, so pflag reads a bare
// `--@shell --output=json` as "shell = --output=json": the output
// selector disappears and the command answers in a mode nobody asked
// for, with exit 0 and no warning.
//
// clib's own argv scanner already refuses a value beginning with "--"
// (complete/action_flags.go). The two parsers disagreeing on one argv is
// the defect; this brings the cobra side into line using pflag's own
// mechanism rather than special-casing argv in the selector seed, which
// is the code this rewrite has already had three bugs in.
//
// Divergence worth naming: clib accepts the space form `--@shell fish`
// and this does not. That path is inert here — clib's Preflight handles
// every real completion invocation before cobra parses, and returns
// early unless an ACTION flag is present, so cobra's copy is reachable
// only by a human typing it. The "=" form works either way.
func sealCompletionFlagValues(root *cobra.Command) {
	for _, name := range []string{complete.FlagComplete, complete.FlagShell} {
		if f := root.PersistentFlags().Lookup(name); f != nil {
			f.NoOptDefVal = completionFlagUnset
		}
	}
}

// quietLevelLabels drops the level column from human status output, so a
// line reads "❌ unknown shell" rather than "ERR ❌ unknown shell". The
// symbol already carries the severity, and a terminal-first CLI talking
// to a person should not read like a server log. Debug is included: its
// own symbol distinguishes it, and a level column that appeared only
// under --verbose would make the quiet form the odd one out.
//
// Parts are filtered out of DefaultParts rather than listed, so a part
// clog adds later keeps its place and its ordering instead of silently
// vanishing from our output. This runs at construction, not in a run
// hook, because the pre-parse failure path logs after cobra unwinds —
// hooking it later would leave exactly the errors a user hits still
// carrying a level column.
func quietLevelLabels() {
	parts := slices.DeleteFunc(clog.DefaultParts(), func(p clog.Part) bool {
		return p == clog.PartLevel
	})
	clog.SetParts(parts...)

	// One spinner identity for the whole tool: braille Dots —
	// single-cell, smooth, no emoji. The default preset is emoji-based,
	// which carries width hazards in a terminal and reads as toy-ish; a
	// Jira CLI wants the plain, professional glyphs.
	clog.SetSpinnerDefaults(spinner.WithConfig(spinner.Dots))
}

// colorFlagName is the flag conductor registers for the --color mode —
// named once so extendStandardFlags and colorModeFromFlags cannot drift
// on the string literal.
const colorFlagName = "color"

// extendStandardFlags attaches help metadata to the flags conductor
// registered for us: the standard trio on the persistent set and the
// -V/--version flag on the root's local set. The @global role group
// bands them below the output selector (band 1) in every command's
// help; -V sits last before help's own entries.
func extendStandardFlags(root *cobra.Command) {
	// A missing flag means conductor stopped registering the built-in —
	// silently skipping would strand the metadata contract, so lookup
	// failure is loud like every other startup wiring failure.
	lookup := func(fs *pflag.FlagSet, name string) *pflag.Flag {
		f := fs.Lookup(name)
		if f == nil {
			panic("root: conductor standard flag --" + name + " is missing — extendStandardFlags is out of date")
		}
		return f
	}
	pf := root.PersistentFlags()
	// The counter takes an optional level (-v, -vv, --verbose=2); pflag
	// cannot say "optional", so the placeholder just names the value.
	clibcobra.Extend(lookup(pf, "verbose"), clibcobra.FlagExtra{
		Terse:       "Verbosity",
		Group:       help.GlobalSection + "/2",
		Placeholder: "n",
	})
	clibcobra.Extend(lookup(pf, "quiet"), clibcobra.FlagExtra{
		Terse: "Quiet",
		Group: help.GlobalSection + "/2",
	})
	// Enum values live in metadata, not prose (help standard §1.6):
	// conductor's usage string enumerates them, so it is rewritten
	// before the metadata takes over rendering them.
	color := lookup(pf, colorFlagName)
	color.Usage = "When to use color"
	clibcobra.Extend(color, clibcobra.FlagExtra{
		Terse:       "Color mode",
		Group:       help.GlobalSection + "/2",
		Placeholder: "when",
		Enum: []string{
			clog.ColorAuto.String(), clog.ColorAlways.String(), clog.ColorNever.String(),
		},
		EnumTerse: []string{
			"Color when stderr is a terminal",
			"Force color and hyperlinks",
			"Disable color and hyperlinks",
		},
		EnumDefault: clog.ColorAuto.String(),
	})
	clibcobra.Extend(lookup(root.Flags(), "version"), clibcobra.FlagExtra{
		Terse: "Version",
		Group: help.GlobalSection + "/3",
	})
}

// colorModeFromFlags reads the parsed --color flag as a clog.ColorMode.
//
// conductor owns the flag's Value (registered via its own Flags type),
// so this reads back through the flag's String()/UnmarshalText round
// trip — the same pattern ResolveSelector uses for the output selector —
// rather than assuming a concrete pflag Value type. A flag conductor did
// not register (a wiring regression) or a value neither side produced
// leaves the mode at ColorAuto, the same safe default an unset flag
// already resolves to.
func colorModeFromFlags(fs *pflag.FlagSet) clog.ColorMode {
	f := fs.Lookup(colorFlagName)
	if f == nil {
		return clog.ColorAuto
	}
	var mode clog.ColorMode
	if err := mode.UnmarshalText([]byte(f.Value.String())); err != nil {
		return clog.ColorAuto
	}
	return mode
}

// failureSink carries what the exit boundary needs from output resolution:
// conductor's exit mapper receives only the error, so the resolved format,
// the operation name, and the stdout writer ride here. One invocation per
// process — plain fields, no locking.
type failureSink struct {
	stdout   io.Writer
	detect   func() cli.Detection
	format   output.Format
	op       string
	command  *cobra.Command
	resolved bool
}

// seed resolves the output selector from the RAW argv, before cobra has
// parsed anything, so failures that fire before PersistentPreRunE (flag
// parse, arg validation, the unknown-command check) still reach the
// machine envelope in machine modes. "A pre-resolution failure has no
// known format" was a false premise: the selector is right there in the
// argv. The probe folds through the SAME single resolution point
// (cli.ResolveSelector) as the real parse, so the two cannot drift; when
// PersistentPreRunE later runs, its post-parse values overwrite these.
// An unresolvable selector (invalid --output value, conflicting aliases)
// still seeds the full json frame: the caller asked for machine output
// and got the request itself wrong — answering with bare stderr prose
// would make exactly that failure unparseable.
func (s *failureSink) seed(root *cobra.Command, args []string) {
	// Best-effort operation attribution: the command the argv names, or
	// the root when it names none (the unknown-command path).
	s.op = "root"
	s.command = nil
	if cmd, _, err := root.Find(args); err == nil {
		s.op = opName(cmd)
		s.command = cmd
	}
	probe := pflag.NewFlagSet("selector-probe", pflag.ContinueOnError)
	probe.ParseErrorsAllowlist.UnknownFlags = true
	probe.Usage = func() {}
	probe.SetOutput(io.Discard)
	probe.String(cli.FlagOutput, string(cli.OutputAuto), "")
	for _, alias := range cli.OutputAliases {
		probe.Bool(alias.Flag, false, "")
	}
	// A probe parse error is ignored: pflag records the selector flags it
	// scanned before failing, and whatever broke the parse is about to be
	// reported through the very boundary this seed is preparing.
	probe.Parse(args) //nolint:errcheck // partial parsing still records the selector flags
	mode, err := cli.ResolveSelector(probe)
	if err != nil {
		s.format = output.FormatJSON
		s.resolved = true
		return
	}
	s.format = cli.ResolveOutputMode(mode, s.detect)
	s.resolved = true
}

// exitCode: Conductor failure boundary, after Cobra unwinds.
// Machine frame -> stdout. Human error/hint -> stderr.
// seed resolves format before parse. Failure frames ignore --jq.
// No live invocation context here; use exitCodeContext when available.
func (s *failureSink) exitCode(err error) int {
	return s.exitCodeContext(context.Background(), err)
}

func (s *failureSink) exitCodeContext(ctx context.Context, err error) int {
	// Read observations from Cobra's completed invocation; rendering still uses
	// the caller's context. Conductor calls this after the command unwinds.
	observationContext := ctx //nolint:contextcheck // This context selects retained facts, not new work.
	if s.command != nil {
		s.op = opName(s.command)
		if commandContext := s.command.Context(); commandContext != nil {
			observationContext = commandContext
		}
	}
	requestWarnings := cli.TakeRequestWarnings(observationContext)
	e := cli.MapError(err)
	var resultErr *cli.ResultError
	if errors.As(err, &resultErr) && resultErr.Mutation {
		e.Hint = "Inspect retained data and verify mutation outcomes before retrying. " + e.Hint
	}
	var filterErr *output.FilterError
	if errors.As(err, &filterErr) {
		e.Hint = "Filtering failed after the command produced its result. Inspect unfiltered `data` in this error frame; check mutation outcomes before retrying."
	}
	exit := e.Exit
	// Human failures print through clog here rather than through
	// Render, so the warnings have to be emitted here too or a person
	// sees the error without the state it happened in.
	if s.format == output.FormatHuman {
		command.EmitWarnings()
		for _, warning := range requestWarnings {
			clog.Warn().Msg(warning.Message)
		}
		if resultErr != nil {
			for _, warning := range resultErr.Warnings {
				clog.Warn().Msg(warning.Message)
			}
		}
	}
	if s.resolved && (s.format != output.FormatHuman || (resultErr != nil && resultErr.Human != nil)) {
		in := output.Inputs{
			Op: s.op,
			// A failing command carries its startup warnings too: they
			// describe the state it failed in, and dropping them here
			// would make a bad theme visible only when nothing went
			// wrong — which is the least useful moment for it.
			Warnings: append(cli.TakeWarnings(), requestWarnings...),
			Errors: []envelope.Error{{
				Code:              e.Code,
				Message:           e.Message,
				Hint:              e.Hint,
				Next:              e.Next,
				Doc:               e.Doc,
				Field:             e.Field,
				Suggestions:       e.Suggestions,
				RetryAfterSeconds: e.RetryAfterSeconds,
				Attempts:          e.Attempts,
				WaitedMS:          int(e.Waited.Milliseconds()),
			}},
			ExitCode:  e.Exit,
			Now:       time.Now(),
			RequestID: output.NewRequestID(),
			ColorMode: cli.ResolvedColorMode(),
		}
		if resultErr != nil {
			in.Warnings = append(in.Warnings, resultErr.Warnings...)
			in.Op = resultErr.Op
			in.Data = resultErr.Data
			in.Human = resultErr.Human
		}
		if filterErr != nil {
			in.Op = filterErr.Envelope.Meta.Command
			in.Data = filterErr.Envelope.Data
			in.Warnings = append(filterErr.Envelope.Warnings, in.Warnings...)
			in.RequestID = filterErr.Envelope.Meta.RequestID
		}
		res := output.Assemble(in)
		var werr error
		if s.format == output.FormatHuman {
			werr = output.RenderFailureReceipt(ctx, s.stdout, res)
		} else {
			werr = output.Render(ctx, s.stdout, s.format, res)
		}
		if werr != nil {
			// stdout is gone — the write failure supersedes the original
			// error's exit, per the output_write_failed contract.
			clog.Error().Msg("write failure output: " + cli.Text(werr.Error()))
			exit = cli.ExitOutput
		}
	}
	if s.resolved && s.format != output.FormatHuman {
		return exit
	}
	// Human diagnostics: same terminal sinks as Jira fields.
	clog.Error().Msg(cli.Text(e.Message))
	// Show one route forward, not two. A suggestion IS the answer; the
	// hint's job is to recover a reader who has no lead, so printing
	// "run --help to see the choices" above "Did you mean zsh?" buries
	// the strong advice under the weak. The envelope still carries both
	// fields — this is the human renderer choosing what earns a line,
	// not the contract dropping data.
	switch {
	case len(e.Suggestions) > 0:
		clog.Hint().Msg("Did you mean " + cli.Text(strings.Join(e.Suggestions, " or ")) + "?")
	case e.Hint != "":
		clog.Hint().Msg(cli.Text(e.Hint))
	}
	return exit
}

// opName turns a cobra command path into the dotted operation name the
// envelope carries ("jira issue view" → "issue.view"; the bare root is
// "root").
func opName(cmd *cobra.Command) string {
	if selected, ok := command.SelectedVariantMeta(cmd); ok {
		return selected.Op
	}
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) <= 1 {
		return "root"
	}
	return strings.Join(parts[1:], ".")
}

// newRoot returns the root command with the output selector wired.
// Subcommands register here as they land, closing over deps for
// the adapters they need; the root command itself never constructs a
// client, so help and version stay client-free.
func newRoot(deps *Deps, sink *failureSink, renderer *help.Renderer) *cobra.Command {
	// Root is a typed read operation over the fully assembled tree. Native
	// human help and machine discovery share construction and output policy.
	var cmd *cobra.Command
	cmd = command.MustNew(command.Spec[envelope.AgentSchemaOutput]{
		Op: "root", Use: name, Short: description, Long: description,
		Passthrough: true,
		HumanHelp:   func(w io.Writer) error { return renderBareHelp(cmd, renderer, w) },
		Example: "$ jira --output=json\n\n" +
			"$ jira --output=compact --jq '.schema.children[].path'\n\n" +
			"$ jira --output=json --jq '.data.schema.extensions.output_contract'",
		Run: func(ctx context.Context, _ command.Invocation) (envelope.AgentSchemaOutput, error) {
			if err := ctx.Err(); err != nil {
				return envelope.AgentSchemaOutput{}, err
			}
			tree, err := discovery.Build(cmd)
			if err != nil {
				return envelope.AgentSchemaOutput{}, err
			}
			return discovery.Schema(tree, "", false)
		},
	})
	// Cobra's typo suggestions use the same distance as flag corrections.
	cmd.SuggestionsMinimumDistance = 2
	cmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		// Resolved before the output selector: it depends on nothing
		// below it, and every human surface — including an error path
		// that never reaches the selector at all — should see a
		// settled color decision.
		colorMode := colorModeFromFlags(cmd.Root().PersistentFlags())
		cli.SetResolvedColorMode(colorMode)
		cli.SetStyleEnabled(cli.ResolveColorMode(cmd.OutOrStdout(), colorMode))

		mode, err := cli.ResolveSelector(cmd.Root().PersistentFlags())
		if err != nil {
			return err
		}
		filter, mode, err := resolveFilter(cmd.Root().PersistentFlags(), mode)
		if err != nil {
			return err
		}
		format := cli.ResolveOutputMode(mode, deps.Detect)
		// Startup warnings describe the state the command is about
		// to run IN, so a person sees them before it runs — not
		// after, and not behind an interactive form that opens
		// first. Machine modes leave them for the envelope, where
		// warnings[] is the contract — with one gap: compact on
		// SUCCESS is bare data, which has nowhere to put a
		// warning, so it drops them. That is design C1, and it
		// means a startup warning reaches an agent only when the
		// command also fails or the caller asked for json.
		if format == output.FormatHuman {
			command.EmitWarnings()
		}
		ctx := cli.WithFormat(core.WithRateLimitObservation(cmd.Context()), format)
		browseOnly := cmd.CommandPath() == cmd.Root().Name()+" open"
		if cmd.Flags().Lookup("web") != nil {
			web, err := cmd.Flags().GetBool("web")
			if err != nil {
				return err
			}
			browseOnly = browseOnly || web
		}
		ctx = command.WithIssueKeyRecorder(ctx, func(keys []string) {
			constructor := deps.Cache
			if browseOnly {
				// A local URL must not open credentials merely to record history.
				constructor = func(ctx context.Context) (core.Cache, error) { return configuredCache(ctx, deps) }
			}
			if constructor == nil {
				return
			}
			store, err := constructor(ctx)
			if err == nil && store != nil {
				_ = cachecmd.RecordIssueKeys(store, keys) //nolint:errcheck // Disposable completion history cannot fail the observed operation.
			}
		})
		if filter != nil {
			ctx = cli.WithFilter(ctx, filter)
		}
		cmd.SetContext(ctx)
		// The exit boundary renders failures AFTER cobra unwinds, when
		// the command context is out of reach — hand it what it needs
		// now.
		sink.format = format
		sink.op = opName(cmd)
		sink.command = cmd
		sink.resolved = true
		return nil
	}
	// Every pflag parse failure leaves the dispatch layer typed — code,
	// offending flag, candidates — instead of cobra's untyped string.
	// Set on the root; cobra resolves the hook upward from any child.
	cmd.SetFlagErrorFunc(newFlagError)
	command.AddOutputFlags(cmd)
	command.AddGlobalFlags(cmd)
	cmd.AddCommand(version.New())
	cmd.AddCommand(releasenotes.New())
	return cmd
}

// resolveFilter compiles --jq and settles how it interacts with the
// output selector, returning the filter (nil when the flag is absent)
// and the mode the invocation should run in.
//
// Explicit `--output=human` with --jq is a loud conflict rather than a
// silent override: the caller stated a mode, and quietly answering in a
// different one is exactly the kind of surprise the output contract
// exists to prevent. An unset selector (auto) is not a statement, so it
// resolves to json — the only shape a filter can be written against.
// That deliberately overrides agent DETECTION too: a jq expression is
// authored against a document shape, and that shape must not flip based
// on whether an env var happens to be set.
//
// Compilation happens here, before any network work, so a malformed
// expression fails the command instead of surfacing after a mutation
// has already run.
func resolveFilter(fs *pflag.FlagSet, mode cli.OutputMode) (*output.Filter, cli.OutputMode, error) {
	vars, err := parseFilterVars(fs)
	if err != nil {
		return nil, mode, err
	}
	if !fs.Changed(cli.FlagJQ) {
		// Bindings with no expression to bind into are a real flag used
		// where it can take no effect — the same class as a completion
		// action given --output, and the same code.
		if len(vars) > 0 {
			// Field names the flag the caller actually passed; saying
			// "jq-arg" when only --jq-argjson was given points an agent at
			// a flag absent from its command line.
			offender := cli.FlagJQArg
			if !fs.Changed(cli.FlagJQArg) {
				offender = cli.FlagJQArgJSON
			}
			e := cli.NewInputError(cli.CodeFlagNotApplicable,
				"--"+cli.FlagJQArg+"/--"+cli.FlagJQArgJSON+" bind values for --jq, but no --jq expression was given")
			e.Field = offender
			return nil, mode, e
		}
		return nil, mode, nil
	}
	src, err := fs.GetString(cli.FlagJQ)
	if err != nil {
		return nil, mode, err
	}
	if mode == cli.OutputHuman {
		return nil, mode, cli.NewInputError(cli.CodeJQOutputConflict,
			"--jq filters machine output, but --output=human was requested")
	}
	filter, err := output.CompileFilter(src, vars...)
	if err != nil {
		e := cli.NewInputError(cli.CodeJQExpressionInvalid, err.Error())
		e.Field = cli.FlagJQ
		return nil, mode, e
	}
	if mode == cli.OutputAuto {
		mode = cli.OutputJSON
	}
	return filter, mode, nil
}

// parseFilterVars reads the --jq-arg / --jq-argjson pairs into bindings
// the expression reaches as $name.
//
// jq spells these `--arg name value`, two tokens; pflag has no way to
// consume two, so the spelling here is `name=value` split on the FIRST
// "=" — a value may contain more, and a name may not.
//
// Both flags share one namespace, and a duplicate name is rejected
// rather than last-wins: silently dropping one of two bindings a caller
// deliberately wrote is the kind of quiet wrong answer this contract
// exists to avoid.
func parseFilterVars(fs *pflag.FlagSet) ([]output.FilterVar, error) {
	var vars []output.FilterVar
	seen := map[string]bool{}

	read := func(flag string, decode func(string) (any, error)) error {
		pairs, err := fs.GetStringArray(flag)
		if err != nil {
			return err
		}
		for _, pair := range pairs {
			name, raw, ok := strings.Cut(pair, "=")
			if !ok || !validJQVarName(name) {
				e := cli.NewInputError(cli.CodeFlagValueInvalid,
					"--"+flag+" takes `name=value` where name is a jq variable "+
						"([A-Za-z_][A-Za-z0-9_]*), but got "+strconv.Quote(pair))
				e.Field = flag
				return e
			}
			if seen[name] {
				e := cli.NewInputError(cli.CodeFlagValueInvalid,
					"jq variable $"+name+" was bound more than once")
				e.Field = flag
				return e
			}
			value, err := decode(raw)
			if err != nil {
				e := cli.NewInputError(cli.CodeFlagValueInvalid,
					"--"+flag+" value for $"+name+" is not valid JSON: "+err.Error())
				e.Field = flag
				return e
			}
			seen[name] = true
			vars = append(vars, output.FilterVar{Name: name, Value: value})
		}
		return nil
	}

	if err := read(cli.FlagJQArg, func(s string) (any, error) { return s, nil }); err != nil {
		return nil, err
	}
	// Decoded through the output layer's number-preserving path, not
	// plain json.Unmarshal: the latter widens every number to float64, so
	// a large Jira identifier bound as a variable would arrive silently
	// altered.
	if err := read(cli.FlagJQArgJSON, func(s string) (any, error) {
		return output.DecodeJSONNumbers([]byte(s))
	}); err != nil {
		return nil, err
	}
	return vars, nil
}

// validJQVarName reports whether name is a jq variable identifier.
//
// gojq's compiler accepts only [A-Za-z_][A-Za-z0-9_]*, and rejecting a
// bad one HERE is what keeps the blame straight: passed through, the
// compile failure surfaces as jq_expression_invalid against the --jq
// expression, telling the caller to fix an expression that was fine
// while the actual mistake sat in --jq-arg.
func validJQVarName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}
