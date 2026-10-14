//go:build windows && nativefilesystem

package config_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/testfixture"
)

func TestNativeWindowsConfigurationJunctions(t *testing.T) {
	for _, suffix := range []string{".", " "} {
		for _, existing := range []bool{false, true} {
			state := "absent"
			if existing {
				state = "existing"
			}
			t.Run("literal "+state+" suffix "+suffix, func(t *testing.T) {
				dir := t.TempDir()
				ordinary := filepath.Join(dir, "settings.toml")
				if err := os.WriteFile(ordinary, []byte("read_only=false\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				literal := `\\?\` + ordinary + suffix
				if _, err := os.Lstat(literal); !os.IsNotExist(err) {
					t.Fatalf("literal fixture was not initially absent: %v", err)
				}
				t.Cleanup(func() {
					if err := os.Remove(literal); err != nil && !os.IsNotExist(err) {
						t.Error(err)
					}
				})
				const authored = "read_only=true\n"
				want := authored
				if existing {
					if err := os.WriteFile(literal, []byte(authored), 0o600); err != nil {
						t.Fatal(err)
					}
					file, err := config.OpenFile(t.Context(), literal)
					if err != nil || file.Path() != literal || !bytes.Equal(file.Contents(), []byte(authored)) {
						t.Fatalf("literal configuration read selected another name: file=%v error=%v", file, err)
					}
					want = "# updated literal\nread_only=true\n"
					if changed, err := file.Save(t.Context(), []byte(want)); err != nil || !changed {
						t.Fatalf("literal configuration save failed: changed=%v error=%v", changed, err)
					}
				} else {
					file, err := config.OpenNewFile(t.Context(), literal)
					if err != nil || file.Path() != literal {
						t.Fatalf("literal creation selected another name: file=%v error=%v", file, err)
					}
					if created, err := file.Create(t.Context(), []byte(authored)); err != nil || !created {
						t.Fatalf("literal configuration creation failed: created=%v error=%v", created, err)
					}
				}
				literalBytes, err := os.ReadFile(literal)
				if err != nil || string(literalBytes) != want {
					t.Fatalf("literal operation lost authored bytes: %q %v", literalBytes, err)
				}
				got, err := os.ReadFile(ordinary)
				if err != nil || string(got) != "read_only=false\n" {
					t.Fatalf("literal operation changed ordinary sibling: %q %v", got, err)
				}
			})
		}
	}
	t.Run("long target", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, strings.Repeat("a", 70), strings.Repeat("b", 70), strings.Repeat("c", 70))
		if err := os.MkdirAll(target, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "selected")
		before := testfixture.Junction(t, link, target)
		want := filepath.Join(target, "settings.toml")
		file, err := config.OpenNewFile(t.Context(), filepath.Join(link, "settings.toml"))
		if err != nil || file.Path() != want {
			t.Fatalf("long junction target was not canonicalized: file=%v error=%v", file, err)
		}
		const authored = "read_only=true\n"
		if created, err := file.Create(t.Context(), []byte(authored)); err != nil || !created {
			t.Fatalf("long canonical target could not be published: created=%v error=%v", created, err)
		}
		got, err := os.ReadFile(want)
		if err != nil || string(got) != authored {
			t.Fatalf("long target lost authored bytes: %q %v", got, err)
		}
		testfixture.SameJunction(t, link, target, before)
	})
	t.Run("missing parents", func(t *testing.T) {
		dir := t.TempDir()
		target, link := filepath.Join(dir, "actual '古い"), filepath.Join(dir, "selected 'é")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		before := testfixture.Junction(t, link, target)
		path := filepath.Join(link, "missing", "nested", "config.toml")
		want := filepath.Join(target, "missing", "nested", "config.toml")
		file, err := config.OpenNewFile(t.Context(), path)
		if err != nil || file.Path() != want {
			t.Fatalf("junction ancestor was not canonicalized: file=%v error=%v", file, err)
		}
		if _, err := os.Stat(filepath.Dir(want)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("preparation published missing directories: %v", err)
		}
		const authored = "# retained exact spelling\nread_only=true\n"
		created, err := file.Create(t.Context(), []byte(authored))
		if !created || err != nil {
			t.Fatalf("junction creation failed: created=%v error=%v", created, err)
		}
		got, err := os.ReadFile(want)
		if err != nil || string(got) != authored {
			t.Fatalf("canonical target lost authored bytes: %q %v", got, err)
		}
		testfixture.SameJunction(t, link, target, before)
	})
	t.Run("redirected parent", func(t *testing.T) {
		dir := t.TempDir()
		first, second, link := filepath.Join(dir, "first"), filepath.Join(dir, "second"), filepath.Join(dir, "selected")
		for _, path := range []string{first, second} {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		testfixture.Junction(t, link, first)
		file, err := config.OpenNewFile(t.Context(), filepath.Join(link, "config.toml"))
		if err != nil || file.Path() != filepath.Join(first, "config.toml") {
			t.Fatalf("junction parent was not selected: %v", err)
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
		before := testfixture.Junction(t, link, second)
		created, err := file.Create(t.Context(), []byte("read_only=true\n"))
		if created || !errors.Is(err, config.ErrFileChanged) {
			t.Fatalf("redirected junction authorized publication: created=%v error=%v", created, err)
		}
		for _, path := range []string{first, second} {
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) != 0 {
				t.Fatalf("refused creation changed a target: %s %v %v", path, entries, err)
			}
		}
		testfixture.SameJunction(t, link, second, before)
	})
	for _, extended := range []bool{false, true} {
		name := "recover absent target"
		if extended {
			name += " extended"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			actual, link := filepath.Join(dir, "actual"), filepath.Join(dir, "selected")
			if err := os.Mkdir(actual, 0o700); err != nil {
				t.Fatal(err)
			}
			before := testfixture.Junction(t, link, actual)
			source := filepath.Join(dir, "old.toml")
			original := []byte(legacyRecoveryFixture)
			if err := os.WriteFile(source, original, 0o600); err != nil {
				t.Fatal(err)
			}
			selected := filepath.Join(link, "missing", "nested", "new.toml")
			if extended {
				selected = `\\?\` + selected
			}
			plan := recoveryPlan(t, source, selected)
			if _, err := os.Stat(filepath.Join(actual, "missing")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("recovery preparation created target directories: %v", err)
			}
			recovery, created, err := plan.Create(t.Context(), filepath.Join(dir, "record.json"))
			if err != nil || !created {
				t.Fatalf("recovery record was not published: created=%v error=%v", created, err)
			}
			if changed, err := recovery.ApplyConfig(t.Context()); err != nil || !changed {
				t.Fatalf("canonical target publication failed: changed=%v error=%v", changed, err)
			}
			path := filepath.Join(actual, "missing", "nested", "new.toml")
			got, err := os.ReadFile(path)
			if err != nil || config.ValidateFile(got) != nil {
				t.Fatalf("junction target did not receive a valid candidate: %v", err)
			}
			if changed, err := recovery.RestoreConfig(t.Context()); err != nil || !changed {
				t.Fatalf("canonical target rollback failed: changed=%v error=%v", changed, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rollback lost target absence: %v", err)
			}
			got, err = os.ReadFile(source)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatalf("rollback changed complete legacy source: %v", err)
			}
			testfixture.SameJunction(t, link, actual, before)
		})
	}
}
