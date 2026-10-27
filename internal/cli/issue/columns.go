package issue

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/gechr/primer/table"
	"github.com/gechr/x/ansi"
	"github.com/gechr/x/human"
	xslices "github.com/gechr/x/slices"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// issueTransitionColumns shares entity and status sinks with other issue tables.
// Field names are sorted so native object key order never changes human output.
func issueTransitionColumns() []table.Column[jira.IssueTransition] {
	return []table.Column[jira.IssueTransition]{
		{Name: "id", Header: "ID", Render: func(t jira.IssueTransition, _ *table.RenderContext) table.Cell {
			return table.TextCell(cli.Entity(t.ID.Value))
		}},
		{Name: "name", Header: "NAME", Flex: true, Render: func(t jira.IssueTransition, _ *table.RenderContext) table.Cell {
			return table.TextCell(cli.OrDash(cli.Entity(t.Name.Value)))
		}},
		{Name: "status", Header: "STATUS", Flex: true, Render: func(t jira.IssueTransition, _ *table.RenderContext) table.Cell {
			return table.TextCell(cli.OrDash(cli.Status(t.To.Value.Name.Value, cli.StatusCategory(t.To.Value.StatusCategory.Value.Key.Value))))
		}},
		{Name: "screen", Header: "SCREEN", Render: func(t jira.IssueTransition, _ *table.RenderContext) table.Cell {
			return table.TextCell(cli.Text(fmt.Sprint(t.HasScreen.Value)))
		}},
		{Name: "fields", Header: "FIELDS", Flex: true, Render: func(t jira.IssueTransition, _ *table.RenderContext) table.Cell {
			if !t.Fields.Set {
				return table.TextCell(cli.Text("not returned"))
			}
			fields := slices.Sorted(maps.Keys(t.Fields.Value))
			for i, name := range fields {
				var metadata struct{ Required bool }
				if json.Unmarshal(t.Fields.Value[name], &metadata) == nil && metadata.Required {
					fields[i] += " (required)"
				}
			}
			return table.TextCell(cli.OrDash(cli.Text(strings.Join(fields, ", "))))
		}},
	}
}

// This file owns the ONE answer to "which sink renders which issue
// field" for every issue-scoped table in this CLI.
//
// It used to be a per-command choice: issue list's own column set named
// cli.IssueKey for KEY, cli.StatusPadded for STATUS, cli.Type for TYPE
// and so on, inside the function that built its table. That is fine
// while there is one table, and it is how the second table quietly
// renders a status with cli.Text — the same field, the same terminal,
// a different color — because the second author read a different
// function. Lifting the pairing here makes the drift impossible rather
// than unlikely: a renderer selects FIELDS, and what a field looks like
// is settled once, in issueColumnTable below.
//
// The rule is enforced, not merely stated:
// tests/guardrails/issue_column_table_test.go fails any table.Column
// literal built outside this file, naming the offender — because a
// renderer that writes its own column has bypassed the table whatever
// sink it happens to reach for.

// issueRow is the plain-value projection every issue-scoped table
// renders from: the fields a column can draw, already unwrapped from
// whichever payload the command carries.
//
// A projection rather than a shared payload type, because the payloads
// genuinely differ — issue list rows are envelope.IssueView (the wire's
// own Nullable-wrapped fields container), link list rows are
// envelope.IssueLinkView (a narrowed, CLI-shaped linked issue) — and
// forcing one of those onto the other would mean either dragging the
// wire model into a place that has no use for it or flattening the
// list's passthrough fields. What the columns need is neither: it is
// this handful of already-resolved values, and each command says how to
// get from its own row to them.
//
// Zero values are legitimate. A field a payload does not carry (a linked
// issue has no `updated`) projects as empty, and the sinks already
// render an empty value as a dash or nothing — so a table can only show
// a column its rows can actually fill, and this type does not have to
// model absence a second time.
type issueRow struct {
	// Key is the issue key, unstyled and unsanitized as it arrived.
	Key string
	// Summary is the issue's summary line.
	Summary string
	// StatusName is the workflow's own name for the status, which a
	// project can spell anything; StatusCategory is Jira's bounded
	// category key, which is what actually decides the color.
	StatusName     string
	StatusCategory cli.StatusCategory
	// TypeName and TypeHierarchy are the issue type and its level —
	// cli.Type colors by both, since the level distinguishes a subtask
	// from an epic regardless of what a tenant named either.
	TypeName      string
	TypeHierarchy int64
	// PriorityName is the priority's name; the column renders it as a
	// glyph and keeps the name for sorting.
	PriorityName string
	// AssigneeName is the assignee's display name, empty for unassigned.
	AssigneeName string
	// Updated is the wire's own timestamp string, humanized by the
	// column rather than here.
	Updated string
}

// issueField names one column in the shared table. A string rather than
// an int enum so the value doubles as the column's machine name (which
// primer uses for sorting and filtering) and reads as itself in a field
// list at a call site.
type issueField string

const (
	fieldKey      issueField = "key"
	fieldStatus   issueField = "status"
	fieldPriority issueField = "priority"
	fieldType     issueField = "type"
	fieldSummary  issueField = "summary"
	fieldAssignee issueField = "assignee"
	fieldUpdated  issueField = "updated"
)

// issueColumnOpts carries the render-time facts a cell needs that no row
// can know for itself: the site a key hyperlinks into, and the width
// every status pill in THIS table shares.
type issueColumnOpts struct {
	// site is the Jira base URL, or "" to degrade every key to plain
	// text (cli.IssueKey's own job to handle).
	site string
	// statusWidth is the visible width every status pill renders at —
	// see statusPillWidth for why it cannot be computed per row.
	statusWidth int
}

// issueColumnSpec is one field's presentation: what its header says,
// whether it absorbs slack width, and — the whole point of this file —
// which sink turns its value into a cell.
type issueColumnSpec struct {
	header string
	flex   bool
	cell   func(issueRow, issueColumnOpts) table.Cell
	// present checks the native key, before the plain-value projection loses it.
	// Nil means the column is issue identity rather than a selected Jira field.
	present func(jira.IssueFields) bool
	// requires names the Jira field this column's value is READ FROM, or
	// "" when the column needs no field at all.
	//
	// It exists for `issue list --columns`, which chooses which of these
	// columns to draw and can therefore ask for one whose data was never
	// fetched — `--fields summary --columns assignee` would draw an
	// ASSIGNEE column of blanks, which reads as "nobody is assigned"
	// rather than as "you did not ask for that field". Recording the
	// dependency HERE, beside the sink that consumes it, is what lets the
	// command refuse that combination instead; a list kept at the call
	// site would go stale the first time a column changed what it reads.
	//
	// Only fieldKey is empty: a key is issue IDENTITY, carried on every
	// search result outside the `fields` object entirely (see
	// buildListOutput, which reads it off the bean), so no --fields value
	// can withhold it.
	requires string
}

// issueColumnTable is the field-to-sink pairing, and the reason this
// file exists. Every issue-scoped table draws from it.
//
// Status, Type, Assignee and Key are all fed through table.StyledCell(
// styled, plain) — both the possibly-styled display text AND the plain
// sanitized text it was built from — so primer measures column width,
// sorts, and filters by the plain form rather than raw byte count or
// embedded escape sequences (see output/table_test.go's own
// TestTableAlignsStyledCellsByPlainWidthNotRawBytes; primer/table's own
// Cell.Text doc names OSC 8 explicitly as content its Text field may
// carry). Status's own styled text can additionally carry a filled PILL
// (background color plus horizontal padding, cli.Status's own
// statusPill) — wider than the bare name — which is exactly the shape
// StyledCell exists for: primer measures the RENDERED text's own visible
// width (ANSI-stripped), padding included, so the column widens to fit
// the pill and the plain copy stays the bare name for sorting and
// filtering, never the padded one. Every other column carries no color
// of its own, so table.TextCell is enough for it.
//
// The pairings themselves, each matching renderView's own mapping
// (view.go):
//
//   - KEY is a plain identifier (cli.Text, never entity or semantic —
//     see renderView's doc comment for why), hyperlinked to its browse
//     URL by cli.IssueKey when styling is genuinely on and site is set.
//   - STATUS is semantic state colored by CATEGORY, never by name
//     (cli.StatusPadded, fed the row's own category key).
//   - P rides as a GLYPH, not its word: the rank is a shape a Jira user
//     already reads at a glance, and a column of "Highest"/"Medium"
//     would spend width the summary needs. The plain copy keeps the NAME
//     so sorting and filtering still see something meaningful rather
//     than an arrow.
//   - TYPE is colored by name and hierarchy level together (cli.Type).
//   - SUMMARY is Jira-authored text with no further meaning (cli.Text),
//     and is the flex column — it is the one that can usefully absorb
//     whatever width the fixed columns leave.
//   - ASSIGNEE is a repeated identity (cli.Entity, which gives the same
//     name the same color wherever it recurs — useful here more than
//     almost anywhere else in this CLI, since a list is exactly where a
//     caller scans for "which of these are mine"), dashed when empty.
//   - UPDATED is a wire timestamp, sanitized with cli.Text before being
//     humanized with cli.Time (H3: no raw ISO timestamp reaches a human
//     surface).
var issueColumnTable = map[issueField]issueColumnSpec{
	fieldKey: {
		header: "KEY",
		cell: func(r issueRow, o issueColumnOpts) table.Cell {
			return table.StyledCell(cli.IssueKey(o.site, r.Key), cli.Text(r.Key))
		},
	},
	fieldStatus: {
		header: "STATUS", requires: "status",
		present: func(f jira.IssueFields) bool { return f.Status.Set },
		cell: func(r issueRow, o issueColumnOpts) table.Cell {
			return table.StyledCell(cli.StatusPadded(r.StatusName, r.StatusCategory, o.statusWidth), cli.Text(r.StatusName))
		},
	},
	fieldPriority: {
		header: "P", requires: "priority",
		present: func(f jira.IssueFields) bool { return f.Priority.Set },
		cell: func(r issueRow, _ issueColumnOpts) table.Cell {
			return table.StyledCell(cli.PriorityIcon(r.PriorityName), cli.Text(r.PriorityName))
		},
	},
	fieldType: {
		header: "TYPE", requires: "issuetype",
		present: func(f jira.IssueFields) bool { return f.Issuetype.Set },
		cell: func(r issueRow, _ issueColumnOpts) table.Cell {
			return table.StyledCell(cli.Type(r.TypeName, r.TypeHierarchy), cli.Text(r.TypeName))
		},
	},
	fieldSummary: {
		header: "SUMMARY", requires: "summary",
		present: func(f jira.IssueFields) bool { return f.Summary.Set },
		flex:    true,
		cell: func(r issueRow, _ issueColumnOpts) table.Cell {
			return table.TextCell(cli.Text(r.Summary))
		},
	},
	fieldAssignee: {
		header: "ASSIGNEE", requires: "assignee",
		present: func(f jira.IssueFields) bool { return f.Assignee.Set },
		cell: func(r issueRow, _ issueColumnOpts) table.Cell {
			return table.StyledCell(cli.OrDash(cli.Entity(r.AssigneeName)), cli.Text(r.AssigneeName))
		},
	},
	fieldUpdated: {
		header: "UPDATED", requires: "updated",
		present: func(f jira.IssueFields) bool { return f.Updated.Set },
		cell: func(r issueRow, _ issueColumnOpts) table.Cell {
			return table.TextCell(cli.Time(cli.Text(r.Updated)))
		},
	},
}

// issueColumnsFor projects fields onto primer columns over a command's
// own row type, using project to reach each row's issueRow.
//
// Generic over the row type rather than over a shared row struct so a
// command keeps rendering the SAME value its machine envelope carries —
// the property issue list's own column set had and this refactor must
// not lose — while still drawing every cell from the one table above.
//
// An unknown field panics: the field lists are package-level constants
// in this same file, so a miss is a wiring mistake in code, caught by
// the first render in any test rather than by whoever runs the command
// (TestEveryIssueFieldHasAColumnSpec pins the same thing statically).
func issueColumnsFor[T any](fields []issueField, opts issueColumnOpts, project func(T) issueRow) []table.Column[T] {
	columns := make([]table.Column[T], 0, len(fields))
	for _, field := range fields {
		spec, ok := issueColumnTable[field]
		if !ok {
			panic(fmt.Sprintf("issue: no column spec for field %q", field))
		}
		columns = append(columns, table.Column[T]{
			Name: string(field), Header: spec.header, Flex: spec.flex,
			Render: func(row T, _ *table.RenderContext) table.Cell {
				return spec.cell(project(row), opts)
			},
		})
	}
	return columns
}

// statusPillWidth is the visible width every status pill in one table
// renders at: the widest status name among the rows about to be drawn.
// Computed ahead of the columns, because a table.Column's own Render
// sees ONE row at a time and so can never know how wide its widest
// sibling is — and a pill sized to its own word leaves the column a
// ragged run of differently-sized blocks instead of an aligned stack.
//
// Visible width, not len: a status name carries no ANSI yet at this
// point, but it may well carry multi-byte runes, and the pill's own
// padding has to agree with what the terminal will actually draw.
//
// Generic over the row type, taking the same projection its table does,
// so the width and the cells are measured from one reading of a row
// rather than two that could disagree.
func statusPillWidth[T any](rows []T, project func(T) issueRow) int {
	widest := 0
	for _, row := range rows {
		widest = max(widest, ansi.StringWidth(project(row).StatusName))
	}
	return widest
}

// issueFieldOrder is the canonical left-to-right order of the shared
// columns, and the second thing this file settles once.
//
// The table above says what a field LOOKS like; without this, each
// command would still pick its own left-to-right arrangement, and two
// issue tables drawn one after another would put STATUS in different
// places for no reason a reader could infer. That is the same class of
// drift as picking a different sink, one layer up — and with four more
// keyed reads queued behind link list it would arrive four more times.
//
// A command SELECTS from this order, it does not reorder it: every
// command's field list must be a subsequence of this slice, which
// TestCommandFieldListsFollowTheCanonicalOrder enforces. Dropping a
// column is a per-command judgment (the rows differ); moving one is a
// change to what every issue table looks like, so it happens here.
//
// The order itself: identity first, then the two at-a-glance state
// columns a caller scans down (STATUS, P), then the classifier (TYPE),
// then the flexing prose (SUMMARY), then the trailing per-row facts
// (ASSIGNEE, UPDATED) that only some rows even carry.
var issueFieldOrder = []issueField{
	fieldKey, fieldStatus, fieldPriority, fieldType, fieldSummary, fieldAssignee, fieldUpdated,
}

// issueListFields is `issue list`'s column set: every shared column,
// which is exactly the canonical order — aliased rather than restated so
// the two cannot drift.
var issueListFields = issueFieldOrder

// issueFieldNames is the accepted set for `issue list --columns`, in the
// canonical order, ready to be named in help and in a refusal.
//
// Derived from issueFieldOrder rather than restated, so a column added to
// this file becomes selectable — and becomes DOCUMENTED as selectable —
// without anyone remembering to update a second list.
func issueFieldNames() []string {
	return xslices.Map(issueFieldOrder, func(f issueField) string { return string(f) })
}

// parseIssueFields turns `--columns`' raw values into the column list a
// renderer draws, refusing anything it cannot honestly draw.
//
// **Order is the CALLER's, not the canonical one**, and that is a
// deliberate departure from issueFieldOrder's rule one paragraph up.
// That rule governs the field lists this package DECLARES: two tables
// drawn one after another must agree, and a command author reordering
// columns for their own taste is the drift it prevents. A caller typing
// `--columns status,key` is not an author — they are asking for this one
// invocation to be arranged the way they are about to read it, which is
// the entire point of the flag. Selecting from the canonical order while
// silently re-sorting the result would be the surprise.
//
// Three refusals, all before any request is made:
//
//   - an unknown name, which names the accepted set rather than leaving
//     a caller to guess it — the same posture --order-by takes;
//   - a repeat, because a table with two ASSIGNEE columns is a typo
//     rendered faithfully, never a request;
//   - an empty selection, which would draw a table of nothing at all.
//
// Values are trimmed and lower-cased first: the headers render upper-case
// (KEY, STATUS), so a caller reading one off a table and typing it back is
// making no mistake worth refusing them for.
func parseIssueFields(values []string) ([]issueField, error) {
	out := make([]issueField, 0, len(values))
	seen := map[issueField]bool{}
	for _, raw := range values {
		name := issueField(strings.ToLower(strings.TrimSpace(raw)))
		if name == "" {
			continue
		}
		if _, ok := issueColumnTable[name]; !ok {
			e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
				"--columns: %q is not a column — pick from %s",
				raw, strings.Join(issueFieldNames(), ", "),
			))
			e.Field = flagColumns
			return nil, e
		}
		if seen[name] {
			e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
				"--columns names %q twice — each column can be drawn only once", name,
			))
			e.Field = flagColumns
			return nil, e
		}
		seen[name] = true
		out = append(out, name)
	}
	if len(out) == 0 {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--columns names no columns at all — pick from %s, or drop the flag for the default set",
			strings.Join(issueFieldNames(), ", "),
		))
		e.Field = flagColumns
		return nil, e
	}
	return out, nil
}

// refuseUnfetchedColumns refuses a `--columns` selection asking for a
// column whose Jira field this invocation's own `--fields` never
// requested.
//
// The failure it prevents is a QUIET one, which is why it is worth a
// check rather than a note in help: `--fields summary --columns assignee`
// would draw an ASSIGNEE column, and every cell in it would be the dash
// cli.OrDash renders for an empty value. That reads as "none of these
// issues is assigned" — a statement about the project — when the truth is
// "you did not ask Jira for that field". A wrong answer nobody can tell
// is wrong is worse than a refusal.
//
// It is skipped entirely when --fields was not given, because the default
// set (defaultListFields, list.go) is exactly what these columns need,
// and skipped again when any value carries acli's wildcard or exclusion
// grammar (`*all`, `*navigable`, `-description`) — those describe a set
// resolved by the SERVER, so this package cannot know its membership, and
// a check that guessed would refuse working invocations. Under a
// wildcard the honest outcome is the column drawn from whatever came
// back, which is what happens with no check at all.
func refuseUnfetchedColumns(columns []issueField, fields []string) error {
	if len(fields) == 0 {
		return nil
	}
	requested := map[string]bool{}
	for _, f := range fields {
		f = strings.ToLower(strings.TrimSpace(f))
		if strings.HasPrefix(f, "*") || strings.HasPrefix(f, "-") {
			return nil
		}
		requested[f] = true
	}
	for _, c := range columns {
		need := issueColumnTable[c].requires
		if need == "" || requested[need] {
			continue
		}
		e := cli.NewInputError(cli.CodeFlagConflict, fmt.Sprintf(
			"--columns asks for %q, which is drawn from the %q field, but --fields did not ask Jira for it — "+
				"every cell would be blank, which reads as an empty value rather than an unfetched one; "+
				"add %q to --fields, or drop %q from --columns",
			c, need, need, c,
		))
		e.Field = flagColumns
		return e
	}
	return nil
}

// issueLinkFields are the issue-scoped columns `issue link list` draws
// for the issue at the far end of each link — the canonical order minus
// two. The rows are all links of ONE issue, so an ASSIGNEE column would
// have the caller scanning for something they did not ask about, and a
// link carries no `updated` at all (see envelope.LinkedIssueFields for
// what the wire sends). The LINK column that names the relationship is
// not an issue field and is spliced in separately below.
var issueLinkFields = []issueField{
	fieldKey, fieldStatus, fieldPriority, fieldType, fieldSummary,
}

// issueRowFromView projects a search/list result row.
func issueRowFromView(r envelope.IssueView) issueRow {
	it := val(r.Fields.Issuetype)
	return issueRow{
		Key:            r.Key,
		Summary:        r.Fields.Summary.Value,
		StatusName:     val(r.Fields.Status).Name.Value,
		StatusCategory: statusCategoryKey(r.Fields),
		TypeName:       it.Name.Value,
		TypeHierarchy:  it.HierarchyLevel.Value,
		PriorityName:   val(r.Fields.Priority).Name.Value,
		AssigneeName:   val(r.Fields.Assignee).DisplayName.Value,
		Updated:        r.Fields.Updated.Value,
	}
}

// issueRowFromLink projects the issue at the far end of a link. The
// narrowing already happened at the envelope boundary (link.go's
// linkViews), so unlike issueRowFromView this reaches through no
// Nullable layers at all.
func issueRowFromLink(l envelope.IssueLinkView) issueRow {
	return issueRow{
		Key:            l.Issue.Key,
		Summary:        l.Issue.Fields.Summary,
		StatusName:     l.Issue.Fields.Status.Name,
		StatusCategory: cli.StatusCategory(l.Issue.Fields.Status.Category),
		TypeName:       l.Issue.Fields.Type.Name,
		TypeHierarchy:  l.Issue.Fields.Type.HierarchyLevel,
		PriorityName:   l.Issue.Fields.Priority.Name,
	}
}

// issueListColumns builds `issue list`'s table over envelope.IssueView —
// the same type the machine envelope carries, so the human render and
// the machine payload share one row shape rather than a second one built
// just for display.
//
// fields is `--columns`' own selection, or nil for issueListFields — the
// whole shared set, which is what the command drew before the flag
// existed and still draws without it. Nil rather than the caller
// spelling the default out, so "no --columns" has ONE meaning in ONE
// place; a caller that passed issueListFields explicitly would be making
// the same statement in a second way that could later disagree.
func issueListColumns(site string, statusWidth int, fields []issueField) []table.Column[envelope.IssueView] {
	if len(fields) == 0 {
		fields = issueListFields
	}
	columns := issueColumnsFor(fields, issueColumnOpts{site: site, statusWidth: statusWidth}, issueRowFromView)
	for i, field := range fields {
		present, render := issueColumnTable[field].present, columns[i].Render
		columns[i].Render = func(row envelope.IssueView, ctx *table.RenderContext) table.Cell {
			if present != nil && !present(row.Fields) {
				return table.TextCell("not returned")
			}
			cell := render(row, ctx)
			if output.IsBlank(cell.Text) {
				return table.TextCell("-")
			}
			return cell
		}
	}
	return columns
}

// availableIssueListFields omits default columns absent from every returned row.
// Explicit selections retain their order and display an unavailable cell instead.
func availableIssueListFields(rows []envelope.IssueView) []issueField {
	var fields []issueField
	for _, field := range issueListFields {
		present := issueColumnTable[field].present
		if present == nil || slices.ContainsFunc(rows, func(row envelope.IssueView) bool { return present(row.Fields) }) {
			fields = append(fields, field)
		}
	}
	return fields
}

// issueLinkColumns builds `issue link list`'s table: the shared
// issue-scoped columns for the linked issue, with the relationship
// column spliced in right after the key.
//
// LINK sits second rather than first because the KEY is what a caller
// scans for and the relationship is what they read once they have found
// it — and because putting the one non-shared column at the front would
// make this table look like a different family from `issue list`'s when
// they are drawn one after the other. Splicing it in rather than
// prepending is the same argument applied to the columns underneath: the
// shared ones keep their canonical order (issueFieldOrder) around it, so
// STATUS is in the same place relative to KEY in both tables.
func issueLinkColumns(site string, statusWidth int) []table.Column[envelope.IssueLinkView] {
	opts := issueColumnOpts{site: site, statusWidth: statusWidth}
	shared := issueColumnsFor(issueLinkFields, opts, issueRowFromLink)
	// The relationship is Jira-authored text — an admin names link types
	// and their inward/outward phrasings — so it goes through cli.Text
	// like any other Jira string, and carries no color of its own: the
	// link's meaning is the words, and coloring them would compete with
	// the status pill on the same row for the eye.
	relation := table.Column[envelope.IssueLinkView]{
		Name: "link", Header: "LINK",
		Render: func(l envelope.IssueLinkView, _ *table.RenderContext) table.Cell {
			return table.TextCell(cli.Text(l.Relation))
		},
	}
	return append(append(shared[:1:1], relation), shared[1:]...)
}

// issueAttachmentColumns builds `issue attachment list`'s table.
//
// This one draws NO shared column, and that is worth saying out loud
// because everything above it does. An attachment is not an issue: it
// has no key, no status, no priority, no type — issueRow can project
// none of it, and issueFieldOrder has nothing to select from. Forcing it
// through issueColumnsFor would mean either adding filename/size/mime
// fields to a struct named issueRow, where every other table would then
// have to render them as blanks, or widening the canonical order with
// entries that are not issue fields at all. Both corrupt the abstraction
// to satisfy a lookup that has no answer.
//
// So the columns are declared here directly — in THIS file, because the
// rule the guardrail enforces (tests/guardrails/issue_column_table_test.go)
// is placement, not membership: a reviewer asking "what does this CLI's
// tables look like" reads one file, and a future attachment column
// cannot quietly pick cli.Text for something the shared table colors.
//
// Field to sink:
//
//   - ID is the handle the download and delete verbs take, so it leads,
//     the same position (and the same reason) KEY has everywhere else.
//     Plain text: it is an opaque numeric identifier with no browse URL
//     of its own — cli.IssueKey's hyperlink has no counterpart here.
//   - FILE is the uploader's own filename, which is the most
//     attacker-influenced string this CLI renders — it crosses from a
//     filesystem, not a Jira form — so cli.Text is doing real work, and
//     it is the flex column because it is the one that can usefully
//     absorb slack width.
//   - SIZE goes through cli.Bytes: a byte count rendered as digits would
//     be unreadable at attachment scale, and the sink keeps it in the
//     same numeric color a count gets elsewhere. Left-aligned like every
//     other column — primer's Column carries no alignment control, and
//     hand-padding the cell here would fight the width its own
//     AlignColumns computes.
//   - TYPE is the MIME type — Jira-authored, plain, dashed when the
//     response carried none.
//   - AUTHOR is a repeated identity across the rows (one person uploads
//     several files), which is exactly what cli.Entity's stable
//     per-name color is for, matching ASSIGNEE's own treatment.
//   - CREATED is a wire timestamp, sanitized with cli.Text before being
//     humanized with cli.Time — H3, the same pairing UPDATED uses.
func issueAttachmentColumns() []table.Column[envelope.AttachmentView] {
	return []table.Column[envelope.AttachmentView]{
		{
			Name: "id", Header: "ID",
			Render: func(a envelope.AttachmentView, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.Text(a.ID))
			},
		},
		{
			Name: "file", Header: "FILE", Flex: true,
			Render: func(a envelope.AttachmentView, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.Text(a.Filename))
			},
		},
		{
			Name: "size", Header: "SIZE",
			Render: func(a envelope.AttachmentView, _ *table.RenderContext) table.Cell {
				if !a.Size.Set {
					return table.TextCell("not returned")
				}
				if a.Size.Null {
					return table.TextCell("-")
				}
				// StyledCell, not TextCell: cli.Bytes may carry SGR bytes,
				// and primer must measure and sort by the plain form. The
				// plain copy is the same rendered string with styling off
				// rather than the raw integer — a column sorted by
				// "1048576" while displaying "1.0 MiB" would sort by a
				// value the reader cannot see.
				return table.StyledCell(cli.Bytes(a.Size.Value), human.FormatIECBytes(float64(a.Size.Value)))
			},
		},
		{
			Name: "type", Header: "TYPE",
			Render: func(a envelope.AttachmentView, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.OrDash(cli.Text(a.MimeType)))
			},
		},
		{
			Name: "author", Header: "AUTHOR",
			Render: func(a envelope.AttachmentView, _ *table.RenderContext) table.Cell {
				name := a.Author.DisplayName
				return table.StyledCell(cli.OrDash(cli.Entity(name)), cli.Text(name))
			},
		},
		{
			Name: "created", Header: "CREATED",
			Render: func(a envelope.AttachmentView, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.Time(cli.Text(a.Created)))
			},
		},
	}
}

// issueWatchersColumns builds `issue watchers list`'s table.
//
// The second table here to draw NO shared column, for the reason
// issueAttachmentColumns states in full and this one inherits: a
// watcher is not an issue. It has no key, no status, no priority and no
// type — issueRow can project none of it and issueFieldOrder has
// nothing to select from — so the columns are declared here directly,
// in THIS file, because the rule the guardrail enforces
// (tests/guardrails/issue_column_table_test.go) is placement, not
// membership.
//
// Field to sink:
//
//   - WATCHER is the display name, and it leads rather than the account
//     id — the reverse of the attachment table's ID-first order, and a
//     deliberate difference. An attachment id is the only handle a
//     filename cannot substitute for, so finding it is why that command
//     runs; a watcher is a PERSON, and the question "who is watching"
//     is answered by the name. It goes through cli.Entity, whose stable
//     per-name color matches ASSIGNEE and AUTHOR everywhere else, and
//     it is the flex column because a display name is the one field
//     here of unbounded length.
//   - ID is the account id the add and remove verbs take. Still
//     present, and still plain text — an opaque identifier with no
//     browse URL of its own — just no longer first, since a caller
//     scans for the name and reads the id once they have found it.
//   - TYPE is Jira's own account classification ('atlassian', 'app',
//     'customer'), plain and dashed when the record carried none. It
//     earns a column rather than living only in the envelope because an
//     app in a notification list looks exactly like a colleague until
//     something says otherwise.
//   - ACTIVE is a deactivated account's only visible tell. It renders
//     as a word rather than a checkmark or a bare bool: "yes"/"no" is
//     what a reader can scan, and a glyph column would need a legend
//     the table has nowhere to put. cli.Text, carrying no color of its
//     own — coloring "no" as a failure would overstate it, since a
//     deactivated watcher is a fact about the list, not an error in it.
//
// No CREATED column, unlike every other table here: Jira records when
// an attachment was uploaded and when an issue was updated, but not
// when a watcher started watching. The wire carries no such field, so
// there is nothing to draw.
func issueWatchersColumns() []table.Column[envelope.WatcherView] {
	return []table.Column[envelope.WatcherView]{
		{
			Name: "watcher", Header: "WATCHER", Flex: true,
			Render: func(w envelope.WatcherView, _ *table.RenderContext) table.Cell {
				return table.StyledCell(cli.OrDash(cli.Entity(w.DisplayName)), cli.Text(w.DisplayName))
			},
		},
		{
			Name: "id", Header: "ID",
			Render: func(w envelope.WatcherView, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.Text(w.AccountID))
			},
		},
		{
			Name: "type", Header: "TYPE",
			Render: func(w envelope.WatcherView, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.OrDash(cli.Text(w.AccountType)))
			},
		},
		{
			Name: "active", Header: "ACTIVE",
			Render: func(w envelope.WatcherView, _ *table.RenderContext) table.Cell {
				if !w.Active.Set {
					return table.TextCell("not returned")
				}
				if w.Active.Null {
					return table.TextCell("-")
				}
				active := "no"
				if w.Active.Value {
					active = "yes"
				}
				return table.TextCell(cli.Text(active))
			},
		},
	}
}

// issueWeblinkColumns builds `issue weblink list`'s table.
//
// The third table here to draw NO shared column, for the reason
// issueAttachmentColumns states in full and this one inherits: a web
// link is not an issue. It has no key, no status, no priority and no
// type, so issueRow can project none of it — and the columns are
// declared here, in THIS file, because the rule the guardrail enforces
// (tests/guardrails/issue_column_table_test.go) is placement.
//
// Field to sink:
//
//   - ID is the handle `weblink delete` takes, so it leads — the same
//     position and reason ID has on the attachment table. Plain text: an
//     opaque numeric identifier with no browse URL of its own.
//   - URL is the whole point of the row, so it is the flex column, and
//     it is the one value here that could plausibly run to hundreds of
//     characters. Truncated with an explicit ellipsis rather than left
//     for the table to clip (see truncateURL), and sanitized: a URL
//     arrives from whoever created the link, which may not be the person
//     reading it.
//   - TITLE is author-supplied Jira-authored text, so cli.Text, dashed
//     when empty — though the create path requires one, a link made
//     through the web UI or another tool may not have.
//   - RELATIONSHIP is free text on this resource rather than a
//     configured type (unlike an issue link's), so it is plain and
//     dashed when absent; there is no direction here to color.
func issueWeblinkColumns() []table.Column[envelope.WeblinkView] {
	return []table.Column[envelope.WeblinkView]{
		{
			Name: "id", Header: "ID",
			Render: func(w envelope.WeblinkView, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.Text(w.ID))
			},
		},
		{
			Name: "url", Header: "URL", Flex: true,
			Render: func(w envelope.WeblinkView, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.Text(truncateURL(w.URL, maxWeblinkURLWidth)))
			},
		},
		{
			Name: "title", Header: "TITLE",
			Render: func(w envelope.WeblinkView, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.OrDash(cli.Text(w.Title)))
			},
		},
		{
			Name: "relationship", Header: "RELATIONSHIP",
			Render: func(w envelope.WeblinkView, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.OrDash(cli.Text(w.Relationship)))
			},
		},
	}
}

// maxWeblinkURLWidth caps the URL column.
//
// A fixed number rather than a share of the terminal, because the table
// computes its own widths from the cells it is given and this decides
// what to give it. Sixty characters fits a typical
// https://host/path/to/thing whole and cuts only the query-string tails
// nobody reads across a table — and the ellipsis says it was cut, which
// is the part that matters (see truncateURL).
const maxWeblinkURLWidth = 60

// truncateURL shortens a URL for a fixed-width table cell, keeping the
// front — where the host and the meaningful path live — and marking the
// cut with an ellipsis.
//
// The table's own width handling would clip a long cell anyway; doing it
// here means the ellipsis is part of the VALUE rather than a rendering
// artifact, so a reader can see the URL was shortened instead of copying
// a truncated one and wondering why it 404s. The machine envelope
// carries the whole URL either way — this is a human-surface concern
// only.
func truncateURL(raw string, width int) string {
	runes := []rune(raw)
	if width <= 1 || len(runes) <= width {
		return raw
	}
	return strings.TrimRight(string(runes[:width-1]), " ") + "\u2026"
}

// issueLinkTypeColumns is `issue link types`' table: the type's name and
// both of its directional phrasings.
//
// **All three columns, and the two phrasings are the point.** A caller
// reads this table to learn what to pass to `link create --type`, and
// while the NAME is accepted, it always links outward — the only way to
// create the reverse relationship is to say the inward phrase. A table
// showing names alone would hide half the surface.
//
// Both phrase columns flex: an admin can phrase a custom type as anything
// ("is superseded by the release of"), and truncating the very string a
// caller must retype would defeat the command.
func issueLinkTypeColumns() []table.Column[envelope.IssueLinkTypeView] {
	return []table.Column[envelope.IssueLinkTypeView]{
		{
			Name: "name", Header: "NAME",
			Render: func(t envelope.IssueLinkTypeView, _ *table.RenderContext) table.Cell {
				// cli.Entity, not cli.Text: the name is the identity a
				// caller copies, and it is styled like every other entity
				// this CLI prints. Entity sanitizes too, so the
				// Jira-authored value is still safe (axis H4).
				return table.TextCell(cli.Entity(t.Name))
			},
		},
		{
			Name: "outward", Header: "OUTWARD", Flex: true,
			Render: func(t envelope.IssueLinkTypeView, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.OrDash(cli.Text(t.Outward)))
			},
		},
		{
			Name: "inward", Header: "INWARD", Flex: true,
			Render: func(t envelope.IssueLinkTypeView, _ *table.RenderContext) table.Cell {
				return table.TextCell(cli.OrDash(cli.Text(t.Inward)))
			},
		},
	}
}
