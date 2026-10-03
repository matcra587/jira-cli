package command

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"

	"github.com/gechr/x/terminal"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// Invocation is what a handler is told about the run it is part of.
//
// Facts, never capabilities. Anything here can be READ; nothing here
// lets a command DO something it was not handed. That is the test for
// adding a field: a Jira client would be a capability and belongs in
// the command's own Deps, closed over by its constructor, so a command
// that was never given one cannot reach it.
//
// Named for what it describes rather than called Ctx, because a handler
// already takes a context.Context and two things called ctx meaning
// different things is a reliable way to write the wrong one.
type Invocation struct {
	// Version is the resolved version string, the same one -V prints.
	//
	// A value that belongs to the command TREE rather than to any
	// dependency, copied in by the path. Handlers never hold the
	// *cobra.Command it came from: that would hand every one of them
	// OutOrStdout, and the single reason Run returns data is that it
	// cannot reach a stream.
	Version string

	// Stdin is the command's input.
	//
	// Carried rather than taken from the process so a test can supply a
	// buffer — `auth login --with-token` reads its secret from here, and
	// a handler holding the command instead would also hold the writers
	// this design keeps out of reach.
	Stdin io.Reader

	// StdinIsTerminal reports whether input is a terminal, which is a
	// narrower question than Interactive: a token can be piped in while
	// there is still nobody to answer a prompt.
	StdinIsTerminal bool

	// DryRun is the resolved --dry-run flag, false for a command that
	// does not register one.
	//
	// Read from the flag the path itself registered. A handler that
	// bound its own local instead is how the first mutation converted
	// here went silently live: `config edit` declared a dryRun variable
	// that nothing populated, so --dry-run read false and the preview
	// edited the file. The audit matrix caught it; nothing else did.
	//
	// A mutation that registers no flag at all cannot be built — New
	// refuses it — so this reading false always means the caller did not
	// ask for a preview, never that the question could not be asked.
	DryRun bool

	// Interactive reports whether there is a person to ask.
	//
	// Agent detection beats a TTY, the same rule output resolution
	// follows: a harness that allocates a PTY passes every terminal
	// check and would then sit in front of a form it cannot drive.
	// Stderr rather than stdout, because that is where a prompt is
	// drawn — testing stdout would refuse `jira x --output=json > f`
	// from a real terminal, the exact invocation the stderr routing
	// exists to allow.
	//
	// A fact about this run, resolved once by the path. Commands used to
	// probe the process directly from five separate places, one of them
	// reaching around the detection seam the root already injects, which
	// is why none of those branches could be tested.
	Interactive bool

	// NoInput reports that the caller passed --no-input.
	//
	// Interactive is already false whenever this is true, so nothing needs
	// this to DECIDE whether to prompt. It exists so a refusal can say the
	// right thing: "there is no terminal here to ask at" is accurate for a
	// pipe and false for someone who typed --no-input at a perfectly good
	// terminal, and an error that misdescribes the situation sends the
	// reader looking for a problem they do not have.
	NoInput bool

	// Args are the positional arguments cobra accepted, after the
	// ArgSpec's arity check passed.
	//
	// A fact about this run like the rest of this struct, and the only
	// route a handler has to them: RunE takes cobra's slice and copies
	// it here rather than handing the handler the command. Without this
	// an ArgSpec could declare an issue key, cobra would validate it,
	// and the handler that exists to USE it could not read it — which
	// is exactly what shipped until a review caught it.
	Args []string
}

// Spec declares a command completely: what it is, what it accepts, and
// what it produces.
//
// Only three things are ever per-command — the flags, the data shape,
// and the handler body. Everything else the contract needs is derived
// from this declaration, so it cannot be forgotten by a command that
// forgets to call something.
type Spec[T any] struct {
	// Op is the dotted operation name the envelope carries as
	// meta.command ("version", "issue.view"). Mandatory.
	Op string
	// WritesJira and MutatesLocalState classify the operation. A command
	// declaring either is a mutation: it must register --dry-run, and
	// New refuses it otherwise. Neither set is a read.
	WritesJira        bool
	MutatesLocalState bool
	// Identity, Keyed and Passthrough declare envelope-shape properties
	// the audit inventory cross-checks against the registered type. See
	// Meta for what each gates.
	Identity    bool
	Keyed       bool
	Passthrough bool
	// NoSuccessEnvelope, when non-empty, is the documented reason this
	// command's success emits no envelope. A path command never sets it;
	// it is for the hand-written exceptions this path does not build.
	NoSuccessEnvelope string
	// Variants are other operations this command path reports as,
	// declared so the audit inventory and applicability map see them.
	// Declaring one does NOT change what an invocation emits: the
	// envelope always carries Op. Kept as []Meta because a variant is a
	// whole operation with its own Variant name.
	Variants []Meta

	// Use, Short, Long and Example are the help surface. Short is
	// mandatory — an empty one drops the command out of grouped help,
	// which the inventory walk fails on. They live here rather than
	// being assigned onto the returned command, because assigning them
	// afterwards is the bypass this path exists to close.
	Use     string
	Aliases []string
	Short   string
	Long    string
	Example string

	// Args declares the positional surface. The zero value means the
	// command takes none.
	Args ArgSpec

	// Flags registers this command's own flags. The only per-command
	// surface besides the handler and the data shape.
	//
	// It takes the COMMAND, not its flag set, because every register-
	// and-extend helper does — AddStringVar and friends attach clib
	// metadata as well as declaring the flag, and metadata is
	// mandatory. Handing over a bare *pflag.FlagSet made the helpers
	// uncallable from here and left raw pflag declarations, which
	// ruleguard rejects, as the only way to declare anything. A
	// converted command could not have a flag at all; version has none,
	// so nothing noticed.
	Flags func(*cobra.Command)

	// Human renders the success payload and any retained failure receipt for
	// a person. The root still owns failure diagnostics. Carried on the
	// result rather than registered by name: the op-keyed registry
	// panics on a duplicate and lives for the process, so registering
	// here would die the second time a command tree is built.
	Human output.HumanRenderFunc
	// PageHuman enables shared document paging and its --no-pager flag.
	// Only interactive human output can page; handlers still return typed data.
	PageHuman bool

	// HumanHelp uses the framework's native help for a human invocation,
	// before Run, while machine modes still receive Run's typed result.
	// Only read operations may use it. The shared path owns the help writer
	// and propagates destination failures, including errors Cobra discards.
	HumanHelp func(io.Writer) error

	// RetainOnError reports whether Run's typed data contains an inspectable
	// receipt when it returns an error. Nil preserves the ordinary no-data
	// failure path. The predicate distinguishes completed outcomes from a
	// zero result returned before the operation starts, without reflection or
	// requiring handlers to serialize or write their own error envelopes.
	RetainOnError func(T) bool

	// Warnings derives non-fatal diagnostics from typed results. The shared
	// path carries them through success and retained-failure envelopes alike.
	Warnings func(T) []envelope.Warning

	// Validate rejects bad input BEFORE any I/O happens, so a request
	// that cannot succeed never reaches Jira and never pays for a
	// client. Optional.
	Validate func() error

	// Run produces the operation's DATA. It has no writer in scope and
	// that is the whole design: a handler that cannot reach a stream
	// cannot bypass the envelope, so --output, --jq, the theme and the
	// coloring all follow from the returned value rather than from the
	// author remembering a helper.
	Run func(ctx context.Context, inv Invocation) (T, error)
}

// meta assembles the operation metadata from the spec's flat fields.
// One source, one assembler — the Spec literal is where a command
// declares its operation, and nothing else reconstructs it.
func (s Spec[T]) meta() Meta {
	return Meta{
		Op:                s.Op,
		Args:              &s.Args,
		WritesJira:        s.WritesJira,
		MutatesLocalState: s.MutatesLocalState,
		Identity:          s.Identity,
		Keyed:             s.Keyed,
		Passthrough:       s.Passthrough,
		NoSuccessEnvelope: s.NoSuccessEnvelope,
	}
}

// New builds the cobra command this spec describes, reporting a wiring
// mistake rather than panicking on one.
//
// It exists so the failure path is testable without recover(); call
// sites use MustNew. The stdlib pairs Must with a plain sibling for the
// same reason (regexp.Compile / regexp.MustCompile).
func New[T any](spec Spec[T]) (*cobra.Command, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}

	var noPager bool
	cmd := &cobra.Command{
		Use:     spec.Use,
		Aliases: spec.Aliases,
		Short:   spec.Short,
		Long:    spec.Long,
		Example: spec.Example,
		Args:    spec.Args.validator(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format, ok := cli.FormatFromContext(cmd.Context()); spec.HumanHelp != nil && ok && format == output.FormatHuman {
				return output.RenderHelp(cmd.Context(), cmd.ErrOrStderr(), spec.HumanHelp)
			}
			if spec.Validate != nil {
				if err := spec.Validate(); err != nil {
					return err
				}
			}
			data, err := spec.Run(cmd.Context(), Invocation{
				Args:            args,
				Stdin:           cmd.InOrStdin(),
				StdinIsTerminal: stdinIsTerminal(cmd),
				DryRun:          dryRunFlag(cmd),
				Interactive:     interactive(cmd),
				NoInput:         noInput(cmd),
				// Read at run time from the field cobra's -V template
				// prints, so the flag and the command cannot disagree.
				Version: cmd.Root().Version,
			})
			var warnings []envelope.Warning
			if spec.Warnings != nil {
				warnings = spec.Warnings(data)
			}
			if err != nil {
				if spec.RetainOnError != nil && spec.RetainOnError(data) {
					return &cli.ResultError{Err: err, Op: spec.Op, Data: data, Human: spec.Human, Warnings: warnings, Mutation: spec.meta().Class() != ClassRead}
				}
				return err
			}
			return writeEnvelope(cmd, spec.Op, data, warnings, spec.Human, spec.PageHuman && !noPager && interactive(cmd))
		},
	}

	if spec.Flags != nil {
		spec.Flags(cmd)
	}
	if spec.PageHuman {
		AddBoolVar(cmd, &noPager, FlagNoPager, false, "Print directly instead of paging long human output", FlagOpts{Terse: "Print without pager"})
	}
	predictors, err := spec.Args.annotation()
	if err != nil {
		return nil, err
	}
	if predictors != "" {
		// Merged, not assigned. Nothing else sets an annotation today,
		// so overwriting was harmless — and would stay harmless right up
		// until the first thing that does, at which point it would drop
		// that entry with no error anywhere.
		if cmd.Annotations == nil {
			cmd.Annotations = map[string]string{}
		}
		cmd.Annotations["clib"] = predictors
	}
	// A mutation must be previewable. The rules doc's answer to a
	// missing flag was to fail SAFE at run time — assume dry-run is ON
	// when the flag cannot be read — which is the right instinct and the
	// weaker mechanism: it protects the run that already shipped the
	// mistake. Here the flag is registered by the same declaration that
	// says the command mutates, so the two cannot disagree, and a
	// mutation without a preview never reaches a tree.
	//
	// A read is untouched: it has nothing to preview, and assuming
	// dry-run for one would be nonsense rather than safety.
	meta := spec.meta()
	if meta.Class() != ClassRead && cmd.Flags().Lookup(cli.FlagDryRun) == nil {
		return nil, fmt.Errorf("command: %q mutates but registers no --dry-run flag; a mutation with no preview cannot be checked before it runs", meta.Op)
	}
	// Keyed is declared in the Spec (it gates the keyed-results contract
	// and the audit's M3 axis) while the keyed surface is registered in
	// Flags — two statements about one fact, which nothing kept together
	// until here. Both directions are refused: a command promising
	// results[] without the shared surface, and one binding
	// --parallelism while telling the audit it returns a single result.
	switch keyed := declaresKeyedFlags(cmd); {
	case meta.Keyed && !keyed:
		return nil, fmt.Errorf("command: %q declares Keyed but never calls command.AddKeyedFlags; a keyed command's shared surface is not optional", meta.Op)
	case !meta.Keyed && keyed:
		return nil, fmt.Errorf("command: %q calls command.AddKeyedFlags but does not declare Keyed; its results[] contract would go unchecked", meta.Op)
	}

	SetMeta(cmd, meta, spec.Variants...)
	if spec.Human != nil || spec.HumanHelp != nil {
		recordHuman(cmd)
	}
	return cmd, nil
}

// MustNew is New for a call site that cannot handle failure: building
// the command tree. Every error it reports is a wiring mistake in code,
// caught before the program can run rather than by whoever runs it.
func MustNew[T any](spec Spec[T]) *cobra.Command {
	cmd, err := New(spec)
	if err != nil {
		panic(err)
	}
	return cmd
}

// validate rejects a spec that could not produce a working command.
//
// The registry check is the reason this runs at construction: an
// operation's Output shape is registered in a runtime map keyed by the
// op string, so the language cannot check that T matches. Doing it here
// means every run and every test meets a mistake, and the audit
// inventory builds the whole tree — so CI sees it long before a user
// could.
func (s Spec[T]) validate() error {
	switch {
	case s.HumanHelp != nil && (s.Human != nil || s.meta().Class() != ClassRead):
		return fmt.Errorf("command: %q native help requires a read operation without a bespoke human renderer", s.Op)
	case s.Op == "":
		return fmt.Errorf("command: spec has no operation name")
	case s.Use == "":
		return fmt.Errorf("command: %q has no Use", s.Op)
	case s.Short == "":
		// An empty Short drops the command out of grouped help
		// entirely, which is a silent disappearance rather than a
		// visible fault.
		return fmt.Errorf("command: %q has no Short; it would vanish from grouped help", s.Op)
	case s.Run == nil:
		return fmt.Errorf("command: %q has no Run", s.Op)
	}

	reg, ok := envelope.Registered(s.Op)
	if !ok {
		return fmt.Errorf("command: %q has no envelope registration; register its Output struct in internal/envelope", s.Op)
	}
	var zero T
	if got, want := reflect.TypeOf(zero), reflect.TypeOf(reg.Sample); got != want {
		return fmt.Errorf("command: %q returns %v but registered %v", s.Op, got, want)
	}
	// Variants get the same check. They do not select what is emitted,
	// but the inventory publishes them as operations this command
	// reports, and one registered under a different Output type would
	// promise a shape nothing here can produce.
	for _, v := range s.Variants {
		vreg, ok := envelope.Registered(v.Op)
		if !ok {
			return fmt.Errorf("command: variant %q of %q has no envelope registration", v.Op, s.Op)
		}
		if got, want := reflect.TypeOf(zero), reflect.TypeOf(vreg.Sample); got != want {
			return fmt.Errorf("command: variant %q returns %v but registered %v", v.Op, got, want)
		}
	}
	return nil
}

// dryRunFlag reads the --dry-run flag when the command registered one.
//
// A command with no such flag is not a mutation and cannot be previewed,
// so false is the only answer — and reading a flag that does not exist
// is a wiring question, not a runtime one. GetBool already reports an
// undefined flag as an error, so the miss needs no separate lookup.
func dryRunFlag(cmd *cobra.Command) bool {
	on, err := cmd.Flags().GetBool(cli.FlagDryRun)
	return err == nil && on
}

// stdinIsTerminal reports whether the command's input is the process's
// own terminal. Only the real os.Stdin can be one — a test injects a
// buffer, which is correctly treated as piped input.
func stdinIsTerminal(cmd *cobra.Command) bool {
	return cmd.InOrStdin() == os.Stdin && terminal.Is(os.Stdin)
}

// interactive reports whether a person can answer a prompt.
//
// Kept here so every command gets one answer rather than each probing
// the process for itself. Built ON stdinIsTerminal because being asked
// is the strictly narrower case: someone who can answer must also be
// typing at a terminal.
func interactive(cmd *cobra.Command) bool {
	return decideInteractive(
		noInput(cmd),
		cli.Detect(os.Stdout).Agent,
		stdinIsTerminal(cmd),
		terminal.Is(os.Stderr),
	)
}

// decideInteractive is the rule, separated from the probes above for
// cli.decideFormat's reason: every input here is something a test cannot
// fake in-process, so a test of interactive() itself asserts almost
// nothing. An agent's environment and a CI runner both answer false to the
// terminal probes, which means an assertion that --no-input forces false
// passes identically with the flag ignored entirely — that exact test was
// written first, and a planted removal of the flag check did not fail it.
//
// --no-input is checked FIRST because it is the one input that is a
// STATEMENT rather than a probe: a caller at a perfectly good terminal
// saying they will not answer questions. Every prompt in this CLI is gated
// on the result, so forcing false here is what makes the guarantee whole —
// a command that would have asked refuses instead, naming --force.
func decideInteractive(noInput, agentDetected, stdinTTY, stderrTTY bool) bool {
	switch {
	case noInput:
		return false
	case agentDetected:
		return false
	default:
		return stdinTTY && stderrTTY
	}
}

// noInput reads the global --no-input flag off the root's persistent set.
//
// The ROOT's set rather than cmd.Flags(), the same lookup output
// resolution makes: the flag is registered once, persistently, on root,
// and reaching for it there works identically from any depth without
// depending on cobra having merged inherited flags into this command yet.
// A tree built without the flag (a unit test constructing a bare command)
// answers false, which is the same thing an unset flag means.
func noInput(cmd *cobra.Command) bool {
	on, err := cmd.Root().PersistentFlags().GetBool(cli.FlagNoInput)
	return err == nil && on
}

// humanByCommand records bespoke human renderers and native human help, so
// a guardrail can verify every registered operation has a human surface.
//
// Keyed by *cobra.Command for the reason SetMeta is: the tree owns
// identity, and this codebase builds many trees in one process. Membership
// only — the renderer itself travels on the result, not through here, so
// nothing looks a renderer up by name.
var (
	humanMu        sync.RWMutex
	humanByCommand = map[*cobra.Command]bool{}
)

func recordHuman(cmd *cobra.Command) {
	humanMu.Lock()
	defer humanMu.Unlock()
	humanByCommand[cmd] = true
}

// DeclaresHuman reports whether cmd declares a bespoke renderer or native
// human help. Exported for the guardrail that checks every
// envelope-bearing operation can be rendered for a person: without it,
// a command shipping with no human surface fails only when somebody
// runs it, after the work is done.
func DeclaresHuman(cmd *cobra.Command) bool {
	humanMu.RLock()
	defer humanMu.RUnlock()
	return humanByCommand[cmd]
}
