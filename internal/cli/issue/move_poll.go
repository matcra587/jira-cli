package issue

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	adapter "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

type bulkPollFlags struct {
	wait     bool
	max      int
	interval time.Duration
	cmd      *cobra.Command
}

func (f *bulkPollFlags) register(cmd *cobra.Command) {
	f.cmd = cmd
	command.AddBoolVar(cmd, &f.wait, "wait", false, "Observe the task until terminal, canceled or the polling bound is reached", command.FlagOpts{Terse: "Wait for task"})
	command.AddIntVar(cmd, &f.max, "max-polls", 60, "Maximum progress observations when --wait is set (1-1000)", command.FlagOpts{Terse: "Progress read bound", Placeholder: "n"})
	command.AddDurationVar(cmd, &f.interval, "poll-interval", time.Second, "Delay between progress observations with --wait (1ms-1m)", command.FlagOpts{Terse: "Progress read delay", Placeholder: "duration"})
}

func (f bulkPollFlags) validate() error {
	if f.max < 1 || f.max > 1000 {
		err := cli.NewInputError(cli.CodeFlagValueInvalid, "--max-polls must be between 1 and 1000")
		err.Field = "max-polls"
		return err
	}
	if f.interval < time.Millisecond || f.interval > time.Minute {
		err := cli.NewInputError(cli.CodeFlagValueInvalid, "--poll-interval must be between 1ms and 1m")
		err.Field = "poll-interval"
		return err
	}
	if !f.wait {
		for _, name := range []string{"max-polls", "poll-interval"} {
			if f.cmd.Flags().Changed(name) {
				err := cli.NewInputError(cli.CodeFlagNotApplicable, "--"+name+" requires --wait")
				err.Field = name
				return err
			}
		}
	}
	return nil
}

// pollBulk only observes a task. It never submits a move, and publishes each
// validated observation before any later cancellation, delay or request fails.
func pollBulk(ctx context.Context, client core.JiraClient, state *envelope.BulkTaskState, flags bulkPollFlags) error {
	limit := 1
	if flags.wait {
		limit = flags.max
	}
	for i := 0; i < limit; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i > 0 {
			timer := time.NewTimer(flags.interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		state.Polls++
		read, err := client.GetBulkOperationProgress(ctx, state.Task.ID)
		if err != nil {
			return err
		}
		if read.Progress == nil {
			return cli.NewCodedError(cli.CodeBulkOperationResponseInvalid, "bulk progress returned no observation")
		}
		if err := read.Progress.ValidateTask(state.Task.ID); err != nil {
			return cli.NewCodedError(cli.CodeBulkOperationResponseInvalid, err.Error())
		}
		state.Progress = read.Progress
		state.Terminal = read.Progress.Terminal()
		if state.Terminal {
			return nil
		}
	}
	state.PollLimitReached = flags.wait
	return nil
}

func newMoveStatus(deps Deps) *cobra.Command {
	var flags bulkPollFlags
	return command.MustNew(command.Spec[envelope.IssueMoveStatusOutput]{
		Op: "issue.move.status", Identity: true, Passthrough: true,
		Use: "status TASK_ID", Short: "Observe a submitted move task",
		Long:    "Read an existing bulk task without submitting or repeating a move. Without --wait, read one observation. With --wait, stop at a terminal state, the explicit polling bound or cancellation. A running task at 100 percent is still running. COMPLETE can include failed or inaccessible issues; inspect the native outcomes. A valid read succeeds even when the reported job failed. Tasks remain available for a limited server retention period.",
		Example: "$ jira issue move status 10000 --output=json\n\n$ jira issue move status 10000 --wait --max-polls 60 --poll-interval 1s",
		Args:    command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "TASK_ID"}}},
		Flags:   flags.register,
		Validate: func() error {
			if err := flags.validate(); err != nil {
				return err
			}
			if err := adapter.ValidateBulkTaskID(flags.cmd.Flags().Args()[0]); err != nil {
				return cli.NewInputError(cli.CodeArgValueInvalid, err.Error())
			}
			if isWeb(flags.cmd) {
				err := cli.NewInputError(cli.CodeFlagNotApplicable, "--web is unavailable for task progress; omit it to read the native task observation")
				err.Field = flagWeb
				return err
			}
			return nil
		},
		Human:         renderMoveStatus,
		RetainOnError: func(out envelope.IssueMoveStatusOutput) bool { return out.Task != nil },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueMoveStatusOutput, error) {
			out := envelope.IssueMoveStatusOutput{BulkTaskState: envelope.BulkTaskState{Task: &envelope.BulkTaskRef{ID: inv.Args[0]}}}
			if err := ctx.Err(); err != nil {
				return out, err
			}
			client, err := deps.JiraClient(ctx)
			if err != nil {
				return out, err
			}
			err = pollBulk(ctx, client, &out.BulkTaskState, flags)
			return out, err
		},
	})
}

func renderMoveStatus(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueMoveStatusOutput)
	if !ok {
		return fmt.Errorf("issue.move.status: expected IssueMoveStatusOutput, got %T", env.Data)
	}
	renderBulkState(v, out.BulkTaskState)
	return nil
}
