package envelope

import "github.com/matcra587/jira-cli/internal/jira"

// UserSearchMatch is a usable account identity, never an automatically selected
// assignee. Email remains nullable because privacy settings can hide it.
type UserSearchMatch struct {
	AccountID    string                `json:"account_id"`
	DisplayName  string                `json:"display_name"`
	EmailAddress jira.Nullable[string] `json:"email_address,omitzero"`
}

func (UserSearchMatch) ref() {}

// UserSearchPagination describes observations of a bare-array API. Complete
// means an empty page was observed, within this endpoint's search window; it
// never asserts directory completeness or permission to see every account.
type UserSearchPagination struct {
	StartAt     int  `json:"start_at"`
	MaxResults  int  `json:"max_results"`
	Fetched     int  `json:"fetched"`
	NextStart   *int `json:"next_start,omitempty"`
	Complete    bool `json:"complete"`
	SearchLimit int  `json:"search_limit"`
}

// UserSearchOutput retains validated pages if a later page fails. Skipped
// counts rows without explicit active=true or with explicit deleted=true.
type UserSearchOutput struct {
	Query           string               `json:"query"`
	Users           []UserSearchMatch    `json:"users"`
	Count           int                  `json:"count"`
	Skipped         int                  `json:"skipped"`
	Pagination      UserSearchPagination `json:"pagination"`
	Truncated       bool                 `json:"truncated"`
	TruncatedReason string               `json:"truncated_reason,omitempty"`
}

func init() { Register("user.search", UserSearchOutput{}, nil) }
