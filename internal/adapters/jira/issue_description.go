package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// decodeDescriptionIssue admits one complete bounded native issue object.
// The raw API capture uses the same byte cap. Strict native-object decoding
// refuses duplicates/trailing data and retains original description bytes
// independently of the generated wire decoder's presentation formatting.
func decodeDescriptionIssue(body io.Reader) (*wire.IssueBean, error) {
	data, err := io.ReadAll(io.LimitReader(body, core.DefaultMaxAPIResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read description source: %w", core.ErrIssueDescriptionInvalid, err)
	}
	if int64(len(data)) > core.DefaultMaxAPIResponseBytes {
		return nil, fmt.Errorf("%w: description response exceeds the shared native capture limit", core.ErrIssueDescriptionInvalid)
	}
	object, err := wire.DecodeNativeObject(data, []string{"id", "key", "fields"})
	if err != nil {
		return nil, fmt.Errorf("%w: decode description source: %w", core.ErrIssueDescriptionInvalid, err)
	}
	fields, err := wire.DecodeNativeObject(object["fields"], []string{"description", "updated"})
	if err != nil {
		return nil, fmt.Errorf("%w: decode description fields: %w", core.ErrIssueDescriptionInvalid, err)
	}
	var issue wire.IssueBean
	if err := json.Unmarshal(data, &issue); err != nil {
		return nil, fmt.Errorf("%w: decode native description types: %w", core.ErrIssueDescriptionInvalid, err)
	}
	if issue.Fields.Set && !issue.Fields.Null && issue.Fields.Value.Description.Set && !issue.Fields.Value.Description.Null {
		issue.Fields.Value.Description.Value = bytes.Clone(fields["description"])
	}
	return &issue, nil
}
