package issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	xslices "github.com/gechr/x/slices"
	xsync "github.com/gechr/x/sync"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// This command's own flag names, spelled once so registration and every
// refusal below name the same strings. The body pair (--comment,
// --comment-file) is not here: it belongs to the shared registrar, which
// owns its own spellings (command.AsComment).
const (
	flagTransition = "transition"
	flagResolution = "resolution"
	flagAssignee   = "assignee"
	flagAtomicOnly = "atomic-only"
)

// transitionAliases is this command's own hidden-alias table — the ONE
// list command.AddHiddenAliases (registration) and
// command.ResolveHiddenAliases (resolution, in Validate below) both read,
// so neither half can drift from the other.
//
// --status is acli's spelling for the same thing (design §5). It is a
// genuine synonym rather than a near-miss, which is what §2 requires
// before a spelling may be aliased at all: acli's --status names the
// workflow state to end up in, and so does --transition — this command
// already accepts either a transition NAME or a target STATUS in that
// same value (see splitPositionals and resolveTransition), so the two
// flags do not merely overlap, they take the identical grammar.
//
// Worth saying which --status this is NOT. `issue list --status` is a
// repeatable JQL FILTER matching issues in a state; this one names the
// state to move an issue TO. Same word, opposite direction, different
// commands — and that is exactly why it is a per-command table here
// rather than a row in any shared registrar, where it would land on
// every command that ever composes a list filter.
var transitionAliases = []command.HiddenAlias{
	{Flag: "status", Canonical: flagTransition},
}

// newTransition returns `jira issue transition`.
//
// THE FIRST COMMAND WHOSE REQUEST SHAPE IS NOT KNOWN UNTIL JIRA IS
// ASKED, and the reason is a platform behavior no document surfaces: a
// transition with no screen accepts a comment, performs the status
// change, and silently discards the comment. Jira returns 204 either
// way. The OpenAPI spec says nothing — IssueUpdateDetails carries
// `update` and never mentions screens — and a default Jira Cloud
// workflow has no transition screens at all, so the naive
// implementation ships a --comment that loses the comment on the
// commonest configuration there is.
//
// Everything unusual about this file follows from that one fact:
//
//   - the transition list is read before the write, and hasScreen off
//     that read decides the request shape (resolveTransition);
//   - a screenless transition splits into two requests and SAYS so, in
//     the envelope and on stderr (transitionOne);
//   - --atomic-only refuses instead of splitting, for a caller who needs
//     the guarantee rather than the outcome;
//   - a comment that fails after the transition landed is a SUCCESSFUL
//     key carrying a warning, because the status did change
//     (transitionOne, and envelope.IssueTransitionData's own doc).
func newTransition(deps Deps) *cobra.Command {
	var keyed command.KeyedFlags
	var sel command.SelectorFlags
	var body command.BodyFlags
	// WithValidateRemote and nothing else. --force has nothing to skip
	// (a transition prompts for no confirmation), --verify has no extra
	// round trip to gate (there is no readback here to decline), and
	// --no-readback would suppress a readback this command never makes.
	// The remote flag earns its place because screen state is knowable
	// only from the site, so a preview that wants to report the ROUTE
	// has to be allowed to ask (design §3.1).
	var mut command.MutationFlags
	var transition, resolution, assignee string
	var atomicOnly bool
	// cmd is captured so Validate can reach the parsed flag set and the
	// positional arguments — the same capture newCommentAdd makes for
	// alias folding, needed here for that AND for the positional split,
	// which cannot happen in Spec.Validate any other way (Validate is
	// handed no args).
	var cmd *cobra.Command

	cmd = command.MustNew(command.Spec[envelope.IssueTransitionOutput]{
		Op:            "issue.transition",
		WritesJira:    true,
		Identity:      true,
		Keyed:         true,
		Passthrough:   true,
		RetainOnError: func(out envelope.IssueTransitionOutput) bool { return len(out.Results) > 0 },
		// Native request members survive in payload and results[].data.payload.
		// Jira's camelCase field IDs and extensions retain their own vocabulary
		// at these exact boundaries; the surrounding CLI fields remain curated.
		Use:   "transition ISSUE_KEY... [STATUS]",
		Short: "Move issues through the workflow",
		Long: "Move one or more issues to a new status, and optionally comment in " +
			"the same breath.\n\n" +
			"Name the status as the last argument, or as `--transition`. With " +
			"`--transition` every positional is an issue key; without it the " +
			"LAST positional is the status and everything before it is a key, so " +
			"`jira issue transition KEY-123 KEY-124 Done` moves two issues. The " +
			"value matches a transition's own name, the name of the status it " +
			"leads to, or a transition id. A bare ID skips lookup; payloads require workflow reads.\n\n" +
			"Discover IDs, destinations and screen field metadata with `jira issue transition list ISSUE_KEY...`.\n\n" +
			"Positional target completion offers fresh cached IDs from explicit transition-list reads, scoped to each issue, site and account. Multiple typed keys need a common ID with the same known destination. These are offline hints, not permission or a promise that the transition remains available. Native payloads and unresolved selectors offer no target hints.\n\n" +
			"`--json-input` accepts the whole native transition request: `transition`, " +
			"`fields`, `update`, `historyMetadata`, `properties` and other request members. " +
			"A payload target replaces the positional status, so every positional is an issue key. " +
			"An agreeing `--transition` is allowed; conflicting targets are refused before reads. " +
			"A flat field object with a payload or CLI target and a bare ADF comment remain supported. " +
			"A top-level ADF `comment` uses the same fallback as `--comment`; native " +
			"fields, update operations and properties stay in one request and are refused on a screenless transition. " +
			"Use explicit request sections to distinguish native extensions from flat fields. " +
			"Duplicate JSON members and field/comment inputs are refused. Returned screen metadata checks " +
			"required inputs and defaults before writes. Ambiguous destinations require an ID; " +
			"explicitly unavailable transitions are excluded.\n\n" +
			"**`--comment` is not always atomic, and the envelope always says " +
			"which it was.** Jira honors fields and comments sent with a " +
			"transition only when that transition has a SCREEN. On a screenless " +
			"transition it can accept the request, change the status, and discard " +
			"those changes without a word — and a default Jira Cloud workflow " +
			"has no transition screens. So this command reads the workflow " +
			"first: where there is a screen the comment rides the same request " +
			"(`comment_posted: \"atomic\"`), and where there is not it posts the " +
			"comment as a second request and tells you (`\"separate\"`, plus a " +
			"warning). `--atomic-only` refuses the key instead of splitting, for " +
			"a caller who needs the guarantee rather than the outcome.\n\n" +
			"`--resolution` and `--assignee` ride the same request as fields, so " +
			"\"close as Won't Do, reassign, and explain\" is one call where a " +
			"screen exists. They have NO fallback: unlike a comment, there is no " +
			"automatic separate field write here, so on a screenless transition " +
			"the key is refused rather than moved with the fields silently " +
			"dropped. `--assignee` takes an Atlassian account id.\n\n" +
			"This is a keyed command: one entry per key at `data.results[]`, in " +
			"the order you gave them. A key whose COMMENT failed is a " +
			"SUCCESSFUL entry — the status changed, which is what you asked for " +
			"— carrying `comment_posted: \"failed\"` and a warning naming the " +
			"key. Retrying such a key would re-transition an issue that already " +
			"moved, so the envelope is careful never to invite it.\n\n" +
			"Total failure, timeout and cancellation retain ordered per-key outcomes. " +
			"`attempted` means the write seam was invoked; `applied` records a native " +
			"HTTP 204 acknowledgement, including one joined after interruption. " +
			"Neither is a status readback. A failed comment may already be stored; " +
			"inspect the comment list before adding another. Its error code and hint " +
			"remain in the per-key data.\n\n" +
			"`--dry-run` composes the request and stops without contacting " +
			"Jira, so on its own it can report the comment that WOULD be sent " +
			"but not which route it would take — screen state lives on the " +
			"site. Add `--validate-remote` and the preview reads the workflow " +
			"and fills in `comment_plan` for each key, still writing nothing.\n\n" +
			command.SelectorHelp + "\n\n" +
			"`--web` is refused here. It opens one page and this command writes " +
			"to a list of keys.",
		Example: "# move one issue\n" +
			"$ jira issue transition KEY-123 Done\n\n" +
			"# several issues at once\n" +
			"$ jira issue transition KEY-123 KEY-124 'In Progress'\n\n" +
			"# the status as a flag, so every positional is a key\n" +
			"$ jira issue transition KEY-123 --transition Done\n\n" +
			"# move it and explain why, atomically where the workflow allows\n" +
			"$ jira issue transition KEY-123 Done --comment 'Fixed in 1.4.2.'\n\n" +
			"# refuse rather than split the comment into a second request\n" +
			"$ jira issue transition KEY-123 Done --comment 'Fixed.' --atomic-only\n\n" +
			"# close it out, reassign, and explain — one request where a screen exists\n" +
			"$ jira issue transition KEY-123 Done --resolution \"Won't Do\" --assignee 5b10a2844c20165700ede21g --comment 'Superseded.'\n\n" +
			"# find out which route the comment would take, without writing anything\n" +
			"$ jira issue transition KEY-123 Done --comment 'Draft.' --dry-run --validate-remote\n\n" +
			"# preview a native request whose transition member names the target\n" +
			"$ jira issue transition KEY-123 --json-input transition.json --dry-run --output=json\n\n" +
			"# validate that native request against the current workflow without writing\n" +
			"$ jira issue transition KEY-123 --json-input transition.json --dry-run --validate-remote --output=json\n\n" +
			"# just the keys whose comment did not ride the transition\n" +
			"$ jira issue transition KEY-123 KEY-124 Done --comment 'Done.' --output=json --jq '.data.results[] | select(.data.comment_posted != \"atomic\") | .issue.key'\n\n" +
			"# every issue a query matches; the status is still a positional\n" +
			"$ jira issue transition --jql 'project = KEY AND status = \"In Review\"' Done",
		// Min is 0 because a selector replaces the KEY half of this
		// command's `KEY… [STATUS]` grammar entirely. splitPositionals and
		// SelectorFlags.Validate own the refusals from here; cobra's arity
		// check cannot express "keys OR a selector, and a status either
		// way".
		Args: command.ArgSpec{Min: 0, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "transitionissuekey"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddKeyedFlags(c, &keyed)
			// AsComment: the capability shipped in 0.15 as --markdown,
			// named for its encoding rather than its purpose, and nobody
			// found it. --markdown still works, unlisted (design §3.1).
			command.AddBodyFlags(c, &body, command.AsComment(), command.WithJSONInputForwarding(), command.WithJSONInputHelp(
				"Read a whole native transition payload or an ADF comment from this JSON file, or `-` for standard input", "Payload from file",
			))
			// --force arrives WITH the selectors: a transition that names its
			// keys still prompts for nothing, and a selector-driven one
			// prompts because the caller did not name the issues. The
			// "--force has nothing to skip" note above newTransition was true
			// until the selectors gave it something.
			command.AddMutationFlags(c, &mut, command.WithValidateRemote(), command.WithForce())
			command.AddSelectorFlags(c, &sel)
			command.AddStringVar(c, &transition, flagTransition, "",
				"The status or transition to move to, instead of the last positional",
				command.FlagOpts{Terse: "Target status", Placeholder: "status", Forward: true})
			command.AddStringVar(c, &resolution, flagResolution, "",
				"Set the resolution as part of the transition",
				command.FlagOpts{Terse: "Set resolution", Placeholder: "name", Complete: "predictor=cacheresolution"})
			command.AddStringVar(c, &assignee, flagAssignee, "",
				"Assign the issue as part of the transition, by Atlassian account id",
				command.FlagOpts{Terse: "Set assignee", Placeholder: "account-id"})
			command.AddBoolVar(c, &atomicOnly, flagAtomicOnly, false,
				"Refuse a key whose comment cannot ride the transition itself",
				command.FlagOpts{Terse: "Atomic or nothing"})
			command.AddHiddenAliases(c, transitionAliases)
		},
		Validate: func() error {
			if err := keyed.Validate(); err != nil {
				return err
			}
			// Alias folding FIRST, for newCommentAdd's reason: --body must
			// have become --comment before the conflict check looks.
			if err := body.ResolveAliases(cmd.Flags()); err != nil {
				return err
			}
			// And this command's own alias, for the same reason one step
			// further on: --status must have become --transition before
			// splitPositionals below reads it, or an invocation that named
			// the target with --status alone would look to that function
			// like one that named no target at all and would be refused
			// for missing the positional it deliberately replaced.
			if err := command.ResolveHiddenAliases(cmd.Flags(), transitionAliases); err != nil {
				return err
			}
			if err := body.Conflict(); err != nil {
				return err
			}
			// cmd.Flags().Args() is what cobra itself hands RunE, read one
			// step early. Spec.Validate takes no arguments, and refusing a
			// bad positional split here rather than at the top of Run is
			// what keeps every input refusal on the same side of the I/O
			// line as the rest of them.
			var keys []string
			if body.JSONInput == "" {
				var err error
				keys, _, err = splitPositionals(cmd.Flags().Args(), transition, sel.Given())
				if err != nil {
					return err
				}
			} else if !sel.Given() {
				// Payload target grammar needs file I/O. Validate key presence
				// now; Run validates the resolved split before selector I/O.
				keys = cmd.Flags().Args()
			}
			// The SPLIT key count, never len(args): this command spends a
			// positional on the status, and counting raw argv would read
			// `--jql … Done` as one key beside a selector and refuse a
			// perfectly good invocation.
			if err := sel.Validate(len(keys)); err != nil {
				return err
			}
			if err := atomicOnlyNeedsComment(atomicOnly, &body); err != nil {
				return err
			}
			return refuseWebOnKeyedWrite(cmd)
		},
		Human: renderTransition,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueTransitionOutput, error) {
			return runTransition(ctx, deps, inv, transitionOptions{
				body:           &body,
				selector:       sel,
				force:          mut.Force,
				flagTransition: transition,
				resolution:     resolution,
				assignee:       assignee,
				atomicOnly:     atomicOnly,
				validateRemote: mut.ValidateRemote,
				parallelism:    keyed.Parallelism,
			})
		},
	})
	cmd.AddCommand(newTransitionList(deps))
	return cmd
}

// transitionOptions is what runTransition needs from the flag set,
// gathered into one struct so the handler's signature does not grow a
// parameter per flag — commentAddOptions' own reasoning, and --dry-run
// is absent here for its reason too: it arrives on the Invocation.
type transitionOptions struct {
	body *command.BodyFlags
	// selector and force are the bulk-selector surface. force is only
	// ever consulted for a selector-driven run: naming keys prompts for
	// nothing, so there is no prompt for it to skip.
	selector       command.SelectorFlags
	force          bool
	flagTransition string
	resolution     string
	assignee       string
	atomicOnly     bool
	validateRemote bool
	parallelism    int
}

// splitPositionals divides the positional arguments into issue keys and
// the status to move to.
//
// The grammar is `KEY… [STATUS]`, which is ambiguous on its face — the
// arguments are all bare words and nothing in them says where the keys
// stop. The rule that resolves it is the presence of --transition, not
// the SHAPE of the last argument: matching a key pattern would make
// `jira issue transition KEY-123 KEY-124` mean "move KEY-123 to a status
// called KEY-124", which is absurd, while a project whose keys are
// unusual would break a pattern-matcher in the other direction. So the
// flag decides, and the rule is one sentence a person can hold: with
// --transition every positional is a key, without it the last one is the
// status.
//
// It is a pure function called from BOTH Validate (to refuse) and Run
// (to use), rather than a split performed once and remembered, so the
// two can never disagree about where the boundary fell.
//
// **selectorGiven changes which half is optional, and nothing else.** A
// bulk selector names the issues, so the keys stop being required and a
// lone positional becomes the STATUS rather than a key with no status —
// which is the opposite reading of the same argv, and the one reason
// this function has to know about selectors at all. The boundary rule is
// untouched: --transition still decides whether the last positional is a
// status, so `--jql … KEY-1 Done` still splits into a key and a status,
// and it is SelectorFlags.Validate that refuses the key beside the
// selector — not this function, which would otherwise have to know a
// second rule it has no business owning.
func splitPositionals(args []string, transitionFlagValue string, selectorGiven bool) (keys []string, status string, err error) {
	if transitionFlagValue != "" {
		if len(args) == 0 && !selectorGiven {
			return nil, "", missingKeys()
		}
		return args, transitionFlagValue, nil
	}
	switch len(args) {
	case 0:
		if selectorGiven {
			return nil, "", missingStatus()
		}
		return nil, "", missingKeys()
	case 1:
		if selectorGiven {
			// The selector named the issues, so this single word is the
			// status — the exact argv that means "a key with no status"
			// without one.
			return nil, args[0], nil
		}
		// One positional and no flag. It is a key with no status, not a
		// status with no key: the arity check already required at least
		// one argument and a caller who typed a single word meant the
		// issue. Saying so beats a generic count error, which would send
		// them to --help to discover a grammar they nearly had right.
		// flagTransition is the flag's NAME. It used to be the parameter
		// holding the flag's VALUE, which is "" everywhere this branch is
		// reachable — the early return above takes every invocation that
		// set it — so the sentence rendered "name it with --" and pointed
		// the reader at nothing. The parameter is now spelled
		// transitionFlagValue so the two cannot be confused again.
		e := cli.NewInputError(cli.CodeArgCountInvalid, fmt.Sprintf(
			"no status given — discover available transitions with `jira issue transition list %s`, then add the status after the key or name it with --%s",
			args[0], flagTransition,
		))
		e.Field = flagTransition
		e.Suggestions = []string{"jira issue transition list " + args[0]}
		return nil, "", e
	}
	return args[:len(args)-1], args[len(args)-1], nil
}

// missingKeys refuses an invocation with no issue key at all. Reachable
// only through --transition with no positionals and no selector: without
// either, the ArgSpec's own minimum has already refused an empty argv.
func missingKeys() error {
	return cli.NewInputError(cli.CodeArgCountInvalid,
		"no issue key given — name at least one issue to transition")
}

// missingStatus refuses a selector-driven invocation that never says
// which status to move to. The selector supplies the issues, so the
// status is the only half left, and nothing else in the argv can stand
// in for it.
func missingStatus() error {
	e := cli.NewInputError(cli.CodeArgCountInvalid, fmt.Sprintf(
		"no status given — add the status after the selector (`jira issue transition --%s '…' Done`) or name it with --%s",
		command.FlagJQL, flagTransition,
	))
	e.Field = flagTransition
	return e
}

// atomicOnlyNeedsComment refuses --atomic-only on an invocation with no
// comment.
//
// The flag's entire job is to turn the comment's screenless fallback
// into a refusal. With no comment there is no fallback, so the flag
// would silently do nothing — and a flag that does nothing is worse than
// an absent one, because an agent reading the schema will pass it and
// believe it bought a guarantee (command.WithForce's own argument,
// applied to this command's own flag).
func atomicOnlyNeedsComment(atomicOnly bool, body *command.BodyFlags) error {
	if !atomicOnly || body.Given() {
		return nil
	}
	e := cli.NewInputError(cli.CodeFlagNotApplicable, fmt.Sprintf(
		"--%s only means something alongside a comment — it makes a comment that cannot ride the transition a refusal instead of a second request",
		flagAtomicOnly,
	))
	e.Field = flagAtomicOnly
	return e
}

// runTransition composes the request once and either previews it or
// applies it to every key.
//
// **Compose once, apply N times**, and **the preview builds no client**
// — both properties are runCommentAdd's, adopted here unchanged and for
// its reasons, with ONE documented exception. `--dry-run
// --validate-remote` does build a client, because the question that
// combination exists to answer (which route would the comment take?) is
// answerable only by the site. It still writes nothing: the remote
// preview reads the transition list and stops, and nothing on that path
// can reach DoTransition or AddComment.
//
// That exception is why the split between --dry-run and --validate-remote
// is worth keeping rather than collapsing. A bare --dry-run remains
// honestly free of network and works with no credentials at all; asking
// for the network is a separate, explicit act.
func runTransition(ctx context.Context, deps Deps, inv command.Invocation, opts transitionOptions) (envelope.IssueTransitionOutput, error) {
	var zero envelope.IssueTransitionOutput

	input, err := readTransitionInput(ctx, opts.body, inv.Stdin, inv.StdinIsTerminal)
	if err != nil {
		return zero, err
	}
	keys, status, err := transitionInputArgs(inv.Args, opts.flagTransition, input.target, opts.selector.Given())
	if err != nil {
		return zero, err
	}
	if err := opts.selector.Validate(len(keys)); err != nil {
		return zero, err
	}
	if err := transitionFieldConflicts(input.payload, opts.resolution, opts.assignee); err != nil {
		return zero, err
	}
	if opts.atomicOnly && len(input.body.ADF) == 0 {
		return zero, atomicOnlyNeedsComment(true, &command.BodyFlags{})
	}

	// ONE client for the run, however many resolvers want one — the
	// selector's search, and the fan-out itself.
	newClient := oneClient(deps.JiraClient)

	// The selector resolves FIRST, so the confirmation below names the
	// issues instead of echoing the query back (design §3.5 rule 1).
	targetInvocation := inv
	targetInvocation.Args = keys
	_, targets, err := resolveTargets(ctx, newClient, targetInvocation, selectorInputs{
		selector:       opts.selector,
		validateRemote: opts.validateRemote,
	})
	if err != nil {
		return zero, err
	}
	keys = targets.Keys
	plan := transitionPlan{
		want:        status,
		comment:     input.body.ADF,
		resolution:  opts.resolution,
		assignee:    opts.assignee,
		atomicOnly:  opts.atomicOnly,
		payload:     input.payload,
		targetExtra: input.targetExtra,
		native:      input.native,
	}
	payload, err := localTransitionPayload(plan)
	if err != nil {
		return zero, err
	}
	out := envelope.IssueTransitionOutput{
		MutationState: envelope.MutationState{DryRun: inv.DryRun},
		Transition:    status,
		Comment:       input.body.ADF,
		Payload:       payload,
		AtomicOnly:    opts.atomicOnly,
		Resolution:    opts.resolution,
		Assignee:      opts.assignee,
	}
	if len(input.body.ADF) > 0 {
		out.CommentRoute = input.body.Route
	}

	out.Selector = targets.Summary

	if inv.DryRun && !opts.validateRemote {
		// keys is empty when a selector went unresolved; the warning
		// ResolveTargets raised says why.
		out.Results = localPreviewResults(keys)
		return out, nil
	}

	client, err := newClient(ctx)
	if err != nil {
		return zero, err
	}
	if inv.DryRun {
		results, err := remotePreviewFanOut(ctx, client, keys, plan, opts.parallelism)
		out.Results = results
		return out, err
	}
	// Resolution first, then the question — so the prompt names the
	// issues rather than echoing the query back.
	if err := confirmSelected(ctx, deps.Confirm, inv, opts.force,
		selectedWrite{Gerund: "transitioning", Imperative: "Transition"}, targets); err != nil {
		if errors.Is(err, errDestructiveAborted) {
			return zero, cli.NewCodedError(cli.CodeCanceled,
				fmt.Sprintf("transition canceled — %s unchanged", joinPlainKeys(keys)))
		}
		return zero, err
	}

	store := transitionHintCache(ctx, deps)
	results, err := transitionFanOut(ctx, client, keys, plan, opts.parallelism)
	out.Results = results
	invalidateTransitionHints(store, results)
	return out, err
}

// transitionPlan is the resolved request, identical for every key. It is
// what compose-once composes: one target, one comment document, one set
// of field values, decided before the first key is touched.
type transitionPlan struct {
	// want is what the caller asked to move to, unresolved: a transition
	// name, a status name, or a transition id. Which of the three it is
	// can only be settled per key, against that key's own workflow.
	want string
	// comment is the ADF document to attach, empty when none was given.
	comment     []byte
	resolution  string
	assignee    string
	atomicOnly  bool
	payload     jira.IssueWriteRequest
	targetExtra map[string]json.RawMessage
	native      bool
}

// requiresScreenPayload reports whether composed fields, operations or
// properties need a workflow screen. Convenience comments can fall back to
// a separate request and are checked independently.
func (p transitionPlan) requiresScreenPayload() bool {
	return p.resolution != "" || p.assignee != "" || len(p.payload.Fields) > 0 || len(p.payload.Update) > 0 || transitionValuePresent(p.payload.Extra["properties"])
}

// wantsComment reports whether the plan carries a comment.
func (p transitionPlan) wantsComment() bool { return len(p.comment) > 0 }

// needsScreenState reports whether this plan must read the transition
// list before it can act.
//
// The design's rule is "the screen read fires ONLY when a comment flag
// is present" — a bare `transition KEY-123 Done` must not pay a round
// trip it never paid before. Implementing it exposed that the rule is
// slightly wrong about the mechanism, in a way that makes it MORE true
// rather than less: the transition endpoint takes an id and nothing
// else, so resolving the word "Done" already requires this exact read.
// The comment does not add a round trip; it only makes one unavoidable
// that a bare id could have skipped.
//
// So the real rule, which honors the design's intent exactly, is: read
// the list unless the caller gave a bare id AND asked for nothing that
// depends on a screen. That is the only invocation shape where the read
// is genuinely optional, and it is the one that skips it.
func (p transitionPlan) needsScreenState() bool {
	return p.native || p.wantsComment() || p.requiresScreenPayload() || !isTransitionID(p.want)
}

// isTransitionID defines the decimal-ID spelling. Numeric inputs select IDs;
// names and destinations are resolved only for other spellings.
func isTransitionID(want string) bool {
	if want == "" {
		return false
	}
	return strings.IndexFunc(want, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

// localPreviewResults builds the per-key entries for a bare `--dry-run`:
// one per key, in argv order, each successful and each carrying an empty
// operation object.
//
// Empty, and honestly so. A local preview knows the comment it would
// send — that is up at the top of the envelope, identical to what a live
// run posts — and it knows nothing else per key: not which transition
// "Done" resolves to on this issue's workflow, not whether that
// transition has a screen, not which route the comment would take.
// Filling any of those in would be inventing a verification that never
// happened, so the fields stay absent and `--validate-remote` is what
// fills them.
func localPreviewResults(keys []string) []envelope.KeyedResult[envelope.IssueTransitionData] {
	return xslices.Map(keys, func(key string) envelope.KeyedResult[envelope.IssueTransitionData] {
		return envelope.KeyedResult[envelope.IssueTransitionData]{
			Issue: envelope.IssueRef{Key: key},
			OK:    true,
			// A pointer to an empty struct rather than nil, for
			// previewResults' reason: the contract says a successful key
			// carries `data`, and a preview's key succeeded.
			Data: &envelope.IssueTransitionData{},
		}
	})
}

// transitionOutcome is one key's result plus the raw error behind a
// failure — commentAddOutcome's pairing, for its reason: the envelope's
// KeyError is the right shape to PUBLISH per key and the wrong thing to
// fail the command with.
type transitionOutcome struct {
	result envelope.KeyedResult[envelope.IssueTransitionData]
	// err is nil exactly when result.OK.
	err error
	// warning is a non-fatal thing that happened to a SUCCESSFUL key,
	// carried out of the goroutine rather than raised inside it so the
	// warnings reach the envelope in argv order rather than in whichever
	// order the fan-out finished. Zero value means nothing to say.
	warning envelope.Warning
}

// remotePreviewFanOut answers `--dry-run --validate-remote` for every
// key: it reads each key's workflow and reports which route the comment
// would take, and it writes nothing.
//
// A separate fan-out from the live one rather than a flag threaded
// through it. The two share the resolve step and diverge completely
// after it — this one has no write to make and no partial failure to
// report — and a shared function with an `if dryRun` around its only
// side effect is exactly the shape that let `config edit`'s preview go
// live. Here the preview path cannot write because the code that writes
// is not on it.
func remotePreviewFanOut(
	ctx context.Context,
	client core.JiraClient,
	keys []string,
	plan transitionPlan,
	parallelism int,
) ([]envelope.KeyedResult[envelope.IssueTransitionData], error) {
	outcomes := make([]transitionOutcome, len(keys))
	_, err := command.Spin(ctx, previewSpinnerLabel(keys), func(ctx context.Context) (struct{}, error) {
		xsync.Parallel(parallelism, len(keys), func(i int) {
			outcomes[i] = previewOne(ctx, client, keys[i], plan)
		})
		return struct{}{}, nil
	})
	results, failure := collectOutcomes(outcomes)
	return results, errors.Join(err, failure)
}

// previewOne resolves one key's transition and reports the route a live
// run would take, without writing anything.
func previewOne(ctx context.Context, client core.JiraClient, key string, plan transitionPlan) transitionOutcome {
	view, err := resolveTransition(ctx, client, key, plan)
	if err != nil {
		return failedTransitionResult(key, err)
	}
	data := envelope.IssueTransitionData{Transition: view}
	if plan.requiresScreenPayload() && view != nil && !view.HasScreen {
		// The same refusal the live path makes, foreseen rather than
		// performed. A preview that stayed silent here would report a
		// route for the comment and say nothing about the key being
		// refused outright.
		return failedTransitionResult(key, fieldsNeedScreen(view, plan))
	}
	if plan.wantsComment() {
		data.CommentPlan = commentPlanFor(view, plan.atomicOnly)
	}
	payload, err := transitionRequest(plan, view.ID, view.HasScreen).MarshalJSON()
	if err != nil {
		return failedTransitionResult(key, err)
	}
	data.Payload = payload
	return transitionOutcome{result: envelope.KeyedResult[envelope.IssueTransitionData]{
		Issue: envelope.IssueRef{Key: key},
		OK:    true,
		Data:  &data,
	}}
}

// commentPlanFor names the route a live run would take for one key.
//
// A nil view means the transition was resolved without reading the list
// — a bare id with nothing screen-dependent asked for — which cannot
// happen alongside a comment (needsScreenState forces the read), so the
// atomic answer here is unreachable through that door and stays only as
// the total case.
func commentPlanFor(view *envelope.TransitionView, atomicOnly bool) string {
	switch {
	case view == nil || view.HasScreen:
		return envelope.CommentPlanAtomic
	case atomicOnly:
		return envelope.CommentPlanRefused
	}
	return envelope.CommentPlanSeparate
}

// transitionFanOut applies the plan to every key and assembles the keyed
// payload.
//
// The keyed partial-failure policy is commentAddFanOut's, unchanged and
// for its reasons, with one addition this command needs and that one did
// not: a key can half-succeed WITHIN itself. The transition lands and
// the comment does not. That is not a failed key — the status changed,
// which is what the caller asked for, and reporting it as a failure
// would invite a retry that re-transitions an issue that already moved,
// possibly down a different edge of the workflow. So it is a successful
// key carrying comment_posted=failed and a warning that names it.
//
// Read-only mode needs no check here, for commentAddFanOut's reason: the
// transport refuses each POST before the wire. The transition LIST is a
// GET and is allowed, so a read-only run resolves every key and then
// fails every one of them identically at the write — which is the same
// answer a single key would have produced.
func transitionFanOut(
	ctx context.Context,
	client core.JiraClient,
	keys []string,
	plan transitionPlan,
	parallelism int,
) ([]envelope.KeyedResult[envelope.IssueTransitionData], error) {
	outcomes := make([]transitionOutcome, len(keys))
	// ONE spinner around the whole fan-out, for commentAddFanOut's
	// reason: a spinner owns the terminal's live region and several
	// drawing at once interleave into corruption.
	_, err := command.Spin(ctx, transitionSpinnerLabel(keys, plan.want), func(ctx context.Context) (struct{}, error) {
		// Every key runs regardless of another's failure. Parallel writes
		// outcomes[i] from the goroutine handling key i, which needs no
		// lock: x/sync.Parallel hands each call a distinct index and the
		// slice was sized up front.
		xsync.Parallel(parallelism, len(keys), func(i int) {
			outcomes[i] = transitionOne(ctx, client, keys[i], plan)
		})
		return struct{}{}, nil
	})
	// Spin joins every worker before assembly. Preserve acknowledgements and
	// failures while the interruption still describes the whole run.
	results, failure := collectOutcomes(outcomes)
	return results, errors.Join(err, failure)
}

// collectOutcomes splits the per-key outcomes into the published results
// and the errors behind them, raises each successful key's warning in
// ARGV order, and applies the total-failure rule.
//
// Shared by both fan-outs because the assembly is identical; only what
// fills the outcomes differs.
//
// The warnings are raised HERE rather than inside the goroutines, and
// the ordering is the whole reason: cli.AddWarning appends to a
// process-wide slice, so raising from a concurrent fan-out would order
// the warnings by whichever key finished first and make the same
// invocation print them differently run to run. Walking the outcomes in
// index order afterwards makes them argv-ordered, exactly like
// results[]. This is the worked example cli.AddWarning's own doc comment
// points a keyed command at.
func collectOutcomes(outcomes []transitionOutcome) ([]envelope.KeyedResult[envelope.IssueTransitionData], error) {
	results := make([]envelope.KeyedResult[envelope.IssueTransitionData], len(outcomes))
	raw := make([]error, len(outcomes))
	for i, o := range outcomes {
		results[i], raw[i] = o.result, o.err
		if o.warning.Type != "" {
			cli.AddWarning(o.warning)
		}
	}
	return results, command.KeyedFailure(results, raw)
}

// transitionOne moves one key and attaches its comment by whichever
// route the workflow allows. It returns a result rather than an error
// because a failure here is data — the entry the caller reads at
// data.results[] — and not the command's own failure.
func transitionOne(ctx context.Context, client core.JiraClient, key string, plan transitionPlan) (outcome transitionOutcome) {
	if err := ctx.Err(); err != nil {
		return failedTransitionResult(key, err)
	}
	var observed bool
	client = transitionReadObserver{JiraClient: client, observed: &observed}
	defer func() {
		if outcome.result.Err != nil {
			outcome.result.Err.JiraResponse = outcome.result.Err.JiraResponse || observed
		}
	}()
	view, err := resolveTransition(ctx, client, key, plan)
	if err != nil {
		return failedTransitionResult(key, err)
	}
	screened := view == nil || view.HasScreen

	// Both refusals happen BEFORE the write, so a refused key is a key
	// nothing happened to. Refusing after the transition landed would
	// leave the caller with a moved issue and an error, which is the
	// worst of both answers.
	if plan.requiresScreenPayload() && !screened {
		return failedTransitionResult(key, fieldsNeedScreen(view, plan))
	}
	if plan.wantsComment() && !screened && plan.atomicOnly {
		return failedTransitionResult(key, atomicUnavailable(view))
	}

	atomic := plan.wantsComment() && screened
	req := transitionRequest(plan, view.ID, atomic)
	payload, err := req.MarshalJSON()
	if err != nil {
		return failedTransitionResult(key, err)
	}
	write, err := client.DoTransition(ctx, key, req)
	data := envelope.IssueTransitionData{Transition: view, Payload: payload, Attempted: true, Applied: write.StatusCode == http.StatusNoContent}
	if atomic && data.Applied {
		data.CommentPosted = envelope.CommentPostedAtomic
	}
	if !data.Applied && err == nil {
		err = core.ErrTransitionResponseInvalid
	}
	if err != nil {
		outcome := failedTransitionResult(key, err)
		outcome.result.Data = &data
		return outcome
	}
	if plan.wantsComment() && !atomic {
		return separateComment(ctx, client, key, plan, data)
	}
	return transitionOutcome{result: okTransitionResult(key, data)}
}

// transitionReadObserver retains a native metadata response even when resolving
// its contents produces a local refusal. Each worker owns its observation.
type transitionReadObserver struct {
	core.JiraClient
	observed *bool
}

func (c transitionReadObserver) GetTransitions(ctx context.Context, key string) (core.TransitionRead, error) {
	read, err := c.JiraClient.GetTransitions(ctx, key)
	*c.observed = *c.observed || read.Transitions != nil
	return read, err
}

// separateComment posts the comment as its own request, after a
// transition that has already landed.
//
// Reached only where the transition had no screen, so the comment could
// not have ridden it. The transition is DONE by the time this runs, and
// every branch below therefore reports a successful key: the caller's
// status change happened, and what varies is only whether the comment
// joined it.
//
// It reuses the same AddComment seam `issue comment add` writes through,
// rather than a transition-specific one, because it is the same act — a
// comment on an issue — and a caller inspecting the result finds a
// comment indistinguishable from one posted any other way. Its id comes
// back for free in the 201, which is why the separate path can publish
// comment_id and the atomic path (a 204 with no body) cannot.
func separateComment(
	ctx context.Context,
	client core.JiraClient,
	key string,
	plan transitionPlan,
	data envelope.IssueTransitionData,
) transitionOutcome {
	write, err := client.AddComment(ctx, key, jira.AddCommentRequest{Body: json.RawMessage(plan.comment)})
	receiptErr := core.ValidateCommentReceipt(write.Comment, "")
	if receiptErr == nil {
		// An accompanying error does not erase a usable resource identity.
		data.CommentID = write.Comment.ID.Value
	}
	// A failed call need not return a receipt. Do not replace a permission,
	// cancellation or transport error with a fabricated acknowledgement error.
	// Successful calls still require a usable native comment identity.
	if err == nil {
		err = receiptErr
	}
	if err != nil {
		class := cli.MapError(err)
		data.CommentPosted = envelope.CommentPostedFailed
		data.CommentError, data.CommentErrorCode = class.Message, class.Code
		data.CommentErrorHint = "Inspect the issue's comments before adding another; the comment may already exist. Do not repeat the acknowledged transition."
		return transitionOutcome{
			result: okTransitionResult(key, data),
			warning: envelope.Warning{
				Type: envelope.WarningCommentFailed,
				Message: fmt.Sprintf(
					"%s moved to %s, but its comment could not be confirmed (%s). Inspect the comments before adding one with `jira issue comment add %s`; do not repeat the acknowledged transition.",
					key, transitionStatusName(data.Transition), data.CommentError, key,
				),
			},
		}
	}
	data.CommentPosted = envelope.CommentPostedSeparate
	return transitionOutcome{
		result: okTransitionResult(key, data),
		warning: envelope.Warning{
			Type: envelope.WarningCommentNotAtomic,
			Message: fmt.Sprintf(
				"%s: the %q transition has no screen, so Jira would have discarded a comment sent with it — the comment was posted as a separate request instead, and the two were not atomic",
				key, transitionName(data.Transition),
			),
		},
	}
}

// transitionFields builds the `fields` half of the request from the
// plan, or nil when no field was asked for.
func transitionFields(plan transitionPlan) *jira.TransitionFields {
	fields := &jira.TransitionFields{}
	if plan.resolution != "" {
		fields.Resolution = &jira.ResolutionRef{Name: plan.resolution}
	}
	if plan.assignee != "" {
		fields.Assignee = &jira.AccountIDRef{AccountID: plan.assignee}
	}
	return fields
}

// resolveTransition turns what the caller typed into the transition to
// perform, reading the issue's own transition list when it must.
//
// A nil view with a nil error is the ONE shortcut: a bare transition id,
// with nothing asked for that depends on a screen. Callers must treat a
// nil view as "screened", which is not an assumption but a consequence —
// nothing on that path sends fields or a comment, so whether a screen
// exists cannot change the request.
func resolveTransition(ctx context.Context, client core.JiraClient, key string, plan transitionPlan) (*envelope.TransitionView, error) {
	if !plan.needsScreenState() {
		return &envelope.TransitionView{ID: plan.want, HasScreen: true}, nil
	}
	read, err := client.GetTransitions(ctx, key)
	if err != nil {
		return nil, err
	}
	if err := core.ValidateTransitions(read.Transitions); err != nil {
		return nil, err
	}
	available := make([]jira.IssueTransition, 0, len(read.Transitions.Transitions.Value))
	var matches []jira.IssueTransition
	for _, t := range read.Transitions.Transitions.Value {
		if t.IsAvailable.Set && !t.IsAvailable.Value {
			continue
		}
		available = append(available, t)
		if matchesTransition(t, plan.want) {
			matches = append(matches, t)
		}
	}
	if len(matches) == 0 {
		return nil, unknownTransition(key, plan.want, available)
	}
	if len(matches) > 1 {
		e := cli.NewInputError(cli.CodeArgValueInvalid, fmt.Sprintf(
			"%s has several available transitions matching %q; choose a transition ID from `jira issue transition list %s`",
			key, plan.want, key,
		))
		e.Field = flagTransition
		for _, match := range matches {
			e.Suggestions = append(e.Suggestions, "jira issue transition "+key+" --"+flagTransition+"="+match.ID.Value)
		}
		return nil, e
	}
	selected := matches[0]
	if err := validateTransitionFields(key, selected, plan); err != nil {
		return nil, err
	}
	return &envelope.TransitionView{
		ID:        selected.ID.Value,
		Name:      selected.Name.Value,
		Status:    selected.To.Value.Name.Value,
		HasScreen: selected.HasScreen.Value,
	}, nil
}

// matchesTransition reports whether one transition is what the caller
// asked for.
//
// A decimal input matches only an ID, so a numeric workflow name cannot
// override an explicitly selected ID. Other inputs match the transition name
// or destination status; resolveTransition refuses multiple matching edges.
// The transition's own name AND its target status both count,
// because a workflow names them independently — the transition "Start
// Progress" leads to the status "In Progress", and a caller who typed
// either meant the same move. Case-insensitive, because Jira's own UI
// title-cases what an admin typed and nobody should have to guess which
// they stored.
func matchesTransition(t jira.IssueTransition, want string) bool {
	if isTransitionID(want) {
		return t.ID.Value == want
	}
	return t.ID.Value == want ||
		strings.EqualFold(t.Name.Value, want) ||
		strings.EqualFold(t.To.Value.Name.Value, want)
}

// unknownTransition refuses a status that is not on this issue's list,
// naming the ones that are.
//
// The available transitions ride Suggestions rather than the message,
// which is the taxonomy's own split (the offender rides Field, the
// candidates ride Suggestions) and matters here more than usual: what is
// available depends on the issue's CURRENT status and the caller's
// permissions, so the list is the answer to "then what can I do?" and an
// agent should be able to read it without parsing prose.
func unknownTransition(key, want string, available []jira.IssueTransition) error {
	names := xslices.Map(available, func(t jira.IssueTransition) string { return t.Name.Value })
	e := cli.NewInputError(cli.CodeArgValueInvalid, fmt.Sprintf(
		"%s has no transition to %q from its current status", key, want,
	))
	e.Field = flagTransition
	e.Suggestions = names
	return e
}

// fieldsNeedScreen refuses a key whose fields cannot be delivered.
//
// Fields differ from a comment in the one way that matters: there is no
// fallback. A comment has a second endpoint that can carry it, so a
// screenless transition splits into two requests; a resolution has none
// this command holds, so the choice is between refusing and moving the
// issue with the fields silently dropped — which is exactly the failure
// this whole command exists to prevent, just relocated to a different
// flag.
//
// Native input uses a screen-specific remedy so recovery preserves authored
// changes. The flag-only path retains its historical not-applicable contract.
func fieldsNeedScreen(view *envelope.TransitionView, plan transitionPlan) error {
	if len(plan.payload.Fields) > 0 || len(plan.payload.Update) > 0 || transitionValuePresent(plan.payload.Extra["properties"]) {
		e := cli.NewInputError(cli.CodeTransitionScreenRequired, fmt.Sprintf(
			"the %q transition has no screen; native fields, update operations and properties cannot be applied safely with it — nothing was changed; inspect the workflow and submit these changes separately",
			transitionName(view),
		))
		e.Field = command.FlagJSONInput
		return e
	}
	e := cli.NewInputError(cli.CodeFlagNotApplicable, fmt.Sprintf(
		"the %q transition has no screen, and Jira silently discards fields sent with a screenless transition — nothing was changed; run the transition on its own, or drop --%s/--%s",
		transitionName(view), flagResolution, flagAssignee,
	))
	e.Field = flagResolution
	return e
}

// atomicUnavailable refuses a key whose comment cannot ride the
// transition, on behalf of --atomic-only.
//
// This is the branch that keeps 0.15's behavior reachable. 0.15 refused
// every screenless transition carrying a comment, which is honest and
// leaves the caller's actual problem unsolved on the common workflow;
// the default here solves it and says so, and this flag is for the
// caller who genuinely needed the guarantee rather than the outcome.
func atomicUnavailable(view *envelope.TransitionView) error {
	e := cli.NewInputError(cli.CodeFlagNotApplicable, fmt.Sprintf(
		"the %q transition has no screen, so a comment cannot ride it and --%s refuses to split the request — nothing was changed; drop --%s to transition and comment separately",
		transitionName(view), flagAtomicOnly, flagAtomicOnly,
	))
	e.Field = flagAtomicOnly
	return e
}

// transitionName is the transition's name for one sentence of prose,
// falling back to its id. A transition resolved from a bare id has no
// name to print, and "the "" transition" would read as a bug.
func transitionName(view *envelope.TransitionView) string {
	switch {
	case view == nil:
		return "requested"
	case view.Name != "":
		return view.Name
	}
	return view.ID
}

// transitionStatusName is the status an issue landed in, for prose,
// falling back to the transition's own name when the list did not carry
// a target status.
func transitionStatusName(view *envelope.TransitionView) string {
	if view != nil && view.Status != "" {
		return view.Status
	}
	return transitionName(view)
}

// okTransitionResult records one key's success.
func okTransitionResult(key string, data envelope.IssueTransitionData) envelope.KeyedResult[envelope.IssueTransitionData] {
	return envelope.KeyedResult[envelope.IssueTransitionData]{
		Issue: envelope.IssueRef{Key: key},
		OK:    true,
		Data:  &data,
	}
}

// failedTransitionResult records one key's failure, resolved through the
// SAME taxonomy the envelope's own errors[] entries use, so a caller
// branches on one vocabulary of codes wherever a failure appears.
func failedTransitionResult(key string, err error) transitionOutcome {
	class := cli.MapError(err)
	return transitionOutcome{
		result: envelope.KeyedResult[envelope.IssueTransitionData]{
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

// transitionSpinnerLabel is the live fan-out's one spinner label.
// Present tense, and correctly so: runTransition returns before this
// fan-out on a local preview, and the remote preview draws its own.
func transitionSpinnerLabel(keys []string, want string) string {
	if len(keys) == 1 {
		return fmt.Sprintf("Moving %s to %s", keys[0], want)
	}
	return fmt.Sprintf("Moving %d issues to %s", len(keys), want)
}

// previewSpinnerLabel is the remote preview's spinner label.
//
// It says CHECKING, never moving. This is the one animation in this
// command that runs while nothing is being written, so its wording is
// the H2 preview-tense rule in its hardest position — a spinner is drawn
// before the outcome is known, and a label promising a move would be on
// screen throughout a run that makes none.
func previewSpinnerLabel(keys []string) string {
	if len(keys) == 1 {
		return "Checking " + keys[0] + "'s workflow"
	}
	return fmt.Sprintf("Checking the workflow on %d issues", len(keys))
}

// renderTransition writes the human view.
//
// Preview tense is structural, not a phrasing habit (axis H2): the
// preview and the live view are separate functions with no shared
// sentence, so a future edit to the live wording cannot leak a
// past-tense claim into the preview — renderCommentAdd's own split, for
// its reason.
func renderTransition(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueTransitionOutput)
	if !ok {
		return fmt.Errorf("issue.transition: human renderer received %T, want envelope.IssueTransitionOutput", env.Data)
	}
	if out.DryRun {
		renderTransitionPreview(v, out)
		return nil
	}
	renderTransitionResults(v, out)
	return nil
}

// renderTransitionPreview draws the dry-run view. Every verb is
// conditional, and the route line appears only for a key that actually
// resolved one — a bare `--dry-run` cannot know the route, and inventing
// a line saying so for every key would bury the one thing the preview
// does report.
func renderTransitionPreview(v *output.View, out envelope.IssueTransitionOutput) {
	if renderUnresolvedSelector(v, out.Selector, "move") {
		return
	}
	// Each key is styled on its own — cli.Entity, one Render call per key
	// — and the plain connectives assembled around them, as SIBLING spans
	// on one line rather than nested (axis H5).
	keys := xslices.Map(out.Results, func(r envelope.KeyedResult[envelope.IssueTransitionData]) string {
		return cli.Entity(r.Issue.Key)
	})
	v.Line(fmt.Sprintf("Would move %s to %s.", joinKeys(keys), cli.Text(out.Transition)))
	for _, r := range out.Results {
		if !r.OK {
			v.Line(cli.Entity(r.Issue.Key) + cli.Text(": "+r.Err.Message))
			v.Hint(r.Err.Hint)
			continue
		}
		if line := previewRouteLine(r); line != "" {
			v.Line(line)
		}
	}
	if len(out.Comment) > 0 && !anyPlanKnown(out) {
		v.Hint("Screen state lives on the site, so this preview cannot say whether the comment would ride the transition — add --validate-remote to find out.")
	}
	v.Hint("Nothing was sent. Drop --dry-run to apply it.")
}

// previewRouteLine is one key's foreseen comment route, or "" for a key
// with nothing foreseen.
func previewRouteLine(r envelope.KeyedResult[envelope.IssueTransitionData]) string {
	key := cli.Entity(r.Issue.Key)
	switch r.Data.CommentPlan {
	case envelope.CommentPlanAtomic:
		return key + cli.Text(": the comment would ride the transition.")
	case envelope.CommentPlanSeparate:
		return key + cli.Text(": no transition screen, so the comment would be posted as a separate request.")
	case envelope.CommentPlanRefused:
		return key + cli.Text(": no transition screen, so --"+flagAtomicOnly+" would refuse this key.")
	}
	return ""
}

// anyPlanKnown reports whether the preview resolved a route for any key,
// which is what distinguishes a remote preview from a local one without
// re-reading the flag the envelope does not carry.
func anyPlanKnown(out envelope.IssueTransitionOutput) bool {
	for _, r := range out.Results {
		if r.OK && r.Data.CommentPlan != "" {
			return true
		}
	}
	return false
}

// renderTransitionResults draws the live view: one line per key, in argv
// order, saying what happened to it.
//
// A failed key's message is Jira-authored in the common case, so it goes
// through cli.Text like every other Jira string while the hint rides the
// message path — renderCommentAddResults' own split. The comment's fate
// gets its own sentence rather than being folded into the first: the
// status change and the comment are two outcomes, and a key where one
// worked and the other did not must not read as a single verdict.
func renderTransitionResults(v *output.View, out envelope.IssueTransitionOutput) {
	for _, r := range out.Results {
		key := cli.Entity(r.Issue.Key)
		if !r.OK {
			if r.Data != nil && r.Data.Applied {
				v.Line(key + cli.Text(": moved to "+transitionStatusName(r.Data.Transition)+"; the run reported an error afterwards."))
			}
			v.Line(key + cli.Text(": "+r.Err.Message))
			v.Hint(r.Err.Hint)
			continue
		}
		v.Line(key + cli.Text(": moved to "+transitionStatusName(r.Data.Transition)+"."))
		if line := commentOutcomeLine(r); line != "" {
			v.Line(line)
			if r.Data.CommentErrorHint != "" {
				v.Hint(r.Data.CommentErrorHint)
			}
		}
	}
}

// commentOutcomeLine is one key's comment sentence, or "" when no
// comment was asked for.
func commentOutcomeLine(r envelope.KeyedResult[envelope.IssueTransitionData]) string {
	indent := cli.Text("  ")
	switch r.Data.CommentPosted {
	case envelope.CommentPostedAtomic:
		return indent + cli.Text("Comment added with the transition.")
	case envelope.CommentPostedSeparate:
		return indent + cli.Text("Comment added separately — the transition had no screen to carry it.")
	case envelope.CommentPostedFailed:
		return indent + cli.Text("Comment not confirmed: "+r.Data.CommentError)
	}
	return ""
}
