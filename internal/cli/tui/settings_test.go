package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
)

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
	bytes, err := os.ReadFile(path)
	if err != nil || len(bytes) != 0 {
		t.Fatal("canceled screen changed its configuration")
	}
}
