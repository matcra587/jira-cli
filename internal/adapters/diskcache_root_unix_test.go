//go:build unix

package adapters

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiskCachePublicationKeepsOpenedNamespace(t *testing.T) {
	t.Parallel()
	for _, replacement := range []string{"directory", "symlink"} {
		for _, failed := range []bool{false, true} {
			name := replacement + "/success"
			if failed {
				name = replacement + "/failed_rename"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				base := t.TempDir()
				original := filepath.Join(base, "cache")
				moved := filepath.Join(base, "opened-cache")
				outside := filepath.Join(base, "outside")
				for _, dir := range []string{original, outside} {
					if err := os.Mkdir(dir, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				cache := DiskCache{dir: original}
				key := "metadata"
				entryName := filepath.Base(cache.path(key))
				if failed {
					if err := os.Mkdir(filepath.Join(original, entryName), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(original, entryName, "keep"), []byte("retained"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				written := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
				var protectedPath string
				calls := 0
				cache.now = func() time.Time {
					calls++
					if err := os.Rename(original, moved); err != nil {
						t.Fatal(err)
					}
					if replacement == "symlink" {
						if err := os.Symlink(outside, original); err != nil {
							t.Fatal(err)
						}
						protectedPath = filepath.Join(outside, entryName)
					} else {
						if err := os.Mkdir(original, 0o700); err != nil {
							t.Fatal(err)
						}
						protectedPath = filepath.Join(original, entryName)
					}
					if err := os.WriteFile(protectedPath, []byte("untouched replacement"), 0o600); err != nil {
						t.Fatal(err)
					}
					return written
				}
				value := []byte("owned publication")
				err := cache.Set(key, value)
				if calls != 1 {
					t.Fatalf("clock called %d times; namespace replacement did not run once", calls)
				}
				protected, readErr := os.ReadFile(protectedPath)
				if readErr != nil || string(protected) != "untouched replacement" {
					t.Fatalf("publication escaped the opened namespace: replacement=%q error=%v", protected, readErr)
				}
				if failed {
					var renameError *os.LinkError
					nativeFailure := nativeCacheRenameFailure(t, entryName)
					if !errors.As(err, &renameError) || renameError.Op != nativeFailure.Op ||
						renameError.New != nativeFailure.New || !errors.Is(err, nativeFailure.Err) {
						t.Fatalf("failed publication lost the native rename cause: %v", err)
					}
					kept, readErr := os.ReadFile(filepath.Join(moved, entryName, "keep"))
					if readErr != nil || string(kept) != "retained" {
						t.Fatalf("failed publication changed the destination: %q, %v", kept, readErr)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					anchored := DiskCache{dir: moved}
					got, stamp, ok := anchored.Get(key)
					if !ok || !bytes.Equal(got, value) || !stamp.Equal(written) {
						t.Fatalf("opened namespace lacks the exact publication: %q, %v, %v", got, stamp, ok)
					}
				}
				entries, readErr := os.ReadDir(moved)
				if readErr != nil || len(entries) != 1 || entries[0].Name() != entryName {
					t.Fatalf("opened namespace contains abandoned temporary files: %v, %v", entries, readErr)
				}
			})
		}
	}
}
