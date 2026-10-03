package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/cli"
	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func cacheClearFixture(t *testing.T) (*adapters.ScopedCache, *adapters.ScopedCache, []string, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	disk, err := adapters.NewDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	first, err := adapters.NewScopedCache(disk, "https://example.com", "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	second, err := adapters.NewScopedCache(disk, "https://example.com", "other@example.com")
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"XDG_CACHE_HOME=" + dir, "JIRA_BASE_URL=https://example.com", "JIRA_EMAIL=person@example.com", "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_READ_ONLY=true", "JIRA_NO_UPDATE_CHECK=1"}
	return first, second, env, dir
}

func TestCacheClearBinaryPreviewScopeAndCredentialIsolation(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			first, second, env, _ := cacheClearFixture(t)
			keys := []string{metacache.BoardListKey, metacache.BoardSelectorPrefix + "https://example.com:id:12", metacache.BoardSelectorPrefix + "https://example.com:name:Example board"}
			for _, key := range append(keys, metacache.LinkTypesPrefix+"https://example.com") {
				for _, cache := range []*adapters.ScopedCache{first, second} {
					if err := cache.Set(key, []byte("owned metadata")); err != nil {
						t.Fatal(err)
					}
				}
			}
			args := []string{"cache", "clear", "boards", "--no-input"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			for _, preview := range []bool{true, false} {
				invoke := append([]string(nil), args...)
				if preview {
					invoke = append(invoke, "--dry-run")
				} else {
					invoke = append(invoke, "--force")
				}
				stdout, stderr, exit := runBinaryEnv(t, env, invoke...)
				if exit != 0 {
					t.Fatalf("local clear attempted authentication/network or failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
				}
				if mode == "human" {
					want := "removed 3 of 3"
					if preview {
						want = "would remove 3"
					}
					if !strings.Contains(strings.Join(strings.Fields(stdout), " "), want) {
						t.Fatalf("human receipt = %q", stdout)
					}
				} else {
					var out envelope.CacheClearOutput
					if mode == "json" {
						var result frame
						if json.Unmarshal([]byte(stdout), &result) != nil || !result.OK || result.Meta.Command != "cache.clear" {
							t.Fatal("missing full clear frame")
						}
						if err := json.Unmarshal(result.Data, &out); err != nil {
							t.Fatal(err)
						}
					} else if err := json.Unmarshal([]byte(stdout), &out); err != nil {
						t.Fatal(err)
					}
					if out.Resource != "boards" || out.DryRun != preview || out.WouldRemove != 3 || len(out.Entries) != 3 || preview && out.Removed != 0 || !preview && out.Removed != 3 {
						t.Fatalf("clear receipt = %+v", out)
					}
					for _, entry := range out.Entries {
						if entry.Resource != "boards" || entry.Attempted == preview || entry.Removed == preview || entry.Error != nil {
							t.Fatalf("entry receipt = %+v", entry)
						}
					}
				}
				for _, key := range keys {
					if _, _, ok := first.Get(key); ok != preview {
						t.Fatal("preview/live flag did not control stored metadata")
					}
					if raw, _, ok := second.Get(key); !ok || string(raw) != "owned metadata" {
						t.Fatal("clear crossed account scope")
					}
				}
				if _, _, ok := first.Get(metacache.LinkTypesPrefix + "https://example.com"); !ok {
					t.Fatal("board clear removed link-type metadata")
				}
			}
		})
	}
}

func TestCacheClearBinaryPartialFailureRetainsAcknowledgedSibling(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			first, _, env, dir := cacheClearFixture(t)
			for _, key := range []string{metacache.BoardListKey, metacache.LinkTypesPrefix + "https://example.com"} {
				if err := first.Set(key, []byte("owned")); err != nil {
					t.Fatal(err)
				}
			}
			entries, err := first.Entries(t.Context())
			if err != nil || len(entries) != 2 {
				t.Fatal("fixture inventory failed")
			}
			failedID := entries[0].ID
			var failedPath string
			if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() && entry.Name() == failedID {
					failedPath = path
				}
				return nil
			}); err != nil || failedPath == "" {
				t.Fatal("owned fixture path missing")
			}
			if err := os.Remove(failedPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(failedPath, 0o700); err != nil {
				t.Fatal(err)
			}
			kept := filepath.Join(failedPath, "keep")
			if err := os.WriteFile(kept, []byte("preserve"), 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"cache", "clear", "--force", "--no-input"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != cli.ExitValidation {
				t.Fatalf("partial failure exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			if mode == "human" {
				if !strings.Contains(strings.Join(strings.Fields(stdout), " "), "removed 1 of 2") || !strings.Contains(stdout, "failed entry") {
					t.Fatalf("human partial receipt = %q", stdout)
				}
			} else {
				var result struct {
					OK     bool                      `json:"ok"`
					Data   envelope.CacheClearOutput `json:"data"`
					Errors []envelope.Error          `json:"errors"`
				}
				if json.Unmarshal([]byte(stdout), &result) != nil || result.OK || len(result.Errors) != 1 || result.Errors[0].Code != cli.CodeCacheIO || result.Data.Removed != 1 || result.Data.WouldRemove != 2 || len(result.Data.Entries) != 2 {
					t.Fatalf("partial receipt = %q", stdout)
				}
				failed, removed := 0, 0
				for _, entry := range result.Data.Entries {
					if !entry.Attempted {
						t.Fatal("partial run lost attempted outcomes")
					}
					if entry.Error != nil {
						failed++
					} else if entry.Removed {
						removed++
					}
				}
				if failed != 1 || removed != 1 {
					t.Fatal("partial receipt fabricated completed entries")
				}
			}
			if raw, err := os.ReadFile(kept); err != nil || string(raw) != "preserve" {
				t.Fatal("clear recursively removed an owned directory")
			}
			remaining, err := first.Entries(t.Context())
			if err != nil || len(remaining) != 1 || remaining[0].ID != failedID {
				t.Fatal("acknowledged sibling was not independently removed")
			}
		})
	}
}
