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

	xstrings "github.com/gechr/x/strings"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// ErrBoardResponseInvalid identifies a malformed Agile board/project response.
// It must not become an empty successful lookup or a caller-input error.
var ErrBoardResponseInvalid = errors.New("board response invalid")

// GetBoards reads a single offset page through the shared safe-read transport.
// Board names are partial matches; a caller owns disambiguation and any drain.
func (c *Client) GetBoards(ctx context.Context, name string, startAt, maxResults int) (core.BoardRead, error) {
	query := agilePageQuery(startAt, maxResults)
	if name != "" {
		query.Set("name", name)
	}
	page, retry, err := getAgilePage(ctx, c, []string{"rest", "agile", "1.0", "board"}, query, startAt, maxResults, validBoards)
	if err != nil {
		return core.BoardRead{}, fmt.Errorf("get boards: %w", err)
	}
	return core.BoardRead{Page: page, Retry: retry}, nil
}

// GetBoardProjects reads associated projects, not the static /project/full
// relation. Read-only mode and cancellation apply through the existing client.
func (c *Client) GetBoardProjects(ctx context.Context, boardID int64, startAt, maxResults int) (core.BoardProjectRead, error) {
	if boardID <= 0 {
		return core.BoardProjectRead{}, fmt.Errorf("get board projects: board id must be positive")
	}
	path := []string{"rest", "agile", "1.0", "board", strconv.FormatInt(boardID, 10), "project"}
	page, retry, err := getAgilePage(ctx, c, path, agilePageQuery(startAt, maxResults), startAt, maxResults, validBoardProjects)
	if err != nil {
		return core.BoardProjectRead{}, fmt.Errorf("get board projects: %w", err)
	}
	return core.BoardProjectRead{Page: page, Retry: retry}, nil
}

func agilePageQuery(startAt, maxResults int) url.Values {
	return url.Values{"startAt": {strconv.Itoa(startAt)}, "maxResults": {strconv.Itoa(maxResults)}}
}

// getAgilePage owns the common response protocol for both offset endpoints.
// A short page is allowed; an empty non-final page cannot advance a drain.
// A missing total is allowed; completion requires an explicit isLast instead.
func getAgilePage[T any](ctx context.Context, c *Client, path []string, query url.Values, startAt, maxResults int, valid func([]T) error) (*wire.AgilePage[T], core.RetryInfo, error) {
	if startAt < 0 || maxResults <= 0 {
		return nil, core.RetryInfo{}, fmt.Errorf("page offset must be nonnegative and size positive")
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return nil, core.RetryInfo{}, err
	}
	resp, retry, err := c.doWithRetry(req)
	if err != nil {
		return nil, core.RetryInfo{}, err
	}
	defer resp.Body.Close() //nolint:errcheck // a read-only close failure loses no state
	if resp.StatusCode != http.StatusOK {
		return nil, core.RetryInfo{}, c.apiError(resp, retry)
	}
	var page wire.AgilePage[T]
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&page); err != nil {
		return nil, core.RetryInfo{}, fmt.Errorf("%w: decode page: %w", ErrBoardResponseInvalid, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, core.RetryInfo{}, fmt.Errorf("%w: response contains trailing data", ErrBoardResponseInvalid)
	}
	if err := validAgilePage(page, startAt, maxResults); err != nil {
		return nil, core.RetryInfo{}, fmt.Errorf("%w: %w", ErrBoardResponseInvalid, err)
	}
	if err := valid(page.Values.Value); err != nil {
		return nil, core.RetryInfo{}, fmt.Errorf("%w: %w", ErrBoardResponseInvalid, err)
	}
	return &page, retry, nil
}

func validAgilePage[T any](page wire.AgilePage[T], startAt, maxResults int) error {
	if !page.IsLast.Set || page.IsLast.Null || !page.StartAt.Set || page.StartAt.Null || !page.MaxResults.Set || page.MaxResults.Null || !page.Values.Set || page.Values.Null {
		return fmt.Errorf("page lacks explicit pagination facts or values array")
	}
	if page.StartAt.Value != startAt || page.MaxResults.Value <= 0 || len(page.Values.Value) > maxResults || len(page.Values.Value) > page.MaxResults.Value {
		return fmt.Errorf("page offset or size disagrees with the request")
	}
	if len(page.Values.Value) == 0 && !page.IsLast.Value {
		return fmt.Errorf("non-final page makes no progress")
	}
	if page.Total.Set && !page.Total.Null {
		if page.Total.Value < 0 || (len(page.Values.Value) > 0 && (page.Total.Value < startAt || len(page.Values.Value) > page.Total.Value-startAt)) {
			return fmt.Errorf("page total contradicts returned rows")
		}
	}
	return nil
}

func validBoards(boards []wire.Board) error {
	seen := make(map[int64]struct{}, len(boards))
	for _, board := range boards {
		if !board.ID.Set || board.ID.Null || board.ID.Value <= 0 || !board.Name.Set || board.Name.Null || xstrings.IsBlank(board.Name.Value) {
			return fmt.Errorf("board lacks a positive identity or nonblank name")
		}
		if _, duplicate := seen[board.ID.Value]; duplicate {
			return fmt.Errorf("page repeats a board identity")
		}
		seen[board.ID.Value] = struct{}{}
	}
	return nil
}

func validBoardProjects(projects []wire.BoardProject) error {
	seen := make(map[string]struct{}, len(projects))
	for _, project := range projects {
		if !project.ID.Set || project.ID.Null || xstrings.IsBlank(project.ID.Value) || !project.Key.Set || project.Key.Null || xstrings.IsBlank(project.Key.Value) {
			return fmt.Errorf("project lacks an identity or key")
		}
		if _, duplicate := seen[project.ID.Value]; duplicate {
			return fmt.Errorf("page repeats a project identity")
		}
		seen[project.ID.Value] = struct{}{}
	}
	return nil
}
