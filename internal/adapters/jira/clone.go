package jira

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// decodeCloneIssue validates the complete full-field read before any clone
// composition can turn a partially decoded native document into a write.
func decodeCloneIssue(body io.Reader) (*wire.IssueBean, error) {
	decoder := json.NewDecoder(body)
	var issue *wire.IssueBean
	if err := decoder.Decode(&issue); err != nil {
		return nil, fmt.Errorf("%w: cannot decode full issue: %w", core.ErrIssueCloneSourceInvalid, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("%w: full issue contains trailing data: %w", core.ErrIssueCloneSourceInvalid, err)
		}
		return nil, fmt.Errorf("%w: full issue contains trailing data", core.ErrIssueCloneSourceInvalid)
	}
	if err := core.ValidateIssueCloneSource(issue); err != nil {
		return nil, err
	}
	return issue, nil
}
