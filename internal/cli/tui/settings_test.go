package tui

import (
	"context"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/config"
)

func TestDashboardProjectsEveryPublicScalarAndSavesProfileBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	const authored = "format_version=1\ndefault_profile='work'\n[aliases]\nquick='version'\n[profiles.work]\nbase_url='https://example.invalid'\n[profiles.other]\nbase_url='https://other.invalid'\n"
	if err := os.WriteFile(path, []byte(authored), 0o600); err != nil {
		t.Fatal(err)
	}
	load := func() *config.Resolved {
		t.Helper()
		resolved, err := config.Load(config.Options{Path: path, Environ: func() []string { return []string{"JIRA_DEFAULT_BOARD=environment-board"} }})
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	options, err := dashboardOptions(t.Context(), load())
	if err != nil {
		t.Fatal(err)
	}
	if len(options.SettingValues) != len(config.WritableSettingKeys()) {
		t.Fatal("public setting projection is incomplete")
	}
	for _, key := range config.WritableSettingKeys() {
		if _, ok := options.SettingValues[key]; !ok || options.SettingSources[key] == "" {
			t.Fatalf("missing effective value or source for %s", key)
		}
	}
	if _, ok := options.SettingValues["api_token"]; ok {
		t.Fatal("credential entered the screen options")
	}
	if options.SettingSources["default_board"] != "env" {
		t.Fatal("environment ownership disappeared")
	}
	if err := options.SaveSettings(map[string]string{"default_project": "DEMO", "timeout": "30s", "read_only": "true", "team_account_ids": `["person-1","person-2"]`}); err != nil {
		t.Fatal(err)
	}
	resolved := load()
	if resolved.Config.DefaultProject != "DEMO" || !resolved.Config.ReadOnly || resolved.Config.Timeout.String() != "30s" || len(resolved.Config.TeamAccountIDs) != 2 || !maps.Equal(resolved.Config.Aliases, map[string]string{"quick": "version"}) {
		t.Fatal("batch save lost scalar types or aliases")
	}
	file, err := config.OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	board, err := file.Setting("default_board")
	if err != nil || board.Present {
		t.Fatal("save published an untouched environment value")
	}
	other, err := config.Load(config.Options{Path: path, Profile: "other", Environ: func() []string { return nil }})
	if err != nil || other.Config.BaseURL != "https://other.invalid" || other.Config.DefaultProject != "" {
		t.Fatal("batch save modified the other profile")
	}
}

func TestDashboardSettingsPublishAndRefuseExternalEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	const authored = "format_version=1\ndefault_profile='work'\n[profiles.work]\nbase_url='https://example.invalid'\n"
	if err := os.WriteFile(path, []byte(authored), 0o600); err != nil {
		t.Fatal(err)
	}
	load := func() *config.Resolved {
		t.Helper()
		resolved, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	options, err := dashboardOptions(t.Context(), load())
	if err != nil {
		t.Fatal(err)
	}
	if !options.Settings["tui_preview"] || options.SettingSources["tui_preview"] != "default" {
		t.Fatal("default display preference lost provenance")
	}
	if err := options.SaveSettings(map[string]string{"tui_preview": "false"}); err != nil {
		t.Fatal(err)
	}
	resolved := load()
	if resolved.Config.TUIPreview || resolved.Profile != "work" || resolved.Config.BaseURL != "https://example.invalid" {
		t.Fatal("display save lost profile settings or its explicit false")
	}
	if err := options.SaveSettings(map[string]string{"tui_labels": "false"}); err != nil {
		t.Fatalf("second save did not advance snapshot: %v", err)
	}
	const external = "# external edit\n" + authored
	if err := os.WriteFile(path, []byte(external), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := options.SaveSettings(map[string]string{"tui_status": "false"}); !errors.Is(err, config.ErrFileChanged) {
		t.Fatalf("external edit was not refused: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != external {
		t.Fatal("conflict replaced external content")
	}
}

func TestDashboardSettingsAndQueriesUseScreenCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	options, err := dashboardOptions(t.Context(), resolved)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	options.BindContext(ctx)
	cancel()
	if err := options.SaveSettings(map[string]string{"tui_preview": "false"}); !errors.Is(err, context.Canceled) {
		t.Fatal("settings save outlived the screen", err)
	}
	if _, err := options.LoadViews(); !errors.Is(err, context.Canceled) {
		t.Fatal("saved-view read outlived the screen", err)
	}
	if err := options.SaveView("canceled", "project = DEMO"); !errors.Is(err, context.Canceled) {
		t.Fatal("query publication outlived the screen", err)
	}
	if _, err := options.LoadThemes(""); !errors.Is(err, context.Canceled) {
		t.Fatal("theme discovery outlived the screen", err)
	}
	if _, err := options.ReadTheme(filepath.Join(t.TempDir(), "missing.toml")); !errors.Is(err, context.Canceled) {
		t.Fatal("theme import outlived the screen", err)
	}
	definition := &config.ThemeFile{Base: "dark"}
	if _, published, err := options.WriteTheme(
		"Canceled",
		definition,
		nil,
	); published || !errors.Is(err, context.Canceled) {
		t.Fatal("theme publication outlived the screen", published, err)
	}
	exported := filepath.Join(t.TempDir(), "canceled.toml")
	if published, err := options.ExportTheme(exported, definition); published || !errors.Is(err, context.Canceled) {
		t.Fatal("theme export outlived the screen", published, err)
	}
	if _, err := os.Stat(exported); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled theme export published a file", err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil || len(bytes) != 0 {
		t.Fatal("canceled screen changed its configuration")
	}
}

func TestDashboardSettingsWritesRemainBoundToSelectedNondefaultProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	const authored = "format_version=1\ndefault_profile='other'\n[profiles.work]\nbase_url='https://work.invalid'\n[profiles.other]\nbase_url='https://other.invalid'\n"
	if err := os.WriteFile(path, []byte(authored), 0o600); err != nil {
		t.Fatal(err)
	}
	load := func(profile string) *config.Resolved {
		t.Helper()
		resolved, err := config.Load(config.Options{Path: path, Profile: profile, Environ: func() []string { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	options, err := dashboardOptions(t.Context(), load("work"))
	if err != nil {
		t.Fatal(err)
	}
	for _, values := range []map[string]string{{"default_project": "NEW"}, {"timeout": "30s"}} {
		if err := options.SaveSettings(values); err != nil {
			t.Fatal(err)
		}
	}
	work, other := load("work"), load("other")
	if work.Config.DefaultProject != "NEW" || work.Config.Timeout.String() != "30s" || other.Config.DefaultProject != "" || other.Config.Timeout != 0 {
		t.Fatal("initial or reopened settings snapshot drifted to the saved default profile")
	}
}

func TestDashboardRefusesDifferentFileThanItsResolvedConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte("default_project='OLD'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("default_project='NEW'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := dashboardOptions(t.Context(), resolved); !errors.Is(err, config.ErrFileChanged) {
		t.Fatalf("settings accepted a different preimage from its resolved values: %v", err)
	}
}

func TestDashboardNamedThemeMetadataSurvivesReopeningOptions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.toml")
	themeDir := filepath.Join(dir, "themes")
	saved, _, err := config.SaveTheme(t.Context(), themeDir, "Ocean", &config.ThemeFile{Base: "dark"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := config.SaveTheme(t.Context(), themeDir, "Sea", saved.Definition, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("theme='dark'\ntheme_path='themes/Ocean.toml'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		resolved, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
		if err != nil {
			t.Fatal(err)
		}
		options, err := dashboardOptions(t.Context(), resolved)
		if err != nil || options.ThemeLabel != "Sea" {
			t.Fatal("saved display name lost at runtime creation", options.ThemeLabel, err)
		}
		entries, err := options.LoadThemes(options.SettingValues["theme_path"])
		if err != nil || len(entries) != 1 || !entries[0].Active || entries[0].Name != "Sea" {
			t.Fatal("relative selection not bound to active library entry", entries, err)
		}
	}
}

func TestDashboardWinningThemeEnvironmentDoesNotReadBypassedCustomFile(t *testing.T) {
	if os.Getenv("JIRA_TUI_THEME_OVERRIDE_PROBE") != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestDashboardWinningThemeEnvironmentDoesNotReadBypassedCustomFile$", "-test.count=1")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "XDG_CONFIG_HOME=" + t.TempDir(), "JIRA_THEME=nord", "JIRA_TUI_THEME_OVERRIDE_PROBE=1"}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("theme override probe: %v\n%s", err, output)
		}
		return
	}
	cli.ConfigureTheme()
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte("theme='dark'\ntheme_path='missing-custom.toml'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	options, err := dashboardOptions(t.Context(), resolved)
	if err != nil || options.Theme != "nord" || options.ThemeLabel != "nord" || options.ThemeNotice != "" || options.SettingSources["theme"] != "env" {
		t.Fatalf("bypassed custom metadata hid winning theme: %+v %v", options, err)
	}
}
