package selfupdate

import (
	"context"
	"net/http"

	"github.com/gechr/clive"
)

// ReleaseTool gives Conductor's Clive notifier the same anonymous, published
// stable release policy as the update command. It performs no work until Clive
// schedules a bounded background refresh and never installs anything.
type ReleaseTool struct{}

// BinaryName identifies the executable and Clive's JIRA_NO_UPDATE_CHECK switch.
func (ReleaseTool) BinaryName() string { return "jira" }

// DisplayName identifies the tool in Clive's terminal hint.
func (ReleaseTool) DisplayName() string { return "jira-cli" }

// VersionLink uses Clive's release-link renderer after version validation.
func (ReleaseTool) VersionLink(value string) string {
	if err := validateVersion(value); err != nil {
		return ""
	}
	return (clive.Info{Module: Module}).VersionLink(value)
}

// LatestRef reads a published release rather than a tag that may have no assets.
func (ReleaseTool) LatestRef(ctx context.Context, client *http.Client) (string, error) {
	source, err := NewGitHubSource(client)
	if err != nil {
		return "", err
	}
	release, err := source.Latest(ctx)
	if err != nil {
		return "", err
	}
	return release.Version, nil
}
