package core

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/matcra587/jira-cli/internal/jira"
)

// ErrWeblinkResponseInvalid identifies an unusable remote-link response.
// An unusable write acknowledgement requires inspection before retrying.
var ErrWeblinkResponseInvalid = errors.New("invalid remote-link response")

// ValidateRemoteLinkRequest checks the effective native input before transport.
// Optional values retain their authored presence; Jira owns their interpretation.
func ValidateRemoteLinkRequest(request jira.RemoteLinkRequest) (jira.RemoteIssueLinkRequest, error) {
	native, err := request.Native()
	if err != nil {
		return native, err
	}
	if !native.Object.Set || native.Object.Null {
		return native, errors.New("web link needs an object")
	}
	object := native.Object.Value
	if !object.URL.Set || object.URL.Null || !object.Title.Set || object.Title.Null {
		return native, errors.New("web link object needs string url and title members")
	}
	return native, ValidateRemoteLinkURL(object.URL.Value)
}

// ValidateRemoteLinkURL permits absolute HTTP and HTTPS targets without
// contacting them. Both command preflight and the adapter use this boundary.
func ValidateRemoteLinkURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("web link URL is malformed: %w", err)
	}
	if parsed.Hostname() == "" || (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
		return errors.New("web link URL must be an absolute HTTP or HTTPS URL with a host")
	}
	return nil
}

// ValidateRemoteLinkIdentity requires the positive native numeric identity
// needed to inspect or remove a link. It does not prove the stored content.
func ValidateRemoteLinkIdentity(link *jira.RemoteIssueLink) error {
	if link == nil || !link.ID.Set || link.ID.Null || link.ID.Value <= 0 {
		return fmt.Errorf("%w: the response has no positive native remote-link identity", ErrWeblinkResponseInvalid)
	}
	return nil
}

// ValidateRemoteLinkList requires distinct usable identities within one issue.
// The adapter separately distinguishes a native empty array from a null body.
func ValidateRemoteLinkList(links []jira.RemoteIssueLink) error {
	seen := make(map[int64]bool, len(links))
	for i := range links {
		if err := ValidateRemoteLinkIdentity(&links[i]); err != nil {
			return err
		}
		if seen[links[i].ID.Value] {
			return fmt.Errorf("%w: the list repeats a native remote-link identity", ErrWeblinkResponseInvalid)
		}
		seen[links[i].ID.Value] = true
	}
	return nil
}
