package command

import (
	"bytes"
	"encoding/csv"
	"errors"
	"slices"
	"strings"
)

// CSVCompletion retains the exact completed prefix of one CSV argument and the
// decoded field being completed. It never interprets shell quoting or expansion.
type CSVCompletion struct {
	head     string
	prefix   string
	selected []string
}

// ParseCSVCompletion accepts a partial final quoted field, while completed
// fields must obey encoding/csv's strict grammar. Record separators cannot be
// represented by the completion protocol. x/strings.SplitCSV trims values and
// drops quoting; clib's last-comma split cannot distinguish quoted delimiters.
func ParseCSVCompletion(value string) (CSVCompletion, error) {
	if strings.ContainsAny(value, "\r\n") {
		return CSVCompletion{}, errors.New("csv completion requires one line")
	}
	var out CSVCompletion
	var field strings.Builder
	const (
		plain = iota
		quoted
		closed
	)
	state, start := plain, true
	for i := 0; i < len(value); i++ {
		char := value[i]
		switch state {
		case quoted:
			if char != '"' {
				field.WriteByte(char)
				continue
			}
			if i+1 < len(value) && value[i+1] == '"' {
				field.WriteByte('"')
				i++
				continue
			}
			state = closed
			continue
		case closed:
			if char != ',' {
				return CSVCompletion{}, errors.New("csv completion has data after a closing quote")
			}
		case plain:
			if char == '"' {
				if !start {
					return CSVCompletion{}, errors.New("csv completion has a quote inside an unquoted field")
				}
				state, start = quoted, false
				continue
			}
			if char != ',' {
				field.WriteByte(char)
				start = false
				continue
			}
		}
		out.selected = append(out.selected, field.String())
		out.head = value[:i+1]
		field.Reset()
		state, start = plain, true
	}
	out.prefix = field.String()
	return out, nil
}

func (c CSVCompletion) Prefix() string     { return c.prefix }
func (c CSVCompletion) Selected() []string { return slices.Clone(c.selected) }

// QuotedCandidate always quotes the current CSV field. Fish ranks candidates by
// their native match type and suppresses quoted fields when unquoted fields have
// a better prefix rank. Uniform CSV quoting preserves every matched identity.
func (c CSVCompletion) QuotedCandidate(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", errors.New("csv completion candidate requires one line")
	}
	return c.head + `"` + strings.ReplaceAll(value, `"`, `""`) + `"`, nil
}

// DirectoryCandidate leaves the current CSV quote open so another completion
// can extend this field. Shell quoting remains the native generator's concern.
func (c CSVCompletion) DirectoryCandidate(value string) (string, error) {
	quoted, err := c.QuotedCandidate(value)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(quoted, `"`), nil
}

// Candidate replaces only the unfinished field. The preceding CSV spelling is
// retained exactly; encoding/csv supplies literal commas, quotes and whitespace.
func (c CSVCompletion) Candidate(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", errors.New("csv completion candidate requires one line")
	}
	if value == "" {
		// An unquoted empty record is skipped by encoding/csv. An explicit
		// quoted field preserves one empty value even without earlier fields.
		return c.head + `""`, nil
	}
	var encoded bytes.Buffer
	writer := csv.NewWriter(&encoded)
	if err := writer.Write([]string{value}); err != nil {
		return "", err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}
	return c.head + strings.TrimSuffix(encoded.String(), "\n"), nil
}
