package core_test

import (
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestCloudRoutingPreservesSiteAndRefusesPathInjection(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"", "fixture-cloud", "  Fixture_Cloud.7  "} {
		got, err := core.JiraAPIBaseURL("https://example.invalid", id)
		want := "https://example.invalid"
		if id != "" {
			want = "https://api.atlassian.com/ex/jira/" + strings.TrimSpace(id)
		}
		if err != nil || got != want {
			t.Fatal("fixed gateway or classic site routing changed", err)
		}
	}
	for _, id := range []string{" ", ".", "..", "cloud/other", "%2e%2e", "cloud?query", "cloud#fragment", "cloud\\segment", "cloud\x00", "https://other.invalid", "cloud@other", "cloud:port"} {
		if got, err := core.JiraAPIBaseURL("https://example.invalid", id); err == nil || got != "" {
			t.Fatal("invalid cloud identifier entered an authenticated route")
		}
	}
}
