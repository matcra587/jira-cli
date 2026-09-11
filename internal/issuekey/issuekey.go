package issuekey

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/matcra587/jira-cli/internal/errtax"
)

// DefaultMaxExpansion caps how many keys one ParseExpressions call may yield
// when Options.MaxExpansion is unset, bounding range expansion.
const DefaultMaxExpansion = 1000

var keyPattern = regexp.MustCompile(`^([A-Z][A-Z0-9_]+)-([0-9]+)$`)

// Options tunes ParseExpressions. A zero or negative MaxExpansion falls back to
// DefaultMaxExpansion.
type Options struct {
	MaxExpansion int
}

// IsExpression reports whether s begins like an issue-key expression (a single
// key, a range, or a comma list) rather than free text such as a status name.
// It only checks the leading token against the key pattern — it does not
// validate a range end or list members — so it stays cheap and never expands a
// range. ParseExpressions still fully validates the keys afterwards. Callers
// use it to tell an issue key from a status name in mixed argument lists like
// `transition KEY "In Progress"`.
func IsExpression(s string) bool {
	head := strings.TrimSpace(s)
	for _, sep := range []string{",", "..", ":"} {
		if i := strings.Index(head, sep); i >= 0 {
			head = head[:i]
		}
	}
	return keyPattern.MatchString(strings.TrimSpace(head))
}

// ExpansionLimitError reports a locally-enforced issue-key expansion limit.
type ExpansionLimitError struct {
	Max int
}

func (e *ExpansionLimitError) Error() string {
	return fmt.Sprintf("issue key expansion exceeds maximum of %d keys", e.Max)
}

// Code classifies the failure under issue_key_expansion_limit.
func (e *ExpansionLimitError) Code() errtax.Code { return errtax.CodeIssueKeyExpansionLimit }

var _ errtax.Coded = (*ExpansionLimitError)(nil) //nolint:errcheck // compile-time interface assertion

// ParseExpressions expands issue-key expressions into canonical Jira keys.
// Supported forms are single keys, comma lists, and inclusive ranges using
// ":" or ".." with either a numeric end or a full issue key end.
func ParseExpressions(inputs []string, opts Options) ([]string, error) {
	maxExpansion := opts.MaxExpansion
	if maxExpansion <= 0 {
		maxExpansion = DefaultMaxExpansion
	}
	out := make([]string, 0, len(inputs))
	seen := map[string]bool{}
	for _, input := range inputs {
		parts := strings.SplitSeq(input, ",")
		for part := range parts {
			remaining := maxExpansion - len(out)
			if remaining <= 0 {
				return nil, &ExpansionLimitError{Max: maxExpansion}
			}
			if part == "" {
				return nil, fmt.Errorf("issue key expression contains an empty list member")
			}
			if strings.ContainsFunc(part, unicode.IsSpace) {
				return nil, fmt.Errorf(
					"issue key expression %q must not contain whitespace; "+
						"use repeated --key flags or comma-separated keys without spaces",
					part,
				)
			}
			keys, err := parsePart(part, remaining, maxExpansion)
			if err != nil {
				return nil, err
			}
			for _, key := range keys {
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, key)
			}
		}
	}
	return out, nil
}

func parsePart(part string, remaining, maxExpansion int) ([]string, error) {
	if left, right, ok := strings.Cut(part, ".."); ok {
		return parseRange(left, right, remaining, maxExpansion)
	}
	if left, right, ok := strings.Cut(part, ":"); ok {
		return parseRange(left, right, remaining, maxExpansion)
	}
	key, err := parseKey(part)
	if err != nil {
		return nil, err
	}
	return []string{key.String()}, nil
}

func parseRange(left, right string, remaining, maxExpansion int) ([]string, error) {
	start, err := parseKey(left)
	if err != nil {
		return nil, fmt.Errorf("issue key range start: %w", err)
	}
	end, err := parseRangeEnd(right, start.Project)
	if err != nil {
		return nil, fmt.Errorf("issue key range end: %w", err)
	}
	if end.Project != start.Project {
		return nil, fmt.Errorf("issue key range endpoints must use the same project: %s and %s", start.Project, end.Project)
	}
	if end.Number < start.Number {
		return nil, fmt.Errorf(
			"descending issue key ranges are not supported: %s to %s",
			start.String(),
			end.String(),
		)
	}
	count := end.Number - start.Number + 1
	if count > remaining {
		return nil, &ExpansionLimitError{Max: maxExpansion}
	}
	keys := make([]string, 0, count)
	for n := start.Number; n <= end.Number; n++ {
		keys = append(keys, issueKey{Project: start.Project, Number: n}.String())
	}
	return keys, nil
}

func parseRangeEnd(value, project string) (issueKey, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return issueKey{}, fmt.Errorf("range end is required")
	}
	if strings.Contains(value, "-") {
		return parseKey(value)
	}
	n, err := parsePositiveNumber(value)
	if err != nil {
		return issueKey{}, err
	}
	return issueKey{Project: project, Number: n}, nil
}

type issueKey struct {
	Project string
	Number  int
}

func (k issueKey) String() string {
	return fmt.Sprintf("%s-%d", k.Project, k.Number)
}

func parseKey(value string) (issueKey, error) {
	value = strings.TrimSpace(value)
	matches := keyPattern.FindStringSubmatch(value)
	if matches == nil {
		return issueKey{}, fmt.Errorf("invalid issue key %q", value)
	}
	n, err := parsePositiveNumber(matches[2])
	if err != nil {
		return issueKey{}, err
	}
	return issueKey{Project: matches[1], Number: n}, nil
}

func parsePositiveNumber(value string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("issue number %q must be numeric", value)
	}
	if n <= 0 {
		return 0, fmt.Errorf("issue number must be positive")
	}
	return n, nil
}
