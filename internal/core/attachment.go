package core

import (
	"errors"
	"fmt"
	"strings"

	"github.com/matcra587/jira-cli/internal/jira"
)

// ErrAttachmentResponseInvalid identifies an unusable attachment observation
// or write acknowledgement. A write may have landed despite its response.
var ErrAttachmentResponseInvalid = errors.New("invalid attachment response")

// ValidateAttachmentReceipt requires a distinct resource identity per input.
// It does not claim content verification; that needs an independent read.
func ValidateAttachmentReceipt(attachments []jira.Attachment, expected int) error {
	if expected <= 0 || len(attachments) != expected {
		return fmt.Errorf("%w: upload returned %d resources for %d files", ErrAttachmentResponseInvalid, len(attachments), expected)
	}
	return validateAttachmentIdentities(attachments)
}

// ValidateAttachmentIssue requires an explicit complete attachment array on
// an identified issue. Jira may canonicalize an old key or a numeric issue ID,
// so the returned identity need not equal the caller's lookup value.
func ValidateAttachmentIssue(issue *jira.IssueBean) error {
	if issue == nil || !issue.ID.Set || issue.ID.Null || strings.TrimSpace(issue.ID.Value) == "" || !issue.Key.Set || issue.Key.Null || strings.TrimSpace(issue.Key.Value) == "" {
		return fmt.Errorf("%w: attachment observation has no issue identity", ErrAttachmentResponseInvalid)
	}
	if !issue.Fields.Set || issue.Fields.Null || !issue.Fields.Value.Attachment.Set || issue.Fields.Value.Attachment.Null || issue.Fields.Value.Attachment.Value == nil {
		return fmt.Errorf("%w: issue has no explicit attachment array", ErrAttachmentResponseInvalid)
	}
	return validateAttachmentIdentities(issue.Fields.Value.Attachment.Value)
}

func validateAttachmentIdentities(attachments []jira.Attachment) error {
	seen := make(map[string]bool, len(attachments))
	for _, attachment := range attachments {
		id := attachment.ID
		if !id.Set || id.Null || strings.TrimSpace(id.Value) == "" || seen[id.Value] {
			return fmt.Errorf("%w: attachment response has a missing or repeated resource identity", ErrAttachmentResponseInvalid)
		}
		seen[id.Value] = true
	}
	return nil
}
