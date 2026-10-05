package issue

import (
	"context"
	"fmt"
	"strconv"

	xsync "github.com/gechr/x/sync"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// This group's own flag names, spelled once so registration and every
// refusal below name the same strings.
const (
	flagURL          = "url"
	flagTitle        = "title"
	flagSummary      = "summary"
	flagRelationship = "relationship"
	flagGlobalID     = "global-id"
)

// newWeblinkAdd returns `jira issue weblink add`.
func newWeblinkAdd(deps Deps) *cobra.Command {
	var keyed command.KeyedFlags
	// Bare, no options. --force has nothing destructive to confirm,
	// --validate-remote has nothing to check against the site (a URL is
	// the caller's own and Jira never validates it either), --verify has
	// no extra round trip to gate — the POST's own response carries the
	// link's identifier — and --no-readback would suppress a readback
	// this command never makes.
	var mut command.MutationFlags
	var link weblinkFlags
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueWeblinkAddOutput]{
		Op:         "issue.weblink.add",
		WritesJira: true,
		// Identity for the keyed reason link create records: the slot is
		// data.results[].issue, which every keyed command fills and the
		// audit's M1 axis checks there.
		Identity:    true,
		Passthrough: true,
		Keyed:       true,
		Use:         "add ISSUE_KEY...",
		Short:       "Attach a web link to issues",
		Long: "Attach a URL to one or more issues.\n\n" +
			"`--json-input` reads a complete native remote-link request from a " +
			"file, or `-` for standard input. It supports `application`, object " +
			"`icon` and `status`, and future request members. Native `object.url` " +
			"and `object.title` are required. Explicit flags can supply omitted " +
			"members; setting the same member in both routes is refused. " +
			"The complete composed request is available at `data.payload`.\n\n" +
			"`--url` requires an absolute HTTP or HTTPS URL. `--title` is " +
			"optional for flag-only input; omitting it uses the URL as the title. A preview " +
			"checks URL syntax without fetching the target.\n\n" +
			"**`--global-id` makes this create-or-REPLACE.** Without it, every " +
			"run adds another link — run it twice and the issue carries the URL " +
			"twice. With it, a link already carrying that id on that issue is " +
			"replaced instead, which is what makes this command safe to re-run " +
			"from a pipeline. Each result says which happened at " +
			"`data.results[].data.created`.\n\n" +
			"Replacement clears omitted summary and relationship fields. " +
			"An unusable acknowledgement reports `weblink_response_invalid`; " +
			"inspect the issue's links before retrying because Jira may already " +
			"have stored the write. Writes are never automatically replayed.\n\n" +
			"This is a keyed command: one entry per key at `data.results[]`, in " +
			"the order you gave them. Every key gets the SAME link, which is the " +
			"shape a list of keys means here — \"this runbook covers all three " +
			"of these\". A key that fails does not fail the others, and the " +
			"envelope names which ones took the link. Partial success succeeds; " +
			"total failure returns an error. Total failure and interruption " +
			"retain the composed request and ordered outcomes.\n\n" +
			"`--dry-run` composes the request and stops. It never contacts " +
			"Jira, so it reports the link that WOULD be attached but cannot tell " +
			"you whether the issues exist — or, when you gave a `--global-id`, " +
			"whether this would add a link or replace one.\n\n" +
			"There is no `--force`. Attaching a link is additive and reversible " +
			"with `jira issue weblink delete`.\n\n" +
			"`--web` is refused here. It opens one page and this command writes " +
			"to a list of keys.",
		Example: "# attach a runbook to one issue\n" +
			"$ jira issue weblink add KEY-123 --url https://example.com/runbook --title 'Deploy runbook'\n\n" +
			"# the same link on several issues\n" +
			"$ jira issue weblink add KEY-123 KEY-124 --url https://example.com/incident/42 --title 'Incident 42'\n\n" +
			"# re-runnable: the same global id replaces rather than duplicates\n" +
			"$ jira issue weblink add KEY-123 --url https://ci.example.com/build/7 --title 'Build 7' --global-id ci-build-7\n\n" +
			"# with a relationship and a summary\n" +
			"$ jira issue weblink add KEY-123 --url https://example.com/pr/9 --title 'PR 9' --relationship 'is implemented by' --summary 'Adds the retry loop.'\n\n" +
			"# preview the request without sending it\n" +
			"$ jira issue weblink add KEY-123 --url https://example.com --title Example --dry-run\n\n" +
			"# attach a native link with application, icon and status metadata\n" +
			"$ jira issue weblink add KEY-123 --json-input remote-link.json\n\n" +
			"# inspect the complete native request\n" +
			"$ jira issue weblink add KEY-123 --json-input remote-link.json --dry-run --output=json --jq '.data.payload'",
		Args: command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			link.cmd = c
			command.AddKeyedFlags(c, &keyed)
			command.AddMutationFlags(c, &mut)
			command.AddStringVar(c, &link.jsonInput, command.FlagJSONInput, "",
				"Read a complete native remote-link request from a JSON file, or `-` for standard input",
				command.FlagOpts{Terse: "Native link payload", Placeholder: "path"})
			command.AddHiddenAliases(c, command.ADFRouteAliases())
			command.AddStringVar(c, &link.url, flagURL, "",
				"The URL to attach",
				command.FlagOpts{Terse: "Target URL", Placeholder: "url"})
			command.AddStringVar(c, &link.title, flagTitle, "",
				"The link's display text; flag-only input defaults to the URL when omitted or empty",
				command.FlagOpts{Terse: "Link title", Placeholder: "text"})
			command.AddStringVar(c, &link.summary, flagSummary, "",
				"Optional prose shown under the title",
				command.FlagOpts{Terse: "Link summary", Placeholder: "text"})
			command.AddStringVar(c, &link.relationship, flagRelationship, "",
				"How the issue relates to the link, as free text",
				command.FlagOpts{Terse: "Relationship", Placeholder: "text"})
			command.AddStringVar(c, &link.globalID, flagGlobalID, "",
				"Your own id for this link, making a re-run replace it instead of adding another",
				command.FlagOpts{Terse: "Dedupe id", Placeholder: "id"})
		},
		Validate: func() error {
			if err := resolvePayloadAliases(cmd.Flags()); err != nil {
				return err
			}
			if err := keyed.Validate(); err != nil {
				return err
			}
			if err := link.validate(); err != nil {
				return err
			}
			return refuseWebOnKeyedWrite(cmd)
		},
		Human:         renderWeblinkAdd,
		RetainOnError: func(out envelope.IssueWeblinkAddOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueWeblinkAddOutput, error) {
			return runWeblinkAdd(ctx, deps, inv, link, keyed.Parallelism)
		},
	})
}

// weblinkFlags is the link this command composes, gathered into one
// struct so the handler's signature does not grow a parameter per flag —
// commentAddOptions' own reasoning.
type weblinkFlags struct {
	cmd          *cobra.Command
	jsonInput    string
	url          string
	title        string
	summary      string
	relationship string
	globalID     string
}

// validate refuses an invocation that could not produce a usable link,
// before any client is built.
//
// The shared boundary requires an absolute HTTP or HTTPS URL with a host.
// Validation does not fetch the target, and previews remain local.
func (f weblinkFlags) validate() error {
	if f.jsonInput != "" {
		if f.cmd != nil && f.cmd.Flags().Changed(flagURL) {
			if err := core.ValidateRemoteLinkURL(f.url); err != nil {
				e := cli.NewInputError(cli.CodeFlagValueInvalid, "--"+flagURL+": "+err.Error())
				e.Field = flagURL
				return e
			}
		}
		// Native input and collisions require reading the payload. They are
		// checked during composition, before client construction.
		return nil
	}
	if f.url == "" {
		e := cli.NewInputError(cli.CodeFlagRequired, fmt.Sprintf(
			"a web link needs a target — give one with --%s", flagURL,
		))
		e.Field = flagURL
		return e
	}
	if err := core.ValidateRemoteLinkURL(f.url); err != nil {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s must be an absolute HTTP or HTTPS URL with a host (https://example.com/page), and %q is not",
			flagURL, f.url,
		))
		e.Field = flagURL
		return e
	}
	return nil
}

// runWeblinkAdd composes the link once and either previews it or
// attaches it to every key.
//
// The preview builds no client, which is runCommentAdd's property
// unchanged and for its reason.
func runWeblinkAdd(ctx context.Context, deps Deps, inv command.Invocation, link weblinkFlags, parallelism int) (envelope.IssueWeblinkAddOutput, error) {
	var zero envelope.IssueWeblinkAddOutput
	req, native, err := link.compose(ctx, inv)
	if err != nil {
		return zero, err
	}
	payload, err := req.MarshalJSON()
	if err != nil {
		return zero, err
	}
	object := native.Object.Value
	out := envelope.IssueWeblinkAddOutput{
		MutationState: envelope.MutationState{DryRun: inv.DryRun},
		Payload:       payload,
		URL:           object.URL.Value,
		Title:         object.Title.Value,
		Summary:       object.Summary.Value,
		Relationship:  native.Relationship.Value,
		GlobalID:      native.GlobalID.Value,
	}
	if inv.DryRun {
		out.Results = weblinkPreviewResults(inv.Args)
		return out, nil
	}

	client, err := deps.JiraClient()
	if err != nil {
		return zero, err
	}
	out.Results, err = weblinkAddFanOut(ctx, client, inv.Args, req, parallelism)
	return out, err
}

// weblinkPreviewResults builds the per-key entries for a dry run: one
// per key, in argv order, each successful and each carrying an empty
// operation object.
//
// Created is absent rather than false, and the distinction is this
// preview's own: false would claim Jira REPLACED an existing link, which
// is a specific thing to have happened and did not. Absence says the
// question was never asked — see envelope.IssueWeblinkAddData.Created,
// which is a pointer for exactly this reason.
func weblinkPreviewResults(keys []string) []envelope.KeyedResult[envelope.IssueWeblinkAddData] {
	results := make([]envelope.KeyedResult[envelope.IssueWeblinkAddData], len(keys))
	for i, key := range keys {
		results[i] = envelope.KeyedResult[envelope.IssueWeblinkAddData]{
			Issue: envelope.IssueRef{Key: key},
			OK:    true,
			Data:  &envelope.IssueWeblinkAddData{},
		}
	}
	return results
}

// weblinkAddFanOut attaches the link to every key and assembles the
// keyed payload.
//
// The keyed partial-failure policy is commentAddFanOut's, unchanged and
// for its reasons — and with the same sharp edge linkCreateFanOut has:
// without a --global-id, re-running a key that already succeeded adds a
// SECOND copy of the link, so results[] is what makes a retry safe.
func weblinkAddFanOut(
	ctx context.Context,
	client core.JiraClient,
	keys []string,
	req jira.RemoteLinkRequest,
	parallelism int,
) ([]envelope.KeyedResult[envelope.IssueWeblinkAddData], error) {
	outcomes := make([]weblinkAddOutcome, len(keys))
	// ONE spinner around the whole fan-out, for commentAddFanOut's reason.
	_, err := command.Spin(ctx, weblinkAddSpinnerLabel(keys), func(ctx context.Context) (struct{}, error) {
		xsync.Parallel(parallelism, len(keys), func(i int) {
			outcomes[i] = attachWeblink(ctx, client, keys[i], req)
		})
		return struct{}{}, nil
	})
	results := make([]envelope.KeyedResult[envelope.IssueWeblinkAddData], len(outcomes))
	raw := make([]error, len(outcomes))
	for i, o := range outcomes {
		results[i], raw[i] = o.result, o.err
	}
	if err != nil {
		return results, err
	}
	return results, command.KeyedFailure(results, raw)
}

// weblinkAddOutcome is one key's result plus the raw error behind a
// failure — commentAddOutcome's pairing, for its reason.
type weblinkAddOutcome struct {
	result envelope.KeyedResult[envelope.IssueWeblinkAddData]
	// err is nil exactly when result.OK.
	err error
}

// attachWeblink attaches one key's link.
//
// It publishes Created unconditionally on success — a real pointer to a
// real bool — because that is the one thing this response says that the
// caller could not already know, and the status code is the only place
// Jira says it.
func attachWeblink(ctx context.Context, client core.JiraClient, key string, req jira.RemoteLinkRequest) weblinkAddOutcome {
	write, err := client.AddRemoteLink(ctx, key, req)
	if err != nil {
		return failedWeblinkAddResult(key, err)
	}
	if err := core.ValidateRemoteLinkIdentity(write.Link); err != nil {
		return failedWeblinkAddResult(key, err)
	}
	created := write.Created
	data := envelope.IssueWeblinkAddData{
		Created: &created,
		Weblink: &envelope.WeblinkRef{
			ID:   strconv.FormatInt(write.Link.ID.Value, 10),
			Self: write.Link.Self.Value,
		},
	}
	return weblinkAddOutcome{result: envelope.KeyedResult[envelope.IssueWeblinkAddData]{
		Issue: envelope.IssueRef{Key: key},
		OK:    true,
		Data:  &data,
	}}
}

// failedWeblinkAddResult records one key's failure, resolved through the
// SAME taxonomy the envelope's own errors[] entries use.
func failedWeblinkAddResult(key string, err error) weblinkAddOutcome {
	class := cli.MapError(err)
	return weblinkAddOutcome{
		result: envelope.KeyedResult[envelope.IssueWeblinkAddData]{
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

// weblinkAddSpinnerLabel is the fan-out's one spinner label. Present
// tense, and correctly so: runWeblinkAdd returns before the fan-out on a
// dry run, so no animation is drawn over a run that writes nothing.
func weblinkAddSpinnerLabel(keys []string) string {
	if len(keys) == 1 {
		return "Attaching a web link to " + keys[0]
	}
	return fmt.Sprintf("Attaching a web link to %d issues", len(keys))
}

// renderWeblinkAdd writes the human view. Preview tense is structural
// (axis H2): the two branches are separate functions with no shared
// sentence.
func renderWeblinkAdd(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueWeblinkAddOutput)
	if !ok {
		return fmt.Errorf("issue.weblink.add: human renderer received %T, want envelope.IssueWeblinkAddOutput", env.Data)
	}
	if out.DryRun {
		renderWeblinkAddPreview(v, out)
		return nil
	}
	renderWeblinkAddResults(v, out)
	return nil
}

// renderWeblinkAddPreview draws the dry-run view.
//
// The URL is Jira-bound but CALLER-authored — it came off this command
// line — and it still goes through cli.Text, because a preview that
// echoed a hostile argument straight to a terminal would be the one
// place this CLI let an escape sequence through on its own input.
func renderWeblinkAddPreview(v *output.View, out envelope.IssueWeblinkAddOutput) {
	keys := weblinkKeys(out.Results)
	v.Line(fmt.Sprintf("Would attach %s to %s.", cli.Text(out.URL), joinKeys(keys)))
	if out.GlobalID != "" {
		v.Line(cli.Text(fmt.Sprintf("Would replace any existing link with the global id %q.", out.GlobalID)))
	}
	v.Section("Payload")
	v.Block(cli.Text(string(out.Payload)))
	v.Hint("Nothing was sent. Drop --dry-run to apply it.")
}

// renderWeblinkAddResults draws the live view: one line per key, in argv
// order, saying whether the link was ADDED or REPLACED rather than
// collapsing the two into "done" — they are different things to have
// happened, and the caller who passed --global-id passed it to find out
// which.
func renderWeblinkAddResults(v *output.View, out envelope.IssueWeblinkAddOutput) {
	for _, r := range out.Results {
		key := cli.Entity(r.Issue.Key)
		if !r.OK {
			v.Line(key + cli.Text(": "+r.Err.Message))
			v.Hint(r.Err.Hint)
			continue
		}
		v.Line(key + cli.Text(": "+weblinkOutcomeWord(r.Data)+"."))
	}
}

// weblinkOutcomeWord says what happened to one key.
//
// A nil Created is treated as "web link added", which is reachable only
// from a hand-built payload — attachWeblink always sets it on the live
// path — and is the right default anyway: adding is what this command
// does, and "replaced" is the claim that would need evidence.
func weblinkOutcomeWord(data *envelope.IssueWeblinkAddData) string {
	if data != nil && data.Created != nil && !*data.Created {
		return "web link replaced"
	}
	return "web link added"
}

// newWeblinkDelete returns `jira issue weblink delete`.
func newWeblinkDelete(deps Deps) *cobra.Command {
	// Bare, no options — and notably no --force, where `comment delete`
	// takes one. The test is the undo: a deleted comment is prose nobody
	// can recover, while a web link is a URL and a title that `weblink
	// add` puts straight back. A confirmation earns its keystroke only in
	// front of something irreversible.
	var mut command.MutationFlags
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueWeblinkDeleteOutput]{
		Op:         "issue.weblink.delete",
		WritesJira: true,
		Identity:   true,
		Use:        "delete ISSUE_KEY WEBLINK_ID",
		Short:      "Delete a web link from an issue",
		Long: "Remove one URL from an issue.\n\n" +
			"It takes the issue AND the link id, because a web link's id is " +
			"scoped to the issue it hangs off. That is the opposite of `jira " +
			"issue link delete`, whose ids are unique across the site and which " +
			"therefore takes no key — the two resources are different in exactly " +
			"this way, and the commands say so rather than pretending to match.\n\n" +
			"Get the id from `jira issue weblink list KEY-123`.\n\n" +
			"There is no `--force`. A web link is a URL and a title, and `jira " +
			"issue weblink add` puts one back — unlike `jira issue comment " +
			"delete`, which destroys prose and therefore does ask.\n\n" +
			"`--dry-run` composes the request and stops. It never contacts " +
			"Jira, so it cannot tell you whether the link exists.\n\n" +
			"`--web` is refused here. It opens an issue page, and this command " +
			"writes to one link on it.",
		Example: "# remove a web link\n" +
			"$ jira issue weblink delete KEY-123 10042\n\n" +
			"# find the id first\n" +
			"$ jira issue weblink list KEY-123 --output=json --jq '.data.results[].data.weblinks[] | {id, url}'\n\n" +
			"# check what would go, without deleting\n" +
			"$ jira issue weblink delete KEY-123 10042 --dry-run",
		Args: command.ArgSpec{Min: 2, Max: 2, Positions: []command.Arg{
			{Name: "ISSUE_KEY", Predictor: "issuekey"},
			{Name: "WEBLINK_ID"},
		}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddMutationFlags(c, &mut)
		},
		Validate: func() error {
			return refuseWebOnSingleWrite(cmd)
		},
		Human: renderWeblinkDelete,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueWeblinkDeleteOutput, error) {
			return runWeblinkDelete(ctx, deps, inv)
		},
	})
}

// runWeblinkDelete previews or removes one web link. The preview builds
// no client, which is runCommentAdd's property unchanged.
func runWeblinkDelete(ctx context.Context, deps Deps, inv command.Invocation) (envelope.IssueWeblinkDeleteOutput, error) {
	var zero envelope.IssueWeblinkDeleteOutput

	key, linkID := inv.Args[0], inv.Args[1]
	out := envelope.IssueWeblinkDeleteOutput{
		MutationState: envelope.MutationState{DryRun: inv.DryRun},
		WeblinkID:     linkID,
	}
	out.Issue.Key = key
	if inv.DryRun {
		return out, nil
	}

	client, err := deps.JiraClient()
	if err != nil {
		return zero, err
	}
	if _, err := command.Spin(ctx, "Deleting web link "+linkID+" on "+key, func(ctx context.Context) (core.RemoteLinkDeleteWrite, error) {
		return client.DeleteRemoteLink(ctx, key, linkID)
	}); err != nil {
		return zero, err
	}
	return out, nil
}

// renderWeblinkDelete writes the human view: one line, in the tense of
// whichever path ran (axis H2 — the two sentences share no fragment).
func renderWeblinkDelete(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueWeblinkDeleteOutput)
	if !ok {
		return fmt.Errorf("issue.weblink.delete: human renderer received %T, want envelope.IssueWeblinkDeleteOutput", env.Data)
	}
	// cli.Entity for the identities, cli.Text for the connectives, as
	// SIBLING spans on one line rather than nested (axis H5).
	id, key := cli.Entity(out.WeblinkID), cli.Entity(out.Issue.Key)
	if out.DryRun {
		v.Line(cli.Text("Would delete web link ") + id + cli.Text(" on ") + key + cli.Text("."))
		v.Hint("Nothing was deleted. Drop --dry-run to apply it.")
		return nil
	}
	v.Line(cli.Text("Deleted web link ") + id + cli.Text(" on ") + key + cli.Text("."))
	return nil
}
