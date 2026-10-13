package config_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/credentials"
)

const namedConfig = `format_version = 1
default_profile = 'work'
theme = 'dark'
editor = 'global-editor'
[aliases]
'Mixed.Name' = 'version'
[profiles.work]
base_url = 'https://work.example.invalid'
email = 'work@example.invalid'
credential_id = 'b6ff8a17-9f47-4e25-83b8-cf3f38466700'
read_only = true
timeout = '30s'
default_project = 'DEMO'
editor = 'work-editor'
[profiles.personal]
base_url = 'https://personal.example.invalid'
email = 'personal@example.invalid'
credential_id = '2a3d34d2-52a8-4dfb-b0da-0d6ff5d22428'
read_only = false
`

func TestNamedProfileSelectionPrecedenceAndEnvironmentCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, option, flag, environment, want string
		flagSet                               bool
	}{
		{name: "saved default", want: "work"},
		{name: "environment", environment: "personal", want: "personal"},
		{name: "flag", flag: "work", flagSet: true, environment: "personal", want: "work"},
		{name: "explicit option", option: "personal", flag: "work", flagSet: true, want: "personal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config.SetFlagLayer(config.FlagLayer{Profile: tc.flag, ProfileSet: tc.flagSet})
			t.Cleanup(func() { config.SetFlagLayer(config.FlagLayer{}) })
			path := writeConfig(t, namedConfig)
			loaded, err := config.Load(config.Options{Path: path, Profile: tc.option, Environ: func() []string {
				variables := []string{"JIRA_READ_ONLY=false", "JIRA_API_TOKEN=synthetic-token"}
				if tc.environment != "" {
					variables = append(variables, "JIRA_PROFILE="+tc.environment)
				}
				return variables
			}})
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Profile != tc.want || loaded.DefaultProfile != "work" || loaded.Config.ReadOnly || loaded.Source("read_only") != config.LayerEnv || loaded.Config.APIToken != "synthetic-token" {
				t.Fatal("selection or existing environment precedence changed")
			}
			wantEditor := "global-editor"
			if tc.want == "work" {
				wantEditor = "work-editor"
			}
			if loaded.Config.Editor != wantEditor || loaded.Config.Theme != "dark" || loaded.Config.Aliases["Mixed.Name"] != "version" {
				t.Fatal("global settings or literal aliases lost during profile selection")
			}
		})
	}
}

func TestNamedProfileValidationCoversUnselectedDeclarations(t *testing.T) {
	t.Parallel()
	for name, data := range map[string]string{
		"unknown key":            namedConfig + "read_ony=true\n",
		"wrong type":             namedConfig + "timeout=false\n",
		"bad site":               strings.Replace(namedConfig, "https://personal.example.invalid", "not a URL", 1),
		"bad credential ID":      strings.Replace(namedConfig, "2a3d34d2-52a8-4dfb-b0da-0d6ff5d22428", "not-an-id", 1),
		"duplicate credential":   strings.Replace(namedConfig, "2a3d34d2-52a8-4dfb-b0da-0d6ff5d22428", "b6ff8a17-9f47-4e25-83b8-cf3f38466700", 1),
		"unknown default":        strings.Replace(namedConfig, "default_profile = 'work'", "default_profile = 'missing'", 1),
		"unknown version":        strings.Replace(namedConfig, "format_version = 1", "format_version = 2", 1),
		"floating version":       strings.Replace(namedConfig, "format_version = 1", "format_version = 1.0", 1),
		"secret":                 namedConfig + "api_token=''\n",
		"case alias":             namedConfig + "EMAIL='other@example.invalid'\n",
		"global in profile":      namedConfig + "theme='light'\n",
		"SDK location collision": strings.ReplaceAll(strings.ReplaceAll(namedConfig, "credential_id = 'b6ff8a17-9f47-4e25-83b8-cf3f38466700'", "secret_backend='1password'\nonepassword_vault='Fixture Vault'\nonepassword_item='Fixture Item'"), "credential_id = '2a3d34d2-52a8-4dfb-b0da-0d6ff5d22428'", "secret_backend='1password'\nonepassword_vault='Fixture Vault'\nonepassword_item='Fixture Item'"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := &tokenReferenceStore{}
			_, err := config.Resolve(t.Context(), config.Options{Path: writeConfig(t, data), Profile: "work", Environ: func() []string {
				return []string{"JIRA_BASE_URL=https://override.example.invalid", "JIRA_API_TOKEN=synthetic"}
			}}, store)
			if err == nil || store.reads != 0 {
				t.Fatal("invalid unselected profile was masked or read credentials", err)
			}
		})
	}
}

func TestNamedProfileStoredIdentityCannotReplaceAuthoredSelection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, site, email string }{
		{name: "site", site: "https://other.example.invalid", email: "work@example.invalid"},
		{name: "account", site: "https://work.example.invalid", email: "other@example.invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(credentials.Record{Version: 2, Site: tc.site, Email: tc.email, Token: "synthetic-stored"})
			if err != nil {
				t.Fatal(err)
			}
			store := &tokenReferenceStore{raw: string(raw)}
			loaded, err := config.Resolve(t.Context(), config.Options{Path: writeConfig(t, namedConfig), Environ: func() []string { return nil }}, store)
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Config.APIToken != "" || loaded.Config.BaseURL != "https://work.example.invalid" || loaded.Config.Email != "work@example.invalid" || len(loaded.Warnings) != 1 {
				t.Fatal("stored tuple overwrote selected profile identity")
			}
		})
	}
}

func TestNamedSettingsPreserveOtherProfilesAndUseCapturedSelection(t *testing.T) {
	t.Setenv("JIRA_PROFILE", "personal")
	path := writeConfig(t, namedConfig)
	file, err := config.OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("JIRA_PROFILE", "work")
	setting, err := file.Setting("read_only")
	if err != nil || !setting.Present || setting.Value != "false" {
		t.Fatal("file selection was not captured", err)
	}
	update, err := file.PrepareSettings(map[string]string{"email": "", "timeout": "0s", "read_only": "false", "editor": "personal-editor", "theme": "light"})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := update.Save(t.Context()); err != nil || !changed {
		t.Fatal(err)
	}
	personal, err := config.Load(config.Options{Path: path, Profile: "personal", Environ: func() []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	work, err := config.Load(config.Options{Path: path, Profile: "work", Environ: func() []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if personal.Config.Email != "" || personal.Config.Timeout != 0 || personal.Config.ReadOnly || personal.Config.Editor != "personal-editor" || work.Config.Email != "work@example.invalid" || !work.Config.ReadOnly || work.Config.Editor != "work-editor" || work.Config.Theme != "light" || work.Config.Aliases["Mixed.Name"] != "version" {
		t.Fatal("settings update lost profile boundaries or explicit zero values")
	}
	stale, err := file.PrepareSetting("email", "another@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stale.Save(t.Context()); !errors.Is(err, config.ErrFileChanged) {
		t.Fatal("stale profile update replaced new document", err)
	}
}

func TestProfileManagementKeepsCredentialLocatorAndDefault(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	file, err := config.OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	add, err := file.PrepareProfileAdd("work", "https://work.example.invalid", "work@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preview created configuration")
	}
	if changed, err := add.Save(t.Context()); err != nil || !changed {
		t.Fatal(err)
	}
	file, err = config.OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := file.Profiles()
	if err != nil || len(profiles) != 1 || profiles[0].Name != "work" || !profiles[0].Default || profiles[0].CredentialID == "" || profiles[0].CredentialID == "default" {
		t.Fatal("first profile did not persist independent locator", err)
	}
	id := profiles[0].CredentialID
	rename, err := file.PrepareProfileRename("work", "renamed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rename.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	file, err = config.OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err = file.Profiles()
	if err != nil || profiles[0].Name != "renamed" || profiles[0].CredentialID != id || !profiles[0].Default {
		t.Fatal("rename changed credential identity", err)
	}
	remove, err := file.PrepareProfileRemove("renamed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remove.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
	if err != nil || loaded.Profile != "" || loaded.Config.Email != "" {
		t.Fatal("last removal retained selected identity", err)
	}
}

func TestFlatToNamedProfilesPreservesAuthoredSettingsAndDefaultSlot(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "base_url='https://old.example.invalid'\nemail='old@example.invalid'\nread_only=false\ntheme='dark'\n[aliases]\n'Mixed.Name'='version'\n")
	file, err := config.OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	add, err := file.PrepareProfileAdd("new", "https://new.example.invalid", "new@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := add.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
	if err != nil || loaded.Profile != "default" || loaded.Config.CredentialID != "default" || loaded.Config.BaseURL != "https://old.example.invalid" || loaded.Config.Aliases["Mixed.Name"] != "version" {
		t.Fatal("flat conversion changed existing identity or locator", err)
	}
}

func TestCapturedConfigurationRetainsFileAndEnvironmentAcrossRebuilds(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, namedConfig)
	variables := []string{"JIRA_PROFILE=personal", "JIRA_EMAIL=captured@example.invalid"}
	opts, err := config.CaptureOptions(t.Context(), config.Options{Path: path, Environ: func() []string { return variables }})
	if err != nil {
		t.Fatal(err)
	}
	variables[0], variables[1] = "JIRA_PROFILE=work", "JIRA_EMAIL=changed@example.invalid"
	if err := os.WriteFile(path, []byte("unknown_key=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(opts)
	if err != nil || loaded.Profile != "personal" || loaded.Config.Email != "captured@example.invalid" || loaded.Config.BaseURL != "https://personal.example.invalid" {
		t.Fatal("captured invocation reread external state", err)
	}
}

func TestUnknownNamedSelectionAndEmptyFlagRefuse(t *testing.T) {
	path := writeConfig(t, namedConfig)
	for _, name := range []string{"missing", "bad.name"} {
		if _, err := config.Load(config.Options{Path: path, Profile: name, Environ: func() []string { return nil }}); err == nil {
			t.Fatal("unknown profile accepted")
		}
	}
	config.SetFlagLayer(config.FlagLayer{ProfileSet: true})
	t.Cleanup(func() { config.SetFlagLayer(config.FlagLayer{}) })
	if _, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }}); err == nil {
		t.Fatal("empty explicit selector became default")
	}
}

func TestExplicitEmptyProfileEnvironmentRefusesBeforeProvider(t *testing.T) {
	path := writeConfig(t, namedConfig)
	_, err := config.Load(config.Options{Path: path, Environ: func() []string { return []string{"JIRA_PROFILE="} }})
	if !errors.Is(err, config.ErrFileInvalid) {
		t.Fatalf("empty explicit profile must be invalid configuration: %v", err)
	}
}
