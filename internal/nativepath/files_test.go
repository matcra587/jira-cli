package nativepath

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRootedFileOperationsKeepDirectoryIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native literal-file guard separately exercises Windows directory delete sharing")
	}
	parent := t.TempDir()
	selected, moved := filepath.Join(parent, "selected"), filepath.Join(parent, "moved")
	if err := os.Mkdir(selected, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(selected)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := os.Rename(selected, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(selected, 0o700); err != nil {
		t.Fatal(err)
	}
	const original = "replacement directory data"
	if err := os.WriteFile(filepath.Join(selected, "selected.bin"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := CreateFile(root, "selected.bin", false, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	const contents = "rooted file bytes"
	if _, err := file.WriteString(contents); err != nil {
		if closeErr := file.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, stat := range []func(*os.Root, string) (os.FileInfo, error){Stat, Lstat} {
		info, err := stat(root, "selected.bin")
		if err != nil || info.Size() != int64(len(contents)) {
			t.Fatalf("metadata used replaced directory: %v %v", info, err)
		}
	}
	if err := Rename(root, "selected.bin", "renamed.bin"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(moved, "renamed.bin")); err != nil || string(got) != contents {
		t.Fatalf("rename left selected root: %q %v", got, err)
	}
	if err := Remove(root, "renamed.bin"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(moved, "renamed.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rooted removal did not remove selected entry", err)
	}
	if got, err := os.ReadFile(filepath.Join(selected, "selected.bin")); err != nil || string(got) != original {
		t.Fatalf("rooted operations changed replacement directory: %q %v", got, err)
	}
}
