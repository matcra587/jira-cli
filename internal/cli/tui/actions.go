package tui

import (
	"context"
	"fmt"

	issuecmd "github.com/matcra587/jira-cli/internal/cli/issue"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/tui/dashboard"
)

func dashboardActions(ctx context.Context, client core.JiraClient, readOnly bool) dashboard.Actions {
	return dashboard.Actions{
		ReadOnly: readOnly,
		Transitions: func(key string) ([]jira.IssueTransition, error) {
			read, err := client.GetTransitions(ctx, key)
			if err != nil {
				return nil, err
			}
			if err := core.ValidateTransitions(read.Transitions); err != nil {
				return nil, err
			}
			return read.Transitions.Transitions.Value, nil
		},
		Users: func(query string) ([]*jira.User, error) {
			read, err := client.SearchUsers(ctx, query, 0, 50)
			if err != nil {
				return nil, err
			}
			return core.ActiveUserSearchPage(read.Users, 50, make(map[string]struct{}))
		},
		CurrentUser: func() (string, error) {
			read, err := client.CurrentUser(ctx)
			if err != nil {
				return "", err
			}
			if err := core.RequireCurrentUserIdentity(read.User); err != nil {
				return "", err
			}
			return read.User.AccountID.Value, nil
		},
		Comments: func(key string, start int) (*jira.CommentPage, error) {
			read, err := client.GetComments(ctx, key, start, 20, core.CommentOrderNewestFirst)
			if err != nil {
				return nil, err
			}
			if err := core.ValidateCommentPage(read.Page, start, 20); err != nil {
				return nil, err
			}
			return read.Page, nil
		},
		Write: func(kind, key, value string) (core.IssueActionReceipt, error) {
			switch kind {
			case "transition":
				return issuecmd.ScreenTransition(ctx, client, key, value)
			case "assign":
				return issuecmd.ScreenAssign(ctx, client, key, value)
			case "comment":
				return issuecmd.ScreenComment(ctx, client, key, value)
			default:
				return core.IssueActionReceipt{}, fmt.Errorf("unknown issue action %q", kind)
			}
		},
	}
}
