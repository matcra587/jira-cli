package jira

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// decodeTransitions accepts one complete native workflow document. Reader
// failures keep their original cause; incomplete documents are not published.
func decodeTransitions(body io.Reader) (*wire.Transitions, error) {
	decoder := json.NewDecoder(body)
	var list *wire.Transitions
	if err := decoder.Decode(&list); err != nil {
		return nil, fmt.Errorf("%w: workflow response could not be decoded: %w", core.ErrTransitionReadInvalid, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("%w: workflow response contains trailing data: %w", core.ErrTransitionReadInvalid, err)
		}
		return nil, fmt.Errorf("%w: workflow response contains trailing data", core.ErrTransitionReadInvalid)
	}
	if err := core.ValidateTransitions(list); err != nil {
		return nil, err
	}
	return list, nil
}
