package command

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
)

// filterClause pairs one convenience flag's collected values with the
// JQL field name it composes into. The order this list is built in
// (ComposeJQL below) is the fixed AND order every composed query uses,
// so the same flags always produce byte-identical JQL — useful for a
// test, and for a caller that caches on the string.
type filterClause struct {
	// flag is the --name a caller typed, for the error Field and doc.
	flag string
	// field is the JQL field name the flag composes into.
	field string
	// values are the flag's own collected values, already comma-split by
	// pflag's StringSlice.
	values []string
}

// JQLInput contains structured filters and optional board or current-account
// scope. Raw JQL is exclusive with structured input unless current-account
// scope explicitly permits narrowing. Direction and custom ordering remain
// explicit so list callers can retain their established presets.
type JQLInput struct {
	JQL string
	// AssignedToCurrentUser pins the assignee and allows raw conditions to
	// narrow the structured filters. The raw query cannot replace this scope.
	AssignedToCurrentUser bool
	// BoardJQL is the query behind --board/--board-id, verbatim from the
	// board's filter. It is a SCOPE rather than a filter: it composes as
	// a parenthesised leading clause that the convenience filters narrow,
	// and cannot be combined with an explicit query. See ComposeJQL.
	BoardJQL                   string
	Project, Assignee, Status  []string
	Key, Epic                  []string
	Created, Updated, Resolved string
	// Descending sets an explicit direction; nil retains the field default.
	Descending *bool
	// CustomOrder permits a quoted field identifier instead of the list preset.
	CustomOrder                 bool
	IssueType, Label            []string
	Reporter, Priority          []string
	OrderBy                     string
	Reverse                     bool
	CreatedAfter, CreatedBefore string
	UpdatedAfter, UpdatedBefore string
	Watching                    bool
}

// ComposeJQL joins structured filters with AND in a stable order. Values on
// each positive filter are alternatives; status exclusions are constraints.
// Board conditions are parenthesized before narrowing, and their ordering is
// retained unless OrderBy explicitly overrides or disables it. Raw JQL passes
// through when no structured filter competes with it. Current-account scope
// instead narrows parenthesized raw conditions, retaining their own ordering.
func ComposeJQL(in JQLInput) (string, error) {
	if in.AssignedToCurrentUser && len(in.Assignee) != 0 {
		e := cli.NewInputError(cli.CodeFlagConflict, "The current-account scope cannot be combined with an assignee filter")
		e.Field = "assignee"
		return "", e
	}
	assignees := in.Assignee
	if in.AssignedToCurrentUser {
		assignees = []string{"me"}
	}
	filters := []filterClause{
		{flag: "project", field: "project", values: in.Project},
		{flag: "key", field: "key", values: in.Key},
		{flag: "epic", field: "parent", values: in.Epic},
		{flag: "assignee", field: "assignee", values: assignees},
		{flag: "status", field: "status", values: in.Status},
		{flag: "type", field: "issuetype", values: in.IssueType},
		{flag: "label", field: "labels", values: in.Label},
		{flag: "reporter", field: "reporter", values: in.Reporter},
		{flag: "priority", field: "priority", values: in.Priority},
	}

	var clauses []string
	var firstFilterFlag string
	for _, f := range filters {
		if len(f.values) == 0 {
			continue
		}
		if firstFilterFlag == "" {
			firstFilterFlag = f.flag
		}
		clause, err := structuredJQLClause(f.field, f.values)
		if err != nil {
			e := cli.NewInputError(cli.CodeFlagValueInvalid, err.Error())
			e.Field = f.flag
			return "", e
		}
		clauses = append(clauses, clause)
	}

	dateFilters := []struct {
		flag, field, op, value string
	}{
		{"created-after", "created", ">=", in.CreatedAfter},
		{"created-before", "created", "<=", in.CreatedBefore},
		{"updated-after", "updated", ">=", in.UpdatedAfter},
		{"updated-before", "updated", "<=", in.UpdatedBefore},
	}
	for _, d := range dateFilters {
		if d.value == "" {
			continue
		}
		if firstFilterFlag == "" {
			firstFilterFlag = d.flag
		}
		clause, err := jqlDateClause(d.field, d.op, d.value)
		if err != nil {
			e := cli.NewInputError(cli.CodeFlagValueInvalid, err.Error())
			e.Field = d.flag
			return "", e
		}
		clauses = append(clauses, clause)
	}

	for _, d := range []struct{ field, value string }{
		{field: "created", value: in.Created},
		{field: "updated", value: in.Updated},
		{field: "resolved", value: in.Resolved},
	} {
		if d.value == "" {
			continue
		}
		if firstFilterFlag == "" {
			firstFilterFlag = d.field
		}
		clause, err := jqlDateExpression(d.field, d.value)
		if err != nil {
			e := cli.NewInputError(cli.CodeFlagValueInvalid, err.Error())
			e.Field = d.field
			return "", e
		}
		clauses = append(clauses, clause)
	}

	if in.Descending != nil && (in.OrderBy == "" || in.Reverse) {
		e := cli.NewInputError(cli.CodeFlagConflict, "--desc needs --order-by and cannot be combined with --reverse")
		e.Field = "desc"
		return "", e
	}

	if in.Watching {
		if firstFilterFlag == "" {
			firstFilterFlag = "watching"
		}
		// No user-typed value at all — a bare bool composing to a fixed
		// JQL function call, nothing here for QuoteJQL to protect.
		clauses = append(clauses, "watcher = currentUser()")
	}

	var orderClause string
	if in.OrderBy != "" {
		if firstFilterFlag == "" {
			firstFilterFlag = "order-by"
		}
		oc, err := structuredJQLOrder(in)
		if err != nil {
			e := cli.NewInputError(cli.CodeFlagValueInvalid, err.Error())
			e.Field = "order-by"
			return "", e
		}
		orderClause = oc
	}

	jql := strings.TrimSpace(in.JQL)
	board := strings.TrimSpace(in.BoardJQL)
	if jql != "" && in.AssignedToCurrentUser {
		if err := validateJQLScope(jql); err != nil {
			e := cli.NewInputError(cli.CodeFlagValueInvalid, err.Error())
			e.Field = "jql"
			return "", e
		}
	}
	if board != "" {
		if err := validateJQLScope(board); err != nil {
			e := cli.NewInputError(cli.CodeFlagValueInvalid, err.Error())
			e.Field = "board"
			return "", e
		}
		if firstFilterFlag == "" {
			firstFilterFlag = "board"
		}
		// The board's own ORDER BY is split off before its query becomes a
		// clause: JQL only accepts one, at the very end of the whole query,
		// so leaving it inside the parentheses would not parse. It is then
		// reinstated as the run's ordering unless --order-by asked for a
		// different one, because a board's order is part of what the board
		// IS — a backlog in rank order is not the same list shuffled.
		boardFilter, boardOrder := SplitJQLOrderBy(board)
		if boardFilter == "" {
			e := cli.NewInputError(cli.CodeFlagValueInvalid,
				fmt.Sprintf("--%s resolved to a query with no conditions, only an ordering, which would match every issue on the site", "board"))
			e.Field = "board"
			return "", e
		}
		clauses = append([]string{"(" + boardFilter + ")"}, clauses...)
		if in.OrderBy == "" {
			orderClause = boardOrder
		}
	}
	if in.AssignedToCurrentUser && board != "" {
		// The required account is not a removable flag. A raw query that
		// competes with board scope must name the actual board selector.
		firstFilterFlag = "board"
	}

	switch {
	case jql != "" && firstFilterFlag != "" && (!in.AssignedToCurrentUser || board != ""):
		e := cli.NewInputError(cli.CodeFlagNotApplicable,
			fmt.Sprintf("--%s cannot be combined with --jql: composing an explicit query with an implied filter is ambiguous, so the filter is refused rather than silently appended to or overridden by your query — drop --%s, or fold the filter into --jql yourself", firstFilterFlag, firstFilterFlag))
		e.Field = firstFilterFlag
		return "", e
	case jql != "" && in.AssignedToCurrentUser:
		conditions, rawOrder := SplitJQLOrderBy(jql)
		if conditions != "" {
			clauses = append(clauses, "("+conditions+")")
		}
		if rawOrder != "" {
			orderClause = rawOrder
		}
	case jql != "":
		return jql, nil
	}
	composed := strings.Join(clauses, " AND ")
	if orderClause != "" {
		if composed != "" {
			composed += " "
		}
		composed += orderClause
	}
	return composed, nil
}

// validateJQLScope requires closed groups and literals before a query is
// parenthesized beneath another scope. An unmatched closer in the input could
// otherwise close our wrapper and make a later OR escape that scope. This is
// a composition boundary check; Jira still owns full grammar validation.
func validateJQLScope(query string) error {
	depth := 0
	var quote byte
	for i := 0; i < len(query); i++ {
		c := query[i]
		if quote != 0 {
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return errors.New("scoped JQL has an unmatched closing parenthesis")
			}
			depth--
		}
	}
	if quote != 0 || depth != 0 {
		return errors.New("scoped JQL needs balanced parentheses and closed quoted literals")
	}
	return nil
}

// jqlInClause builds one `field in (v1, v2, ...)` clause, quoting every
// value through QuoteJQL so it cannot break out of its own string
// literal. `in (...)` is used even for a single value — always valid
// JQL, and one shape means no special-casing between "one value" and
// "several".
func jqlInClause(field string, values []string) (string, error) {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		var err error
		v, err = cleanJQLValue(v)
		if err != nil {
			return "", err
		}
		q, err := QuoteJQL(v)
		if err != nil {
			return "", err
		}
		quoted = append(quoted, q)
	}
	return fmt.Sprintf("%s in (%s)", field, strings.Join(quoted, ", ")), nil
}

// allowedOrderByFields is the issue-list preset, listed in stable order in
// refusal messages. CustomOrder explicitly permits other safe identifiers.
var allowedOrderByFields = []string{"created", "updated", "priority", "status", "key", "summary", "assignee"}

// orderByFieldDefaultDirection is --order-by's own default direction per
// field, before --reverse (when given) flips it. Two families, not seven
// one-off choices:
//
//   - DESC for a field where the BIGGER or NEWER value is the
//     interesting one by default: created/updated (most recent first —
//     the "what changed lately" reading a search is usually for), and
//     priority (this CLI's own severity scale — PriorityIcon's rank
//     order in sinks.go runs Highest, High, Medium, Low, Lowest, and
//     DESC surfaces "Highest" first, matching that same scale).
//   - ASC for an identity or text field where alphabetical reading order
//     is the natural default: status, key, summary, assignee — there is
//     no "bigger" status or assignee, only an order a human reads top to
//     bottom the way a directory listing does.
//
// Every key here must also appear in allowedOrderByFields, and vice
// versa — TestOrderByFieldDefaultDirectionCoversEveryAllowedField pins
// that the two lists cannot drift apart.
var orderByFieldDefaultDirection = map[string]string{
	"created":  "DESC",
	"updated":  "DESC",
	"priority": "DESC",
	"status":   "ASC",
	"key":      "ASC",
	"summary":  "ASC",
	"assignee": "ASC",
}

// flipDirection swaps ASC/DESC — --reverse's own mechanism, pulled out
// so structuredJQLOrder reads as "look up the default, maybe flip it"
// rather than a second switch buried inside it.
func flipDirection(direction string) string {
	if direction == "ASC" {
		return "DESC"
	}
	return "ASC"
}

// relativeDateFilter accepts Jira's minute/hour/day/week operands. Named
// bounds permit an omitted sign; expression filters require an explicit sign.
var relativeDateFilter = regexp.MustCompile(`^([+-]?)([0-9]+)([dwhm])$`)

// absoluteDateLayouts are the date/time layouts an absolute
// --created-after/--created-before/--updated-after/--updated-before
// value is tried against, in order — both separators JQL's own date
// fields accept (yyyy-MM-dd and yyyy/MM/dd), each with and without a
// trailing HH:mm. The layouts cannot ambiguously match the same input (a
// bare date has no space, a date-plus-time always does), so trying them
// in this fixed order is never a source of nondeterminism.
var absoluteDateLayouts = []string{
	"2006-01-02 15:04",
	"2006/01/02 15:04",
	"2006-01-02",
	"2006/01/02",
}

// jqlDateClause validates and canonicalizes date operands. A positive sign
// is reserved JQL syntax and is not a valid date-period prefix even when
// quoted. Canonical positive periods omit the sign; negative periods retain it.
// https://support.atlassian.com/jira-software-cloud/docs/jql-fields/
func jqlDateClause(field, op, value string) (string, error) {
	if m := relativeDateFilter.FindStringSubmatch(value); m != nil {
		sign, digits, unit := m[1], m[2], m[3]
		operand := sign + digits + unit
		if sign == "+" {
			operand = digits + unit
		}
		return fmt.Sprintf("%s %s %s", field, op, operand), nil
	}

	for _, layout := range absoluteDateLayouts {
		t, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		canonicalLayout := "2006-01-02"
		if strings.Contains(layout, "15:04") {
			canonicalLayout = "2006-01-02 15:04"
		}
		quoted, err := QuoteJQL(t.Format(canonicalLayout))
		if err != nil {
			// canonical is built entirely from time.Parse's own output,
			// reformatted through the SAME stdlib layout machinery that
			// just parsed it — it can carry no quote, backslash, or
			// control character, so QuoteJQL failing here would be this
			// function's own bug, never a bad caller value.
			return "", fmt.Errorf("date filter: %w", err)
		}
		return fmt.Sprintf("%s %s %s", field, op, quoted), nil
	}

	return "", fmt.Errorf("value %q is neither an absolute date (e.g. %q) nor a relative one Jira understands (e.g. %q, %q)",
		value, "2026-08-01", "-7d", "-2w")
}

// SplitJQLOrderBy divides a JQL query into its conditions and its trailing
// ORDER BY clause, returning the conditions first and the clause (or "")
// second.
//
// It scans for the LAST `ORDER BY` that sits at the top level — outside
// any string literal and outside any parentheses — because that is the
// only position JQL's own grammar allows one in. Scanning naively for
// the substring would cut a query in half at `summary ~ "order by
// priority"`, which is a perfectly legal filter and exactly the sort of
// thing a saved board filter contains.
//
// A query with no ORDER BY comes back unchanged with an empty clause,
// which is the common case and needs no special handling by the caller.
func SplitJQLOrderBy(query string) (conditions, order string) {
	const marker = "order"

	depth := 0
	inQuote := false
	var quote byte
	last := -1
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case inQuote:
			// A backslash escapes the next byte inside a JQL string
			// literal, including a closing quote, so it must be skipped or
			// `"a\""` would look like it ended early.
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				inQuote = false
			}
		case c == '"' || c == '\'':
			inQuote = true
			quote = c
		case c == '(':
			depth++
		case c == ')':
			if depth > 0 {
				depth--
			}
		case depth == 0 && (c == 'o' || c == 'O'):
			if i+len(marker) <= len(query) && strings.EqualFold(query[i:i+len(marker)], marker) &&
				startsToken(query, i) && endsToken(query, i+len(marker)) {
				j := i + len(marker)
				for j < len(query) && isJQLSpace(query[j]) {
					j++
				}
				if j > i+len(marker) && j+2 <= len(query) && strings.EqualFold(query[j:j+2], "by") && endsToken(query, j+2) {
					last = i
				}
			}
		}
	}
	if last < 0 {
		return strings.TrimSpace(query), ""
	}
	return strings.TrimSpace(query[:last]), strings.TrimSpace(query[last:])
}

// startsToken reports whether index i begins a word rather than landing
// inside one, so `reorder by` is not mistaken for `order by`.
func startsToken(s string, i int) bool {
	return i == 0 || !isJQLWordByte(s[i-1])
}

// endsToken reports whether index i ends a word, so `order byte` is not
// mistaken for `order by`.
func endsToken(s string, i int) bool {
	return i >= len(s) || !isJQLWordByte(s[i])
}

// isJQLWordByte reports whether c can appear inside a bare JQL word.
func isJQLWordByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '_' || c == '-' || c >= 0x80:
		return true
	}
	return false
}

func isJQLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
