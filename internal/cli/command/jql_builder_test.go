package command

import (
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestJQLQuoteEscapesBackslashAndDoubleQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "ABC", want: `"ABC"`},
		{name: "double quote", in: `say "hi"`, want: `"say \"hi\""`},
		{name: "backslash", in: `a\b`, want: `"a\\b"`},
		{name: "backslash then quote", in: `\"`, want: `"\\\""`},
		{name: "unicode passthrough", in: "日本語", want: `"日本語"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := QuoteJQL(tt.in)
			if err != nil {
				t.Fatalf("QuoteJQL(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("QuoteJQL(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

// TestJQLQuoteResistsInjection is the core security proof: a value
// carrying a JQL metacharacter sequence — an unescaped closing quote
// followed by a fresh boolean clause — must not be able to alter the
// query's structure. The quoted result, fed back through the SAME
// quote/unquote reasoning a JQL parser applies, must decode to exactly
// the original malicious-looking string as ONE value, never as a
// closing quote plus trailing JQL.
func TestJQLQuoteResistsInjection(t *testing.T) {
	malicious := `X" OR project = "SECRET`
	got, err := QuoteJQL(malicious)
	if err != nil {
		t.Fatalf("QuoteJQL(%q): %v", malicious, err)
	}
	want := `"X\" OR project = \"SECRET"`
	if got != want {
		t.Errorf("QuoteJQL(%q) = %s, want %s", malicious, got, want)
	}
	// The escaped quotes must be the ONLY quote characters besides the
	// wrapping pair — an unescaped `"` anywhere in the middle would be
	// exactly the injection this function exists to prevent.
	inner := strings.TrimSuffix(strings.TrimPrefix(got, `"`), `"`)
	inner = strings.ReplaceAll(inner, `\"`, "")
	if strings.Contains(inner, `"`) {
		t.Errorf("QuoteJQL(%q) = %s leaves an unescaped quote in the body", malicious, got)
	}
}

func TestJQLQuoteRejectsControlCharacters(t *testing.T) {
	for _, in := range []string{"a\nb", "a\rb", "a\tb", "a\x00b", "a\x1bb"} {
		if _, err := QuoteJQL(in); err == nil {
			t.Errorf("QuoteJQL(%q) = nil error, want a rejection of the embedded control character", in)
		}
	}
}

func TestComposeJQLSingleFilterInClause(t *testing.T) {
	got, err := ComposeJQL(JQLInput{Project: []string{"ABC"}})
	if err != nil {
		t.Fatalf("ComposeJQL: %v", err)
	}
	if want := `project in ("ABC")`; got != want {
		t.Errorf("ComposeJQL = %q, want %q", got, want)
	}
}

// TestComposeJQLRepeatedValuesOnOneFlagAreOred pins the within-flag
// composition rule: two --label values match EITHER label.
func TestComposeJQLRepeatedValuesOnOneFlagAreOred(t *testing.T) {
	got, err := ComposeJQL(JQLInput{Label: []string{"bug", "urgent"}})
	if err != nil {
		t.Fatalf("ComposeJQL: %v", err)
	}
	if want := `labels in ("bug", "urgent")`; got != want {
		t.Errorf("ComposeJQL = %q, want %q", got, want)
	}
}

// TestComposeJQLDifferentFlagsAreAnded pins the across-flag composition
// rule, and the fixed field order every composed query uses.
func TestComposeJQLDifferentFlagsAreAnded(t *testing.T) {
	got, err := ComposeJQL(JQLInput{
		Project:   []string{"ABC"},
		Assignee:  []string{"ada@example.com"},
		Status:    []string{"Open"},
		IssueType: []string{"Bug"},
		Label:     []string{"urgent"},
	})
	if err != nil {
		t.Fatalf("ComposeJQL: %v", err)
	}
	want := `project in ("ABC") AND assignee in ("ada@example.com") AND status in ("Open") AND issuetype in ("Bug") AND labels in ("urgent")`
	if got != want {
		t.Errorf("ComposeJQL = %q, want %q", got, want)
	}
}

// TestComposeJQLReporterAndPriorityComposeLikeOtherFilters proves the
// new convenience filters follow the exact same OR-within/AND-across
// rule as --assignee/--status, and land in the fixed field order after
// them.
func TestComposeJQLReporterAndPriorityComposeLikeOtherFilters(t *testing.T) {
	got, err := ComposeJQL(JQLInput{
		Reporter: []string{"ada@example.com", "grace@example.com"},
		Priority: []string{"High"},
	})
	if err != nil {
		t.Fatalf("ComposeJQL: %v", err)
	}
	want := `reporter in ("ada@example.com", "grace@example.com") AND priority in ("High")`
	if got != want {
		t.Errorf("ComposeJQL = %q, want %q", got, want)
	}
}

func TestComposeJQLExplicitJQLPassesThroughUnfiltered(t *testing.T) {
	got, err := ComposeJQL(JQLInput{JQL: `project = ABC ORDER BY created DESC`})
	if err != nil {
		t.Fatalf("ComposeJQL: %v", err)
	}
	if want := `project = ABC ORDER BY created DESC`; got != want {
		t.Errorf("ComposeJQL = %q, want %q", got, want)
	}
}

func TestComposeJQLNeitherJQLNorFilterIsEmptyString(t *testing.T) {
	got, err := ComposeJQL(JQLInput{})
	if err != nil {
		t.Fatalf("ComposeJQL: %v", err)
	}
	if got != "" {
		t.Errorf("ComposeJQL = %q, want an empty string when nothing was asked for", got)
	}
}

// TestComposeJQLRefusesMixingJQLWithAFilter is the deliberate ambiguity
// refusal: --jql alongside any convenience filter is a classified error,
// never silently combined and never silently ignored.
func TestComposeJQLRefusesMixingJQLWithAFilter(t *testing.T) {
	_, err := ComposeJQL(JQLInput{JQL: `project = ABC`, Project: []string{"ABC"}})
	if err == nil {
		t.Fatal("ComposeJQL returned nil error for --jql mixed with --project, want a refusal")
	}
	var inputErr *cli.InputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("ComposeJQL error = %v (%T), want *cli.InputError", err, err)
	}
	if inputErr.Code != cli.CodeFlagNotApplicable {
		t.Errorf("code = %q, want %q", inputErr.Code, cli.CodeFlagNotApplicable)
	}
	if inputErr.Field != "project" {
		t.Errorf("field = %q, want the offending flag %q", inputErr.Field, "project")
	}
}

// TestComposeJQLRefusesMixingJQLWithAnyFilterFlag proves the refusal
// covers every convenience filter, not just --project, and names
// whichever one was actually given.
func TestComposeJQLRefusesMixingJQLWithAnyFilterFlag(t *testing.T) {
	tests := []struct {
		name      string
		in        JQLInput
		wantField string
	}{
		{name: "assignee", in: JQLInput{Assignee: []string{"ada"}}, wantField: "assignee"},
		{name: "status", in: JQLInput{Status: []string{"Open"}}, wantField: "status"},
		{name: "type", in: JQLInput{IssueType: []string{"Bug"}}, wantField: "type"},
		{name: "label", in: JQLInput{Label: []string{"urgent"}}, wantField: "label"},
		{name: "reporter", in: JQLInput{Reporter: []string{"ada"}}, wantField: "reporter"},
		{name: "priority", in: JQLInput{Priority: []string{"High"}}, wantField: "priority"},
		{name: "created-after", in: JQLInput{CreatedAfter: "-7d"}, wantField: "created-after"},
		{name: "created-before", in: JQLInput{CreatedBefore: "-7d"}, wantField: "created-before"},
		{name: "updated-after", in: JQLInput{UpdatedAfter: "-7d"}, wantField: "updated-after"},
		{name: "updated-before", in: JQLInput{UpdatedBefore: "-7d"}, wantField: "updated-before"},
		{name: "watching", in: JQLInput{Watching: true}, wantField: "watching"},
		{name: "order-by", in: JQLInput{OrderBy: "created"}, wantField: "order-by"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.in.JQL = `project = ABC`
			_, err := ComposeJQL(tt.in)
			var inputErr *cli.InputError
			if !errors.As(err, &inputErr) {
				t.Fatalf("ComposeJQL error = %v (%T), want *cli.InputError", err, err)
			}
			if inputErr.Code != cli.CodeFlagNotApplicable {
				t.Errorf("code = %q, want %q", inputErr.Code, cli.CodeFlagNotApplicable)
			}
			if inputErr.Field != tt.wantField {
				t.Errorf("field = %q, want %q", inputErr.Field, tt.wantField)
			}
		})
	}
}

// TestComposeJQLRejectsEmptyFilterValue proves a blank --project ""
// (or a comma-separated run producing one, "--project=A,,B") fails
// loudly rather than composing a clause with an empty JQL string
// literal.
func TestComposeJQLRejectsEmptyFilterValue(t *testing.T) {
	_, err := ComposeJQL(JQLInput{Project: []string{"ABC", "  "}})
	if err == nil {
		t.Fatal("ComposeJQL returned nil error for a blank filter value")
	}
	var inputErr *cli.InputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("ComposeJQL error = %v (%T), want *cli.InputError", err, err)
	}
	if inputErr.Code != cli.CodeFlagValueInvalid {
		t.Errorf("code = %q, want %q", inputErr.Code, cli.CodeFlagValueInvalid)
	}
	if inputErr.Field != "project" {
		t.Errorf("field = %q, want %q", inputErr.Field, "project")
	}
}

// TestComposeJQLRejectsControlCharacterInFilterValue is the end-to-end
// proof that ComposeJQL — not just QuoteJQL in isolation — refuses a
// filter value carrying a JQL-breaking control character, with the
// offending flag named.
func TestComposeJQLRejectsControlCharacterInFilterValue(t *testing.T) {
	_, err := ComposeJQL(JQLInput{Status: []string{"Open\nORDER BY created"}})
	if err == nil {
		t.Fatal("ComposeJQL returned nil error for a status value carrying a newline")
	}
	var inputErr *cli.InputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("ComposeJQL error = %v (%T), want *cli.InputError", err, err)
	}
	if inputErr.Field != "status" {
		t.Errorf("field = %q, want %q", inputErr.Field, "status")
	}
}

// TestComposeJQLRejectsControlCharacterInReporterOrPriority extends the
// injection coverage to the two NEW IN-clause filters, the same way
// TestComposeJQLRejectsControlCharacterInFilterValue already covers
// --status.
func TestComposeJQLRejectsControlCharacterInReporterOrPriority(t *testing.T) {
	tests := []struct {
		name      string
		in        JQLInput
		wantField string
	}{
		{name: "reporter", in: JQLInput{Reporter: []string{"ada\nORDER BY created"}}, wantField: "reporter"},
		{name: "priority", in: JQLInput{Priority: []string{"High\nORDER BY created"}}, wantField: "priority"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ComposeJQL(tt.in)
			if err == nil {
				t.Fatalf("ComposeJQL returned nil error for a hostile %s value", tt.wantField)
			}
			var inputErr *cli.InputError
			if !errors.As(err, &inputErr) {
				t.Fatalf("ComposeJQL error = %v (%T), want *cli.InputError", err, err)
			}
			if inputErr.Field != tt.wantField {
				t.Errorf("field = %q, want %q", inputErr.Field, tt.wantField)
			}
		})
	}
}

// TestComposeJQLPriorityValueIsQuotedNotInjected is the direct injection
// proof for --priority: a value carrying an unescaped quote and a fresh
// boolean clause must come back as ONE quoted, escaped string literal —
// never as a closing quote plus trailing JQL the server would parse as a
// second clause.
func TestComposeJQLPriorityValueIsQuotedNotInjected(t *testing.T) {
	malicious := `High" OR project = "SECRET`
	got, err := ComposeJQL(JQLInput{Priority: []string{malicious}})
	if err != nil {
		t.Fatalf("ComposeJQL: %v", err)
	}
	want := `priority in ("High\" OR project = \"SECRET")`
	if got != want {
		t.Errorf("ComposeJQL = %q, want %q", got, want)
	}
}

// TestComposeJQLDateFiltersComposeAsRangeClauses pins the four date
// flags' own composition shape: >=/<= against the field, ANDed with
// everything else, in the fixed declared order.
func TestComposeJQLDateFiltersComposeAsRangeClauses(t *testing.T) {
	got, err := ComposeJQL(JQLInput{
		CreatedAfter:  "2026-08-01",
		CreatedBefore: "-1d",
		UpdatedAfter:  "-2w",
		UpdatedBefore: "2026-08-01 10:00",
	})
	if err != nil {
		t.Fatalf("ComposeJQL: %v", err)
	}
	want := `created >= "2026-08-01" AND created <= -1d AND updated >= -2w AND updated <= "2026-08-01 10:00"`
	if got != want {
		t.Errorf("ComposeJQL = %q, want %q", got, want)
	}
}

// TestComposeJQLDateFilterRejectsUnparsableValue proves a value that is
// neither an absolute date nor a relative one Jira understands is
// refused with a classified error naming the offending flag, rather than
// escaped straight into the query.
func TestComposeJQLDateFilterRejectsUnparsableValue(t *testing.T) {
	_, err := ComposeJQL(JQLInput{CreatedAfter: "next tuesday"})
	if err == nil {
		t.Fatal("ComposeJQL returned nil error for an unparsable date")
	}
	var inputErr *cli.InputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("ComposeJQL error = %v (%T), want *cli.InputError", err, err)
	}
	if inputErr.Code != cli.CodeFlagValueInvalid {
		t.Errorf("code = %q, want %q", inputErr.Code, cli.CodeFlagValueInvalid)
	}
	if inputErr.Field != "created-after" {
		t.Errorf("field = %q, want %q", inputErr.Field, "created-after")
	}
}

// TestComposeJQLDateFilterRejectsControlCharacter extends the injection
// coverage to the date flags: a value carrying a JQL-breaking control
// character matches neither the relative pattern nor any absolute
// layout, so it is refused the same way QuoteJQL refuses one directly.
func TestComposeJQLDateFilterRejectsControlCharacter(t *testing.T) {
	_, err := ComposeJQL(JQLInput{UpdatedBefore: "2026-08-01\nORDER BY created"})
	if err == nil {
		t.Fatal("ComposeJQL returned nil error for a date value carrying a newline")
	}
	var inputErr *cli.InputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("ComposeJQL error = %v (%T), want *cli.InputError", err, err)
	}
	if inputErr.Field != "updated-before" {
		t.Errorf("field = %q, want %q", inputErr.Field, "updated-before")
	}
}

// TestComposeJQLWatchingComposesToCurrentUserFunction pins --watching's
// own fixed clause — no user-typed value at all, so nothing here is a
// candidate for injection.
func TestComposeJQLWatchingComposesToCurrentUserFunction(t *testing.T) {
	got, err := ComposeJQL(JQLInput{Watching: true})
	if err != nil {
		t.Fatalf("ComposeJQL: %v", err)
	}
	if want := `watcher = currentUser()`; got != want {
		t.Errorf("ComposeJQL = %q, want %q", got, want)
	}
}

// TestComposeJQLWatchingAndsWithOtherFilters proves --watching composes
// like every other filter here: AND, not a replacement for the rest of
// the query.
func TestComposeJQLWatchingAndsWithOtherFilters(t *testing.T) {
	got, err := ComposeJQL(JQLInput{Project: []string{"ABC"}, Watching: true})
	if err != nil {
		t.Fatalf("ComposeJQL: %v", err)
	}
	if want := `project in ("ABC") AND watcher = currentUser()`; got != want {
		t.Errorf("ComposeJQL = %q, want %q", got, want)
	}
}

// TestComposeJQLOrderByAppendsOrderByClause proves --order-by composes
// as a trailing ORDER BY suffix, after every AND-joined filter clause,
// using that field's own default direction.
func TestComposeJQLOrderByAppendsOrderByClause(t *testing.T) {
	got, err := ComposeJQL(JQLInput{Project: []string{"ABC"}, OrderBy: "created"})
	if err != nil {
		t.Fatalf("ComposeJQL: %v", err)
	}
	if want := `project in ("ABC") ORDER BY created DESC`; got != want {
		t.Errorf("ComposeJQL = %q, want %q", got, want)
	}
}

// TestComposeJQLOrderByAloneNeedsNoFilterClauses proves --order-by with
// no filters at all still composes to a bare ORDER BY, valid JQL on its
// own (matching everything).
func TestComposeJQLOrderByAloneNeedsNoFilterClauses(t *testing.T) {
	got, err := ComposeJQL(JQLInput{OrderBy: "key"})
	if err != nil {
		t.Fatalf("ComposeJQL: %v", err)
	}
	if want := `ORDER BY key ASC`; got != want {
		t.Errorf("ComposeJQL = %q, want %q", got, want)
	}
}

// TestComposeJQLOrderByReverseFlipsDirection pins --reverse's own job:
// flipping whatever orderByFieldDefaultDirection says for the field.
func TestComposeJQLOrderByReverseFlipsDirection(t *testing.T) {
	got, err := ComposeJQL(JQLInput{OrderBy: "created", Reverse: true})
	if err != nil {
		t.Fatalf("ComposeJQL: %v", err)
	}
	if want := `ORDER BY created ASC`; got != want {
		t.Errorf("ComposeJQL = %q, want %q", got, want)
	}

	got, err = ComposeJQL(JQLInput{OrderBy: "key", Reverse: true})
	if err != nil {
		t.Fatalf("ComposeJQL: %v", err)
	}
	if want := `ORDER BY key DESC`; got != want {
		t.Errorf("ComposeJQL = %q, want %q", got, want)
	}
}

// TestComposeJQLOrderByRejectsUnknownField proves an unknown --order-by
// field is refused, listing the allowed set — never escaped straight
// into the query the way a raw --jql passthrough would be.
func TestComposeJQLOrderByRejectsUnknownField(t *testing.T) {
	_, err := ComposeJQL(JQLInput{OrderBy: "votes"})
	if err == nil {
		t.Fatal("ComposeJQL returned nil error for an unknown --order-by field")
	}
	var inputErr *cli.InputError
	if !errors.As(err, &inputErr) {
		t.Fatalf("ComposeJQL error = %v (%T), want *cli.InputError", err, err)
	}
	if inputErr.Code != cli.CodeFlagValueInvalid {
		t.Errorf("code = %q, want %q", inputErr.Code, cli.CodeFlagValueInvalid)
	}
	if inputErr.Field != "order-by" {
		t.Errorf("field = %q, want %q", inputErr.Field, "order-by")
	}
	for _, field := range allowedOrderByFields {
		if !strings.Contains(inputErr.Message, field) {
			t.Errorf("message = %q, want it to list the allowed field %q", inputErr.Message, field)
		}
	}
}

// TestOrderByFieldDefaultDirectionCoversEveryAllowedField pins that
// allowedOrderByFields and orderByFieldDefaultDirection's own keys can
// never drift apart — one listing the accepted set for an error message,
// the other providing the default direction jqlOrderByClause looks up
// for every field that set names.
func TestOrderByFieldDefaultDirectionCoversEveryAllowedField(t *testing.T) {
	if len(allowedOrderByFields) != len(orderByFieldDefaultDirection) {
		t.Fatalf("allowedOrderByFields has %d entries, orderByFieldDefaultDirection has %d — they must name the same fields",
			len(allowedOrderByFields), len(orderByFieldDefaultDirection))
	}
	for _, field := range allowedOrderByFields {
		direction, ok := orderByFieldDefaultDirection[field]
		if !ok {
			t.Errorf("allowedOrderByFields contains %q, which has no entry in orderByFieldDefaultDirection", field)
			continue
		}
		if direction != "ASC" && direction != "DESC" {
			t.Errorf("orderByFieldDefaultDirection[%q] = %q, want ASC or DESC", field, direction)
		}
	}
}
