package config

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingUpdateValidatesAllWritableScalarsAndPreservesOtherDeclarations(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"credential_id":   "b6ff8a17-9f47-4e25-83b8-cf3f38466700",
		"cloud_id":        "fixture-cloud",
		"api_token_env":   "JIRA_TOKEN_SYNTHETIC_WORK",
		"default_project": "DEFAULT", "default_issue_type": "Task", "default_board": "Sprint",
		"refresh_interval": "45", "team_account_ids": `["team-1","team,2"]`,
		"base_url": "https://example.invalid", "email": "saved@example.invalid",
		"account_id": "cached-account", "account_id_site": "https://example.invalid", "account_id_email": "saved@example.invalid", "account_id_cloud_id": "fixture-cloud",
		"tui_compact": "false", "tui_updated": "false", "tui_auto_page": "false",
		"tui_preview_dock": "bottom", "tui_preview_size": "55",
		"tui_refresh_interval": "45", "tui_refresh_on_focus": "false", "tui_change_markers": "false",
		"tui_workspace":       `{"landing_view":"search"}`,
		"tui_hierarchy_query": `issuetype = "10002" ORDER BY updated DESC`,
		"tui_preview":         "false", "tui_status": "false", "tui_assignee": "false",
		"tui_labels": "false", "tui_priority": "false", "tui_issue_type": "false",
		"keyring": "false", "read_only": "false", "timeout": "2m", "theme": "dark",
		"secret_backend": "1password", "onepassword_account": "Fixture Account", "onepassword_vault": "Fixture Vault", "onepassword_item": "Fixture Item",
		"glyphs": "nerd", "workday_seconds": "36000", "queries_path": "local queries", "theme_path": "custom palette.toml", "editor": "future-editor --wait",
	}
	if len(values) != len(WritableSettingKeys()) {
		t.Fatal("writable setting inventory lacks a typed publication case")
	}
	for _, key := range WritableSettingKeys() {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			path := aliasFixture(t, "# authored\n[aliases]\n'Mixed.Name'='version'\n")
			file, err := OpenFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			update, err := file.PrepareSetting(key, values[key])
			if err != nil || !update.WouldChange() || update.Previous.Present || !update.Next.Present || update.Next.Secret {
				t.Fatalf("new declaration has wrong prepared values: %+v %v", update, err)
			}
			if changed, err := update.Save(t.Context()); err != nil || !changed {
				t.Fatalf("validated scalar was not published: %t %v", changed, err)
			}
			loaded, err := Load(Options{Path: path, Environ: func() []string { return nil }})
			if err != nil || !maps.Equal(loaded.Config.Aliases, map[string]string{"Mixed.Name": "version"}) || loaded.Source(key) != LayerFile || loaded.Config.Values()[key] != update.Next.Value {
				t.Fatalf("publication lost aliases, type or provenance: %+v %v", loaded, err)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fields, _, err := decodeFile(contents)
			if err != nil || len(fields) != 2 {
				t.Fatalf("scalar save invented other declarations: %v %v", fields, err)
			}
			if key == "timeout" && fields[key] != "2m0s" {
				t.Fatal("duration was not stored canonically as TOML text")
			}
			if key == "tui_preview_size" {
				if value, ok := fields[key].(int64); !ok || value != 55 {
					t.Fatal("preview percentage lost its integer type")
				}
			}
			if key == "tui_preview_dock" && fields[key] != "bottom" {
				t.Fatal("preview dock lost its canonical string")
			}
			if key == "workday_seconds" {
				if value, ok := fields[key].(int64); !ok || value != 36000 {
					t.Fatal("workday scalar lost its integer type")
				}
			}
			if values[key] == "false" {
				if value, ok := fields[key].(bool); !ok || value {
					t.Fatal("boolean scalar was stored as a string or true")
				}
			}
		})
	}
}

func TestSettingUpdateNoopKeepsBytesMetadataAndExplicitZeroValues(t *testing.T) {
	t.Parallel()
	const authored = "# preserve spelling and comments\nread_only=false\ntimeout='120s'\nemail=''\n"
	path := aliasFixture(t, authored)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range []struct{ key, value string }{{"read_only", "false"}, {"timeout", "2m"}, {"email", ""}} {
		file, err := OpenFile(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		update, err := file.PrepareSetting(setting.key, setting.value)
		if err != nil || update.WouldChange() || !update.Previous.Present || update.Previous.Value != update.Next.Value {
			t.Fatalf("equal typed value fabricated a change: %+v %v", update, err)
		}
		if changed, err := update.Save(t.Context()); err != nil || changed {
			t.Fatalf("equal setting rewrote authored content: %t %v", changed, err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != authored {
		t.Fatal("no-op changed file bytes")
	}
	after, err := os.Stat(path)
	if err != nil || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("no-op changed metadata")
	}
}

func TestSettingUpdateAbsentEmptyAndFalseAreNewDeclarations(t *testing.T) {
	t.Parallel()
	for _, setting := range []struct{ key, value string }{{"read_only", "false"}, {"timeout", "0s"}, {"email", ""}} {
		path := filepath.Join(t.TempDir(), "not-created", "settings.toml")
		file, err := OpenFile(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		update, err := file.PrepareSetting(setting.key, setting.value)
		if err != nil || !update.WouldChange() || update.Previous.Present || !update.Next.Present {
			t.Fatalf("absent setting collapsed into a zero declaration: %+v %v", update, err)
		}
		if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("preparation created a directory")
		}
		if changed, err := update.Save(t.Context()); err != nil || !changed {
			t.Fatalf("explicit zero declaration was not saved: %t %v", changed, err)
		}
		stored, err := ReadFile(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		value, err := stored.Setting(setting.key)
		if err != nil || !value.Present || value.Value != setting.value {
			t.Fatalf("stored zero declaration was omitted: %+v %v", value, err)
		}
	}
}

func TestSettingUpdateRejectsInvalidInputsWithoutPublication(t *testing.T) {
	t.Parallel()
	for _, setting := range []struct{ key, value string }{
		{"api_token_env", "AWS_SECRET_ACCESS_KEY"},
		{"api_token", "synthetic-forbidden-setting"},
		{"aliases", "{}"},
		{"read_ony", "true"},
		{"read_only", "yes"},
		{"keyring", "maybe"},
		{"timeout", "-1s"},
		{"timeout", "one minute"},
		{"workday_seconds", "0"},
		{"workday_seconds", "2.5"},
		{"workday_seconds", "9223372036854775808"},
		{"glyphs", "bogus"},
		{"theme", "bogus"},
		{"base_url", "acme"},
		{"base_url", "   "},
	} {
		if err := ValidateSettingValue(setting.key, setting.value); err == nil {
			t.Fatalf("invalid input accepted before I/O: %s", setting.key)
		}
		path := aliasFixture(t, "email='original@example.invalid'\n")
		file, err := OpenFile(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.PrepareSetting(setting.key, setting.value); err == nil {
			t.Fatalf("invalid input accepted by independent owner: %s", setting.key)
		}
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != "email='original@example.invalid'\n" {
			t.Fatal("refused assignment changed the original")
		}
	}
}

func TestSettingUpdateRepairsMalformedSiteWithRedactedPreviousValue(t *testing.T) {
	t.Parallel()
	const siteSecret = "synthetic-setting-site"
	path := aliasFixture(t, "base_url='https://user:"+siteSecret+"@example.invalid/%zz'\nemail='keep@example.invalid'\n")
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.PrepareSetting("read_only", "true"); !errors.Is(err, ErrFileInvalid) {
		t.Fatal("an unrelated update kept an operationally invalid file")
	}
	update, err := file.PrepareSetting("base_url", "https://fixed.example.invalid")
	if err != nil || !update.Previous.Secret || strings.Contains(update.Previous.Value, siteSecret) || update.Next.Value != "https://fixed.example.invalid" {
		t.Fatalf("site repair lost safe before/after values: %+v %v", update, err)
	}
	if changed, err := update.Save(t.Context()); err != nil || !changed {
		t.Fatalf("site repair did not publish: %t %v", changed, err)
	}
}

func TestSettingUpdateCancellationAndSnapshotConflictRefusePublication(t *testing.T) {
	t.Parallel()
	path := aliasFixture(t, "read_only=false\n")
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	update, err := file.PrepareSetting("read_only", "true")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := update.Save(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("save lost cancellation: %v", err)
	}
	if err := os.WriteFile(path, []byte("email='intervening@example.invalid'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := update.Save(t.Context()); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("setting publication ignored the original snapshot: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "email='intervening@example.invalid'\n" {
		t.Fatal("setting publication replaced intervening content")
	}
}

func TestSettingUpdatePreparationKeepsSnapshotAndOtherAuthoredValues(t *testing.T) {
	t.Parallel()
	const token = "synthetic-retained-token"
	const original = "# authored\napi_token='" + token + "'\nread_only=true\nemail='keep@example.invalid'\n[aliases]\n'Mixed.Name'='version'\n"
	path := aliasFixture(t, original)
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := file.PrepareSetting("read_only", "false")
	if err != nil {
		t.Fatal(err)
	}
	second, err := file.PrepareSetting("email", "next@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if string(file.Contents()) != original {
		t.Fatal("preparation changed the immutable snapshot")
	}
	firstFields, _, err := decodeFile(first.candidate)
	if err != nil {
		t.Fatal(err)
	}
	secondFields, _, err := decodeFile(second.candidate)
	if err != nil {
		t.Fatal(err)
	}
	if firstFields["email"] != "keep@example.invalid" || firstFields["read_only"] != false || secondFields["email"] != "next@example.invalid" || secondFields["read_only"] != true {
		t.Fatal("independent candidates accumulated each other's changes")
	}
	if changed, err := first.Save(t.Context()); err != nil || !changed {
		t.Fatalf("first candidate did not publish: %t %v", changed, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, stored, err := decodeFile(contents)
	if err != nil || stored.APIToken != token || stored.Email != "keep@example.invalid" || !maps.Equal(stored.Aliases, map[string]string{"Mixed.Name": "version"}) {
		t.Fatal("scalar publication changed other authored values")
	}
	if _, err := second.Save(t.Context()); !errors.Is(err, ErrFileChanged) {
		t.Fatal("second candidate overwrote the first publication")
	}
}

func TestSettingUpdateRefusesInvalidUTF8InsteadOfChangingItsSpelling(t *testing.T) {
	t.Parallel()
	value := string([]byte{'x', 0xff, 'y'})
	if err := ValidateSettingValue("email", value); err == nil {
		t.Fatal("invalid UTF-8 scalar was accepted before publication")
	}
}

func TestConfigFileInvalidUTF8NeverPassesValidationOrSettingRead(t *testing.T) {
	t.Parallel()
	contents := []byte("email='x\xffy'\n")
	if err := ValidateFile(contents); !errors.Is(err, ErrFileInvalid) {
		t.Fatalf("invalid UTF-8 TOML was accepted for publication: %v", err)
	}
	path := aliasFixture(t, string(contents))
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Setting("email"); !errors.Is(err, ErrFileInvalid) {
		t.Fatalf("stored inspection silently changed invalid text: %v", err)
	}
	if _, err := file.PrepareSetting("read_only", "true"); !errors.Is(err, ErrFileInvalid) {
		t.Fatalf("setting update normalized invalid existing text: %v", err)
	}
}
