package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/adapters"
	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	jqlcmd "github.com/matcra587/jira-cli/internal/cli/jql"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestCacheClearUsesExplicitScopeWithoutAuthentication(t *testing.T) {
	for _, mode := range []string{"preview", "live", "missing account", "invalid site"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			disk, err := adapters.NewDiskCache()
			if err != nil {
				t.Fatal(err)
			}
			store, err := adapters.NewScopedCache(disk, "https://example.com", "person@example.com")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Set(metacache.BoardListKey, []byte("owned")); err != nil {
				t.Fatal(err)
			}
			deps := testDeps(t)
			deps.Cache = func(ctx context.Context) (core.Cache, error) {
				t.Error("local management opened credential-backed cache")
				return nil, errors.New("unexpected")
			}
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				t.Error("local management constructed Jira client")
				return nil, errors.New("unexpected")
			}
			deps.Credentials = func() (core.CredentialStore, error) {
				t.Error("local management opened credentials")
				return nil, errors.New("unexpected")
			}
			deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
				prefs := jqlcmd.BuildPreferences{BaseURL: "https://example.com", Account: "person@example.com"}
				if mode == "missing account" {
					prefs.Account = ""
				}
				if mode == "invalid site" {
					prefs.BaseURL = "invalid"
				}
				return prefs, nil
			}
			calls := 0
			deps.CacheFor = func(site, account string) (core.Cache, error) {
				calls++
				if site != "https://example.com" || account != "person@example.com" {
					t.Fatal("scope identity changed")
				}
				return store, nil
			}
			args := []string{"cache", "clear", "boards", "--output=json", "--no-input", "--force"}
			if mode != "live" {
				args = append(args, "--dry-run")
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			exit := execute(t.Context(), program, sink, args)
			var frame struct {
				OK     bool                      `json:"ok"`
				Data   envelope.CacheClearOutput `json:"data"`
				Errors []envelope.Error          `json:"errors"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
				t.Fatal(err)
			}
			invalid := mode == "missing account" || mode == "invalid site"
			if invalid {
				if exit != cli.ExitValidation || frame.OK || calls != 0 || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeConfigInvalid {
					t.Fatalf("scope refusal=%d/%s", exit, &stdout)
				}
			} else if exit != 0 || !frame.OK || calls != 1 || frame.Data.WouldRemove != 1 || (mode == "live" && frame.Data.Removed != 1) || (mode == "preview" && frame.Data.Removed != 0) {
				t.Fatalf("local outcome=%d/%s", exit, &stdout)
			}
			if _, _, ok := store.Get(metacache.BoardListKey); ok != (mode != "live") {
				t.Fatal("local scope selection changed the wrong stored state")
			}
		})
	}
}

type cachedBoardCredential struct {
	core.CredentialStore
	raw string
}

func (s cachedBoardCredential) Get(context.Context, string) (string, error) { return s.raw, nil }

func TestStoredCredentialBoardCacheRemainsReadableFromAuthoredPublicDefaults(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, key := range []string{"JIRA_BASE_URL", "JIRA_EMAIL", "JIRA_API_TOKEN", "JIRA_API_TOKEN_ENV", "JIRA_DEFAULT_BOARD", "JIRA_DEFAULT_PROJECT", "JIRA_DEFAULT_ISSUE_TYPE"} {
		t.Setenv(key, "")
	}
	t.Setenv("JIRA_KEYRING", "true")
	path := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(path, []byte("base_url='https://EXAMPLE.invalid/'\nemail='public@example.invalid'\ndefault_board='Sprint'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"issue", "mine", "--as-jql", "--config", path, "--output=json"}
	applyGlobalFlags(args)
	t.Cleanup(func() { applyGlobalFlags(nil) })
	record, err := json.Marshal(credentials.Record{Version: 2, Site: "https://example.invalid", Email: "public@example.invalid", Token: "synthetic-cached-default-token"})
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the real credential merge and production cache factory. Stored
	// identity canonicalizes the producer site; local file-only preferences
	// retain the authored spelling, so these outer namespaces differ.
	producer, err := newCache(t.Context(), cachedBoardCredential{raw: string(record)})
	if err != nil || producer.Set("v2:board:https://example.invalid:name:sprint", []byte(`{"id":42,"name":"Sprint","jql":"project = BOARD ORDER BY Rank ASC"}`)) != nil {
		t.Fatal("cannot warm retained credential-resolved scope", err)
	}
	preferences, err := buildPreferences()
	if err != nil || preferences.BaseURL != "https://EXAMPLE.invalid/" {
		t.Fatal("public settings unexpectedly opened or merged credentials", err)
	}
	deps := testDeps(t)
	deps.BuildPreferences, deps.CacheFor = buildPreferences, newScopedDiskCache
	deps.Credentials = func() (core.CredentialStore, error) { t.Fatal("public defaults opened credentials"); return nil, nil }
	deps.Cache = func(context.Context) (core.Cache, error) {
		t.Fatal("public defaults opened credential-backed cache")
		return nil, nil
	}
	deps.JiraClient = func(context.Context) (core.JiraClient, error) {
		t.Fatal("public defaults fetched a missing scope")
		return nil, nil
	}
	var stdout, stderr bytes.Buffer
	program, sink := newProgram(deps, &stdout, &stderr)
	exit := execute(t.Context(), program, sink, args)
	var frame struct {
		OK   bool                     `json:"ok"`
		Data envelope.IssueListOutput `json:"data"`
	}
	if exit != 0 || json.Unmarshal(stdout.Bytes(), &frame) != nil || !frame.OK || frame.Data.JQL != "(project = BOARD) AND assignee = currentUser() ORDER BY Rank ASC" || frame.Data.Board == nil || frame.Data.Board.ID != 42 || frame.Data.Board.Precedence != "default_board" {
		t.Fatal("stored-record producer lost its public default scope", exit, stdout.String())
	}
}

func TestDefaultBoardRejectsStoredTupleMismatchBeforeLiveEntry(t *testing.T) {
	for _, mismatch := range []string{"site", "account"} {
		for _, branch := range []string{"search", "count", "web"} {
			t.Run(mismatch+"/"+branch, func(t *testing.T) {
				t.Setenv("XDG_CACHE_HOME", t.TempDir())
				for _, key := range []string{"JIRA_BASE_URL", "JIRA_EMAIL", "JIRA_API_TOKEN", "JIRA_API_TOKEN_ENV", "JIRA_DEFAULT_BOARD", "JIRA_DEFAULT_PROJECT", "JIRA_DEFAULT_ISSUE_TYPE"} {
					t.Setenv(key, "")
				}
				t.Setenv("JIRA_KEYRING", "true")
				path := filepath.Join(t.TempDir(), "settings.toml")
				if err := os.WriteFile(path, []byte("base_url='https://public.invalid'\nemail='public@example.invalid'\ndefault_board='Sprint'\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				args := []string{"issue", "list", "--config", path, "--output=json"}
				if branch != "search" {
					args = append(args, "--"+branch)
				}
				applyGlobalFlags(args)
				t.Cleanup(func() { applyGlobalFlags(nil) })
				selected := credentials.Record{Version: 2, Site: "https://public.invalid", Email: "public@example.invalid", Token: "synthetic-mismatched-board-token"}
				if mismatch == "site" {
					selected.Site = "https://stored.invalid"
				} else {
					selected.Email = "stored@example.invalid"
				}
				raw, err := json.Marshal(selected)
				if err != nil {
					t.Fatal(err)
				}
				resolved, err := config.Resolve(t.Context(), config.Options{}, cachedBoardCredential{raw: string(raw)})
				if err != nil || resolved.Config.BaseURL != selected.Site || resolved.Config.Email != selected.Email {
					t.Fatal("synthetic stored tuple did not establish the disputed live binding", err)
				}
				requests := 0
				client, err := adapterjira.New(adapterjira.Config{BaseURL: resolved.Config.BaseURL, Email: resolved.Config.Email, APIToken: resolved.Config.APIToken, HTTPClient: &http.Client{Transport: cloudRootTransport(func(*http.Request) (*http.Response, error) {
					requests++
					return nil, errors.New("foreign scope must refuse before HTTP")
				})}})
				if err != nil {
					t.Fatal(err)
				}
				store, err := newScopedDiskCache("https://public.invalid", "public@example.invalid")
				if err != nil || store.Set("v2:board:https://public.invalid:name:Sprint", []byte(`{"id":42,"name":"Sprint","jql":"project = BOARD"}`)) != nil {
					t.Fatal("cannot seed configured public snapshot", err)
				}
				deps := testDeps(t)
				deps.BuildPreferences, deps.CacheFor = buildPreferences, newScopedDiskCache
				deps.JiraClient = func(context.Context) (core.JiraClient, error) { return client, nil }
				deps.Credentials = func() (core.CredentialStore, error) { t.Fatal("default reopened stored credentials"); return nil, nil }
				var stdout, stderr bytes.Buffer
				program, sink := newProgram(deps, &stdout, &stderr)
				exit := execute(t.Context(), program, sink, args)
				var frame struct {
					OK     bool             `json:"ok"`
					Errors []envelope.Error `json:"errors"`
				}
				if exit != cli.ExitValidation || json.Unmarshal(stdout.Bytes(), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeConfigInvalid || requests != 0 {
					t.Fatal("configured board crossed stored tuple before live refusal", exit, stdout.String())
				}
			})
		}
	}
}
