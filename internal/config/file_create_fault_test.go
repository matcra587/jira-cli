package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNewFilePublicationFailureDoesNotExposePartialDestination(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{errors.ErrUnsupported, os.ErrPermission} {
		t.Run(cause.Error(), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			file, err := OpenNewFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			attempted := false
			created, err := file.create(t.Context(), []byte("read_only=true\n"), fileCreationOps{
				resolve: resolveCreationPath,
				link: func(staged, selected string) error {
					attempted = true
					data, err := os.ReadFile(staged)
					if err != nil || string(data) != "read_only=true\n" || selected != path {
						t.Fatalf("publication attempted incomplete or misdirected data: %q %q %v", data, selected, err)
					}
					if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("destination was exposed before publication: %v", err)
					}
					return cause
				},
				syncDirectory: func(string) error {
					t.Error("failed publication attempted directory sync")
					return nil
				},
			})
			if created || !errors.Is(err, cause) || !attempted {
				t.Fatalf("publication failure lost its cause or claimed creation: created=%v attempted=%v err=%v", created, attempted, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("publication failure left a destination or staging files: %v %v", entries, err)
			}
		})
	}
}

func TestNewFileDirectorySyncFailureRetainsPublishedOutcome(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	file, err := OpenNewFile(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("injected directory sync failure")
	synced := false
	const data = "# complete authored candidate\nread_only=true\n"
	created, err := file.create(t.Context(), []byte(data), fileCreationOps{
		resolve: resolveCreationPath, link: os.Link,
		syncDirectory: func(parent string) error {
			synced = true
			contents, err := os.ReadFile(path)
			if parent != dir || err != nil || string(contents) != data {
				t.Fatalf("directory sync did not follow complete publication: %q %q %v", parent, contents, err)
			}
			return cause
		},
	})
	if !created || !errors.Is(err, cause) || !synced {
		t.Fatalf("directory sync failure hid publication: created=%v synced=%v err=%v", created, synced, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != data {
		t.Fatalf("directory sync failure rolled back completed bytes: %q %v", contents, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "config.toml" {
		t.Fatalf("directory sync failure retained staging files: %v %v", entries, err)
	}
}

func TestNewFileCancellationDuringTargetInspectionRefusesSideEffects(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"before preparation", "before publication"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			parent := filepath.Join(dir, "missing")
			path := filepath.Join(parent, "config.toml")
			file, err := OpenNewFile(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			canceled := false
			created, err := file.create(ctx, []byte("read_only=true\n"), fileCreationOps{
				resolve: func(requested string) (string, error) {
					resolved, err := resolveCreationPath(requested)
					entries, inspectErr := os.ReadDir(parent)
					if inspectErr != nil && !errors.Is(inspectErr, os.ErrNotExist) {
						t.Fatal(inspectErr)
					}
					if stage == "before preparation" || len(entries) > 0 {
						cancel()
						canceled = true
					}
					return resolved, err
				},
				link: os.Link, syncDirectory: syncCreationDirectory,
			})
			if created || !errors.Is(err, context.Canceled) || !canceled || !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatalf("canceled inspection accepted creation: created=%v canceled=%v err=%v", created, canceled, err)
			}
			entries, err := os.ReadDir(parent)
			if stage == "before preparation" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("canceled preparation created its parent: %v", err)
				}
			} else if err != nil || len(entries) != 0 {
				t.Fatalf("canceled publication left a destination or staging files: %v %v", entries, err)
			}
		})
	}
}
