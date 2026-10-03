package jira

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// decodeCommentReceipt accepts exactly one object with a usable identity.
// It runs after a successful HTTP write, so failures require inspection
// rather than replaying a write that Jira may already have stored.
func decodeCommentReceipt(body io.Reader, expectedID string) (*wire.Comment, error) {
	decoder := json.NewDecoder(body)
	var comment *wire.Comment
	if err := decoder.Decode(&comment); err != nil {
		return nil, fmt.Errorf("%w: Jira acknowledged the write but its response could not be decoded: %w", core.ErrCommentResponseInvalid, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("%w: Jira acknowledged the write but its response contains trailing data: %w", core.ErrCommentResponseInvalid, err)
		}
		return nil, fmt.Errorf("%w: Jira acknowledged the write but its response contains trailing data", core.ErrCommentResponseInvalid)
	}
	if err := core.ValidateCommentReceipt(comment, expectedID); err != nil {
		return nil, err
	}
	return comment, nil
}

// decodeCommentPage requires one nonnull native page with explicit pagination
// metadata. Pointer integers distinguish missing/null metadata from real zeros
// without changing the shared wire type used by embedded issue previews.
func decodeCommentPage(body io.Reader, start, limit int) (*wire.CommentPage, error) {
	decoder := json.NewDecoder(body)
	var native *struct {
		StartAt    *int           `json:"startAt"`
		MaxResults *int           `json:"maxResults"`
		Total      *int           `json:"total"`
		Comments   []wire.Comment `json:"comments"`
	}
	if err := decoder.Decode(&native); err != nil {
		return nil, fmt.Errorf("%w: comment page could not be decoded: %w", core.ErrCommentResponseInvalid, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("%w: comment page contains trailing data: %w", core.ErrCommentResponseInvalid, err)
		}
		return nil, fmt.Errorf("%w: comment page contains trailing data", core.ErrCommentResponseInvalid)
	}
	if native == nil || native.StartAt == nil || native.MaxResults == nil || native.Total == nil {
		return nil, fmt.Errorf("%w: comment page has no explicit pagination metadata", core.ErrCommentResponseInvalid)
	}
	page := &wire.CommentPage{StartAt: *native.StartAt, MaxResults: *native.MaxResults, Total: *native.Total, Comments: native.Comments}
	if err := core.ValidateCommentPage(page, start, limit); err != nil {
		return nil, err
	}
	return page, nil
}
