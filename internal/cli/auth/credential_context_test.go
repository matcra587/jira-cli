package auth

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/core"
)

type credentialContextKey struct{}

func TestAuthSiteReceivesOperationContext(t *testing.T) {
	t.Parallel()
	marker := &struct{ name string }{name: "auth site"}
	caller, cancel := context.WithDeadline(context.WithValue(t.Context(), credentialContextKey{}, marker), time.Now().Add(time.Minute))
	defer cancel()
	deadline, _ := caller.Deadline()
	calls := 0
	deps := Deps{
		Site: func(ctx context.Context) (SiteInfo, error) {
			calls++
			if ctx.Value(credentialContextKey{}) != marker {
				t.Error("site resolution lost caller context value")
			}
			if observed, ok := ctx.Deadline(); !ok || !observed.Equal(deadline) {
				t.Errorf("site deadline = %v, %t; want caller deadline %v", observed, ok, deadline)
			}
			if err := ctx.Err(); err != nil {
				t.Errorf("site received an already canceled context: %v", err)
			}
			cancel()
			if ctx.Err() != context.Canceled {
				t.Errorf("site did not observe caller cancellation: %v", ctx.Err())
			}
			return SiteInfo{}, context.Canceled
		},
		ClientFor: func(string, string, string, string) (core.JiraClient, error) {
			t.Error("failed site resolution constructed a client")
			return nil, context.Canceled
		},
	}
	cmd := New(deps)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"whoami"})
	if err := cmd.ExecuteContext(caller); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("site path did not run exactly once with cancellation: calls=%d error=%v", calls, err)
	}
}
