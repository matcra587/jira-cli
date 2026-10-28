package dashboard

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gechr/primer/carousel"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/palette"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
)

type themeEditorDialog struct {
	resolvedColors map[string]string
	selectedRow    int
	hasError       bool
	chooserRow     int
	parent         *pickerDialog
	source         *config.SavedTheme
	name, notice   string
	definition     *config.ThemeFile
	sections       *carousel.Model
	chooser        *paletteState
	roles          map[string]string
	restore        func()
	preview        func(*config.ThemeFile) error
	saving         bool
	hintWidth      int
}

func (d *themeEditorDialog) Title() string   { return "Edit theme · " + cli.Text(d.name) }
func (*themeEditorDialog) Hints() []key.Hint { return nil }
func (d *themeEditorDialog) Footer() string {
	if d.saving {
		return "Saving theme…"
	}
	return renderDialogHints(d.hintWidth, []key.Hint{{Key: "tab", Desc: "section"}, {Key: "enter", Desc: "edit"}, {Key: "ctrl+s", Desc: "save theme"}, {Key: "esc", Desc: "discard"}})
}

func (d *themeEditorDialog) Content(width int) string {
	prefix := d.sections.View() + "\n" + strings.Join(layout.WrapLines(d.notice, width), "\n") + "\n"
	d.chooserRow = strings.Count(prefix, "\n")
	return prefix + d.themeRows(width)
}

func (d *themeEditorDialog) ScrollTo() (int, int, bool) {
	if d.hasError {
		return 1, max(1, d.chooserRow-1), true
	}
	return d.selectedRow, 1, true
}

func (d *themeEditorDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	if d.saving {
		return d, nil, dialog.ResultNone
	}
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "esc":
			return d, nil, dialog.ResultClose
		case "tab", "shift+tab":
			if k.String() == "tab" {
				d.sections.Next()
			} else {
				d.sections.Prev()
			}
			d.chooser = nil
			d.rebuild()
			return d, nil, dialog.ResultNone
		case "enter", "ctrl+s":
			return d, nil, dialog.ResultNone
		case "ctrl+p", "ctrl+k":
			// Keep command shortcuts reserved instead of inheriting the
			// palette's Ctrl+P navigation alias.
			return d, nil, dialog.ResultNone
		}
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		updatePalettePaste(&d.chooser.menu, paste.Content)
		return d, nil, dialog.ResultNone
	}
	return d, d.chooser.menu.Update(msg), dialog.ResultNone
}

func (d *themeEditorDialog) rebuild() {
	query, selection := "", ""
	if d.chooser != nil {
		query = d.chooser.menu.Query()
		if entry, ok := d.chooser.menu.Selected(); ok {
			selection = entry.Name
		}
	}
	values, err := cli.ThemeColorValues(d.definition)
	if err != nil {
		d.notice = cli.Text(err.Error())
		d.hasError = true
		return
	}
	d.resolvedColors = values
	d.roles = map[string]string{}
	entries := []palette.Entry{}
	section := d.sections.ActiveItem()
	roles := config.ThemeColorRoles()
	prefix := "colors"
	if section == "Syntax" {
		roles = config.ThemeSyntaxRoles()
		prefix = "syntax"
	}
	if section == "Palette" {
		entries = append(entries,
			palette.Entry{Name: "Base preset", Key: d.definition.Base},
			palette.Entry{Name: "Entity palette", Key: strconv.Itoa(len(d.definition.EntityColors)) + " overrides"},
		)
		d.roles["Base preset"], d.roles["Entity palette"] = "base", "entities"
	} else {
		for _, role := range roles {
			name := themeRoleName(role)
			roleKey := themeDefinitionKey(prefix, role)
			value := values[roleKey]
			if value == "" {
				value = "terminal default"
			}
			swatch := value
			if strings.HasPrefix(value, "#") && cli.StyleEnabled() {
				swatch = lipgloss.NewStyle().Background(lipgloss.Color(value)).Render("  ") + " " + value
			}
			entries = append(entries, palette.Entry{Name: name, Key: swatch})
			d.roles[name] = roleKey
		}
	}
	state := newPaletteState(entries)
	d.chooser = &state
	updatePalettePaste(&d.chooser.menu, query)
	for i, entry := range d.chooser.visibleEntries() {
		if entry.Name == selection {
			for range i {
				d.chooser.menu.Update(key.Replay("down"))
			}
			break
		}
	}
}

func (m *Model) openThemeEditor(
	parent *pickerDialog,
	name string,
	def *config.ThemeFile,
	source *config.SavedTheme,
) {
	d := &themeEditorDialog{
		parent: parent, name: name, definition: config.CloneThemeFile(def), source: source,
		sections: carousel.New("Interface", "Syntax", "Palette"),
		notice:   "Enter edits a color. Ctrl+S saves; Esc discards.",
	}
	if d.definition.Colors == nil {
		d.definition.Colors = map[string]string{}
	}
	if d.definition.Syntax == nil {
		d.definition.Syntax = map[string]string{}
	}
	d.definition.Name = name
	d.preview = func(next *config.ThemeFile) error {
		restore, err := cli.PreviewThemeDefinition(next)
		if err != nil {
			return err
		}
		if d.restore == nil {
			d.restore = restore
		}
		m.refreshThemeStyles()
		return nil
	}
	d.rebuild()
	m.dialogs.Push(d)
}

func (m *Model) editThemeColor(d *themeEditorDialog) {
	selected, ok := d.chooser.menu.Selected()
	if !ok {
		return
	}
	role := d.roles[selected.Name]
	kind, value := "color", ""
	switch role {
	case "base":
		kind, value = "base", d.definition.Base
	case "entities":
		kind, value = "entities", strings.Join(d.definition.EntityColors, ", ")
	default:
		section, name, _ := strings.Cut(role, ".")
		if section == "colors" {
			value = d.definition.Colors[name]
		} else {
			value = d.definition.Syntax[name]
		}
	}
	before := config.CloneThemeFile(d.definition)
	field := m.openThemeInput(themeInputRequest{
		title: selected.Name, kind: kind, value: value, parent: d.parent, editor: d,
	})
	field.apply = func(value string) error {
		next := config.CloneThemeFile(before)
		switch role {
		case "base":
			next.Base = value
		case "entities":
			next.EntityColors = nil
			if value != "" {
				for _, color := range strings.Split(value, ",") {
					next.EntityColors = append(next.EntityColors, strings.TrimSpace(color))
				}
			}
		default:
			section, name, _ := strings.Cut(role, ".")
			colors := next.Colors
			if section == "syntax" {
				colors = next.Syntax
			}
			if value == "" {
				delete(colors, name)
			} else {
				colors[name] = value
			}
		}
		if err := d.preview(next); err != nil {
			return err
		}
		d.definition = next
		return nil
	}
	field.cancel = func() {
		d.definition = before
		if err := d.preview(before); err != nil {
			d.notice = m.errorText(err)
		}
		d.rebuild()
	}
}
