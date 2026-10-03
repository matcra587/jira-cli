package envelope

func init() {
	Register("epic.list", EpicListOutput{}, nil)
	Register("epic.board", EpicBoardOutput{}, nil)
}

// EpicListOutput preserves the issue identity and fields returned by search.
// Pagination and truncation have the same meaning as issue.list.
type EpicListOutput struct {
	Epics           []IssueView      `json:"epics"`
	JQL             string           `json:"jql"`
	Pagination      CursorPagination `json:"pagination"`
	Truncated       bool             `json:"truncated"`
	TruncatedReason string           `json:"truncated_reason,omitempty"`
}

// EpicBoardOutput aggregates visible child issues by their actual status name.
// Totals cover only the returned epics and validated child pages. CountsComplete
// also requires the epic search to be exhausted, never merely a satisfied limit.
// Jira's search index can lag recent edits even when every search is exhausted.
type EpicBoardOutput struct {
	Epics           []EpicBoardRow   `json:"epics"`
	JQL             string           `json:"jql"`
	Pagination      CursorPagination `json:"pagination"`
	Truncated       bool             `json:"truncated"`
	TruncatedReason string           `json:"truncated_reason,omitempty"`
	// Status names are tenant-defined, so these maps deliberately remain open.
	Totals         map[string]int `json:"totals"`
	UnknownStatus  int            `json:"unknown_status"`
	CountsComplete bool           `json:"counts_complete"`
}

type EpicBoardRow struct {
	IssueView
	// Counts is null until the child search is attempted. An attempted search
	// always includes the baseline To Do, In Progress and Done zero buckets.
	Counts   map[string]int    `json:"counts"`
	Children EpicChildrenState `json:"children"`
}

// EpicChildrenState makes incomplete, unattempted and failed counts explicit.
// UnknownStatus counts fetched children without a usable status name.
type EpicChildrenState struct {
	Attempted       bool             `json:"attempted"`
	Complete        bool             `json:"complete"`
	Pagination      CursorPagination `json:"pagination"`
	Truncated       bool             `json:"truncated"`
	TruncatedReason string           `json:"truncated_reason,omitempty"`
	UnknownStatus   int              `json:"unknown_status"`
	Error           *Error           `json:"error,omitempty"`
}
