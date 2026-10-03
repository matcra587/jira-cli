package jira

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// decodeRemoteLinkReceipt accepts one document with a usable native identity.
// A malformed acknowledgement does not establish that the write was rejected.
func decodeRemoteLinkReceipt(body io.Reader) (*wire.RemoteIssueLink, error) {
	decoder := json.NewDecoder(body)
	var link *wire.RemoteIssueLink
	if err := decoder.Decode(&link); err != nil {
		return nil, fmt.Errorf("%w: Jira acknowledged the write but its response could not be decoded: %w", core.ErrWeblinkResponseInvalid, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("%w: Jira acknowledged the write but its response contains trailing data: %w", core.ErrWeblinkResponseInvalid, err)
		}
		return nil, fmt.Errorf("%w: Jira acknowledged the write but its response contains trailing data", core.ErrWeblinkResponseInvalid)
	}
	if err := core.ValidateRemoteLinkIdentity(link); err != nil {
		return nil, err
	}
	return link, nil
}

// decodeRemoteLinks requires one explicit array with distinct usable identities.
// A null response does not establish that an issue has no remote links.
func decodeRemoteLinks(body io.Reader) ([]wire.RemoteIssueLink, error) {
	decoder := json.NewDecoder(body)
	var links []wire.RemoteIssueLink
	if err := decoder.Decode(&links); err != nil {
		return nil, fmt.Errorf("%w: the remote-link list could not be decoded: %w", core.ErrWeblinkResponseInvalid, err)
	}
	if links == nil {
		return nil, fmt.Errorf("%w: the response has no remote-link array", core.ErrWeblinkResponseInvalid)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("%w: the remote-link list contains trailing data: %w", core.ErrWeblinkResponseInvalid, err)
		}
		return nil, fmt.Errorf("%w: the remote-link list contains trailing data", core.ErrWeblinkResponseInvalid)
	}
	if err := core.ValidateRemoteLinkList(links); err != nil {
		return nil, err
	}
	return links, nil
}
