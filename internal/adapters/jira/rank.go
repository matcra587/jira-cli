package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// DeleteIssue implements core.JiraClient: removes one issue.
//
// deleteSubtasks is sent ALWAYS, explicitly, rather than only when true.
// The endpoint defaults it to false, so omitting it would work — but a
// request whose most consequential parameter is implicit is one a
// reviewer reading the wire cannot check, and this is the parameter that
// decides whether one issue or five disappear.
func (c *Client) DeleteIssue(ctx context.Context, key string, deleteSubtasks bool) (core.IssueDeleteWrite, error) {
	query := url.Values{}
	query.Set("deleteSubtasks", strconv.FormatBool(deleteSubtasks))
	req, err := c.newRequest(ctx, http.MethodDelete, []string{"rest", "api", "3", "issue", key}, query, nil)
	if err != nil {
		return core.IssueDeleteWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.IssueDeleteWrite{}, fmt.Errorf("delete issue: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices && resp.StatusCode != http.StatusNoContent {
		return core.IssueDeleteWrite{}, fmt.Errorf("%w: Jira answered issue deletion with unexpected status %d", core.ErrIssueDeleteResponseInvalid, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusNoContent {
		// This is where the refusal for an issue WITH subtasks arrives: a
		// 400 whose message names them. It reaches the caller as an
		// ordinary apiError carrying Jira's own words, which say more than
		// anything this client could add.
		return core.IssueDeleteWrite{}, c.apiError(resp, info)
	}
	return core.IssueDeleteWrite{StatusCode: http.StatusNoContent, Retry: info}, nil
}

// RankIssues implements core.JiraClient: reorders issues in the backlog.
//
// The shared site-root transport reaches the Agile endpoint without another
// client. One ordered request contains at most fifty distinct sources. A 204
// acknowledges the batch; a 207 requires a complete attributable entries object
// before any per-issue acknowledgement is published. Writes are never replayed.
func (c *Client) RankIssues(ctx context.Context, body wire.RankRequest) (core.RankWrite, error) {
	if err := core.ValidateRankRequest(body); err != nil {
		return core.RankWrite{}, err
	}
	req, err := c.newRequest(ctx, http.MethodPut, []string{"rest", "agile", "1.0", "issue", "rank"}, nil, body)
	if err != nil {
		return core.RankWrite{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.RankWrite{}, fmt.Errorf("rank issues: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; a close failure loses nothing
	switch resp.StatusCode {
	case http.StatusNoContent:
		return core.RankWrite{StatusCode: http.StatusNoContent, Retry: info}, nil
	case http.StatusMultiStatus:
		outcomes, err := decodeRankReceipt(resp.Body)
		if err != nil {
			return core.RankWrite{}, err
		}
		write := core.RankWrite{StatusCode: http.StatusMultiStatus, Outcomes: outcomes, Retry: info}
		if _, err := core.OrderedRankOutcomes(body.Issues, write); err != nil {
			return core.RankWrite{}, err
		}
		return write, nil
	}
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return core.RankWrite{}, fmt.Errorf("%w: Jira acknowledged ranking with unexpected status %d", core.ErrRankResponseInvalid, resp.StatusCode)
	}
	return core.RankWrite{}, c.apiError(resp, info)
}

// decodeRankReceipt follows the complete entries object in the vendored Agile
// 207 example. An unusable response leaves the write outcome uncertain.
func decodeRankReceipt(body io.Reader) ([]core.RankOutcome, error) {
	// Preserve the previous rank decoder's byte ceiling, detecting overflow
	// explicitly instead of allowing a limited reader to fabricate EOF.
	data, err := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: the native entries could not be read: %w", core.ErrRankResponseInvalid, err)
	}
	if len(data) > maxErrorBodyBytes {
		return nil, fmt.Errorf("%w: the native entries exceed %d bytes", core.ErrRankResponseInvalid, maxErrorBodyBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var response *struct {
		Entries []struct {
			IssueID  wire.Nullable[int64]  `json:"issueId"`
			IssueKey wire.Nullable[string] `json:"issueKey"`
			Status   wire.Nullable[int]    `json:"status"`
			Errors   []string              `json:"errors"`
		} `json:"entries"`
	}
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("%w: the native entries could not be decoded: %w", core.ErrRankResponseInvalid, err)
	}
	if response == nil || len(response.Entries) == 0 {
		return nil, fmt.Errorf("%w: the response has no native entries", core.ErrRankResponseInvalid)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("%w: the response contains trailing data: %w", core.ErrRankResponseInvalid, err)
		}
		return nil, fmt.Errorf("%w: the response contains trailing data", core.ErrRankResponseInvalid)
	}
	outcomes := make([]core.RankOutcome, len(response.Entries))
	for i, entry := range response.Entries {
		if !entry.Status.Set || entry.Status.Null || entry.IssueID.Set && (entry.IssueID.Null || entry.IssueID.Value <= 0) || entry.IssueKey.Set && (entry.IssueKey.Null || entry.IssueKey.Value == "") {
			return nil, fmt.Errorf("%w: a native entry has missing or unusable identity or status", core.ErrRankResponseInvalid)
		}
		outcomes[i] = core.RankOutcome{IssueID: entry.IssueID.Value, IssueKey: entry.IssueKey.Value, Status: entry.Status.Value, Errors: entry.Errors}
	}
	return outcomes, nil
}
