package dashboard

import (
	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/cli"
)

func renderDialogHints(width int, hints []key.Hint) string {
	prefix := ""
	renderer := key.Renderer{Prefix: &prefix, Width: width, Gap: " · "}
	if cli.StyleEnabled() {
		renderer.Styles = key.Styles{Key: *cli.ResolvedTheme().Blue, Text: *cli.ResolvedTheme().Dim}
	}
	return renderer.Render(hints)
}

// Center the label form as one left-aligned group using the frame's own
// symmetric padding, so its title, fields and pinned footer share an origin.
// Narrow screens reclaim that padding rather than compressing the controls.
func (m *Model) prepareDialogLayout() {
	if menu, ok := m.dialogs.Top().(*settingMenu); ok {
		menu.hintWidth = max(1, min(64, m.pageWidth()-12))
		return
	}
	if p, ok := m.dialogs.Top().(*pickerDialog); ok && p.kind == "theme" {
		p.hintWidth = max(1, min(64, m.pageWidth()-12))
		return
	}
	if editor, ok := m.dialogs.Top().(*themeEditorDialog); ok {
		editor.hintWidth = max(1, min(64, m.pageWidth()-12))
		return
	}
	var labels *labelsDialog
	var review *writeReview
	switch d := m.dialogs.Top().(type) {
	case *labelsDialog:
		labels = d
	case *creationLabels:
		labels = &d.labelsDialog
	case *writeReview:
		if d.kind == "labels" {
			review = d
		}
	}
	if labels == nil && review == nil {
		return
	}
	padding := 2
	hintWidth := max(1, min(52, m.pageWidth()-12))
	if m.pageWidth() >= 70 {
		padding, hintWidth = 7, 48
	}
	if review != nil {
		if review.framePadding != padding {
			m.dialogs.SetFrame(dialogFramePadding(64, 24, padding))
			review.framePadding = padding
		}
		review.hintWidth = hintWidth
		return
	}
	if labels.framePadding != padding {
		m.dialogs.SetFrame(dialogFramePadding(64, 24, padding))
		labels.framePadding = padding
	}
	labels.hintWidth = hintWidth
}
