package envelope

import "github.com/matcra587/jira-cli/internal/jira"

// BoardRow is a visible board identity with independently fetched associated
// projects. Type preserves absence/null; incomplete project keys are explicit.
type BoardRow struct {
	ID                int64                 `json:"id"`
	Name              string                `json:"name"`
	Type              jira.Nullable[string] `json:"type,omitzero"`
	ProjectKeys       []string              `json:"project_keys"`
	ProjectsComplete  bool                  `json:"projects_complete"`
	ProjectsNextStart *int                  `json:"projects_next_start,omitempty"`
}

func (BoardRow) ref() {}

// BoardProjectFailure retains a board's enrichment failure without hiding its
// identity or claiming an empty project relation. Error uses shared taxonomy.
type BoardProjectFailure struct {
	BoardID int64 `json:"board_id"`
	Error   Error `json:"error"`
}

// BoardsPagination is a client-side window over the fetched/cache snapshot.
// Total counts that snapshot, not all boards Jira might expose after truncation.
// Complete describes the server walk; IsLast describes only this output window.
type BoardsPagination struct {
	MaxResults int  `json:"max_results"`
	Total      int  `json:"total"`
	IsLast     bool `json:"is_last"`
	Complete   bool `json:"complete"`
}

type BoardsListOutput struct {
	Boards            []BoardRow            `json:"boards"`
	Count             int                   `json:"count"`
	Pagination        BoardsPagination      `json:"pagination"`
	FromCache         bool                  `json:"from_cache"`
	FetchedAt         string                `json:"fetched_at"`
	CacheState        string                `json:"cache_state"`
	CacheSourceState  string                `json:"cache_source_state"`
	CacheEmpty        bool                  `json:"cache_empty"`
	CacheSaved        bool                  `json:"cache_saved"`
	PagesFetched      int                   `json:"pages_fetched"`
	Truncated         bool                  `json:"truncated"`
	TruncatedReason   string                `json:"truncated_reason,omitempty"`
	NextStart         *int                  `json:"next_start,omitempty"`
	RetryAfterSeconds int                   `json:"retry_after_seconds,omitempty"`
	ProjectFailures   []BoardProjectFailure `json:"project_failures"`
}

func init() { Register("boards.list", BoardsListOutput{}, nil) }
