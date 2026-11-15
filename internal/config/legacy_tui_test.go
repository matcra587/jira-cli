package config

import (
	"errors"
	"slices"
	"testing"

	koanftoml "github.com/knadh/koanf/parsers/toml"
)

const runtimeTUIFixture = `[[profiles]]
name='default'
secret_backend='env'
[tui]
preview='left'
preview_size=55
refresh_interval=90
tabs=['search','Queue']
default_tab='Queue'
default_lens='Urgent'
[[tui.sections]]
title='Queue'
jql='project = DEMO ORDER BY key ASC'
[[tui.lenses]]
title='Urgent'
jql='priority = High'
[tui.keys]
copy_key=['alt+y']
`

func TestProfileMigrationActivatesLegacyTUIThroughCurrentSettings(t *testing.T) {
	t.Parallel()
	migration, err := PrepareProfileMigration(t.Context(), []byte(runtimeTUIFixture), "", "", LegacyMigrationOptions{})
	if err != nil || len(migration.Unmapped()) != 0 || len(migration.Deferred()) != 0 {
		t.Fatal("supported settings did not map", err, migration)
	}
	candidate, err := migration.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := decodeProfileDocument(candidate, true)
	if err != nil {
		t.Fatal("candidate rejected by current loader", err)
	}
	fields, _, err := doc.selected("")
	if err != nil {
		t.Fatal(err)
	}
	current, err := unmarshalFields(fields)
	if err != nil || !current.TUIPreview || current.TUIPreviewDock != PreviewDockLeft || current.TUIPreviewSize != 55 || current.TUIRefreshInterval != 90 || !current.TUIRefreshOnFocus {
		t.Fatal("runtime preferences lost", current, err)
	}
	w := current.TUIWorkspace
	if len(w.Sections) != 1 || w.Sections[0].JQL != "project = DEMO ORDER BY key ASC" || w.LandingView != "legacy-sections-1" || !slices.Equal(w.Tabs, []string{"search", "legacy-sections-1"}) || w.LandingLens != "legacy-lenses-1" || !slices.Equal(w.Keys["copy-key"], []string{"alt+y"}) {
		t.Fatal("workspace queries, references or bindings lost", w)
	}
	for _, profile := range doc.profiles {
		for key := range profile.(map[string]any) {
			if globalSetting(key) && key != "editor" {
				t.Fatal("TUI setting was stored in a profile", key)
			}
		}
	}
}

func TestLegacyTUIUnsupportedDeclarationsNeverSilentlyChangeMeaning(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ field, declaration string }{
		{"preview_size", "preview_size=80"},
		{"preview", "preview='unknown'"},
		{"tabs", "tabs=['issues','epics']"},
		{"default_tab", "default_tab='issues'"},
		{"icons", "icons='nerd'"},
		{"keys", "keys={filter=['/']}"}, // Conflicts with the current search shortcut.
		{"keys", "keys={worklog=['alt+w']}"},
		{"lenses", "lenses=[{title='Ordered',jql='project = DEMO ORDER BY key ASC'}]"},
	} {
		t.Run(test.declaration, func(t *testing.T) {
			original := []byte("[[profiles]]\nname='default'\n[tui]\n" + test.declaration)
			migration, err := PrepareProfileMigration(t.Context(), original, "", "", LegacyMigrationOptions{})
			if err != nil || !slices.Contains(migration.Unmapped(), "tui."+test.field) {
				t.Fatal("unsupported meaning disappeared", err)
			}
			if candidate, err := migration.Candidate(); candidate != nil || !errors.Is(err, ErrLegacyPreferencesUnmapped) {
				t.Fatal("partial migration became publishable", err)
			}
		})
	}
}

func TestLegacyTUIZeroAndAbsentPreviewSizeRetainOldDefault(t *testing.T) {
	t.Parallel()
	for _, declaration := range []string{"", "preview_size=0\n"} {
		original := []byte("[[profiles]]\nname='default'\n[tui]\npreview='hidden'\nrefresh_interval=0\n" + declaration)
		migration, err := PrepareProfileMigration(t.Context(), original, "", "", LegacyMigrationOptions{})
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := migration.Candidate()
		if err != nil {
			t.Fatal(err)
		}
		fields, err := koanftoml.Parser().Unmarshal(candidate)
		if err != nil || fields["tui_preview_size"] != int64(50) || fields["tui_preview"] != false || fields["tui_refresh_interval"] != int64(0) || fields["tui_refresh_on_focus"] != false {
			t.Fatal("zero/absence semantics changed", err, string(candidate))
		}
	}
}

func TestLegacyFormatDetectionDoesNotRequireMigrationForSupportedFiles(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"", "default_project='DEMO'\n", "format_version=1\ndefault_profile=''\n[profiles]\n"} {
		if _, err := decodeProfileDocument([]byte(source), true); err != nil {
			t.Fatal("supported settings were treated as legacy", err)
		}
	}
	for _, source := range []string{"format_version=999\n[profiles]\n", "[[profiles]]\nunknown='invalid'\n"} {
		if _, err := decodeProfileDocument([]byte(source), true); err == nil || errors.Is(err, ErrMigrationRequired) {
			t.Fatal("malformed or future settings were offered legacy conversion", err)
		}
	}
}
