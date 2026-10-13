package command

import (
	"context"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestResolveAccountsWithoutLookupBuildsNoClient(t *testing.T) {
	for _, preview := range []bool{false, true} {
		for _, remote := range []bool{false, true} {
			account := "account-123"
			calls := 0
			client, err := ResolveAccounts(t.Context(), func(ctx context.Context) (core.JiraClient, error) {
				calls++
				return nil, errors.New("unexpected credential access")
			}, preview, remote, []AccountField{{Flag: "assignee", Value: &account}})
			if err != nil || client != nil || calls != 0 || account != "account-123" {
				t.Fatalf("unneeded account lookup constructed client: preview=%v remote=%v calls=%d err=%v", preview, remote, calls, err)
			}
		}
	}
}
