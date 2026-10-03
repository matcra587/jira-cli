package root

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/cli"
	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	jqlcmd "github.com/matcra587/jira-cli/internal/cli/jql"
	"github.com/matcra587/jira-cli/internal/core"
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
			deps.Cache = func() (core.Cache, error) {
				t.Error("local management opened credential-backed cache")
				return nil, errors.New("unexpected")
			}
			deps.JiraClient = func() (core.JiraClient, error) {
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
