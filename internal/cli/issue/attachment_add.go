package issue

import (
	"context"
	"fmt"

	xslices "github.com/gechr/x/slices"
	xsync "github.com/gechr/x/sync"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/attachmentfiles"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// flagFile names a file to upload. Repeatable, and it is also what
// disambiguates this command's positional grammar — see splitKeysAndFiles.
const flagFile = "file"

// newAttachmentAdd returns `jira issue attachment add`.
//
// Its multipart request streams from local files. Two decisions are its own:
//
//   - the positional grammar, which cannot be `KEY… PATH…` as the layout
//     design writes it, because two variadic lists cannot be split
//     without a heuristic this tree has already rejected once
//     (splitKeysAndFiles);
//   - the upload streams from disk rather than being composed in memory,
//     which is the adapter's doing (adapters/jira.AddAttachment) but is
//     why this command hands over PATHS rather than bytes.
func newAttachmentAdd(deps Deps) *cobra.Command {
	var keyed command.KeyedFlags
	// Bare, no options. --force has nothing destructive to confirm,
	// --validate-remote has nothing to check against the site (the files
	// are local and their acceptance is decided by the upload itself),
	// --verify has no extra round trip to gate — the upload's own
	// response carries the created attachments — and --no-readback would
	// suppress a readback this command never makes.
	var mut command.MutationFlags
	var files []string
	// cmd is captured so Validate can reach the parsed positional
	// arguments, which the grammar split needs and Spec.Validate is not
	// handed — the same capture newTransition makes, for the same reason.
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueAttachmentAddOutput]{
		Op:         "issue.attachment.add",
		WritesJira: true,
		Identity:   true,
		Keyed:      true,
		Use:        "add ISSUE_KEY... [PATH...]",
		Short:      "Attach files to issues",
		Long: "Upload one or more files to one or more issues.\n\n" +
			"Shell completion follows the selected grammar: recent issue keys for issue positions and local paths for file positions or `--file`. Quote the whole CSV argument when a file name contains a comma; completion retains the selected CSV fields.\n\n" +
			"**The grammar is decided by `--file`, not by guessing.** With " +
			"`--file` every positional is an issue key, so `jira issue " +
			"attachment add KEY-123 KEY-124 --file report.pdf` attaches one file " +
			"to two issues. Without it the FIRST positional is the issue and " +
			"everything after it is a path, so `jira issue attachment add KEY-123 " +
			"a.log b.log` attaches two files to one issue. The two forms cover " +
			"the two things people actually do, and neither requires the CLI to " +
			"guess whether `report.pdf` is a key.\n\n" +
			"That is a departure from a `KEY… PATH…` grammar, and a necessary " +
			"one: two variadic lists side by side cannot be split without a rule " +
			"like \"does it look like a key\" or \"does it exist on disk\", and " +
			"both make the meaning of a command line depend on something other " +
			"than what was typed.\n\n" +
			"Local preflight requires regular files, with each file and their " +
			"combined content at most 100 MiB (104857600 bytes). Symlinks to " +
			"regular files are allowed. The live adapter opens and rechecks every " +
			"input before each request. Contents stream without buffering the " +
			"whole batch; growth cannot exceed the content limit.\n\n" +
			"This is a keyed command: one entry per key at `data.results[]`, in " +
			"the order you gave them. Every key gets the SAME files. A key that " +
			"fails does not fail the others, and the envelope names which ones " +
			"took the upload — which matters more here than almost anywhere " +
			"else, because Jira has no de-duplication for attachments: re-running " +
			"a key that already succeeded attaches a SECOND copy under the same " +
			"name. Total failures and canceled runs retain ordered outcomes and " +
			"local input metadata in the full failure frame, including completed " +
			"attachment IDs. Failed entries do not prove that Jira stored nothing; " +
			"inspect uncertain targets before retrying.\n\n" +
			"Each successful entry carries the attachments Jira created, with the " +
			"id `jira issue attachment download` and `delete` both take. It costs " +
			"no extra request: the upload's own response carries them.\n\n" +
			"`--dry-run` checks local metadata and reports authored paths, sizes " +
			"and MIME types inferred from filename extensions at `data.file_details`. " +
			"It reads no file contents and never contacts Jira. It cannot prove " +
			"readability, issue existence or that files will remain unchanged. " +
			"An incomplete acknowledgement requires inspection before retrying: " +
			"Jira may already have stored attachments.\n\n" +
			"`--web` is refused here. It opens one page and this command writes " +
			"to a list of keys.",
		Example: "# attach one file to one issue\n" +
			"$ jira issue attachment add KEY-123 report.pdf\n\n" +
			"# several files, one issue\n" +
			"$ jira issue attachment add KEY-123 a.log b.log c.log\n\n" +
			"# one file, several issues — --file makes every positional a key\n" +
			"$ jira issue attachment add KEY-123 KEY-124 --file incident.log\n\n" +
			"# several files across several issues\n" +
			"$ jira issue attachment add KEY-123 KEY-124 --file a.log --file b.log\n\n" +
			"# inspect metadata without reading contents or uploading\n" +
			"$ jira issue attachment add KEY-123 report.pdf --dry-run\n\n" +
			"# just the new attachment ids, as json\n" +
			"$ jira issue attachment add KEY-123 report.pdf --output=json --jq '.data.results[].data.attachments[].id'",
		Args: command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "word_attachment"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddKeyedFlags(c, &keyed)
			command.AddMutationFlags(c, &mut)
			command.AddStringSliceVar(c, &files, flagFile, nil,
				"A file to upload; repeatable, and it makes every positional an issue key",
				command.FlagOpts{Terse: "File to upload", Placeholder: "path", Forward: true, Complete: "predictor=word_file_csv,comma"})
		},
		Validate: func() error {
			if err := keyed.Validate(); err != nil {
				return err
			}
			// The split is checked here, before any I/O, so a grammar
			// mistake is refused rather than discovered after the first
			// file has been opened.
			if _, _, err := splitKeysAndFiles(cmd.Flags().Args(), files); err != nil {
				return err
			}
			return refuseWebOnKeyedWrite(cmd)
		},
		Human:         renderAttachmentAdd,
		RetainOnError: func(out envelope.IssueAttachmentAddOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueAttachmentAddOutput, error) {
			return runAttachmentAdd(ctx, deps, inv, files, keyed.Parallelism)
		},
	})
}

// splitKeysAndFiles divides the positional arguments into issue keys and
// file paths.
//
// The grammar is `KEY… [PATH…]`, which is ambiguous on its face — every
// argument is a bare word and nothing in one says whether it is a key or
// a path. The rule that resolves it is the presence of --file, exactly
// as `issue transition` resolves its own `KEY… [STATUS]` with
// --transition: with the flag every positional is a key, without it the
// first is the key and the rest are paths.
//
// The two rejected alternatives are worth naming, because both look
// reasonable:
//
//   - MATCH A KEY PATTERN. `transition`'s own splitPositionals records
//     why not: a project whose keys are unusual breaks the matcher, and
//     the failure is a command that silently does something else.
//   - TEST THE FILESYSTEM. Worse still — the meaning of a command line
//     would depend on what happens to exist on disk when it runs, so the
//     same invocation could parse two ways on two machines.
//
// It is a pure function called from BOTH Validate (to refuse) and Run
// (to use), rather than a split performed once and remembered, so the
// two can never disagree about where the boundary fell.
func splitKeysAndFiles(args, fileFlags []string) (keys, paths []string, err error) {
	if len(fileFlags) > 0 {
		if len(args) == 0 {
			return nil, nil, missingAttachmentKeys()
		}
		// --file given: every positional is a key, and any further paths
		// come from the repeated flag.
		return args, fileFlags, nil
	}
	switch len(args) {
	case 0:
		return nil, nil, missingAttachmentKeys()
	case 1:
		// One positional and no --file. It is a key with no file, which
		// is the likeliest typo in this grammar and deserves better than
		// a generic arity error.
		e := cli.NewInputError(cli.CodeArgCountInvalid, fmt.Sprintf(
			"no file given — name one after the issue (`jira issue attachment add %s report.pdf`) or pass --%s",
			args[0], flagFile,
		))
		e.Field = flagFile
		return nil, nil, e
	}
	return args[:1], args[1:], nil
}

// missingAttachmentKeys refuses an invocation with no issue key.
// Reachable only through --file with no positionals: without the flag
// the ArgSpec's own minimum has already refused an empty argv.
func missingAttachmentKeys() error {
	return cli.NewInputError(cli.CodeArgCountInvalid,
		"no issue key given — name at least one issue to attach to")
}

// runAttachmentAdd uploads the files to every key.
//
// Preflight inspects local metadata before client construction. A preview
// reads no contents and builds no client. The live adapter owns and rechecks
// its input descriptors because paths can change after inspection.
func runAttachmentAdd(ctx context.Context, deps Deps, inv command.Invocation, fileFlags []string, parallelism int) (envelope.IssueAttachmentAddOutput, error) {
	var zero envelope.IssueAttachmentAddOutput

	keys, paths, err := splitKeysAndFiles(inv.Args, fileFlags)
	if err != nil {
		return zero, err
	}
	metadata, err := attachmentfiles.Inspect(ctx, paths)
	if err != nil {
		return zero, err
	}
	details := xslices.Map(metadata, func(file attachmentfiles.Metadata) envelope.AttachmentFileInput {
		return envelope.AttachmentFileInput{Path: file.Path, Size: file.Size, MIMEInferred: file.MIMEInferred}
	})
	out := envelope.IssueAttachmentAddOutput{
		MutationState: envelope.MutationState{DryRun: inv.DryRun},
		Files:         paths,
		FileDetails:   details,
	}
	if inv.DryRun {
		out.Results = attachmentPreviewResults(keys)
		return out, nil
	}

	client, err := deps.JiraClient(ctx)
	if err != nil {
		return zero, err
	}
	out.Results, err = attachmentAddFanOut(ctx, client, keys, paths, parallelism)
	return out, err
}

// attachmentPreviewResults builds the per-key entries for a dry run: one
// per key, in argv order, each successful and each carrying an empty
// operation object — previewResults' shape, for its reason.
func attachmentPreviewResults(keys []string) []envelope.KeyedResult[envelope.IssueAttachmentAddData] {
	results := make([]envelope.KeyedResult[envelope.IssueAttachmentAddData], len(keys))
	for i, key := range keys {
		results[i] = envelope.KeyedResult[envelope.IssueAttachmentAddData]{
			Issue: envelope.IssueRef{Key: key},
			OK:    true,
			Data:  &envelope.IssueAttachmentAddData{},
		}
	}
	return results
}

// attachmentAddFanOut uploads to every key and assembles the keyed
// payload.
//
// The keyed partial-failure policy is commentAddFanOut's, and the
// duplicate hazard is linkCreateFanOut's in its sharpest form: Jira
// keeps two attachments with the same filename quite happily, so
// re-running a key that already succeeded leaves the issue carrying the
// file twice with no way to tell the copies apart but their ids.
// Retained results identify completed resources and uncertain outcomes.
// Inspect uncertain targets before deciding whether to repeat an upload.
//
// **Each key re-reads the files.** The fan-out passes paths rather than
// open handles, so N keys mean N reads of the same file from disk. That
// is deliberate: sharing one handle across concurrent uploads would
// require seeking it per request, and buffering the file once to avoid
// the re-read would put the whole thing in memory — the exact cost the
// streaming upload exists to avoid.
func attachmentAddFanOut(
	ctx context.Context,
	client core.JiraClient,
	keys, paths []string,
	parallelism int,
) ([]envelope.KeyedResult[envelope.IssueAttachmentAddData], error) {
	outcomes := make([]attachmentAddOutcome, len(keys))
	// ONE spinner around the whole fan-out, for commentAddFanOut's
	// reason: a spinner owns the terminal's live region and several
	// drawing at once interleave into corruption.
	_, err := command.Spin(ctx, attachmentAddSpinnerLabel(keys, paths), func(ctx context.Context) (struct{}, error) {
		xsync.Parallel(parallelism, len(keys), func(i int) {
			outcomes[i] = uploadTo(ctx, client, keys[i], paths)
		})
		return struct{}{}, nil
	})
	results := make([]envelope.KeyedResult[envelope.IssueAttachmentAddData], len(outcomes))
	raw := make([]error, len(outcomes))
	for i, o := range outcomes {
		results[i], raw[i] = o.result, o.err
	}
	if err != nil {
		// Spin joins every producer before returning. Preserve completed
		// identities and failed targets while cancellation describes the run.
		return results, err
	}
	return results, command.KeyedFailure(results, raw)
}

// attachmentAddOutcome is one key's result plus the raw error behind a
// failure — commentAddOutcome's pairing, for its reason.
type attachmentAddOutcome struct {
	result envelope.KeyedResult[envelope.IssueAttachmentAddData]
	// err is nil exactly when result.OK.
	err error
}

// uploadTo attaches the files to one key.
func uploadTo(ctx context.Context, client core.JiraClient, key string, paths []string) attachmentAddOutcome {
	write, err := client.AddAttachment(ctx, key, paths)
	if err != nil {
		return failedAttachmentAddResult(key, err)
	}
	if err := core.ValidateAttachmentReceipt(write.Attachments, len(paths)); err != nil {
		return failedAttachmentAddResult(key, err)
	}
	data := envelope.IssueAttachmentAddData{Attachments: attachmentViews(write.Attachments)}
	return attachmentAddOutcome{result: envelope.KeyedResult[envelope.IssueAttachmentAddData]{
		Issue: envelope.IssueRef{Key: key},
		OK:    true,
		Data:  &data,
	}}
}

// failedAttachmentAddResult records one key's failure, resolved through
// the SAME taxonomy the envelope's own errors[] entries use.
func failedAttachmentAddResult(key string, err error) attachmentAddOutcome {
	class := cli.MapError(err)
	return attachmentAddOutcome{
		result: envelope.KeyedResult[envelope.IssueAttachmentAddData]{
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

// attachmentAddSpinnerLabel is the fan-out's one spinner label. Present
// tense, and correctly so: runAttachmentAdd returns before the fan-out
// on a dry run, so no animation is drawn over a run that sends nothing.
func attachmentAddSpinnerLabel(keys, paths []string) string {
	what := fmt.Sprintf("%d files", len(paths))
	if len(paths) == 1 {
		what = "a file"
	}
	if len(keys) == 1 {
		return fmt.Sprintf("Uploading %s to %s", what, keys[0])
	}
	return fmt.Sprintf("Uploading %s to %d issues", what, len(keys))
}

// renderAttachmentAdd writes the human view. Preview tense is structural
// (axis H2): the two branches are separate functions with no shared
// sentence.
func renderAttachmentAdd(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueAttachmentAddOutput)
	if !ok {
		return fmt.Errorf("issue.attachment.add: human renderer received %T, want envelope.IssueAttachmentAddOutput", env.Data)
	}
	if out.DryRun {
		renderAttachmentAddPreview(v, out)
		return nil
	}
	renderAttachmentAddResults(v, out)
	return nil
}

// renderAttachmentAddPreview draws the dry-run view.
//
// The paths go through cli.Text even though they came off this command
// line rather than from Jira: a preview echoing an argument straight to
// a terminal would be the one place this CLI let an escape sequence
// through on its own input.
func renderAttachmentAddPreview(v *output.View, out envelope.IssueAttachmentAddOutput) {
	targets := joinKeys(weblinkKeys(out.Results))
	for _, file := range out.FileDetails {
		v.Line(fmt.Sprintf("Would upload %s (%s; inferred MIME %s) to %s.", cli.Text(file.Path), cli.Bytes(file.Size), cli.Text(file.MIMEInferred), targets))
	}
	v.Hint("Local metadata checked; no file contents read and nothing uploaded. Drop --dry-run to upload.")
}

// renderAttachmentAddResults draws the live view: one line per key, then
// the id and name of each attachment that key gained.
//
// The ids are reported rather than just a count, because they are the
// handle the download and delete verbs take and the only place a caller
// gets them without a second command.
func renderAttachmentAddResults(v *output.View, out envelope.IssueAttachmentAddOutput) {
	for _, r := range out.Results {
		key := cli.Entity(r.Issue.Key)
		if !r.OK {
			v.Line(key + cli.Text(": "+r.Err.Message))
			v.Hint(r.Err.Hint)
			continue
		}
		v.Line(key + cli.Text(": "+attachmentCountPhrase(len(r.Data.Attachments))+"."))
		for _, a := range r.Data.Attachments {
			// The filename is whatever the uploader called it — the most
			// attacker-influenced string this command renders, since it
			// crosses from a filesystem rather than a Jira form — so
			// cli.Text is doing real work here (axis H4).
			v.Line("  " + cli.Entity(a.ID) + cli.Text("  "+a.Filename))
		}
	}
}

// attachmentCountPhrase says how many files landed, in words that read
// correctly for one.
func attachmentCountPhrase(n int) string {
	if n == 1 {
		return "1 file attached"
	}
	return fmt.Sprintf("%d files attached", n)
}
