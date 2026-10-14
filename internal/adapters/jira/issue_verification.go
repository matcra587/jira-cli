package jira

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

var errIssueVerificationInvalid = errors.New("invalid issue verification readback")

var _ core.IssueVerificationReader = (*Client)(nil)

// GetIssueVerification uses the ordinary scoped/retry owner with strict native
// admission before values can supply post-write comparison evidence.
func (c *Client) GetIssueVerification(ctx context.Context, key string, fields []string) (core.IssueVerificationRead, error) {
	var nativeFields map[string]json.RawMessage
	read, err := c.getIssue(ctx, key, fields, &nativeFields)
	return core.IssueVerificationRead{Read: read, Fields: nativeFields}, err
}

func decodeVerificationIssue(body io.Reader, selectors []string) (*wire.IssueBean, map[string]json.RawMessage, error) {
	raw, err := io.ReadAll(io.LimitReader(body, core.DefaultMaxAPIResponseBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errIssueVerificationInvalid, err)
	}
	if int64(len(raw)) > core.DefaultMaxAPIResponseBytes || !jsontext.Value(raw).IsValid() {
		return nil, nil, errIssueVerificationInvalid
	}
	object, err := wire.DecodeNativeObject(raw, []string{"id", "key", "fields"})
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errIssueVerificationInvalid, err)
	}
	fields, err := wire.DecodeNativeObject(object["fields"], selectors)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errIssueVerificationInvalid, err)
	}
	var issue wire.IssueBean
	if err := json.Unmarshal(raw, &issue); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errIssueVerificationInvalid, err)
	}
	return &issue, fields, nil
}
