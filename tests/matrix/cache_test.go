//go:build flagmatrix

package matrix

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/adapters"
	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestCacheClearFlagsControlActualLocalDeletion(t *testing.T) {
	for _, flag := range []string{"dry-run", "force", "yes"} {
		t.Run(flag, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("XDG_CACHE_HOME", root)
			disk, err := adapters.NewDiskCache()
			if err != nil {
				t.Fatal(err)
			}
			cache, err := adapters.NewScopedCache(disk, "https://example.com", "person@example.com")
			if err != nil {
				t.Fatal(err)
			}
			if err := cache.Set(metacache.BoardListKey, []byte("owned fixture")); err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"XDG_CACHE_HOME": root, "JIRA_BASE_URL": "https://example.com", "JIRA_EMAIL": "person@example.com", "JIRA_API_TOKEN": "", "JIRA_KEYRING": "true", "DBUS_SESSION_BUS_ADDRESS": "/dev/null", "JIRA_READ_ONLY": "true", "JIRA_NO_UPDATE_CHECK": "1"}
			args := []string{"cache", "clear", "boards", "--output=json", "--no-input", "--" + flag}
			for _, enabled := range []bool{false, true} {
				invoke := append([]string(nil), args...)
				if !enabled {
					invoke[len(invoke)-1] += "=false"
				}
				run := mx4Run(t, mx4Opts{Env: env}, invoke...)
				if !enabled {
					if run.Exit != 3 {
						t.Fatalf("explicitly false flag bypassed acknowledgement: %d", run.Exit)
					}
					if _, _, ok := cache.Get(metacache.BoardListKey); !ok {
						t.Fatal("refusal removed fixture")
					}
					continue
				}
				var out envelope.CacheClearOutput
				mx4Data(t, mx4OK(t, run), &out)
				preview := flag == "dry-run"
				if out.DryRun != preview || out.WouldRemove != 1 || preview && out.Removed != 0 || !preview && out.Removed != 1 {
					t.Fatalf("flag changed no receipt effect: %+v", out)
				}
				if _, _, ok := cache.Get(metacache.BoardListKey); ok != preview {
					t.Fatal("flag changed no storage effect")
				}
			}
			// Read-only preview also avoids creating any namespace on a cold root.
			cold := filepath.Join(t.TempDir(), "absent")
			env["XDG_CACHE_HOME"] = cold
			run := mx4Run(t, mx4Opts{Env: env}, "cache", "clear", "--dry-run", "--output=json", "--no-input")
			if run.Exit != 0 {
				t.Fatal("cold preview failed")
			}
			if _, err := os.Stat(cold); !os.IsNotExist(err) {
				t.Fatal("cold preview created cache storage")
			}
		})
		Prove(t, "jira cache clear", flag, "false refuses before local mutation; enabled dry-run retains the exact fixture and cold storage, while force/yes removes the selected cached entry without Jira credentials")
	}
}
