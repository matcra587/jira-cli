package jira

import "encoding/json"

// BoardPage is one page of GET /rest/agile/1.0/board.
type BoardPage = AgilePage[Board]

// Board is an Agile board, modeled from the pinned agile.json getAllBoards
// response. gen-wire currently reads the platform document; these few Agile
// fields remain handwritten, with raw capture for location, permissions,
// privacy and future members rather than silently discarding them.
type Board struct {
	ID    Nullable[int64]            `json:"id,omitzero"`
	Name  Nullable[string]           `json:"name,omitzero"`
	Type  Nullable[string]           `json:"type,omitzero"`
	Extra map[string]json.RawMessage `json:"-"`
}

type boardAlias Board

var boardKeys = []string{"id", "name", "type"}

func (v Board) MarshalJSON() ([]byte, error) {
	return marshalMergeExtra(boardAlias(v), v.Extra, boardKeys)
}

func (v *Board) UnmarshalJSON(data []byte) error {
	var next boardAlias
	if err := unmarshalKeepExtra(data, &next, &next.Extra, boardKeys); err != nil {
		return err
	}
	*v = Board(next)
	return nil
}

// BoardProjectPage is a page of projects associated with a board's filter or
// issues: GET /rest/agile/1.0/board/{boardId}/project. This is deliberately not
// the /project/full endpoint, whose static-association semantics differ.
type BoardProjectPage = AgilePage[BoardProject]

// BoardProject preserves the endpoint's project identity and display name.
// Its id is a string in the pinned response example, unlike a board's id.
type BoardProject struct {
	ID    Nullable[string]           `json:"id,omitzero"`
	Key   Nullable[string]           `json:"key,omitzero"`
	Name  Nullable[string]           `json:"name,omitzero"`
	Extra map[string]json.RawMessage `json:"-"`
}

type boardProjectAlias BoardProject

var boardProjectKeys = []string{"id", "key", "name"}

func (v BoardProject) MarshalJSON() ([]byte, error) {
	return marshalMergeExtra(boardProjectAlias(v), v.Extra, boardProjectKeys)
}

func (v *BoardProject) UnmarshalJSON(data []byte) error {
	var next boardProjectAlias
	if err := unmarshalKeepExtra(data, &next, &next.Extra, boardProjectKeys); err != nil {
		return err
	}
	*v = BoardProject(next)
	return nil
}

// BoardConfiguration is the part of GET
// /rest/agile/1.0/board/{boardId}/configuration this CLI reads.
type BoardConfiguration struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Filter is the saved filter the board's contents come from. This one
	// field is the whole reason this type exists: a board IS its filter's
	// query plus a column layout, and the query is the half that answers
	// "which issues are on this board".
	Filter BoardFilter `json:"filter"`
	// SubQuery is a Kanban board's extra clause, ANDed with the filter's
	// query by Jira itself to hide done work from the board.
	//
	// Read and DELIBERATELY NOT APPLIED — see resolveBoardJQL, which
	// records the reasoning. It is modeled so that the decision not to
	// use it is visible here rather than looking like an oversight.
	SubQuery BoardSubQuery `json:"subQuery"`
}

// BoardFilter identifies the saved filter behind a board. Id is a
// STRING in the spec even though a board's own id is a number, which is
// the spec's inconsistency rather than this model's.
type BoardFilter struct {
	ID string `json:"id"`
}

// BoardSubQuery is a Kanban board's supplementary JQL clause.
type BoardSubQuery struct {
	Query string `json:"query"`
}
