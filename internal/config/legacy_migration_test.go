package config_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
)

const legacySelectionFixture = `# Keep these exact bytes for rollback.
default_profile = 'work-staging'
queries_path = 'relative/queries'
editor = 'global-editor --wait'
[theme]
name = 'dracula'
path = 'palettes/colors.toml'
[aliases]
'my.issues' = 'issue list --jql "assignee = currentUser()"'
[[profiles]]
name = 'personal'
base_url = 'https://personal.invalid'
email = 'personal@example.invalid'
secret_backend = 'env'
timeout = 99
editor = 'personal-editor'
[[profiles]]
name = 'work-staging'
base_url = 'https://WORK.invalid/'
email = 'work@example.invalid'
secret_backend = '1password'
onepassword_account = 'Selected Account'
vault = 'Selected Vault'
item = ''
timeout = 0
workday_seconds = 25200
read_only = true
editor = 'profile-editor --wait'
`

func preparedLegacy(t *testing.T, data, profile string) *config.LegacyMigration {
	t.Helper()
	prepared, err := config.PrepareLegacyMigration(t.Context(), []byte(data), filepath.Join(t.TempDir(), "config.toml"), profile)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func legacyCandidate(t *testing.T, prepared *config.LegacyMigration) *config.Resolved {
	t.Helper()
	candidate, err := prepared.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, string(candidate))
	resolved, err := config.Load(config.Options{Path: path, Environ: func() []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestLegacyMigrationSelectsOneProfileAndRetainsItsCredentialIdentity(t *testing.T) {
	t.Parallel()
	sourcePath := filepath.Join(t.TempDir(), "source", "old.toml")
	prepared, err := config.PrepareLegacyMigration(t.Context(), []byte(legacySelectionFixture), sourcePath, "")
	if err != nil {
		t.Fatal(err)
	}
	selection := prepared.Selection()
	if selection.Name != "work-staging" || selection.Site != "https://work.invalid" || selection.Email != "work@example.invalid" || selection.Backend != core.CredentialBackendOnePassword {
		t.Fatal("legacy default selection or normalized binding changed")
	}
	if selection.KeyringName() != "WORK.invalid/work-staging" || selection.Item != "jira-cli-WORK.invalid-work-staging" || selection.EnvTokenKey() != "JIRA_TOKEN_WORK_STAGING" {
		t.Fatal("old source credential namespace was guessed from the new normalized site")
	}
	resolved := legacyCandidate(t, prepared)
	if resolved.Config.APITokenEnv != "JIRA_TOKEN_WORK_STAGING" || !slices.Contains(prepared.Mapped(), "api_token_env") {
		t.Fatal("migration lost the cross-backend historical token override")
	}
	if resolved.Config.BaseURL != "https://work.invalid" || resolved.Config.Email != "work@example.invalid" || !resolved.Config.ReadOnly || resolved.Config.Timeout != 30*time.Second || resolved.Config.WorkdaySeconds != 25200 {
		t.Fatal("selected preferences or old timeout fallback were lost")
	}
	if resolved.Config.SecretBackend != core.CredentialBackendOnePassword || resolved.Config.OnePasswordAccount != "Selected Account" || resolved.Config.OnePasswordVault != "Selected Vault" || resolved.Config.OnePasswordItem != selection.Item {
		t.Fatal("SDK coordinates or managed default item ownership changed")
	}
	if resolved.Config.Editor != "profile-editor --wait" || resolved.Config.QueriesPath != "relative/queries" || resolved.Config.Theme != "dracula" || resolved.Config.Aliases["my.issues"] != `issue list --jql "assignee = currentUser()"` {
		t.Fatal("global preferences, literal alias identity or profile editor override were lost")
	}
	if resolved.Config.ThemePath != filepath.Join(filepath.Dir(sourcePath), "palettes", "colors.toml") {
		t.Fatal("relative theme origin was lost during conversion")
	}
	if bytes.Contains(prepared.Original(), []byte("personal@example.invalid")) == false || !bytes.Equal(prepared.Original(), []byte(legacySelectionFixture)) {
		t.Fatal("unselected profile or exact recovery preimage was discarded")
	}
	personal := legacyCandidate(t, preparedLegacy(t, legacySelectionFixture, "personal"))
	if personal.Config.Email != "personal@example.invalid" || personal.Config.Timeout != 99*time.Second || personal.Config.SecretBackend != core.CredentialBackendEnvironment || personal.Config.Editor != "personal-editor" || personal.Config.ReadOnly {
		t.Fatal("explicit selection merged another profile's preferences")
	}
}

func TestLegacyMigrationPreservesOldDefaultsInsteadOfCurrentEnvironment(t *testing.T) {
	t.Setenv("JIRA_EMAIL", "environment@example.invalid")
	t.Setenv("JIRA_TIMEOUT", "900s")
	t.Setenv("JIRA_SECRET_BACKEND", "env")
	for _, suffix := range []string{"", "timeout=-1\nworkday_seconds=0\n", "timeout='40'\nworkday_seconds='36000'\n"} {
		prepared := preparedLegacy(t, "[[profiles]]\nname='default'\n"+suffix, "")
		resolved := legacyCandidate(t, prepared)
		wantTimeout, wantWorkday := 30*time.Second, 28800
		if strings.Contains(suffix, "'40'") {
			wantTimeout, wantWorkday = 40*time.Second, 36000
		}
		if resolved.Config.Email != "" || resolved.Config.Timeout != wantTimeout || resolved.Config.WorkdaySeconds != wantWorkday || resolved.Config.SecretBackend != core.CredentialBackendKeyring {
			t.Fatal("migration copied transient environment or new timeout default")
		}
	}
	for _, data := range []string{"", "profiles=[]\n", "profiles={}\n", "default_profile='default'\n"} {
		if legacyCandidate(t, preparedLegacy(t, data, "")).Config.Timeout != 30*time.Second {
			t.Fatal("old implicit default profile no longer prepares")
		}
	}
}

func TestLegacyMigrationUnmappedPreferencesBlockPartialPublication(t *testing.T) {
	t.Parallel()
	data := `default_profile='default'
[theme]
name='auto'
[tui]
icons='nerd'
tabs=['issues','epics']
[[profiles]]
name='default'
default_project='KEY'
default_issue_type='Task'
default_board='Selected Board'
refresh_interval=30
cloud_id='fixture-cloud'
team_account_ids=['fixture-team']
account_id='fixture-account'
`
	prepared := preparedLegacy(t, data, "")
	want := []string{"tui.icons", "tui.tabs"}
	if !slices.Equal(prepared.Unmapped(), want) {
		t.Fatalf("unmapped declarations disappeared: got=%v want=%v", prepared.Unmapped(), want)
	}
	if candidate, err := prepared.Candidate(); !errors.Is(err, config.ErrLegacyPreferencesUnmapped) || candidate != nil {
		t.Fatal("an incomplete conversion exposed a publishable candidate")
	}
	if !bytes.Equal(prepared.Original(), []byte(data)) {
		t.Fatal("unmapped values were not retained in the exact preimage")
	}
}

func TestLegacyMigrationPreparationOwnsCopiesAndPublishesNoRawConfiguration(t *testing.T) {
	t.Parallel()
	data := []byte(legacySelectionFixture)
	prepared, err := config.PrepareLegacyMigration(t.Context(), data, filepath.Join(t.TempDir(), "old.toml"), "")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if prepared.SourceSHA256() != hex.EncodeToString(digest[:]) {
		t.Fatal("source digest does not identify original bytes")
	}
	data[0] = '!'
	original := prepared.Original()
	original[0] = '!'
	candidate, err := prepared.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	candidate[0] = '!'
	mapped := prepared.Mapped()
	mapped[0] = "corrupt"
	selection := prepared.Selection()
	selection.Name = "changed"
	again, err := prepared.Candidate()
	if err != nil || again[0] == '!' || prepared.Original()[0] != '#' || prepared.Mapped()[0] == "corrupt" || prepared.Selection().Name != "work-staging" {
		t.Fatal("caller mutation changed a prepared migration")
	}
	encoded, err := json.Marshal(prepared) //nolint:staticcheck // SA9005: intentionally guard against exporting recovery state through JSON.
	if err != nil || string(encoded) != "{}" {
		t.Fatal("prepared internal state became a public configuration payload")
	}
}

func TestLegacyMigrationRejectsInvalidOrSecretBearingInputsWithoutDisclosure(t *testing.T) {
	t.Parallel()
	const private = "synthetic-private-material"
	for name, data := range map[string]string{
		"top-level token":      "api_token='" + private + "'\n[[profiles]]\nname='default'\n",
		"nested token":         "[[profiles]]\nname='default'\ntoken='" + private + "'\n",
		"unknown TUI":          "[tui]\nunknown='" + private + "'\n[[profiles]]\nname='default'\n",
		"invalid syntax":       "private='" + private + "\n",
		"private decode value": "[[profiles]]\nname='default'\ntimeout='" + private + "'\n",
		"duplicate profiles":   "[[profiles]]\nname='default'\n[[profiles]]\nname='default'\n",
		"missing default":      "default_profile='missing'\n[[profiles]]\nname='default'\n",
		"ambiguous schema":     "editor='first'\nEDITOR='second'\n",
		"Unicode folding":      "'querieſ_path'='private-path'\n",
		"bad backend":          "[[profiles]]\nname='default'\nsecret_backend='" + private + "'\n",
		"bad auth type":        "[[profiles]]\nname='default'\nauth_type='" + private + "'\n",
		"URL credentials":      "[[profiles]]\nname='default'\nbase_url='https://person:" + private + "@example.invalid'\n",
		"remote HTTP":          "[[profiles]]\nname='default'\nbase_url='http://example.invalid'\n",
		"unsafe namespace":     "[[profiles]]\nname='default'\n[[profiles]]\nname='unsafe/name'\n",
		"invalid cloud":        "[[profiles]]\nname='default'\ncloud_id='unsafe/path'\n",
		"overflow timeout":     "[[profiles]]\nname='default'\ntimeout=9223372036854775807\n",
		"bad UTF8":             "[[profiles]]\nname='default'\n#\xff",
	} {
		t.Run(name, func(t *testing.T) {
			profile := ""
			if name == "unsafe namespace" {
				profile = "unsafe/name"
			}
			prepared, err := config.PrepareLegacyMigration(t.Context(), []byte(data), "", profile)
			if prepared != nil || !errors.Is(err, config.ErrLegacyConfig) || strings.Contains(err.Error(), private) {
				t.Fatal("invalid input prepared a migration or disclosed private material")
			}
			if name == "invalid syntax" && errors.Unwrap(err) == nil {
				t.Fatal("sanitization discarded the underlying parser failure")
			}
		})
	}
}

func TestLegacyMigrationCancellationAndNamespaceEncoding(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if prepared, err := config.PrepareLegacyMigration(ctx, []byte("not even TOML"), "", ""); prepared != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled preparation decoded input")
	}
	for name, want := range map[string]string{"work-staging": "JIRA_TOKEN_WORK_STAGING", "work_staging": "JIRA_TOKEN_WORK__STAGING", "work_-a": "JIRA_TOKEN_WORK___A"} {
		if got := (config.LegacySelection{Name: name}).EnvTokenKey(); got != want {
			t.Fatalf("historical namespace encoding changed: got=%s want=%s", got, want)
		}
	}
}

func TestLegacyMigrationPreservesDeclaredEmptyAndDormantSDKPreferences(t *testing.T) {
	t.Parallel()
	prepared := preparedLegacy(t, "editor=''\n[aliases]\n[theme]\nname=''\npath=''\n[[profiles]]\nname='default'\nsecret_backend='keyring'\nonepassword_account='Selected Account'\nvault='Selected Vault'\nitem='Pinned Item'\n", "")
	resolved := legacyCandidate(t, prepared)
	for _, key := range []string{"aliases", "editor", "theme", "theme_path", "onepassword_account", "onepassword_vault", "onepassword_item"} {
		if resolved.Source(key) != config.LayerFile {
			t.Fatalf("authored declaration became a default: %s", key)
		}
	}
	if resolved.Config.OnePasswordAccount != "Selected Account" || resolved.Config.OnePasswordVault != "Selected Vault" || resolved.Config.OnePasswordItem != "Pinned Item" || resolved.Config.SecretBackend != core.CredentialBackendKeyring {
		t.Fatal("dormant SDK coordinates changed the selected backend or were discarded")
	}
}

func TestLegacyMigrationDoesNotGuessExpansionOrThemeOrigin(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"~/palette.toml", "$PALETTE/palette.toml"} {
		data := "[theme]\npath='" + path + "'\n[[profiles]]\nname='default'\n"
		prepared, err := config.PrepareLegacyMigration(t.Context(), []byte(data), "", "")
		if prepared != nil || !errors.Is(err, config.ErrLegacyConfig) {
			t.Fatal("literal relative theme path guessed an unavailable source origin")
		}
	}
	prepared := preparedLegacy(t, "queries_path='$QUERY_DIRECTORY'\n[[profiles]]\nname='default'\n", "")
	if len(prepared.Unmapped()) != 0 || legacyCandidate(t, prepared).Config.QueriesPath != "$QUERY_DIRECTORY" {
		t.Fatal("existing query expansion was resolved or refused during preparation")
	}
	if migration, err := config.PrepareLegacyMigration(t.Context(), []byte("[theme]\npath='relative.toml'\n[[profiles]]\nname='default'\n"), "", ""); migration != nil || !errors.Is(err, config.ErrLegacyConfig) {
		t.Fatal("relative theme origin was guessed from the process directory")
	}
	if candidate, err := new(config.LegacyMigration).Candidate(); candidate != nil || !errors.Is(err, config.ErrLegacyConfig) {
		t.Fatal("zero preparation produced a publishable candidate")
	}
}

func TestLegacyMigrationTracksWeakShapesAndCaseInsensitiveDeclarations(t *testing.T) {
	t.Parallel()
	for _, data := range []string{
		"[THEME]\nPATH='$LEGACY_THEME/palette.toml'\n[[profiles]]\nname='default'\n",
		"[TUI]\nICONS='nerd'\n[[profiles]]\nname='default'\n",
	} {
		prepared := preparedLegacy(t, data, "")
		if strings.HasPrefix(data, "[THEME]") {
			if len(prepared.Unmapped()) != 0 || !strings.HasSuffix(legacyCandidate(t, prepared).Config.ThemePath, filepath.Join("$LEGACY_THEME", "palette.toml")) {
				t.Fatal("case-insensitive literal theme path was discarded or expanded")
			}
			continue
		}
		if len(prepared.Unmapped()) != 1 {
			t.Fatal("accepted declaration shape bypassed unmapped tracking")
		}
		if candidate, err := prepared.Candidate(); candidate != nil || !errors.Is(err, config.ErrLegacyPreferencesUnmapped) {
			t.Fatal("weak decoding exposed a partial candidate")
		}
	}
	prepared := preparedLegacy(t, "Editor='global-editor'\n[Aliases]\n'Mixed.Alias'='issue list'\n[THEME]\nNAME='dracula'\n[profiles]\nName='default'\nEditor='profile-editor'\nVault='Selected Vault'\n", "")
	resolved := legacyCandidate(t, prepared)
	if resolved.Config.Editor != "profile-editor" || resolved.Config.Theme != "dracula" || resolved.Config.Aliases["Mixed.Alias"] != "issue list" || resolved.Config.OnePasswordVault != "Selected Vault" {
		t.Fatal("case-insensitive schema keys or literal user keys were lost")
	}
}

func TestLegacyMigrationUsesHistoricalSingleLabelCredentialHosts(t *testing.T) {
	t.Parallel()
	for baseURL, host := range map[string]string{
		"https://jira/":              "jira.atlassian.net",
		"https://JIRA:8443/":         "JIRA.atlassian.net",
		"https://JIRA.invalid:8443/": "JIRA.invalid:8443",
		"http://localhost:8080/":     "localhost:8080",
		"http://[::1]:8080/":         "[::1]:8080",
		"https://[2001:db8::1]/":     "https://2001:db8::1.atlassian.net",
	} {
		prepared := preparedLegacy(t, "[[profiles]]\nname='default'\nbase_url='"+baseURL+"'\n", "")
		selection := prepared.Selection()
		if selection.Host != host || selection.KeyringName() != host+"/default" || selection.Item != "jira-cli-"+host+"-default" {
			t.Fatal("historical credential namespace or SDK title changed")
		}
	}
}

func TestLegacyMigrationPreservesEffectiveEditorPrecedence(t *testing.T) {
	t.Parallel()
	for profileEditor, want := range map[string]string{
		"":                          "global-editor --wait",
		"   ":                       "global-editor --wait",
		"  profile-editor --wait  ": "profile-editor --wait",
	} {
		data := "editor='  global-editor --wait  '\n[[profiles]]\nname='default'\neditor='" + profileEditor + "'\n"
		if legacyCandidate(t, preparedLegacy(t, data, "")).Config.Editor != want {
			t.Fatal("whitespace-only profile editor suppressed the global editor")
		}
	}
}
