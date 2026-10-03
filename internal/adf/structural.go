package adf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// StructuralEqual compares JSON without depending on object key order,
// whitespace, or string escape spelling. Array order, absent versus null,
// and number spelling remain significant. Numbers never pass through float64:
// opaque identifiers beyond 2^53 must not become equal after rounding.
// Duplicate object keys and trailing values are rejected as ambiguous input.
// This is structural equality, not byte equality or rendered-text equivalence.
func StructuralEqual(left, right []byte) (bool, error) {
	a, err := canonicalStructure(left)
	if err != nil {
		return false, fmt.Errorf("adf: compare left structure: %w", err)
	}
	b, err := canonicalStructure(right)
	if err != nil {
		return false, fmt.Errorf("adf: compare right structure: %w", err)
	}
	return bytes.Equal(a, b), nil
}

func canonicalStructure(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	value, err := structuralValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON value")
		}
		return nil, fmt.Errorf("trailing JSON data: %w", err)
	}
	result, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode JSON structure: %w", err)
	}
	return result, nil
}

// Token traversal is needed because Decode into a map silently collapses
// duplicate keys. The standard codec still owns parsing and number syntax.
func structuralValue(dec *json.Decoder) (any, error) {
	value, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("read JSON value: %w", err)
	}
	delim, ok := value.(json.Delim)
	if !ok {
		return value, nil
	}
	switch delim {
	case '{':
		object := make(map[string]any)
		for dec.More() {
			keyValue, err := dec.Token()
			if err != nil {
				return nil, fmt.Errorf("read JSON key: %w", err)
			}
			key, ok := keyValue.(string)
			if !ok {
				return nil, fmt.Errorf("JSON object key is not a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate JSON object key %q", key)
			}
			child, err := structuralValue(dec)
			if err != nil {
				return nil, err
			}
			object[key] = child
		}
		if _, err := dec.Token(); err != nil {
			return nil, fmt.Errorf("close JSON object: %w", err)
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for dec.More() {
			child, err := structuralValue(dec)
			if err != nil {
				return nil, err
			}
			array = append(array, child)
		}
		if _, err := dec.Token(); err != nil {
			return nil, fmt.Errorf("close JSON array: %w", err)
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}
