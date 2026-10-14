package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// ErrUserSearchResponseInvalid marks an upstream protocol failure, not bad
// caller input or an empty search. The CLI taxonomy preserves that distinction.
var ErrUserSearchResponseInvalid = core.ErrUserSearchResponseInvalid

// SearchUsers implements the pinned findUsers operation. Filtering precedes
// paging on this endpoint (unlike assignable/picker search). Privacy controls
// may omit email; the generated User preserves absence, null and extensions.
func (c *Client) SearchUsers(ctx context.Context, query string, startAt, maxResults int) (core.UserSearchRead, error) {
	if err := ctx.Err(); err != nil {
		return core.UserSearchRead{}, err
	}
	params := url.Values{}
	params.Set("query", query)
	params.Set("startAt", strconv.Itoa(startAt))
	params.Set("maxResults", strconv.Itoa(maxResults))
	response, err := c.doAPI(ctx, core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/user/search", Query: params, MaxResponseBytes: core.DefaultMaxAPIResponseBytes}, func(response *http.Response) error {
		return c.requireAuthenticatedReply(response, ErrUserSearchResponseInvalid, "user search")
	})
	if err != nil {
		return core.UserSearchRead{}, err
	}
	if response == nil || response.StatusCode != http.StatusOK || response.Truncated {
		return core.UserSearchRead{}, ErrUserSearchResponseInvalid
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(response.Body, &rows); err != nil {
		return core.UserSearchRead{}, fmt.Errorf("%w: decode array: %w", ErrUserSearchResponseInvalid, err)
	}
	if rows == nil || maxResults < 1 || maxResults > core.UserSearchLimit || len(rows) > maxResults {
		return core.UserSearchRead{}, ErrUserSearchResponseInvalid
	}
	users := make([]*wire.User, len(rows))
	for index, raw := range rows {
		if _, err := wire.DecodeNativeObject(raw, []string{"accountId", "active", "deleted"}); err != nil {
			return core.UserSearchRead{}, fmt.Errorf("%w: native account: %w", ErrUserSearchResponseInvalid, err)
		}
		if err := json.Unmarshal(raw, &users[index]); err != nil {
			return core.UserSearchRead{}, fmt.Errorf("%w: decode account: %w", ErrUserSearchResponseInvalid, err)
		}
	}
	if _, err := core.ActiveUserSearchPage(users, maxResults, make(map[string]struct{})); err != nil {
		return core.UserSearchRead{}, err
	}
	if err := ctx.Err(); err != nil {
		return core.UserSearchRead{}, err
	}
	return core.UserSearchRead{Users: users, Retry: response.Retry}, nil
}
