package issue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

// ScreenEditable reads current summary and label values with immutable identity.
func ScreenEditable(ctx context.Context, client core.JiraClient, key string) (*jira.IssueBean, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	read, err := client.GetIssue(ctx, key, []string{"summary", "labels"})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	issue := read.Issue
	if issue == nil || !issue.ID.Set || issue.ID.Null || issue.ID.Value == "" || !issue.Key.Set || issue.Key.Null || issue.Key.Value != key || !issue.Fields.Set || issue.Fields.Null {
		return nil, cli.NewCodedError(cli.CodeIssueEditSourceInvalid, "issue response has no complete editing identity and native fields")
	}
	return issue, nil
}

// ScreenEditField keeps literal field values and uses the shared metadata and
// edit owners. The immutable issue ID is the write target; metadata is no CAS.
func ScreenEditField(ctx context.Context, client core.JiraClient, id, field, value string) (core.IssueActionReceipt, error) {
	if err := ctx.Err(); err != nil {
		return core.IssueActionReceipt{}, err
	}
	var native any
	switch field {
	case "summary":
		if strings.TrimSpace(value) == "" {
			return core.IssueActionReceipt{}, cli.NewInputError("validation_failed", "summary must not be blank")
		}
		native = value
	case "labels":
		var labels []string
		if err := json.Unmarshal([]byte(value), &labels); err != nil || labels == nil {
			return core.IssueActionReceipt{}, cli.NewInputError("validation_failed", "labels must be an explicit string array")
		}
		native = labels
	default:
		return core.IssueActionReceipt{}, cli.NewInputError("validation_failed", "unsupported screen edit field")
	}
	encoded, err := jira.MarshalNoEscape(native)
	if err != nil {
		return core.IssueActionReceipt{}, err
	}
	req := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{field: encoded}}
	if _, err := editFieldPreflight(ctx, client, id, req); err != nil {
		return core.IssueActionReceipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return core.IssueActionReceipt{}, err
	}
	outcome := editOne(ctx, client, id, req, false, true)
	return core.IssueActionReceipt{Attempted: true, Applied: outcome.result.OK}, outcome.err
}

// ScreenTransition shares workflow validation and acknowledgement handling with
// the ordinary command. Native mode forces a fresh workflow read, even for IDs.
func ScreenTransition(ctx context.Context, client core.JiraClient, key, id string) (core.IssueActionReceipt, error) {
	outcome := transitionOne(ctx, client, key, transitionPlan{want: id, native: true})
	receipt := core.IssueActionReceipt{}
	if data := outcome.result.Data; data != nil {
		receipt.Attempted, receipt.Applied = data.Attempted, data.Applied
	}
	return receipt, outcome.err
}

// ScreenAssign uses the same exact-account and edit owners as command input.
// An empty account explicitly clears the assignee; names are never guessed.
func ScreenAssign(ctx context.Context, client core.JiraClient, key, account string) (core.IssueActionReceipt, error) {
	if err := ctx.Err(); err != nil {
		return core.IssueActionReceipt{}, err
	}
	value := json.RawMessage("null")
	if account != "" {
		id, err := core.ResolveActiveAccount(ctx, client, account)
		if err != nil {
			return core.IssueActionReceipt{}, err
		}
		encoded, err := jira.MarshalNoEscape(map[string]string{"accountId": id})
		if err != nil {
			return core.IssueActionReceipt{}, err
		}
		value = encoded
	}
	outcome := editOne(ctx, client, key, jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"assignee": value}}, false, true)
	return core.IssueActionReceipt{Attempted: true, Applied: outcome.result.OK}, outcome.err
}

// ScreenComment composes new Markdown through the shared authoring owner and
// validates the same native comment receipt as the command. It never retries.
func ScreenComment(ctx context.Context, client core.JiraClient, key, markdown string) (core.IssueActionReceipt, error) {
	if err := ctx.Err(); err != nil {
		return core.IssueActionReceipt{}, err
	}
	body, err := command.ConvertMarkdown(command.FlagMarkdown, markdown)
	if err != nil {
		return core.IssueActionReceipt{}, err
	}
	if len(body.Lossy) > 0 {
		return core.IssueActionReceipt{}, fmt.Errorf("comment Markdown needs review: %s; use native ADF or the comment command to inspect conversion warnings", body.Lossy[0].Message)
	}
	outcome := postComment(ctx, client, key, jira.AddCommentRequest{Body: body.ADF})
	receipt := core.IssueActionReceipt{Attempted: true, Applied: outcome.result.OK}
	if outcome.result.Data != nil && outcome.result.Data.Comment != nil {
		receipt.CommentID = outcome.result.Data.Comment.ID
	}
	return receipt, outcome.err
}
