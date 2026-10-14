package nativepath

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestLiteralFileOperationsKeepSelectedRoot(t *testing.T) {
	for _, name := range []string{"report café.bin.", "report café.bin "} {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			parent := t.TempDir()
			selected := filepath.Join(parent, "selected")
			moved := filepath.Join(parent, "moved")
			if err := os.Mkdir(selected, 0o700); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(selected)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := root.Close(); err != nil {
					t.Error(err)
				}
				if err := os.RemoveAll(`\\?\` + parent); err != nil {
					t.Error(err)
				}
			})
			replacementDirectory := selected
			if err := os.Rename(selected, moved); err != nil {
				if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
					t.Fatal(err)
				}
				t.Log("Native root prevents directory replacement through delete sharing")
				moved = selected
				replacementDirectory = filepath.Join(parent, "unrelated")
			}
			if err := os.Mkdir(replacementDirectory, 0o700); err != nil {
				t.Fatal(err)
			}
			const untouched = "replacement directory must stay unchanged\n"
			replacement := `\\?\` + filepath.Join(replacementDirectory, name)
			if err := os.WriteFile(replacement, []byte(untouched), 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := CreateFile(root, name, false, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			const contents = "selected directory file\n"
			if _, err := file.WriteString(contents); err != nil {
				if closeErr := file.Close(); closeErr != nil {
					t.Error(closeErr)
				}
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			actual := `\\?\` + filepath.Join(moved, name)
			if got, err := os.ReadFile(actual); err != nil || string(got) != contents {
				t.Fatalf("create reopened replacement directory: %q %v", got, err)
			}
			for _, stat := range []func(*os.Root, string) (os.FileInfo, error){Stat, Lstat} {
				info, err := stat(root, name)
				if err != nil || info.Name() != name || info.Size() != int64(len(contents)) {
					t.Fatalf("metadata selected normalized or replacement entry: %v %v", info, err)
				}
			}
			if duplicate, err := CreateFile(root, name, false, 0o600); !errors.Is(err, os.ErrExist) {
				if duplicate != nil {
					if closeErr := duplicate.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				}
				t.Fatalf("exclusive native create lost collision classification: %v", err)
			}
			newname := "renamed file "
			if err := Rename(root, name, newname); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(actual); !os.IsNotExist(err) {
				t.Fatal("rename retained its source entry", err)
			}
			renamed := `\\?\` + filepath.Join(moved, newname)
			if data, err := os.ReadFile(renamed); err != nil || string(data) != contents {
				t.Fatalf("rename selected replacement directory: %q %v", data, err)
			}
			if err := Remove(root, newname); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(renamed); !os.IsNotExist(err) {
				t.Fatal("remove retained its literal entry", err)
			}
			if got, err := os.ReadFile(replacement); err != nil || string(got) != untouched {
				t.Fatalf("operation changed replacement directory: %q %v", got, err)
			}
		})
	}
}

func TestLiteralFileLeavesRejectTraversalDevicesAndStreams(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	for _, name := range []string{".", "..", `..\outside.`, `C:\outside.`, `NUL.`, `file:stream.`, "bad\x00.", "bad\xff.", `bad?.`, `nested\file.`} {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			file, err := CreateFile(root, name, false, 0o600)
			if file != nil {
				if closeErr := file.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			}
			if !errors.Is(err, os.ErrInvalid) {
				t.Fatalf("invalid literal leaf admitted: %v", err)
			}
			if _, err := Lstat(root, name); !errors.Is(err, os.ErrInvalid) {
				t.Fatalf("invalid literal metadata admitted: %v", err)
			}
			if err := Remove(root, name); !errors.Is(err, os.ErrInvalid) {
				t.Fatalf("invalid literal removal admitted: %v", err)
			}
			if err := Rename(root, "stage.tmp", name); !errors.Is(err, os.ErrInvalid) {
				t.Fatalf("invalid literal destination admitted: %v", err)
			}
		})
	}
}

func TestLiteralFilePathsPublishReopenableNativeNames(t *testing.T) {
	for _, tc := range []struct{ directory, name, want string }{
		{`C:\owned`, "plain.bin", `C:\owned\plain.bin`},
		{`C:\owned`, "plain.bin.", `\\?\C:\owned\plain.bin.`},
		{`C:\owned`, "plain.bin ", `\\?\C:\owned\plain.bin `},
		{`\\server\share\owned`, "plain.bin.", `\\?\UNC\server\share\owned\plain.bin.`},
		{`\\?\C:\owned.`, "plain.bin", `\\?\C:\owned.\plain.bin`},
	} {
		t.Run(strings.ReplaceAll(tc.directory+tc.name, `\`, "_"), func(t *testing.T) {
			if got := FilePath(tc.directory, tc.name); got != tc.want {
				t.Fatalf("public native file path = %q, want %q", got, tc.want)
			}
		})
	}
}
