package jira

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	xslices "github.com/gechr/x/slices"
)

// ParseJQLResponse checks native objects before generated decoding. Jira may
// normalize query text; count and order associate results with the request.
func ParseJQLResponse(data []byte, queryCount int) (ParsedJqlQueries, error) {
	members, err := DecodeNativeObject(data, parsedJqlQueriesKeys)
	if err != nil {
		return ParsedJqlQueries{}, err
	}
	queries, err := jqlObjectArray(members["queries"], parsedJqlQueryKeys)
	if err != nil {
		return ParsedJqlQueries{}, fmt.Errorf("queries: %w", err)
	}
	if queryCount < 1 || len(queries) != queryCount {
		return ParsedJqlQueries{}, errors.New("parsed query count does not match the request")
	}
	for i, query := range queries {
		for _, name := range []string{"errors", "warnings"} {
			if err := jqlStringArray(query[name]); err != nil {
				return ParsedJqlQueries{}, fmt.Errorf("query %d %s: %w", i+1, name, err)
			}
		}
		if raw := query["structure"]; len(raw) > 0 && !isJQLNull(raw) {
			if _, err := DecodeNativeObject(raw, nil); err != nil {
				return ParsedJqlQueries{}, fmt.Errorf("query %d structure: %w", i+1, err)
			}
		}
	}
	var parsed ParsedJqlQueries
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ParsedJqlQueries{}, err
	}
	for _, query := range parsed.Queries.Value {
		if !query.Query.Set || query.Query.Null {
			return ParsedJqlQueries{}, errors.New("parsed query lacks its query text")
		}
	}
	return parsed, nil
}

// ParseJQLReferenceResponse retains optional/null metadata and extensions.
// All lists are optional in the pin, so an empty object is valid. Flags remain
// strings, including future server values; no boolean coercion occurs.
func ParseJQLReferenceResponse(data []byte) (JQLReferenceData, error) {
	members, err := DecodeNativeObject(data, jQLReferenceDataKeys)
	if err != nil {
		return JQLReferenceData{}, err
	}
	if err := jqlStringArray(members["jqlReservedWords"]); err != nil {
		return JQLReferenceData{}, fmt.Errorf("jqlReservedWords: %w", err)
	}
	for _, list := range []struct {
		name         string
		keys         []string
		stringArrays []string
	}{
		{"visibleFieldNames", fieldReferenceDataKeys, []string{"operators", "types"}},
		{"visibleFunctionNames", functionReferenceDataKeys, []string{"types"}},
	} {
		objects, err := jqlObjectArray(members[list.name], list.keys)
		if err != nil {
			return JQLReferenceData{}, fmt.Errorf("%s: %w", list.name, err)
		}
		for _, object := range objects {
			for _, name := range list.stringArrays {
				if err := jqlStringArray(object[name]); err != nil {
					return JQLReferenceData{}, fmt.Errorf("%s %s: %w", list.name, name, err)
				}
			}
		}
	}
	var reference JQLReferenceData
	if err := json.Unmarshal(data, &reference); err != nil {
		return JQLReferenceData{}, err
	}
	return reference, nil
}

// jqlObjectArray checks fixed objects the CLI models. Raw syntax-tree internals
// and extensions remain uninterpreted rather than claiming schema validation.
func jqlObjectArray(raw json.RawMessage, keys []string) ([]map[string]json.RawMessage, error) {
	if len(raw) == 0 || isJQLNull(raw) {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	objects := make([]map[string]json.RawMessage, len(items))
	for i, item := range items {
		object, err := DecodeNativeObject(item, keys)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i+1, err)
		}
		objects[i] = object
	}
	return objects, nil
}

// Optional containers retain null. Null elements cannot become empty strings:
// that would change a diagnostic, operator, type or reserved word.
func jqlStringArray(raw json.RawMessage) error {
	if len(raw) == 0 || isJQLNull(raw) {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	if xslices.AnyFunc(items, isJQLNull) {
		return errors.New("string array contains a null element")
	}
	return nil
}

func isJQLNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
