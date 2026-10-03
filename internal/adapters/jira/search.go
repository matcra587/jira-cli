package jira

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	wire "github.com/matcra587/jira-cli/internal/jira"
)

// ErrSearchResponseInvalid distinguishes upstream protocol failures from
// caller input. Malformed pages never establish an empty successful search.
var ErrSearchResponseInvalid = errors.New("search response invalid")

func decodeSearchPage(body io.Reader, maxResults int) (*wire.SearchResults, error) {
	var page wire.SearchResults
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&page); err != nil {
		return nil, fmt.Errorf("%w: decode page: %w", ErrSearchResponseInvalid, err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing response data", ErrSearchResponseInvalid)
	}
	// A zero request omits maxResults and uses the pinned server default.
	if maxResults == 0 {
		maxResults = 50
	}
	if err := page.ValidatePage(maxResults); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSearchResponseInvalid, err)
	}
	return &page, nil
}
