//go:build linux && nativecachesync

package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const cacheSyncProcessEnvironment = "JIRA_TEST_CACHE_SYNC_DIRECTORY"

type cacheSyncResult struct {
	Error      string `json:"error"`
	NativeEIO  bool   `json:"native_eio"`
	NativeOp   string `json:"native_op"`
	NativePath string `json:"native_path"`
}

type cacheSyncCall struct {
	thread, descriptor, path, result string
	position                         int
}

// This opt-in gate requires strace rather than substituting mocked Sync calls.
// Missing tracing support fails the gate; ordinary hermetic CI does not run it.
func TestDiskCacheNativeSync(t *testing.T) {
	t.Parallel()
	tracer, err := exec.LookPath("strace")
	if err != nil {
		t.Fatalf("native cache sync acceptance requires strace: %v", err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"success", "file_failure", "directory_failure"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			directory := filepath.Join(t.TempDir(), "cache")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			cache := DiskCache{dir: directory}
			name := filepath.Base(cache.path("native-sync"))
			old, err := json.Marshal(diskEntry{Key: "native-sync", Written: time.Unix(1, 0).UTC(), Value: []byte("old value")})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, name), old, 0o600); err != nil {
				t.Fatal(err)
			}
			tracePath := filepath.Join(t.ArtifactDir(), "sync.trace")
			arguments := []string{
				"--follow-forks", "--kill-on-exit", "--decode-fds=path", "--string-limit=1024",
				"--trace=fsync,renameat,close,unlinkat", "--output=" + tracePath,
			}
			switch stage {
			case "file_failure":
				arguments = append(arguments, "--inject=fsync:error=EIO:when=1")
			case "directory_failure":
				arguments = append(arguments, "--inject=fsync:error=EIO:when=2")
			}
			arguments = append(arguments, binary, "-test.run=^TestDiskCacheNativeSyncProcess$", "-test.v")
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, tracer, arguments...)
			child.Env = append(os.Environ(), cacheSyncProcessEnvironment+"="+directory, "GOMAXPROCS=1")
			output, err := child.CombinedOutput()
			if err != nil {
				t.Fatalf("native cache sync process did not complete: %v, deadline=%v, %s", err, ctx.Err(), output)
			}
			var result cacheSyncResult
			const marker = "native cache sync result: "
			observations := 0
			for line := range strings.SplitSeq(string(output), "\n") {
				if raw, ok := strings.CutPrefix(line, marker); ok {
					if err := json.Unmarshal([]byte(raw), &result); err != nil {
						t.Fatalf("invalid child result: %v", err)
					}
					observations++
				}
			}
			if observations != 1 {
				t.Fatalf("expected one native Set observation, got %d: %s", observations, output)
			}
			trace, err := os.ReadFile(tracePath)
			if err != nil {
				t.Fatal(err)
			}
			assertCacheSyncTrace(t, string(trace), directory, name, stage)
			if stage == "success" {
				if result.Error != "" || result.NativeEIO {
					t.Fatalf("successful native publication returned an error: %+v", result)
				}
			} else {
				prefix := "failed to sync cache temporary file:"
				if stage == "directory_failure" {
					prefix = "failed to sync cache directory after rename:"
				}
				if !result.NativeEIO || result.NativeOp != "sync" || !strings.HasPrefix(result.Error, prefix) {
					t.Fatalf("sync failure lost its native cause or stage: %+v", result)
				}
				if stage == "file_failure" && !strings.HasPrefix(result.NativePath, filepath.Join(directory, "."+name+".")) {
					t.Fatalf("file sync error describes the wrong descriptor: %+v", result)
				}
				if stage == "directory_failure" && filepath.Clean(result.NativePath) != directory {
					t.Fatalf("directory sync error describes the wrong descriptor: %+v", result)
				}
			}
			stored, err := os.ReadFile(filepath.Join(directory, name))
			if err != nil {
				t.Fatalf("sync outcome lost the destination: %v", err)
			}
			if stage == "file_failure" {
				if !bytes.Equal(stored, old) {
					t.Fatalf("file sync failure replaced the prior value: %s", stored)
				}
			} else {
				value, written, ok := cache.Get("native-sync")
				if !ok || string(value) != "new value" || !written.Equal(time.Unix(2, 0).UTC()) {
					t.Fatalf("published value or timestamp missing: %q, %v, %v", value, written, ok)
				}
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 1 || entries[0].Name() != name {
				t.Fatalf("sync outcome left temporary entries: %v, %v", entries, err)
			}
			info, err := os.Stat(filepath.Join(directory, name))
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("published permissions are not 0600: %v, %v", info, err)
			}
			t.Logf("native syscall trace: %s", tracePath)
		})
	}
}

func assertCacheSyncTrace(t *testing.T, trace, directory, name, stage string) {
	t.Helper()
	pattern := regexp.MustCompile(`^(\d+)\s+fsync\((\d+)<([^>]+)>\)\s+=\s+(.*)$`)
	var calls []cacheSyncCall
	lines := strings.Split(trace, "\n")
	for index, line := range lines {
		if strings.Contains(line, "fsync(") {
			match := pattern.FindStringSubmatch(line)
			if len(match) != 5 {
				t.Fatalf("cannot verify native sync trace line: %s", line)
			}
			calls = append(calls, cacheSyncCall{thread: match[1], descriptor: match[2], path: match[3], result: match[4], position: index})
		}
	}
	expected := 2
	if stage == "file_failure" {
		expected = 1
	}
	if len(calls) != expected {
		t.Fatalf("observed %d fsync calls, want %d: %s", len(calls), expected, trace)
	}
	if !strings.HasPrefix(calls[0].path, filepath.Join(directory, "."+name+".")) {
		t.Fatalf("first fsync did not target the unpublished temporary file: %+v", calls[0])
	}
	wantFileResult := "0"
	if stage == "file_failure" {
		wantFileResult = "-1 EIO (Input/output error) (INJECTED)"
	}
	if calls[0].result != wantFileResult {
		t.Fatalf("file fsync did not execute the requested outcome: %+v", calls[0])
	}
	temporaryClose := cacheSyncClosePosition(t, lines, calls[0])
	renames := 0
	for index, line := range lines {
		if !strings.Contains(line, "renameat(") {
			continue
		}
		renames++
		if stage == "file_failure" || index <= temporaryClose || index >= calls[1].position || !strings.HasSuffix(line, "= 0") {
			t.Fatalf("publication did not occur between successful file sync and directory sync: %s", line)
		}
	}
	if stage == "file_failure" {
		if renames != 0 {
			t.Fatalf("file sync failure published a value: %s", trace)
		}
		return
	}
	if renames != 1 || calls[0].thread != calls[1].thread || filepath.Clean(calls[1].path) != directory {
		t.Fatalf("directory sync did not follow one publication on the owned thread: %s", trace)
	}
	wantDirectoryResult := "0"
	if stage == "directory_failure" {
		wantDirectoryResult = "-1 EIO (Input/output error) (INJECTED)"
	}
	if calls[1].result != wantDirectoryResult {
		t.Fatalf("directory fsync did not execute the requested outcome: %+v", calls[1])
	}
	cacheSyncClosePosition(t, lines, calls[1])
}

func cacheSyncClosePosition(t *testing.T, lines []string, call cacheSyncCall) int {
	t.Helper()
	pattern := regexp.MustCompile(`^` + call.thread + `\s+close\(` + call.descriptor + `<` + regexp.QuoteMeta(call.path) + `>\)\s+=\s+0$`)
	position := -1
	for index, line := range lines {
		if pattern.MatchString(line) {
			if position != -1 || index <= call.position {
				t.Fatalf("synced descriptor did not close exactly once after sync: %s", line)
			}
			position = index
		}
	}
	if position == -1 {
		t.Fatalf("synced descriptor never closed successfully: %+v", call)
	}
	return position
}

func TestDiskCacheNativeSyncProcess(t *testing.T) {
	directory := os.Getenv(cacheSyncProcessEnvironment)
	if directory == "" {
		return
	}
	// Injection counts are per tracee. Keep both Sync calls on one owned thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cache := DiskCache{dir: directory, now: func() time.Time { return time.Unix(2, 0).UTC() }}
	err := cache.Set("native-sync", []byte("new value"))
	result := cacheSyncResult{NativeEIO: errors.Is(err, unix.EIO)}
	if err != nil {
		result.Error = err.Error()
		var cause *os.PathError
		if errors.As(err, &cause) {
			result.NativeOp, result.NativePath = cause.Op, cause.Path
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("native cache sync result: %s\n", raw)
}
