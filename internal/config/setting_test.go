package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoredSettingInspectsOnlyDeclaredValuesAndRedactsBeforeReturning(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	const siteSecret = "synthetic-password"
	const original = "read_only=false\ntimeout='0s'\nemail=''\napi_token='synthetic-stored-secret'\nbase_url='https://user:" + siteSecret + "@example.invalid'\n[aliases]\n'Mixed.Name'='version'\n"
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := ReadFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key, value      string
		present, secret bool
	}{{"read_only", "false", true, false}, {"timeout", "0s", true, false}, {"email", "", true, false}, {"keyring", "", false, false}, {"workday_seconds", "", false, false}, {"api_token", "(set)", true, true}, {"aliases", `{"Mixed.Name":"version"}`, true, false}} {
		setting, err := file.Setting(tc.key)
		if err != nil || setting.Key != tc.key || setting.Value != tc.value || setting.Present != tc.present || setting.Secret != tc.secret {
			t.Fatalf("stored %s has wrong declaration: %+v, %v", tc.key, setting, err)
		}
	}
	site, err := file.Setting("base_url")
	if err != nil || !site.Present || !site.Secret || strings.Contains(site.Value, siteSecret) {
		t.Fatalf("stored site was not safely inspectable: %+v %v", site, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != original {
		t.Fatalf("stored inspection altered authored content: %q %v", contents, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode() != before.Mode() || !info.ModTime().Equal(before.ModTime()) {
		t.Fatalf("stored inspection changed permissions: %v", err)
	}
}

func TestStoredSettingRejectsUnknownKeysAndMalformedDeclaredTypes(t *testing.T) {
	t.Parallel()
	for _, content := range []string{"unknown=true\n", "read_only=1\n", "glyphs='bogus'\n", "[email]\nvalue='x'\n", "aliases='version'\n", "read_only=[\n"} {
		path := filepath.Join(t.TempDir(), "settings.toml")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := OpenFile(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Setting("read_only"); !errors.Is(err, ErrFileInvalid) {
			t.Fatalf("malformed stored file accepted: %q %v", content, err)
		}
	}
	file, err := OpenFile(t.Context(), filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Setting("read_ony"); err == nil {
		t.Fatal("unknown key accepted as an absent supported setting")
	}
}

func TestReadFileRequiresExplicitTargetAndPreservesCancellation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "absent.toml")
	if _, err := ReadFile(t.Context(), path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing explicit read target silently replaced: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ReadFile(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("read file lost cancellation cause: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("read path created the target: %v", err)
	}
}

func TestStoredSettingRedactsMalformedSiteURLsBeforeReturning(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	const secret = "synthetic-malformed-site-password"
	if err := os.WriteFile(path, []byte("base_url='https://user:"+secret+"@example.invalid/%zz'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := ReadFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	setting, err := file.Setting("base_url")
	if err != nil || !setting.Present || !setting.Secret || strings.Contains(setting.Value, secret) {
		t.Fatalf("malformed site credentials escaped stored inspection: %+v %v", setting, err)
	}
}
