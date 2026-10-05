package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

const profileFoundation = `format_version=1
default_profile='work'
editor='global-editor'
[profiles.work]
base_url='https://work.example.invalid'
email='work@example.invalid'
credential_id='6261bdc7-c460-4d6a-9b67-d3d5f78fc627'
read_only=true
timeout='30s'
editor='work-editor'
[profiles.personal]
base_url='https://personal.example.invalid'
email='personal@example.invalid'
credential_id='de25b949-7427-45db-9e29-fe27de9d0ef7'
read_only=false
`

type foundationStore struct {
	records map[string]string
	reads   int
}

func (s *foundationStore) Get(key string) (string, error) {
	s.reads++
	if value, ok := s.records[key]; ok {
		return value, nil
	}
	return "", core.ErrNoCredential
}
func (s *foundationStore) Set(key, value string) error { s.records[key] = value; return nil }
func (s *foundationStore) Delete(key string) error     { delete(s.records, key); return nil }

func foundationConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProfileFoundationSelectionAndIndependentSettings(t *testing.T) {
	for _, tc := range []struct{ name, option, flag, env, want string }{
		{"default", "", "", "", "work"},
		{"environment", "", "", "personal", "personal"},
		{"flag", "", "work", "personal", "work"},
		{"option", "personal", "work", "", "personal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config.SetFlagLayer(config.FlagLayer{Profile: tc.flag, ProfileSet: tc.flag != ""})
			t.Cleanup(func() { config.SetFlagLayer(config.FlagLayer{}) })
			path := foundationConfig(t, profileFoundation)
			loaded, err := config.Load(config.Options{Path: path, Profile: tc.option, Environ: func() []string {
				variables := []string{"JIRA_READ_ONLY=false", "JIRA_TIMEOUT=0s"}
				if tc.env != "" {
					variables = append(variables, "JIRA_PROFILE="+tc.env)
				}
				return variables
			}})
			if err != nil {
				t.Fatal(err)
			}
			if !loaded.NamedProfiles || loaded.Profile != tc.want || loaded.DefaultProfile != "work" || loaded.Config.ReadOnly || loaded.Config.Timeout != 0 {
				t.Fatalf("selection changed: %+v", loaded)
			}
		})
	}
	path := foundationConfig(t, profileFoundation)
	file, err := config.OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	file, err = file.SelectProfile("personal")
	if err != nil {
		t.Fatal(err)
	}
	update, err := file.PrepareSetting("read_only", "true")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := update.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"work", "personal"} {
		loaded, err := config.Load(config.Options{Path: path, Profile: name, Environ: func() []string { return nil }})
		if err != nil || !loaded.Config.ReadOnly {
			t.Fatalf("profile publication %s: %v", name, err)
		}
		want := "global-editor"
		if name == "work" {
			want = "work-editor"
		}
		if loaded.Config.Editor != want {
			t.Fatal("profile editor inheritance changed")
		}
	}
}

func TestProfileFoundationStoredSlotsAndBinding(t *testing.T) {
	t.Parallel()
	path := foundationConfig(t, profileFoundation)
	store := &foundationStore{records: map[string]string{}}
	for _, identity := range []struct{ slot, site, email, token string }{
		{"6261bdc7-c460-4d6a-9b67-d3d5f78fc627", "https://work.example.invalid", "work@example.invalid", "work-token"},
		{"de25b949-7427-45db-9e29-fe27de9d0ef7", "https://personal.example.invalid", "personal@example.invalid", "personal-token"},
		{"default", "https://flat.example.invalid", "flat@example.invalid", "flat-token"},
	} {
		if err := credentials.Save(credentials.Select(store, identity.slot), credentials.Record{Site: identity.site, Email: identity.email, Token: identity.token}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"work", "personal"} {
		loaded, err := config.Load(config.Options{Path: path, Profile: name, Credentials: store, Environ: func() []string { return nil }})
		if err != nil || loaded.Config.APIToken != name+"-token" {
			t.Fatalf("slot selection %s: %v", name, err)
		}
	}
	// Renaming only the profile identity retains its existing verified tuple.
	data := strings.ReplaceAll(profileFoundation, "default_profile='work'", "default_profile='office'")
	data = strings.ReplaceAll(data, "[profiles.work]", "[profiles.office]")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(config.Options{Path: path, Profile: "office", Credentials: store, Environ: func() []string { return nil }})
	if err != nil || loaded.Config.APIToken != "work-token" {
		t.Fatal("rename lost credential", err)
	}
	if err := credentials.Save(credentials.Select(store, "6261bdc7-c460-4d6a-9b67-d3d5f78fc627"), credentials.Record{Site: "https://other.example.invalid", Email: "work@example.invalid", Token: "other-token"}); err != nil {
		t.Fatal(err)
	}
	loaded, err = config.Load(config.Options{Path: path, Profile: "office", Credentials: store, Environ: func() []string { return nil }})
	if err != nil || loaded.Config.APIToken != "" || loaded.Config.BaseURL != "https://work.example.invalid" || len(loaded.Warnings) != 1 {
		t.Fatal("stored metadata replaced authored identity", err)
	}
	if _, err := credentials.Load(store); err != nil {
		t.Fatal("flat credential changed", err)
	}
}

func TestProfileFoundationInvalidOrUnselectedCannotReadCredentials(t *testing.T) {
	t.Parallel()
	for name, data := range map[string]string{
		"unknown key":             profileFoundation + "misspelled=true\n",
		"invalid unselected site": strings.ReplaceAll(profileFoundation, "https://personal.example.invalid", "invalid"),
		"duplicate slot":          strings.ReplaceAll(profileFoundation, "de25b949-7427-45db-9e29-fe27de9d0ef7", "6261bdc7-c460-4d6a-9b67-d3d5f78fc627"),
		"invalid flat slot":       "credential_id='invalid'\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := &foundationStore{records: map[string]string{}}
			_, err := config.Load(config.Options{Path: foundationConfig(t, data), Credentials: store, Environ: func() []string { return nil }})
			if err == nil || store.reads != 0 {
				t.Fatal("invalid declarations reached provider")
			}
		})
	}
	for _, data := range []string{"format_version=1\ndefault_profile=''\n[profiles]\n", strings.ReplaceAll(profileFoundation, "credential_id='6261bdc7-c460-4d6a-9b67-d3d5f78fc627'\n", "")} {
		store := &foundationStore{records: map[string]string{}}
		loaded, err := config.Load(config.Options{Path: foundationConfig(t, data), Credentials: store, Environ: func() []string { return nil }})
		if err != nil || !loaded.NamedProfiles || store.reads != 0 {
			t.Fatal("missing named locator reached flat provider", err)
		}
	}
	if err := credentials.Save(credentials.UnselectedStore{}, credentials.Record{Token: "synthetic-token"}); err == nil || errors.Is(err, core.ErrNoCredential) {
		t.Fatal("missing named locator allowed write")
	}
}

func TestProfileFoundationManagementKeepsDurableLocators(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	open := func() *config.File {
		t.Helper()
		file, err := config.OpenFile(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		return file
	}
	first, err := open().PrepareProfileAdd("work", "https://work.example.invalid", "work@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preparation created settings")
	}
	if saved, err := first.Save(t.Context()); err != nil || !saved {
		t.Fatal("first publication", err)
	}
	profiles, err := open().Profiles()
	if err != nil || len(profiles) != 1 || profiles[0].CredentialID == "" || !profiles[0].Default {
		t.Fatal("missing durable locator", err)
	}
	id := profiles[0].CredentialID
	second, err := open().PrepareProfileAdd("personal", "https://personal.example.invalid", "personal@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	rename, err := open().PrepareProfileRename("work", "office")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rename.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	profiles, err = open().Profiles()
	if err != nil || len(profiles) != 2 || profiles[0].Name != "office" || profiles[0].CredentialID != id || !profiles[0].Default {
		t.Fatal("rename changed locator or default", err)
	}
	if _, err := open().PrepareProfileRemove("office"); !errors.Is(err, config.ErrFileInvalid) {
		t.Fatal("removed default while another profile remains", err)
	}
	changeDefault, err := open().PrepareProfileDefault("personal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := changeDefault.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	remove, err := open().PrepareProfileRemove("office")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remove.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	profiles, err = open().Profiles()
	if err != nil || len(profiles) != 1 || profiles[0].Name != "personal" || !profiles[0].Default {
		t.Fatal("remove affected remaining profile", err)
	}
}

func TestProfileFoundationConversionPreservesFlatLocation(t *testing.T) {
	t.Parallel()
	path := foundationConfig(t, "base_url='https://flat.example.invalid'\nemail='flat@example.invalid'\neditor='shared-editor'\n")
	file, err := config.OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	update, err := file.PrepareProfileAdd("second", "https://second.example.invalid", "second@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := update.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(config.Options{Path: path, Profile: "default", Environ: func() []string { return nil }})
	if err != nil || loaded.Config.CredentialID != "default" || loaded.Config.BaseURL != "https://flat.example.invalid" || loaded.Config.Editor != "shared-editor" {
		t.Fatal("flat conversion changed identity", err)
	}
	file, err = config.OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	file, err = file.SelectProfile("second")
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := file.PrepareCredentialID()
	if err != nil || reservation.WouldChange() {
		t.Fatal("reservation replaced existing locator", err)
	}
}
