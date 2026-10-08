package issue

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// newMoveFields preserves the historical confirmed issue-field PUT workflow.
// Jira determines which fields the edit endpoint can change; this route does
// not promise project migration or submit a native bulk job.
func newMoveFields(deps Deps) *cobra.Command {
	var in issueWriteFlags
	var keyed command.KeyedFlags
	var mutation command.MutationFlags
	return command.MustNew(command.Spec[envelope.IssueEditOutput]{
		Op: "issue.move.fields", WritesJira: true, Identity: true, Keyed: true, Passthrough: true,
		Use: "fields ISSUE_KEY...", Short: "Apply a confirmed issue-field move payload",
		Long: "Apply a native fields/update issue payload to explicit issues using the historical move field-write workflow. " +
			"This sends an issue-edit PUT for each key. Jira decides whether the requested project, type or other fields can change through that endpoint. " +
			"For actual project/type migration through a bulk job, use `jira issue move` with targetToSourcesMapping instead.\n\n" +
			"Read --json-input from a file or - for standard input. Keys accept comma lists and bounded ranges. " +
			"A local --dry-run composes the exact payload without credentials, network access or prompts. " +
			"Live writes require confirmation; scripts and agents must pass --force. " +
			"Fields, native ADF, update operations and unknown request members are preserved. " +
			"Writes are never automatically replayed. Ordered per-key receipts retain successful writes beside failures; " +
			"a mixed result exits successfully, while total failure returns the relevant error. " +
			"A successful PUT confirms the field write was acknowledged, not a project migration.",
		Example: "$ echo '{\"fields\":{\"issuetype\":{\"id\":\"10001\"}}}' | jira issue move fields KEY-1 --json-input - --dry-run --output=json\n\n" +
			"$ jira issue move fields KEY-1 KEY-2 --json-input fields.json --force --output=json",
		Args: command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(cmd *cobra.Command) {
			in.cmd = cmd
			command.AddKeyedFlags(cmd, &keyed)
			command.AddMutationFlags(cmd, &mutation, command.WithForce())
			command.AddStringVar(cmd, &in.jsonInput, command.FlagJSONInput, "", "Read a fields/update issue payload from a file; - reads standard input", command.FlagOpts{Terse: "Native field payload", Placeholder: "path", Hint: "file"})
			command.AddHiddenAliases(cmd, command.ADFRouteAliases())
		},
		Validate: func() error {
			if err := keyed.Validate(); err != nil {
				return err
			}
			if err := resolvePayloadAliases(in.cmd.Flags()); err != nil {
				return err
			}
			if in.jsonInput == "" {
				err := cli.NewInputError(cli.CodeFlagRequired, "--json-input is required; provide a native fields/update issue payload")
				err.Field = command.FlagJSONInput
				return err
			}
			return refuseWebOnKeyedWrite(in.cmd)
		},
		Human:         renderEdit,
		RetainOnError: func(out envelope.IssueEditOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueEditOutput, error) {
			keys, err := command.ParseIssueKeys(inv.Args)
			if err != nil {
				return envelope.IssueEditOutput{}, err
			}
			inv.Args = keys
			return runEdit(ctx, deps, inv, &in, editOptions{parallelism: keyed.Parallelism, notify: true, force: mutation.Force, fieldMove: true})
		},
	})
}

func confirmFieldMove(ctx context.Context, deps Deps, inv command.Invocation, keys []string, force bool) error {
	subject := joinPlainKeys(keys)
	err := confirmDestructive(ctx, deps.Confirm, inv, force, destructiveQuestion{
		Subject: subject, Risk: "applying a move field payload to " + subject + " needs confirmation",
		Question:    []string{"Apply the move field payload to ", subject, "?"},
		Caveat:      "Jira decides which fields can change through the issue-edit endpoint. Review the payload before continuing.",
		Affirmative: "Apply", Negative: "Keep",
	})
	if errors.Is(err, errDestructiveAborted) {
		return cli.NewCodedError(cli.CodeCanceled, "field move canceled before submission")
	}
	return err
}
