package core

import (
	"errors"
	"fmt"
	"strings"

	"github.com/matcra587/jira-cli/internal/jira"
)

// ErrCommentResponseInvalid identifies an unusable comment page or write
// acknowledgement. A write may have been stored; inspect before repeating it.
var ErrCommentResponseInvalid = errors.New("invalid comment response")

// ValidateCommentReceipt requires a nonblank comment identity. For an edit,
// expectedID also prevents a receipt for another comment being published.
// A valid identity does not prove the stored body; that needs an independent read.
func ValidateCommentReceipt(comment *jira.Comment, expectedID string) error {
	if comment == nil || !comment.ID.Set || comment.ID.Null || strings.TrimSpace(comment.ID.Value) == "" {
		return fmt.Errorf("%w: Jira acknowledged the write but its response has no comment identity", ErrCommentResponseInvalid)
	}
	if expectedID != "" && comment.ID.Value != expectedID {
		return fmt.Errorf("%w: Jira acknowledged the edit but its response identifies another comment", ErrCommentResponseInvalid)
	}
	return nil
}

// ValidateCommentPage checks native offsets, page bounds and identities before
// publishing a page. An empty beyond-end request may be clamped to total by
// Jira. A nonempty page must begin at the requested offset. Short pages are
// valid; an empty page before total cannot advance a walk.
func ValidateCommentPage(page *jira.CommentPage, start, limit int) error {
	if page == nil || page.Comments == nil {
		return fmt.Errorf("%w: comment page has no explicit comments array", ErrCommentResponseInvalid)
	}
	if page.StartAt < 0 || page.MaxResults < 0 || page.Total < 0 || len(page.Comments) > page.MaxResults {
		return fmt.Errorf("%w: comment page has invalid pagination bounds", ErrCommentResponseInvalid)
	}
	if page.StartAt != start && (len(page.Comments) != 0 || start < page.Total || page.StartAt != page.Total) {
		return fmt.Errorf("%w: comment page does not match the requested offset", ErrCommentResponseInvalid)
	}
	if page.MaxResults > limit || (len(page.Comments) > 0 && (page.StartAt > page.Total || len(page.Comments) > page.Total-page.StartAt)) {
		return fmt.Errorf("%w: comment page exceeds its requested size or native total", ErrCommentResponseInvalid)
	}
	if len(page.Comments) == 0 && page.StartAt < page.Total {
		return fmt.Errorf("%w: comment page is empty before its reported total", ErrCommentResponseInvalid)
	}
	seen := make(map[string]struct{}, len(page.Comments))
	for i := range page.Comments {
		if err := ValidateCommentReceipt(&page.Comments[i], ""); err != nil {
			return fmt.Errorf("%w: comment page contains an unusable identity", ErrCommentResponseInvalid)
		}
		id := page.Comments[i].ID.Value
		if _, exists := seen[id]; exists {
			return fmt.Errorf("%w: comment page repeats an identity", ErrCommentResponseInvalid)
		}
		seen[id] = struct{}{}
	}
	return nil
}
