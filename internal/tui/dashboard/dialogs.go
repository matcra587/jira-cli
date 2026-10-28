package dashboard

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/gechr/primer/dialog"
	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/core"
)

func (m *Model) updateCommands(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch d := m.dialogs.Top().(type) {
		case *pickerDialog:
			if d.kind == "theme" {
				if m.localSaveBusy {
					return nil
				}
				if k.String() == "enter" {
					if selection, ok := d.themes[d.value()]; ok && selection.err != nil {
						d.notice = m.errorText(selection.err)
						return nil
					}
				}
				switch k.String() {
				case "ctrl+n", "ctrl+d", "ctrl+e", "ctrl+r", "ctrl+i", "ctrl+x":
					return m.themePickerAction(d, k.String())
				}
			}
		case *themeEditorDialog:
			if m.localSaveBusy {
				return nil
			}
			switch k.String() {
			case "enter":
				m.editThemeColor(d)
				return nil
			case "ctrl+s":
				return m.saveThemeEditor(d)
			}
		}
	}
	manualScroll := false
	switch m.dialogs.Top().(type) {
	case *writeReview, *operationDialog, *bulkReview, *creationReview, *creationOutcomeDialog, *descriptionReview, *descriptionReload:
		manualScroll = true
	}
	if manualScroll {
		if press, ok := msg.(tea.KeyPressMsg); ok {
			switch press.String() {
			case "pgdown":
				m.dialogs.ScrollBy(max(1, m.height/2))
				return nil
			case "pgup":
				m.dialogs.ScrollBy(-max(1, m.height/2))
				return nil
			}
		}
	}
	cmd, closed, result := m.dialogs.Update(msg)
	if p, ok := m.dialogs.Top().(*pickerDialog); ok && p.kind == "theme" && result == dialog.ResultNone {
		m.previewHighlightedTheme(p)
	}
	if result != dialog.ResultNone {
		switch d := closed.(type) {
		case *themeEditorDialog:
			if d.restore != nil {
				d.restore()
				m.refreshThemeStyles()
				d.parent.restyle()
			}
		case *commandDialog:
			m.dialogs.SetFrame(commandFrame())
		case *writeReview:
			if d.kind == "labels" {
				m.dialogs.SetFrame(commandFrame())
			}
		case *descriptionDialog:
			m.keepDescriptionDraft(d)
		case *pickerDialog:
			if d.restorePreview != nil {
				d.restorePreview()
				m.refreshThemeStyles()
			}
			if result == dialog.ResultClose && d.kind == "create-type" && d.creation == m.creation {
				m.creation.project = core.IssueCreateProjectRead{}
				m.creation.typeID = ""
			}
		case *creationLabels:
			m.dialogs.SetFrame(commandFrame())
			if d.draft == m.creation {
				m.creation.labels = slices.Clone(d.labels)
				m.creation.labelsSet = result == dialog.ResultSubmit || !slices.Equal(d.labels, d.original) || m.creation.labelsSet
				m.creation.pendingLabel = d.line.Value()
			}
		case *summaryDialog:
			if d.value() == d.original {
				delete(m.fieldDrafts, d.id+":summary")
			} else {
				m.fieldDrafts[d.id+":summary"] = d.value()
			}
		case *labelsDialog:
			m.dialogs.SetFrame(commandFrame())
			changed := !slices.Equal(d.labels, d.original)
			if !changed && d.line.Value() == "" {
				delete(m.labelDrafts, d.id)
			} else {
				m.labelDrafts[d.id] = labelDraft{labels: slices.Clone(d.labels), pending: d.line.Value(), changed: changed}
			}
		case *bulkTextDialog:
			m.keepBulkDraft(d)
		case *textDialog:
			if d.kind == "comment" {
				m.drafts[d.target] = d.value()
			}
		}
	}
	if result != dialog.ResultSubmit {
		return cmd
	}
	switch d := closed.(type) {
	case *themeInputDialog:
		return tea.Batch(cmd, m.acceptThemeInput(d))
	case *descriptionDialog:
		return tea.Batch(cmd, m.acceptDescriptionEditor(d))
	case *descriptionReview:
		return tea.Batch(cmd, m.submitDescription(d))
	case *descriptionReload:
		if m.dialogs.Top() != d.editor {
			return cmd
		}
		snapshot := d.editor.draft.session.Snapshot()
		return tea.Batch(cmd, m.loadDescription(snapshot.IssueID, snapshot.IssueID, d.editor))
	case *creationDiscard:
		if d.draft == m.creation {
			m.creation = &creationDraft{}
			m.creationEditor = nil
			return tea.Batch(cmd, m.creationProjects())
		}
		return cmd
	case *creationReview:
		return tea.Batch(cmd, m.submitCreation(d))
	case *creationLabels:
		return cmd
	case *creationText:
		return tea.Batch(cmd, m.acceptCreationText(d))
	case *summaryDialog:
		m.acceptSummary(d)
		return cmd
	case *labelsDialog:
		m.acceptLabels(d)
		return cmd
	case *hierarchyTypeMenu:
		if d.page == m.settings && m.settings.editor == d.editor {
			if m.keepSetting("tui_hierarchy_query", d.value()) {
				m.settings.editor = nil
			}
		}
		return cmd
	case *settingMenu:
		if d.page == m.settings {
			m.keepSetting(d.target, d.value())
		}
		return cmd
	case *commandDialog:
		if entry, ok := d.menu.Selected(); ok {
			return tea.Batch(cmd, m.updateKey(key.Replay(entry.Key)))
		}
	case *pickerDialog:
		return tea.Batch(cmd, m.acceptPicker(d))
	case *operationDialog:
		if d.earlier {
			m.openBatchHistory()
		} else {
			m.openBatchResult(m.lastBatch)
		}
		return cmd
	case *bulkReview:
		return tea.Batch(cmd, m.submitBulk(d))
	case *bulkTextDialog:
		return tea.Batch(cmd, m.acceptBulkText(d))
	case *writeReview:
		return tea.Batch(cmd, m.submitWrite(d))
	case *textDialog:
		return tea.Batch(cmd, m.acceptText(d))
	}

	return cmd
}
