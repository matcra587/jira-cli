package core

import (
	"errors"
	"fmt"

	"github.com/matcra587/jira-cli/internal/jira"
)

// JQLValidationMode uses the pinned parser's exact validation options. None
// skips semantic validation; syntax errors may still appear in its results.
type JQLValidationMode string

const (
	JQLValidationStrict JQLValidationMode = "strict"
	JQLValidationWarn   JQLValidationMode = "warn"
	JQLValidationNone   JQLValidationMode = "none"
)

// Validate checks the parser's closed set of semantic validation modes.
func (mode JQLValidationMode) Validate() error {
	switch mode {
	case JQLValidationStrict, JQLValidationWarn, JQLValidationNone:
	default:
		return errors.New("jql validation mode must be strict, warn or none")
	}
	return nil
}

// ValidateJQLQueries checks the parser's request boundary. Whitespace and
// duplicate queries are intentional input; Jira owns syntax and semantics.
// The pin declares no query-count limit, so no arbitrary limit is imposed.
func ValidateJQLQueries(queries []string, mode JQLValidationMode) error {
	if err := mode.Validate(); err != nil {
		return err
	}
	if len(queries) == 0 {
		return errors.New("at least one JQL query is required")
	}
	for i, query := range queries {
		if query == "" {
			return fmt.Errorf("jql query %d must not be empty", i+1)
		}
	}
	return nil
}

// JQLParseRead retains the native response and safe-read retry facts. Jira may
// normalize returned query text; callers retain the original inputs separately.
type JQLParseRead struct {
	Parsed *jira.ParsedJqlQueries
	Retry  RetryInfo
}

// JQLReferenceRead retains nullable metadata, extensions and transport facts.
type JQLReferenceRead struct {
	Reference *jira.JQLReferenceData
	Retry     RetryInfo
}
