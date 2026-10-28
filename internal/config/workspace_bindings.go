package config

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"
)

// WorkspaceAction describes one rebindable main-view action. Editors and menus
// retain their local input conventions; Escape and Ctrl+C remain reserved.
type WorkspaceAction struct {
	ID, Title string
	Keys      []string
}

// WorkspaceActions returns fresh definitions shared by admission, binding owners,
// Settings and help. The first default key identifies the existing action route.
func WorkspaceActions() []WorkspaceAction {
	return []WorkspaceAction{
		{ID: "create", Title: "Create issue", Keys: []string{"n"}},
		{ID: "edit", Title: "Edit issue fields", Keys: []string{"e"}},
		{ID: "labels", Title: "Edit labels", Keys: []string{"l"}},
		{ID: "browse", Title: "Open issue in browser", Keys: []string{"O"}},
		{ID: "copy-key", Title: "Copy issue key", Keys: []string{"y"}},
		{ID: "copy-url", Title: "Copy issue URL", Keys: []string{"Y"}},
		{ID: "filter-text", Title: "Filter loaded text", Keys: []string{"ctrl+f"}},
		{ID: "filter-facet", Title: "Filter by field", Keys: []string{"f"}},
		{ID: "bulk", Title: "Marked issue actions", Keys: []string{"B"}},
		{ID: "history", Title: "Operation history", Keys: []string{"L"}},
		{ID: "mark", Title: "Mark selected issue", Keys: []string{"space"}},
		{ID: "mark-all", Title: "Mark all visible issues", Keys: []string{"ctrl+a"}},
		{ID: "invert", Title: "Invert visible marks", Keys: []string{"X"}},
		{ID: "mark-range", Title: "Mark issue range", Keys: []string{"x"}},
		{ID: "section-prev", Title: "Previous detail section", Keys: []string{"["}},
		{ID: "section-next", Title: "Next detail section", Keys: []string{"]"}},
		{ID: "links", Title: "Open linked issue", Keys: []string{"o"}},
		{ID: "switch-issue", Title: "Switch issue", Keys: []string{"ctrl+o"}},
		{ID: "saved-views", Title: "Saved searches", Keys: []string{"v"}},
		{ID: "lenses", Title: "Choose view lens", Keys: []string{"alt+l"}},
		{ID: "view-picker", Title: "Switch view", Keys: []string{"alt+v"}},
		{ID: "save-view", Title: "Save current query", Keys: []string{"ctrl+s"}},
		{ID: "transition", Title: "Transition issue", Keys: []string{"t"}},
		{ID: "assign", Title: "Assign issue", Keys: []string{"a"}},
		{ID: "comment", Title: "Comment on issue", Keys: []string{"c"}},
		{ID: "zoom", Title: "Zoom list or restore preview", Keys: []string{"z"}},
		{ID: "focus", Title: "Focus list or preview", Keys: []string{"ctrl+w"}},
		{ID: "quit", Title: "Quit dashboard", Keys: []string{"q"}},
		{ID: "settings", Title: "Settings", Keys: []string{","}},
		{ID: "help", Title: "Keyboard shortcuts", Keys: []string{"?"}},
		{ID: "commands", Title: "Commands", Keys: []string{"ctrl+p", "ctrl+k"}},
		{ID: "hierarchy", Title: "Hierarchy roots", Keys: []string{"H"}},
		{ID: "hierarchy-around", Title: "Hierarchy around issue", Keys: []string{"ctrl+g"}},
		{ID: "my-work", Title: "My work", Keys: []string{"1"}},
		{ID: "project", Title: "Project or explicit search", Keys: []string{"2"}},
		{ID: "search", Title: "Edit JQL", Keys: []string{"/"}},
		{ID: "view-next", Title: "Next view", Keys: []string{"tab"}},
		{ID: "view-prev", Title: "Previous view", Keys: []string{"shift+tab"}},
		{ID: "pause-refresh", Title: "Pause or resume automatic refresh", Keys: []string{"R"}},
		{ID: "refresh", Title: "Refresh now", Keys: []string{"r"}},
		{ID: "more", Title: "Load more issues or comments", Keys: []string{"m"}},
		{ID: "details", Title: "Open issue details", Keys: []string{"enter"}},
		{ID: "back", Title: "Return to list or hierarchy history", Keys: []string{"h"}},
		{ID: "children", Title: "Issue children", Keys: []string{"right"}},
		{ID: "parent", Title: "Parent hierarchy level", Keys: []string{"left"}},
		{ID: "hierarchy-back", Title: "Previous hierarchy list", Keys: []string{"backspace"}},
		{ID: "down", Title: "Next issue or scroll down", Keys: []string{"j", "down"}},
		{ID: "up", Title: "Previous issue or scroll up", Keys: []string{"k", "up"}},
		{ID: "page-down", Title: "Page down", Keys: []string{"pgdown", "ctrl+d"}},
		{ID: "page-up", Title: "Page up", Keys: []string{"pgup", "ctrl+u"}},
		{ID: "top", Title: "First issue or scroll to top", Keys: []string{"g", "home"}},
		{ID: "bottom", Title: "Last issue or scroll to bottom", Keys: []string{"G", "end"}},
	}
}

// ValidateWorkspaceKeys admits a complete, conflict-free map before mutation.
// Primer Replay supplies the selected terminal key vocabulary and spelling.
func ValidateWorkspaceKeys(overrides map[string][]string) error {
	if len(overrides) == 0 {
		return nil
	}
	actions := WorkspaceActions()
	for id, shortcuts := range overrides {
		if !slices.ContainsFunc(actions, func(action WorkspaceAction) bool { return action.ID == id }) {
			return fmt.Errorf("unknown workspace action %q", id)
		}
		if len(shortcuts) == 0 || len(shortcuts) > 4 {
			return fmt.Errorf("action %q needs 1–4 shortcuts; remove its override to reset", id)
		}
		for _, shortcut := range shortcuts {
			msg := key.Replay(shortcut)
			shiftedText := msg.Mod.Contains(tea.ModShift) && !msg.Mod.Contains(tea.ModCtrl) && unicode.IsPrint(msg.Code) && msg.Code != tea.KeySpace
			if shiftedText {
				return fmt.Errorf("use the printed character for shifted text, such as A or ?, instead of %q", shortcut)
			}
			if shortcut == "" || strings.ContainsFunc(shortcut, unicode.IsControl) || msg.Code == tea.KeyExtended || msg.String() != shortcut {
				return fmt.Errorf("invalid shortcut %q; use canonical terminal key names", shortcut)
			}
			if shortcut == "esc" || shortcut == "ctrl+c" {
				return fmt.Errorf("%s is reserved for cancel or terminal exit", shortcut)
			}
		}
	}
	used := make(map[string]string)
	for _, action := range actions {
		shortcuts := action.Keys
		if override, ok := overrides[action.ID]; ok {
			shortcuts = override
		}
		for _, shortcut := range shortcuts {
			if previous, ok := used[shortcut]; ok {
				return fmt.Errorf("shortcut %q is already bound to %s", shortcut, previous)
			}
			used[shortcut] = action.Title
		}
	}
	return nil
}
