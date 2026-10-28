package config

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/matcra587/jira-cli/internal/core"
)

// Workspace stores related view references atomically, so removing a view cannot
// publish dangling tabs or startup choices. Empty configuration uses built-ins.
type Workspace struct {
	Sections    []WorkspaceSection `koanf:"sections" json:"sections,omitempty"`
	Lenses      []WorkspaceSection `koanf:"lenses" json:"lenses,omitempty"`
	Tabs        []string           `koanf:"tabs" json:"tabs,omitempty"`
	LandingView string             `koanf:"landing_view" json:"landing_view,omitempty"`
	LandingLens string             `koanf:"landing_lens" json:"landing_lens,omitempty"`
}

// WorkspaceSection names an authored query or narrowing lens by a stable ID.
type WorkspaceSection struct {
	ID    string `koanf:"id" json:"id"`
	Title string `koanf:"title" json:"title"`
	JQL   string `koanf:"jql" json:"jql"`
}

// BuiltinWorkspaceViews returns stable view IDs in their default order.
func BuiltinWorkspaceViews() []string { return []string{"my-work", "project", "search", "hierarchy"} }

// TabIDs returns the explicit order, or built-ins followed by custom views.
func (w Workspace) TabIDs() []string {
	if w.Tabs != nil {
		return slices.Clone(w.Tabs)
	}
	ids := BuiltinWorkspaceViews()
	for _, section := range w.Sections {
		ids = append(ids, section.ID)
	}
	return ids
}

// Clone isolates settings drafts and parked runtime preferences.
func (w Workspace) Clone() Workspace {
	w.Sections, w.Lenses, w.Tabs = slices.Clone(w.Sections), slices.Clone(w.Lenses), slices.Clone(w.Tabs)
	return w
}

// JSON provides the public config get/set representation. All fields are strings
// or string collections, so encoding has no unsupported values or cycles.
func (w Workspace) JSON() string {
	data, _ := json.Marshal(w) //nolint:errcheck // closed string-only value model
	return string(data)
}

// ParseWorkspace admits the same atomic object used by Settings and config set.
func ParseWorkspace(value string) (Workspace, error) {
	var workspace Workspace
	if !strings.HasPrefix(strings.TrimSpace(value), "{") {
		return workspace, fmt.Errorf("tui_workspace must be a JSON object")
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&workspace); err != nil {
		return workspace, fmt.Errorf("decode tui_workspace: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return workspace, fmt.Errorf("tui_workspace must contain exactly one object")
	}
	return workspace, workspace.Validate()
}

// Validate checks references and local structure without credentials or Jira I/O.
func (w Workspace) Validate() error {
	if len(w.Sections) > 32 || len(w.Lenses) > 32 || len(w.Tabs) > 36 {
		return fmt.Errorf("workspace supports up to 32 custom views, 32 lenses and 36 tabs")
	}
	views := BuiltinWorkspaceViews()
	if err := validateWorkspaceSections(w.Sections, views); err != nil {
		return fmt.Errorf("workspace views: %w", err)
	}
	if err := validateWorkspaceSections(w.Lenses, nil); err != nil {
		return fmt.Errorf("workspace lenses: %w", err)
	}
	for _, section := range w.Sections {
		views = append(views, section.ID)
	}
	for _, lens := range w.Lenses {
		if _, err := core.NarrowJQL("id IS NOT EMPTY", lens.JQL); err != nil {
			return fmt.Errorf("lens %q: %w", lens.ID, err)
		}
	}
	if w.Tabs != nil && len(w.Tabs) == 0 {
		return fmt.Errorf("workspace must retain at least one tab")
	}
	seen := make(map[string]bool)
	for _, id := range w.TabIDs() {
		if !slices.Contains(views, id) || seen[id] {
			return fmt.Errorf("workspace tabs must reference distinct existing view IDs")
		}
		seen[id] = true
	}
	if w.LandingView != "" && !seen[w.LandingView] {
		return fmt.Errorf("workspace landing_view must name a visible tab")
	}
	if w.LandingLens != "" && !slices.ContainsFunc(w.Lenses, func(lens WorkspaceSection) bool { return lens.ID == w.LandingLens }) {
		return fmt.Errorf("workspace landing_lens must name an existing lens")
	}
	return nil
}

func validateWorkspaceSections(sections []WorkspaceSection, reserved []string) error {
	seen := make(map[string]bool)
	titles := make(map[string]bool)
	for _, id := range reserved {
		seen[id] = true
	}
	for _, section := range sections {
		if err := ValidateProfileName(section.ID); err != nil || len(section.ID) > 64 || strings.ToLower(section.ID) != section.ID {
			return fmt.Errorf("IDs must be unique lowercase ASCII names of at most 64 characters")
		}
		if seen[section.ID] {
			return fmt.Errorf("duplicate or reserved view ID %q", section.ID)
		}
		title := strings.TrimSpace(section.Title)
		if title == "" || !utf8.ValidString(title) || utf8.RuneCountInString(title) > 64 || strings.ContainsFunc(title, unicode.IsControl) || titles[strings.ToLower(title)] {
			return fmt.Errorf("titles must be distinct nonempty text of at most 64 characters without controls")
		}
		if strings.TrimSpace(section.JQL) == "" || len(section.JQL) > 8192 || !utf8.ValidString(section.JQL) || strings.ContainsFunc(section.JQL, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) {
			return fmt.Errorf("JQL must be nonempty UTF-8 text of at most 8192 bytes without terminal controls")
		}
		seen[section.ID], titles[strings.ToLower(title)] = true, true
	}
	return nil
}

func workspaceSettingValue(value string) (map[string]any, error) {
	workspace, err := ParseWorkspace(value)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]any)
	if err := json.Unmarshal([]byte(workspace.JSON()), &fields); err != nil {
		return nil, err
	}
	return fields, nil
}
