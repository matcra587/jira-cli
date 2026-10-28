package issue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

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
