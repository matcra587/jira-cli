//go:build live

package live

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A fresh process exercises Go's real proxy selection without its process-wide
// environment cache or any network requests, Jira calls, or credential access.
func TestLiveOfflineTransitionProxyIsolation(t *testing.T) {
	const probeEnv = "JIRA_LIVE_PROXY_PROBE_OUTPUT"
	if path := os.Getenv(probeEnv); path != "" {
		var observed []string
		for _, scheme := range []string{"http", "https"} {
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, scheme+"://offline-proxy.example.invalid/", nil)
			if err != nil {
				t.Fatal(err)
			}
			proxy, err := http.ProxyFromEnvironment(request)
			if err != nil {
				t.Fatal(err)
			}
			value := ""
			if proxy != nil {
				value = proxy.String()
			}
			observed = append(observed, value)
		}
		raw, err := json.Marshal(observed)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "observed-proxies.json")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestLiveOfflineTransitionProxyIsolation$", "-test.count=1")
	child.Env = append(append(os.Environ(), "JIRA_LIVE_CLEANUP_HELPER=", probeEnv+"="+path, "NO_PROXY=", "no_proxy=*"), offlineTransitionEnvironment("XDG_CACHE_HOME="+dir, "public@example.invalid")...)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("proxy-selection probe did not run: %v output=%q", err, output)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("proxy-selection probe produced no receipt: %v", err)
	}
	var observed []string
	if err := json.Unmarshal(raw, &observed); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(observed, []string{"http://127.0.0.1:1", "http://127.0.0.1:1"}) {
		t.Fatalf("inherited proxy bypass escaped offline isolation: %q", observed)
	}
}
