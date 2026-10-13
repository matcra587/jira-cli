package issue

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestIssueEditPrepareRefusesMissingBoundReadCapability(t *testing.T) {
	t.Parallel()
	constructors := 0
	cmd := newEditPrepare(Deps{JiraClient: func(context.Context) (core.JiraClient, error) {
		constructors++
		return &unusedJiraClient{}, nil
	}})
	cmd.SetArgs([]string{"KEY-1"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	err := cmd.ExecuteContext(t.Context())
	if !errors.Is(err, core.ErrIssueDescriptionUnavailable) || cli.MapError(err).Code != cli.CodeIssueDescriptionUnavailable || constructors != 1 {
		t.Fatalf("missing capability became a snapshot-scope error: %v; constructors=%d", err, constructors)
	}
}
