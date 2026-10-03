package envelope

import (
	"time"

	"github.com/matcra587/jira-cli/internal/jira"
)

// JQLBuildOutput is local composition, not a claim of server validation.
// URL is empty when site preferences cannot supply a usable browser link.
type JQLBuildOutput struct {
	JQL        string        `json:"jql"`
	URL        string        `json:"url"`
	Precedence string        `json:"precedence"`
	BoardScope JQLBuildBoard `json:"board_scope"`
}

// JQLBuildBoard reports the exact saved filter and the snapshot's timestamp.
// An offline snapshot can be stale and does not prove current board membership.
type JQLBuildBoard struct {
	Applied  bool      `json:"applied"`
	ID       int64     `json:"id,omitzero"`
	Name     string    `json:"name,omitempty"`
	JQL      string    `json:"jql,omitempty"`
	CachedAt time.Time `json:"cached_at,omitzero"`
}

// JQLValidateOutput keeps authored queries separate from Jira's normalized
// query text. Parsed is the complete native response, including raw syntax
// trees and future members; Queries retains the convenient validity contract.
type JQLValidateOutput struct {
	Mode             string                `json:"mode"`
	SubmittedQueries []string              `json:"submitted_queries"`
	Queries          []JQLValidateEntry    `json:"queries"`
	Parsed           jira.ParsedJqlQueries `json:"parsed"`
}

// Valid means Jira returned no errors, not that warnings are absent or the
// query has matches. Mode none skips semantic validation, not syntax parsing.
type JQLValidateEntry struct {
	Query    string                  `json:"query"`
	Valid    bool                    `json:"valid"`
	Errors   jira.Nullable[[]string] `json:"errors,omitzero"`
	Warnings jira.Nullable[[]string] `json:"warnings,omitzero"`
}

// JQLReferenceOutput offers field/function discovery alongside the complete
// live native metadata. Optional and null values remain distinct in Reference.
type JQLReferenceOutput struct {
	Fields        []JQLReferenceField     `json:"fields"`
	Functions     []JQLReferenceFunction  `json:"functions"`
	ReservedWords jira.Nullable[[]string] `json:"reserved_words,omitzero"`
	Reference     jira.JQLReferenceData   `json:"reference"`
}

type JQLReferenceField struct {
	Value         jira.Nullable[string] `json:"value,omitzero"`
	DisplayName   jira.Nullable[string] `json:"display_name,omitzero"`
	CustomFieldID jira.Nullable[string] `json:"custom_field_id,omitzero"`
}

type JQLReferenceFunction struct {
	Value       jira.Nullable[string] `json:"value,omitzero"`
	DisplayName jira.Nullable[string] `json:"display_name,omitzero"`
}

func init() {
	Register("jql.build", JQLBuildOutput{}, map[string]any{"description": "Locally composed JQL and an optional browser URL, with explicit cached board-filter provenance. Composition does not establish server validity or matching issues."})
	Register("jql.validate", JQLValidateOutput{}, map[string]any{"description": "Server parser diagnostics and per-query validity, with original submitted queries and the complete native parse response."})
	Register("jql.reference", JQLReferenceOutput{}, map[string]any{"description": "Live site-visible JQL fields, functions and reserved words, with complete native metadata."})
}
