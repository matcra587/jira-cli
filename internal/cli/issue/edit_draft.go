package issue

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/issueedit"
	"github.com/matcra587/jira-cli/internal/output"
)

const flagDraftDirectory = "directory"

func draftFlags(c *cobra.Command, directory *string, mutation bool) {
	command.AddStringVar(c, directory, flagDraftDirectory, "", "Use this explicit draft directory; creation requires an unused name and an existing parent", command.FlagOpts{Terse: "Draft directory", Placeholder: "DIR", Hint: "dir"})
	if mutation {
		command.AddDryRunFlag(c)
	}
}

func validateDraftFlags(c *cobra.Command, directory string) error {
	if isWeb(c) {
		err := cli.NewInputError(cli.CodeFlagNotApplicable, "--web cannot prepare or inspect local editing artifacts")
		err.Field = flagWeb
		return err
	}
	if directory == "" {
		code := cli.CodeFlagRequired
		if c.Flags().Changed(flagDraftDirectory) {
			code = cli.CodeFlagValueInvalid
		}
		err := cli.NewInputError(code, "--directory requires a literal filesystem path")
		err.Field = flagDraftDirectory
		return err
	}
	return nil
}

func newEditDraft(deps Deps) *cobra.Command {
	var directory string
	var edit bool
	var cmd *cobra.Command
	return command.MustNew(command.Spec[envelope.IssueEditDraftOutput]{
		Op: "issue.edit.draft", MutatesLocalState: true, Passthrough: true,
		Use: "draft ISSUE_KEY", Short: "Prepare a recoverable native description draft",
		Long: "Read the native issue description and publish a recoverable editing directory.\n\n" +
			"`--directory` must name an unused directory under an existing parent. The original " +
			"bound native backup is saved at `source.json`, anchored text at `description.md`, " +
			"and readiness at `manifest.json`. Native source and manifest are read-only where " +
			"supported. No existing name is replaced, and partial creation is retained. " +
			"Directory-sync support varies by platform; inspect the actual durability receipt.\n\n" +
			"`--edit` opens only the Markdown through the configured shared editor. " +
			"Failures and cancellation retain source and authored work. Headless, agent and " +
			"`--no-input` invocations refuse editor launch before reading Jira or creating files. " +
			"Without `--edit`, preparation needs no terminal. `--dry-run` opens no client and " +
			"creates nothing; with `--edit` it validates the selected executable without launching it.\n\n" +
			"Use `jira issue edit inspect --directory DIR` for a local candidate, add `--refresh` " +
			"for a fresh native read/merge, or use `jira issue edit resume --directory DIR` to " +
			"continue editing. These commands work in read-only mode and never write Jira. " +
			"Source scope, a timestamp, readiness and a merge are not server-enforced lost-update protection.",
		Example: `$ jira issue edit draft KEY-123 --directory ./description-draft

# Open the prepared Markdown after publishing its native backup
$ jira issue edit draft KEY-123 --directory ./description-draft --edit

# Preview the destination without Jira or file publication
$ jira issue edit draft KEY-123 --directory ./description-draft --dry-run`,
		Args: command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			draftFlags(c, &directory, true)
			command.AddBoolVar(c, &edit, "edit", false, "Open the saved anchored Markdown in the configured editor", command.FlagOpts{Terse: "Open editor"})
		},
		Validate:      func() error { return validateDraftFlags(cmd, directory) },
		Human:         renderIssueDraft,
		RetainOnError: func(out envelope.IssueEditDraftOutput) bool { return out.Draft.Directory != "" || out.Source != nil },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueEditDraftOutput, error) {
			return runEditDraft(ctx, deps, inv, directory, edit)
		},
	})
}

func newEditInspect(deps Deps) *cobra.Command {
	var directory string
	var refresh bool
	var cmd *cobra.Command
	return command.MustNew(command.Spec[envelope.IssueEditInspectOutput]{
		Op: "issue.edit.inspect", Passthrough: true, Use: "inspect", Short: "Inspect or reconcile a saved description draft",
		Long: "Reopen the ready native backup and apply saved anchored Markdown locally.\n\n" +
			"No configuration, credentials or Jira client are needed for ordinary inspection. " +
			"Changed compares candidate bytes with the original source. Invalid, changed or " +
			"redirected artifacts refuse without replacing files. Protected regions remain " +
			"unchanged and present once; failed validation retains saved Markdown for repair.\n\n" +
			"`--refresh` first validates the entire local edit, then constructs one client " +
			"with the original site/account/API scope and reads by immutable issue ID. " +
			"Nonconflicting remote changes are retained; conflicts return no candidate. " +
			"The source backup remains unchanged. No candidate, fresh read or timestamp " +
			"authorizes a Jira write or prevents a later writer.",
		Example: `$ jira issue edit inspect --directory ./description-draft --output=json

# Reconcile saved text against current native Jira state
$ jira issue edit inspect --directory ./description-draft --refresh`,
		Flags: func(c *cobra.Command) {
			cmd = c
			draftFlags(c, &directory, false)
			command.AddBoolVar(c, &refresh, "refresh", false, "Read current native issue state and merge; never publish files or Jira changes", command.FlagOpts{Terse: "Fresh native merge"})
		},
		Validate:      func() error { return validateDraftFlags(cmd, directory) },
		Human:         renderIssueDraft,
		RetainOnError: func(out envelope.IssueEditInspectOutput) bool { return out.Source != nil },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueEditInspectOutput, error) {
			return runEditInspect(ctx, deps, directory, refresh)
		},
	})
}

func newEditResume(deps Deps) *cobra.Command {
	var directory string
	var cmd *cobra.Command
	return command.MustNew(command.Spec[envelope.IssueEditDraftOutput]{
		Op: "issue.edit.resume", MutatesLocalState: true, Passthrough: true,
		Use: "resume", Short: "Resume a saved native description draft in your editor",
		Long: "Open the draft's saved anchored Markdown in the shared configured editor.\n\n" +
			"The original backup and readiness must still be valid; invalid authored text may " +
			"be repaired. The original files are checked again after the editor exits. " +
			"Failed or canceled editors retain the native backup and saved work, and " +
			"the typed failure receipt reports recovery paths and actual attempted effects. " +
			"Editor cancellation does not prove that no Markdown was saved.\n\n" +
			"A terminal is required, and agents or `--no-input` refuse before launch. " +
			"`--dry-run` validates the draft and chosen executable, needs no terminal and " +
			"changes nothing. The editor selection uses JIRA_EDITOR, stored editor, VISUAL, " +
			"then EDITOR; argv is literal and there is no fallback executable. " +
			"This local operation never opens Jira credentials or writes Jira. Use " +
			"`jira issue edit inspect --directory DIR --refresh` for a fresh read/merge afterward.",
		Example: `$ jira issue edit resume --directory ./description-draft

# Check recovery and editor selection without launching anything
$ jira issue edit resume --directory ./description-draft --dry-run`,
		Flags:         func(c *cobra.Command) { cmd = c; draftFlags(c, &directory, true) },
		Validate:      func() error { return validateDraftFlags(cmd, directory) },
		Human:         renderIssueDraft,
		RetainOnError: func(out envelope.IssueEditDraftOutput) bool { return out.Source != nil },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueEditDraftOutput, error) {
			return runEditResume(ctx, deps, inv, directory)
		},
	})
}

func selectedDraftEditor(ctx context.Context, deps Deps, inv command.Invocation) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !inv.DryRun && !inv.Interactive {
		return nil, cli.NewInputError(cli.CodeTerminalRequired, "an editor requires interactive terminal input; inspect the saved draft or use --dry-run")
	}
	if deps.EditorCommand == nil || deps.EditFile == nil {
		return nil, cli.NewInputError(cli.CodeEditorMissing, "no shared editor capability configured")
	}
	editor, err := deps.EditorCommand(ctx)
	if err != nil {
		return nil, err
	}
	if len(editor) == 0 || editor[0] == "" {
		return nil, cli.NewInputError(cli.CodeEditorMissing, "no editor configured")
	}
	return editor, ctx.Err()
}

func descriptionReader(ctx context.Context, deps Deps) (core.IssueDescriptionClient, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if deps.JiraClient == nil {
		return nil, core.ErrIssueDescriptionUnavailable
	}
	client, err := deps.JiraClient(ctx)
	if err != nil {
		return nil, err
	}
	reader, ok := client.(core.IssueDescriptionClient)
	if !ok {
		return nil, core.ErrIssueDescriptionUnavailable
	}
	return reader, ctx.Err()
}

func runEditDraft(ctx context.Context, deps Deps, inv command.Invocation, directory string, edit bool) (out envelope.IssueEditDraftOutput, err error) {
	out.DryRun = inv.DryRun
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.RequestedIssue = inv.Args[0]
	if edit {
		out.Editor, err = selectedDraftEditor(ctx, deps, inv)
		if err != nil {
			return out, err
		}
	}
	target, err := issueedit.PrepareTarget(ctx, directory)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, target.Close()) }()
	out.Draft = target.State()
	if inv.DryRun {
		return out, ctx.Err()
	}
	reader, err := descriptionReader(ctx, deps)
	if err != nil {
		return out, err
	}
	session, err := command.Spin(ctx, "Preparing recoverable description", func(taskCtx context.Context) (*core.IssueDescriptionSession, error) {
		session, _, err := core.PrepareIssueDescription(taskCtx, reader, inv.Args[0])
		return session, err
	})
	if err != nil {
		return out, err
	}
	snapshot := session.Snapshot()
	out.Source = &snapshot
	out.Draft, err = target.Create(ctx, snapshot)
	if err != nil {
		return out, err
	}
	draft, err := issueedit.Open(ctx, out.Draft.Directory)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, draft.Close()) }()
	if edit {
		out.Draft.EditorAttempted = true
		if err := deps.EditFile(ctx, out.Editor, out.Draft.MarkdownFile); err != nil {
			return out, err
		}
		out.Draft.EditorSucceeded = true
	}
	out.Description, err = draft.Candidate(ctx)
	if err == nil {
		changed := !bytes.Equal(out.Description, snapshot.DescriptionBytes)
		out.Changed = &changed
	}
	return out, err
}

func runEditInspect(ctx context.Context, deps Deps, directory string, refresh bool) (out envelope.IssueEditInspectOutput, err error) {
	draft, err := issueedit.Open(ctx, directory)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, draft.Close()) }()
	out.Draft = draft.State()
	snapshot := draft.Snapshot()
	out.Source = &snapshot
	local, err := draft.Candidate(ctx)
	if err != nil {
		return out, err
	}
	if !refresh {
		out.Description = local
		changed := !bytes.Equal(local, snapshot.DescriptionBytes)
		out.Changed = &changed
		return out, nil
	}
	reader, err := descriptionReader(ctx, deps)
	if err != nil {
		return out, err
	}
	merged, err := command.Spin(ctx, "Reconciling native description", func(taskCtx context.Context) (core.IssueDescriptionMerge, error) {
		return draft.Session().Merge(taskCtx, reader, draft.Markdown())
	})
	if err != nil {
		return out, err
	}
	out.Current, out.Description, out.Changed, out.Refreshed = &merged.Current, merged.Description, &merged.Changed, true
	return out, nil
}

func runEditResume(ctx context.Context, deps Deps, inv command.Invocation, directory string) (out envelope.IssueEditDraftOutput, err error) {
	out.DryRun = inv.DryRun
	out.Editor, err = selectedDraftEditor(ctx, deps, inv)
	if err != nil {
		return out, err
	}
	draft, err := issueedit.Open(ctx, directory)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, draft.Close()) }()
	out.Draft = draft.State()
	snapshot := draft.Snapshot()
	out.Source = &snapshot
	if inv.DryRun {
		return out, ctx.Err()
	}
	out.Draft.EditorAttempted = true
	if err := deps.EditFile(ctx, out.Editor, out.Draft.MarkdownFile); err != nil {
		return out, err
	}
	out.Draft.EditorSucceeded = true
	out.Description, err = draft.Candidate(ctx)
	if err == nil {
		changed := !bytes.Equal(out.Description, snapshot.DescriptionBytes)
		out.Changed = &changed
	}
	return out, err
}

func renderIssueDraft(view *output.View, env envelope.Envelope) error {
	var result envelope.IssueEditDraftResult
	preview := false
	requested := ""
	switch out := env.Data.(type) {
	case envelope.IssueEditDraftOutput:
		result, preview = out.IssueEditDraftResult, out.DryRun
		requested = out.RequestedIssue
	case envelope.IssueEditInspectOutput:
		result = out.IssueEditDraftResult
	default:
		return fmt.Errorf("issue editing draft: unexpected payload %T", env.Data)
	}
	view.Fields(output.Field{Label: "Draft", Value: cli.Text(result.Draft.Directory)}, output.Field{Label: "Native backup", Value: cli.Text(result.Draft.SourceFile)}, output.Field{Label: "Markdown", Value: cli.Text(result.Draft.MarkdownFile)})
	if result.Source != nil {
		view.Fields(output.Field{Label: "Issue", Value: cli.Entity(result.Source.IssueKey)})
	} else if requested != "" {
		view.Fields(output.Field{Label: "Requested issue", Value: cli.Entity(requested)})
	}
	if preview {
		view.Hint("Preview only; no editor, artifacts or Jira changes.")
	} else {
		view.Hint("Retain the backup and saved Markdown. This candidate has not changed Jira.")
	}
	return nil
}
