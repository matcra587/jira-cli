package dashboard

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/key"
	"github.com/gechr/primer/layout"
	"github.com/gechr/primer/palette"
)

type choice struct{ label, description, value string }

type pickerDialog struct {
	initialThemes             bool
	hintWidth, themeCursorRow int
	manageThemes              bool
	themes                    map[string]themeSelection
	loading                   bool
	notice, selectTheme       string
	restorePreview            func()
	previewedTheme            string
	creation                  *creationDraft
	title, kind, target       string
	menu                      palette.Model
	choices                   []choice
	padRows                   int
	keys                      []string
	transitionIDs             map[string]map[string]string
	facets                    map[string]issueFacet
}

// Primer v0.2.9 has no palette style setter. Rebuild through its public API,
// preserving the filter and cursor while applying the highlighted palette.
func (p *pickerDialog) restyle() {
	query, cursor := p.menu.Query(), p.menu.CursorLine()
	entries := make([]palette.Entry, 0, len(p.choices))
	for _, c := range p.choices {
		entries = append(entries, palette.Entry{Name: c.label, Desc: c.description})
	}
	p.menu = palette.New("Type to filter", entries, commandStyles())
	for _, r := range query {
		p.menu.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	for i := 2; i < cursor; i++ {
		p.menu.Update(key.Replay("down"))
	}
}

func (p *pickerDialog) Title() string     { return p.title }
func (p *pickerDialog) Hints() []key.Hint { return nil }
func (p *pickerDialog) Footer() string {
	if p.kind == "theme" {
		return p.themeFooter()
	}
	if p.kind == "issue" {
		return "enter open · esc back"
	}
	return "enter select · esc back"
}

func (p *pickerDialog) Content(width int) string {
	if p.kind == "theme" {
		return p.themeContent(width)
	}
	rows := strings.Split(p.menu.View(), "\n")
	for len(rows) < max(12, p.padRows) {
		rows = append(rows, "")
	}
	return strings.Join(layout.NormalizeLines(rows, width), "\n")
}

func (p *pickerDialog) ScrollTo() (int, int, bool) {
	if p.kind == "theme" {
		return p.themeCursorRow, 1, true
	}
	return p.menu.CursorLine(), 1, true
}

func (p *pickerDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd, dialog.Result) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "esc":
			return p, nil, dialog.ResultClose
		case "enter":
			if _, ok := p.menu.Selected(); ok || p.kind == "issue" && strings.TrimSpace(p.menu.Query()) != "" {
				return p, nil, dialog.ResultSubmit
			}
		}
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		updatePalettePaste(&p.menu, paste.Content)
		return p, nil, dialog.ResultNone
	}
	return p, p.menu.Update(msg), dialog.ResultNone
}

func (m *Model) pick(title, kind, target string, choices []choice) {
	choices = slices.Clone(choices)
	entries := make([]palette.Entry, 0, len(choices))
	used := make(map[string]bool)
	for i, c := range choices {
		label := c.label
		if used[label] {
			label = fmt.Sprintf("%s (%d)", label, i+1)
		}
		for used[label] {
			label += " ·"
		}
		used[label] = true
		choices[i].label = label
		entries = append(entries, palette.Entry{Name: label, Desc: c.description})
	}
	menu := palette.New("Type to filter", entries, commandStyles())
	m.dialogs.Push(&pickerDialog{title: title, kind: kind, target: target, choices: choices, menu: menu, padRows: len(strings.Split(menu.View(), "\n"))})
}

func (p *pickerDialog) value() string {
	if selected, ok := p.menu.Selected(); ok {
		for _, c := range p.choices {
			if selected.Name == c.label {
				return c.value
			}
		}
	}
	return strings.TrimSpace(p.menu.Query())
}

// Primer's palette owns typed filtering but deliberately ignores PasteMsg.
// Dialogs share this translation, excluding terminal control characters.
func updatePalettePaste(menu *palette.Model, text string) {
	for _, r := range text {
		if !unicode.IsControl(r) {
			menu.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
	}
}
