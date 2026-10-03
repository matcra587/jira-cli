// Package jira is the REST adapter behind core.JiraClient: it owns the
// HTTP transport to a Jira Cloud site — request building, auth, the
// safe-retry loop, and the typed APIError — and decodes responses into the
// wire types in internal/jira. Import it aliased (adapterjira) wherever
// the wire package is also in scope.
//
// Request bodies use the wire package's MarshalNoEscape so HTML characters,
// line separators and raw JSON string escapes survive serialization. Plain
// encoding/json, even with HTML escaping off, is not that boundary.
package jira
