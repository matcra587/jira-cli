package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileNoopPreservesMetadataWithoutTrustingInvalidBytes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		data    string
		invalid bool
	}{
		{name: "valid", data: "# keep formatting\nread_only=true\n"},
		{name: "invalid", data: "base_url='invalid'\n", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			old := time.Unix(946684800, 0)
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			file, err := OpenFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			changed, err := file.Save(t.Context(), []byte(tc.data))
			if changed || (err != nil) != tc.invalid || (tc.invalid && !errors.Is(err, ErrFileInvalid)) {
				t.Fatalf("no-op accepted invalid bytes or reported a change: changed=%v, err=%v", changed, err)
			}
			after, err := os.Stat(path)
			if err != nil || !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() {
				t.Fatalf("no-op changed file metadata: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != tc.data {
				t.Fatalf("no-op changed bytes: %q, %v", data, err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "missing", "config.toml")
	file, err := OpenFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := file.Save(t.Context(), nil)
	if err != nil || changed {
		t.Fatalf("empty missing snapshot saved: changed=%v, err=%v", changed, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no-op created directory: %v", err)
	}
}

func TestFileRepairKeepsAuthoredBytesAndSnapshotOwnership(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	const invalid = "base_url = 'invalid'\n"
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := OpenFile(t.Context(), path)
	if err != nil || !file.Exists() || file.Path() != path {
		t.Fatalf("cannot open invalid file for repair: %v", err)
	}
	copy := file.Contents()
	copy[0] = '!'
	if string(file.Contents()) != invalid {
		t.Fatal("caller changed the original snapshot through its byte copy")
	}
	const repaired = "# authored comment\nread_only=true\n[aliases]\n'my.inbox' = 'version'\n"
	changed, err := file.Save(t.Context(), []byte(repaired))
	if err != nil || !changed {
		t.Fatalf("valid repair refused: changed=%v, err=%v", changed, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != repaired {
		t.Fatalf("repair reformatted authored bytes: %q, %v", after, err)
	}
	if string(file.Contents()) != invalid {
		t.Fatal("save changed the immutable snapshot")
	}
}

func TestFileInvalidSaveCannotBeMaskedByEnvironment(t *testing.T) {
	t.Setenv("JIRA_BASE_URL", "https://example.invalid")
	t.Setenv("JIRA_GLYPHS", "default")
	for _, data := range []string{"base_url='invalid'\n", "glyphs='unknown'\n", "unknown=true\n", "workday_seconds=0\n", "read_only='perhaps'\n", "[aliases]\ninbox=true\n", "timeout='invalid'\n", "["} {
		t.Run(data, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing", "config.toml")
			file, err := OpenFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			changed, err := file.Save(t.Context(), []byte(data))
			if changed || !errors.Is(err, ErrFileInvalid) {
				t.Fatalf("invalid file accepted: changed=%v, err=%v", changed, err)
			}
			if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid candidate created a directory: %v", err)
			}
		})
	}
}

func TestFileCanceledOpenAndSaveLeaveMissingPathAlone(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing", "config.toml")
	file, err := OpenFile(t.Context(), path)
	if err != nil || file.Exists() || len(file.Contents()) != 0 {
		t.Fatalf("missing snapshot failed: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := OpenFile(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open error: %v", err)
	}
	changed, err := file.Save(ctx, []byte("read_only=true\n"))
	if changed || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled save error: changed=%v, err=%v", changed, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled operation created a directory: %v", err)
	}
}
