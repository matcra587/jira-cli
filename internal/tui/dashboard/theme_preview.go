package dashboard

import tea "charm.land/bubbletea/v2"

func (m *Model) pickTheme() tea.Cmd {
	m.pick("Choose a theme", "theme", "", nil)
	p := m.dialogs.Top().(*pickerDialog)
	p.manageThemes = m.options.WriteTheme != nil
	p.initialThemes = true
	m.fillThemePicker(p, nil, m.themeName)
	return m.loadThemePicker(p)
}

// Preset resets leave the cached custom label intact; accepting a picker
// selection owns replacing that label alongside its selected file path.
func (m *Model) setPreviewTheme(name string) {
	m.applyThemeSelection(themeSelection{
		name: m.settings.themeLabel,
		base: name,
	})
}

func (m *Model) refreshThemeStyles() {
	m.applyStyles()
	m.dialogs.SetFrame(commandFrame())
	m.settings.tabs.ActiveStyle, m.settings.tabs.InactiveStyle = m.tabs.ActiveStyle, m.tabs.InactiveStyle
	m.settings.rows.Styles.Dim = m.rows.Styles.Dim
	m.previewSettings()
}

func (m *Model) previewHighlightedTheme(p *pickerDialog) {
	if _, ok := p.menu.Selected(); !ok {
		return
	}
	name := p.value()
	if name == p.previewedTheme {
		return
	}
	selection, ok := p.themes[name]
	if !ok {
		return
	}
	restore, err := previewThemeSelection(selection)
	if err != nil {
		p.notice = m.errorText(err)
		return
	}
	p.notice = ""
	if p.restorePreview == nil {
		p.restorePreview = restore
	}
	p.previewedTheme = name
	m.refreshThemeStyles()
	p.restyle()
}
