package envelope

import (
	"encoding/json"

	"github.com/matcra587/jira-cli/internal/jira"
)

// BulkTaskRef identifies a submitted asynchronous job, not an issue outcome.
type BulkTaskRef struct {
	ID string `json:"id"`
}

func (BulkTaskRef) ref() {}

type IssueMoveTarget struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

// BulkTaskState retains the last validated observation when a later read,
// cancellation or polling bound stops the command. Terminal is a job fact;
// processedAccessibleIssues is Jira's separate per-issue success report.
type BulkTaskState struct {
	Task     *BulkTaskRef                `json:"task,omitempty"`
	Progress *jira.BulkOperationProgress `json:"progress,omitempty"`
	// Polls counts progress client calls, including failed calls. Transport
	// retries of a safe read do not count as additional observations.
	Polls            int  `json:"polls"`
	Terminal         bool `json:"terminal"`
	PollLimitReached bool `json:"poll_limit_reached"`
}

type IssueMoveOutput struct {
	MutationState
	BulkTaskState
	Payload           json.RawMessage              `json:"payload"`
	Targets           []IssueMoveTarget            `json:"targets"`
	BindingsValidated bool                         `json:"bindings_validated"`
	RemoteValidated   bool                         `json:"remote_validated"`
	Attempted         bool                         `json:"attempted"`
	Acknowledged      bool                         `json:"acknowledged"`
	Receipt           *jira.SubmittedBulkOperation `json:"receipt,omitempty"`
	ReceiptError      *KeyError                    `json:"receipt_error,omitempty"`
	KeyedResults[IssueMoveData]
}

type IssueMoveData struct {
	// Applied is true only when a terminal progress receipt explicitly names
	// this issue ID as successfully processed. Acceptance alone keeps it false.
	Applied     bool   `json:"applied"`
	Outcome     string `json:"outcome"`
	Destination string `json:"destination,omitempty"`
}

type IssueMoveStatusOutput struct{ BulkTaskState }

func init() {
	Register("issue.move", IssueMoveOutput{}, map[string]any{"description": "Native bulk move request, explicit target binding, acknowledgement and separately observed per-issue outcomes. A task receipt is not completed movement."})
	Register("issue.move.status", IssueMoveStatusOutput{}, map[string]any{"description": "Read or resume bounded observations of an existing bulk task without submitting a move."})
}
