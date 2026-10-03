package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

var ErrBulkOperationResponseInvalid = errors.New("bulk operation response invalid")

// ValidateBulkTaskID applies the task endpoint's local path rules without
// constructing a client or reading credentials.
func ValidateBulkTaskID(taskID string) error {
	if taskID == "" || strings.TrimSpace(taskID) != taskID {
		return errors.New("bulk progress requires a nonblank task identity with no surrounding whitespace")
	}
	return validatePathSegments([]string{taskID})
}

// SubmitIssueMove reuses the shared mutation policy. It submits one job, never
// a sequence of per-issue edits or workflow transitions.
func (c *Client) SubmitIssueMove(ctx context.Context, body wire.IssueBulkMovePayload) (core.IssueMoveSubmission, error) {
	if _, err := body.MoveBindings(); err != nil {
		return core.IssueMoveSubmission{}, fmt.Errorf("submit move: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, []string{"rest", "api", "3", "bulk", "issues", "move"}, nil, body)
	if err != nil {
		return core.IssueMoveSubmission{}, err
	}
	resp, retry, err := c.doWithRetry(req)
	if err != nil {
		return core.IssueMoveSubmission{}, fmt.Errorf("submit move: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body; no buffered write
	if resp.StatusCode != http.StatusCreated {
		return core.IssueMoveSubmission{}, c.apiError(resp, retry)
	}
	out := core.IssueMoveSubmission{Acknowledged: true, Retry: retry}
	var task wire.SubmittedBulkOperation
	if err := decodeBulkJSON(resp.Body, &task, "SubmittedBulkOperation"); err != nil {
		return out, fmt.Errorf("move was accepted but its task receipt could not be decoded: %w", err)
	}
	if err := task.ValidateIdentity(); err != nil {
		return out, fmt.Errorf("move was accepted but its task receipt is invalid: %w: %w", ErrBulkOperationResponseInvalid, err)
	}
	if err := ValidateBulkTaskID(task.TaskID.Value); err != nil {
		return out, fmt.Errorf("move was accepted but its task identity is unsafe: %w: %w", ErrBulkOperationResponseInvalid, err)
	}
	out.Task = &task
	return out, nil
}

func (c *Client) GetBulkOperationProgress(ctx context.Context, taskID string) (core.BulkOperationRead, error) {
	if err := ValidateBulkTaskID(taskID); err != nil {
		return core.BulkOperationRead{}, err
	}
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "api", "3", "bulk", "queue", taskID}, nil, nil)
	if err != nil {
		return core.BulkOperationRead{}, err
	}
	resp, retry, err := c.doWithRetry(req)
	if err != nil {
		return core.BulkOperationRead{}, fmt.Errorf("read bulk progress: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body; no buffered write
	if resp.StatusCode != http.StatusOK {
		return core.BulkOperationRead{}, c.apiError(resp, retry)
	}
	var progress wire.BulkOperationProgress
	if err := decodeBulkJSON(resp.Body, &progress, "BulkOperationProgress"); err != nil {
		return core.BulkOperationRead{}, err
	}
	if err := progress.ValidateTask(taskID); err != nil {
		return core.BulkOperationRead{}, fmt.Errorf("%w: %w", ErrBulkOperationResponseInvalid, err)
	}
	return core.BulkOperationRead{Progress: &progress, Retry: retry}, nil
}

func decodeBulkJSON(body io.Reader, dst any, schema string) error {
	decoder := json.NewDecoder(body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return fmt.Errorf("%w: decode: %w", ErrBulkOperationResponseInvalid, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing response data", ErrBulkOperationResponseInvalid)
	}
	if _, err := wire.DecodeNativeObject(raw, wire.WireSchemaProperties()[schema]); err != nil {
		return fmt.Errorf("%w: %w", ErrBulkOperationResponseInvalid, err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%w: decode: %w", ErrBulkOperationResponseInvalid, err)
	}
	return nil
}
