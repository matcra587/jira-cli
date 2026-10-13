package core

// JiraScope is the public routing identity of one constructed Jira client.
// The API base distinguishes scoped gateway tenants from browser/site identity.
// It carries no token and does not cause credential resolution or remote reads.
type JiraScope struct {
	Site       string
	Account    string
	APIBaseURL string
}

// JiraScopedClient reports the immutable scope actually used by its transport.
type JiraScopedClient interface {
	JiraScope() JiraScope
}
