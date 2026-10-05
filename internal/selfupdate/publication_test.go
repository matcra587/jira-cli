package selfupdate

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	xos "github.com/gechr/x/os"
)

func publicationFixture(t *testing.T) (string, *Target, *os.File, string) {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "jira")
	writeExecutable(t, path, "original executable", 0o750)
	target, err := OpenTarget(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := target.Close(); err != nil {
			t.Error(err)
		}
	})
	candidatePath := filepath.Join(t.TempDir(), "jira")
	writeExecutable(t, candidatePath, "updated executable", 0o700)
	candidate, err := os.Open(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := candidate.Close(); err != nil {
			t.Error(err)
		}
	})
	digest := sha256.Sum256([]byte("updated executable"))
	return path, target, candidate, fmt.Sprintf("%x", digest)
}

func writeExecutable(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func assertExecutable(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("executable = %q, error %v; want %q", data, err, want)
	}
}

func assertNoStage(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".new" {
			t.Fatalf("unfinished stage retained: %s", entry.Name())
		}
	}
}

func TestPublishVerifiedExecutable(t *testing.T) {
	path, target, candidate, digest := publicationFixture(t)
	// A prior version/integrity read must not leave the copy starting at EOF.
	if _, err := candidate.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	out, err := target.Publish(t.Context(), candidate, digest)
	if err != nil || !out.Published || out.Uncertain || out.SHA256 != digest || out.Bytes != int64(len("updated executable")) {
		t.Fatalf("publication = %+v, error %v", out, err)
	}
	if xos.IsUnix() && !out.Durable {
		t.Fatal("synced Unix publication did not retain durability")
	}
	assertExecutable(t, path, "updated executable")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != target.info.Mode().Perm() {
		t.Fatalf("permissions = %v, error %v", info, err)
	}
	assertNoStage(t, filepath.Dir(path))
}

func TestPublishRejectsChangedCandidate(t *testing.T) {
	path, target, candidate, _ := publicationFixture(t)
	out, err := target.Publish(t.Context(), candidate, fmt.Sprintf("%x", sha256.Sum256([]byte("different candidate"))))
	if err == nil || out.Published || out.Uncertain {
		t.Fatalf("unchecked candidate publication = %+v, error %v", out, err)
	}
	assertExecutable(t, path, "original executable")
	assertNoStage(t, filepath.Dir(path))
}

func TestPublishRejectsInterveningChange(t *testing.T) {
	for _, mode := range []string{"contents", "entry", "permissions", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			path, target, candidate, digest := publicationFixture(t)
			want := "intervening executable"
			switch mode {
			case "contents":
				writeExecutable(t, path, want, 0o750)
			case "entry":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				writeExecutable(t, path, want, 0o750)
			case "permissions":
				if !xos.IsUnix() {
					t.Skip("Unix permission-bit comparison")
				}
				// Toggle an observed bit so restrictive creation umasks cannot
				// turn the intervening permission change into a no-op.
				changedMode := target.info.Mode().Perm() ^ 0o040
				if err := os.Chmod(path, changedMode); err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != changedMode || info.Mode().Perm() == target.info.Mode().Perm() {
					t.Fatal("intervening permission change was not applied")
				}
				want = "original executable"
			case "symlink":
				other := filepath.Join(t.TempDir(), "other")
				writeExecutable(t, other, want, 0o750)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			out, err := target.Publish(t.Context(), candidate, digest)
			if !errors.Is(err, ErrTargetChanged) || out.Published || out.Uncertain {
				t.Fatalf("intervening change publication = %+v, error %v", out, err)
			}
			assertExecutable(t, path, want)
			assertNoStage(t, filepath.Dir(path))
		})
	}
}

func TestPublishRejectsInvalidCandidate(t *testing.T) {
	for _, mode := range []string{"empty", "oversized", "directory", "closed"} {
		t.Run(mode, func(t *testing.T) {
			path, target, candidate, digest := publicationFixture(t)
			switch mode {
			case "empty", "oversized":
				size := int64(0)
				if mode == "oversized" {
					size = MaxExecutableBytes + 1
				}
				// Truncate through a separately owned writable descriptor; the
				// candidate retained by Publish remains read-only.
				if err := os.Truncate(candidate.Name(), size); err != nil {
					t.Fatal(err)
				}
			case "directory":
				file, err := os.Open(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := file.Close(); err != nil {
						t.Error(err)
					}
				}()
				candidate = file
			case "closed":
				file, err := os.Open(candidate.Name())
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				candidate = file
			}
			out, err := target.Publish(t.Context(), candidate, digest)
			if err == nil || out.Published || out.Uncertain {
				t.Fatalf("invalid candidate publication = %+v, error %v", out, err)
			}
			assertExecutable(t, path, "original executable")
			assertNoStage(t, filepath.Dir(path))
		})
	}
}

func TestOpenTargetRejectsInvalidExecutable(t *testing.T) {
	for _, mode := range []string{"empty", "missing", "directory", "symlink", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "jira")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "empty":
				writeExecutable(t, path, "", 0o750)
			case "directory":
				path = directory
			case "symlink":
				other := filepath.Join(directory, "other")
				writeExecutable(t, other, "old", 0o750)
				if err := os.Symlink(other, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			case "canceled":
				writeExecutable(t, path, "old", 0o750)
				cancel()
			}
			target, err := OpenTarget(ctx, path)
			if target != nil {
				defer func() {
					if err := target.Close(); err != nil {
						t.Error(err)
					}
				}()
			}
			if err == nil || target != nil {
				t.Fatalf("invalid installation = %v, error %v", target, err)
			}
		})
	}
}

func TestPublishCanceledPreservesExecutable(t *testing.T) {
	path, target, candidate, digest := publicationFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out, err := target.Publish(ctx, candidate, digest)
	if !errors.Is(err, context.Canceled) || out.Published {
		t.Fatalf("canceled publication = %+v, error %v", out, err)
	}
	assertExecutable(t, path, "original executable")
	assertNoStage(t, filepath.Dir(path))
}

func TestPublicationReceiptSurvivesDirectorySyncFailure(t *testing.T) {
	if !xos.IsUnix() {
		t.Skip("Unix directory durability boundary")
	}
	path, target, candidate, digest := publicationFixture(t)
	failure := errors.New("directory storage sync failed")
	target.syncDirectory = func(*os.Root) error { return failure }
	out, err := target.Publish(t.Context(), candidate, digest)
	if !errors.Is(err, failure) || !out.Published || out.Durable || out.Uncertain || out.SHA256 != digest {
		t.Fatalf("publication lost after sync failure: %+v, error %v", out, err)
	}
	assertExecutable(t, path, "updated executable")
	assertNoStage(t, filepath.Dir(path))
}

func TestPublicationReceiptSurvivesLateCancellation(t *testing.T) {
	if !xos.IsUnix() {
		t.Skip("Unix directory durability boundary")
	}
	path, target, candidate, digest := publicationFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	target.syncDirectory = func(root *os.Root) error {
		err := syncInstallationDirectory(root)
		cancel()
		return err
	}
	out, err := target.Publish(ctx, candidate, digest)
	if !errors.Is(err, context.Canceled) || !out.Published || !out.Durable || out.Uncertain || out.SHA256 != digest {
		t.Fatalf("publication lost after late cancellation: %+v, error %v", out, err)
	}
	assertExecutable(t, path, "updated executable")
	assertNoStage(t, filepath.Dir(path))
}

func TestFinalPublicationCancellation(t *testing.T) {
	for _, windows := range []bool{false, true} {
		t.Run(fmt.Sprintf("windows_%t", windows), func(t *testing.T) {
			directory := t.TempDir()
			writeExecutable(t, filepath.Join(directory, "jira"), "old", 0o750)
			writeExecutable(t, filepath.Join(directory, "stage"), "new", 0o750)
			root, err := os.OpenRoot(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := root.Close(); err != nil {
					t.Error(err)
				}
			}()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			out, err := replaceExecutable(ctx, windows, root.Rename, "stage", "jira", Publication{})
			if !errors.Is(err, context.Canceled) || out.Published || out.Backup != "" || out.Uncertain {
				t.Fatalf("canceled final replacement = %+v, error %v", out, err)
			}
			assertExecutable(t, filepath.Join(directory, "jira"), "old")
			assertExecutable(t, filepath.Join(directory, "stage"), "new")
		})
	}
}

func TestUnixPublicationFailurePreservesExecutable(t *testing.T) {
	directory := t.TempDir()
	writeExecutable(t, filepath.Join(directory, "jira"), "old", 0o750)
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	out, err := replaceExecutable(t.Context(), false, root.Rename, "missing-stage", "jira", Publication{})
	if !errors.Is(err, os.ErrNotExist) || out.Published || out.Uncertain || out.Backup != "" {
		t.Fatalf("failed replacement = %+v, error %v", out, err)
	}
	assertExecutable(t, filepath.Join(directory, "jira"), "old")
}

func TestWindowsPublicationRollback(t *testing.T) {
	for _, mode := range []string{"success", "publish_failure", "rollback_failure", "canceled_after_backup", "backup_failure"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			writeExecutable(t, filepath.Join(directory, "jira.exe"), "old", 0o750)
			writeExecutable(t, filepath.Join(directory, "stage"), "new", 0o750)
			root, err := os.OpenRoot(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := root.Close(); err != nil {
					t.Error(err)
				}
			}()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("rename unavailable")
			rename := func(old, new string) error {
				if mode == "backup_failure" && old == "jira.exe" {
					return failure
				}
				if (mode == "publish_failure" || mode == "rollback_failure") && old == "stage" {
					return failure
				}
				if mode == "rollback_failure" && old != "jira.exe" {
					return failure
				}
				if err := root.Rename(old, new); err != nil {
					return err
				}
				if mode == "canceled_after_backup" && old == "jira.exe" {
					cancel()
				}
				return nil
			}
			out, err := replaceExecutable(ctx, true, rename, "stage", "jira.exe", Publication{})
			switch mode {
			case "success":
				if err != nil || !out.Published || out.Durable || out.Uncertain || out.Backup == "" {
					t.Fatalf("publication = %+v, error %v", out, err)
				}
				assertExecutable(t, filepath.Join(directory, "jira.exe"), "new")
				assertExecutable(t, filepath.Join(directory, out.Backup), "old")
			case "rollback_failure":
				if !errors.Is(err, failure) || out.Published || !out.Uncertain || out.Backup == "" {
					t.Fatalf("failed rollback = %+v, error %v", out, err)
				}
				assertExecutable(t, filepath.Join(directory, out.Backup), "old")
				if _, err := os.Stat(filepath.Join(directory, "jira.exe")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unexpected executable after failed rollback: %v", err)
				}
			default:
				if err == nil || out.Published || out.Uncertain || out.Backup != "" {
					t.Fatalf("restored publication = %+v, error %v", out, err)
				}
				if mode == "canceled_after_backup" && !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation cause: %v", err)
				}
				assertExecutable(t, filepath.Join(directory, "jira.exe"), "old")
			}
		})
	}
}

func TestPublishKeepsReservedFiles(t *testing.T) {
	path, target, candidate, digest := publicationFixture(t)
	directory := filepath.Dir(path)
	for _, name := range []string{".jira.old", ".jira.new"} {
		writeExecutable(t, filepath.Join(directory, name), "existing recovery artifact", 0o600)
	}
	if _, err := target.Publish(t.Context(), candidate, digest); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".jira.old", ".jira.new"} {
		assertExecutable(t, filepath.Join(directory, name), "existing recovery artifact")
	}
}

func TestPublishRetainsDirectoryAcrossRename(t *testing.T) {
	if !xos.IsUnix() {
		t.Skip("directory descriptor rename semantics require Unix")
	}
	path, target, candidate, digest := publicationFixture(t)
	directory := filepath.Dir(path)
	moved := directory + "-moved"
	if err := os.Rename(directory, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(moved) })
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, path, "unrelated replacement directory", 0o750)
	out, err := target.Publish(t.Context(), candidate, digest)
	if err != nil || !out.Published {
		t.Fatalf("retained-directory publication = %+v, error %v", out, err)
	}
	assertExecutable(t, path, "unrelated replacement directory")
	assertExecutable(t, filepath.Join(moved, "jira"), "updated executable")
}
