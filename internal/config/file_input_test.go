package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigFileInputPreservesRegularBytesAndNativeCauses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.toml")
	data := []byte("# authored formatting\nread_only=false\n[aliases]\n'my.inbox' = 'version'\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readFileInput(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("regular input changed: data=%q err=%v", got, err)
	}
	missing := filepath.Join(dir, "missing.toml")
	_, err = readFileInput(missing)
	var native *os.PathError
	if !errors.Is(err, os.ErrNotExist) || !errors.As(err, &native) || native.Path != missing || native.Op != "open" {
		t.Fatalf("missing input lost its native cause: %v", err)
	}
	if _, err := readFileInput(dir); !errors.Is(err, ErrFileInvalid) {
		t.Fatalf("directory input was not refused before reading: %v", err)
	}
}

func TestConfigFileInputRejectsDirectoryAcrossOwners(t *testing.T) {
	t.Parallel()
	path := t.TempDir()
	if _, err := Load(Options{Path: path, Environ: func() []string { return nil }}); !errors.Is(err, ErrFileInvalid) {
		t.Fatalf("layered directory input accepted: %v", err)
	}
	if _, err := OpenFile(t.Context(), path); !errors.Is(err, ErrFileInvalid) {
		t.Fatalf("directory snapshot accepted: %v", err)
	}
	file := filepath.Join(t.TempDir(), "settings.toml")
	if err := os.WriteFile(file, []byte("read_only=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := OpenFile(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(file, 0o700); err != nil {
		t.Fatal(err)
	}
	if changed, err := snapshot.Save(t.Context(), []byte("read_only=true\n")); changed || !errors.Is(err, ErrFileInvalid) {
		t.Fatalf("save accepted a substituted directory: changed=%v err=%v", changed, err)
	}
	info, err := os.Stat(file)
	if err != nil || !info.IsDir() {
		t.Fatalf("refusal changed substituted directory: %v", err)
	}
}
