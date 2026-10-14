//go:build windows && nativefilesystem

package nativepath

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/matcra587/jira-cli/internal/testfixture"
)

func TestLiteralFileReparseEntriesRefuseTraversal(t *testing.T) {
	for _, name := range []string{"link.", "link "} {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			parent := t.TempDir()
			outside := t.TempDir()
			const unchanged = "owned target marker\n"
			marker := filepath.Join(outside, "guard.txt")
			if err := os.WriteFile(marker, []byte(unchanged), 0o600); err != nil {
				t.Fatal(err)
			}
			ordinary := filepath.Join(parent, "junction")
			before := testfixture.Junction(t, ordinary, outside)
			literal := `\\?\` + filepath.Join(parent, name)
			if err := os.Rename(ordinary, literal); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Remove(literal); err != nil {
					t.Error(err)
				}
			})
			root, err := os.OpenRoot(parent)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := root.Close(); err != nil {
					t.Error(err)
				}
			}()
			if info, err := Lstat(root, name); err != nil || info.Mode().IsRegular() {
				t.Fatalf("literal lstat did not inspect the reparse entry itself: %v %v", info, err)
			}
			if _, err := Stat(root, name); err == nil {
				t.Fatal("literal metadata followed a reparse entry")
			}
			if file, err := CreateFile(root, name, true, 0o600); err == nil {
				if closeErr := file.Close(); closeErr != nil {
					t.Error(closeErr)
				}
				t.Fatal("literal create followed a reparse entry")
			}
			if err := Rename(root, name, "moved."); err == nil {
				t.Fatal("literal rename admitted a directory reparse source")
			}
			if err := Remove(root, name); err == nil {
				t.Fatal("literal file removal admitted a directory reparse entry")
			}
			testfixture.SameJunction(t, literal, outside, before)
			if got, err := os.ReadFile(marker); err != nil || string(got) != unchanged {
				t.Fatalf("literal operation changed its reparse target: %q %v", got, err)
			}
		})
	}
}
