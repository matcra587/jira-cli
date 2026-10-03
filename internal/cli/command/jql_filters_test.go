package command

import (
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestJQLStructuredFilters(t *testing.T) {
	tests := []struct {
		name string
		in   JQLInput
		want string
	}{
		{name: "keys", in: JQLInput{Key: []string{"ABC-001:3", "ABC-2", "XYZ-9"}}, want: `key in ("ABC-1", "ABC-2", "ABC-3", "XYZ-9")`},
		{name: "epics", in: JQLInput{Epic: []string{"ABC-1", "ABC-2"}}, want: `parent in ("ABC-1", "ABC-2")`},
		{name: "me", in: JQLInput{Assignee: []string{"me"}}, want: `assignee = currentUser()`},
		{name: "unassigned", in: JQLInput{Assignee: []string{"none"}}, want: `assignee is EMPTY`},
		{name: "mixed users", in: JQLInput{Reporter: []string{"ada", "ME", "empty"}}, want: `(reporter in ("ada") OR reporter = currentUser() OR reporter is EMPTY)`},
		{name: "negative status", in: JQLInput{Status: []string{"!Done"}}, want: `status != "Done"`},
		{name: "categories", in: JQLInput{Status: []string{">=In Progress"}}, want: `statusCategory in ("In Progress", "Done")`},
		{name: "status precedence", in: JQLInput{Project: []string{"ABC"}, Status: []string{"Open", "Blocked", "<Done", "!Closed", "!Abandoned"}}, want: `project in ("ABC") AND (status in ("Open", "Blocked") OR statusCategory in ("To Do", "In Progress")) AND status != "Closed" AND status != "Abandoned"`},
		{name: "positive named bound", in: JQLInput{CreatedAfter: "+2w"}, want: `created >= 2w`},
		{name: "relative", in: JQLInput{Updated: "-7d", Created: "+2w", Resolved: "<=2026-08-01"}, want: `created >= 2w AND updated >= -7d AND resolved <= "2026-08-01"`},
		{name: "date ranges", in: JQLInput{Created: "2026-08-01..2026-08-31", Updated: "..-1d", Resolved: "-2w.."}, want: `created >= "2026-08-01" AND created <= "2026-08-31" AND updated <= -1d AND resolved >= -2w`},
		{name: "date comparator", in: JQLInput{Created: ">2026-08-01"}, want: `created > "2026-08-01"`},
		{name: "explicit ascending", in: JQLInput{OrderBy: "updated", Descending: new(false)}, want: `ORDER BY updated ASC`},
		{name: "explicit descending", in: JQLInput{OrderBy: "key", Descending: new(true)}, want: `ORDER BY key DESC`},
		{name: "custom field", in: JQLInput{OrderBy: "cf[12345]", CustomOrder: true, Descending: new(true)}, want: `ORDER BY "cf[12345]" DESC`},
		{name: "no order", in: JQLInput{OrderBy: "none", CustomOrder: true, BoardJQL: "project = ABC ORDER BY Rank ASC"}, want: `(project = ABC)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ComposeJQL(tt.in)
			if err != nil || got != tt.want {
				t.Fatalf("ComposeJQL = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestJQLStructuredFilterRefusals(t *testing.T) {
	tests := []struct {
		name, field string
		in          JQLInput
	}{
		{name: "key overflow", field: "key", in: JQLInput{Key: []string{"ABC-1:1001"}}},
		{name: "key syntax", field: "key", in: JQLInput{Key: []string{`ABC-1" OR project = ABC`}}},
		{name: "epic range", field: "epic", in: JQLInput{Epic: []string{"ABC-1:3"}}},
		{name: "empty status", field: "status", in: JQLInput{Status: []string{"!"}}},
		{name: "empty category", field: "status", in: JQLInput{Status: []string{"<To Do"}}},
		{name: "unknown category", field: "status", in: JQLInput{Status: []string{">Ready"}}},
		{name: "status control", field: "status", in: JQLInput{Status: []string{"Done\n"}}},
		{name: "user control", field: "assignee", in: JQLInput{Assignee: []string{"me\n"}}},
		{name: "unsigned relative", field: "updated", in: JQLInput{Updated: "7d"}},
		{name: "invalid calendar", field: "created", in: JQLInput{Created: "2026-02-30"}},
		{name: "empty range", field: "resolved", in: JQLInput{Resolved: ".."}},
		{name: "multiple range", field: "created", in: JQLInput{Created: "-2w..-1w..-1d"}},
		{name: "operand injection", field: "updated", in: JQLInput{Updated: "-7d OR project = ABC"}},
		{name: "direction without order", field: "desc", in: JQLInput{Descending: new(true)}},
		{name: "direction conflict", field: "desc", in: JQLInput{OrderBy: "key", Descending: new(false), Reverse: true}},
		{name: "unsafe order", field: "order-by", in: JQLInput{OrderBy: "updated DESC, key", CustomOrder: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ComposeJQL(tt.in)
			var input *cli.InputError
			if !errors.As(err, &input) || input.Field != tt.field {
				t.Fatalf("error = %v; want input error for %s", err, tt.field)
			}
		})
	}
}

func TestJQLNewFiltersRefuseRawQuery(t *testing.T) {
	for _, in := range []JQLInput{
		{BoardJQL: "project = ABC"}, {Key: []string{"ABC-1"}}, {Epic: []string{"ABC-1"}}, {Created: "-1d"}, {Updated: "-1d"}, {Resolved: "-1d"},
	} {
		in.JQL = "project = ABC"
		_, err := ComposeJQL(in)
		var input *cli.InputError
		if !errors.As(err, &input) || input.Code != cli.CodeFlagNotApplicable {
			t.Fatalf("raw query mixed with %+v: %v", in, err)
		}
	}
}

func TestJQLOrderSplitWhitespace(t *testing.T) {
	for _, gap := range []string{" ", "  ", "\t", "\n"} {
		query := `summary ~ "ORDER BY key" AND project = ABC ORDER` + gap + `BY updated DESC`
		conditions, order := SplitJQLOrderBy(query)
		if conditions != `summary ~ "ORDER BY key" AND project = ABC` || !strings.HasPrefix(order, "ORDER") {
			t.Fatalf("split %q = %q, %q", query, conditions, order)
		}
	}
}

func TestJQLOrderSplitKeepsUnicodeIdentifiers(t *testing.T) {
	query := "日本語order by = value"
	conditions, order := SplitJQLOrderBy(query)
	if conditions != query || order != "" {
		t.Fatalf("split inside Unicode word: %q, %q", conditions, order)
	}
}

func TestJQLCustomFieldIdentifiers(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{
		{value: "cf[12345]", valid: true},
		{value: "cf[0]", valid: true},
		{value: "cf[]"},
		{value: "cf[١٢٣]"},
		{value: "cf[123x]"},
		{value: "cf[123]tail"},
		{value: "cf[123"},
		{value: "votes", valid: true},
		{value: "日本語", valid: true},
		{value: "custom_field-1.name", valid: true},
		{value: ""},
		{value: "updated DESC"},
		{value: "key,updated"},
		{value: "updated\n"},
	} {
		t.Run(test.value, func(t *testing.T) {
			if safeJQLField(test.value) != test.valid {
				t.Fatalf("identifier %q: want valid=%v", test.value, test.valid)
			}
		})
	}
}
