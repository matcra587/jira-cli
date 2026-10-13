package core

import (
	"errors"
	"strings"

	xstrings "github.com/gechr/x/strings"
)

// ValidateCloudID checks public routing metadata. Empty selects classic site
// routing; a scoped ID must remain one opaque gateway path segment.
func ValidateCloudID(id string) error {
	if id == "" {
		return nil
	}
	trimmed := strings.TrimSpace(id)
	if trimmed == "" || trimmed == "." || trimmed == ".." {
		return errors.New("cloud_id must be empty or a path-safe cloud identifier")
	}
	for _, character := range trimmed {
		if !xstrings.IsAlphanumericChar(character) && character != '-' && character != '_' && character != '.' {
			return errors.New("cloud_id must be empty or a path-safe cloud identifier")
		}
	}
	return nil
}

// JiraAPIBaseURL selects the sole supported scoped-token gateway. The site URL
// remains the browser and credential identity; this function never discovers it
// or accepts another authenticated host.
func JiraAPIBaseURL(site, cloudID string) (string, error) {
	if err := ValidateCloudID(cloudID); err != nil {
		return "", err
	}
	if cloudID == "" {
		return site, nil
	}
	return "https://api.atlassian.com/ex/jira/" + strings.TrimSpace(cloudID), nil
}
