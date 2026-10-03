package jira

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// decodeIssueCreation requires one complete native object before publishing its
// identity. A trailing document or reader failure cannot establish a receipt.
func decodeIssueCreation(reader io.Reader) (core.IssueCreateWrite, error) {
	write := core.IssueCreateWrite{StatusCode: http.StatusCreated}
	decoder := json.NewDecoder(reader)
	var receipt json.RawMessage
	if err := decoder.Decode(&receipt); err != nil {
		return write, fmt.Errorf("%w: decoding acknowledged creation: %w", core.ErrIssueCreateResponseInvalid, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return write, fmt.Errorf("%w: reading creation response end: %w", core.ErrIssueCreateResponseInvalid, err)
		}
		return write, fmt.Errorf("%w: unexpected trailing creation response", core.ErrIssueCreateResponseInvalid)
	}
	if bytes.HasPrefix(bytes.TrimSpace(receipt), []byte("{")) {
		write.Receipt = receipt
	}
	var issue *wire.CreatedIssue
	if err := json.Unmarshal(receipt, &issue); err != nil {
		return write, fmt.Errorf("%w: decoding issue identity: %w", core.ErrIssueCreateResponseInvalid, err)
	}
	write.Issue = issue
	if err := core.ValidateIssueCreation(write); err != nil {
		write.Issue = nil
		return write, err
	}
	return write, core.ValidateIssueCreateFollowups(receipt)
}
