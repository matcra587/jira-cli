package core

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"regexp"
	"time"

	"github.com/matcra587/jira-cli/internal/adf"
)

func prepareNumber(raw json.RawMessage) (preparedIssueFieldValue, error) {
	value := bytes.TrimSpace(raw)
	var text string
	quoted := json.Unmarshal(value, &text) == nil
	if quoted {
		value = []byte(text)
	}
	if len(value) == 0 || !jsontext.Value(value).IsValid() || value[0] != '-' && (value[0] < '0' || value[0] > '9') {
		return preparedIssueFieldValue{}, fmt.Errorf("expected a finite JSON number or numeric string")
	}
	if quoted {
		return validatedPreparationValue(bytes.Clone(value), nil), nil
	}
	return validatedPreparationValue(raw, nil), nil
}

func prepareDate(raw json.RawMessage) (preparedIssueFieldValue, error) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return preparedIssueFieldValue{}, fmt.Errorf("expected a YYYY-MM-DD date string")
	}
	if _, err := time.Parse(time.DateOnly, value); err != nil {
		return preparedIssueFieldValue{}, fmt.Errorf("expected an in-range YYYY-MM-DD date: %w", err)
	}
	return validatedPreparationValue(raw, nil), nil
}

func prepareDatetime(raw json.RawMessage) (preparedIssueFieldValue, error) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return preparedIssueFieldValue{}, fmt.Errorf("expected an ISO-8601 datetime string with a timezone")
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999-0700"} {
		if _, err := time.Parse(layout, value); err == nil {
			return validatedPreparationValue(raw, nil), nil
		}
	}
	return preparedIssueFieldValue{}, fmt.Errorf("expected an in-range ISO-8601 datetime string with a timezone")
}

func prepareCascading(raw json.RawMessage) (preparedIssueFieldValue, error) {
	if _, err := prepareIdentity("value", "id")(raw); err != nil {
		return preparedIssueFieldValue{}, err
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return preparedIssueFieldValue{}, fmt.Errorf("cascading selection requires a native object")
	}
	if child, exists := object["child"]; exists {
		var childObject map[string]json.RawMessage
		if json.Unmarshal(child, &childObject) != nil || childObject == nil {
			return preparedIssueFieldValue{}, fmt.Errorf("cascading child requires a native identity object")
		}
		if _, err := prepareIdentity("value", "id")(child); err != nil {
			return preparedIssueFieldValue{}, fmt.Errorf("cascading child: %w", err)
		}
	}
	return validatedPreparationValue(raw, nil), nil
}

func prepareTextarea(raw json.RawMessage) (preparedIssueFieldValue, error) {
	var markdown string
	if json.Unmarshal(raw, &markdown) == nil {
		document, warnings, err := adf.FromMarkdownLossy(markdown)
		if err != nil {
			return preparedIssueFieldValue{}, err
		}
		for _, warning := range warnings {
			if warning.Lossy {
				return preparedIssueFieldValue{}, fmt.Errorf("Markdown drops content; use adf convert to review and explicitly accept its diagnostics before preparing native ADF")
			}
		}
		if document == nil {
			return preparedIssueFieldValue{}, fmt.Errorf("Markdown conversion produced no document")
		}
		encoded, err := document.Marshal()
		if err == nil {
			err = adf.Validate(encoded)
		}
		if err != nil {
			return preparedIssueFieldValue{}, err
		}
		return validatedPreparationValue(encoded, warnings), nil
	}
	if _, err := adf.ParseDocument(raw); err != nil {
		return preparedIssueFieldValue{}, fmt.Errorf("expected Markdown or a native ADF document: %w", err)
	}
	// Preserved native ADF may contain future or schema-invalid content. The
	// preservation parser is not a schema validator and must not claim one ran.
	return preparedIssueFieldValue{Value: raw}, nil
}

// Jira keys have a fixed syntactic shape; no ID is inferred from a scalar.
var preparationParentKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[0-9]+$`)

func prepareParent(raw json.RawMessage) (preparedIssueFieldValue, error) {
	value, err := prepareIdentity("key", "id")(raw)
	if err != nil {
		return preparedIssueFieldValue{}, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(value.Value, &object); err != nil {
		return preparedIssueFieldValue{}, err
	}
	if key, present := object["key"]; present {
		var text string
		if json.Unmarshal(key, &text) != nil || !preparationParentKey.MatchString(text) {
			return preparedIssueFieldValue{}, fmt.Errorf("parent key must have Jira's KEY-123 shape; use an explicit id object for an ID")
		}
	}
	return value, nil
}
