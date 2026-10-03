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

func newTransitionList(deps Deps) *cobra.Command {
	var keyed command.KeyedFlags
	var cmd *cobra.Command
	return command.MustNew(command.Spec[envelope.IssueTransitionListOutput]{
		Op: "issue.transition.list", Identity: true, Keyed: true, Passthrough: true,
		Use: "list ISSUE_KEY...", Short: "Discover available workflow transitions",
		Long: "Read the available transitions for each explicit issue, including native transition IDs, destinations, screen state and returned field metadata. This replaces the historical no-target transition listing.\n\n" +
			"The read requests expand=transitions.fields. Machine results retain the complete native response at results[].data, including schemas, allowed values, defaults and unknown extensions. A field absent from returned metadata is not proof that Jira accepts it. Human output marks required fields when Jira supplies that fact.\n\n" +
			"This command only reads and works in read-only mode. Results follow issue order; partial failure does not discard successful reads. Total failure, timeout and interruption retain all joined outcomes. Discovery is a snapshot, not a guarantee that a later transition will succeed. --web is refused; use issue view ISSUE_KEY --web for an issue page.",
		Example: "$ jira issue transition list KEY-123\n\n$ jira issue transition list KEY-123 KEY-124 --parallelism 1 --output=json",
		Args:    command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY"}}},
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
		RetainOnError: func(out envelope.IssueTransitionListOutput) bool { return len(out.Results) > 0 },
		Human:         renderTransitionList,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueTransitionListOutput, error) {
			return runTransitionList(ctx, deps, inv.Args, keyed.Parallelism)
		},
	})
}

type transitionReadOutcome struct {
	result envelope.KeyedResult[jira.Transitions]
	err    error
}

func runTransitionList(ctx context.Context, deps Deps, keys []string, parallelism int) (envelope.IssueTransitionListOutput, error) {
	var client core.JiraClient
	var err error
	if ctx.Err() == nil {
		client, err = deps.JiraClient()
		if err != nil {
			return envelope.IssueTransitionListOutput{}, err
		}
	}
	outcomes := make([]transitionReadOutcome, len(keys))
	_, err = command.Spin(ctx, fmt.Sprintf("Loading transitions for %d issues", len(keys)), func(ctx context.Context) (struct{}, error) {
		xsync.Parallel(parallelism, len(keys), func(i int) {
			outcomes[i] = readTransitionList(ctx, client, keys[i])
		})
		return struct{}{}, nil
	})
	out := envelope.IssueTransitionListOutput{}
	out.Results = make([]envelope.KeyedResult[jira.Transitions], len(keys))
	raw := make([]error, len(keys))
	for i, outcome := range outcomes {
		out.Results[i], raw[i] = outcome.result, outcome.err
	}
	return out, errors.Join(err, command.KeyedFailure(out.Results, raw))
}

func readTransitionList(ctx context.Context, client core.JiraClient, key string) transitionReadOutcome {
	row := envelope.KeyedResult[jira.Transitions]{Issue: envelope.IssueRef{Key: key}}
	err := ctx.Err()
	if err == nil {
		read, readErr := client.GetTransitions(ctx, key)
		valid := core.ValidateTransitions(read.Transitions)
		if valid == nil {
			row.Data = read.Transitions
		}
		err = readErr
		if err == nil {
			err = valid
		}
	}
	if err != nil {
		class := cli.MapError(err)
		row.Err = &envelope.KeyError{Code: class.Code, Message: class.Message, Hint: class.Hint}
	} else {
		row.OK = true
	}
	return transitionReadOutcome{result: row, err: err}
}

func renderTransitionList(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueTransitionListOutput)
	if !ok {
		return fmt.Errorf("issue.transition.list: human renderer received %T", env.Data)
	}
	columns := issueTransitionColumns()
	for i, row := range out.Results {
		if i > 0 {
			v.Line()
		}
		v.Section(cli.Text(row.Issue.Key))
		if !row.OK {
			v.Line(cli.Text(row.Err.Message))
			v.Hint(row.Err.Hint)
		}
		if row.Data == nil {
			continue
		}
		if len(row.Data.Transitions.Value) == 0 {
			v.Line(cli.Text("No available transitions."))
			continue
		}
		output.Table(v, cli.ResolvedTheme(), columns, row.Data.Transitions.Value)
	}
	return nil
}
