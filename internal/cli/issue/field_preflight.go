package issue

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"strings"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

const fieldPreflightHelp = "`--validate-remote` checks requested field IDs and operation names " +
	"against complete live create or per-issue edit metadata, including " +
	"literal inputs with no account lookup. It works on previews and live " +
	"runs; a bare preview stays local. `field_preflight.fields` records " +
	"checked IDs (per result on edit). Native values remain unchanged. " +
	"Values, requiredness, permissions and top-level properties remain Jira's " +
	"validation. Refused contexts send no write; metadata is a snapshot, " +
	"not permission, a guarantee or concurrency protection."

func createFieldPreflight(ctx context.Context, client core.JiraClient, request jira.IssueWriteRequest) (*envelope.IssueFieldPreflight, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	project, projectObject, err := preflightCreateContext(request, "project", "key")
	if err != nil {
		return nil, err
	}
	_, typeObject, err := preflightCreateContext(request, "issuetype", "name")
	if err != nil {
		return nil, err
	}
	reader, ok := client.(InputDiscoveryClient)
	if !ok {
		return nil, core.ErrIssueMetadataUnavailable
	}
	read, err := reader.GetCreateIssueProject(ctx, project)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resolved, err := core.DecodeIssueCreateProject(read.Native)
	if err != nil {
		return nil, err
	}
	if authored, exists := projectObject["key"]; exists && !strings.EqualFold(authored, resolved.Key) {
		return nil, &core.IssueFieldPreflightError{Field: "project", Reason: "authored project identities disagree with the resolved context"}
	}
	if authored, exists := projectObject["id"]; exists && authored != resolved.ID {
		return nil, &core.IssueFieldPreflightError{Field: "project", Reason: "authored project identities disagree with the resolved context"}
	}
	metadata, err := core.DiscoverCreateIssueMetadataForIdentity(ctx, reader, resolved.ID, typeObject["id"], typeObject["name"])
	if err != nil {
		return nil, err
	}
	fields, err := core.PreflightIssueFields(ctx, request, metadata.Fields, true)
	if err != nil {
		return nil, err
	}
	return &envelope.IssueFieldPreflight{Fields: fields}, nil
}

func renderFieldPreflight(v *output.View, preflight *envelope.IssueFieldPreflight) {
	if preflight != nil {
		if len(preflight.Fields) == 0 {
			v.Line("No authored field IDs to check against metadata.")
		} else {
			v.Line("Field screen and operation metadata checked: " + cli.Text(strings.Join(preflight.Fields, ", ")) + ".")
		}
		v.Hint("Field values and permissions remain unchecked; metadata is a snapshot.")
	}
}

func editFieldPreflight(ctx context.Context, client core.JiraClient, key string, request jira.IssueWriteRequest) (*envelope.IssueFieldPreflight, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reader, ok := client.(core.IssueMetadataClient)
	if !ok {
		return nil, core.ErrIssueMetadataUnavailable
	}
	read, err := reader.GetEditIssueFields(ctx, key)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	metadata, err := core.DecodeEditIssueFields(read.Native)
	if err != nil {
		return nil, err
	}
	fields, err := core.PreflightIssueFields(ctx, request, metadata.Fields, false)
	if err != nil {
		return nil, err
	}
	return &envelope.IssueFieldPreflight{Fields: fields}, nil
}

// Metadata needs a determinate native context. Do not guess from display text,
// convert scalar input, or choose between conflicting fields/update containers.
func preflightCreateContext(request jira.IssueWriteRequest, field, alternative string) (string, map[string]string, error) {
	refuse := func() (string, map[string]string, error) {
		return "", nil, &core.IssueFieldPreflightError{Field: field, Reason: "create preflight needs one native object with a usable id or " + alternative}
	}
	raw, replaced := request.Fields[field]
	if update, updated := request.Update[field]; updated {
		if replaced {
			return refuse()
		}
		var operations []map[string]json.RawMessage
		if jsonv2.Unmarshal(update, &operations) != nil || len(operations) == 0 {
			return refuse()
		}
		for _, operation := range operations {
			if len(operation) != 1 {
				return refuse()
			}
			var set bool
			raw, set = operation["set"]
			if !set {
				return refuse()
			}
		}
	}
	var object map[string]json.RawMessage
	if jsonv2.Unmarshal(raw, &object) != nil || object == nil {
		return refuse()
	}
	identities := make(map[string]string, 2)
	for _, name := range []string{"id", alternative} {
		if value, exists := object[name]; exists {
			var identity string
			if jsonv2.Unmarshal(value, &identity) != nil || strings.TrimSpace(identity) == "" {
				return refuse()
			}
			identities[name] = identity
		}
	}
	for _, name := range []string{"id", alternative} {
		if value, exists := identities[name]; exists {
			return value, identities, nil
		}
	}
	return refuse()
}
