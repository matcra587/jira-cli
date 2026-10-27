package issue

import (
	"context"
	"errors"
	"fmt"

	xsync "github.com/gechr/x/sync"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// newEdit returns `jira issue edit`.
//
// `create`'s sibling, sharing its whole input surface (issueWriteFlags) and
// `comment add`'s whole keyed surface. What is genuinely its own is the
// add-versus-replace question that only exists once an issue HAS values —
// settled in command.IssueFieldFlags.Apply, and stated in the Long text
// below because it is the one thing here that can surprise.
func newEdit(deps Deps) *cobra.Command {
	var keyed command.KeyedFlags
	var sel command.SelectorFlags
	var in issueWriteFlags
	// --verify for create's reason: the edit endpoint answers 204, so
	// confirming costs a real second request. No --force: an edit is not
	// destructive in the sense --force exists for, and the one genuinely
	// lossy thing it can do (clearing an assignee) is spelled out as its
	// own flag rather than hidden inside another.
	var mut command.MutationFlags

	return command.MustNew(command.Spec[envelope.IssueEditOutput]{
		Op:         "issue.edit",
		WritesJira: true,
		Identity:   true,
		// data.payload is a wire passthrough: the keys inside it are
		// Jira's own (issuetype, fixVersions, a tenant's
		// customfield_NNNNN), not names this CLI minted, so the
		// snake_case rule stops at that boundary and the fidelity axis
		// proves an unmodeled field survives composition.
		Passthrough: true,
		Warnings:    editVerificationWarnings,
		Keyed:       true,
		Use:         "edit ISSUE_KEY...",
		Short:       "Change fields on an issue",
		Long: "Change fields on one or more issues.\n\n" +
			"The three input routes are `create`'s, and mean the same things: " +
			"`--json-input` for a whole native payload, " +
			"`--markdown`/`--markdown-file` for the description, and the field " +
			"flags for the standard system fields. A field flag beside " +
			"`--json-input` is fine until the two set the SAME field, which is " +
			"refused — and the check reads both `fields` and `update`, so a " +
			"field set through either half collides.\n\n" +
			"Native input also retains top-level `properties`, `historyMetadata`, " +
			"`transition` and future request members. A property-only payload is " +
			"a change even without `fields` or `update`; Jira validates its " +
			"meaning. Those two containers must be objects when present, and " +
			"explicit empty objects survive composition.\n\n" +
			"**The repeatable flags ADD; they do not replace.** `--label " +
			"urgent` on an issue that already has two labels leaves it with " +
			"three, and the same goes for `--component`, `--fix-version` and " +
			"`--affects-version`. That is the safe direction: an unwanted extra " +
			"label is trivially removed, while a silent replacement would " +
			"delete values the command never showed you. Every one of them has " +
			"a matching removal flag — `--remove-label`, `--remove-component`, " +
			"`--remove-fix-version`, `--remove-affects-version` — and naming " +
			"one value in both halves of a pair is refused rather than " +
			"resolved, because the result would depend on which verb Jira " +
			"applied last. Replacing a list wholesale is the one thing no flag " +
			"does: that goes through `--json-input`, which reaches every " +
			"field and every verb.\n\n" +
			"`--reporter` takes an Atlassian account ID; `--assignee` also " +
			"accepts bare email. Both accept `@me` for " +
			"whoever your credentials authenticate as. Resolving " +
			"`@me` reads the current user from Jira on live runs. Local previews " +
			"use a matching cached account_id with an unverified warning, or " +
			"leave the sentinel unresolved. `--validate-remote` reads Jira instead.\n\n" +
			"`--unassign` clears the assignee. It is a separate flag rather " +
			"than a magic `--assignee` value, because a sentinel string cannot " +
			"be discovered from help, schema or completion.\n\n" +
			"This is a keyed command: results come back as one entry per key, " +
			"in the order you gave them, at `data.results[]`. A key that fails " +
			"does not fail the others, and the edits that DID apply are real — " +
			"the envelope names exactly which keys got one so you can retry " +
			"only the rest. Only a run where EVERY key failed is reported as a " +
			"failure of the command itself.\n\n" +
			"`--json-input` takes a file path, or `-` to read the payload from " +
			"standard input, so a composed document can be piped straight in. " +
			"It is also the route to a tenant's custom fields, which ride the " +
			"payload under their own ids and reach Jira untouched — there is " +
			"deliberately no `--field key=value`.\n\n" +
			"`--generate-json` reads complete live edit metadata for each " +
			"requested issue, using explicit keys or the selectors below. It " +
			"preserves every native field definition and actual custom field " +
			"ID, with a separate native `fields`/`update` skeleton for each " +
			"context. Null values are placeholders: fill wanted values and " +
			"remove unwanted fields or update operations before submitting " +
			"through `--json-input`. No values or defaults are guessed, and " +
			"contexts are never merged. This is a read: it works in read-only " +
			"mode, prompts for nothing, honors bounded selector resolution and " +
			"`--parallelism`, and refuses authored-input or mutation flags, " +
			"including `--dry-run`, `--force`, `--notify` and `--verify`. " +
			"Results report `issue.edit.input`; partial and total failures " +
			"retain ordered outcomes. Metadata is a snapshot, not permission " +
			"or a guarantee for a later edit.\n\n" +
			"`--dry-run` composes the request and stops, so `data.payload` is " +
			"byte-identical to what a live run sends — but it cannot tell you " +
			"whether the issues exist or whether the values are valid on them. " +
			"What it prints is valid input for `--json-input`, so a preview " +
			"round-trips into a real run.\n\n" +
			fieldPreflightHelp + "\n\n" +
			"`--assignee` accepts an account ID or bare email. Email resolves the unique visible active search match before live writes or explicit remote previews; local previews retain it with an unresolved warning. Missing, ambiguous or incomplete searches refuse. Privacy can hide email, and resolution does not prove exact email equality or assignability. Native JSON account IDs stay literal.\n\n" +
			"`--verify` reads every requested field once after each acknowledged " +
			"edit. `verified.fields` reports matches, differs or unverified with " +
			"native observed values; absent and null remain distinct, including " +
			"summary, status and updated metadata. Known collection " +
			"add/remove checks use final membership. Unsupported operations, missing " +
			"fields and different ADF remain unverified. Readback failure or differences " +
			"retain the successful edit and emit warnings; inspect before further " +
			"changes. This later observation does not protect against another writer.\n\n" +
			"`--project` is absent. On an existing issue, changing the project " +
			"is a MOVE rather than a field write, and it needs Jira's bulk-move " +
			"endpoint rather than this one.\n\n" +
			command.SelectorHelp + "\n\n" +
			"**Notifications.** Every edit emails the issue's watchers by " +
			"default, which is Jira's own behavior. `--notify=false` asks it " +
			"not to — worth reaching for with a selector, since editing a " +
			"hundred issues otherwise sends a hundred emails to everyone " +
			"watching each of them.\n\n" +
			"Jira decides whether to honor that, and it will not tell you " +
			"which it did. Suppressing the notification needs the administer " +
			"Jira or administer project permission, and Jira IGNORES the " +
			"request from anyone without it — the edit still succeeds, the " +
			"emails still go out, and the response looks identical either " +
			"way. So `notify` in the output is what was ASKED FOR, never a " +
			"record of what happened. If it matters, check your permission " +
			"first.\n\n" +
			"`--web` is refused here. It opens one page and this command writes " +
			"to a list of keys.",
		Example: "# retitle one issue\n" +
			"$ jira issue edit KEY-123 --summary 'Rotate the signing key before Q4'\n\n" +
			"# inspect each issue's own input metadata and native skeleton\n" +
			"$ jira issue edit KEY-123 KEY-124 --generate-json --output=json\n\n" +
			"# extract one skeleton; fill wanted values and remove unused fields\n" +
			"$ jira issue edit KEY-123 --generate-json --output=compact --jq '.results[0].data.payload' > issue.json\n\n" +
			"# add a label to several issues at once\n" +
			"$ jira issue edit KEY-123 KEY-124 --label needs-triage\n\n" +
			"# add one label and remove another\n" +
			"$ jira issue edit KEY-123 --label ready --remove-label needs-triage\n\n" +
			"# drop a fix version that slipped\n" +
			"$ jira issue edit KEY-123 --remove-fix-version 2026.8\n\n" +
			"# pipe a payload in, for a field the flags do not reach\n" +
			"$ echo '{\"fields\":{\"customfield_10016\":5}}' | jira issue edit KEY-123 --json-input -\n\n" +
			"# hand it over, and set a due date\n" +
			"$ jira issue edit KEY-123 --assignee 5b10a2844c20165700ede21g --due 2026-09-01\n\n" +
			"# take it yourself\n" +
			"$ jira issue edit KEY-123 --assignee @me\n\n" +
			"# drop the assignee\n" +
			"$ jira issue edit KEY-123 --unassign\n\n" +
			"# rewrite the description from a file\n" +
			"$ jira issue edit KEY-123 --markdown-file notes.md\n\n" +
			"# preview the request without sending it\n" +
			"$ jira issue edit KEY-123 --priority High --dry-run\n\n" +
			"# which keys failed, as json\n" +
			"$ jira issue edit KEY-123 KEY-124 --label triage --output=json --jq '.data.results[] | select(.ok == false)'\n\n" +
			"# every issue a query matches; the confirmation lists them first\n" +
			"$ jira issue edit --jql 'project = KEY AND labels = stale' --label triage\n\n" +
			"# which issues that query would touch, resolved but writing nothing\n" +
			"$ jira issue edit --jql 'project = KEY AND labels = stale' --label triage --dry-run --validate-remote\n\n" +
			"# a bulk edit that does not email every watcher of every issue\n" +
			"$ jira issue edit --jql 'project = KEY AND labels = stale' --label triage --notify=false",
		// Min is 0 because a selector replaces the positionals entirely;
		// SelectorFlags.Validate gives the "name something" refusal, which
		// can name both routes where cobra's arity check could not.
		Args: command.ArgSpec{Min: 0, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			command.AddKeyedFlags(c, &keyed)
			in.register(c, command.EditIssueVerb)
			registerInputDiscovery(c)
			// --validate-remote opts previews into selector/account resolution
			// and complete per-issue field metadata. --force arrives with the
			// selectors, which are what give this command a
			// confirmation to skip. An edit that names its keys still prompts
			// for nothing.
			command.AddMutationFlags(c, &mut, command.WithVerify(), command.WithValidateRemote(), command.WithForce(), command.WithNotify())
			command.AddSelectorFlags(c, &sel)
		},
		Read: command.WhenBoolRead(flagGenerateJSON, command.ReadSpec[envelope.IssueEditInputOutput]{
			Op: "issue.edit.input", Identity: true, Keyed: true, Passthrough: true, Human: renderEditInput,
			Validate: func() error {
				if err := validateInputDiscoveryFlags(in.cmd, command.FlagParallelism, command.FlagJQL, command.FlagFilter, command.FlagFromFile); err != nil {
					return err
				}
				if err := keyed.Validate(); err != nil {
					return err
				}
				return sel.Validate(len(in.cmd.Flags().Args()))
			},
			RetainOnError: func(out envelope.IssueEditInputOutput) bool { return len(out.Results) > 0 },
			Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueEditInputOutput, error) {
				return runEditInput(ctx, deps, inv, sel, keyed.Parallelism)
			},
		}),
		Validate: func() error {
			if err := keyed.Validate(); err != nil {
				return err
			}
			if err := in.validate(); err != nil {
				return err
			}
			// The notify alias folds before anything reads mut.Notify, the
			// same ordering every other alias-bearing Validate uses.
			if err := mut.ResolveNotify(in.cmd.Flags()); err != nil {
				return err
			}
			// Every positional here is a key, so the count goes through
			// as-is.
			if err := sel.Validate(len(in.cmd.Flags().Args())); err != nil {
				return err
			}
			return refuseWebOnKeyedWrite(in.cmd)
		},
		Human:         renderEdit,
		RetainOnError: func(out envelope.IssueEditOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueEditOutput, error) {
			return runEdit(ctx, deps, inv, &in, editOptions{
				verify:         mut.Verify,
				notify:         mut.Notify,
				validateRemote: mut.ValidateRemote,
				selector:       sel,
				force:          mut.Force,
				parallelism:    keyed.Parallelism,
			})
		},
	})
}

// editOptions is what runEdit needs from the flag set beyond the shared
// input bundle, gathered so the handler's signature does not grow a
// parameter per flag.
//
// --dry-run is deliberately absent for the reason commentAddOptions
// documents: it arrives on the Invocation, read from the flag the
// construction path registered itself, and copying it here would give one
// fact two carriers that could disagree.
type editOptions struct {
	verify bool
	// notify is --notify, already folded with its hidden alias. It rides
	// the request as a query parameter rather than in the body, which is
	// why it is not part of jira.IssueWriteRequest and does not appear in
	// the published payload.
	notify         bool
	validateRemote bool
	// selector and force are the bulk-selector surface. force is only
	// consulted for selector-driven edits and confirmed field moves.
	// Ordinary edits with explicit keys have no confirmation to skip.
	selector    command.SelectorFlags
	force       bool
	parallelism int
	// fieldMove preserves the historical move confirmation while sharing
	// composition, transport, keyed receipts and rendering with ordinary edits.
	fieldMove bool
}

// runEdit composes the request once and either previews it or applies it
// to every key.
//
// **Compose once, send N times**, exactly as runCommentAdd does: the
// payload is resolved before any key is touched, so all N issues get a
// byte-identical request and a per-key failure can never be a per-key
// difference in what was sent. It is also what makes the preview
// trustworthy — the SAME composed request is what a live run sends.
//
// **A BARE preview builds no client.** With no `@me` to resolve and no
// --validate-remote, a dry run returns before deps.JiraClient() is called,
// so it cannot reach the network because it never obtains the thing that
// could. Explicit remote checks cover selector/account resolution and field
// metadata. Their shared owners reuse the same lazily constructed client.
//
// **Account resolution happens BEFORE composition**, because compose bakes
// the value into the payload — and here that matters doubly: one payload
// goes to every key, so a sentinel resolved afterwards would have to be
// rewritten in N places or not at all.
func runEdit(ctx context.Context, deps Deps, inv command.Invocation, in *issueWriteFlags, opts editOptions) (envelope.IssueEditOutput, error) {
	var zero envelope.IssueEditOutput
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	// ONE client for the whole run, however many of the three resolvers
	// below want one — the selector's search, `@me`'s /myself lookup, and
	// the fan-out itself. Each is written to build only if it needs one
	// (a bare preview builds none), and oneClient is what keeps that
	// property while making two clients impossible.
	newClient := oneClient(deps.JiraClient)

	// The selector resolves FIRST, before the payload is composed and long
	// before the confirmation. Its keys are what the confirmation names —
	// the ordering rule design §3.5 turns on.
	_, targets, err := resolveTargets(ctx, newClient, inv, selectorInputs{
		selector:       opts.selector,
		validateRemote: opts.validateRemote,
	})
	if err != nil {
		return zero, err
	}

	if _, err := command.ResolveAccounts(ctx, newClient, deps.Preferences, inv.DryRun, opts.validateRemote, in.fields.AccountFields()); err != nil {
		return zero, err
	}

	composed, err := in.compose(ctx, inv.Stdin, inv.StdinIsTerminal)
	if err != nil {
		return zero, err
	}

	out := envelope.IssueEditOutput{
		MutationState:    envelope.MutationState{DryRun: inv.DryRun},
		Notify:           opts.notify,
		Payload:          composed.payload,
		DescriptionRoute: composed.route,
	}
	out.Selector = targets.Summary
	if opts.fieldMove {
		if _, bulk := composed.req.Extra["targetToSourcesMapping"]; bulk || (len(composed.req.Fields) == 0 && len(composed.req.Update) == 0) {
			return zero, moveInputError(errors.New("field moves need a fields/update issue payload; native targetToSourcesMapping belongs to jira issue move"))
		}
	}

	// An edit that changes nothing is a caller mistake, not a no-op worth
	// a round trip per key. It is checked on the COMPOSED request rather
	// than on the flags, so `--json-input` satisfies it — and on BOTH
	// paths, because a preview of a request Jira would reject outright is
	// the one thing a preview must not report as fine.
	if composed.req.Empty() {
		e := cli.NewInputError(cli.CodeFlagRequired,
			"editing an issue needs at least one change — set a field with a flag like --summary or --label, give a description with --markdown, or send a payload with --json-input")
		e.Field = command.FlagSummary
		return zero, e
	}
	if inv.DryRun && !opts.validateRemote {
		// Empty exactly when the selector went unresolved; the warning
		// ResolveTargets raised says why.
		out.Results = previewEditResults(targets.Keys)
		return out, nil
	}
	if inv.DryRun {
		client, err := newClient(ctx)
		if err != nil {
			return zero, err
		}
		out.Results, err = editFanOut(ctx, client, targets.Keys, composed.req, opts, true)
		return out, err
	}
	if opts.fieldMove {
		if err := confirmFieldMove(ctx, deps, inv, targets.Keys, opts.force); err != nil {
			return zero, err
		}
	}

	// Resolution first, then the question — so the prompt names the issues
	// rather than echoing the query back.
	if err := confirmSelected(ctx, deps.Confirm, inv, opts.force,
		selectedWrite{Gerund: "editing", Imperative: "Edit"}, targets); err != nil {
		if errors.Is(err, errDestructiveAborted) {
			return zero, cli.NewCodedError(cli.CodeCanceled,
				fmt.Sprintf("edit canceled — %s unchanged", joinPlainKeys(targets.Keys)))
		}
		return zero, err
	}

	// The sentinel must never reach the wire — resolveAccounts settled it
	// on this path, so this guards a future reordering rather than a case
	// that can arise today.
	if err := command.RefuseMeWithoutResolution(in.fields.AccountFields()); err != nil {
		return zero, err
	}
	client, err := newClient(ctx)
	if err != nil {
		return zero, err
	}
	results, err := editFanOut(ctx, client, targets.Keys, composed.req, opts, false)
	out.Results = results
	return out, err
}

// previewEditResults builds the per-key entries for a local run: one per
// key, in argv order, each successful and each carrying an empty operation
// object.
//
// The entry shape stays identical to a live run's, which is the point — a
// caller can diff a preview against a real result without the two
// disagreeing about their structure. A pointer to an empty struct, not
// nil: the contract says a successful key carries `data`, and a preview's
// key succeeded (in the narrow sense its envelope documents — the payload
// for that key composed, not that Jira would accept it).
func previewEditResults(keys []string) []envelope.KeyedResult[envelope.IssueEditData] {
	results := make([]envelope.KeyedResult[envelope.IssueEditData], len(keys))
	for i, key := range keys {
		results[i] = envelope.KeyedResult[envelope.IssueEditData]{
			Issue: envelope.IssueRef{Key: key},
			OK:    true,
			Data:  &envelope.IssueEditData{},
		}
	}
	return results
}

// editFanOut applies the edit to every key and assembles the keyed payload.
//
// The partial-failure policy is `comment add`'s, transferred unchanged and
// for its stronger write-side reason: the edits that already applied are
// real and cannot be taken back, so the only safe way to retry is to know
// exactly which keys succeeded — which is what data.results[] is. A run
// where some keys applied is `ok: true`, exit 0, with the failures inline.
// Read-only mode needs no check here either: the transport refuses each
// PUT before it reaches the wire, so every key fails with
// read_only_blocked and KeyedFailure returns the original error.
func editFanOut(
	ctx context.Context,
	client core.JiraClient,
	keys []string,
	req jira.IssueWriteRequest,
	opts editOptions,
	dryRun bool,
) ([]envelope.KeyedResult[envelope.IssueEditData], error) {
	outcomes := make([]editOutcome, len(keys))
	// ONE spinner around the whole fan-out, never one per key: a spinner
	// owns the terminal's live region and several drawing at once
	// interleave into corruption.
	label := editSpinnerLabel(keys)
	if dryRun {
		label = "Checking issue field metadata"
	}
	_, err := command.Spin(ctx, label, func(ctx context.Context) (struct{}, error) {
		// Every key runs regardless of another's failure, which is what
		// makes a bad key a partial result. Parallel writes outcomes[i]
		// from the goroutine handling key i and needs no lock: x/sync
		// hands each call a distinct index and the slice was sized up
		// front. Order therefore comes from the ARGV, never from which PUT
		// finished first.
		xsync.Parallel(opts.parallelism, len(keys), func(i int) {
			var preflight *envelope.IssueFieldPreflight
			if opts.validateRemote {
				var err error
				preflight, err = editFieldPreflight(ctx, client, keys[i], req)
				if err != nil {
					outcomes[i] = failedEditResult(keys[i], err)
					return
				}
			}
			if dryRun {
				outcomes[i].result = envelope.KeyedResult[envelope.IssueEditData]{
					Issue: envelope.IssueRef{Key: keys[i]}, OK: true,
					Data: &envelope.IssueEditData{FieldPreflight: preflight},
				}
				return
			}
			outcomes[i] = editOne(ctx, client, keys[i], req, opts.verify, opts.notify)
			if outcomes[i].result.Data != nil {
				outcomes[i].result.Data.FieldPreflight = preflight
			}
		})
		return struct{}{}, nil
	})
	results := make([]envelope.KeyedResult[envelope.IssueEditData], len(outcomes))
	raw := make([]error, len(outcomes))
	for i, o := range outcomes {
		results[i], raw[i] = o.result, o.err
	}
	if err != nil {
		// Spin joins all producers even when cancellation stops its animation.
		// Keep their completed outcomes, and report cancellation for the run
		// rather than silently turning a completed edit into an empty frame.
		return results, err
	}
	return results, command.KeyedFailure(results, raw)
}

// editOutcome is one key's result plus the raw error behind a failure —
// the same pairing commentAddOutcome makes, and for its reason: the
// envelope's KeyError is the right shape to PUBLISH per key and the wrong
// thing to fail the command with, so the original travels alongside for
// command.KeyedFailure to hand back untouched.
type editOutcome struct {
	result envelope.KeyedResult[envelope.IssueEditData]
	// err is nil exactly when result.OK.
	err error
}

// editSpinnerLabel names the live write fan-out. Remote previews use a separate
// metadata-check label, keeping the two operation tenses distinct.
func editSpinnerLabel(keys []string) string {
	if len(keys) == 1 {
		return "Editing " + keys[0]
	}
	return fmt.Sprintf("Editing %d issues", len(keys))
}

// editOne applies the edit to one key, and reads it back when asked.
//
// It returns a result rather than an error precisely because a failure
// here is DATA — the entry the caller reads at data.results[] — and not
// the command's own failure.
//
// A failed READBACK is not a failed key. The edit landed; only the
// confirmation did not, so the key stays ok and publishes VerifyFailed.
// Treating it as a failure would tell a caller to retry a write that
// already succeeded.
func editOne(ctx context.Context, client core.JiraClient, key string, req jira.IssueWriteRequest, verify, notify bool) editOutcome {
	if _, err := client.EditIssue(ctx, key, req, notify); err != nil {
		return failedEditResult(key, err)
	}
	data := &envelope.IssueEditData{}
	if verify {
		verified, err := verifyIssue(ctx, client, key, req)
		data.Verified, data.VerifyFailed = verified, err != nil
	}
	return editOutcome{result: envelope.KeyedResult[envelope.IssueEditData]{
		Issue: envelope.IssueRef{Key: key},
		OK:    true,
		Data:  data,
	}}
}

// failedEditResult records one key's failure, resolved through the SAME
// taxonomy the envelope's own errors[] entries use (cli.MapError), so a
// caller branches on one vocabulary of codes wherever a failure appears.
func failedEditResult(key string, err error) editOutcome {
	class := cli.MapError(err)
	return editOutcome{
		result: envelope.KeyedResult[envelope.IssueEditData]{
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

// renderEdit writes the human view.
//
// Preview tense is structural rather than a phrasing habit (axis H2): the
// local branches and the live branch are separate functions with no shared
// sentence between them.
func renderEdit(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueEditOutput)
	if !ok {
		return fmt.Errorf("issue.edit: human renderer received %T, want envelope.IssueEditOutput", env.Data)
	}
	if out.DryRun {
		renderEditPreview(v, out)
		return nil
	}
	renderEditResults(v, out)
	return nil
}

// renderEditPreview draws the dry-run view: which keys would change, and
// the payload that would be sent to each. Every verb is conditional.
func renderEditPreview(v *output.View, out envelope.IssueEditOutput) {
	if renderUnresolvedSelector(v, out.Selector, "edit") {
		return
	}
	// Each key is styled on its own — cli.Entity, one call per key — and
	// the plain connectives are assembled around them. They are SIBLING
	// spans on one line, never nested: each carries its own reset and the
	// next opens a fresh style, which is the shape H5 requires.
	keys := make([]string, 0, len(out.Results))
	refused := false
	for _, r := range out.Results {
		if !r.OK {
			refused = true
			v.Line(cli.Entity(r.Issue.Key) + cli.Text(": "+r.Err.Message))
			v.Hint(r.Err.Hint)
			continue
		}
		keys = append(keys, cli.Entity(r.Issue.Key))
		if r.Data != nil {
			renderFieldPreflight(v, r.Data.FieldPreflight)
		}
	}
	if len(keys) != 0 {
		v.Line(fmt.Sprintf("Would send this payload to %s.", joinKeys(keys)))
	}
	// Section for renderCreatePreview's reason, and with the same
	// constraint: the title is passed PLAIN, because clog's Divider styles
	// what it is handed and a pre-styled string would strand a reset
	// mid-span (axis H5).
	v.Section("Payload")
	v.Block(cli.Text(string(out.Payload)))
	// The local preview can be reused as native input. Live --generate-json
	// separately discovers each issue's editable fields and operations.
	if refused {
		v.Hint("Nothing was sent. Repair the refused contexts before further changes.")
	} else {
		v.Hint("Nothing was sent. Drop --dry-run to apply it, or save this payload and pass it back with --json-input.")
	}
}

// renderEditResults draws the live view: one line per key, in argv order,
// saying whether that key changed and why not when it did not.
func renderEditResults(v *output.View, out envelope.IssueEditOutput) {
	for _, r := range out.Results {
		// cli.Entity for the key and cli.Text for the rest, as two sibling
		// spans concatenated into ONE Line argument. cli.Text is what
		// sanitizes the Jira-authored message before it can reach a
		// terminal (axis H4).
		key := cli.Entity(r.Issue.Key)
		if !r.OK {
			v.Line(key + cli.Text(": "+r.Err.Message))
			v.Hint(r.Err.Hint)
			continue
		}
		renderFieldPreflight(v, r.Data.FieldPreflight)
		switch {
		case r.Data.Verified != nil:
			v.Line(key + ": updated.")
			renderVerificationFields(v, r.Data.Verified)
		case r.Data.VerifyFailed:
			v.Line(key + cli.Text(": updated, but reading it back failed."))
		default:
			v.Line(key + cli.Text(": updated."))
		}
	}
}
