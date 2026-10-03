package command

import (
	"fmt"
	"strings"
	"unicode"

	xstrings "github.com/gechr/x/strings"
)

func structuredJQLClause(field string, values []string) (string, error) {
	switch field {
	case "key":
		keys, err := ParseIssueKeys(values)
		if err != nil {
			return "", err
		}
		return jqlInClause(field, keys)
	case "parent":
		keys := make([]string, 0, len(values))
		for _, value := range values {
			key, err := ParseIssueKey(value)
			if err != nil {
				return "", err
			}
			keys = append(keys, key)
		}
		return jqlInClause(field, keys)
	case "assignee", "reporter":
		return jqlUserClause(field, values)
	case "status":
		return jqlStatusClause(values)
	default:
		return jqlInClause(field, values)
	}
}

// cleanJQLValue checks controls before trimming, so malformed input cannot
// become a valid alias or operator through whitespace normalization.
func cleanJQLValue(value string) (string, error) {
	if _, err := QuoteJQL(value); err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("value cannot be empty")
	}
	return value, nil
}

func jqlUserClause(field string, values []string) (string, error) {
	plain, branches := []string{}, []string{}
	for _, value := range values {
		value, err := cleanJQLValue(value)
		if err != nil {
			return "", err
		}
		switch strings.ToLower(value) {
		case "me", "currentuser()":
			branches = append(branches, field+" = currentUser()")
		case "none", "empty", "unassigned":
			branches = append(branches, field+" is EMPTY")
		default:
			plain = append(plain, value)
		}
	}
	if len(plain) > 0 {
		clause, err := jqlInClause(field, plain)
		if err != nil {
			return "", err
		}
		branches = append([]string{clause}, branches...)
	}
	return jqlAlternatives(branches), nil
}

func jqlAlternatives(branches []string) string {
	if len(branches) == 1 {
		return branches[0]
	}
	return "(" + strings.Join(branches, " OR ") + ")"
}

func jqlStatusClause(values []string) (string, error) {
	plain, positives, negatives := []string{}, []string{}, []string{}
	for _, value := range values {
		value, err := cleanJQLValue(value)
		if err != nil {
			return "", err
		}
		switch {
		case strings.HasPrefix(value, "!"):
			name, err := cleanJQLValue(value[1:])
			if err != nil {
				return "", fmt.Errorf("status exclusion: %w", err)
			}
			quoted, err := QuoteJQL(name)
			if err != nil {
				return "", err
			}
			negatives = append(negatives, "status != "+quoted)
		case jqlComparator(value) != "":
			clause, err := jqlStatusCategory(value)
			if err != nil {
				return "", err
			}
			positives = append(positives, clause)
		default:
			plain = append(plain, value)
		}
	}
	if len(plain) > 0 {
		clause, err := jqlInClause("status", plain)
		if err != nil {
			return "", err
		}
		positives = append([]string{clause}, positives...)
	}
	clauses := []string{}
	if len(positives) > 0 {
		clauses = append(clauses, jqlAlternatives(positives))
	}
	return strings.Join(append(clauses, negatives...), " AND "), nil
}

func jqlComparator(value string) string {
	for _, op := range []string{"<=", ">=", "<", ">"} {
		if strings.HasPrefix(value, op) {
			return op
		}
	}
	return ""
}

func jqlStatusCategory(value string) (string, error) {
	op := jqlComparator(value)
	var target int
	switch strings.ToLower(strings.TrimSpace(value[len(op):])) {
	case "to do", "todo":
		target = 0
	case "in progress", "inprogress":
		target = 1
	case "done":
		target = 2
	default:
		return "", fmt.Errorf("status comparator %q needs a category: To Do, In Progress or Done", value)
	}
	categories := []string{}
	for i, category := range []string{"To Do", "In Progress", "Done"} {
		var match bool
		switch op {
		case "<":
			match = i < target
		case "<=":
			match = i <= target
		case ">":
			match = i > target
		case ">=":
			match = i >= target
		}
		if match {
			categories = append(categories, category)
		}
	}
	if len(categories) == 0 {
		return "", fmt.Errorf("status comparator %q matches no category", value)
	}
	if len(categories) == 1 {
		quoted, err := QuoteJQL(categories[0])
		return "statusCategory = " + quoted, err
	}
	return jqlInClause("statusCategory", categories)
}

func jqlDateExpression(field, value string) (string, error) {
	value, err := cleanJQLValue(value)
	if err != nil {
		return "", err
	}
	if left, right, ranged := strings.Cut(value, ".."); ranged {
		left, right = strings.TrimSpace(left), strings.TrimSpace(right)
		if xstrings.AllEmpty(left, right) {
			return "", fmt.Errorf("date range needs at least one bound")
		}
		clauses := []string{}
		for _, bound := range []struct{ op, value string }{{op: ">=", value: left}, {op: "<=", value: right}} {
			if bound.value == "" {
				continue
			}
			clause, err := jqlSignedDateClause(field, bound.op, bound.value)
			if err != nil {
				return "", err
			}
			clauses = append(clauses, clause)
		}
		return strings.Join(clauses, " AND "), nil
	}
	op := jqlComparator(value)
	if op == "" {
		return jqlSignedDateClause(field, ">=", value)
	}
	return jqlSignedDateClause(field, op, strings.TrimSpace(value[len(op):]))
}

func jqlSignedDateClause(field, op, value string) (string, error) {
	if relativeDateFilter.MatchString(value) && !strings.HasPrefix(value, "+") && !strings.HasPrefix(value, "-") {
		return "", fmt.Errorf("relative date %q needs an explicit + or - sign", value)
	}
	return jqlDateClause(field, op, value)
}

func structuredJQLOrder(in JQLInput) (string, error) {
	if in.CustomOrder && in.OrderBy == "none" {
		return "", nil
	}
	direction, known := orderByFieldDefaultDirection[in.OrderBy]
	field := in.OrderBy
	if !known {
		if !in.CustomOrder {
			return "", fmt.Errorf("value %q is not one of the allowed order-by fields (%s)", field, strings.Join(allowedOrderByFields, ", "))
		}
		if !safeJQLField(field) {
			return "", fmt.Errorf("invalid order-by field %q; use a field identifier", field)
		}
		quoted, err := QuoteJQL(field)
		if err != nil {
			return "", err
		}
		field, direction = quoted, "ASC"
	}
	if in.Descending != nil {
		direction = "ASC"
		if *in.Descending {
			direction = "DESC"
		}
	}
	if in.Reverse {
		direction = flipDirection(direction)
	}
	return fmt.Sprintf("ORDER BY %s %s", field, direction), nil
}

func safeJQLField(value string) bool {
	if value == "" {
		return false
	}
	if digits, ok := strings.CutPrefix(value, "cf["); ok {
		if digits, ok = strings.CutSuffix(digits, "]"); !ok {
			return false
		}
		return xstrings.IsDigits(digits)
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}
