package jira

import (
	"fmt"
	"net/http"
)

// requireAuthenticatedReply admits observations bound to this client's scope.
// Credential-free media redirects remain usable, but their responses cannot
// establish account identity or supply a bound native issue editing source.
func (c *Client) requireAuthenticatedReply(response *http.Response, invalid error, resource string) error {
	if response == nil || response.Request == nil || response.Request.URL == nil || !withinBase(c.base, response.Request.URL) {
		return fmt.Errorf("%w: %s reply left the authenticated API scope", invalid, resource)
	}
	email, token, authenticated := response.Request.BasicAuth()
	if !authenticated || email != c.email || token != c.token {
		return fmt.Errorf("%w: %s request lost its credential binding", invalid, resource)
	}
	return nil
}
