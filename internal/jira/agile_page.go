package jira

import "encoding/json"

// AgilePage is the offset page used by the board and associated-project
// endpoints in the pinned Agile spec. A total is optional; absence must never
// become a fabricated zero. Completion comes from an explicit isLast value.
// Nullable also keeps an absent/null values member distinct from an empty page.
// Extensions use the same raw-capture owner as platform responses.
type AgilePage[T any] struct {
	IsLast     Nullable[bool]             `json:"isLast,omitzero"`
	MaxResults Nullable[int]              `json:"maxResults,omitzero"`
	StartAt    Nullable[int]              `json:"startAt,omitzero"`
	Total      Nullable[int]              `json:"total,omitzero"`
	Values     Nullable[[]T]              `json:"values,omitzero"`
	Extra      map[string]json.RawMessage `json:"-"`
}

type agilePageAlias[T any] AgilePage[T]

var agilePageKeys = []string{"isLast", "maxResults", "startAt", "total", "values"}

func (v AgilePage[T]) MarshalJSON() ([]byte, error) {
	return marshalMergeExtra(agilePageAlias[T](v), v.Extra, agilePageKeys)
}

// UnmarshalJSON replaces the receiver only after a successful decode. Reusing
// a page cannot retain pagination facts or extensions from a previous read.
func (v *AgilePage[T]) UnmarshalJSON(data []byte) error {
	var next agilePageAlias[T]
	if err := unmarshalKeepExtra(data, &next, &next.Extra, agilePageKeys); err != nil {
		return err
	}
	*v = AgilePage[T](next)
	return nil
}
