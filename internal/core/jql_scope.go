package core

import (
	"fmt"
	"strings"
)

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

// NarrowJQL applies a named lens without changing the base query's ordering.
// Both conditions are parenthesized so OR clauses retain their authored scope.
func NarrowJQL(base, lens string) (string, error) {
	conditions, order := SplitJQLOrderBy(base)
	extra, lensOrder := SplitJQLOrderBy(lens)
	if extra == "" || lensOrder != "" {
		return "", fmt.Errorf("a lens must contain conditions without ORDER BY")
	}
	if conditions == "" {
		conditions = extra
	} else {
		conditions = "(" + conditions + ") AND (" + extra + ")"
	}
	if order != "" {
		conditions += " " + order
	}
	return conditions, nil
}
