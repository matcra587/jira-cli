package selfupdate

import (
	"errors"
	"net/http"
	"testing"
)

func TestNotificationUsesPublishedReleasePolicy(t *testing.T) {
	fixture := newReleaseFixture(t, "linux", "amd64", []byte("unused archive"))
	tool := ReleaseTool{}
	// Reuse the selected source's trusted fixture transport without changing
	// the production repository URL, anonymous policy or metadata bounds.
	client := &http.Client{Transport: fixture.source.assets.Transport}
	latest, err := tool.LatestRef(t.Context(), client)
	if err != nil || latest != "v1.2.3" {
		t.Fatalf("notification release = %q err=%v", latest, err)
	}
	fixture.assertRequestCount(t, "/releases/latest", 1)
	fixture.assertRequestCount(t, "/tags", 0)
	fixture.changeMetadata(t, func(metadata map[string]any) { metadata["draft"] = true })
	if latest, err := tool.LatestRef(t.Context(), client); latest != "" || !errors.Is(err, ErrReleaseInvalid) {
		t.Fatalf("draft notification = %q err=%v", latest, err)
	}
	if tool.BinaryName() != "jira" || tool.DisplayName() != "jira-cli" || tool.VersionLink("v1.2.3") == "" || tool.VersionLink("bad\x1bversion") != "" {
		t.Fatal("notification identity or unsafe version rendering changed")
	}
}
