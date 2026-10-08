package command

import (
	"time"

	clibcobra "github.com/gechr/clib/cli/cobra"
	"github.com/gechr/clib/help"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/cli"
)

// FlagNoPager opts out of shared human document paging.
const FlagNoPager = "no-pager"

// AddOutputFlags registers the output selector on root's persistent
// flags: the canonical --output (the only selector help, schema, and
// completion advertise) plus the hidden functional aliases. Hidden is a
// deliberate 0-magic carve-out the design owns as an exception: an
// agent that reflexively tries --json from other-CLI muscle memory just
// works, while the documented contract keeps exactly one way to choose
// output. The aliases never become independent state — ResolveSelector
// folds them into the canonical value before anything reads a mode.
//
// This package registers flags raw on purpose (it is the construction
// path the ruleguard exemption exists for); the clib metadata rides in
// the same call so help and completion render the enum.
func AddOutputFlags(cmd *cobra.Command) {
	fs := cmd.PersistentFlags()

	fs.String(cli.FlagOutput, string(cli.OutputAuto), "Output format")
	// The @global role group keeps the selector beside conductor's
	// standard flags in every command's help; band 1 puts it first —
	// output choice is the flag an agent reads help for.
	clibcobra.Extend(fs.Lookup(cli.FlagOutput), clibcobra.FlagExtra{
		Terse:       "Output format",
		Group:       help.GlobalSection + "/1",
		Placeholder: "mode",
		Enum:        cli.OutputModeValues,
		EnumTerse:   cli.OutputModeTerse,
		EnumDefault: string(cli.OutputAuto),
	})

	// --jq sits at band 2, immediately under the selector it depends on:
	// it reshapes what --output produces and cannot act without it. No
	// Enum (a jq program is an open expression language) and no
	// Complete: the only completable thing would be the frame's top-level
	// keys on an empty word, useless the moment a `[`, `|` or function
	// name is typed, and the predictor laws make a mostly-wrong predictor
	// worse than none. Deliberate omission — do not "fix" it later.
	fs.String(cli.FlagJQ, "", "Filter machine output through a jq `expression`")
	clibcobra.Extend(fs.Lookup(cli.FlagJQ), clibcobra.FlagExtra{
		Terse:       "jq filter",
		Group:       help.GlobalSection + "/2",
		Placeholder: "expression",
	})

	// Values for the filter, bound as $name. Repeatable, and deliberately
	// NOT enum-shaped or completable: the names are the caller's own.
	fs.StringArray(cli.FlagJQArg, nil, "Bind a string as `name=value`, available to `--jq` as `$name` (repeatable)")
	clibcobra.Extend(fs.Lookup(cli.FlagJQArg), clibcobra.FlagExtra{
		Terse:       "jq string variable",
		Group:       help.GlobalSection + "/2",
		Placeholder: "name=value",
	})
	fs.StringArray(cli.FlagJQArgJSON, nil, "Bind JSON as `name=json`, available to `--jq` as `$name` (repeatable)")
	clibcobra.Extend(fs.Lookup(cli.FlagJQArgJSON), clibcobra.FlagExtra{
		Terse:       "jq JSON variable",
		Group:       help.GlobalSection + "/2",
		Placeholder: "name=json",
	})

	for _, alias := range cli.OutputAliases {
		// Hidden = true removes the alias from help and completion in
		// one move; the usage string exists only for source readers,
		// since nothing renders it.
		fs.Bool(alias.Flag, false, "Shorthand for --output="+string(alias.Mode))
		fs.Lookup(alias.Flag).Hidden = true
	}
}

// AddGlobalFlags registers the global flags that are not about output:
// --config, --timeout and --no-input (design §3).
//
// Unlike the output aliases above these are DOCUMENTED surface — visible
// in help, the schema and completion — so each carries clib metadata like
// any other flag. They sit at band 3, below the output selector and its
// filter, because they change the invocation's environment rather than
// what it prints.
//
// **Registration is all that happens here.** --config and --timeout are
// read from the RAW argv before this tree is ever built (root's own
// seeding), because the config file has to be located before the theme
// resolves and before any adapter is constructed. Cobra parses them again
// on the way past, which is what puts them in help and makes an
// unparseable value a typed flag error rather than a silent fallback — but
// nothing reads the parsed value back off this flag set. --no-input is the
// exception and is read normally, at Invocation time.
func AddGlobalFlags(cmd *cobra.Command) {
	fs := cmd.PersistentFlags()

	fs.String(cli.FlagConfig, "", "Read configuration from this file instead of the default location")
	clibcobra.Extend(fs.Lookup(cli.FlagConfig), clibcobra.FlagExtra{
		Terse:       "Config file",
		Group:       help.GlobalSection + "/3",
		Placeholder: "path",
		Hint:        "file",
	})
	markCompletionForward(fs.Lookup(cli.FlagConfig))

	fs.StringP(cli.FlagProfile, "P", "", "Select a named profile instead of JIRA_PROFILE or the configured default")
	clibcobra.Extend(fs.Lookup(cli.FlagProfile), clibcobra.FlagExtra{
		Terse: "Profile", Group: help.GlobalSection + "/3", Placeholder: "name", Complete: "predictor=profile",
	})
	markCompletionForward(fs.Lookup(cli.FlagProfile))

	// No Enum: a duration is an open value, not a vocabulary. The default
	// is spelled 0s rather than left blank so help states the actual
	// behavior — no deadline — instead of implying there is one it is not
	// naming.
	fs.Duration(cli.FlagTimeout, 0, "Bound each request to Jira, like `30s` or `2m`; 0 waits indefinitely")
	clibcobra.Extend(fs.Lookup(cli.FlagTimeout), clibcobra.FlagExtra{
		Terse:       "Request timeout",
		Group:       help.GlobalSection + "/3",
		Placeholder: "duration",
	})

	fs.Bool(cli.FlagNoInput, false, "Never prompt; refuse instead of asking")
	clibcobra.Extend(fs.Lookup(cli.FlagNoInput), clibcobra.FlagExtra{
		Terse: "Never prompt",
		Group: help.GlobalSection + "/3",
	})
}

// AddDryRunFlag registers --dry-run on a mutating command.
//
// Every mutation carries it, and it means the same thing everywhere: do
// the resolution and the checks, report what WOULD happen, change
// nothing. That uniformity is the point — an agent that has learned the
// flag on one command can reach for it on any mutation without first
// checking whether this one honors it.
func AddDryRunFlag(cmd *cobra.Command) {
	fs := cmd.Flags()
	fs.Bool(cli.FlagDryRun, false, "Report what would happen, and change nothing")
	clibcobra.Extend(fs.Lookup(cli.FlagDryRun), clibcobra.FlagExtra{
		Terse: "Preview only",
	})
}

// FlagOpts is the clib metadata a command-local flag carries. Terse is
// required — it is what completion and the compact help render, and a
// flag without one is invisible in exactly the surfaces an agent reads.
type FlagOpts struct {
	// Terse is the short label for help and completion.
	Terse string
	// Placeholder names the value in usage, like `name=value`.
	Placeholder string
	// Shorthand is the single-letter form, empty for none.
	Shorthand string
	// Complete supplies a registered clib predictor for an open vocabulary.
	Complete string
	// Forward sends this valued flag to dynamic completion callbacks so they
	// can distinguish positional roles without reading files or contacting Jira.
	Forward bool
	// Hint delegates value completion to the shell (for example, file or dir).
	Hint string
	// Enum supplies static completion/schema values for valued flags.
	Enum        []string
	EnumTerse   []string
	EnumDefault string
}

// flagExtra keeps valued flags on one metadata contract; registration and
// callback forwarding remain explicit in each typed helper.
func (opts FlagOpts) flagExtra() clibcobra.FlagExtra {
	return clibcobra.FlagExtra{
		Terse:       opts.Terse,
		Placeholder: opts.Placeholder,
		Enum:        opts.Enum,
		EnumTerse:   opts.EnumTerse,
		EnumDefault: opts.EnumDefault,
		Complete:    opts.Complete,
		Hint:        opts.Hint,
	}
}

// AddStringVar registers a string flag with its clib metadata in one
// call.
//
// One call on purpose: registering and extending separately is how a
// flag ends up with no metadata, which the ruleguard rule exists to
// prevent — so the helper makes the correct thing the shortest thing.
func AddStringVar(cmd *cobra.Command, p *string, name, value, usage string, opts FlagOpts) {
	fs := cmd.Flags()
	fs.StringVarP(p, name, opts.Shorthand, value, usage)
	clibcobra.Extend(fs.Lookup(name), opts.flagExtra())
	if opts.Forward {
		markCompletionForward(fs.Lookup(name))
	}
}

// AddStringSliceVar registers a comma-separated string-slice flag with
// its clib metadata. --fields is the canonical shape: a caller narrows
// a set by naming its members.
func AddStringSliceVar(cmd *cobra.Command, p *[]string, name string, value []string, usage string, opts FlagOpts) {
	fs := cmd.Flags()
	fs.StringSliceVarP(p, name, opts.Shorthand, value, usage)
	preserveCSVInput(fs.Lookup(name))
	clibcobra.Extend(fs.Lookup(name), opts.flagExtra())
	if opts.Forward {
		markCompletionForward(fs.Lookup(name))
	}
}

// AddStringArrayVar registers repeated literal values without CSV splitting.
// Query parameters can contain commas and equals signs as ordinary data.
func AddStringArrayVar(cmd *cobra.Command, p *[]string, name string, value []string, usage string, opts FlagOpts) {
	fs := cmd.Flags()
	fs.StringArrayVarP(p, name, opts.Shorthand, value, usage)
	clibcobra.Extend(fs.Lookup(name), opts.flagExtra())
	if opts.Forward {
		markCompletionForward(fs.Lookup(name))
	}
}

// AddInt64Var registers a byte bound without depending on the platform's int width.
func AddInt64Var(cmd *cobra.Command, p *int64, name string, value int64, usage string, opts FlagOpts) {
	fs := cmd.Flags()
	fs.Int64VarP(p, name, opts.Shorthand, value, usage)
	clibcobra.Extend(fs.Lookup(name), opts.flagExtra())
	if opts.Forward {
		markCompletionForward(fs.Lookup(name))
	}
}

// AddBoolVar registers a bool flag with its clib metadata.
func AddBoolVar(cmd *cobra.Command, p *bool, name string, value bool, usage string, opts FlagOpts) {
	fs := cmd.Flags()
	fs.BoolVarP(p, name, opts.Shorthand, value, usage)
	clibcobra.Extend(fs.Lookup(name), clibcobra.FlagExtra{
		Terse: opts.Terse,
	})
}

// AddPersistentBoolVar registers a PERSISTENT bool flag with its clib
// metadata — AddBoolVar's own analog for a flag whose meaning belongs to
// a whole command GROUP rather than one leaf. `issue`'s own --web is the
// first caller: it means the same "open the thing this command shows in
// a browser" on every subcommand under `issue`, so it registers once,
// here, on the group, and every subcommand inherits it via cobra's own
// persistent-flag merge rather than each declaring it for itself.
func AddPersistentBoolVar(cmd *cobra.Command, p *bool, name string, value bool, usage string, opts FlagOpts) {
	fs := cmd.PersistentFlags()
	fs.BoolVarP(p, name, opts.Shorthand, value, usage)
	clibcobra.Extend(fs.Lookup(name), clibcobra.FlagExtra{
		Terse: opts.Terse,
	})
}

// AddIntVar registers an int flag with its clib metadata — the pagination
// pair (--start/--limit) is the first caller.
func AddIntVar(cmd *cobra.Command, p *int, name string, value int, usage string, opts FlagOpts) {
	fs := cmd.Flags()
	fs.IntVarP(p, name, opts.Shorthand, value, usage)
	clibcobra.Extend(fs.Lookup(name), opts.flagExtra())
	if opts.Forward {
		markCompletionForward(fs.Lookup(name))
	}
}

// AddDurationVar registers a duration with the same help metadata as other
// typed flags. Invalid duration syntax is handled by the shared flag parser.
func AddDurationVar(cmd *cobra.Command, p *time.Duration, name string, value time.Duration, usage string, opts FlagOpts) {
	fs := cmd.Flags()
	fs.DurationVarP(p, name, opts.Shorthand, value, usage)
	clibcobra.Extend(fs.Lookup(name), opts.flagExtra())
	if opts.Forward {
		markCompletionForward(fs.Lookup(name))
	}
}

// ListFlags is the shared option set every JQL-backed list command binds
// its own flags into. AddListFlags below is the ONE call that wires up
// the whole surface, so a future list command (epic list, board list,
// …) gets --order-by/--reverse/--created-after/--created-before/
// --updated-after/--updated-before/--reporter/--priority/--watching/
// --count by calling one function, rather than every command re-declaring
// the same ten flags — and their mandatory clib metadata — for itself.
//
// issue list's own --project/--assignee/--status/--type/--label filters
// stay OUT of this struct on purpose: they are THAT command's own
// convenience shorthand, not necessarily every future list command's (an
// epic has no assignee the way an issue does), whereas every field here
// composes into a JQL clause any JQL-backed list shares regardless of
// what it is listing.
type ListFlags struct {
	// OrderBy names the field results are sorted by. Left unvalidated
	// here on purpose: this package stays domain-agnostic about what a
	// "field" even means, and the command composing JQL is the one that
	// knows its own allowed set and default sort direction per field.
	OrderBy string
	// Reverse flips OrderBy's own default direction.
	Reverse bool
	// CreatedAfter, CreatedBefore, UpdatedAfter and UpdatedBefore are raw
	// date bounds — absolute or Jira's own relative form — left unparsed
	// here for the same domain-agnostic reason as OrderBy: this package
	// registers the flag, the command composing JQL parses and validates
	// the value.
	CreatedAfter  string
	CreatedBefore string
	UpdatedAfter  string
	UpdatedBefore string
	// Reporter and Priority are repeatable convenience filters, composing
	// exactly like issue list's own --assignee/--status: repeated values
	// on ONE flag combine with OR, and this flag ANDs against every
	// other filter given alongside it.
	Reporter []string
	Priority []string
	// Watching keeps the bare adjective rather than IsWatching. This
	// struct's whole job is to mirror each flag's own spelling 1:1
	// (OrderBy for --order-by, Reverse for --reverse, …); IsWatching
	// would be the one field disagreeing with its own flag's spelling
	// for no reason specific to it, and golang-naming's "prefer isX"
	// guidance is a default a stated reason is allowed to override — this
	// is that stated reason.
	Watching bool
	// Count asks the command to report how many issues match WITHOUT
	// fetching them — a different execution path entirely (the
	// approximate-count endpoint, not the search every other field here
	// composes into), so it composes no clause of its own.
	Count bool
}

// AddListFlags registers ListFlags' whole surface on cmd in one call.
// --order-by, --reverse and --watching additionally carry hidden
// peer-muscle-memory aliases (ListFlagAliases below); a caller MUST
// follow registration with a ResolveListFlagAliases call once flags are
// actually parsed to fold those aliases back into f — AddListFlags itself
// runs at construction, before cobra has parsed anything, so it cannot do
// that folding itself (the same "Flags runs once, before parsing" timing
// issue list's own newList documents for cmd.Flags().Changed).
func AddListFlags(cmd *cobra.Command, f *ListFlags) {
	AddStringVar(cmd, &f.OrderBy, "order-by", "",
		"Sort results by this field",
		FlagOpts{Terse: "Sort field", Placeholder: "field", Enum: allowedOrderByFields})
	AddBoolVar(cmd, &f.Reverse, "reverse", false,
		"Reverse --order-by's own default direction",
		FlagOpts{Terse: "Reverse sort"})
	AddStringVar(cmd, &f.CreatedAfter, "created-after", "",
		"Match issues created on or after this date — absolute (2026-08-01) or Jira's own relative form (-7d, -2w)",
		FlagOpts{Terse: "Created after", Placeholder: "date"})
	AddStringVar(cmd, &f.CreatedBefore, "created-before", "",
		"Match issues created on or before this date — absolute (2026-08-01) or Jira's own relative form (-7d, -2w)",
		FlagOpts{Terse: "Created before", Placeholder: "date"})
	AddStringVar(cmd, &f.UpdatedAfter, "updated-after", "",
		"Match issues updated on or after this date — absolute (2026-08-01) or Jira's own relative form (-7d, -2w)",
		FlagOpts{Terse: "Updated after", Placeholder: "date"})
	AddStringVar(cmd, &f.UpdatedBefore, "updated-before", "",
		"Match issues updated on or before this date — absolute (2026-08-01) or Jira's own relative form (-7d, -2w)",
		FlagOpts{Terse: "Updated before", Placeholder: "date"})
	AddJQLFilterVar(cmd, &f.Reporter, "reporter",
		"Match reporters; me is the current user, none means empty (repeatable; combines with OR)",
		FlagOpts{Terse: "Reporter", Placeholder: "name"})
	AddJQLFilterVar(cmd, &f.Priority, "priority",
		"Match one of these priorities (repeatable; combines with OR)",
		FlagOpts{Terse: "Priority", Placeholder: "name"})
	AddBoolVar(cmd, &f.Watching, "watching", false,
		"Match only issues the current user watches",
		FlagOpts{Terse: "Watched only"})
	AddBoolVar(cmd, &f.Count, "count", false,
		"Report how many issues match, without fetching them",
		FlagOpts{Terse: "Count only"})

	AddHiddenAliases(cmd, ListFlagAliases)
}

// ListFlagAliases is the ONE table AddListFlags (registration, above) and
// ResolveListFlagAliases (resolution, below) both read — exactly cli.
// OutputAliases' own shape (detector.go), so neither half can drift from
// the other: a flag registered here that resolution never checks, or a
// resolution probing a flag this table never registered, cannot happen,
// because both sides read this same slice rather than each keeping its
// own list.
var ListFlagAliases = []HiddenAlias{
	{Flag: "sort", Canonical: "order-by"},
	{Flag: "order", Canonical: "order-by"},
	{Flag: "desc", Canonical: "reverse"},
	{Flag: "watched", Canonical: "watching"},
}

// ResolveListFlagAliases folds every hidden alias in ListFlagAliases into
// its canonical flag, through the shared resolver (ResolveHiddenAliases,
// aliases.go) that every alias table in this CLI now goes through.
//
// NamingBothSpellings is the one thing that distinguishes it, and the
// reason is at that option's own declaration: --sort, --order and
// --order-by are three spellings with no primary among them, so a refusal
// that named only --order-by would report a flag the caller may never have
// typed.
//
// Must run AFTER cobra has parsed the command's actual flags — see
// AddListFlags' own doc comment for the timing reason — so a command's
// own Validate closure is where this call belongs, not Flags.
func ResolveListFlagAliases(fs *pflag.FlagSet) error {
	return ResolveHiddenAliases(fs, ListFlagAliases, NamingBothSpellings())
}
