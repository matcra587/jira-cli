package config_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/config"
)

func TestNewFileRefusesExistingNames(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"file", "directory", "symlink", "dangling symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			switch kind {
			case "file":
				if err := os.WriteFile(path, []byte("invalid = ["), 0o640); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o750); err != nil {
					t.Fatal(err)
				}
			default:
				if xos.IsWindows() {
					t.Skip("symlink creation requires an enabled Windows privilege")
				}
				target := filepath.Join(dir, "target.toml")
				if kind == "symlink" {
					if err := os.WriteFile(target, []byte("read_only=true\n"), 0o640); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := config.OpenNewFile(t.Context(), path); !errors.Is(err, config.ErrFileChanged) {
				t.Fatalf("existing %s accepted for creation: %v", kind, err)
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
				t.Fatalf("creation preparation changed existing %s: %v", kind, err)
			}
		})
	}
}

func TestNewFileRejectsCancellationAndInvalidCandidatesBeforeCreation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "unknown key", data: []byte("unknown=true\n")},
		{name: "invalid site", data: []byte("base_url='invalid'\n")},
		{name: "invalid syntax", data: []byte("[")},
		{name: "invalid utf8", data: []byte{'#', 0xff}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing", "config.toml")
			file, err := config.OpenNewFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			created, err := file.Create(t.Context(), tc.data)
			if created || !errors.Is(err, config.ErrFileInvalid) {
				t.Fatalf("invalid candidate accepted: created=%v err=%v", created, err)
			}
			if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid candidate touched missing parent: %v", err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "missing", "config.toml")
	file, err := config.OpenNewFile(t.Context(), path)
	if err != nil || file.Exists() || len(file.Contents()) != 0 {
		t.Fatalf("new target has unexpected contents: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := config.OpenNewFile(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preparation accepted: %v", err)
	}
	created, err := file.Create(ctx, []byte("read_only=true\n"))
	if created || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled creation accepted: created=%v err=%v", created, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled creation touched missing parent: %v", err)
	}
}

func TestNewFilePreservesExistingParentAndCleansStaging(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o750); err != nil { //nolint:gosec // owned directory fixture pins preservation of existing parent permissions
		t.Fatal(err)
	}
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	file, err := config.OpenNewFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	const data = "# preserve candidate bytes\nread_only=true\n"
	created, err := file.Create(t.Context(), []byte(data))
	if err != nil || !created {
		t.Fatalf("valid creation failed: created=%v err=%v", created, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != data {
		t.Fatalf("creation changed candidate bytes: %q %v", contents, err)
	}
	after, err := os.Stat(dir)
	if err != nil || before.Mode() != after.Mode() {
		t.Fatalf("creation changed existing parent permissions: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || (!xos.IsWindows() && info.Mode().Perm() != 0o600) {
		t.Fatalf("creation did not secure its file: %v %v", info, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "config.toml" {
		t.Fatalf("creation retained staging files: %v %v", entries, err)
	}
	created, err = file.Create(t.Context(), []byte("read_only=false\n"))
	if created || !errors.Is(err, config.ErrFileChanged) {
		t.Fatalf("second creation replaced existing file: created=%v err=%v", created, err)
	}
	contents, err = os.ReadFile(path)
	if err != nil || string(contents) != data {
		t.Fatalf("second creation changed published bytes: %q %v", contents, err)
	}
}

func TestNewFileConcurrentCreatorsCannotReplaceWinner(t *testing.T) {
	t.Parallel()
	const creators = 16
	for round := range 8 {
		t.Run(fmt.Sprintf("round %d", round), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			updates := make([]*config.SettingsUpdate, creators)
			for i := range creators {
				file, err := config.OpenNewFile(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				updates[i], err = file.PrepareSettings(map[string]string{
					"base_url": "https://example.invalid",
					"email":    fmt.Sprintf("creator%d@example.invalid", i),
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			type result struct {
				creator int
				created bool
				err     error
			}
			start := make(chan struct{})
			results := make(chan result, creators)
			for i, update := range updates {
				go func() {
					<-start
					created, err := update.Create(t.Context())
					results <- result{creator: i, created: created, err: err}
				}()
			}
			close(start)
			winner, successes := -1, 0
			for range creators {
				out := <-results
				if out.created {
					winner, successes = out.creator, successes+1
					if out.err != nil {
						t.Errorf("publication durability failed: %v", out.err)
					}
				} else if !errors.Is(out.err, config.ErrFileChanged) {
					t.Errorf("competing creation has unexpected outcome: %v", out.err)
				}
			}
			if successes != 1 {
				t.Fatalf("competing creators published %d winners; want exactly one", successes)
			}
			file, err := config.OpenFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			email, err := file.Setting("email")
			if err != nil || !email.Present || email.Value != fmt.Sprintf("creator%d@example.invalid", winner) {
				t.Fatalf("losing creator replaced the winner: %+v %v", email, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "config.toml" {
				t.Fatalf("competing creators retained staging files: %v %v", entries, err)
			}
		})
	}
}

func TestNewFileRefusesRedirectedParent(t *testing.T) {
	t.Parallel()
	if xos.IsWindows() {
		t.Skip("symlink creation requires an enabled Windows privilege")
	}
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	for _, path := range []string{first, second} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "selected")
	if err := os.Symlink(first, link); err != nil {
		t.Fatal(err)
	}
	file, err := config.OpenNewFile(t.Context(), filepath.Join(link, "config.toml"))
	if err != nil || file.Path() != filepath.Join(first, "config.toml") {
		t.Fatalf("linked parent was not resolved: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	created, err := file.Create(t.Context(), []byte("read_only=true\n"))
	if created || !errors.Is(err, config.ErrFileChanged) {
		t.Fatalf("redirected parent accepted: created=%v err=%v", created, err)
	}
	for _, path := range []string{first, second} {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			t.Fatalf("redirected creation touched %s: %v %v", path, entries, err)
		}
	}
}

func TestNewFilePermissionFailureLeavesNoPartialDestination(t *testing.T) {
	t.Parallel()
	if xos.IsWindows() || os.Geteuid() == 0 {
		t.Skip("requires native Unix directory permissions without root bypass")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	file, err := config.OpenNewFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // owned directory fixture must deny writes while retaining traversal
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // restore private directory traversal so the owned fixture can be removed
			t.Error(err)
		}
	})
	created, err := file.Create(t.Context(), []byte("read_only=true\n"))
	if created || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("permission failure lost its cause or claimed publication: created=%v err=%v", created, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("permission failure left destination or staging files: %v %v", entries, err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o500 {
		t.Fatalf("creation changed a nonwritable existing parent: %v %v", info, err)
	}
}

func TestNewFileResolvesSymlinkAncestorsBeforeCreatingMissingParents(t *testing.T) {
	t.Parallel()
	if xos.IsWindows() {
		t.Skip("symlink creation requires an enabled Windows privilege")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "actual")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "selected")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(link, "missing", "nested", "config.toml")
	want := filepath.Join(target, "missing", "nested", "config.toml")
	file, err := config.OpenNewFile(t.Context(), path)
	if err != nil || file.Path() != want {
		t.Fatalf("new config snapshot did not resolve its existing symlink ancestor: path=%v err=%v", file, err)
	}
	if _, err := os.Stat(filepath.Dir(want)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preparation created missing directories: %v", err)
	}
	const data = "read_only=true\n"
	created, err := file.Create(t.Context(), []byte(data))
	if !created || err != nil {
		t.Fatalf("linked new parent refused valid creation: created=%v err=%v", created, err)
	}
	contents, err := os.ReadFile(want)
	if err != nil || string(contents) != data {
		t.Fatalf("linked initialization lost complete bytes: %q %v", contents, err)
	}
	linked, err := os.Readlink(link)
	if err != nil || linked != target {
		t.Fatalf("initialization changed selected directory link: %q %v", linked, err)
	}
}

func TestNewFileCannotRecreateRemovedExistingSnapshot(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("read_only=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := config.OpenFile(t.Context(), path)
	if err != nil || !file.Exists() {
		t.Fatalf("existing snapshot was not loaded: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	created, err := file.Create(t.Context(), []byte("read_only=false\n"))
	if created || !errors.Is(err, config.ErrFileChanged) {
		t.Fatalf("removed existing snapshot became a creation target: created=%v err=%v", created, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old snapshot recreated its removed file: %v", err)
	}
}

func TestNewFileRejectsRegularAncestorsWithoutEffects(t *testing.T) {
	t.Parallel()
	for _, relative := range []string{"blocker/config.toml", "blocker/missing/config.toml"} {
		t.Run(relative, func(t *testing.T) {
			dir := t.TempDir()
			blocker := filepath.Join(dir, "blocker")
			if err := os.WriteFile(blocker, []byte("retain ancestor bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			if file, err := config.OpenNewFile(t.Context(), filepath.Join(dir, relative)); err == nil || file != nil {
				t.Fatal("regular ancestor was accepted as an absent directory")
			}
			data, err := os.ReadFile(blocker)
			if err != nil || string(data) != "retain ancestor bytes" {
				t.Fatal("path validation changed a regular ancestor")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "blocker" {
				t.Fatal("path validation created unrelated state")
			}
		})
	}
}
