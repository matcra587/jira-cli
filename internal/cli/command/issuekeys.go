package command

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/matcra587/jira-cli/internal/cli"
)

// MaxIssueKeyExpansion bounds explicit comma lists and inclusive ranges before
// a mutation constructs a client. Duplicate keys retain their first position.
const MaxIssueKeyExpansion = 1000

var issueKeyPattern = regexp.MustCompile(`^([A-Z][A-Z0-9_]*)-([0-9]+)$`)

// ParseIssueKey accepts one explicit key, never a list or range.
func ParseIssueKey(value string) (string, error) {
	project, number, err := issueKeyParts(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%d", project, number), nil
}

func issueKeyParts(value string) (string, int, error) {
	parts := issueKeyPattern.FindStringSubmatch(value)
	if parts == nil {
		return "", 0, cli.NewInputError(cli.CodeArgValueInvalid, fmt.Sprintf("invalid issue key %q; use PROJECT-123", value))
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil || n < 1 {
		return "", 0, cli.NewInputError(cli.CodeArgValueInvalid, "issue number must be a positive integer within the supported range")
	}
	return parts[1], n, nil
}

// ParseIssueKeys expands repeated keys, comma lists and inclusive KEY-1:3 or
// KEY-1..KEY-3 ranges. Spaces, descending ranges and mixed projects are refused.
// Iteration uses a bounded count so an endpoint at MaxInt cannot overflow a loop.
func ParseIssueKeys(inputs []string) ([]string, error) {
	keys := []string{}
	seen := map[string]bool{}
	for _, input := range inputs {
		for part := range strings.SplitSeq(input, ",") {
			left, right, ranged := strings.Cut(part, "..")
			if !ranged {
				left, right, ranged = strings.Cut(part, ":")
			}
			project, start, err := issueKeyParts(left)
			if err != nil {
				return nil, err
			}
			end := start
			if ranged {
				if strings.Contains(right, "-") {
					var endProject string
					endProject, end, err = issueKeyParts(right)
					if err == nil && endProject != project {
						err = cli.NewInputError(cli.CodeArgValueInvalid, "issue key range endpoints must use the same project")
					}
				} else {
					_, end, err = issueKeyParts(project + "-" + right)
				}
				if err != nil {
					return nil, err
				}
				if end < start {
					return nil, cli.NewInputError(cli.CodeArgValueInvalid, "descending issue key ranges are not supported")
				}
			}
			if end-start >= MaxIssueKeyExpansion {
				return nil, keyExpansionError()
			}
			for offset := range end - start + 1 {
				key := fmt.Sprintf("%s-%d", project, start+offset)
				if seen[key] {
					continue
				}
				if len(keys) == MaxIssueKeyExpansion {
					return nil, keyExpansionError()
				}
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	return keys, nil
}

func keyExpansionError() error {
	return cli.NewInputError(cli.CodeArgValueInvalid, fmt.Sprintf("issue key expansion exceeds maximum of %d keys; split the explicit target list", MaxIssueKeyExpansion))
}
