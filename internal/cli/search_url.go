package cli

import "net/url"

// SearchBrowseURL builds Jira's issue navigator URL, with the entire query
// encoded as one jql parameter. It preserves a self-hosted site's base path.
// Invalid sites and embedded credentials produce no link. A site fragment
// cannot redirect the navigator, and only HTTP(S) links are supported.
func SearchBrowseURL(site, jql string) (string, bool) {
	base, err := url.Parse(site)
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil {
		return "", false
	}
	link := base.JoinPath("issues/")
	link.Fragment, link.RawFragment = "", ""
	link.RawQuery = url.Values{"jql": {jql}}.Encode()
	return link.String(), true
}
