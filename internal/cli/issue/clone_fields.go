package issue

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
)

const cloneRankSchema = "com.pyxis.greenhopper.jira:gh-lexo-rank"

// cloneServerField preserves the baseline exclusions for a fresh issue.
// Explicit input overrides are layered after this source-only policy.
func cloneServerField(name string) bool {
	if strings.HasPrefix(name, "aggregate") {
		return true
	}
	switch name {
	case "id", "key", "self", "created", "updated", "creator", "reporter",
		"status", "resolution", "resolutiondate", "statusCategory",
		"statuscategorychangedate", "lastViewed", "issuerestriction",
		"timeestimate", "timespent", "timeoriginalestimate", "workratio",
		"progress", "timetracking", "rankBeforeIssue", "rankAfterIssue",
		"comment", "worklog", "subtasks", "attachment", "votes", "watches",
		"issuelinks", "worklogs":
		return true
	default:
		return false
	}
}

// readCloneFieldSchemas identifies native custom-field types without treating
// create metadata as an exclusion whitelist. That metadata can omit ordinary
// writable fields. Field values alone cannot distinguish rank from text.
func readCloneFieldSchemas(ctx context.Context, client core.JiraClient) (map[string]string, error) {
	response, err := client.DoAPI(ctx, core.APIRequest{
		Method: http.MethodGet, Endpoint: "/rest/api/3/field",
		MaxResponseBytes: core.DefaultMaxAPIResponseBytes,
	})
	if err != nil {
		return nil, err
	}
	if response == nil || response.StatusCode != http.StatusOK || response.Truncated {
		return nil, invalidCloneSource("field metadata requires a complete HTTP 200 response")
	}
	var definitions []*struct {
		ID     string `json:"id"`
		Schema *struct {
			Custom string `json:"custom"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(response.Body, &definitions); err != nil || definitions == nil {
		return nil, invalidCloneSource("field metadata requires one complete native array")
	}
	schemas := make(map[string]string, len(definitions))
	for _, field := range definitions {
		if field == nil || strings.TrimSpace(field.ID) == "" {
			return nil, invalidCloneSource("field metadata contains an unusable field identity")
		}
		if _, duplicate := schemas[field.ID]; duplicate {
			return nil, invalidCloneSource("field metadata repeats field %q", field.ID)
		}
		custom := ""
		if field.Schema != nil {
			custom = field.Schema.Custom
		}
		schemas[field.ID] = custom
	}
	return schemas, nil
}

func invalidCloneSource(format string, args ...any) error {
	return cli.NewCodedError(cli.CodeIssueCloneSourceInvalid, fmt.Sprintf("issue.clone: "+format, args...))
}
