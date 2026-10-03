package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// ErrJQLResponseInvalid distinguishes protocol failures from query diagnostics.
var ErrJQLResponseInvalid = errors.New("jql response invalid")

func (c *Client) ParseJQL(ctx context.Context, queries []string, mode core.JQLValidationMode) (core.JQLParseRead, error) {
	if err := core.ValidateJQLQueries(queries, mode); err != nil {
		return core.JQLParseRead{}, err
	}
	params := url.Values{"validation": {string(mode)}}
	body := wire.JqlQueriesToParse{Queries: wire.NullableOf(queries)}
	req, err := c.newRequest(ctx, http.MethodPost, []string{"rest", "api", "3", "jql", "parse"}, params, body)
	if err != nil {
		return core.JQLParseRead{}, err
	}
	resp, retry, err := c.doWithRetry(req)
	if err != nil {
		return core.JQLParseRead{}, fmt.Errorf("parse JQL: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only response; close failure loses no state
	if resp.StatusCode != http.StatusOK {
		return core.JQLParseRead{}, c.apiError(resp, retry)
	}
	raw, err := decodeJQLJSON(resp.Body)
	if err != nil {
		return core.JQLParseRead{}, err
	}
	parsed, err := wire.ParseJQLResponse(raw, len(queries))
	if err != nil {
		return core.JQLParseRead{}, fmt.Errorf("%w: %w", ErrJQLResponseInvalid, err)
	}
	return core.JQLParseRead{Parsed: &parsed, Retry: retry}, nil
}

func (c *Client) GetJQLReference(ctx context.Context) (core.JQLReferenceRead, error) {
	req, err := c.newRequest(ctx, http.MethodGet, []string{"rest", "api", "3", "jql", "autocompletedata"}, nil, nil)
	if err != nil {
		return core.JQLReferenceRead{}, err
	}
	resp, retry, err := c.doWithRetry(req)
	if err != nil {
		return core.JQLReferenceRead{}, fmt.Errorf("read JQL reference: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-only response; close failure loses no state
	if resp.StatusCode != http.StatusOK {
		return core.JQLReferenceRead{}, c.apiError(resp, retry)
	}
	raw, err := decodeJQLJSON(resp.Body)
	if err != nil {
		return core.JQLReferenceRead{}, err
	}
	reference, err := wire.ParseJQLReferenceResponse(raw)
	if err != nil {
		return core.JQLReferenceRead{}, fmt.Errorf("%w: %w", ErrJQLResponseInvalid, err)
	}
	return core.JQLReferenceRead{Reference: &reference, Retry: retry}, nil
}

func decodeJQLJSON(body io.Reader) (json.RawMessage, error) {
	decoder := json.NewDecoder(body)
	var raw, trailing json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w: decode: %w", ErrJQLResponseInvalid, err)
	}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("%w: trailing response data: %w", ErrJQLResponseInvalid, err)
		}
		return nil, fmt.Errorf("%w: trailing response data", ErrJQLResponseInvalid)
	}
	return raw, nil
}
