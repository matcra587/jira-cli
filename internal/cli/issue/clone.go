package issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	xsync "github.com/gechr/x/sync"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// cloneProjectAliases spells acli's name for "where the copy goes".
//
// **The design's §5 row is INVERTED here, deliberately.** It makes
// --to-project canonical with --project hidden; this keeps --project
// canonical and hides --to-project. Both restore the acli invocation,
// which is what the row is for, and the difference is which spelling
// `clone --help` shows.
//
// --project wins because clone does not own that flag. It comes from
// AddIssueFieldFlags, the SAME registrar `issue create` uses, and the
// stated reason for sharing it (see newClone's Flags) is that every
// override spells and validates identically to the flag that would have
// set the field on a fresh issue. Renaming one field for one command is
// exactly the drift that sharing the registrar exists to prevent — and it
// would have to be threaded through the registrar's own spellings, its
// Apply, and every refusal that names a flag, to move one word in one
// help page.
//
// The ambiguity argument for --to-project is real and does not win: on a
// command taking source KEYs, --project could be read as "the project of
// the issue I am cloning". The help text and the flag's own description
// ("Project to create the issue in") answer that, and the cost of the
// alternative is a vocabulary split across two commands that a caller
// moves between constantly.
var cloneProjectAliases = []command.HiddenAlias{
	{Flag: "to-project", Canonical: command.FlagProject},
}

// flagReplace is this command's own flag name, spelled once so
// registration and every refusal below name the same string.
const flagReplace = "replace"

// There is deliberately no `--to-site` here, and it is ABSENT rather than
// registered-and-refused.
//
// acli has one and a cross-site clone is a real thing to want. This CLI
// cannot do it today, and the reason is structural rather than missing
// plumbing: credentials.Record holds ONE site, one email and one token,
// and credentials.Load takes no site argument — there is exactly one
// stored credential and no second one to authenticate a destination with.
// A `--to-site` that read from the configured site and wrote to a named
// other one would need a credential this CLI has nowhere to keep.
//
// So it is not built. A flag that announces a capability it does not have
// is worse than an absent one — the same ground `--adf-strict` was refused
// on. When a second credential becomes storable, this becomes a real
// feature rather than a flag that has to start apologizing.

// cloneReplacement is one `old=new` pair from --replace.
type cloneReplacement struct {
	old string
	new string
}

// parseCloneReplacements turns the raw --replace values into pairs.
//
// The separator is the FIRST `=`, so `--replace 'a=b=c'` replaces `a` with
// `b=c` rather than being refused: `=` is an ordinary character in issue
// summaries and a caller replacing one is not making a mistake. An empty
// left side IS refused — a pattern matching the empty string matches
// everywhere, and `strings.ReplaceAll` would splice the replacement
// between every rune.
func parseCloneReplacements(raw []string) ([]cloneReplacement, error) {
	pairs := make([]cloneReplacement, 0, len(raw))
	for _, r := range raw {
		old, replacement, ok := strings.Cut(r, "=")
		if !ok {
			e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
				"--%s takes `old=new`, and %q has no `=` — say what to search for and what to put in its place",
				flagReplace, r,
			))
			e.Field = flagReplace
			return nil, e
		}
		if old == "" {
			e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
				"--%s was given %q, whose search text is empty — an empty pattern matches between every character, so name the text you want replaced",
				flagReplace, r,
			))
			e.Field = flagReplace
			return nil, e
		}
		pairs = append(pairs, cloneReplacement{old: old, new: replacement})
	}
	return pairs, nil
}

// applyCloneReplacements rewrites the request's summary, reporting
// whether anything actually changed.
//
// **The summary and nothing else, which is a narrowing worth stating.**
// The other text a clone carries is the description, and that is an ADF
// DOCUMENT rather than a string. Replacing text in it means walking the
// tree's text nodes: adf.Node keeps its members unexported with the
// parse-ordered raw capture as its marshaling source of truth, so there is
// no supported way to rewrite one from here, and doing it textually over
// the marshaled bytes instead would rewrite link hrefs, mention ids and
// attribute values as readily as prose. A search-and-replace that
// corrupted a link while renaming a release would be worse than not having
// the flag. `--json-input` and `--markdown` are the routes that set a
// clone's body outright.
//
// Applied LAST, over whatever the summary ended up being — copied from the
// source, set by the payload, or set by `--summary`. A transformation that
// runs after the value is settled has one meaning wherever the value came
// from, where one that ran mid-composition would mean something different
// depending on which route won.
//
// A pair that matches nothing changes nothing and is not an error here;
// runClone decides what to say about a --replace that matched nothing
// ANYWHERE, which is the case that actually indicates a typo.
func applyCloneReplacements(req *jira.IssueWriteRequest, pairs []cloneReplacement) (bool, error) {
	if len(pairs) == 0 {
		return false, nil
	}
	raw, ok := req.Fields[command.FieldSummary]
	if !ok {
		// Nothing to rewrite: the source carried no summary and no route
		// set one. Not an error — the create will fail on its own terms if
		// a summary was required, and that is a better message than one
		// about a flag the caller used correctly.
		return false, nil
	}
	var summary string
	if err := json.Unmarshal(raw, &summary); err != nil {
		return false, fmt.Errorf("issue.clone: reading the summary to apply --%s: %w", flagReplace, err)
	}
	rewritten := summary
	for _, p := range pairs {
		rewritten = strings.ReplaceAll(rewritten, p.old, p.new)
	}
	if rewritten == summary {
		return false, nil
	}
	if err := req.SetField(command.FieldSummary, rewritten); err != nil {
		return false, err
	}
	return true, nil
}

// newClone returns `jira issue clone`.
//
// THE ONLY COMPOSITE OPERATION IN THIS CLI: Jira has no copy endpoint,
// so a clone is a READ of the source followed by a CREATE from what it
// found. Everything unusual about this file comes from that — most of
// all the fact that what carries over is this CLI's decision rather than
// the server's, which is why the policy is stated in the help text and
// enforced by the source-field policy.
func newClone(deps Deps) *cobra.Command {
	var keyed command.KeyedFlags
	var sel command.SelectorFlags
	var mut command.MutationFlags
	var fields command.IssueFieldFlags
	var jsonInput string
	var replaceRaw []string
	var replacements []cloneReplacement
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueCloneOutput]{
		Op:          "issue.clone",
		WritesJira:  true,
		Identity:    true,
		Keyed:       true,
		Passthrough: true,
		Use:         "clone ISSUE_KEY...",
		Short:       "Copy issues into new ones",
		Long: "Create a new issue from each named one.\n\n" +
			"**Jira has no copy operation.** This reads the source issue and " +
			"creates a new one from what it found, which means what carries over " +
			"is this CLI's decision — so here it is, plainly, because you cannot " +
			"discover it without running the command on something real.\n\n" +
			"**Carried:** source fields including assignee, description, custom " +
			"fields and unknown native values. Explicit null, false and zero " +
			"values remain distinct. Project, issue type, priority and assignee " +
			"use minimal native references rather than expanded read attributes.\n\n" +
			"**Not carried:** identity, workflow status and resolution, reporter, " +
			"timestamps, time tracking, aggregate progress, watchers, votes, " +
			"comments, worklogs, subtasks, attachments, links and native rank. " +
			"Custom-field schemas are read once per run when needed to identify " +
			"rank. Ordinary text that resembles a rank token still carries. " +
			"Unusable source fields or metadata stop that source before creation " +
			"with `issue_clone_source_invalid`. Jira validates the copied fields " +
			"against the destination project and issue type.\n\n" +
			"Every field flag `jira issue create` accepts works here as an " +
			"OVERRIDE, applied on top of what was copied — `--summary 'Retry of " +
			"the migration'` replaces the source's summary rather than adding to " +
			"it. Links and attachments are not copied, so `jira issue link " +
			"create` and `jira issue attachment add` are how you carry those " +
			"across deliberately.\n\n" +
			"`--json-input` overrides fields the flags cannot name — a custom " +
			"field, or anything needing a shape a flag does not have. It takes a " +
			"native `fields`/`update` document, the same one `jira issue create " +
			"--json-input` takes, and it is layered on top of the copy exactly " +
			"where the field flags are rather than replacing the whole request. " +
			"It takes a file path, or `-` to read the document from standard " +
			"input. A field named by BOTH the document and a flag is refused " +
			"rather than silently resolved, and both halves of the document are " +
			"checked — `update.priority` collides with `--priority` just as " +
			"`fields.priority` does.\n\n" +
			"Top-level request members such as `properties`, `historyMetadata` " +
			"and `transition` also survive the override. They come only from " +
			"your input, not from the source issue response. Jira validates " +
			"their meaning. `fields` and `update` must be objects when present; " +
			"an empty `fields` override leaves copied fields in place.\n\n" +
			"`--replace old=new` rewrites the copied SUMMARY, which is how you " +
			"clone a release ticket for the next version without retyping it. It " +
			"is repeatable and the pairs apply in order, after every other route " +
			"has settled what the summary is. It does NOT touch the description: " +
			"that is a structured document rather than a string, and a textual " +
			"search-and-replace across it would rewrite link targets and " +
			"formatting as readily as prose — use `--json-input` to set a body " +
			"outright. Search text that appears in no source's summary changes " +
			"nothing and says so in `warnings[]`, rather than failing keys that " +
			"cloned correctly.\n\n" +
			"This is a keyed command: one entry per SOURCE key at " +
			"`data.results[]`, in the order you gave them, each carrying the " +
			"clone's own new key. A key that fails does not fail the others, and " +
			"that matters here because cloning is not idempotent — re-running a " +
			"key that already succeeded makes a SECOND copy, so the envelope " +
			"naming which sources produced a clone is what makes a retry safe. " +
			"Total failure and interruption retain every source's outcome. " +
			"A failed row can still carry `data.applied` (HTTP 201), a usable " +
			"`data.clone` identity and the complete native `data.receipt`. " +
			"Inspect these before retrying any source.\n\n" +
			"`--dry-run` reports which issues would be cloned and stops. It " +
			"contacts nobody, so it cannot show you the fields it would copy — " +
			"those live on the source, and reading them is what a live run does " +
			"first.\n\n" +
			command.SelectorHelp + "\n\n" +
			"`--web` is refused here. It opens one page and this command writes " +
			"to a list of keys.",
		Example: "# copy one issue\n" +
			"$ jira issue clone KEY-123\n\n" +
			"# copy it with a new summary\n" +
			"$ jira issue clone KEY-123 --summary 'Retry of the migration'\n\n" +
			"# copy several, taking them yourself\n" +
			"$ jira issue clone KEY-123 KEY-124 --assignee @me\n\n" +
			"# copy into another project\n" +
			"$ jira issue clone KEY-123 --project OTHER\n\n" +
			"# clone a release ticket for the next version\n" +
			"$ jira issue clone KEY-123 --replace 'v1.4=v1.5'\n\n" +
			"# override a custom field the flags cannot name\n" +
			"$ jira issue clone KEY-123 --json-input overrides.json\n\n" +
			"# the same, reading the overrides from standard input\n" +
			"$ echo '{\"fields\":{\"customfield_10016\":8}}' | jira issue clone KEY-123 --json-input -\n\n" +
			"# see which issues would be cloned, without cloning them\n" +
			"$ jira issue clone KEY-123 --dry-run\n\n" +
			"# just the new keys, as json\n" +
			"$ jira issue clone KEY-123 KEY-124 --output=json --jq '.data.results[].data.clone.key'\n\n" +
			"# every issue a query matches; the confirmation lists them first\n" +
			"$ jira issue clone --jql 'project = KEY AND labels = template' --to-project OTHER",
		// Min is 0 because a selector replaces the positionals entirely;
		// SelectorFlags.Validate gives the "name something" refusal, which
		// can name both routes where cobra's arity check could not.
		Args: command.ArgSpec{Min: 0, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddKeyedFlags(c, &keyed)
			// --force arrives WITH the selectors, which are what give this
			// command a confirmation to skip. A clone that names its keys
			// still prompts for nothing.
			command.AddMutationFlags(c, &mut, command.WithValidateRemote(), command.WithForce())
			command.AddSelectorFlags(c, &sel)
			// The SAME registrar `issue create` uses, so every override
			// spells and validates identically to the flag that would have
			// set the field on a fresh issue. A clone-specific copy of the
			// field surface is how the two would drift.
			command.AddIssueFieldFlags(c, &fields, command.CreateIssueVerb)
			command.AddHiddenAliases(c, cloneProjectAliases)
			// The §2.1 ADF route, spelled and aliased exactly as `issue
			// create` spells it — the same flag name, the same shared alias
			// table — because a caller who learned the route on create is
			// reaching for it here to override the same fields.
			command.AddStringVar(c, &jsonInput, command.FlagJSONInput, "",
				"Read native clone overrides, including fields, update and other request members, from this JSON file, or `-` for standard input",
				command.FlagOpts{Terse: "Overrides from file", Placeholder: "path"})
			command.AddHiddenAliases(c, command.ADFRouteAliases())
			command.AddStringSliceVar(c, &replaceRaw, flagReplace, nil,
				"Replace `old=new` in the copied summary (repeatable, applied in order)",
				command.FlagOpts{Terse: "Rewrite summary", Placeholder: "old=new"})
		},
		Validate: func() error {
			if err := keyed.Validate(); err != nil {
				return err
			}
			// Folded FIRST, so a project that arrived as --to-project is
			// visible to the conflict checks below and to Apply — the
			// ordering every other alias-bearing command's Validate uses.
			if err := command.ResolveHiddenAliases(cmd.Flags(), cloneProjectAliases); err != nil {
				return err
			}
			if err := resolvePayloadAliases(cmd.Flags()); err != nil {
				return err
			}
			// The FIELD aliases, which clone inherits by sharing
			// AddIssueFieldFlags with create and edit. Registration comes
			// with the registrar; resolution does not, and clone was
			// registering --labels and --remove-labels without ever
			// folding them — so they parsed, were accepted, and were
			// silently discarded. create and edit fold theirs in write.go
			// for the same reason; a command that borrows the registrar
			// borrows this call with it.
			if err := fields.ResolveIssueFieldAliases(cmd.Flags()); err != nil {
				return err
			}
			if err := fields.Conflict(); err != nil {
				return err
			}
			// Parsed in Validate so a malformed pair is refused before any
			// source is read, rather than N reads into a fan-out. The
			// payload-versus-flag collision check cannot join it: that one
			// needs the parsed payload, and parsing means reading a file,
			// which is compose's job rather than Validate's (issueWriteFlags
			// splits the two the same way and for the same reason).
			parsed, err := parseCloneReplacements(replaceRaw)
			if err != nil {
				return err
			}
			replacements = parsed
			// Every positional here is a key, so the count goes through
			// as-is.
			if err := sel.Validate(len(cmd.Flags().Args())); err != nil {
				return err
			}
			return refuseWebOnKeyedWrite(cmd)
		},
		Human:         renderClone,
		RetainOnError: func(out envelope.IssueCloneOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueCloneOutput, error) {
			return runClone(ctx, deps, inv, cloneOptions{
				fields:         &fields,
				jsonInput:      jsonInput,
				replacements:   replacements,
				selector:       sel,
				force:          mut.Force,
				validateRemote: mut.ValidateRemote,
				parallelism:    keyed.Parallelism,
			})
		},
	})
}

// runClone reads each source and creates its clone.
//
// **The preview builds no client**, which costs this command more than
// it costs the others and is still right: the fields a clone would carry
// live on the SOURCE, so a preview that reported them would have to read
// every source issue — turning `--dry-run` into a command that contacts
// Jira N times. The rule holds and the preview says less.
//
// `--validate-remote` is registered for the @me resolution the shared
// field registrar performs (design §8 item 0), not for the source read:
// there is no preview shape in which reading N issues to describe a copy
// is cheaper than making the copy.
// cloneOptions is what runClone needs from the flag set, gathered into one
// struct so the handler's signature does not grow a parameter per flag —
// linkCreateOptions' own reasoning, and --dry-run is absent for its reason
// too: it arrives on the Invocation.
type cloneOptions struct {
	fields *command.IssueFieldFlags
	// jsonInput is a path to a file holding clone OVERRIDES — a native
	// `fields`/`update` document — or "-" for standard input. Overrides
	// rather than a whole payload, which is where this differs from the
	// identically-named flag on create and edit: the base of a clone's
	// request is the SOURCE issue, and the document is layered on top of it
	// exactly where the field flags are.
	jsonInput    string
	replacements []cloneReplacement

	// selector and force are the bulk-selector surface. force is only
	// ever consulted for a selector-driven run: naming keys prompts for
	// nothing, so there is no prompt for it to skip.
	selector       command.SelectorFlags
	force          bool
	validateRemote bool
	parallelism    int
}

func runClone(ctx context.Context, deps Deps, inv command.Invocation, opts cloneOptions) (envelope.IssueCloneOutput, error) {
	var zero envelope.IssueCloneOutput

	out := envelope.IssueCloneOutput{MutationState: envelope.MutationState{DryRun: inv.DryRun}}
	accounts := opts.fields.AccountFields()

	// Read and checked ONCE for the whole run rather than per key: the
	// document does not depend on the source, and a per-key read would
	// re-open the file N times — and could not read `-` at all more than
	// once. It happens before the preview returns so that a malformed
	// override or a collision with a field flag is refused by `--dry-run`
	// too, which is the run a caller makes to find exactly that out.
	overrides, err := readCloneOverrides(ctx, opts, inv.Stdin, inv.StdinIsTerminal)
	if err != nil {
		return zero, err
	}

	// ONE client for the run, however many resolvers want one — the
	// selector's search, `@me`, and the fan-out itself.
	newClient := oneClient(deps.JiraClient)

	// The selector resolves FIRST, so the confirmation below names the
	// issues instead of echoing the query back (design §3.5 rule 1).
	_, targets, err := resolveTargets(ctx, newClient, inv, selectorInputs{
		selector:       opts.selector,
		validateRemote: opts.validateRemote,
	})
	if err != nil {
		return zero, err
	}
	out.Selector = targets.Summary

	if inv.DryRun && !opts.validateRemote {
		if warning := command.MeUnresolvedWarning(accounts); warning.Type != "" {
			cli.AddWarning(warning)
		}
		// targets.Keys is empty when a selector went unresolved; the
		// warning ResolveTargets raised says why.
		out.Results = clonePreviewResults(targets.Keys)
		return out, nil
	}

	client, err := newClient()
	if err != nil {
		return zero, err
	}
	// Resolved ONCE for the whole run, before any source is read: every
	// clone gets the same account, and a per-key resolution would pay for
	// the same lookup N times (command.ResolveMe's own reasoning).
	if err := command.ResolveMe(ctx, client, accounts); err != nil {
		return zero, err
	}
	if inv.DryRun {
		out.Results = clonePreviewResults(targets.Keys)
		return out, nil
	}

	// Resolution first, then the question — so the prompt names the
	// issues rather than echoing the query back.
	if err := confirmSelected(deps.Confirm, inv, opts.force,
		selectedWrite{Gerund: "cloning", Imperative: "Clone"}, targets); err != nil {
		if errors.Is(err, errDestructiveAborted) {
			return zero, cli.NewCodedError(cli.CodeCanceled,
				fmt.Sprintf("clone canceled — nothing copied from %s", joinPlainKeys(targets.Keys)))
		}
		return zero, err
	}

	results, replaced, err := cloneFanOut(ctx, client, targets.Keys, opts, overrides)
	// A --replace that matched NOTHING anywhere is the shape that means a
	// typo, and it is the only one worth saying anything about. A pattern
	// matching some sources and not others is an ordinary invocation on a
	// keyed command — `--replace v1=v2` across ten issues where six mention
	// v1 is exactly what the flag is for — so refusing, or warning, on a
	// per-key miss would fail four keys for doing nothing wrong.
	if len(opts.replacements) > 0 && !replaced {
		cli.AddWarning(cloneReplaceUnmatchedWarning(opts.replacements))
	}
	out.Results = results
	return out, err
}

// readCloneOverrides parses --json-input and refuses any collision with a
// field flag, returning the empty request when the route was not taken.
//
// **BOTH halves of the document are checked**, which is §2.1's rule and not
// an optimisation to skip: an override can arrive as `fields.priority` or
// as `update.priority[{"set":"High"}]`, and a check reading only `fields`
// would let the second sit beside `--priority` and compose a request whose
// meaning depends on which half Jira applies last. IssueWriteRequest.Keys
// reads both, which is why the check is one call.
func readCloneOverrides(ctx context.Context, opts cloneOptions, stdin io.Reader, stdinIsTerminal bool) (jira.IssueWriteRequest, error) {
	var req jira.IssueWriteRequest
	if opts.jsonInput == "" {
		return req, nil
	}
	data, err := command.ReadInputFileContext(ctx, command.FlagJSONInput, opts.jsonInput, stdin, stdinIsTerminal)
	if err != nil {
		return req, err
	}
	if err := json.Unmarshal(data, &req); err != nil {
		// The caller's error with the flag named, never a transport error:
		// a malformed document must fail as the input it is rather than
		// traveling to Jira and returning as a 400 about a payload nobody
		// wrote. issueWriteFlags.readPayload argues the same case, and
		// leaves DisallowUnknownFields off for the same reason — this is a
		// Jira payload and rejecting the members this CLI does not model
		// would make the lossless route lossy.
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s: %s is not a valid issue payload: %v", command.FlagJSONInput, opts.jsonInput, err,
		))
		e.Field = command.FlagJSONInput
		return jira.IssueWriteRequest{}, e
	}
	if err := opts.fields.ConflictWithPayload(req); err != nil {
		return jira.IssueWriteRequest{}, err
	}
	return req, nil
}

// cloneReplaceUnmatchedWarning is what a --replace that changed nothing
// anywhere publishes instead of a refusal.
//
// This warning describes the summaries actually composed, independently of
// whether their creates succeeded. Per-key outcomes retain creation facts.
func cloneReplaceUnmatchedWarning(pairs []cloneReplacement) envelope.Warning {
	olds := make([]string, len(pairs))
	for i, p := range pairs {
		olds[i] = strconv.Quote(p.old)
	}
	return envelope.Warning{
		Type: envelope.WarningCloneReplaceUnmatched,
		Message: fmt.Sprintf(
			"--%s changed nothing: no composed source summary contained %s. Inspect per-source outcomes before retrying any clone.",
			flagReplace, strings.Join(olds, " or "),
		),
	}
}

// clonePreviewResults builds the per-key entries for a dry run —
// previewResults' shape, and empty for a reason specific to this
// command: the clone does not exist, and neither does the description of
// what it would contain, because that lives on a source this run did not
// read.
func clonePreviewResults(keys []string) []envelope.KeyedResult[envelope.IssueCloneData] {
	results := make([]envelope.KeyedResult[envelope.IssueCloneData], len(keys))
	for i, key := range keys {
		results[i] = envelope.KeyedResult[envelope.IssueCloneData]{
			Issue: envelope.IssueRef{Key: key},
			OK:    true,
			Data:  &envelope.IssueCloneData{},
		}
	}
	return results
}

// cloneFanOut clones every source and assembles the keyed payload.
//
// The keyed partial-failure policy is commentAddFanOut's, and cloning
// has the sharpest version of the duplicate hazard in this package: a
// clone is not idempotent in any sense, so re-running a source that
// already succeeded produces a second issue with a second key that
// nobody will notice until it turns up in a board.
// It also reports whether ANY key's summary was actually rewritten by
// --replace, which is the one fact about the run that no single key's entry
// can carry — see runClone's use of it.
func cloneFanOut(
	ctx context.Context,
	client core.JiraClient,
	keys []string,
	opts cloneOptions,
	overrides jira.IssueWriteRequest,
) ([]envelope.KeyedResult[envelope.IssueCloneData], bool, error) {
	outcomes := make([]cloneOutcome, len(keys))
	_, err := command.Spin(ctx, cloneSpinnerLabel(keys), func(ctx context.Context) (struct{}, error) {
		// Read field schemas only when a source carries custom fields. One
		// immutable result is shared by the bounded fan-out, including errors.
		schemas := sync.OnceValues(func() (map[string]string, error) {
			return readCloneFieldSchemas(ctx, client)
		})
		xsync.Parallel(opts.parallelism, len(keys), func(i int) {
			outcomes[i] = cloneOne(ctx, client, keys[i], opts, overrides, schemas)
		})
		return struct{}{}, nil
	})
	results := make([]envelope.KeyedResult[envelope.IssueCloneData], len(outcomes))
	raw := make([]error, len(outcomes))
	replaced := false
	for i, o := range outcomes {
		results[i], raw[i] = o.result, o.err
		replaced = replaced || o.replaced
	}
	if err != nil {
		return results, replaced, err
	}
	return results, replaced, command.KeyedFailure(results, raw)
}

// parallelism is opts.parallelism, named so the fan-out reads as one call.
// cloneOutcome is one source's result plus the raw error behind a
// failure — commentAddOutcome's pairing, for its reason.
type cloneOutcome struct {
	result envelope.KeyedResult[envelope.IssueCloneData]
	// err is nil exactly when result.OK.
	err error
	// replaced reports that --replace actually rewrote this source's
	// summary, which the fan-out folds into one answer for the whole run.
	replaced bool
}

// cloneOne reads one source and creates its clone.
//
// A failed create can still carry a native acknowledgement or usable identity.
// Retain both independently of the original error so retrying cannot silently
// duplicate an already acknowledged clone.
func cloneOne(
	ctx context.Context,
	client core.JiraClient,
	key string,
	opts cloneOptions,
	overrides jira.IssueWriteRequest,
	schemas func() (map[string]string, error),
) (outcome cloneOutcome) {
	read, err := client.GetIssue(ctx, key, []string{"*all"})
	// Source identity survives local payload composition failures after the
	// native read, independently of the error chosen for the joined batch.
	defer func() {
		if outcome.result.Err != nil {
			outcome.result.Err.JiraResponse = outcome.result.Err.JiraResponse || read.Issue != nil
		}
	}()
	if err != nil {
		return failedCloneResult(key, err)
	}
	if err := core.ValidateIssueCloneSource(read.Issue); err != nil {
		return failedCloneResult(key, err)
	}

	ranks := make(map[string]bool)
	for name := range val(read.Issue.Fields).Extra {
		if !strings.HasPrefix(name, "customfield_") {
			continue
		}
		definitions, err := schemas()
		if err != nil {
			return failedCloneResult(key, err)
		}
		custom := definitions[name]
		if strings.TrimSpace(custom) == "" {
			return failedCloneResult(key, invalidCloneSource("custom field %q has no usable native schema", name))
		}
		if custom == cloneRankSchema {
			ranks[name] = true
		}
	}
	req, replaced, err := cloneRequest(read.Issue, opts, overrides, ranks)
	if err != nil {
		return failedCloneResult(key, err)
	}
	write, err := client.CreateIssue(ctx, req)
	data := envelope.IssueCloneData{Applied: write.StatusCode == http.StatusCreated, Receipt: write.Receipt}
	if receiptErr := core.ValidateIssueCreation(write); receiptErr == nil {
		data.Clone = &envelope.IssueRef{ID: write.Issue.ID, Key: write.Issue.Key, Self: write.Issue.Self}
	} else if err == nil {
		err = receiptErr
	}
	if err == nil {
		err = core.ValidateIssueCreateFollowups(write.Receipt)
	}
	if err != nil {
		outcome := failedCloneResult(key, err)
		outcome.replaced = replaced
		if data.Applied {
			outcome.result.Data = &data
		}
		return outcome
	}
	return cloneOutcome{
		result: envelope.KeyedResult[envelope.IssueCloneData]{
			Issue: envelope.IssueRef{Key: key},
			OK:    true,
			Data:  &data,
		},
		replaced: replaced,
	}
}

// cloneRequest composes the create payload from the source's fields,
// then lets the caller's own flags override it.
//
// **Order is the whole contract: copy first, override second.** The
// source's values go in, and IssueFieldFlags.Apply writes over whichever
// of them the caller named — so `--summary x` replaces rather than
// merges, which is what a caller typing an override means. Applying them
// the other way round would make every override a no-op for any field
// the source happened to have.
//
// Source-owned lifecycle fields and collections are excluded. Other native
// values retain their bytes, including custom fields, explicit null and
// unknown future members. Rank fields are identified by schema, never by a
// token-shaped value that an ordinary text field could legitimately contain.
// The full order is copy, then payload, then flags, then --replace. The
// payload and the flags cannot disagree — a collision between them was
// refused before any source was read (readCloneOverrides) — so their
// relative order is unobservable, and they are applied in the order §2.1
// lists the routes. Both beat the copy, which is what an override means.
// --replace runs last, over whatever the summary settled at, so the
// transformation reads the same whichever route produced the text.
func cloneRequest(
	source *jira.IssueBean,
	opts cloneOptions,
	overrides jira.IssueWriteRequest,
	ranks map[string]bool,
) (jira.IssueWriteRequest, bool, error) {
	// The source's fields are read back out of their own MARSHALED form
	// rather than off the struct, and that is not a detour.
	// jira.IssueFields declares the common fields as typed members and
	// keeps everything else in Extra, so reading either half alone finds
	// only some of what a clone carries — `summary` is declared,
	// `components` is not. Marshaling merges the two exactly as the wire
	// had them (marshalMergeExtra, through marshalNoEscape), which also
	// means a description's ADF reaches the create request with its
	// bytes intact.
	//
	// This was a real bug before the corpus caught it: reading Extra
	// alone silently dropped the summary, and the unit test agreed
	// because its fixture had put everything in Extra too.
	encoded, err := val(source.Fields).MarshalJSON()
	if err != nil {
		return jira.IssueWriteRequest{}, false, fmt.Errorf("issue.clone: re-encoding the source's fields: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil {
		return jira.IssueWriteRequest{}, false, fmt.Errorf("issue.clone: reading the source's fields: %w", err)
	}
	req := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{}}
	for name, value := range raw {
		if cloneServerField(name) || ranks[name] {
			continue
		}
		req.Fields[name] = value
	}
	for name, member := range map[string]string{
		"project": "key", "issuetype": "name", "priority": "name", "assignee": "accountId",
	} {
		var reference map[string]json.RawMessage
		if value, present := req.Fields[name]; present && json.Unmarshal(value, &reference) == nil {
			var identity string
			if json.Unmarshal(reference[member], &identity) == nil && identity != "" {
				// Keep the identifier's native spelling and remove read-only
				// expanded attributes. Null and absent references stay distinct.
				req.Fields[name] = append(append([]byte(`{"`+member+`":`), reference[member]...), '}')
			}
		}
	}
	req.ApplyOverrides(overrides)
	if err := opts.fields.Apply(&req); err != nil {
		return jira.IssueWriteRequest{}, false, err
	}
	replaced, err := applyCloneReplacements(&req, opts.replacements)
	if err != nil {
		return jira.IssueWriteRequest{}, false, err
	}
	return req, replaced, nil
}

// failedCloneResult records one source's failure, resolved through the
// SAME taxonomy the envelope's own errors[] entries use.
func failedCloneResult(key string, err error) cloneOutcome {
	class := cli.MapError(err)
	return cloneOutcome{
		result: envelope.KeyedResult[envelope.IssueCloneData]{
			Issue: envelope.IssueRef{Key: key},
			OK:    false,
			Err: &envelope.KeyError{
				Code:    class.Code,
				Message: class.Message,
				Hint:    class.Hint,
			},
		},
		err: err,
	}
}

// cloneSpinnerLabel is the fan-out's one spinner label. Present tense,
// and reached only on the live path.
func cloneSpinnerLabel(keys []string) string {
	if len(keys) == 1 {
		return "Cloning " + keys[0]
	}
	return fmt.Sprintf("Cloning %d issues", len(keys))
}

// renderClone writes the human view. Preview tense is structural (axis
// H2): the branches share no sentence.
func renderClone(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueCloneOutput)
	if !ok {
		return fmt.Errorf("issue.clone: human renderer received %T, want envelope.IssueCloneOutput", env.Data)
	}
	if out.DryRun {
		if renderUnresolvedSelector(v, out.Selector, "clone") {
			return nil
		}
		keys := weblinkKeys(out.Results)
		v.Line(fmt.Sprintf("Would clone %s.", joinKeys(keys)))
		v.Hint("The fields a clone carries live on the source issue, so this preview cannot list them — see this command's help for what carries over.")
		v.Hint("Nothing was created. Drop --dry-run to apply it.")
		return nil
	}
	renderCloneResults(v, out)
	return nil
}

// renderCloneResults draws the live view: one line per source, naming
// the key of the issue it produced — which is the value a caller runs
// this command to learn.
func renderCloneResults(v *output.View, out envelope.IssueCloneOutput) {
	for _, r := range out.Results {
		key := cli.Entity(r.Issue.Key)
		if r.Data != nil && r.Data.Applied {
			if r.Data.Clone != nil {
				v.Line(key + cli.Text(" → ") + cli.Entity(r.Data.Clone.Key))
			} else {
				v.Line(key + cli.Text(": Jira acknowledged creation, but returned no usable clone identity."))
			}
		}
		if !r.OK {
			v.Line(key + cli.Text(": "+r.Err.Message))
			v.Hint(r.Err.Hint)
		}
	}
}
