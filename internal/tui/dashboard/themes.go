package dashboard

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/palette"
	"github.com/gechr/primer/task"
	xslices "github.com/gechr/x/slices"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
)

const themeTaskScope task.Scope = "theme-library"

type themeSelection struct {
	name, path, base string
	definition       *config.ThemeFile
	source           *config.SavedTheme
	err              error
}

type themesLoaded struct {
	picker  *pickerDialog
	entries []*config.SavedTheme
}

type themeImported struct {
	picker     *pickerDialog
	definition *config.ThemeFile
}

type themeEditorSaved struct {
	editor    *themeEditorDialog
	saved     *config.SavedTheme
	published bool
}

type themeRenamed struct {
	picker    *pickerDialog
	saved     *config.SavedTheme
	published bool
}

type themeExported struct {
	picker    *pickerDialog
	published bool
}

func (m *Model) loadThemePicker(p *pickerDialog) tea.Cmd {
	if m.options.LoadThemes == nil {
		return nil
	}
	p.loading = true
	load, active := m.options.LoadThemes, m.settings.values["theme_path"]
	return m.tasks.Start(task.Spec{Scope: themeTaskScope, Run: func() (any, error) {
		entries, err := load(active)
		return themesLoaded{picker: p, entries: entries}, err
	}})
}

func (m *Model) fillThemePicker(p *pickerDialog, entries []*config.SavedTheme, selected string) {
	query := p.menu.Query()
	p.choices = nil
	used := map[string]bool{}
	p.themes = map[string]themeSelection{}
	for _, name := range config.ThemeChoices() {
		p.choices = append(p.choices, choice{label: name, value: name})
		p.themes[name] = themeSelection{name: name, base: name}
		used[name] = true
	}
	for _, entry := range entries {
		value := "custom:" + entry.Path
		label := entry.Name
		if used[label] {
			label += " · custom"
		}
		for used[label] {
			label += " ·"
		}
		used[label] = true
		p.choices = append(p.choices, choice{label: label, value: value})
		base := "dark"
		if entry.Definition != nil {
			base = entry.Definition.Base
		}
		p.themes[value] = themeSelection{
			name: entry.Name, path: entry.Path, base: base,
			definition: entry.Definition, source: entry, err: entry.Err,
		}
	}
	p.menu = palette.New("Type to filter", themePickerEntries(p), commandStyles())
	for _, r := range query {
		p.menu.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	selectedLabel := ""
	for _, choice := range p.choices {
		if choice.value == selected {
			selectedLabel = choice.label
			break
		}
	}
	for i, entry := range visiblePaletteEntries(p.menu, len(p.choices)) {
		if entry.Name == selectedLabel {
			for range i {
				p.menu.Update(key.Replay("down"))
			}
			break
		}
	}
}

func themePickerEntries(p *pickerDialog) []palette.Entry {
	return xslices.Map(p.choices, func(c choice) palette.Entry {
		return palette.Entry{Name: c.label}
	})
}

func previewThemeSelection(selection themeSelection) (func(), error) {
	if selection.err != nil {
		return nil, selection.err
	}
	if selection.definition != nil {
		return cli.PreviewThemeDefinition(selection.definition)
	}
	return cli.PreviewTheme(selection.base)
}

func (m *Model) applyThemeSelection(selection themeSelection) {
	restore, err := previewThemeSelection(selection)
	if err != nil {
		m.settings.notice = m.errorText(err)
		return
	}
	if m.settings.restoreTheme == nil {
		m.settings.restoreTheme = restore
	}
	m.themeName = selection.base
	m.settings.values["theme_path"] = selection.path
	m.settings.themeLabel = selection.name
	m.settings.themeDirty = true
	m.refreshThemeStyles()
}

func (m *Model) themePickerAction(p *pickerDialog, action string) tea.Cmd {
	if p.loading || m.localSaveBusy {
		return nil
	}
	if action == "ctrl+n" || action == "ctrl+i" {
		if m.options.WriteTheme == nil {
			p.notice = "Theme management is unavailable in this session."
			return nil
		}
		if action == "ctrl+n" {
			m.openThemeInput(themeInputRequest{
				title: "New theme name", kind: "create-name", parent: p,
				definition: cli.CurrentThemeDefinition(),
			})
		} else {
			m.openThemeInput(themeInputRequest{
				title: "Import theme", kind: "import-path", parent: p,
			})
		}
		return nil
	}
	selected, ok := p.themes[p.value()]
	if !ok {
		return nil
	}
	if selected.err != nil {
		p.notice = m.errorText(selected.err)
		return nil
	}
	if m.options.WriteTheme == nil {
		p.notice = "Theme management is unavailable in this session."
		return nil
	}
	def := config.CloneThemeFile(selected.definition)
	if def == nil {
		if config.IsAutomaticTheme(selected.base) {
			def = cli.CurrentThemeDefinition()
		} else {
			def = &config.ThemeFile{Base: selected.base, Colors: map[string]string{}, Syntax: map[string]string{}}
		}
	}
	switch action {
	case "ctrl+d":
		m.openThemeInput(themeInputRequest{
			title: "Duplicate theme", kind: "create-name", parent: p, definition: def,
		})
	case "ctrl+e":
		if selected.source != nil && selected.source.Managed() {
			m.openThemeEditor(
				p,
				selected.name,
				def,
				selected.source,
			)
		} else {
			m.openThemeInput(themeInputRequest{
				title: "Name your copy", kind: "create-name", parent: p, definition: def,
			})
		}
	case "ctrl+r":
		if selected.source == nil || !selected.source.Managed() {
			p.notice = "Duplicate this theme before renaming it."
			return nil
		}
		m.openThemeInput(themeInputRequest{
			title: "Rename theme", kind: "rename-name", value: selected.name,
			parent: p, definition: def, source: selected.source,
		})
	case "ctrl+x":
		m.openThemeInput(themeInputRequest{
			title: "Export theme", kind: "export-path", parent: p, definition: def,
		})
	}
	return nil
}

func (m *Model) acceptThemeInput(d *themeInputDialog) tea.Cmd {
	value := d.value()
	switch d.kind {
	case "create-name", "import-name":
		m.openThemeEditor(
			d.parent,
			value,
			d.definition,
			nil,
		)
	case "rename-name":
		return m.renameTheme(d)
	case "import-path":
		if m.options.ReadTheme == nil {
			return nil
		}
		d.parent.loading = true
		read := m.options.ReadTheme
		return m.tasks.Start(task.Spec{Scope: themeTaskScope, Run: func() (any, error) {
			def, err := read(value)
			if def != nil && def.Name == "" {
				def.Name = strings.TrimSuffix(filepath.Base(value), filepath.Ext(value))
			}
			return themeImported{picker: d.parent, definition: def}, err
		}})
	case "export-path":
		if m.options.ExportTheme == nil {
			return nil
		}
		m.localSaveBusy = true
		export, def := m.options.ExportTheme, config.CloneThemeFile(d.definition)
		d.parent.notice = "Exporting theme…"
		return m.tasks.Start(task.Spec{Scope: themeTaskScope, Run: func() (any, error) {
			published, err := export(value, def)
			return themeExported{picker: d.parent, published: published}, err
		}})
	}
	return nil
}

func (m *Model) saveThemeEditor(editor *themeEditorDialog) tea.Cmd {
	if m.options.WriteTheme == nil || m.localSaveBusy {
		return nil
	}
	m.localSaveBusy = true
	editor.saving = true
	editor.hasError = false
	editor.notice = "Saving theme…"
	write, definition := m.options.WriteTheme, config.CloneThemeFile(editor.definition)
	name, source := editor.name, editor.source
	return m.tasks.Start(task.Spec{Scope: themeTaskScope, Run: func() (any, error) {
		saved, published, err := write(name, definition, source)
		return themeEditorSaved{editor: editor, saved: saved, published: published}, err
	}})
}

func (m *Model) renameTheme(d *themeInputDialog) tea.Cmd {
	if m.options.WriteTheme == nil || m.localSaveBusy {
		return nil
	}
	m.localSaveBusy = true
	d.parent.notice = "Renaming theme…"
	write, definition := m.options.WriteTheme, config.CloneThemeFile(d.definition)
	name, source := d.value(), d.source
	return m.tasks.Start(task.Spec{Scope: themeTaskScope, Run: func() (any, error) {
		saved, published, err := write(name, definition, source)
		return themeRenamed{picker: d.parent, saved: saved, published: published}, err
	}})
}

func (m *Model) acceptThemeTask(msg task.FinishedMsg) tea.Cmd {
	switch result := msg.Result.(type) {
	case themesLoaded:
		m.completeThemeLoad(result, msg.Err)
	case themeImported:
		m.completeThemeImport(result, msg.Err)
	case themeEditorSaved:
		return m.completeThemeEditorSave(result, msg.Err)
	case themeRenamed:
		return m.completeThemeRename(result, msg.Err)
	case themeExported:
		m.completeThemeExport(result, msg.Err)
	}
	return nil
}

func (m *Model) themeTaskError(err error, published bool) string {
	text := m.errorText(err)
	if published {
		text = "Theme file was published, but completion failed. Inspect the library before retrying. " + text
	}
	return text
}

func (m *Model) completeThemeLoad(result themesLoaded, err error) {
	p := result.picker
	p.loading = false
	if m.settings == nil {
		return
	}
	if err != nil {
		p.notice = m.themeTaskError(err, false)
		return
	}
	if m.dialogs.Top() != p {
		return
	}
	selected := p.value()
	for _, entry := range result.entries {
		if entry.Active && p.initialThemes && p.previewedTheme == "" {
			m.settings.themeLabel = entry.Name
			selected = "custom:" + entry.Path
			break
		}
	}
	p.initialThemes = false
	if p.selectTheme != "" {
		selected = p.selectTheme
		p.menu = palette.New("Type to filter", themePickerEntries(p), commandStyles())
		p.selectTheme = ""
	}
	m.fillThemePicker(p, result.entries, selected)
	p.previewedTheme = ""
	m.previewHighlightedTheme(p)
}

func (m *Model) completeThemeImport(result themeImported, err error) {
	p := result.picker
	p.loading = false
	if m.settings == nil {
		return
	}
	if err != nil {
		p.notice = m.themeTaskError(err, false)
		return
	}
	if m.dialogs.Top() != p {
		return
	}
	m.openThemeInput(themeInputRequest{
		title: "Imported theme name", kind: "import-name", value: result.definition.Name,
		parent: p, definition: result.definition,
	})
}

func (m *Model) completeThemeEditorSave(result themeEditorSaved, err error) tea.Cmd {
	editor := result.editor
	p := editor.parent
	p.loading = false
	m.localSaveBusy = false
	editor.saving = false
	if m.settings == nil {
		return nil
	}
	if err != nil {
		p.notice = m.themeTaskError(err, result.published)
		editor.notice = p.notice
		editor.hasError = true
		return nil
	}
	if result.saved == nil {
		p.notice = "Theme save returned no theme."
		return nil
	}
	if m.dialogs.Top() != editor {
		return nil
	}
	m.dialogs.Pop()
	if editor.restore != nil {
		editor.restore()
	}
	if p.restorePreview != nil {
		p.restorePreview()
		p.restorePreview = nil
	}
	m.applyThemeSelection(themeSelection{
		name: result.saved.Name, path: result.saved.Path, base: result.saved.Definition.Base,
		definition: result.saved.Definition, source: result.saved,
	})
	m.settings.notice = "Theme saved. Save Settings to retain this selection."
	return m.refreshSavedThemePicker(p, result.saved)
}

func (m *Model) completeThemeRename(result themeRenamed, err error) tea.Cmd {
	p := result.picker
	p.loading = false
	m.localSaveBusy = false
	if m.settings == nil {
		return nil
	}
	if err != nil {
		p.notice = m.themeTaskError(err, result.published)
		return nil
	}
	if result.saved == nil {
		p.notice = "Theme save returned no theme."
		return nil
	}
	return m.refreshSavedThemePicker(p, result.saved)
}

func (m *Model) refreshSavedThemePicker(p *pickerDialog, saved *config.SavedTheme) tea.Cmd {
	p.selectTheme = "custom:" + saved.Path
	if m.settings.values["theme_path"] == saved.Path {
		m.settings.themeLabel = saved.Name
	}
	if m.options.SettingValues["theme_path"] == saved.Path {
		m.options.ThemeLabel = saved.Name
	}
	p.notice = "Theme saved."
	return m.loadThemePicker(p)
}

func (m *Model) completeThemeExport(result themeExported, err error) {
	p := result.picker
	p.loading = false
	m.localSaveBusy = false
	if m.settings == nil {
		return
	}
	if err != nil {
		p.notice = m.themeTaskError(err, result.published)
		return
	}
	p.notice = "Theme exported."
}

func themeRoleName(role string) string {
	switch role {
	case "blue":
		return "Accent"
	case "bold":
		return "Headings"
	case "dim":
		return "Muted text"
	case "foreground":
		return "Text"
	case "bold_green":
		return "Positive heading"
	default:
		text := strings.ReplaceAll(role, "_", " ")
		if text == "" {
			return text
		}
		return strings.ToUpper(text[:1]) + text[1:]
	}
}

func themeDefinitionKey(section, role string) string { return fmt.Sprintf("%s.%s", section, role) }
