package jira

// RankRequest is the request body for PUT /rest/agile/1.0/issue/rank.
//
// **Every issue rides ONE request.** The endpoint takes an array, which
// is why `issue rank` performs no fan-out: the order the caller gave is
// the array's order, and that is what the server applies relative to the
// anchor. Sending the issues one at a time would make each move relative
// to a backlog the previous move had already changed.
//
// The two anchor fields are mutually exclusive — an issue cannot go both
// before and after another — and the CLI refuses the pair before it
// reaches here, so this type never has to decide which wins.
type RankRequest struct {
	// Issues are the issue keys to move, in the order they should end up.
	Issues []string `json:"issues"`
	// RankBeforeIssue places them immediately before this issue; empty
	// when the caller anchored the other way.
	RankBeforeIssue string `json:"rankBeforeIssue,omitempty"`
	// RankAfterIssue places them immediately after this issue.
	RankAfterIssue string `json:"rankAfterIssue,omitempty"`
}
