package config

import "strings"

// Canonicalize schema keys before both weak decoding and declaration tracking.
// Mapstructure accepts case-insensitive keys and lifts a singleton table into a
// slice. Tracking a different shape could expose a silently partial candidate.
// User-defined alias names and TUI action names remain literal.
func canonicalLegacyFields(fields map[string]any) (map[string]any, error) {
	fields, err := canonicalLegacyTable(fields)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"profiles", "theme", "tui"} {
		value, present := fields[key]
		if !present {
			continue
		}
		if key == "profiles" {
			value, err = canonicalLegacyTables(value)
		} else if table, ok := value.(map[string]any); ok {
			value, err = canonicalLegacyTable(table)
		}
		if err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if tui, ok := fields["tui"].(map[string]any); ok {
		for _, key := range []string{"sections", "lenses"} {
			if value, present := tui[key]; present {
				tui[key], err = canonicalLegacyTables(value)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return fields, nil
}

func canonicalLegacyTable(table map[string]any) (map[string]any, error) {
	next := make(map[string]any, len(table))
	for key, value := range table {
		key = strings.ToLower(key)
		if _, duplicate := next[key]; duplicate {
			return nil, ErrLegacyConfig
		}
		next[key] = value
	}
	return next, nil
}

func canonicalLegacyTables(value any) (any, error) {
	if table, ok := value.(map[string]any); ok {
		value = []any{table}
		if len(table) == 0 {
			value = []any{} // WeaklyTypedInput treats an empty map as an empty slice.
		}
	}
	tables, ok := value.([]any)
	if !ok {
		return value, nil // Strict typed decoding reports incompatible values.
	}
	for index, value := range tables {
		if table, ok := value.(map[string]any); ok {
			next, err := canonicalLegacyTable(table)
			if err != nil {
				return nil, err
			}
			tables[index] = next
		}
	}
	return tables, nil
}
