package jira

// RankRequest is the request body for PUT /rest/agile/1.0/issue/rank.
//
// Each request carries one ordered batch of at most fifty issues. The array
// order is the order applied relative to this request's anchor. CLI planning
// validates larger moves first and chains separate requests sequentially;
// this native request type never performs chunking or replays a write.
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
