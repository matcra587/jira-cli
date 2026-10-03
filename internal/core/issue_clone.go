package core

import (
	"errors"
	"fmt"

	"github.com/matcra587/jira-cli/internal/jira"
)

// ErrIssueCloneSourceInvalid identifies source data that must not seed a write.
var ErrIssueCloneSourceInvalid = errors.New("invalid issue clone source")

// ValidateIssueCloneSource requires an explicit native field object. An
// absent or null object cannot be treated as an empty issue to copy.
func ValidateIssueCloneSource(issue *jira.IssueBean) error {
	if issue == nil || !issue.Fields.Set || issue.Fields.Null {
		return fmt.Errorf("%w: source has no native field object", ErrIssueCloneSourceInvalid)
	}
	return nil
}
