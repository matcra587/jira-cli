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

// ErrUserSearchResponseInvalid marks an upstream protocol failure, not bad
// caller input or an empty search. The CLI taxonomy preserves that distinction.
var ErrUserSearchResponseInvalid = errors.New("user search response invalid")

// SearchUsers implements the pinned findUsers operation. Filtering precedes
// paging on this endpoint (unlike assignable/picker search). Privacy controls
// may omit email; the generated User preserves absence, null and extensions.
func (c *Client) SearchUsers(ctx context.Context, query string, startAt, maxResults int) (core.UserSearchRead, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("startAt", strconv.Itoa(startAt))
	params.Set("maxResults", strconv.Itoa(maxResults))
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "api", "3", "user", "search"}, params, nil)
	if err != nil {
		return core.UserSearchRead{}, err
	}
	resp, info, err := c.doWithRetry(req)
	if err != nil {
		return core.UserSearchRead{}, fmt.Errorf("search users: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body; close failure loses no state
	if resp.StatusCode != http.StatusOK {
		return core.UserSearchRead{}, c.apiError(resp, info)
	}
	var users []*wire.User
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&users); err != nil {
		return core.UserSearchRead{}, fmt.Errorf("%w: decode array: %w", ErrUserSearchResponseInvalid, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return core.UserSearchRead{}, fmt.Errorf("%w: response contains trailing data", ErrUserSearchResponseInvalid)
	}
	if users == nil || len(users) > maxResults {
		return core.UserSearchRead{}, fmt.Errorf("%w: missing array or oversized page", ErrUserSearchResponseInvalid)
	}
	for _, user := range users {
		if user == nil || !user.AccountID.Set || user.AccountID.Null || user.AccountID.Value == "" {
			return core.UserSearchRead{}, fmt.Errorf("%w: match lacks an account identity", ErrUserSearchResponseInvalid)
		}
	}
	return core.UserSearchRead{Users: users, Retry: info}, nil
}
