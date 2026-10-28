package issue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// ScreenPrepareCreate authors standard fields without submitting them. New
// Markdown must convert without content loss; native identity/metadata stay owned.
func ScreenPrepareCreate(ctx context.Context, client core.JiraClient, input core.IssueCreationInput) (jira.IssueWriteRequest, error) {
	var req jira.IssueWriteRequest
	if err := ctx.Err(); err != nil {
		return req, err
	}
	if strings.TrimSpace(input.ProjectID) == "" || strings.TrimSpace(input.IssueTypeID) == "" || strings.TrimSpace(input.Summary) == "" {
		return req, cli.NewInputError("validation_failed", "project, issue type and summary are required")
	}
	for _, field := range []struct {
		name  string
		value any
	}{{"project", map[string]string{"id": input.ProjectID}}, {"issuetype", map[string]string{"id": input.IssueTypeID}}, {"summary", input.Summary}} {
		if err := req.SetField(field.name, field.value); err != nil {
			return req, err
		}
	}
	if input.Labels.Set {
		labels := input.Labels.Value
		if input.Labels.Null {
			return req, cli.NewInputError("validation_failed", "labels must be an explicit array")
		}
		if labels == nil {
			labels = []string{}
		}
		if err := req.SetField("labels", labels); err != nil {
			return req, err
		}
	}
	if input.Assignee.Set {
		var value any
		if !input.Assignee.Null {
			account, err := core.ResolveActiveAccount(ctx, client, input.Assignee.Value)
			if err != nil {
				return req, err
			}
			value = map[string]string{"accountId": account}
		}
		if err := req.SetField("assignee", value); err != nil {
			return req, err
		}
	}
	if input.Markdown != "" {
		body, err := command.ConvertMarkdown(command.FlagMarkdown, input.Markdown)
		if err != nil {
			return req, err
		}
		if len(body.Lossy) > 0 {
			return req, cli.NewInputError("validation_failed", fmt.Sprintf("description conversion needs review: %s", body.Lossy[0].Message))
		}
		if err := req.SetField("description", json.RawMessage(body.ADF)); err != nil {
			return req, err
		}
	}
	if _, err := createFieldPreflight(ctx, client, req); err != nil {
		return req, err
	}
	return req, ctx.Err()
}

// ScreenCreate rechecks current field availability and submits the reviewed
// request once. It shares command acknowledgement and partial-outcome handling.
func ScreenCreate(ctx context.Context, client core.JiraClient, request jira.IssueWriteRequest) (envelope.IssueCreateOutput, error) {
	var out envelope.IssueCreateOutput
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := requireCreateFields(request); err != nil {
		return out, err
	}
	preflight, err := createFieldPreflight(ctx, client, request)
	if err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out, err = createIssueOnce(ctx, client, request)
	out.FieldPreflight = preflight
	return out, err
}
