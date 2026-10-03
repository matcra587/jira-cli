package issue

import (
	"context"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// newCreate returns `jira issue create`.
//
// THE FIRST FIELD-TAKING WRITE, and the first command in this tree with no
// subject to name: every other issue command is about issues that already
// exist, and this one makes one. Three consequences are contract for the
// writes that copy it:
//
//   - the payload is composed from three routes that are alternatives
//     rather than layers, and a disagreement between them is refused
//     rather than resolved (issueWriteFlags.compose);
//   - the identity is a SERVER-created outcome, so it is conditional in
//     the envelope while the composed request is not
//     (envelope.IssueCreateOutput);
//   - `--verify` earns its place here, unlike on `comment add`, because
//     Jira's 201 carries an identity and not an issue — the confirmation
//     costs a real second request (command.WithVerify).
func newCreate(deps Deps) *cobra.Command {
	var in issueWriteFlags
	// mut carries --dry-run and --verify. No --force: creating an issue is
	// additive, so there is no destructive step to confirm and nothing for
	// the flag to skip (see command.WithForce's own doc comment).
	var mut command.MutationFlags

	return command.MustNew(command.Spec[envelope.IssueCreateOutput]{
		Op:         "issue.create",
		WritesJira: true,
		Identity:   true,
		// data.payload is a wire passthrough: the keys inside it are
		// Jira's own (issuetype, fixVersions, a tenant's
		// customfield_NNNNN), not names this CLI minted, so the
		// snake_case rule stops at that boundary and the fidelity axis
		// proves an unmodeled field survives composition.
		Passthrough: true,
		Use:         "create",
		Short:       "Create an issue",
		Long: "Create one issue in a project.\n\n" +
			"The issue's content comes from three routes, and they are " +
			"alternatives rather than layers: `--json-input` sends a whole " +
			"native payload (the lossless route, and the only one that reaches " +
			"a custom field), `--markdown`/`--markdown-file` write the " +
			"description as Markdown converted to ADF, and the field flags " +
			"below set the standard system fields. Mixing the field flags with " +
			"`--json-input` is allowed right up until the two set the SAME " +
			"field, which is refused rather than silently resolved — and the " +
			"check reads both halves of the payload, so a field set through " +
			"`update` collides exactly as one in `fields` does.\n\n" +
			"Native input also retains top-level `properties`, `historyMetadata`, " +
			"`transition` and future request members. Jira validates their meaning. " +
			"`fields` and `update` must be objects when present; explicit empty " +
			"objects survive composition.\n\n" +
			"`--json-input` takes a file path, or `-` to read the payload from " +
			"standard input — so a composed document can be piped straight in " +
			"without ever touching disk. That is the route to a field this " +
			"command has no flag for: a tenant's custom fields go in the " +
			"payload verbatim, under their own ids, and reach Jira untouched. " +
			"There is deliberately no `--field key=value`, because it could " +
			"not express an array, an object or a cascading select without " +
			"inventing a second, untyped way to say what the payload already " +
			"says exactly.\n\n" +
			"`--project`, `--type` and `--summary` are required, from a flag or " +
			"from the payload — Jira rejects a create without them, and " +
			"refusing locally costs a round trip less.\n\n" +
			"A local `--dry-run` composes the whole request and stops. It never " +
			"contacts Jira, so `data.payload` is byte-identical to what a live " +
			"run sends, but it cannot tell you whether the project exists or " +
			"whether `--priority Blocker` is a priority on this site. It is " +
			"also how you learn the payload shape: what it prints is valid " +
			"input for `--json-input`, so a preview round-trips into a real " +
			"run.\n\n" +
			"`--verify` reads the new issue back and reports its stored " +
			"summary and status. It is a SECOND request, which is why it is a " +
			"flag: the creation response identifies the new issue rather than " +
			"returning its stored fields. `data.receipt` preserves the native " +
			"response, including optional transition and watcher outcomes. " +
			"`data.applied` records HTTP 201 even when the response lacks a " +
			"usable identity. A failed followup retains the created key; retry " +
			"only that followup, because repeating create makes another issue. " +
			"Creation failures and interruption retain the attempted payload " +
			"and any acknowledged identity in the full failure frame.\n\n" +
			"`--assignee` and `--reporter` take an Atlassian account id, not a " +
			"display name — or `@me`, which resolves to whoever your " +
			"credentials authenticate as. Resolving `@me` reads the current " +
			"user from the site, so `--dry-run` leaves it unresolved in the " +
			"payload and says so in a warning; add `--validate-remote` to " +
			"resolve it in a preview too. A live run always resolves it.\n\n" +
			"`--web` is refused: there is no page to open for an issue that " +
			"does not exist yet.",
		Example: "# the smallest useful create\n" +
			"$ jira issue create --project ENG --type Task --summary 'Rotate the signing key'\n\n" +
			"# with a description written as Markdown\n" +
			"$ jira issue create --project ENG --type Bug --summary 'Login loops' --markdown 'Reproduced on staging.'\n\n" +
			"# preview the request without sending it\n" +
			"$ jira issue create --project ENG --type Task --summary 'Draft' --dry-run\n\n" +
			"# learn the payload shape, then send one\n" +
			"$ jira issue create --project ENG --type Task --summary 'Draft' --dry-run --output=json --jq '.data.payload' > issue.json\n" +
			"$ jira issue create --json-input issue.json\n\n" +
			"# pipe a payload in, with a custom field flags cannot reach\n" +
			"$ echo '{\"fields\":{\"project\":{\"key\":\"ENG\"},\"issuetype\":{\"name\":\"Task\"},\"summary\":\"Ship it\",\"customfield_10016\":5}}' | jira issue create --json-input -\n\n" +
			"# repeatable fields, and a due date\n" +
			"$ jira issue create --project ENG --type Task --summary 'Ship it' --label release --label urgent --component api --due 2026-09-01\n\n" +
			"# just the new key, as json\n" +
			"$ jira issue create --project ENG --type Task --summary 'Ship it' --output=json --jq '.data.issue.key'\n\n" +
			"# assign it to yourself\n" +
			"$ jira issue create --project ENG --type Task --summary 'Ship it' --assignee @me",
		Args: command.ArgSpec{},
		Flags: func(c *cobra.Command) {
			in.register(c, command.CreateIssueVerb)
			// --validate-remote joins --verify now that something backs it:
			// resolving `@me` in a preview is a real read against the site,
			// which is exactly what the flag opts into (see
			// command.WithValidateRemote, and resolveAccounts for the
			// three-branch shape it selects).
			command.AddMutationFlags(c, &mut, command.WithVerify(), command.WithValidateRemote())
		},
		Validate: func() error {
			if err := in.validate(); err != nil {
				return err
			}
			return refuseWebOnCreate(in.cmd)
		},
		Human:         renderCreate,
		RetainOnError: func(out envelope.IssueCreateOutput) bool { return out.Attempted },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueCreateOutput, error) {
			return runCreate(ctx, deps, inv, &in, createOptions{
				verify:         mut.Verify,
				validateRemote: mut.ValidateRemote,
			})
		},
	})
}

// createOptions is what runCreate needs from the mutation flags, gathered
// so the handler's signature does not grow a parameter per flag — the same
// shape editOptions and commentAddOptions take, and for the same reason.
//
// --dry-run is deliberately absent: it arrives on the Invocation, read from
// the flag the construction path registered itself.
type createOptions struct {
	verify         bool
	validateRemote bool
}

// refuseWebOnCreate refuses --web on the one command in this group with
// nothing to browse to.
//
// A sibling of refuseWebOnKeyedRead and refuseWebOnKeyedWrite rather than a
// reuse: those two refuse because there are MANY subjects and --web opens
// one page. This one refuses because there are NONE — the issue does not
// exist until the command succeeds, and by then the flag has already been
// parsed. Saying "this command writes to a list of keys" here would
// misdescribe why the flag cannot work.
//
// It stays CodeFlagNotApplicable for their reason: the flag is real and
// spelled right, so the fix is to drop it.
func refuseWebOnCreate(cmd *cobra.Command) error {
	if !isWeb(cmd) {
		return nil
	}
	e := cli.NewInputError(cli.CodeFlagNotApplicable,
		"--web opens an existing page, and this command creates an issue that has no page until it succeeds — run `jira issue view KEY --web` with the key it returns")
	e.Field = flagWeb
	return e
}

// runCreate composes the request once and either publishes it or sends it.
//
// **A BARE preview builds no client.** With no `@me` to resolve and no
// --validate-remote, a dry run returns before deps.JiraClient() is ever
// called, which is what "never contacts Jira" has to mean in code rather
// than in a doc comment: the preview cannot reach the network because it
// never obtains the thing that could. It also means it works with no
// credentials configured at all — a caller can learn the payload shape
// before they have logged in.
//
// The one exception is asked for explicitly. `--validate-remote` opts into
// the reads that need the site, and resolving `@me` is one; resolveAccounts
// owns that decision for both verbs, and a preview that takes it says so in
// a warning rather than quietly.
//
// **Account resolution happens BEFORE composition**, because compose bakes
// the value into the payload — resolving afterwards would publish a
// payload carrying the sentinel while sending one carrying the id, so the
// preview and the live request would disagree about the very field the
// flag set.
//
// **Required fields are checked after composition, not during Validate.**
// They can arrive from a flag OR from the payload file, and reading that
// file is I/O that Spec.Validate is not the place for. The check is still
// local and still happens before any write, so it costs no round trip — it
// just cannot happen as early as a pure flag refusal.
func runCreate(ctx context.Context, deps Deps, inv command.Invocation, in *issueWriteFlags, opts createOptions) (envelope.IssueCreateOutput, error) {
	var zero envelope.IssueCreateOutput

	client, err := in.resolveAccounts(ctx, deps, inv.DryRun, opts.validateRemote)
	if err != nil {
		return zero, err
	}

	composed, err := in.compose(ctx, inv.Stdin, inv.StdinIsTerminal)
	if err != nil {
		return zero, err
	}

	out := envelope.IssueCreateOutput{
		MutationState:    envelope.MutationState{DryRun: inv.DryRun},
		Payload:          composed.payload,
		DescriptionRoute: composed.route,
	}

	// Checked on BOTH paths, preview included. A dry run that skipped it
	// would report a payload Jira is certain to reject, which is the one
	// thing a preview must not do — the whole point is to find that out
	// before sending.
	if err := requireCreateFields(composed.req); err != nil {
		return zero, err
	}
	if inv.DryRun {
		return out, nil
	}

	// The sentinel must never reach the wire. resolveAccounts already
	// settled it on this path, so this is a guard against a future edit
	// reordering the two, not a case that can arise today.
	if err := command.RefuseMeWithoutResolution(in.fields.AccountFields()); err != nil {
		return zero, err
	}
	if client == nil {
		// No account lookup was needed. Construct only after the payload
		// has passed validation and the local preview has returned.
		if client, err = deps.JiraClient(); err != nil {
			return zero, err
		}
	}
	out.Attempted = true
	write, err := command.Spin(ctx, "Creating the issue", func(ctx context.Context) (core.IssueCreateWrite, error) {
		return client.CreateIssue(ctx, composed.req)
	})
	out.Applied = write.StatusCode == http.StatusCreated
	if out.Applied {
		out.Receipt = write.Receipt
	}
	if receiptErr := core.ValidateIssueCreation(write); receiptErr == nil {
		out.Issue = &envelope.IssueRef{ID: write.Issue.ID, Key: write.Issue.Key, Self: write.Issue.Self}
	} else if err == nil {
		err = receiptErr
	}
	if err == nil {
		err = core.ValidateIssueCreateFollowups(write.Receipt)
	}
	if err != nil {
		return out, err
	}

	if opts.verify {
		// The issue EXISTS — Jira said 201 and gave us its key — so a
		// failed readback must never read as a failed create. Returning the
		// error would discard the key and leave a caller who retries making
		// a SECOND issue, which is the exact duplicate CreateIssue's own
		// doc comment warns about. The failure is published instead, as the
		// outcome it is.
		verified, err := verifyIssue(ctx, client, out.Issue.Key)
		out.Verified, out.VerifyFailed = verified, err != nil
	}
	return out, nil
}

// requireCreateFields refuses a create that Jira would reject anyway.
//
// Local, and worth doing locally: these three are required on every
// project in every Jira, so refusing here turns a round trip and a server
// error about a payload the caller half-wrote into an immediate message
// naming the flag to add. It reads the COMPOSED request rather than the
// flags, which is what lets `--json-input` satisfy the requirement without
// the caller also passing the flags.
func requireCreateFields(req jira.IssueWriteRequest) error {
	present := map[string]bool{}
	for _, k := range req.Keys() {
		present[k] = true
	}
	for _, want := range []struct{ field, flag string }{
		{"project", command.FlagProject},
		{"issuetype", command.FlagIssueType},
		{"summary", command.FlagSummary},
	} {
		if present[want.field] {
			continue
		}
		e := cli.NewInputError(cli.CodeFlagRequired, fmt.Sprintf(
			"creating an issue needs a %s — set it with --%s, or include %q in the --%s payload",
			want.field, want.flag, want.field, command.FlagJSONInput,
		))
		e.Field = want.flag
		return e
	}
	return nil
}

// renderCreate writes the human view.
//
// **Preview tense is structural, not a phrasing habit** (axis H2): the
// local branches and the live branch are separate functions with no shared
// sentence between them, so a future edit to the live wording cannot leak
// a past-tense claim into the preview.
func renderCreate(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueCreateOutput)
	if !ok {
		return fmt.Errorf("issue.create: human renderer received %T, want envelope.IssueCreateOutput", env.Data)
	}
	if out.DryRun {
		renderCreatePreview(v, out)
		return nil
	}
	renderCreateResult(v, out)
	return nil
}

// renderCreatePreview draws the dry-run view. Every verb is conditional,
// and the payload is shown because "what would be sent" is the entire
// question a preview is run to answer.
func renderCreatePreview(v *output.View, out envelope.IssueCreateOutput) {
	v.Line("Would create an issue from this request.")
	// Section rather than a bare Line for the heading: it goes through
	// clog's Divider, which sets this preview apart from the JSON under
	// it. The title is passed PLAIN and must stay that way — Divider
	// styles what it is handed, so a pre-styled string would strand a
	// reset mid-span (axis H5).
	v.Section("Payload")
	v.Block(cli.Text(string(out.Payload)))
	// The hint names the round trip because this preview IS the discovery
	// affordance — there is no separate --generate-json (see write.go). A
	// caller who cannot tell that these bytes are valid input for
	// --json-input would go looking for a flag that deliberately does not
	// exist.
	v.Hint("Nothing was sent. Drop --dry-run to create it, or save this payload and pass it back with --json-input.")
}

// renderCreateResult draws the live view: the new key, and the readback
// when one was asked for.
func renderCreateResult(v *output.View, out envelope.IssueCreateOutput) {
	if out.Issue == nil {
		if out.Applied {
			v.Line("Jira acknowledged creation, but returned no usable issue identity.")
		} else if out.Attempted {
			v.Line("Creation was attempted; no creation acknowledgement was received.")
		}
		return
	}
	// cli.Entity for the key and cli.Text for the prose around it, as
	// SIBLING spans concatenated into one Line argument — never EntityAs
	// over the whole sentence, which would tint the outcome wording with
	// the key's identity color. The same split renderCommentAddResults
	// makes.
	v.Line("Created " + cli.Entity(out.Issue.Key) + cli.Text("."))
	switch {
	case out.Verified != nil:
		// Jira-authored, so it goes through cli.Text before it can reach a
		// terminal (axis H4).
		v.Line(cli.Text(fmt.Sprintf("Reads back as %q, status %s.", out.Verified.Summary, out.Verified.Status)))
	case out.VerifyFailed:
		v.Hint("The issue was created, but reading it back failed — run `jira issue view " + out.Issue.Key + "` to check it.")
	}
}
