package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestRateLimitWarningRootSeparatesInvocations(t *testing.T) {
	parent := core.WithRateLimitObservation(t.Context())
	core.ObserveRateLimitNear(parent)
	for _, signal := range []bool{true, false} {
		var stdout bytes.Buffer
		program, sink := newProgram(testDeps(t), &stdout, &bytes.Buffer{})
		program.Root.AddCommand(command.MustNew(command.Spec[envelope.VersionOutput]{
			Op: "version", Use: "rateprobe", Short: "Observe invocation-local transport facts",
			Run: func(ctx context.Context, _ command.Invocation) (envelope.VersionOutput, error) {
				if signal {
					core.ObserveRateLimitNear(ctx)
				}
				return envelope.VersionOutput{Version: "fixture"}, nil
			},
		}))
		if exit := execute(parent, program, sink, []string{"rateprobe", "--output=json"}); exit != 0 {
			t.Fatalf("invocation refused: exit=%d", exit)
		}
		var frame struct {
			Warnings []envelope.Warning `json:"warnings"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
			t.Fatal(err)
		}
		want := 0
		if signal {
			want = 1
		}
		if len(frame.Warnings) != want {
			t.Fatalf("invocation reused another collector: %s", stdout.String())
		}
	}
	if !core.TakeRateLimitNear(parent) {
		t.Fatal("a child invocation consumed its caller's observation")
	}
}

func TestRateLimitWarningSurvivesLateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(core.WithRateLimitObservation(t.Context()))
	core.ObserveRateLimitNear(ctx)
	cancel()
	cmd := &cobra.Command{Use: "version"}
	cmd.SetContext(ctx)
	var stdout bytes.Buffer
	sink := &failureSink{stdout: &stdout, format: output.FormatJSON, resolved: true, command: cmd}
	if exit := sink.exitCodeContext(t.Context(), errors.Join(context.Canceled, errors.New("later cancellation"))); exit != cli.ExitCanceled {
		t.Fatalf("cancellation classification changed: %d", exit)
	}
	var frame struct {
		Warnings []envelope.Warning `json:"warnings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
		t.Fatal(err)
	}
	if len(frame.Warnings) != 1 || frame.Warnings[0].Type != "rate_limit_near" || core.TakeRateLimitNear(ctx) {
		t.Fatalf("late cancellation lost or duplicated its completed observation: %s", stdout.String())
	}
}
