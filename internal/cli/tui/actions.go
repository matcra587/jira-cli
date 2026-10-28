package tui

import (
	"context"
	"encoding/json"
	"fmt"

	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	issuecmd "github.com/matcra587/jira-cli/internal/cli/issue"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/tui/dashboard"
)

func dashboardActions(ctx context.Context, client core.JiraClient, readOnly bool) dashboard.Actions {
	return dashboard.Actions{
		Description: func(key string) (*core.IssueDescriptionSession, error) {
			return issuecmd.ScreenDescription(ctx, client, key)
		},
		ReviewDescription: func(session *core.IssueDescriptionSession, markdown string) (core.IssueDescriptionMerge, error) {
			return issuecmd.ScreenReviewDescription(ctx, client, session, markdown)
		},
		ReplaceDescription: func(session *core.IssueDescriptionSession, markdown string, review core.IssueDescriptionMerge) (core.IssueActionReceipt, string, error) {
			return issuecmd.ScreenReplaceDescription(ctx, client, session, markdown, review)
		},
		ReadOnly: readOnly,
		HierarchyTypes: func() ([]jira.IssueTypeDetails, error) {
			value, err := cachecmd.FetchMetadata(ctx, client, nil, "issuetypes", false)
			if err != nil {
				return nil, err
			}
			types := make([]jira.IssueTypeDetails, 0, len(value.Items))
			for _, raw := range value.Items {
				var native jira.IssueTypeDetails
				if err := json.Unmarshal(raw, &native); err != nil {
					return nil, core.ErrIssueMetadataInvalid
				}
				types = append(types, native)
			}
			return types, nil
		},
		Projects: func() ([]core.IssueCreateProjectRead, bool, error) {
			value, err := cachecmd.FetchMetadata(ctx, client, nil, "projects", false)
			if err != nil {
				return nil, false, err
			}
			projects := make([]core.IssueCreateProjectRead, 0, len(value.Items))
			for _, native := range value.Items {
				project, err := core.DecodeIssueCreateProject(native)
				if err != nil {
					return nil, false, err
				}
				projects = append(projects, project)
			}
			return projects, value.Complete, nil
		},
		Project: func(selector string) (core.IssueCreateProjectRead, error) {
			reader, ok := client.(core.IssueCreateProjectClient)
			if !ok {
				return core.IssueCreateProjectRead{}, core.ErrIssueMetadataUnavailable
			}
			read, err := reader.GetCreateIssueProject(ctx, selector)
			if err != nil {
				return read, err
			}
			return core.DecodeIssueCreateProject(read.Native)
		},
		CreateTypes: func(project string) (map[string]json.RawMessage, error) {
			reader, ok := client.(core.IssueMetadataClient)
			if !ok {
				return nil, core.ErrIssueMetadataUnavailable
			}
			types, _, err := core.DiscoverCreateIssueTypes(ctx, reader, project)
			return types, err
		},
		PrepareCreate: func(input core.IssueCreationInput) (jira.IssueWriteRequest, error) {
			return issuecmd.ScreenPrepareCreate(ctx, client, input)
		},
		Create: func(request jira.IssueWriteRequest) (envelope.IssueCreateOutput, error) {
			return issuecmd.ScreenCreate(ctx, client, request)
		},
		Editable: func(key string) (*jira.IssueBean, error) { return issuecmd.ScreenEditable(ctx, client, key) },
		Browse:   func(key string) (string, error) { return issuecmd.ScreenOpen(ctx, client.BaseURL(), key) },
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
			case "summary", "labels":
				return issuecmd.ScreenEditField(ctx, client, key, kind, value)
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
