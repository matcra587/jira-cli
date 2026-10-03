package envelope

import "encoding/json"

func init() {
	Register("epic.add", EpicMembershipOutput{}, nil)
	Register("epic.remove", EpicMembershipOutput{}, nil)
}

// EpicMembershipOutput describes one fixed parent edit for explicit child keys.
// Payload is the composed Jira request, identical on preview and live paths.
type EpicMembershipOutput struct {
	MutationState
	Epic    string          `json:"epic,omitempty"`
	Notify  bool            `json:"notify"`
	Payload json.RawMessage `json:"payload"`
	KeyedResults[EpicMembershipData]
}

type EpicMembershipData struct {
	// Applied means Jira acknowledged the PUT, never merely a preview or read.
	Applied           bool            `json:"applied"`
	RemoteValidated   bool            `json:"remote_validated"`
	AlreadyUnparented bool            `json:"already_unparented"`
	Parent            *EpicParentRead `json:"parent,omitempty"`
	VerifyFailed      bool            `json:"verify_failed"`
	VerificationError *KeyError       `json:"verification_error,omitempty"`
}

// EpicParentRead preserves absent versus explicit null versus an object. The
// read requests parent explicitly; absent and null both mean no visible parent.
type EpicParentRead struct {
	State string `json:"state"`
	Key   string `json:"key,omitempty"`
}
