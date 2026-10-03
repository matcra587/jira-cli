package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrIssueCreateResponseInvalid reports an uncertain or unusable creation receipt.
// Inspect the project before repeating a create; Jira may already have made it.
var ErrIssueCreateResponseInvalid = errors.New("invalid issue creation response")

// ErrIssueCreateFollowupFailed reports creation with a failed native followup.
// The created identity remains usable; retrying creation makes another issue.
var ErrIssueCreateFollowupFailed = errors.New("issue created with a failed followup")

// ValidateIssueCreation requires the native acknowledgement and a usable identity.
// Self is an optional URL, not the identity used to inspect the created issue.
func ValidateIssueCreation(write IssueCreateWrite) error {
	if write.StatusCode != http.StatusCreated {
		return fmt.Errorf("%w: expected HTTP 201 acknowledgement", ErrIssueCreateResponseInvalid)
	}
	if write.Issue == nil || strings.TrimSpace(write.Issue.ID) == "" || strings.TrimSpace(write.Issue.Key) == "" {
		return fmt.Errorf("%w: Jira acknowledged creation without a usable issue ID and key", ErrIssueCreateResponseInvalid)
	}
	return nil
}

// ValidateIssueCreateFollowups inspects optional native transition and watcher
// outcomes without discarding any response members. A missing receipt is valid
// for alternate client implementations that return only the identity.
func ValidateIssueCreateFollowups(receipt json.RawMessage) error {
	if len(receipt) == 0 {
		return nil
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(receipt, &members); err != nil {
		return fmt.Errorf("%w: decoding native followups: %w", ErrIssueCreateResponseInvalid, err)
	}
	if members == nil {
		return fmt.Errorf("%w: native receipt must be an object", ErrIssueCreateResponseInvalid)
	}
	var failures []error
	for _, name := range []string{"transition", "watchers"} {
		value, exists := members[name]
		if !exists {
			continue
		}
		var outcome *struct {
			Status *int `json:"status"`
		}
		if err := json.Unmarshal(value, &outcome); err != nil {
			return fmt.Errorf("%w: decoding %s outcome: %w", ErrIssueCreateResponseInvalid, name, err)
		}
		if outcome == nil || outcome.Status == nil || *outcome.Status < 100 || *outcome.Status > 599 {
			return fmt.Errorf("%w: %s outcome has no usable HTTP status", ErrIssueCreateResponseInvalid, name)
		}
		if *outcome.Status < 200 || *outcome.Status >= 300 {
			failures = append(failures, fmt.Errorf("%w: %s returned HTTP %d", ErrIssueCreateFollowupFailed, name, *outcome.Status))
		}
	}
	return errors.Join(failures...)
}
