package jira

import (
	"errors"
	"net/http"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
)

// doObservedRequest observes every completed attempt, including responses later
// discarded by safe retry. Redirected media cannot establish Jira rate state.
func (c *Client) doObservedRequest(req *http.Request) (*http.Response, error) {
	response, err := c.http.Do(req)
	if err == nil && response != nil && strings.EqualFold(strings.TrimSpace(response.Header.Get("X-RateLimit-NearLimit")), "true") {
		if c.requireAuthenticatedReply(response, errors.New("unbound Jira rate observation"), "rate limit") == nil {
			core.ObserveRateLimitNear(req.Context())
		}
	}
	return response, err
}
