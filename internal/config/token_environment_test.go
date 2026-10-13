package config_test

import (
	"context"
	"errors"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
)

type tokenReferenceStore struct {
	core.NoCredentialStore
	reads     int
	raw       string
	afterRead func()
}

func (s *tokenReferenceStore) Get(context.Context, string) (string, error) {
	s.reads++
	if s.afterRead != nil {
		s.afterRead()
	}
	if s.raw != "" {
		return s.raw, nil
	}
	return "", errors.New("synthetic native read failure")
}

func TestTokenEnvironmentPrecedenceAndFallback(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		variables []string
		token     string
		reads     int
		stored    bool
	}{
		{"generic priority", []string{"JIRA_API_TOKEN=direct", "JIRA_TOKEN_WORK=named"}, "direct", 1, true},
		{"named", []string{"JIRA_TOKEN_WORK=named"}, "named", 0, false},
		{"missing", []string{"JIRA_TOKEN_OTHER=other"}, "STORED-SECRET", 1, true},
		{"empty", []string{"JIRA_TOKEN_WORK="}, "STORED-SECRET", 1, true},
		{"empty generic", []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_WORK=named"}, "named", 0, false},
		{"duplicate named", []string{"JIRA_TOKEN_WORK=first", "JIRA_TOKEN_WORK=last", "JIRA_TOKEN_WORK="}, "last", 0, false},
		{"duplicate generic", []string{"JIRA_API_TOKEN=first", "JIRA_API_TOKEN=last", "JIRA_API_TOKEN=", "JIRA_TOKEN_WORK=named"}, "last", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, "base_url='https://file.invalid'\nemail='file@example.invalid'\napi_token_env='JIRA_TOKEN_WORK'\n")
			store := &tokenReferenceStore{raw: storedForCompany}
			resolved, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { return tc.variables }}, store)
			if err != nil || resolved.Config.APIToken != tc.token || store.reads != tc.reads {
				t.Fatal("incorrect token priority or native fallback", err)
			}
			wantSite, wantEmail := "https://file.invalid", "file@example.invalid"
			if tc.stored {
				wantSite, wantEmail = "https://company.atlassian.net", "person@company.example"
			}
			if resolved.Config.BaseURL != wantSite || resolved.Config.Email != wantEmail {
				t.Fatal("token used an unrelated identity")
			}
			wantSource := config.LayerEnv
			if tc.token == "STORED-SECRET" {
				wantSource = config.LayerKeyring
			}
			if resolved.Source("api_token") != wantSource {
				t.Fatal("token provenance lost")
			}
		})
	}
}

func TestTokenEnvironmentNamesRespectPlatformPriority(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reference         string
		variables               []string
		unixToken, windowsToken string
		unixReads, windowsReads int
	}{
		{"lowercase generic", "JIRA_TOKEN_WORK", []string{"jira_api_token=direct", "JIRA_TOKEN_WORK=named"}, "named", "direct", 0, 1},
		{"generic reference", "JIRA_API_TOKEN", []string{"jira_api_token=direct"}, "STORED-SECRET", "direct", 1, 1},
		{"mixed named", "JIRA_TOKEN_WORK", []string{"JiRa_ToKeN_WoRk=named"}, "STORED-SECRET", "named", 1, 0},
		{"mixed selector", "", []string{"JiRa_ApI_ToKeN_EnV=JIRA_TOKEN_WORK", "JIRA_TOKEN_WORK=named"}, "STORED-SECRET", "named", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, "api_token_env='"+tc.reference+"'\n")
			store := &tokenReferenceStore{raw: storedForCompany}
			variables := slices.Clone(tc.variables)
			resolved, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { return variables }}, store)
			token, reads := tc.unixToken, tc.unixReads
			if runtime.GOOS == "windows" {
				token, reads = tc.windowsToken, tc.windowsReads
			}
			if err != nil || resolved.Config.APIToken != token || store.reads != reads || !slices.Equal(variables, tc.variables) {
				t.Fatal("platform name handling changed precedence, values or caller snapshot", err)
			}
		})
	}
}

func TestTokenEnvironmentSnapshotSurvivesNativeCallbacksAndRebuild(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "api_token_env='JIRA_TOKEN_WORK'\n")
	variables := []string{"JIRA_API_TOKEN=original", "JIRA_BASE_URL=https://other.invalid", "JIRA_EMAIL=other@example.invalid"}
	calls := 0
	store := &tokenReferenceStore{raw: storedForCompany, afterRead: func() { variables[0] = "JIRA_API_TOKEN=replaced" }}
	resolved, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { calls++; return variables }}, store)
	if err != nil || calls != 1 || store.reads != 1 || resolved.Config.APIToken != "original" || resolved.Config.BaseURL != "https://other.invalid" || resolved.Config.Email != "other@example.invalid" || len(resolved.Warnings) != 1 {
		t.Fatal("rebuild reread or aliased the captured environment", err)
	}
}

func TestTokenEnvironmentSelectorValidationCannotBeMaskedAndCanBeRepaired(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "# repair public metadata\napi_token_env='AWS_SECRET_ACCESS_KEY'\n")
	store := &tokenReferenceStore{}
	_, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { return []string{"JIRA_API_TOKEN_ENV=JIRA_TOKEN_WORK", "JIRA_TOKEN_WORK=named"} }}, store)
	if err == nil || store.reads != 0 {
		t.Fatal("environment masked invalid file reference")
	}
	file, err := config.ReadFile(t.Context(), path)
	if err != nil {
		t.Fatal("file-only inspection cannot repair public metadata", err)
	}
	setting, err := file.Setting("api_token_env")
	if err != nil || !setting.Present || setting.Secret || setting.Value != "AWS_SECRET_ACCESS_KEY" {
		t.Fatal("public selector inspection lost declaration", err)
	}
	update, err := file.PrepareSetting("api_token_env", "JIRA_TOKEN_WORK")
	if err != nil {
		t.Fatal("replacement could not repair selector", err)
	}
	if changed, err := update.Save(t.Context()); err != nil || !changed {
		t.Fatal("repaired selector was not published", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || config.ValidateFile(data) != nil || strings.Contains(string(data), "named") {
		t.Fatal("repair persisted a token or invalid declaration", err)
	}
}

func TestTokenEnvironmentCancellationNeverReturnsNamedCredentials(t *testing.T) {
	t.Parallel()
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "before snapshot", false: "during snapshot"}[before], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if before {
				cancel()
			}
			path := writeConfig(t, "api_token_env='JIRA_TOKEN_WORK'\n")
			calls := 0
			store := &tokenReferenceStore{}
			resolved, err := config.Resolve(ctx, config.Options{Path: path, Environ: func() []string { calls++; cancel(); return []string{"JIRA_TOKEN_WORK=named"} }}, store)
			wantCalls := 1
			if before {
				wantCalls = 0
			}
			if resolved != nil || !errors.Is(err, context.Canceled) || calls != wantCalls || store.reads != 0 {
				t.Fatal("cancellation returned named credentials or opened store", err)
			}
		})
	}
}

func TestNamedEnvironmentTokenUsesConfiguredIdentityWithoutStore(t *testing.T) {
	t.Parallel()
	const token = " synthetic-reference-token "
	path := writeConfig(t, "base_url='https://example.invalid'\nemail='person@example.invalid'\napi_token_env='JIRA_TOKEN_SYNTHETIC_WORK'\n")
	calls := 0
	store := &tokenReferenceStore{}
	resolved, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string {
		calls++
		return []string{"JIRA_TOKEN_OTHER=synthetic-other-token", "JIRA_TOKEN_SYNTHETIC_WORK=" + token}
	}}, store)
	if err != nil || resolved.Config.APIToken != token || resolved.Config.BaseURL != "https://example.invalid" || resolved.Config.Email != "person@example.invalid" || store.reads != 0 || calls != 1 || resolved.Source("api_token") != config.LayerEnv || resolved.Source("api_token_env") != config.LayerFile {
		t.Fatal("named token lost identity, exact bytes, provenance or lookup isolation", err)
	}
}

func TestTokenEnvironmentReferenceValidationPrecedesNativeReads(t *testing.T) {
	t.Parallel()
	for _, reference := range []string{"AWS_SECRET_ACCESS_KEY", "JIRA_TOKEN_", "JIRA_TOKEN_bad", "JIRA_API_TOKEN_ENV", "JIRA_TOKEN_SYNTHETIC/WORK", "JIRA_TOKEN_SYNTHETIC WORK"} {
		t.Run("invalid reference", func(t *testing.T) {
			path := writeConfig(t, "api_token_env='"+reference+"'\n")
			store := &tokenReferenceStore{}
			_, err := config.Resolve(t.Context(), config.Options{Path: path, Environ: func() []string { return nil }}, store)
			if err == nil || store.reads != 0 || reference != "JIRA_TOKEN_" && strings.Contains(err.Error(), reference) {
				t.Fatal("invalid reference reached native storage or disclosed its value", err)
			}
		})
	}
}
