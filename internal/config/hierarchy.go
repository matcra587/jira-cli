package config

import (
	"errors"
	"strings"
	"unicode"
)

// DefaultTUIHierarchyQuery lists the native Epic level without relying on names.
const DefaultTUIHierarchyQuery = "hierarchyLevel = 1 ORDER BY updated DESC"

// HierarchyQuery preserves authored JQL while keeping it editable in Settings.
type HierarchyQuery string

// UnmarshalText validates local shape; Jira owns query syntax and permissions.
func (q *HierarchyQuery) UnmarshalText(text []byte) error {
	value := string(text)
	if strings.TrimSpace(value) == "" || strings.ContainsFunc(value, unicode.IsControl) {
		return errors.New("tui_hierarchy_query must be a nonempty single-line JQL query")
	}
	*q = HierarchyQuery(value)
	return nil
}
