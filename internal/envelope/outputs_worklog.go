package envelope

import (
	"encoding/json"

	"github.com/matcra587/jira-cli/internal/jira"
)

// WorklogView publishes a stable identity and the complete native server
// record. Fields retains absent/null values, raw ADF and unknown extensions.
type WorklogView struct {
	ID     string       `json:"id"`
	Fields jira.Worklog `json:"fields"`
}

type WorklogListOutput struct{ KeyedResults[WorklogListData] }

type WorklogListData struct {
	Worklogs        []WorklogView    `json:"worklogs"`
	Pagination      OffsetPagination `json:"pagination"`
	Complete        bool             `json:"complete"`
	Truncated       bool             `json:"truncated"`
	TruncatedReason string           `json:"truncated_reason,omitempty"`
	// ReadError accompanies a retained validated prefix from earlier pages.
	ReadError *KeyError `json:"read_error,omitempty"`
}

type WorklogAddOutput struct {
	MutationState
	Payload        json.RawMessage `json:"payload"`
	Notify         bool            `json:"notify"`
	BodyRoute      string          `json:"body_route,omitempty"`
	WorkdaySeconds int             `json:"workday_seconds"`
	KeyedResults[WorklogAddData]
}

type WorklogAddData struct {
	Applied bool         `json:"applied"`
	Worklog *WorklogView `json:"worklog,omitempty"`
	// ReceiptError preserves a 201 acknowledgement whose body was unusable.
	ReceiptError *KeyError `json:"receipt_error,omitempty"`
}

func init() {
	Register("worklog.list", WorklogListOutput{}, map[string]any{"description": "Worklogs per issue with native records, offset pagination and explicit incomplete-read facts."})
	Register("worklog.add", WorklogAddOutput{}, map[string]any{"description": "Exact composed request, requested notification policy and ordered per-issue creation receipts. Previews invent no server record."})
}
