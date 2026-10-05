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

// attachmentFields is the field set the read narrows to, for the same
// reason linkFields does: an issue's attachments arrive inside the issue
// itself — Jira has no list-an-issue's-attachments endpoint — so this
// command IS a GetIssue, and asking for one field keeps it from dragging
// every custom field and the whole description across the wire once per
// key.
var attachmentFields = []string{"attachment"}

// newAttachment returns the `jira issue attachment` command group.
func newAttachment(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attachment",
		Short: "Work with an issue's attachments",
		Long: "Read, upload and remove the files attached to issues.\n\n" +
			"An attachment is identified by its own numeric id, not by its " +
			"filename — two files uploaded under the same name are two " +
			"attachments — so listing is how you find the id that the other " +
			"attachment verbs take.\n\n" +
			"`add` takes issue keys; `download` and `delete` take an attachment " +
			"id and NO key, because an attachment id is unique across the site " +
			"and Jira's own endpoints for them take nothing else.\n\n" +
			"`add` reads the local files you name and streams them, so a " +
			"large upload is never held in memory. `download` writes into a " +
			"directory you choose and never outside it — the filename comes " +
			"from the attachment rather than from you, so it is checked before " +
			"it is used.\n\n" +
			"`delete` cannot be undone and asks before acting, exactly as `jira " +
			"issue comment delete` does. Downloading first is the only undo " +
			"there is.",
		Example: "# what is attached to KEY-123\n" +
			"$ jira issue attachment list KEY-123\n\n" +
			"# attach a file\n" +
			"$ jira issue attachment add KEY-123 report.pdf\n\n" +
			"# fetch one back, by id\n" +
			"$ jira issue attachment download 10042 --to ~/Downloads\n\n" +
			"# remove one, without a prompt\n" +
			"$ jira issue attachment delete 10042 --force",
	}
	cmd.AddCommand(newAttachmentAdd(deps))
	cmd.AddCommand(newAttachmentDelete(deps))
	cmd.AddCommand(newAttachmentDownload(deps))
	cmd.AddCommand(newAttachmentList(deps))
	return cmd
}

// newAttachmentList returns `jira issue attachment list`.
//
// On the absent paging flags. 0.15 gave this command `--limit` (default
// 50) and `--all`, and they are deliberately not carried forward:
// neither was pagination. Jira publishes no attachment-list endpoint, so
// the whole set arrives inside the issue read regardless, and 0.15 cut
// the in-memory array afterward — saving no request, no byte and no
// wait, while hiding attachment 51 onward from anyone who did not know
// to type `--all`. envelope.IssueAttachmentListOutput's own doc comment
// carries the full argument; the short version is that a default-on
// truncation of an already-complete array is a way to get a wrong answer
// quietly, and `--jq` already slices arrays for callers who want less.
func newAttachmentList(deps Deps) *cobra.Command {
	// keyed carries the shared multi-key surface (--parallelism). The
	// same call marks this command keyed, which command.New cross-checks
	// against the Keyed declaration below — see AddKeyedFlags.
	var keyed command.KeyedFlags
	// cmd is captured so Validate can ask it isWeb(cmd) — --web is a
	// PERSISTENT flag registered on the `issue` group (issue.go), so this
	// command inherits it whether or not it can honor it, and refusing an
	// explicit one is this command's own job (refuseWebOnKeyedRead, which
	// link.go owns and states the reasoning for).
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueAttachmentListOutput]{
		Op:       "issue.attachment.list",
		Identity: true,
		Keyed:    true,
		Use:      "list ISSUE_KEY...",
		Short:    "List an issue's attachments",
		Long: "List the files attached to one or more issues, with the id each " +
			"one is downloaded or deleted by.\n\n" +
			"This is a keyed command: results come back as one entry per key, " +
			"in the order you gave them, at `data.results[]`. A key that fails " +
			"does not fail the others — its entry carries `ok: false` and the " +
			"reason, and the command still succeeds. Only a run where EVERY " +
			"key failed is reported as a failure of the command itself.\n\n" +
			"After reads begin, total failure or interruption retains ordered " +
			"outcomes in the full failure frame. A failed entry can retain a " +
			"validated observation; inspect its error before treating the read " +
			"as complete. Missing or unusable native attachment arrays are " +
			"reported as `attachment_response_invalid`, not an empty issue.\n\n" +
			"Every attachment is returned, and there is no page size to set. " +
			"Jira has no endpoint that lists attachments, so they arrive whole " +
			"with the issue they belong to — there is no page to be on, and a " +
			"limit applied here would only hide rows the command had already " +
			"fetched. Use `--jq` if you want fewer of them.\n\n" +
			"`--web` is refused here. It opens one page and this command reads " +
			"a list of keys, so there is no single thing to open: run `jira " +
			"issue view KEY --web` for whichever issue you want to look at.",
		Example: "# what is attached to one issue\n" +
			"$ jira issue attachment list KEY-123\n\n" +
			"# several issues at once\n" +
			"$ jira issue attachment list KEY-123 KEY-124 KEY-125\n\n" +
			"# just the ids and names, as json\n" +
			"$ jira issue attachment list KEY-123 --output=json --jq '.data.results[].data.attachments[] | {id, filename}'",
		Args: command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddKeyedFlags(c, &keyed)
		},
		Validate: func() error {
			if err := keyed.Validate(); err != nil {
				return err
			}
			return refuseWebOnKeyedRead(cmd)
		},
		Human:         renderAttachmentList,
		RetainOnError: func(out envelope.IssueAttachmentListOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueAttachmentListOutput, error) {
			return runAttachmentList(ctx, deps, inv.Args, keyed.Parallelism)
		},
	})
}

// runAttachmentList reads every key's attachments and assembles the
// keyed payload.
//
// Pulled out of the command.Spec closure so it is directly testable
// against a fake core.JiraClient, matching runLinkList's own split. It
// returns no site alongside the payload — unlike link list, whose KEY
// column hyperlinks through cli.IssueKey, nothing in this table is a
// browse target: an attachment id has no issue page of its own, and the
// section headers are the keys the caller typed.
func runAttachmentList(ctx context.Context, deps Deps, keys []string, parallelism int) (envelope.IssueAttachmentListOutput, error) {
	var zero envelope.IssueAttachmentListOutput
	client, err := deps.JiraClient()
	if err != nil {
		return zero, err
	}

	outcomes := make([]attachmentOutcome, len(keys))
	// ONE spinner around the whole fan-out, not one per key: a spinner
	// owns the terminal's live region, and several drawing at once
	// interleave into corruption. The label counts keys rather than
	// naming them, because it must not change per key while the
	// animation is up.
	_, err = command.Spin(ctx, attachmentSpinnerLabel(keys), func(ctx context.Context) (struct{}, error) {
		// Every key runs regardless of another's failure — that is what
		// makes a bad key a partial result rather than a whole-command
		// one — so the closure returns no error and each outcome is
		// recorded in place instead. Parallel writes outcomes[i] from the
		// goroutine handling key i, which needs no lock: x/sync.Parallel
		// hands each call a distinct index, and the slice was sized up
		// front. Order therefore comes from the ARGV, never from which
		// read finished first, which is the ordering guarantee
		// envelope.KeyedResults publishes.
		xsync.Parallel(parallelism, len(keys), func(i int) {
			outcomes[i] = readAttachments(ctx, client, keys[i])
		})
		return struct{}{}, nil
	})
	// The published verdicts and the raw errors behind them, split apart
	// for command.KeyedFailure — which owns the partial-vs-total rule for
	// every keyed command, not just this one.
	results := make([]envelope.KeyedResult[envelope.IssueAttachmentListData], len(outcomes))
	raw := make([]error, len(outcomes))
	for i, o := range outcomes {
		results[i], raw[i] = o.result, o.err
	}
	out := envelope.IssueAttachmentListOutput{}
	out.Results = results
	if err != nil {
		// Spin joins producers before publication. Preserve their observations
		// while interruption remains the whole-run classification.
		return out, err
	}
	return out, command.KeyedFailure(results, raw)
}

// attachmentOutcome is one key's result plus the raw error behind a
// failure — the same pairing keyOutcome makes for links, and for the
// reason its doc comment gives: the envelope's KeyError is the right
// shape to PUBLISH per key and the wrong thing to fail the command with,
// so the original error travels alongside for command.KeyedFailure to
// hand back untouched.
type attachmentOutcome struct {
	result envelope.KeyedResult[envelope.IssueAttachmentListData]
	// err is nil exactly when result.OK.
	err error
}

// attachmentSpinnerLabel is the fan-out's one spinner label. It says how
// many issues are being read rather than which, because the label is
// fixed for the life of the animation while the reads are not.
func attachmentSpinnerLabel(keys []string) string {
	if len(keys) == 1 {
		return "Loading attachments for " + keys[0]
	}
	return fmt.Sprintf("Loading attachments for %d issues", len(keys))
}

// readAttachments is one key's whole outcome: its attachments, or why it
// has none to report. It returns a result rather than an error precisely
// because a failure here is data — the entry the caller reads at
// data.results[] — and not the command's own failure.
func readAttachments(ctx context.Context, client core.JiraClient, key string) attachmentOutcome {
	read, err := client.GetIssue(ctx, key, attachmentFields)
	if read.Issue == nil && err != nil {
		return failedAttachmentResult(key, err)
	}
	if invalid := core.ValidateAttachmentIssue(read.Issue); invalid != nil {
		return failedAttachmentResult(key, fmt.Errorf("attachment list for %q: %w", key, errors.Join(err, invalid)))
	}
	data := envelope.IssueAttachmentListData{
		Attachments: attachmentViews(val(read.Issue.Fields).Attachment.Value),
	}
	outcome := attachmentOutcome{result: envelope.KeyedResult[envelope.IssueAttachmentListData]{
		Issue: envelope.IssueRef{
			ID:   read.Issue.ID.Value,
			Key:  read.Issue.Key.Value,
			Self: read.Issue.Self.Value,
		},
		OK:   true,
		Data: &data,
	}}
	if err != nil {
		failure := failedAttachmentResult(key, err)
		outcome.result.OK = false
		outcome.result.Err = failure.result.Err
		outcome.err = err
	}
	return outcome
}

// failedAttachmentResult records one key's failure, resolved through the
// SAME taxonomy the envelope's own errors[] entries use (cli.MapError),
// so a caller branches on one vocabulary of codes wherever a failure
// appears.
//
// The identity carries only the key: a key that could not be read
// resolved no id or self, and IssueRef omits both rather than emitting
// them empty.
func failedAttachmentResult(key string, err error) attachmentOutcome {
	class := cli.MapError(err)
	return attachmentOutcome{
		result: envelope.KeyedResult[envelope.IssueAttachmentListData]{
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

// attachmentViews narrows one issue's wire attachments to their
// CLI-shaped view. Unlike linkViews there is no question to answer at
// this boundary — an attachment means the same thing from either end —
// so this is a straight field-by-field narrowing, and the only judgment
// in it is which wire fields the contract publishes at all (see
// envelope.AttachmentView, which drops `thumbnail`).
func attachmentViews(attachments []jira.Attachment) []envelope.AttachmentView {
	// Never nil: an issue with no attachments publishes an empty array,
	// so a caller iterating data.results[].data.attachments needs no null
	// check.
	out := make([]envelope.AttachmentView, 0, len(attachments))
	for _, a := range attachments {
		author := val(a.Author)
		out = append(out, envelope.AttachmentView{
			ID:       a.ID.Value,
			Filename: a.Filename.Value,
			MimeType: a.MimeType.Value,
			Size:     a.Size.Value,
			Created:  a.Created.Value,
			Author: envelope.AuthorRef{
				AccountID:   author.AccountID.Value,
				DisplayName: author.DisplayName.Value,
			},
			Content: a.Content.Value,
		})
	}
	return out
}

// renderAttachmentList writes the human view: one section per key, in
// the order the caller gave them, exactly as renderLinkList does and for
// the same reason — a keyed read's whole shape is per-key, and a single
// merged table would lose which issue each file belongs to. A key with
// no attachments gets one plain line rather than an empty table, and a
// FAILED key renders its message and hint in place: the command
// succeeded, so nothing else would ever tell a person that one of the
// keys they asked about did not answer.
func renderAttachmentList(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueAttachmentListOutput)
	if !ok {
		return fmt.Errorf("issue.attachment.list: human renderer received %T, want envelope.IssueAttachmentListOutput", env.Data)
	}
	for i, r := range out.Results {
		if i > 0 {
			v.Line()
		}
		// cli.Text, not a styled sink: Section hands its title to clog's
		// Divider, which bolds whatever it is given, and a pre-styled
		// title would strand its own reset mid-span and drop the bold
		// from everything after it (axis H5 — see cli.EntityAs's own doc
		// comment for the byte-verified version of that bug).
		v.Section(cli.Text(r.Issue.Key))
		renderAttachmentResult(v, r)
	}
	return nil
}

// renderAttachmentResult draws one key's section body: its failure, its
// emptiness, or its attachments.
func renderAttachmentResult(v *output.View, r envelope.KeyedResult[envelope.IssueAttachmentListData]) {
	if !r.OK {
		// The message is Jira-authored in the common case (its own 404
		// prose rides through the taxonomy), so it goes through cli.Text
		// like every other Jira string. The hint is CLI-authored and
		// static, and rides the message path so the flags it names render
		// in clog's inline-code style — the same split renderLinkResult
		// already makes.
		v.Line(cli.Text(r.Err.Message))
		v.Hint(r.Err.Hint)
		if r.Data == nil || len(r.Data.Attachments) == 0 {
			return
		}
	}
	if len(r.Data.Attachments) == 0 {
		v.Line(cli.Text("No attachments."))
		return
	}
	output.Table(v, cli.ResolvedTheme(), issueAttachmentColumns(), r.Data.Attachments)
}
