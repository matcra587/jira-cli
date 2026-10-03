package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/matcra587/jira-cli/internal/jira"
)

// ErrTransitionResponseInvalid identifies an unacknowledged workflow write.
// The request may have landed; inspect the issue before repeating it.
var ErrTransitionResponseInvalid = errors.New("invalid transition acknowledgement")

// ErrTransitionReadInvalid identifies unusable workflow discovery data.
// A failed discovery read must never become permission to perform a transition.
var ErrTransitionReadInvalid = errors.New("invalid transition response")

// ValidateTransitions requires an explicit list, distinct identities and known
// screen state. Native field metadata remains intact, including extensions.
func ValidateTransitions(list *jira.Transitions) error {
	if list == nil || !list.Transitions.Set || list.Transitions.Null || list.Transitions.Value == nil {
		return fmt.Errorf("%w: no explicit transitions array", ErrTransitionReadInvalid)
	}
	seen := make(map[string]struct{}, len(list.Transitions.Value))
	for _, transition := range list.Transitions.Value {
		id := transition.ID.Value
		trimmedID := strings.TrimSpace(id)
		if !transition.ID.Set || transition.ID.Null || trimmedID == "" || id != trimmedID {
			return fmt.Errorf("%w: transition has no usable identity", ErrTransitionReadInvalid)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("%w: transition identity is repeated", ErrTransitionReadInvalid)
		}
		seen[id] = struct{}{}
		if !transition.HasScreen.Set || transition.HasScreen.Null {
			return fmt.Errorf("%w: transition has no explicit screen state", ErrTransitionReadInvalid)
		}
		if transition.Fields.Null || (transition.Fields.Set && transition.Fields.Value == nil) {
			return fmt.Errorf("%w: transition fields metadata is null", ErrTransitionReadInvalid)
		}
		for name, raw := range transition.Fields.Value {
			var field map[string]json.RawMessage
			if strings.TrimSpace(name) == "" || json.Unmarshal(raw, &field) != nil || field == nil {
				return fmt.Errorf("%w: transition field metadata is not a named object", ErrTransitionReadInvalid)
			}
			if required, exists := field["required"]; exists {
				var value bool
				if string(required) == "null" || json.Unmarshal(required, &value) != nil {
					return fmt.Errorf("%w: transition field required state is not a boolean", ErrTransitionReadInvalid)
				}
			}
		}
	}
	return nil
}
