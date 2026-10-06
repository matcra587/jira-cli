//go:build unix

package adapters

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const cacheReplacementEnvironment = "JIRA_TEST_CACHE_REPLACEMENT"

// The wrapper changes the real filesystem only after native Lstat confirms the
// original regular inode. Opening and reading still use the real retained root.
type replacingCacheRoot struct {
	*os.Root
	replace  func() error
	replaced bool
}

func (r *replacingCacheRoot) Lstat(name string) (os.FileInfo, error) {
	info, err := r.Root.Lstat(name)
	if err != nil || r.replaced {
		return info, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("fixture inspection did not find a regular file")
	}
	if err := r.replace(); err != nil {
		return nil, err
	}
	r.replaced = true
	return info, nil
}

func TestReadCacheEntryRefusesReplacement(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"fifo", "symlink_regular", "symlink_fifo", "directory"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, binary, "-test.run=^TestReadCacheEntryReplacementProcess$", "-test.v")
			child.Env = append(os.Environ(), cacheReplacementEnvironment+"="+kind)
			output, err := child.CombinedOutput()
			if !bytes.Contains(output, []byte("replacement after regular inspection")) {
				t.Fatalf("replacement %s did not reach the native inspection boundary: %v, %s", kind, err, output)
			}
			if err != nil {
				t.Fatalf("readCacheEntry(%s replacement) did not refuse promptly: %v, deadline=%v, %s", kind, err, ctx.Err(), output)
			}
		})
	}
}

func TestReadCacheEntryReplacementProcess(t *testing.T) {
	kind := os.Getenv(cacheReplacementEnvironment)
	if kind == "" {
		return
	}
	cache := DiskCache{dir: t.TempDir()}
	name := filepath.Base(cache.path("entry"))
	path := filepath.Join(cache.dir, name)
	if err := os.WriteFile(path, []byte("original entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := cache.openRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close() //nolint:errcheck // The test owns a read-only root.
	wrapped := &replacingCacheRoot{Root: root, replace: func() error {
		target := filepath.Join(cache.dir, "replacement-target")
		// Keep the original inode behind the internal symlink: an identity check
		// alone must not substitute for refusing symlink traversal at open time.
		if err := os.Rename(path, target); err != nil {
			return err
		}
		switch kind {
		case "fifo":
			return unix.Mkfifo(path, 0o600)
		case "symlink_regular":
			return os.Symlink("replacement-target", path)
		case "symlink_fifo":
			if err := os.Remove(target); err != nil {
				return err
			}
			if err := unix.Mkfifo(target, 0o600); err != nil {
				return err
			}
			return os.Symlink("replacement-target", path)
		case "directory":
			return os.Mkdir(path, 0o700)
		default:
			return fmt.Errorf("unknown replacement %q", kind)
		}
	}}
	// Print inside the inspection callback so a blocked native open still leaves
	// evidence that the command ran and that the replacement window was exercised.
	var nativeSymlinkCause error
	replace := wrapped.replace
	wrapped.replace = func() error {
		if err := replace(); err != nil {
			return err
		}
		if strings.HasPrefix(kind, "symlink_") {
			probe, probeErr := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
			if probeErr == nil {
				_ = probe.Close()
				return fmt.Errorf("native no-follow fixture unexpectedly opened the symlink")
			}
			var native *os.PathError
			if !errors.As(probeErr, &native) {
				return fmt.Errorf("native no-follow fixture lost its path cause: %w", probeErr)
			}
			nativeSymlinkCause = native.Err
		}
		t.Log("replacement after regular inspection")
		return nil
	}
	raw, err := readCacheEntry(wrapped, name)
	if nativeSymlinkCause != nil {
		var native *os.PathError
		if !errors.Is(err, nativeSymlinkCause) || !errors.As(err, &native) {
			t.Fatalf("readCacheEntry(%s replacement) lost the independently observed native cause: %v", kind, err)
		}
	}
	if !wrapped.replaced || !errors.Is(err, errUnsafeCacheEntry) || len(raw) != 0 {
		t.Fatalf("readCacheEntry(%s replacement) = %q, %v; want an unsafe-entry refusal", kind, raw, err)
	}
}

func TestReadCacheEntryKeepsOpenedNamespace(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	original := filepath.Join(base, "cache")
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	cache := DiskCache{dir: original}
	name := filepath.Base(cache.path("entry"))
	if err := os.WriteFile(filepath.Join(original, name), []byte("owned bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := cache.openRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close() //nolint:errcheck // The test owns a read-only root.
	wrapped := &replacingCacheRoot{Root: root, replace: func() error {
		if err := os.Rename(original, filepath.Join(base, "moved")); err != nil {
			return err
		}
		if err := os.Mkdir(original, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(original, name), []byte("foreign bytes"), 0o600)
	}}
	raw, err := readCacheEntry(wrapped, name)
	if !wrapped.replaced || err != nil || string(raw) != "owned bytes" {
		t.Fatalf("readCacheEntry(replaced namespace) = %q, %v; want owned bytes", raw, err)
	}
}

func TestReadCacheEntryRetainsMissingCause(t *testing.T) {
	t.Parallel()
	cache := DiskCache{dir: t.TempDir()}
	name := filepath.Base(cache.path("entry"))
	path := filepath.Join(cache.dir, name)
	if err := os.WriteFile(path, []byte("entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := cache.openRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close() //nolint:errcheck // The test owns a read-only root.
	wrapped := &replacingCacheRoot{Root: root, replace: func() error { return os.Remove(path) }}
	raw, err := readCacheEntry(wrapped, name)
	var native *os.PathError
	if !wrapped.replaced || len(raw) != 0 || !errors.Is(err, fs.ErrNotExist) || !errors.As(err, &native) {
		t.Fatalf("readCacheEntry(concurrent removal) = %q, %v; want native missing-file cause", raw, err)
	}
}

func TestOpenCacheEntryUsesNativeReadFlags(t *testing.T) {
	t.Parallel()
	cache := DiskCache{dir: t.TempDir()}
	name := filepath.Base(cache.path("entry"))
	if err := os.WriteFile(filepath.Join(cache.dir, name), []byte("entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := cache.openRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close() //nolint:errcheck // The test owns a read-only root.
	file, err := openCacheEntry(root, name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close() //nolint:errcheck // The test owns the newly opened descriptor.
	conn, err := file.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var flags, descriptorFlags int
	var flagsErr, descriptorErr error
	if err := conn.Control(func(fd uintptr) {
		flags, flagsErr = unix.FcntlInt(fd, unix.F_GETFL, 0)
		descriptorFlags, descriptorErr = unix.FcntlInt(fd, unix.F_GETFD, 0)
	}); err != nil {
		t.Fatal(err)
	}
	if flagsErr != nil || descriptorErr != nil {
		t.Fatalf("native flag probes failed: %v, %v", flagsErr, descriptorErr)
	}
	if flags&unix.O_NONBLOCK == 0 || descriptorFlags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("openCacheEntry native flags = %#x, descriptor flags = %#x; want nonblocking and close-on-exec", flags, descriptorFlags)
	}
}
