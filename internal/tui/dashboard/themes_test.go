package dashboard

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/palette"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
)

func themeLibraryModel(t *testing.T) (*Model, string) {
	t.Helper()
	restore, err := cli.PreviewTheme("dark")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	dir := filepath.Join(t.TempDir(), "themes")
	m := model(t)
	m.options.LoadThemes = func(active string) ([]*config.SavedTheme, error) {
		entries, err := config.LoadSavedThemes(t.Context(), dir)
		for _, entry := range entries {
			entry.Active = entry.Path == active
		}
		return entries, err
	}
	m.options.ReadTheme = func(path string) (*config.ThemeFile, error) { return config.LoadThemeFile(t.Context(), path) }
	m.options.WriteTheme = func(name string, def *config.ThemeFile, source *config.SavedTheme) (*config.SavedTheme, bool, error) {
		return config.SaveTheme(t.Context(), dir, name, def, source)
	}
	m.options.ExportTheme = func(path string, def *config.ThemeFile) (bool, error) {
		return config.ExportTheme(t.Context(), path, def)
	}
	m.openSettings()
	return m, dir
}

func themeKey(m *Model, name string)    { _, cmd := m.Update(key.Replay(name)); applyCommand(m, cmd) }
func themePaste(m *Model, value string) { m.Update(tea.PasteMsg{Content: value}) }

func TestNamedThemeEditorPreviewCancelSaveRenameAndExport(t *testing.T) {
	m, dir := themeLibraryModel(t)
	initial := cli.ResolvedTheme()
	applyCommand(m, m.pickTheme())
	themeKey(m, "ctrl+n")
	themePaste(m, "Ocean")
	themeKey(m, "enter")
	editor, ok := m.dialogs.Top().(*themeEditorDialog)
	if !ok {
		t.Fatal("new theme did not open editor")
	}
	themePaste(m, "Accent")
	themeKey(m, "enter")
	field, ok := m.dialogs.Top().(*themeInputDialog)
	if !ok {
		t.Fatal("role did not open color input")
	}
	themeKey(m, "ctrl+u")
	themePaste(m, "#123456")
	if m.dialogs.Top() != field || editor.definition.Colors["blue"] != "#123456" {
		t.Fatal("valid typed color did not preview before acceptance")
	}
	themeKey(m, "enter")
	if editor.definition.Colors["blue"] != "#123456" {
		t.Fatal("valid color was not kept")
	}
	themeKey(m, "enter")
	field = m.dialogs.Top().(*themeInputDialog)
	field.line.SetValue("not a color")
	themeKey(m, "enter")
	if m.dialogs.Top() != field || editor.definition.Colors["blue"] != "#123456" {
		t.Fatal("invalid color advanced or damaged preview")
	}
	themeKey(m, "esc")
	if editor.definition.Colors["blue"] != "#123456" {
		t.Fatal("canceling a field discarded prior accepted color")
	}
	_, save := m.Update(key.Replay("ctrl+s"))
	if save == nil || !m.localSaveBusy {
		t.Fatal("theme was not scheduled for explicit publication")
	}
	themeKey(m, "esc")
	if m.dialogs.Top() != editor {
		t.Fatal("in-flight publication was abandoned")
	}
	applyCommand(m, save)
	path := filepath.Join(dir, "Ocean.toml")
	def, err := config.LoadThemeFile(t.Context(), path)
	if err != nil || def.Colors["blue"] != "#123456" || m.settings.values["theme_path"] != path {
		t.Fatal("theme save/selection failed", def, err, m.settings.notice)
	}
	themeKey(m, "ctrl+r")
	field = m.dialogs.Top().(*themeInputDialog)
	field.line.SetValue("Sea")
	themeKey(m, "enter")
	def, err = config.LoadThemeFile(t.Context(), path)
	if err != nil || def.Name != "Sea" || m.settings.values["theme_path"] != path {
		t.Fatal("rename changed active path", err)
	}
	themeKey(m, "ctrl+x")
	field = m.dialogs.Top().(*themeInputDialog)
	export := filepath.Join(t.TempDir(), "sea.toml")
	field.line.SetValue(export)
	themeKey(m, "enter")
	if _, err := os.Stat(export); err != nil {
		t.Fatal("export missing", err)
	}
	themeKey(m, "esc")
	var stored map[string]string
	m.options.SaveSettings = func(values map[string]string) error { stored = maps.Clone(values); return nil }
	themeKey(m, "ctrl+s")
	if stored["theme_path"] != path || stored["theme"] != "dark" {
		t.Fatal("theme base/path were not one settings transaction", stored)
	}
	themeKey(m, "esc")
	if cli.ResolvedTheme() == initial {
		t.Fatal("saving theme selection restored the old palette")
	}
	m.openSettings()
	if m.settings.themeLabel != "Sea" {
		t.Fatal("reopened Settings lost saved display name", m.settings.themeLabel)
	}
}

func TestThemeGroupsSelectionCancelAndImportKeepSource(t *testing.T) {
	m, dir := themeLibraryModel(t)
	source := filepath.Join(t.TempDir(), "Original.toml")
	original := []byte("base='light'\n[colors]\nblue='#123456'\n")
	if err := os.WriteFile(source, original, 0o600); err != nil {
		t.Fatal(err)
	}
	before := cli.ResolvedTheme()
	applyCommand(m, m.pickTheme())
	p := m.dialogs.Top().(*pickerDialog)
	shown := xansi.Strip(p.Content(70))
	if !strings.Contains(shown, "Built-in themes") || !strings.Contains(shown, "Your themes") || strings.Contains(shown, "Built-in theme\n") {
		t.Fatal("theme picker lacks grouped, single-line names", shown)
	}
	themeKey(m, "ctrl+i")
	field := m.dialogs.Top().(*themeInputDialog)
	field.line.SetValue(source)
	themeKey(m, "enter")
	field = m.dialogs.Top().(*themeInputDialog)
	field.line.SetValue("Imported")
	themeKey(m, "enter")
	themeKey(m, "ctrl+s")
	if _, err := os.Stat(filepath.Join(dir, "Imported.toml")); err != nil {
		t.Fatal("import not published", err)
	}
	if got, err := os.ReadFile(source); err != nil || string(got) != string(original) {
		t.Fatal("import changed source", err)
	}
	themeKey(m, "esc")
	themeKey(m, "esc")
	if cli.ResolvedTheme() != before {
		t.Fatal("settings cancel failed to restore original palette")
	}
	if _, err := os.Stat(filepath.Join(dir, "Imported.toml")); err != nil {
		t.Fatal("settings cancel deleted an explicitly saved theme")
	}
}

func TestThemeEditorDiscardDoesNotPublishAndPickerGroupsDoNotMove(t *testing.T) {
	m, dir := themeLibraryModel(t)
	applyCommand(m, m.pickTheme())
	p := m.dialogs.Top().(*pickerDialog)
	before := p.Content(70)
	themeKey(m, "down")
	after := p.Content(70)
	if strings.Count(before, "\n") != strings.Count(after, "\n") {
		t.Fatal("selection inserted description/blank rows")
	}
	themeKey(m, "ctrl+n")
	themePaste(m, "Discarded")
	themeKey(m, "enter")
	themeKey(m, "esc")
	if m.dialogs.Top() != p {
		t.Fatal("editor escape did not return to picker")
	}
	if _, err := os.Stat(filepath.Join(dir, "Discarded.toml")); !os.IsNotExist(err) {
		t.Fatal("discarded editor published theme", err)
	}
	themeKey(m, "esc")
}

func TestThemeGlobalActionsSurviveNoMatchesAndPathsRemainLiteral(t *testing.T) {
	m, _ := themeLibraryModel(t)
	applyCommand(m, m.pickTheme())
	themePaste(m, "NoSuchTheme")
	p := m.dialogs.Top().(*pickerDialog)
	if _, ok := p.menu.Selected(); ok {
		t.Fatal("control query unexpectedly matched")
	}
	themeKey(m, "ctrl+n")
	if _, ok := m.dialogs.Top().(*themeInputDialog); !ok {
		t.Fatal("no-match New did not open")
	}
	themeKey(m, "esc")
	var imported, exported string
	m.options.ReadTheme = func(path string) (*config.ThemeFile, error) {
		imported = path
		return &config.ThemeFile{Base: "dark", Colors: map[string]string{}, Syntax: map[string]string{}}, nil
	}
	themeKey(m, "ctrl+i")
	field := m.dialogs.Top().(*themeInputDialog)
	literal := " /tmp/literal theme.toml "
	field.line.SetValue(literal)
	themeKey(m, "enter")
	if imported != literal {
		t.Fatalf("import changed literal path: %q", imported)
	}
	themeKey(m, "esc")
	p.menu = palette.New("Type to filter", themePickerEntries(p), commandStyles())
	m.options.ExportTheme = func(path string, def *config.ThemeFile) (bool, error) { exported = path; return true, nil }
	themeKey(m, "ctrl+x")
	field = m.dialogs.Top().(*themeInputDialog)
	field.line.SetValue(literal)
	themeKey(m, "enter")
	if exported != literal {
		t.Fatalf("export changed literal path: %q", exported)
	}
	themeKey(m, "esc")
}

func TestNamedThemeInputsAndEditorStayVisibleAfterResize(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 80, Height: 24}, {Width: 159, Height: 45}} {
		m, _ := themeLibraryModel(t)
		m.Update(size)
		applyCommand(m, m.pickTheme())
		if strings.Contains(m.View().Content, "terminal too narrow") {
			t.Fatal("picker cannot fit", size)
		}
		themeKey(m, "ctrl+n")
		themePaste(m, "Visible input")
		if !strings.Contains(m.View().Content, "Visible input") {
			t.Fatal("name input hidden", size)
		}
		themeKey(m, "enter")
		if !strings.Contains(m.View().Content, "Accent") {
			t.Fatal("selected color role hidden", size)
		}
		themeKey(m, "enter")
		field := m.dialogs.Top().(*themeInputDialog)
		if !strings.Contains(m.View().Content, field.line.Value()) {
			t.Fatal("color input hidden", size)
		}
		themeKey(m, "esc")
		themeKey(m, "esc")
		themeKey(m, "esc")
	}
}

func TestAsyncThemeCatalogPreservesFilterAndAppliesHighlightedPreview(t *testing.T) {
	m, dir := themeLibraryModel(t)
	saved, _, err := config.SaveTheme(t.Context(), dir, "Custom", &config.ThemeFile{Base: "dark", Colors: map[string]string{"blue": "#123456"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.settings.values["theme_path"] = saved.Path
	cmd := m.pickTheme()
	themePaste(m, "light")
	p := m.dialogs.Top().(*pickerDialog)
	want := p.value()
	applyCommand(m, cmd)
	if p.menu.Query() != "light" || p.value() != want || p.previewedTheme != p.value() {
		t.Fatal("catalog replaced deliberate filter or left selected theme unapplied", p.menu.Query(), p.value(), p.previewedTheme)
	}
	themeKey(m, "esc")
	applyCommand(m, m.pickTheme())
	p = m.dialogs.Top().(*pickerDialog)
	if p.value() != "custom:"+saved.Path || m.settings.themeLabel != "Custom" || p.previewedTheme != p.value() {
		t.Fatal("active custom selection/name/preview were not rebound", p.value(), p.previewedTheme)
	}
	themeKey(m, "esc")
}

func TestThemeEditorUsesStableCompactRoleColorSourceRows(t *testing.T) {
	m, _ := themeLibraryModel(t)
	applyCommand(m, m.pickTheme())
	parent := m.dialogs.Top().(*pickerDialog)
	m.openThemeEditor(parent, "Layout", &config.ThemeFile{Base: "dark", Colors: map[string]string{"blue": "#123456"}, Syntax: map[string]string{}}, nil)
	editor := m.dialogs.Top().(*themeEditorDialog)
	before := xansi.Strip(editor.Content(70))
	themeKey(m, "down")
	after := xansi.Strip(editor.Content(70))
	if strings.Count(before, "\n") != strings.Count(after, "\n") {
		t.Fatal("moving between roles expanded or collapsed rows")
	}
	if strings.Contains(after, "Inherited · Enter") || strings.Contains(after, "Custom · clear") {
		t.Fatal("selected role still expands an instruction row")
	}
	sourceColumn := -1
	for _, line := range strings.Split(after, "\n") {
		if strings.Contains(line, "Role") && strings.Contains(line, "Color") && strings.Contains(line, "Source") {
			sourceColumn = strings.Index(line, "Source")
			break
		}
	}
	if sourceColumn < 0 || sourceColumn > 52 {
		t.Fatal("missing compact table headers", sourceColumn)
	}
	for _, want := range []struct{ name, source string }{{"Accent", "Custom"}, {"Headings", "Base"}, {"Muted text", "Base"}} {
		found := false
		for _, line := range strings.Split(after, "\n") {
			if strings.Contains(line, want.name) {
				found = true
				sourceIndex := strings.Index(line, want.source)
				if sourceIndex < 0 || xansi.WcWidth.StringWidth(line[:sourceIndex]) != sourceColumn {
					t.Fatalf("%s source did not align with header: %q", want.name, line)
				}
			}
		}
		if !found {
			t.Fatalf("missing role %s", want.name)
		}
	}
	for _, width := range []int{28, 40, 70} {
		for _, line := range strings.Split(editor.Content(width), "\n") {
			if xansi.WcWidth.StringWidth(line) > width {
				t.Fatalf("color table overflowed %d columns", width)
			}
		}
	}
	themeKey(m, "esc")
	themeKey(m, "esc")
}

func TestThemeEditorKeepsSwatchAndSourceStylingStableWhenSelectionMoves(t *testing.T) {
	m, _ := themeLibraryModel(t)
	before := cli.StyleEnabled()
	cli.SetStyleEnabled(true)
	t.Cleanup(func() { cli.SetStyleEnabled(before) })
	applyCommand(m, m.pickTheme())
	parent := m.dialogs.Top().(*pickerDialog)
	m.openThemeEditor(parent, "Styling", &config.ThemeFile{Base: "dark", Colors: map[string]string{"blue": "#123456", "green": "#ffee00"}, Syntax: map[string]string{}}, nil)
	editor := m.dialogs.Top().(*themeEditorDialog)
	for _, want := range []struct {
		name    string
		r, g, b uint32
		white   bool
	}{{"Accent", 0x1212, 0x3434, 0x5656, true}, {"Green", 0xffff, 0xeeee, 0, false}} {
		style := editor.roleSelectionStyle(want.name)
		r, g, b, _ := style.GetBackground().RGBA()
		if r != want.r || g != want.g || b != want.b {
			t.Fatalf("%s selection does not use its role color", want.name)
		}
		fr, fg, fb, _ := style.GetForeground().RGBA()
		expected := uint32(0)
		if want.white {
			expected = 0xffff
		}
		if fr != expected || fg != expected || fb != expected {
			t.Fatalf("%s selection text lacks the shared contrast foreground", want.name)
		}
	}
	if !editor.roleSelectionStyle("Headings").GetReverse() {
		t.Fatal("terminal-default role lost its fallback highlight")
	}
	first, ok := editor.chooser.menu.Selected()
	if !ok || !strings.Contains(first.Key, "\x1b[") {
		t.Fatal("control row has no styled color swatch")
	}
	colorTail := func(content string) string {
		t.Helper()
		for _, line := range strings.Split(content, "\n") {
			if strings.Contains(xansi.Strip(line), "Accent") {
				start := strings.Index(line, first.Key)
				if start < 0 {
					t.Fatal("selection rewrote the color swatch styling")
				}
				return line[start:]
			}
		}
		t.Fatal("missing Accent row")
		return ""
	}
	selected := colorTail(editor.Content(70))
	themeKey(m, "down")
	unselected := colorTail(editor.Content(70))
	if selected != unselected {
		t.Fatal("moving selection changed color/source styling")
	}
	themeKey(m, "esc")
	themeKey(m, "esc")
}

func TestThemeEditorPreservesReservedCommandShortcuts(t *testing.T) {
	m, _ := themeLibraryModel(t)
	applyCommand(m, m.pickTheme())
	parent := m.dialogs.Top().(*pickerDialog)
	m.openThemeEditor(
		parent,
		"Shortcuts",
		&config.ThemeFile{Base: "dark"},
		nil,
	)
	editor := m.dialogs.Top().(*themeEditorDialog)
	themeKey(m, "down")
	before, ok := editor.chooser.menu.Selected()
	if !ok {
		t.Fatal("control selection is missing")
	}
	// The former command wrapper reserved these keys. Extracting palette
	// state must not silently enable its Ctrl+P navigation alias.
	for _, shortcut := range []string{"ctrl+p", "ctrl+k"} {
		themeKey(m, shortcut)
		selected, ok := editor.chooser.menu.Selected()
		if m.dialogs.Top() != editor || !ok || selected != before {
			t.Fatalf("%s changed the editor or its selection", shortcut)
		}
	}
	themeKey(m, "ctrl+n")
	if selected, ok := editor.chooser.menu.Selected(); !ok || selected == before {
		t.Fatal("the existing Ctrl+N navigation alias stopped moving")
	}
	themeKey(m, "esc")
	themeKey(m, "esc")
}
