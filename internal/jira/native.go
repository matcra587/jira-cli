package jira

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DecodeNativeObject reads exactly one object without converting its values.
// Repeated decoded keys and case variants of known members are ambiguous
// authoring input. Unknown members retain their original value bytes.
func DecodeNativeObject(data []byte, known []string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if start != json.Delim('{') {
		return nil, errors.New("native input must be a JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("native member name must be a string")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("native input repeats member %q", key)
		}
		for _, member := range known {
			if key != member && strings.EqualFold(key, member) {
				return nil, fmt.Errorf("use the exact native member spelling %q", member)
			}
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("native input has trailing data")
	}
	return fields, nil
}
