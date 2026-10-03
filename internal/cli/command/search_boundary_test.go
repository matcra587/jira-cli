package command

import (
	"context"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type incompleteSearchClient struct {
	core.JiraClient
	page  *jira.SearchResults
	calls int
}

func (c *incompleteSearchClient) SearchIssues(context.Context, string, []string, string, int, ...string) (core.IssueSearchRead, error) {
	c.calls++
	return core.IssueSearchRead{Results: c.page}, nil
}

func TestSearchKeysRequiresExplicitCompletionBeforeResolving(t *testing.T) {
	t.Parallel()
	page := &jira.SearchResults{IsLast: jira.NullableOf(true), Issues: jira.NullableOf([]jira.IssueBean{{Key: jira.NullableOf("KEY-1")}})}
	page.IsLast.Set = false
	client := &incompleteSearchClient{page: page}
	keys, err := searchKeys(t.Context(), client, "project = KEY", FlagJQL)
	if err == nil || cli.MapError(err).Code != cli.CodeSelectorIncomplete || keys != nil || client.calls != 1 {
		t.Fatalf("missing completion resolved a target: keys=%v calls=%d err=%v", keys, client.calls, err)
	}
}
