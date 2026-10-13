package jira

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/matcra587/jira-cli/internal/core"
)

// ErrCloudIDResponseInvalid refuses unusable public tenant metadata without
// reflecting response bytes or selecting another authenticated endpoint.
var ErrCloudIDResponseInvalid = errors.New("cloud discovery response is invalid")

const maxCloudIDResponseBytes = 64 * 1024

// DiscoverCloudID reads public site metadata with no token or cookie state.
// Redirects never select the site-to-cloud association; the command context and
// configured request timeout bound this read like other authentication work.
func (c *Client) DiscoverCloudID(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	target := *c.site
	target.Path, target.RawPath, target.RawQuery, target.Fragment = "/_edge/tenant_info", "", "", ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	client := *c.http
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close() //nolint:errcheck // read-only public metadata body
	if response.StatusCode != http.StatusOK {
		return "", ErrCloudIDResponseInvalid
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxCloudIDResponseBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxCloudIDResponseBytes {
		return "", ErrCloudIDResponseInvalid
	}
	// Decode the cloud ID by name while tolerating unrelated public additions.
	// Duplicate members and trailing JSON cannot define an unambiguous scope.
	var fields map[string]json.RawMessage
	if err := jsonv2.Unmarshal(data, &fields); err != nil {
		return "", ErrCloudIDResponseInvalid
	}
	var id string
	if err := json.Unmarshal(fields["cloudId"], &id); err != nil || id == "" || core.ValidateCloudID(id) != nil {
		return "", ErrCloudIDResponseInvalid
	}
	return strings.TrimSpace(id), ctx.Err()
}
