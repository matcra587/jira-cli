package jira

import (
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

// ErrWorklogResponseInvalid identifies an upstream protocol failure. A failed
// POST receipt can follow a completed write, so callers must inspect before
// repeating the operation.
var ErrWorklogResponseInvalid = errors.New("worklog response invalid")

func (c *Client) GetWorklogs(ctx context.Context, key string, startAt, maxResults int) (core.WorklogRead, error) {
	if startAt < 0 || maxResults < 1 {
		return core.WorklogRead{}, errors.New("get worklogs: offset must be nonnegative and page size positive")
	}
	query := url.Values{}
	query.Set("startAt", strconv.Itoa(startAt))
	query.Set("maxResults", strconv.Itoa(maxResults))
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "api", "3", "issue", key, "worklog"}, query, nil)
	if err != nil {
		return core.WorklogRead{}, err
	}
	resp, retry, err := c.doWithRetry(req)
	if err != nil {
		return core.WorklogRead{}, fmt.Errorf("get worklogs: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body; no buffered write
	if resp.StatusCode != http.StatusOK {
		return core.WorklogRead{}, c.apiError(resp, retry)
	}
	var page wire.PageOfWorklogs
	if err := decodeWorklogJSON(resp.Body, &page); err != nil {
		return core.WorklogRead{}, err
	}
	if err := page.ValidatePage(startAt, maxResults); err != nil {
		return core.WorklogRead{}, fmt.Errorf("%w: %w", ErrWorklogResponseInvalid, err)
	}
	return core.WorklogRead{Page: &page, Retry: retry}, nil
}

// AddWorklog uses the shared read-only and no-replay transport policy. The
// estimate adjustment is Jira's default auto; no hidden field write is added.
func (c *Client) AddWorklog(ctx context.Context, key string, body wire.AddWorklogRequest, notifyUsers bool) (core.WorklogWrite, error) {
	if body.TimeSpentSeconds <= 0 {
		return core.WorklogWrite{}, errors.New("add worklog: time spent must be positive")
	}
	query := notifyQuery(notifyUsers)
	req, err := c.newRequest(ctx, http.MethodPost, []string{"rest", "api", "3", "issue", key, "worklog"}, query, body)
	if err != nil {
		return core.WorklogWrite{}, err
	}
	resp, retry, err := c.doWithRetry(req)
	if err != nil {
		return core.WorklogWrite{}, fmt.Errorf("add worklog: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body; no buffered write
	if resp.StatusCode != http.StatusCreated {
		return core.WorklogWrite{}, c.apiError(resp, retry)
	}
	write := core.WorklogWrite{Acknowledged: true, Retry: retry}
	var worklog wire.Worklog
	if err := decodeWorklogJSON(resp.Body, &worklog); err != nil {
		return write, fmt.Errorf("worklog was created but its receipt could not be decoded: %w", err)
	}
	if err := worklog.ValidateIdentity(); err != nil {
		return write, fmt.Errorf("worklog was created but its receipt has no valid identity: %w: %w", ErrWorklogResponseInvalid, err)
	}
	write.Worklog = &worklog
	return write, nil
}

func decodeWorklogJSON(body io.Reader, dst any) error {
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("%w: decode: %w", ErrWorklogResponseInvalid, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing response data", ErrWorklogResponseInvalid)
	}
	return nil
}
