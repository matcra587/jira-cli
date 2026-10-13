package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gechr/clive/notify"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/cli"
	jqlcmd "github.com/matcra587/jira-cli/internal/cli/jql"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestCacheInspectSuppressesPassiveUpdateIO(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human"} {
		for _, scope := range []string{"valid", "missing email", "storage failure"} {
			t.Run(mode+"/"+scope, func(t *testing.T) {
				t.Setenv("AGENT", "0")
				t.Setenv("JIRA_NO_UPDATE_CHECK", "")
				dir := t.TempDir()
				cacheDir := filepath.Join(dir, "cache")
				notifyDir := filepath.Join(dir, "notifications")
				t.Setenv("XDG_CACHE_HOME", cacheDir)
				disk, err := adapters.NewDiskCache()
				if err != nil {
					t.Fatal(err)
				}
				store, err := adapters.NewScopedCache(disk, "https://example.com", "person@example.com")
				if err != nil {
					t.Fatal(err)
				}
				tool := &notificationTool{}
				deps := testDeps(t)
				deps.NotifyTool = tool
				deps.NotifyOptions = []notify.Option{notify.WithCacheDir(notifyDir), notify.WithCurrentVersion("v1.0.0")}
				deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
					prefs := jqlcmd.BuildPreferences{BaseURL: "https://example.com", Account: "person@example.com"}
					if scope == "missing email" {
						prefs.Account = ""
					}
					return prefs, nil
				}
				deps.CacheFor = func(string, string) (core.Cache, error) {
					if scope == "storage failure" {
						return nil, errors.New("owned storage failure")
					}
					return store, nil
				}
				deps.Credentials = func() (core.CredentialStore, error) {
					t.Fatal("inspection opened credentials")
					return nil, nil
				}
				deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
					t.Fatal("inspection constructed Jira client")
					return nil, nil
				}
				var stdout, stderr bytes.Buffer
				program, sink := newProgram(deps, &stdout, &stderr)
				wantExit := 0
				if scope != "valid" {
					wantExit = cli.ExitValidation
				}
				if exit := execute(t.Context(), program, sink, []string{"cache", "inspect", "--output=" + mode}); exit != wantExit {
					t.Fatalf("inspection exit=%d want=%d stdout=%s stderr=%s", exit, wantExit, &stdout, &stderr)
				}
				if !notify.AwaitRefresh(3 * time.Second) {
					t.Fatal("unexpected outstanding release refresh")
				}
				if tool.calls.Load() != 0 {
					t.Fatalf("inspection checked releases: calls=%d", tool.calls.Load())
				}
				for _, path := range []string{cacheDir, notifyDir} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("inspection created storage: %v", err)
					}
				}
			})
		}
	}
}

func TestCacheInspectUsesExplicitScopeWithoutAuthentication(t *testing.T) {
	for _, mode := range []string{"valid", "missing email", "invalid site"} {
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
			deps := testDeps(t)
			deps.Cache = func(ctx context.Context) (core.Cache, error) {
				t.Fatal("inspection opened credential-backed cache")
				return nil, nil
			}
			deps.Credentials = func() (core.CredentialStore, error) { t.Fatal("inspection opened credential store"); return nil, nil }
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				t.Fatal("inspection constructed Jira client")
				return nil, nil
			}
			deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
				prefs := jqlcmd.BuildPreferences{BaseURL: "https://example.com", Account: "person@example.com"}
				if mode == "missing email" {
					prefs.Account = ""
				}
				if mode == "invalid site" {
					prefs.BaseURL = "invalid"
				}
				return prefs, nil
			}
			calls := 0
			deps.CacheFor = func(site, email string) (core.Cache, error) {
				calls++
				if site != "https://example.com" || email != "person@example.com" {
					t.Fatal("inspection changed scope identity")
				}
				return store, nil
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			exit := execute(t.Context(), program, sink, []string{"cache", "inspect", "--output=json", "--no-input"})
			var frame struct {
				OK     bool                        `json:"ok"`
				Data   envelope.CacheInspectOutput `json:"data"`
				Errors []envelope.Error            `json:"errors"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil {
				t.Fatal(err)
			}
			if mode == "valid" {
				if exit != 0 || !frame.OK || calls != 1 || !frame.Data.Complete || frame.Data.Email != "person@example.com" || frame.Data.BaseURL != "https://example.com" || frame.Data.Count != 0 || len(frame.Data.Resources) != 12 {
					t.Fatalf("explicit scope=%d/%s stderr=%s", exit, &stdout, &stderr)
				}
			} else if exit != cli.ExitValidation || frame.OK || calls != 0 || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeConfigInvalid {
				t.Fatalf("scope refusal=%d/%s stderr=%s", exit, &stdout, &stderr)
			}
		})
	}
}

type partialInspectionCache struct {
	adapters.MemoryCache
	t *testing.T
}

func (s *partialInspectionCache) Entries(context.Context) ([]core.CacheEntry, error) {
	return []core.CacheEntry{{ID: "observed", Key: "future:resource", SizeBytes: 9}}, errors.New("owned partial inventory failure")
}

func (s *partialInspectionCache) DeleteEntry(context.Context, string) (bool, error) {
	s.t.Fatal("inspection deleted storage")
	return false, nil
}

func TestCacheInspectPartialFailureRetainsObservationsInSharedOutput(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("AGENT", "0")
			deps := testDeps(t)
			deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
				return jqlcmd.BuildPreferences{BaseURL: "https://example.com", Account: "person@example.com"}, nil
			}
			deps.CacheFor = func(string, string) (core.Cache, error) { return &partialInspectionCache{t: t}, nil }
			deps.Credentials = func() (core.CredentialStore, error) {
				t.Fatal("partial inspection opened credentials")
				return nil, nil
			}
			deps.JiraClient = func(ctx context.Context) (core.JiraClient, error) {
				t.Fatal("partial inspection constructed Jira client")
				return nil, nil
			}
			args := []string{"cache", "inspect", "--no-input"}
			if mode == "agent" {
				t.Setenv("AGENT", "1")
			} else {
				args = append(args, "--output="+mode)
			}
			var stdout, stderr bytes.Buffer
			program, sink := newProgram(deps, &stdout, &stderr)
			exit := execute(t.Context(), program, sink, args)
			if exit != cli.ExitValidation {
				t.Fatalf("partial inspection exit=%d stdout=%s stderr=%s", exit, &stdout, &stderr)
			}
			if mode == "human" {
				if !bytes.Contains(stdout.Bytes(), []byte("inspection incomplete")) || !bytes.Contains(stdout.Bytes(), []byte("observed")) {
					t.Fatalf("human output lost partial inventory: %s", &stdout)
				}
				return
			}
			var frame struct {
				OK     bool                        `json:"ok"`
				Data   envelope.CacheInspectOutput `json:"data"`
				Errors []envelope.Error            `json:"errors"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil || frame.OK || frame.Data.Complete || frame.Data.Count != 1 || len(frame.Data.Entries) != 1 || frame.Data.Entries[0].ID != "observed" || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeCacheIO {
				t.Fatalf("partial inventory frame=%s stderr=%s err=%v", &stdout, &stderr, err)
			}
		})
	}
}

func TestCacheInspectStorageFailureUsesSharedErrorOwner(t *testing.T) {
	t.Parallel()
	deps := testDeps(t)
	deps.BuildPreferences = func() (jqlcmd.BuildPreferences, error) {
		return jqlcmd.BuildPreferences{BaseURL: "https://example.com", Account: "person@example.com"}, nil
	}
	deps.CacheFor = func(string, string) (core.Cache, error) { return nil, errors.New("owned storage refusal") }
	var stdout, stderr bytes.Buffer
	program, sink := newProgram(deps, &stdout, &stderr)
	exit := execute(t.Context(), program, sink, []string{"cache", "inspect", "--output=compact"})
	var frame struct {
		OK     bool             `json:"ok"`
		Errors []envelope.Error `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &frame); err != nil || exit != cli.ExitValidation || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeCacheIO || !bytes.Contains([]byte(frame.Errors[0].Hint), []byte("retrying the cache operation")) {
		t.Fatalf("storage refusal=%d/%s stderr=%s err=%v", exit, &stdout, &stderr, err)
	}
}
