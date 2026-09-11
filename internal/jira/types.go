package jira

import (
	"encoding/json"
	"maps"
	"reflect"
	"strings"

	"github.com/matcra587/jira-cli/internal/adf"
)

// String, Int, and Bool return pointers to their argument. The wire types in
// this package use pointer fields to distinguish an absent field from a
// zero-valued one (see the omitempty tags), so building a request payload needs
// a way to take the address of a literal — these are that helper.
//
//go:fix inline
func String(v string) *string { return new(v) }

// Int returns a pointer to v. See String for why these helpers exist.
//
//go:fix inline
func Int(v int) *int { return new(v) }

// Bool returns a pointer to v. See String for why these helpers exist.
//
//go:fix inline
func Bool(v bool) *bool { return new(v) }

// Issue is the core issue resource returned by the issue and search endpoints.
// Jira nests most data under fields, but this struct also hoists the commonly
// needed collections (comments, worklogs, linked issues, subtasks) to the top
// level: UnmarshalJSON copies them out of Fields so callers reach them without
// knowing Jira's expand nesting. Nearly every field is a pointer or slice so an
// absent field stays distinguishable from an empty one.
type Issue struct {
	ID           *string      `json:"id,omitempty"`
	Key          *string      `json:"key,omitempty"`
	Self         *string      `json:"self,omitempty"`
	Fields       *IssueFields `json:"fields,omitempty"`
	Comments     []*Comment   `json:"comments,omitempty"`
	Worklogs     []*Worklog   `json:"worklogs,omitempty"`
	LinkedIssues []*Issue     `json:"linked_issues,omitempty"`
	Subtasks     []*Issue     `json:"subtasks,omitempty"`
	// Transitions carries the workflow transitions Jira returns under
	// expand=transitions — the moves valid from the issue's current status.
	// Populated by issue view so a read answers "what can I transition to"
	// without a second call.
	Transitions []*Transition `json:"transitions,omitempty"`
	// EditMeta carries the edit-screen metadata Jira returns under
	// expand=editmeta — which fields are editable, whether they are
	// required, and their allowed values. Populated by issue view so an
	// edit can be planned from the same read.
	EditMeta *EditMeta `json:"editmeta,omitempty"`
}

// IssueFields is the fields object of an issue. System fields are modeled
// explicitly; every customfield_* key Jira returns is captured into
// CustomFields by the custom UnmarshalJSON (the `json:"-"` tag keeps the
// standard decoder from touching it), and every remaining unmodeled system
// key (duedate, resolutiondate, votes, …) is captured into Extra the same
// way. MarshalJSON splices both back so a round-trip preserves the whole
// wire fields block, not just the keys the struct names — dropping the rest
// is exactly the bug that made `created` unreachable before it was modeled.
type IssueFields struct {
	Summary      *string                    `json:"summary,omitempty"`
	IssueType    *IssueType                 `json:"issuetype,omitempty"`
	Description  *adf.Document              `json:"description,omitempty"`
	Status       *Status                    `json:"status,omitempty"`
	Assignee     *User                      `json:"assignee,omitempty"`
	Reporter     *User                      `json:"reporter,omitempty"`
	Priority     *Priority                  `json:"priority,omitempty"`
	Labels       []string                   `json:"labels,omitempty"`
	Components   []Component                `json:"components,omitempty"`
	Parent       *Issue                     `json:"parent,omitempty"`
	FixVersions  []Version                  `json:"fixVersions,omitempty"`
	Versions     []Version                  `json:"versions,omitempty"`
	Created      *string                    `json:"created,omitempty"`
	Updated      *string                    `json:"updated,omitempty"`
	Comment      *CommentPage               `json:"comment,omitempty"`
	Worklog      *WorklogPage               `json:"worklog,omitempty"`
	IssueLinks   []IssueLink                `json:"issuelinks,omitempty"`
	Subtasks     []*Issue                   `json:"subtasks,omitempty"`
	CustomFields map[string]json.RawMessage `json:"-"`
	Extra        map[string]json.RawMessage `json:"-"`
}

// CommentPage is the paged comment container Jira nests under fields.comment on
// a GET issue. Issue.UnmarshalJSON lifts its Comments to the top level.
type CommentPage struct {
	Comments []*Comment `json:"comments,omitempty"`
}

// WorklogPage is the paged worklog container Jira nests under fields.worklog on
// a GET issue. Issue.UnmarshalJSON lifts its Worklogs to the top level.
type WorklogPage struct {
	Worklogs []*Worklog `json:"worklogs,omitempty"`
}

// IssueLink is one entry of an issue's issuelinks array. Exactly one of
// InwardIssue / OutwardIssue is set per entry, naming the other end of the link
// and encoding the direction by which slot it occupies.
type IssueLink struct {
	InwardIssue  *Issue `json:"inwardIssue,omitempty"`
	OutwardIssue *Issue `json:"outwardIssue,omitempty"`
}

// Status is an issue's workflow status. Name is the project-local label;
// StatusCategory is the cross-project bucket used for coloring (see
// StatusCategory).
type Status struct {
	Name           *string         `json:"name,omitempty"`
	StatusCategory *StatusCategory `json:"statusCategory,omitempty"`
}

// StatusCategory is the workflow bucket a status belongs to. Key is one of
// "new", "indeterminate", or "done" — stable across projects and used to
// color statuses by category in human output. ColorName is Jira's own color
// designation for the category ("blue-gray", "yellow", "green", "medium-gray"),
// preferred over Key when coloring so the badge matches the Jira UI.
type StatusCategory struct {
	Key       *string `json:"key,omitempty"`
	Name      *string `json:"name,omitempty"`
	ColorName *string `json:"colorName,omitempty"`
}

// Priority is an issue's priority. Only the name is modeled — it is the value
// the CLI displays and submits by.
type Priority struct {
	Name *string `json:"name,omitempty"`
}

// IssueType is the issue's type (Epic, Story, Task, Bug, Sub-task, ...).
type IssueType struct {
	Name    *string `json:"name,omitempty"`
	IconURL *string `json:"iconUrl,omitempty"`
	Subtask *bool   `json:"subtask,omitempty"`
}

// User is a Jira Cloud account as it appears embedded in an issue (assignee,
// reporter, comment author). AccountID is the only stable identifier — display
// name and email are subject to the account's privacy settings and are often
// absent, so callers must tolerate nil rather than key off them. AccountType
// distinguishes atlassian, app, and customer accounts.
type User struct {
	AccountID    *string `json:"accountId,omitempty"`
	AccountType  *string `json:"accountType,omitempty"`
	DisplayName  *string `json:"displayName,omitempty"`
	EmailAddress *string `json:"emailAddress,omitempty"`
	Active       *bool   `json:"active,omitempty"`
	Deleted      *bool   `json:"deleted,omitempty"`
}

// Component is a project component reference on an issue. Only the name is
// modeled — the identity the CLI displays and diffs by.
type Component struct {
	Name *string `json:"name,omitempty"`
}

// Version is a project version reference as it appears in an issue's
// fixVersions / versions arrays. Only the name is modeled — it is the
// identity the CLI submits and diffs by.
type Version struct {
	Name *string `json:"name,omitempty"`
}

// Epic is the flattened epic projection used by epic-list rendering: just the
// key, summary, and status name. It is not a wire type — an epic on the wire is
// an ordinary Issue — but the trimmed shape callers build for display.
type Epic struct {
	Key     *string `json:"key,omitempty"`
	Summary *string `json:"summary,omitempty"`
	Status  *string `json:"status,omitempty"`
}

// SearchResult is the enhanced-search (/search/jql) response body. Paging is by
// opaque cursor: NextPageToken carries the next page and IsLast marks
// exhaustion — there is no total count, by design of that endpoint.
type SearchResult struct {
	Issues        []*Issue `json:"issues,omitempty"`
	IsLast        bool     `json:"isLast,omitempty"`
	NextPageToken string   `json:"nextPageToken,omitempty"`
}

// Worklog is a single work-log entry on an issue. TimeSpentSeconds is the
// canonical duration; the CLI's human duration strings are parsed to and
// rendered from it.
type Worklog struct {
	ID               *string       `json:"id,omitempty"`
	TimeSpentSeconds *int          `json:"timeSpentSeconds,omitempty"`
	Started          *string       `json:"started,omitempty"`
	Comment          *adf.Document `json:"comment,omitempty"`
}

// Comment is a single issue comment. Body is an ADF document, and Visibility
// (when set) restricts the comment to a Jira role or group.
type Comment struct {
	ID           *string       `json:"id,omitempty"`
	Self         *string       `json:"self,omitempty"`
	Body         *adf.Document `json:"body,omitempty"`
	Author       *User         `json:"author,omitempty"`
	UpdateAuthor *User         `json:"updateAuthor,omitempty"`
	Created      *string       `json:"created,omitempty"`
	Updated      *string       `json:"updated,omitempty"`
	Visibility   *Visibility   `json:"visibility,omitempty"`
}

// Visibility restricts a comment to a Jira role or a Jira group. Atlassian's
// data model treats Type/Value as mutually exclusive across role and group;
// the CLI's --visibility-role and --visibility-group flags enforce that
// exclusivity locally before any HTTP call ().
type Visibility struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// Transition is one workflow transition available from an issue's current
// status, as returned under expand=transitions. ID is what a transition request
// submits; Name is the human label; HasScreen governs whether a payload sent
// with the transition is honored (see the field comment).
type Transition struct {
	ID   *string `json:"id,omitempty"`
	Name *string `json:"name,omitempty"`
	// HasScreen reports whether the transition presents a screen. Jira
	// silently discards fields and update blocks sent to a screenless
	// transition, so payload-carrying transitions must check it.
	HasScreen *bool `json:"hasScreen,omitempty"`
}

// EditMeta is the issue edit-screen metadata Jira returns under
// expand=editmeta on GET issue: a map keyed by field id of what the edit
// screen accepts. The struct keeps Jira's wire shape (camelCase keys) but
// trims each field to the agent-useful subset — unknown keys are dropped on
// unmarshal rather than carried opaquely.
type EditMeta struct {
	Fields map[string]EditMetaField `json:"fields,omitempty"`
}

// EditMetaField describes one editable field on an issue's edit screen.
type EditMetaField struct {
	Name     string `json:"name,omitempty"`
	Key      string `json:"key,omitempty"`
	Required bool   `json:"required"`
	// Operations lists the verbs the field accepts in an update block
	// (set, add, remove, edit, copy).
	Operations []string             `json:"operations,omitempty"`
	Schema     *EditMetaFieldSchema `json:"schema,omitempty"`
	// AllowedValues enumerates the values Jira accepts for the field,
	// trimmed to their identifying keys. Absent when the field is
	// free-form.
	AllowedValues []EditMetaAllowedValue `json:"allowedValues,omitempty"`
}

// EditMetaFieldSchema is the type identity of an editable field.
type EditMetaFieldSchema struct {
	Type string `json:"type,omitempty"`
	// Items is the element type when Type is "array".
	Items string `json:"items,omitempty"`
	// Custom is the fully-qualified custom-field type identifier; empty
	// for system fields.
	Custom string `json:"custom,omitempty"`
}

// EditMetaAllowedValue is one allowed value of an edit-screen field. Jira
// mixes shapes per field type — options carry `value`, versions/components/
// priorities carry `name` — so both are kept and the blank one omitted.
type EditMetaAllowedValue struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

// ProjectFieldSchema is the create/edit field schema for one project +
// issue-type pair, distilled from Jira's createmeta into the shape the CLI
// caches and the mutation pipeline consults. The json tags are snake_case
// because this is the CLI's own envelope shape, not a Jira wire body.
type ProjectFieldSchema struct {
	ProjectKey string        `json:"project_key"`
	IssueType  string        `json:"issue_type"`
	Fields     []FieldSchema `json:"fields,omitempty"`
}

// FieldSchema describes one field on a project's create/edit screen: its id,
// display name, whether it is required, and its type. It is the per-field row of
// ProjectFieldSchema.
type FieldSchema struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Type     string `json:"type"`
	// Custom is the schema.custom token Jira reports for a custom
	// field — the trailing identifier of
	// com.atlassian.jira.plugin.system.customfieldtypes:* (e.g.
	// "select", "datepicker", "float"). Empty for system fields and for
	// custom fields whose type Jira does not expose. It is the branch
	// key the mutation pipeline uses to encode a custom-field value.
	Custom string `json:"custom,omitempty"`
}

// OpenSchemaProperties marks the fields block as an open schema for the
// envelope registry (envelope.OpenSchema): tenant-defined customfield_*
// keys and raw unmodeled system fields legitimately ride beside the named
// members, so the published schema carries additionalProperties: true and
// the conformance guardrail treats undeclared keys here as contract, not
// drift. The return value is documentation, not data.
func (f *IssueFields) OpenSchemaProperties() string {
	return "tenant-defined customfield_* keys and unmodeled Jira system fields"
}

// issueFieldsNamedKeys is the set of wire keys IssueFields models with a
// named member, derived from the struct's json tags so it can never drift
// from the struct itself. Keys in this set decode into their member; every
// other key is captured raw (CustomFields / Extra).
var issueFieldsNamedKeys = func() map[string]bool {
	keys := map[string]bool{}
	t := reflect.TypeFor[IssueFields]()
	for field := range t.Fields() {
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag != "" && tag != "-" {
			keys[tag] = true
		}
	}
	return keys
}()

// UnmarshalJSON decodes the named system fields normally and, in a second
// pass over the raw object, captures every unnamed key raw: customfield_*
// into CustomFields (the keyed lookups the mutation pipeline uses) and every
// other unmodeled key into Extra, so no wire field is silently dropped. The
// `type known` alias sheds the method set to avoid infinite recursion.
func (f *IssueFields) UnmarshalJSON(data []byte) error {
	type known IssueFields
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var k known
	if err := json.Unmarshal(data, &k); err != nil {
		return err
	}
	*f = IssueFields(k)
	f.CustomFields = map[string]json.RawMessage{}
	f.Extra = map[string]json.RawMessage{}
	for key, val := range raw {
		switch {
		case strings.HasPrefix(key, "customfield_"):
			f.CustomFields[key] = val
		case !issueFieldsNamedKeys[key]:
			f.Extra[key] = val
		}
	}
	return nil
}

// MarshalJSON encodes the named fields, then splices the captured
// CustomFields and Extra back in under their original keys so a
// decode/encode round-trip preserves the whole wire fields block. The
// synthetic map keys the alias encode produces are dropped before the merge;
// named members always win over a same-named raw capture (which cannot
// happen for a decode this package performed, since capture excludes named
// keys).
func (f IssueFields) MarshalJSON() ([]byte, error) {
	type known IssueFields
	data, err := json.Marshal(known(f))
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	delete(raw, "CustomFields")
	delete(raw, "Extra")
	maps.Copy(raw, f.CustomFields)
	for key, value := range f.Extra {
		if _, named := raw[key]; !named {
			raw[key] = value
		}
	}
	return json.Marshal(raw)
}

// UnmarshalJSON decodes the issue, then hoists the collections Jira nests under
// fields (comments, worklogs, subtasks) and flattens issuelinks into
// LinkedIssues, so callers reach them at the top level without walking the
// expand nesting. Each hoist is skipped when the top-level slice is already
// populated, letting an explicit value win over the nested copy.
func (i *Issue) UnmarshalJSON(data []byte) error {
	type known Issue
	var k known
	if err := json.Unmarshal(data, &k); err != nil {
		return err
	}
	*i = Issue(k)
	if i.Fields == nil {
		return nil
	}
	if len(i.Comments) == 0 && i.Fields.Comment != nil {
		i.Comments = append([]*Comment(nil), i.Fields.Comment.Comments...)
	}
	if len(i.Worklogs) == 0 && i.Fields.Worklog != nil {
		i.Worklogs = append([]*Worklog(nil), i.Fields.Worklog.Worklogs...)
	}
	if len(i.Subtasks) == 0 {
		i.Subtasks = append([]*Issue(nil), i.Fields.Subtasks...)
	}
	if len(i.LinkedIssues) == 0 {
		for _, link := range i.Fields.IssueLinks {
			if link.OutwardIssue != nil {
				i.LinkedIssues = append(i.LinkedIssues, link.OutwardIssue)
			}
			if link.InwardIssue != nil {
				i.LinkedIssues = append(i.LinkedIssues, link.InwardIssue)
			}
		}
	}
	return nil
}
