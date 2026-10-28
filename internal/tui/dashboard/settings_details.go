package dashboard

import (
	"strconv"
	"strings"

	"github.com/gechr/primer/layout"
	"github.com/gechr/x/human"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
)

func (m *Model) settingsChangeCount() int {
	p := m.settings
	changed := make(map[string]bool)
	for name, value := range p.values {
		if value != p.saved[name] {
			changed[name] = true
		}
	}
	for name, value := range m.preferences {
		if value != p.baseline[name] {
			changed[name] = true
		}
	}
	if p.themeDirty {
		changed["theme"] = true
	}
	return len(changed)
}

func (m *Model) settingsStatus() string {
	if count := m.settingsChangeCount(); count > 0 {
		return human.Pluralize(count, "unsaved change", "unsaved changes")
	}
	return "No unsaved changes."
}

func (m *Model) settingValue(name string) string {
	p := m.settings
	if name == "api_token" || name == "aliases" {
		return "Managed separately"
	}
	if strings.HasPrefix(name, "tui_") {
		return strconv.FormatBool(m.enabled(name))
	}
	if name == "theme" {
		if p.values["theme_path"] != "" {
			label := p.themeLabel
			if label == "" {
				label = "Custom theme"
			}
			return label + " · " + p.values["theme_path"]
		}
		if m.themeName == "" {
			return "auto"
		}
		return m.themeName
	}
	if value := p.values[name]; value != "" {
		return config.Redacted(name, value)
	}
	return "Not set"
}

func (m *Model) settingVisibility(name string) string {
	if !m.enabled(name) {
		return ""
	}
	if m.detailFocused && (name == "tui_status" || name == "tui_assignee" || name == "tui_updated" || name == "tui_compact" || name == "tui_preview") {
		return "Return to the list to see this."
	}
	switch name {
	case "tui_assignee":
		if m.tabs.ActiveItem() == "My work" {
			return "Hidden in My work (all yours)."
		}
		if m.listWidth() < 92 {
			return "Hidden: list needs 92 columns.\nWiden the terminal or turn off the side-by-side preview."
		}
	case "tui_updated":
		if m.listWidth() < 110 {
			return "Hidden: list needs 110 columns.\nWiden the terminal or turn off the side-by-side preview."
		}
	case "tui_status":
		if m.listWidth() < 64 {
			return "Hidden: list needs 64 columns.\nWiden the terminal or turn off the side-by-side preview."
		}
	case "tui_preview":
		if m.pageWidth() < 110 || m.height < 24 {
			return "Hidden: preview needs 114 × 24.\nEnter still opens issue details."
		}
	case "tui_labels", "tui_priority", "tui_issue_type":
		if !m.split() && !m.detailFocused {
			return "Shown in details; Enter opens them."
		}
	}
	return ""
}

func (m *Model) settingsDetails() []string {
	p := m.settings
	i, ok := p.rows.Selected()
	if !ok || i >= len(p.visible) {
		return nil
	}
	s := p.visible[i]
	label := "Value: "
	if p.values[s.key] != p.saved[s.key] || m.preferences[s.key] != p.baseline[s.key] || s.key == "theme" && p.themeDirty {
		label = "Draft: "
	}
	lines := layout.WrapLines(label+cli.Text(m.settingValue(s.key)), m.pageWidth())
	if visibility := m.settingVisibility(s.key); visibility != "" {
		lines = append(lines, layout.WrapLines(visibility, m.pageWidth())...)
	}
	source := p.sources[s.key]
	if source == "" {
		source = "default"
	}
	switch s.key {
	case "api_token":
		source = "auth"
	case "aliases":
		source = "alias"
	}
	switch source {
	case "env":
		source = "environment · locked"
	case "flag":
		source = "invocation flag · locked"
	}
	lines = append(lines, layout.WrapLines("Source: "+source+" · "+s.group, m.pageWidth())...)
	lines = append(lines, layout.WrapLines(s.description, m.pageWidth())...)
	return lines
}
