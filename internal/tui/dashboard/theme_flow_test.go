package dashboard

import (
	"errors"
	"strings"
	"testing"

	"github.com/gechr/primer/key"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
)

func TestThemeSaveFailureRetainsEditorAndReleasesPublication(t *testing.T) {
	m, _ := themeLibraryModel(t)
	applyCommand(m, m.pickTheme())
	themeKey(m, "ctrl+n")
	themePaste(m, "Ocean")
	themeKey(m, "enter")
	editor := m.dialogs.Top().(*themeEditorDialog)
	beforePath, beforeLabel := m.settings.values["theme_path"], m.settings.themeLabel
	m.options.WriteTheme = func(string, *config.ThemeFile, *config.SavedTheme) (*config.SavedTheme, bool, error) {
		return nil, true, errors.New("completion failed")
	}
	_, cmd := m.Update(key.Replay("ctrl+s"))
	if cmd == nil || !m.localSaveBusy || !editor.saving {
		t.Fatal("save did not retain publication ownership")
	}
	applyCommand(m, cmd)
	if m.localSaveBusy || editor.saving || !editor.hasError || m.dialogs.Top() != editor {
		t.Fatal("failed save did not release publication while retaining editor")
	}
	if editor.parent.notice != editor.notice ||
		!strings.HasPrefix(editor.notice, "Theme file was published, but completion failed.") {
		t.Fatalf("publication failure was not surfaced consistently: %q", editor.notice)
	}
	if m.settings.values["theme_path"] != beforePath || m.settings.themeLabel != beforeLabel {
		t.Fatal("failed save changed selection")
	}
}

func TestThemePublicationCompletionReleasesOwnershipWithoutSettings(t *testing.T) {
	for _, operation := range []string{"save", "export"} {
		t.Run(operation, func(t *testing.T) {
			m, _ := themeLibraryModel(t)
			applyCommand(m, m.pickTheme())
			picker := m.dialogs.Top().(*pickerDialog)
			var editor *themeEditorDialog
			if operation == "save" {
				themeKey(m, "ctrl+n")
				themePaste(m, "Ocean")
				themeKey(m, "enter")
				editor = m.dialogs.Top().(*themeEditorDialog)
			} else {
				themeKey(m, "ctrl+x")
				themePaste(m, "unused.toml")
				m.options.ExportTheme = func(string, *config.ThemeFile) (bool, error) { return false, nil }
			}
			keyName := "enter"
			if editor != nil {
				keyName = "ctrl+s"
				m.options.WriteTheme = func(string, *config.ThemeFile, *config.SavedTheme) (*config.SavedTheme, bool, error) {
					return nil, false, nil
				}
			}
			_, cmd := m.Update(key.Replay(keyName))
			if cmd == nil || !m.localSaveBusy {
				t.Fatal("publication was not scheduled")
			}
			picker.loading = true
			m.settings = nil
			applyCommand(m, cmd)
			if m.localSaveBusy || picker.loading || editor != nil && editor.saving {
				t.Fatal("completion retained publication ownership after settings closed")
			}
		})
	}
}

func TestThemeEditorAdmissionRejectsInvalidValuesBeforeStateChanges(t *testing.T) {
	for _, fieldName := range []string{"Accent", "Base preset", "Entity palette"} {
		t.Run(fieldName, func(t *testing.T) {
			m, _ := themeLibraryModel(t)
			applyCommand(m, m.pickTheme())
			themeKey(m, "ctrl+n")
			themePaste(m, "Ocean")
			themeKey(m, "enter")
			editor := m.dialogs.Top().(*themeEditorDialog)
			if fieldName != "Accent" {
				themeKey(m, "tab")
				themeKey(m, "tab")
			}
			themePaste(m, fieldName)
			themeKey(m, "enter")
			field := m.dialogs.Top().(*themeInputDialog)
			beforeDefinition, beforePalette := editor.definition, cli.ResolvedTheme()
			field.line.SetValue("invalid")
			themeKey(m, "enter")
			if m.dialogs.Top() != field || field.notice == "" || editor.definition != beforeDefinition {
				t.Fatal("invalid value was admitted or changed editor definition")
			}
			if cli.ResolvedTheme() != beforePalette || editor.restore != nil {
				t.Fatal("invalid value changed live preview or restore baseline")
			}
			if fieldName == "Entity palette" {
				field.line.SetValue("")
				themeKey(m, "enter")
				if m.dialogs.Top() != editor || editor.definition.EntityColors != nil {
					t.Fatal("clearing entity overrides did not preserve inherited palette")
				}
			}
		})
	}
}
