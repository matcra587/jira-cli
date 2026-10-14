package core

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"slices"

	"github.com/matcra587/jira-cli/internal/jira"
)

// IssueFieldPreflightError refuses an authored field or verb before a write.
// It reports a metadata snapshot mismatch, not permission or value validity.
type IssueFieldPreflightError struct {
	Field, Operation, Reason string
}

func (e *IssueFieldPreflightError) Error() string {
	return fmt.Sprintf("issue field preflight: field %q operation %q: %s", e.Field, e.Operation, e.Reason)
}

// PreflightIssueFields checks screen membership and operation names against a
// complete validated metadata snapshot. Values and native requests stay intact;
// this does not check value types, requiredness, permissions or top-level members.
func PreflightIssueFields(ctx context.Context, request jira.IssueWriteRequest, fields map[string]json.RawMessage, create bool) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, fmt.Errorf("%w: no complete field map", ErrIssueMetadataInvalid)
	}
	checked := make([]string, 0, len(request.Keys()))
	for _, id := range request.Keys() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if create && (id == "project" || id == "issuetype") {
			continue // Structural create identities need not appear on the screen.
		}
		definition, exists := fields[id]
		if !exists {
			return nil, &IssueFieldPreflightError{Field: id, Reason: "field is absent from the context's metadata"}
		}
		members, err := metadataObject(definition)
		if err != nil {
			return nil, err
		}
		if err := validateIssueFieldMetadata(members); err != nil {
			return nil, err
		}
		var allowed []string
		if err := jsonv2.Unmarshal(members["operations"], &allowed); err != nil {
			return nil, fmt.Errorf("%w: invalid field operations: %w", ErrIssueMetadataInvalid, err)
		}
		verbs := make([]string, 0, 1)
		if _, replaced := request.Fields[id]; replaced {
			verbs = append(verbs, "set")
		}
		if raw, updated := request.Update[id]; updated {
			var operations []map[string]json.RawMessage
			if !jsontext.Value(raw).IsValid() || jsonv2.Unmarshal(raw, &operations) != nil || len(operations) == 0 {
				return nil, &IssueFieldPreflightError{Field: id, Reason: "update requires a nonempty array of single-verb objects"}
			}
			for _, operation := range operations {
				if len(operation) != 1 {
					return nil, &IssueFieldPreflightError{Field: id, Reason: "update requires one verb in each operation object"}
				}
				for verb := range operation {
					verbs = append(verbs, verb)
				}
			}
		}
		for _, verb := range verbs {
			if !slices.Contains(allowed, verb) {
				return nil, &IssueFieldPreflightError{Field: id, Operation: verb, Reason: "operation is absent from the field's metadata"}
			}
		}
		checked = append(checked, id)
	}
	return checked, ctx.Err()
}
