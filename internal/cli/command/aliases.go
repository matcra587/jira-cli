package command

import (
	"fmt"
	"slices"

	xmaps "github.com/gechr/x/maps"
	xslices "github.com/gechr/x/slices"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/cli"
)

// This file owns the ONE hidden-alias mechanism: a table type, the
// registrar that puts its entries in the parser, and the resolver that
// folds them back into their canonical flags.
//
// The pair used to live in body.go, which was right while the only
// aliases were body routes. It is not right once a single command has an
// alias of its own — `transition --status`, `list --cursor` — because
// those have nothing to do with a body, and a registrar named for one
// concept quietly becomes the place unrelated things are bolted onto.
// Lifting it here keeps ONE implementation with several tables, rather
// than a second loop per command that could drift in its refusal wording
// or forget the disagreement check entirely.
//
// The policy the mechanism implements is the design's §2:
//
//   - hidden: present in the parser, absent from --help, `jira agent
//     schema` and completions, so an alias widens no contract;
//   - folded at validation, never independent state;
//   - agreeing signals are fine, disagreeing signals are refused;
//   - errors never name an alias, because an introspecting agent was
//     never shown one.

// HiddenAlias pairs one hidden alias with the canonical flag it means.
//
// THE type, for every alias table in this CLI: the body routes, the list
// flags, the issue fields. There were three of these — HiddenAlias,
// ListFlagAlias, IssueFieldAlias — with identical fields and three
// registrars and three resolvers behind them, and IssueFieldAlias's own
// doc comment observed that it was "shaped exactly like" the other two
// without anything following from the observation.
//
// Following from it was not cosmetic. The seam was unfindable enough that
// two agents independently wrote the same resolver on two branches in one
// afternoon, and the three copies had diverged into three DIFFERENT sets
// of correct behavior: only two of them grouped a canonical's aliases so
// that two aliases disagreeing with each other was caught, and only one
// mirrored the canonical flag's TYPE at registration — which is what let
// `--remove-assignee` swallow the next argument as its value and
// `--labels x --labels y` silently discard x. Neither was a bug anyone
// wrote; each was a case one copy had and the others had not.
//
// cli.OutputAlias stays separate, and deliberately: it resolves before
// cobra parses at all (it reads argv directly, for the detector), so it
// shares this shape without sharing this mechanism.
type HiddenAlias struct {
	// Flag is the hidden alias's own name.
	Flag string
	// Canonical is the documented flag it resolves into.
	Canonical string
}

// AddHiddenAliases registers each alias as a hidden stand-in for its
// canonical flag: present in the parser so an acli-, jcli- or
// 0.15-trained invocation works, absent from help, the schema and
// completions so it widens no contract (design §2).
//
// The value goes to a discard: an alias is FOLDED into its canonical flag
// during Validate (ResolveHiddenAliases reads it back off the flag set),
// so nothing ever reads the variable bound here. It exists only because
// pflag needs somewhere to write.
//
// Registered raw, with no clib metadata, which is the same posture
// AddListFlags and AddOutputFlags take for their own aliases: nothing
// renders a hidden flag, so nothing needs a Terse to render it with. That
// is also why registration has to happen HERE rather than in each
// caller's own package: a raw pflag declaration with no clib metadata is
// exactly what the ruleguard rule rejects, and the exemption belongs to
// the one function that deliberately registers metadata-free hidden
// flags, not to every command that needs one.
//
// The alias MIRRORS the canonical flag's type, read back off the flag
// cobra just registered rather than hand-coded per alias, so this stays
// correct if a canonical flag's own type ever changes. That mirroring is
// the whole of the registration contract and it is not a nicety: a string
// alias bound to a BOOL canonical parses `--alias` as a flag wanting a
// value, so `--remove-assignee --dry-run` consumed --dry-run as the value
// and failed with a raw strconv.ParseBool message; a string alias bound to
// a STRING SLICE keeps only the last occurrence, so `--labels x --labels
// y` dropped x without a word. Both shipped, in the one copy of this loop
// that predated the type-mirroring one.
//
// The canonical flag MUST already be registered, and its type must be one
// this knows how to mirror. Both are startup wiring rather than input, so
// both panic — the same posture AddIssueFieldFlags takes for an unknown
// verb. The default branch refuses to guess rather than falling back to a
// string, because falling back is precisely how the two bugs above got in.
func AddHiddenAliases(cmd *cobra.Command, aliases []HiddenAlias) {
	fs := cmd.Flags()
	for _, a := range aliases {
		canon := fs.Lookup(a.Canonical)
		if canon == nil {
			panic(fmt.Sprintf("command: %q aliases --%s to --%s, which is not registered",
				cmd.Name(), a.Flag, a.Canonical))
		}
		usage := "Alias for --" + a.Canonical
		switch canon.Value.Type() {
		case "string":
			var discard string
			fs.StringVar(&discard, a.Flag, "", usage)
		case "bool":
			var discard bool
			fs.BoolVar(&discard, a.Flag, false, usage)
		case "stringSlice":
			var discard []string
			fs.StringSliceVar(&discard, a.Flag, nil, usage)
			preserveCSVInput(fs.Lookup(a.Flag))
		default:
			panic(fmt.Sprintf("command: %q aliases --%s to --%s, whose type %s has no alias form",
				cmd.Name(), a.Flag, a.Canonical, canon.Value.Type()))
		}
		fs.Lookup(a.Flag).Hidden = true
		if completionForward(canon) {
			markCompletionForward(fs.Lookup(a.Flag))
		}
	}
}

// AliasOption customizes ResolveHiddenAliases. Functional options for the
// reason BodyOption is: there is one genuine behavioral difference among
// the alias tables, and an option is how it survives consolidation without
// either half having to keep its own copy of the resolver.
type AliasOption func(*aliasPolicy)

// aliasPolicy is what the options above set.
type aliasPolicy struct {
	// nameBoth makes a disagreement refusal name the ALIAS as well as the
	// canonical flag. Off by default, which is §2's rule.
	nameBoth bool
}

// NamingBothSpellings makes a disagreement name the alias the caller typed
// alongside the canonical flag.
//
// **The difference this preserves is NOT about visibility.** Every alias
// in this CLI is hidden, the list flags included, so there is no Hidden
// bit to branch on. It is about whether a canonical is PRIVILEGED.
//
// The default — naming the canonical alone — is §2's "errors never name an
// alias" rule, and it is right wherever one spelling is the one help, the
// schema and completions advertise: naming --body-file or --cursor sends
// an introspecting agent hunting a flag it was never shown. Both VALUES
// still appear, which is what makes the refusal actionable.
//
// --sort, --order and --order-by are the case that does not fit. They are
// three spellings of one idea with none obviously primary, so a caller who
// typed two of them is better served being told WHICH two disagreed than
// being told that a flag they may not have typed at all was set twice.
func NamingBothSpellings() AliasOption {
	return func(p *aliasPolicy) { p.nameBoth = true }
}

// ResolveHiddenAliases folds every alias in the table into its canonical
// flag, following cli.ResolveSelector's rule exactly: agreeing signals
// are fine, disagreeing signals are refused.
//
// THE resolver — the body routes, the list flags and the issue fields all
// arrive here, and HiddenAlias's own doc comment records what the three
// separate copies cost. The only thing that varies between them is how a
// refusal is worded, which is NamingBothSpellings above.
//
// Must run AFTER cobra has parsed the command's actual flags — a
// command's Spec.Validate is where the call belongs, never Flags, which
// runs at construction before there is anything to fold.
func ResolveHiddenAliases(fs *pflag.FlagSet, aliases []HiddenAlias, opts ...AliasOption) error {
	var policy aliasPolicy
	for _, opt := range opts {
		opt(&policy)
	}

	// Grouped by canonical, so two aliases of one flag are compared against
	// each other and not merely each against the canonical: `--body x
	// --description y` with no --markdown at all must be refused, and a
	// per-alias loop would silently let the second win.
	//
	// The walk order is a SEPARATE slice rather than the map's, and it is
	// load-bearing: it decides which value a refusal reports first, and an
	// error that reordered itself between runs of the same command reads as
	// a different error. xslices.Unique is what makes that first-seen order
	// explicit, and xmaps.GroupFunc preserves encounter order within each
	// group — the two halves the hand-rolled seen-map loop here used to do
	// at once, less legibly.
	canonicalOf := func(a HiddenAlias) string { return a.Canonical }
	byCanonical := xmaps.GroupFunc(slices.Values(aliases), canonicalOf)

	for _, canonical := range xslices.Unique(xslices.Map(aliases, canonicalOf)) {
		if err := resolveOneHiddenAlias(fs, canonical, byCanonical[canonical], policy); err != nil {
			return err
		}
	}
	return nil
}

// resolveOneHiddenAlias resolves a single canonical flag's alias group:
// collect which of the canonical flag and its aliases were actually
// given, refuse a disagreement among them, and — only when the canonical
// flag itself was never given directly — replay the agreed value through
// the canonical flag's own pflag.Value so it lands in the same bound
// variable the canonical flag would have written to.
func resolveOneHiddenAlias(fs *pflag.FlagSet, canonical string, aliases []HiddenAlias, policy aliasPolicy) error {
	var given []string
	if fs.Changed(canonical) {
		given = append(given, canonical)
	}
	for _, a := range aliases {
		if fs.Changed(a.Flag) {
			given = append(given, a.Flag)
		}
	}
	if len(given) == 0 {
		return nil
	}

	first := fs.Lookup(given[0]).Value.String()
	for _, name := range given[1:] {
		if v := fs.Lookup(name).Value.String(); v != first {
			return aliasDisagreement(canonical, given[0], name, first, v, policy)
		}
	}
	if !fs.Changed(canonical) {
		return copyFlagValue(fs.Lookup(canonical).Value, fs.Lookup(given[0]).Value)
	}
	return nil
}

// aliasDisagreement builds the refusal for two spellings of one flag given
// different values. Which flags it NAMES is the policy; both values appear
// either way, because those are what makes the refusal actionable.
//
// One code for both, and it is CodeFlagConflict rather than the
// CodeFlagValueInvalid the list resolver used before it merged into this
// one. That was a genuine mismatch with the taxonomy, whose own
// declaration (cli.CodeFlagConflict, input.go) argues the case: nothing is
// wrong with either value, so flag_value_invalid's hint — "that value
// isn't the type the flag expects, run with --help" — describes a mistake
// the caller did not make. The recovery is to choose between two flags,
// which is exactly what flag_conflict is for.
func aliasDisagreement(canonical, firstFlag, secondFlag, firstValue, secondValue string, policy aliasPolicy) error {
	msg := fmt.Sprintf(
		"--%s was given two different values (%q and %q) — it was set more than once, so pick the one you meant",
		canonical, firstValue, secondValue,
	)
	if policy.nameBoth {
		msg = fmt.Sprintf(
			"--%s and --%s disagree (%q and %q) — they set the same thing, so pick one and drop the other",
			firstFlag, secondFlag, firstValue, secondValue,
		)
	}
	e := cli.NewInputError(cli.CodeFlagConflict, msg)
	e.Field = canonical
	return e
}

// copyFlagValue replays an alias's value through the canonical flag's own
// pflag.Value, so it lands in the same bound variable the canonical flag
// would have written to.
//
// A slice goes through pflag's SliceValue rather than String/Set, and that
// is not a refinement: a stringSlice renders itself as "[x,y]", brackets
// included, so setting a canonical from an alias's String() parsed the
// bracket characters into the values. String/Set round-trips correctly for
// every scalar and for nothing else.
func copyFlagValue(canonical, alias pflag.Value) error {
	canonSlice, canonOK := canonical.(pflag.SliceValue)
	aliasSlice, aliasOK := alias.(pflag.SliceValue)
	if canonOK && aliasOK {
		return canonSlice.Replace(aliasSlice.GetSlice())
	}
	return canonical.Set(alias.String())
}
