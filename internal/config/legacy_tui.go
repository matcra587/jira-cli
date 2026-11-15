package config

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// migrateLegacyTUI projects only preferences with current runtime consumers.
// Unsupported declarations block publication; their values stay in recovery.
func migrateLegacyTUI(old legacyMigrationTUI, declared, next map[string]any) []string {
	remaining := maps.Clone(declared)
	// The old preview's absent/zero ratio was 50%, not today's 40%.
	size := old.PreviewSize
	if size == 0 {
		size = 50
	}
	if _, err := parseTUIPreviewSize(strconv.Itoa(size)); err == nil {
		next["tui_preview_size"] = size
		delete(remaining, "preview_size")
	}
	if _, present := declared["preview"]; present {
		position := strings.ToLower(strings.TrimSpace(old.Preview))
		if position == "hidden" {
			next["tui_preview"] = false
			delete(remaining, "preview")
		} else {
			if position == "" {
				position = "auto"
			}
			var dock PreviewDock
			if dock.UnmarshalText([]byte(position)) == nil {
				next["tui_preview"], next["tui_preview_dock"] = true, string(dock)
				delete(remaining, "preview")
			}
		}
	}
	if _, present := declared["refresh_interval"]; present {
		seconds := max(old.RefreshInterval, 0) // Old non-positive intervals disabled refresh.
		if _, err := parseTUIRefreshInterval(strconv.Itoa(seconds)); err == nil {
			next["tui_refresh_interval"] = seconds
			next["tui_refresh_on_focus"] = seconds > 0
			delete(remaining, "refresh_interval")
		}
	}
	workspace, mapped := legacyTUIWorkspace(old, declared)
	if len(mapped) > 0 && workspace.Validate() == nil {
		fields, err := workspaceSettingValue(workspace.JSON())
		if err == nil {
			next["tui_workspace"] = fields
			for _, key := range mapped {
				delete(remaining, key)
			}
		}
	}
	unmapped := make([]string, 0, len(remaining))
	for key := range remaining {
		unmapped = append(unmapped, "tui."+key)
	}
	return unmapped
}

func legacyTUIWorkspace(old legacyMigrationTUI, declared map[string]any) (Workspace, []string) {
	var workspace Workspace
	var mapped []string
	for _, item := range []struct {
		name string
		from []legacyMigrationTUISection
		to   *[]WorkspaceSection
	}{{"sections", old.Sections, &workspace.Sections}, {"lenses", old.Lenses, &workspace.Lenses}} {
		if _, present := declared[item.name]; !present {
			continue
		}
		for index, section := range item.from {
			*item.to = append(*item.to, WorkspaceSection{
				ID: fmt.Sprintf("legacy-%s-%d", item.name, index+1), Title: section.Title, JQL: section.JQL,
			})
		}
		mapped = append(mapped, item.name)
	}
	// Legacy built-ins other than Search have different scopes or no view
	// equivalent. Never turn Issues into My work or Epics into Hierarchy.
	viewID := func(name string) string {
		switch name {
		case "search":
			return "search"
		case "issues", "epics", "activity", "settings":
			return ""
		}
		return legacySectionID(workspace.Sections, name)
	}
	if _, present := declared["tabs"]; present && len(old.Tabs) > 0 {
		tabs := []string{}
		for _, name := range old.Tabs {
			id := viewID(name)
			if id == "" {
				tabs = nil
				break
			}
			tabs = append(tabs, id)
		}
		if tabs != nil {
			// Legacy custom sections were visible even when omitted from tabs.
			for _, section := range workspace.Sections {
				if !slices.Contains(tabs, section.ID) {
					tabs = append(tabs, section.ID)
				}
			}
			workspace.Tabs = tabs
			mapped = append(mapped, "tabs")
		}
	}
	if _, present := declared["default_tab"]; present {
		id := viewID(old.DefaultTab)
		if id != "" && slices.Contains(workspace.TabIDs(), id) {
			workspace.LandingView = id
			mapped = append(mapped, "default_tab")
		}
	}
	if len(workspace.Lenses) > 0 {
		// The legacy custom-lens set always started with a lens active;
		// an absent or unmatched default selected the first authored lens.
		workspace.LandingLens = legacySectionID(workspace.Lenses, old.DefaultLens)
		if workspace.LandingLens == "" {
			workspace.LandingLens = workspace.Lenses[0].ID
		}
		mapped = append(mapped, "default_lens")
	}
	if _, present := declared["keys"]; present {
		if keys, ok := legacyWorkspaceKeys(old.Keys); ok {
			workspace.Keys = keys
			mapped = append(mapped, "keys")
		}
	}
	return workspace, mapped
}

func legacySectionID(sections []WorkspaceSection, title string) string {
	for _, section := range sections {
		if strings.EqualFold(section.Title, title) {
			return section.ID
		}
	}
	return ""
}

func legacyWorkspaceKeys(old map[string][]string) (map[string][]string, bool) {
	names := map[string]string{
		"up": "up", "down": "down", "top": "top", "bottom": "bottom",
		"page_up": "page-up", "page_down": "page-down", "next_section": "view-next", "prev_section": "view-prev",
		"open": "details", "back": "back", "refresh": "refresh", "toggle_pause": "pause-refresh",
		"filter": "filter-text", "facet": "filter-facet", "jumplist": "switch-issue", "presets": "saved-views",
		"transition": "transition", "comment": "comment", "assign": "assign", "labels": "labels",
		"edit": "edit", "create": "create", "command_palette": "commands", "zoom": "zoom",
		"open_browse": "browse", "copy_key": "copy-key", "copy_url": "copy-url",
		"select": "mark", "select_all": "mark-all", "select_invert": "invert", "select_range": "mark-range",
		"op_log": "history", "help": "help", "quit": "quit",
	}
	keys := make(map[string][]string, len(old))
	for name, shortcuts := range old {
		id, ok := names[name]
		if !ok {
			return nil, false
		}
		keys[id] = slices.Clone(shortcuts)
	}
	return keys, ValidateWorkspaceKeys(keys) == nil
}
