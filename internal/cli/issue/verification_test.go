package issue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type canceledVerificationClient struct {
	unusedJiraClient
	cancel context.CancelFunc
	reads  int
}

func (c *canceledVerificationClient) GetIssue(context.Context, string, []string, ...string) (core.IssueRead, error) {
	c.reads++
	c.cancel()
	return core.IssueRead{Issue: &jira.IssueBean{Fields: jira.NullableOf(jira.IssueFields{Summary: jira.NullableOf("wanted")})}}, nil
}

func TestVerificationCanceledReadCannotSupplySuccess(t *testing.T) {
	t.Parallel()
	for _, before := range []bool{true, false} {
		name := "during read"
		if before {
			name = "before read"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := &canceledVerificationClient{cancel: cancel}
			if before {
				cancel()
			}
			verified, err := verifyIssue(ctx, client, "KEY-1", jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"summary": json.RawMessage(`"wanted"`)}})
			wantReads := 1
			if before {
				wantReads = 0
			}
			if verified != nil || !errors.Is(err, context.Canceled) || client.reads != wantReads {
				t.Fatal("canceled read admitted work or claimed successful verification")
			}
		})
	}
}
