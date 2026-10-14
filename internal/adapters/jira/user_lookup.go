package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// GetUser implements the pinned exact-account getUser operation. Final replies
// must retain the configured API scope and credential binding before admission.
func (c *Client) GetUser(ctx context.Context, accountID string) (core.UserRead, error) {
	if err := ctx.Err(); err != nil {
		return core.UserRead{}, err
	}
	params := url.Values{}
	params.Set("accountId", accountID)
	response, err := c.doAPI(ctx, core.APIRequest{Method: http.MethodGet, Endpoint: "/rest/api/3/user", Query: params, MaxResponseBytes: core.DefaultMaxAPIResponseBytes}, func(response *http.Response) error {
		return c.requireAuthenticatedReply(response, core.ErrUserLookupResponseInvalid, "user lookup")
	})
	if err != nil {
		return core.UserRead{}, err
	}
	if response == nil || response.StatusCode != http.StatusOK || response.Truncated {
		return core.UserRead{}, core.ErrUserLookupResponseInvalid
	}
	if _, err := wire.DecodeNativeObject(response.Body, []string{"accountId", "active", "deleted"}); err != nil {
		return core.UserRead{}, fmt.Errorf("%w: native account: %w", core.ErrUserLookupResponseInvalid, err)
	}
	var user *wire.UserDetails
	if err := json.Unmarshal(response.Body, &user); err != nil {
		return core.UserRead{}, fmt.Errorf("%w: decode account: %w", core.ErrUserLookupResponseInvalid, err)
	}
	if err := core.RequireActiveAccountIdentity(user, accountID); err != nil {
		return core.UserRead{}, err
	}
	if err := ctx.Err(); err != nil {
		return core.UserRead{}, err
	}
	return core.UserRead{User: user, Retry: response.Retry}, nil
}
