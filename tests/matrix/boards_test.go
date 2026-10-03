//go:build flagmatrix

package matrix

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoardsListFlagEffects(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/agile/1.0/board" {
			calls.Add(1)
			_, _ = fmt.Fprint(w, `{"isLast":true,"startAt":0,"maxResults":50,"values":[{"id":42,"name":"Sprint"},{"id":99,"name":"Other"}]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"isLast":true,"startAt":0,"maxResults":50,"values":[]}`)
	}))
	defer server.Close()
	home := t.TempDir()
	env := []string{"HOME=" + home, "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "LOCALAPPDATA=" + filepath.Join(home, "cache"), "APPDATA=" + filepath.Join(home, "config")}
	first := fwParse(t, fwExecEnv(t, server.URL, nil, "", env, "boards", "list", "--limit=1"))
	if fwAt(t, first.Data, "count") != float64(1) || fwAt(t, first.Data, "pagination", "total") != float64(2) || calls.Load() != 1 {
		t.Fatal("limit changed fetch scope or output window")
	}
	Prove(t, "jira boards list", "limit", "real binary drains/enriches the snapshot before applying its one-row output window")
	all := fwParse(t, fwExecEnv(t, server.URL, nil, "", env, "boards", "list", "--limit=1", "--all"))
	if fwAt(t, all.Data, "count") != float64(2) || fwAt(t, all.Data, "from_cache") != true || calls.Load() != 1 {
		t.Fatal("all did not emit the full cached snapshot")
	}
	Prove(t, "jira boards list", "all", "real binary emits both cached identities despite a one-row display limit")
	refresh := fwParse(t, fwExecEnv(t, server.URL, nil, "", env, "boards", "list", "--refresh"))
	if fwAt(t, refresh.Data, "from_cache") != false || calls.Load() != 2 {
		t.Fatal("refresh reused a fresh snapshot")
	}
	Prove(t, "jira boards list", "refresh", "real binary refetches the fresh snapshot and reports live provenance")
	entries := 0
	if err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var disk map[string]json.RawMessage
		if err := json.Unmarshal(raw, &disk); err != nil {
			return err
		}
		if disk["written"] == nil || disk["value"] == nil {
			return fmt.Errorf("unexpected fixture cache entry")
		}
		disk["written"], err = json.Marshal(time.Now().Add(-time.Hour))
		if err != nil {
			return err
		}
		raw, err = json.Marshal(disk)
		if err != nil {
			return err
		}
		entries++
		return os.WriteFile(path, raw, 0o600)
	}); err != nil || entries != 1 {
		t.Fatalf("cannot age the actual isolated cache: entries=%d err=%v", entries, err)
	}
	aged := fwParse(t, fwExecEnv(t, server.URL, nil, "", env, "boards", "list", "--ttl-minutes=2"))
	if fwAt(t, aged.Data, "from_cache") != false || fwAt(t, aged.Data, "cache_source_state") != "stale" || calls.Load() != 3 {
		t.Fatal("ttl did not expire the aged snapshot")
	}
	Prove(t, "jira boards list", "ttl-minutes", "real binary refetches a one-hour-old disk entry under a two-minute freshness window")
}

func TestBoardsUnboundedFlagRefetchesCappedCache(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/agile/1.0/board" {
			_, _ = fmt.Fprint(w, `{"isLast":true,"startAt":0,"maxResults":50,"values":[]}`)
			return
		}
		start, err := strconv.Atoi(r.URL.Query().Get("startAt"))
		if err != nil {
			t.Error(err)
		}
		calls.Add(1)
		_, _ = fmt.Fprintf(w, `{"isLast":%t,"startAt":%d,"maxResults":50,"values":[{"id":%d,"name":"Sprint"}]}`, start == 100, start, start+1)
	}))
	defer server.Close()
	home := t.TempDir()
	env := []string{"HOME=" + home, "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "XDG_CONFIG_HOME=" + filepath.Join(home, "config"), "LOCALAPPDATA=" + filepath.Join(home, "cache"), "APPDATA=" + filepath.Join(home, "config")}
	bounded := fwParse(t, fwExecEnv(t, server.URL, nil, "", env, "boards", "list", "--all"))
	if fwAt(t, bounded.Data, "truncated") != true || fwAt(t, bounded.Data, "count") != float64(100) || calls.Load() != 100 {
		t.Fatal("default page cap not observed")
	}
	unbounded := fwParse(t, fwExecEnv(t, server.URL, nil, "", env, "boards", "list", "--unbounded", "--all"))
	if fwAt(t, unbounded.Data, "truncated") != false || fwAt(t, unbounded.Data, "count") != float64(101) || fwAt(t, unbounded.Data, "from_cache") != false || calls.Load() != 201 {
		t.Fatal("unbounded served a capped cache or stopped at the default bound")
	}
	Prove(t, "jira boards list", "unbounded", "real binary invalidates a capped snapshot and walks the 101st page to explicit completion")
}
