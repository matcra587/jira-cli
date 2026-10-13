package core

// IssuePreferences carries public working context without credentials. Runtime
// configuration stays flat; these values never select another account or profile.
type IssuePreferences struct {
	BaseURL string
	Account string
	// CachedAccountID is public, scope-checked metadata for local previews only.
	// It never establishes the identity of an authenticated client.
	CachedAccountID  string
	DefaultProject   string
	DefaultIssueType string
	DefaultBoard     string
}

// JiraAccountClient exposes the public account actually bound to a constructed
// client. A cached account-scoped query must not infer this from file settings
// after credential resolution, or pay for another remote identity request.
type JiraAccountClient interface {
	Account() string
}
