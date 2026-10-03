package cli

import (
	"net/url"
	"testing"
)

func TestSearchBrowseURLKeepsQueryInsideOneParameter(t *testing.T) {
	t.Parallel()
	query := `summary ~ "<&?=#%\\" AND project = KEY`
	link, ok := SearchBrowseURL("https://example.atlassian.net/jira?old=discard#fragment", query)
	parsed, err := url.Parse(link)
	if !ok || err != nil || parsed.Path != "/jira/issues/" || parsed.Fragment != "" || len(parsed.Query()) != 1 || parsed.Query().Get("jql") != query {
		t.Fatalf("query became URL structure: %q %v", link, err)
	}
}

func TestSearchBrowseURLPreservesEscapedBasePath(t *testing.T) {
	t.Parallel()
	link, ok := SearchBrowseURL("https://example.atlassian.net/jira%2Fspace", "key = KEY-1")
	if !ok || link != "https://example.atlassian.net/jira%2Fspace/issues/?jql=key+%3D+KEY-1" {
		t.Fatalf("encoded base path changed: %q", link)
	}
}

func TestSearchBrowseURLRejectsUnsafeOrIncompleteSite(t *testing.T) {
	t.Parallel()
	for _, site := range []string{"", "example.atlassian.net", "https://", "https://user:password@example.com", "javascript://example.com", "file://example.com", "https://%broken"} {
		if link, ok := SearchBrowseURL(site, "project = KEY"); ok || link != "" {
			t.Fatal("unsafe site produced a browser link")
		}
	}
}
