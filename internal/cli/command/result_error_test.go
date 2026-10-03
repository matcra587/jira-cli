package command

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/spf13/cobra"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestRunRetainsTypedFailureAndOriginalCause(t *testing.T) {
	t.Parallel()
	cause := &adapterjira.APIError{StatusCode: http.StatusTooManyRequests, RetryAfterSeconds: 7, Retry: core.RetryInfo{Attempts: 1, Waited: 2 * time.Millisecond}}
	out := envelope.IssueEditOutput{Payload: json.RawMessage(`{"fields":{"unknown":9007199254740993}}`)}
	out.Results = []envelope.KeyedResult[envelope.IssueEditData]{{Issue: envelope.IssueRef{Key: "KEY-123"}, Err: &envelope.KeyError{Code: "jira_rate_limited", Message: "fixture pause", Hint: "wait"}}}
	cmd, err := New(Spec[envelope.IssueEditOutput]{
		Op: "issue.edit", Use: "probe", Short: "probe", WritesJira: true, Keyed: true,
		Flags: func(c *cobra.Command) {
			var keyed KeyedFlags
			var mutation MutationFlags
			AddKeyedFlags(c, &keyed)
			AddMutationFlags(c, &mutation)
		},
		RetainOnError: func(data envelope.IssueEditOutput) bool { return len(data.Results) > 0 },
		Warnings: func(envelope.IssueEditOutput) []envelope.Warning {
			return []envelope.Warning{{Type: "probe", Message: "retained diagnostic", Lossy: true}}
		},
		Human: func(*output.View, envelope.Envelope) error { return nil },
		Run:   func(context.Context, Invocation) (envelope.IssueEditOutput, error) { return out, cause },
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(t.Context())
	err = cmd.RunE(cmd, nil)
	var retained *cli.ResultError
	var original *adapterjira.APIError
	if !errors.As(err, &retained) || !errors.As(err, &original) || original != cause {
		t.Fatal("construction discarded the typed receipt or rebuilt its original failure")
	}
	data, ok := retained.Data.(envelope.IssueEditOutput)
	if !ok || string(data.Payload) != string(out.Payload) || len(data.Results) != 1 || data.Results[0].Issue.Key != "KEY-123" || retained.Op != "issue.edit" || !retained.Mutation || retained.Human == nil {
		t.Fatal("retained result lost registered data, operation identity or rendering metadata")
	}
	if len(retained.Warnings) != 1 || retained.Warnings[0].Type != "probe" || !retained.Warnings[0].Lossy {
		t.Fatal("shared path discarded typed warnings on a retained failure")
	}
	mapped := cli.MapError(err)
	if mapped.Code != "jira_rate_limited" || mapped.Exit != cli.ExitRateLimit || mapped.RetryAfterSeconds != 7 || mapped.Attempts != 1 || mapped.Waited != 2*time.Millisecond {
		t.Fatal("retaining a result changed the failure taxonomy or transport facts")
	}
}
