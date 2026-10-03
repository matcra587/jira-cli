package command

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/cli"
)

// Mutation-flag names, spelled once so registration and every refusal
// name the same strings.
const (
	FlagForce          = "force"
	FlagValidateRemote = "validate-remote"
	FlagVerify         = "verify"
	FlagNoReadback     = "no-readback"
	FlagNotify         = "notify"
	// FlagYes is --force's hidden alias, named here so registration and
	// any test that asserts its hiddenness cannot disagree about it.
	FlagYes = "yes"
	// FlagSkipNotify is --notify's hidden alias, named here for FlagYes'
	// reason. Unlike FlagYes it is the NEGATION of its canonical flag —
	// see addSkipNotifyAlias.
	FlagSkipNotify = "skip-notify"
)

// MutationFlags is the shared option set a writing command binds.
//
// --dry-run is deliberately NOT a field here. It is registered by
// AddMutationFlags but read off Invocation.DryRun, which the
// construction path fills from the flag it registered itself. Binding it
// into this struct as well would give one fact two carriers that could
// disagree — and they did once: `config edit` declared a local dryRun
// that nothing populated, so --dry-run read false and the preview edited
// the file (see Invocation.DryRun's own doc comment). One carrier, and
// it is the one the path owns.
type MutationFlags struct {
	// Force skips confirmation or acknowledges an explicit headless mutation.
	Force bool
	// ValidateRemote asks for the extra reads that check a value against
	// the live site before writing.
	ValidateRemote bool
	// Verify asks for a readback confirming the write landed.
	Verify bool
	// NoReadback suppresses a readback the command would otherwise do.
	NoReadback bool
	// Notify asks Jira to email the issue's watchers about this change.
	// Default TRUE, which is Jira's own default — see WithNotify for why
	// the flag exists at all when its default changes nothing.
	//
	// Read only after ResolveNotify has folded the hidden --skip-notify
	// into it. A command that reads it straight off the flag set sees
	// true for a caller who passed the alias.
	Notify bool
	// skipNotify is the hidden alias's own value, unexported because
	// nothing outside this package may read it: it is an input to
	// ResolveNotify and never an answer. Notify is the answer.
	skipNotify bool
}

// MutationOption customizes AddMutationFlags. Functional options rather
// than a fixed set, because the real footprints differ sharply: across
// 0.15's 21 mutations, --dry-run is on all of them, --force on 7,
// --validate-remote on 7, --no-readback on 4 and --verify on 2. A
// registrar that applied all of them uniformly would invent surface no
// tool has, on commands that must not carry it — and every flag costs
// clib metadata, a completion entry, help text, and a line of the schema
// an agent reads on every introspection.
type MutationOption func(*MutationFlags, *cobra.Command)

// WithForce registers --force, skipping a destructive command's
// confirmation prompt. 0.15 puts it on 7 commands, all of them
// destructive (delete, and the verbs that overwrite).
//
// A non-destructive mutation must NOT take it. There would be no
// confirmation for it to skip, so the flag would either do nothing or
// require inventing a prompt to justify itself — and a flag that does
// nothing is worse than an absent one, because an agent reading the
// schema will pass it.
//
// --yes rides along as a HIDDEN alias (design §5, sourced from acli). It
// is the spelling a caller trained on another tool reaches for first,
// and accepting it costs nothing: a hidden flag is absent from help, the
// schema and completions, so the contract does not widen.
func WithForce() MutationOption {
	return func(f *MutationFlags, cmd *cobra.Command) {
		AddBoolVar(cmd, &f.Force, FlagForce, false,
			"Skip the confirmation prompt",
			FlagOpts{Terse: "No confirmation"})
		addForceAlias(cmd, f)
	}
}

// WithRequiredForce registers explicit acknowledgement for raw mutations.
// Their impact cannot be inferred from a generic endpoint, so the caller must
// opt in even at a terminal; no guessed resource description earns a prompt.
func WithRequiredForce() MutationOption {
	return func(f *MutationFlags, cmd *cobra.Command) {
		AddBoolVar(cmd, &f.Force, FlagForce, false,
			"Allow this explicit raw mutation (not required for previews or known read endpoints)",
			FlagOpts{Terse: "Allow raw mutation"})
		addForceAlias(cmd, f)
	}
}

// WithHeadlessForce registers acknowledgement for a local mutation whose
// explicit terminal invocation is sufficient consent. It creates no prompt.
func WithHeadlessForce(action string) MutationOption {
	return func(f *MutationFlags, cmd *cobra.Command) {
		AddBoolVar(cmd, &f.Force, FlagForce, false,
			"Allow "+action+" without an interactive terminal (not required for --dry-run)",
			FlagOpts{Terse: "Allow headless change"})
		addForceAlias(cmd, f)
	}
}

// addForceAlias registers the hidden --yes.
//
// It binds the SAME bool as --force rather than a discard needing a
// resolution pass, which is where this differs from the body aliases —
// and the difference is a property of the type, not a shortcut. Those
// are strings, so two spellings can carry DIFFERENT values and
// disagreeing ones have to be refused. A bool flag has only one value
// worth giving, so both spellings set the same true and there is nothing
// for a resolver to reconcile. The one invocation this treats
// differently from a resolver, `--force --yes=false`, resolves to the
// last one parsed; refusing it would be machinery for a combination
// nobody types, and its meaning ("do not skip the prompt") is at least
// the honest reading of what was written.
//
// No clib metadata, matching AddBodyFlags' own hidden aliases: nothing
// renders a hidden flag, so there is nothing for a Terse to render.
func addForceAlias(cmd *cobra.Command, f *MutationFlags) {
	fs := cmd.Flags()
	fs.BoolVar(&f.Force, FlagYes, false, "Alias for --"+FlagForce)
	fs.Lookup(FlagYes).Hidden = true
}

// WithValidateRemote registers --validate-remote, opting into the extra
// reads that check a value against the live site before writing.
//
// Separate from --dry-run on purpose, and the split is load-bearing:
// this repo's dry-run is local-only and never contacts Jira, so a check
// that needs a live read cannot ride it. Keeping them apart is what lets
// `--dry-run` stay honestly free of network and lets a caller ask for the
// remote check on a real run too.
func WithValidateRemote() MutationOption {
	return func(f *MutationFlags, cmd *cobra.Command) {
		AddBoolVar(cmd, &f.ValidateRemote, FlagValidateRemote, false,
			"Check values against the live site before writing",
			FlagOpts{Terse: "Check against site"})
	}
}

// WithVerify registers --verify, asking for a readback that confirms the
// write landed. 0.15 has it on two commands, create and edit.
//
// It earns its place only where confirming costs an EXTRA request. A
// write whose own response carries the created object — `comment add`,
// whose POST answers 201 with the whole comment — has nothing to gate:
// there is no second round trip to opt into, so it publishes the object
// unconditionally instead of hiding it behind a flag.
func WithVerify() MutationOption {
	return func(f *MutationFlags, cmd *cobra.Command) {
		AddBoolVar(cmd, &f.Verify, FlagVerify, false,
			"Read the resource back and confirm the write landed",
			FlagOpts{Terse: "Confirm the write"})
	}
}

// WithoutReadback registers --no-readback, suppressing a readback the
// command would otherwise perform. The watcher verbs are its 4 callers:
// adding a watcher returns no body, so reporting the resulting watcher
// set means a second read a caller may not want to pay for.
func WithoutReadback() MutationOption {
	return func(f *MutationFlags, cmd *cobra.Command) {
		AddBoolVar(cmd, &f.NoReadback, FlagNoReadback, false,
			"Skip reading the resource back after writing",
			FlagOpts{Terse: "No readback"})
	}
}

// WithNotify registers --notify, which asks Jira whether to email the
// issue's watchers about this change. Default TRUE, matching Jira's own
// default for both endpoints that take it.
//
// **The capability is DISABLING notification, and the default is what
// makes that readable.** A bare `--notify` changes nothing — it requests
// what Jira would do anyway — so on its own it would be §7's "an alias
// that changes nothing is a lie" wearing a different hat. It is not,
// because the flag is a bool with two settings and the OTHER setting is
// the whole point: `--notify=false` (or the hidden `--skip-notify`)
// suppresses the email. Spelling the canonical flag positively is what
// keeps `--notify=false` readable at a call site, and one canonical
// spelling is what stops `--notify` and `--skip-notify` becoming two
// half-flags that disagree.
//
// **Why it matters now.** It was a peer-parity nicety while every write
// named its issues one at a time. The bulk selectors changed the stakes:
// `issue edit --jql …` over a hundred issues sends a hundred emails to
// every watcher of each, which is a mailbox nobody asked for and the
// kind of thing that gets a tool banned from a tenant.
//
// **The caveat this flag cannot fix, and must therefore publish.** On
// `issue edit`, Jira requires "administer Jira" or "administer project"
// permission to suppress the notification, and — its own words —
// "if the user doesn't have the necessary permission the request is
// ignored". Not refused: IGNORED. So a caller without that permission
// passes --skip-notify, gets a successful edit, and the emails go out
// anyway, with nothing in the response saying so. Nothing here can
// detect it, which is exactly why the envelope publishes what was
// REQUESTED and says so in those terms, and why the help text warns
// rather than promising. `issue comment edit` carries no such caveat in
// the spec.
func WithNotify() MutationOption {
	return func(f *MutationFlags, cmd *cobra.Command) {
		AddBoolVar(cmd, &f.Notify, FlagNotify, true,
			"Email the issue's watchers about this change; --notify=false suppresses it",
			FlagOpts{Terse: "Notify watchers"})
		addSkipNotifyAlias(cmd, f)
	}
}

// addSkipNotifyAlias registers the hidden --skip-notify.
//
// **It binds its OWN bool, unlike --force's --yes alias**, and the
// difference is the whole reason this needs a resolver where that one
// does not. --yes and --force mean the same thing, so both spellings can
// set the same variable and there is nothing to reconcile. --skip-notify
// is the NEGATION of --notify: binding it to the same variable would
// make `--skip-notify` set Notify true, which is the opposite of what
// the caller wrote.
//
// So it lands in its own field and ResolveNotify folds it, which also
// makes the one genuinely contradictory invocation —
// `--notify --skip-notify` — visible enough to refuse rather than
// resolving to whichever pflag happened to parse last.
//
// No clib metadata, matching every other hidden alias in this tree:
// nothing renders a hidden flag, so there is no Terse for a
// register-and-extend helper to attach.
func addSkipNotifyAlias(cmd *cobra.Command, f *MutationFlags) {
	fs := cmd.Flags()
	fs.BoolVar(&f.skipNotify, FlagSkipNotify, false, "Alias for --"+FlagNotify+"=false")
	fs.Lookup(FlagSkipNotify).Hidden = true
}

// ResolveNotify folds the hidden --skip-notify into Notify, and refuses
// the pair that cannot be honored.
//
// The rule is BodyFlags.ResolveAliases', applied to an inverted alias:
// agreeing signals are fine, a disagreement is refused. Concretely,
// `--notify=false --skip-notify` agree (both say don't email) and pass,
// while `--notify --skip-notify` is a caller asking for both outcomes at
// once and is refused rather than silently resolved.
//
// Refusing beats last-one-wins here where it did not for `--force
// --yes=false` (addForceAlias's own note). That pair is a combination
// nobody types whose plain reading is at least coherent; this one is two
// flags a caller could reasonably reach for in the same command line —
// one from habit, one from another tool — and the outcomes are opposite,
// so the wrong guess sends email to every watcher of every issue.
//
// It is called from Spec.Validate, before any I/O, for the reason
// KeyedFlags.Validate is: pflag parses the values long after
// registration, and a refusal belongs on the same side of the I/O line
// as every other input check.
func (f *MutationFlags) ResolveNotify(fs *pflag.FlagSet) error {
	if !fs.Changed(FlagSkipNotify) {
		return nil
	}
	if fs.Changed(FlagNotify) && f.Notify {
		e := cli.NewInputError(cli.CodeFlagConflict, fmt.Sprintf(
			"--%s and --%s ask for opposite things — one says email the watchers, the other says do not; pick the one you meant",
			FlagNotify, FlagSkipNotify,
		))
		e.Field = FlagNotify
		return e
	}
	f.Notify = false
	return nil
}

// AddMutationFlags registers the shared write surface on cmd.
//
// --dry-run is unconditional and needs no option: every mutation carries
// it, and New already refuses a non-read command that registers none
// (command.go), so the two statements cannot disagree. Everything else
// is opt-in, per the footprints in MutationOption's doc comment.
//
// Calling this with no options at all is the correct thing for a simple
// write and is exactly what `issue comment add` does — it is not an
// oversight there, and a reviewer should read a bare call as "this
// command needs only the preview", not as an unfinished one.
func AddMutationFlags(cmd *cobra.Command, f *MutationFlags, opts ...MutationOption) {
	AddDryRunFlag(cmd)
	for _, opt := range opts {
		opt(f, cmd)
	}
}
