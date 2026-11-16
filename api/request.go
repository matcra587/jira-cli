package api

import (
	"context"
	"net/url"

	adapter "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/core"
)

// Request addresses one native Platform or Agile endpoint relative to the
// configured site/cloud route. Absolute URLs and path traversal are refused.
// Body preserves exact bytes; nil and nonnil empty bodies remain distinct.
type Request struct {
	Method            string
	Endpoint          string
	Query             url.Values
	Body              []byte
	ContentType       string
	IfMatch           string
	IfUnmodifiedSince string
}

// RetryInfo reports actual safe-read attempts and backoff.
type RetryInfo = core.RetryInfo

// Response retains native bytes and transport observations even when Do
// returns an error. Headers omit authentication and cookie headers. Truncated
// means the body is incomplete; HTTP success does not make it usable JSON.
type Response = core.APIResponse

// Do executes one native request through the CLI's shared authentication,
// routing, redirect, read-only and safe-retry boundaries. It never walks pages,
// prints output or infers application actions from remote content. The caller
// must not mutate Request's body/query while Do runs.
func (c *Client) Do(ctx context.Context, request Request) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.transport == nil {
		return nil, &Error{Code: "client_required"}
	}
	native := core.APIRequest{
		Method: request.Method, Endpoint: request.Endpoint, Query: request.Query,
		Body: request.Body, ContentType: request.ContentType,
		IfMatch: request.IfMatch, IfUnmodifiedSince: request.IfUnmodifiedSince,
		MaxResponseBytes: c.limit,
	}
	if err := adapter.ValidateAPIRequest(native); err != nil {
		return nil, &Error{Code: "invalid_request", cause: err}
	}
	response, err := c.transport.DoAPI(ctx, native)
	if err != nil {
		failure := requestError(err)
		if response != nil {
			failure.StatusCode = response.StatusCode
		}
		return response, failure
	}
	return response, nil
}
