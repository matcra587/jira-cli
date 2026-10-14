package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gechr/x/shell"

	"github.com/matcra587/jira-cli/internal/cli"
	cfg "github.com/matcra587/jira-cli/internal/config"
)

func TestEditCancellationRefusesSaveAndPreservesWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	const original = "read_only = false\n"
	const edited = "read_only = true\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := editFile(ctx, path, func(scratch string) error {
		if err := os.WriteFile(scratch, []byte(edited), 0o600); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || cli.ExitCode(err) != cli.ExitCanceled {
		t.Fatalf("cancellation classification lost: %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil || string(after) != original {
		t.Fatalf("canceled edit published: %q, %v", after, readErr)
	}
	assertRejectedEdit(t, path, edited)
}

func TestEditScratchFailureKeepsNativeEditorAndContextCauses(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{"success", "failure", "canceled_success", "canceled_failure"} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "settings.toml")
			const original = "read_only=false\n"
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			editorErr := cli.NewInputError(cli.CodeEditorFailed, "editor failed after removing its scratch file")
			var scratchPath string
			_, err := editFile(ctx, path, func(scratch string) error {
				scratchPath = scratch
				if err := os.Remove(scratch); err != nil {
					return err
				}
				if strings.HasPrefix(outcome, "canceled_") {
					cancel()
				}
				if strings.HasSuffix(outcome, "failure") {
					return editorErr
				}
				return nil
			})
			var native *os.PathError
			if !errors.Is(err, os.ErrNotExist) || !errors.As(err, &native) || native.Path != scratchPath || native.Op != "open" {
				t.Fatalf("scratch read lost its native cause: %v", err)
			}
			wantCode := cli.CodeLocalIO
			if strings.HasSuffix(outcome, "failure") {
				wantCode = cli.CodeEditorFailed
				if !errors.Is(err, editorErr) {
					t.Fatalf("scratch read lost editor failure: %v", err)
				}
			}
			if strings.HasPrefix(outcome, "canceled_") {
				wantCode = cli.CodeCanceled
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("scratch read lost cancellation: %v", err)
				}
			}
			if cli.MapError(err).Code != wantCode {
				t.Fatalf("scratch read lost its actionable category: %v", err)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil || string(after) != original {
				t.Fatalf("scratch read failure changed settings: %q, %v", after, readErr)
			}
		})
	}
}

func TestEditCanceledFailedEditorPreservesRegularSavedWork(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "settings.toml")
	const original = "read_only=false\n"
	const edited = "# saved despite editor failure\nread_only=true\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	editorErr := cli.NewInputError(cli.CodeEditorFailed, "editor failed after saving")
	_, err := editFile(ctx, path, func(scratch string) error {
		if err := os.WriteFile(scratch, []byte(edited), 0o600); err != nil {
			return err
		}
		cancel()
		return editorErr
	})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, editorErr) || cli.MapError(err).Code != cli.CodeCanceled {
		t.Fatalf("failed editor lost cancellation or its original error: %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil || string(after) != original {
		t.Fatalf("canceled failed editor published settings: %q, %v", after, readErr)
	}
	assertRejectedEdit(t, path, edited)
}

func TestCanceledEditNeverInvokesEditorOrCreatesDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "config.toml")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	_, err := editFile(ctx, path, func(string) error { called = true; return nil })
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("canceled edit ran: called=%v, err=%v", called, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled edit created directory: %v", err)
	}
}

func TestEditCanRepairAnInvalidExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("base_url = 'invalid'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const repaired = "# repaired settings\nread_only = true\n"
	out, err := editFile(t.Context(), path, replaceWith(t, repaired))
	if err != nil || !out.Changed || out.Created {
		t.Fatalf("repair failed: %#v, %v", out, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != repaired {
		t.Fatalf("repair lost authored bytes: %q, %v", after, err)
	}
}

func TestUnchangedInvalidEditLeavesEarlierRecoveryUntouched(t *testing.T) {
	for _, recoveryExists := range []bool{false, true} {
		t.Run(fmt.Sprint(recoveryExists), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			const invalid = "base_url = 'invalid'\n"
			const earlier = "# earlier unsaved work\nread_only=true\n"
			if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
				t.Fatal(err)
			}
			if recoveryExists {
				if err := os.WriteFile(path+".rejected", []byte(earlier), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			out, err := editFile(t.Context(), path, func(string) error { return nil })
			if editErrorCode(err) != cli.CodeConfigInvalid || out.Changed || out.Created {
				t.Fatalf("unchanged invalid result: %#v, %v", out, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != invalid {
				t.Fatalf("unchanged invalid config changed: %q, %v", after, err)
			}
			data, err := os.ReadFile(path + ".rejected")
			if recoveryExists {
				if err != nil || string(data) != earlier {
					t.Fatalf("earlier recovery overwritten: %q, %v", data, err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unchanged invalid config created recovery: %q, %v", data, err)
			}
		})
	}
}

func TestEditPreviewUsesSelectedConfig(t *testing.T) {
	selected := filepath.Join(t.TempDir(), "selected.toml")
	cfg.SetFlagLayer(cfg.FlagLayer{Path: selected})
	t.Cleanup(func() { cfg.SetFlagLayer(cfg.FlagLayer{}) })
	editor, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("JIRA_EDITOR", shell.Quote(editor))
	out, err := runEdit(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Path != selected || !out.DryRun || len(out.Editor) != 1 || out.Editor[0] != editor {
		t.Fatalf("preview = %#v, want selected path %q, literal editor %q and dry-run", out, selected, editor)
	}
	if _, err := os.Stat(selected); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview touched selected file: %v", err)
	}
}

func TestEditRefusesRemovedFileAndPreservesWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("read_only = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const edited = "read_only = true\n"
	_, err := editFile(t.Context(), path, func(scratch string) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		return os.WriteFile(scratch, []byte(edited), 0o600)
	})
	if editErrorCode(err) != cli.CodeConfigChanged {
		t.Fatalf("removed file accepted: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed config recreated: %v", err)
	}
	assertRejectedEdit(t, path, edited)
}

func TestEditPreservesConflictingWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("read_only = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const edited = "read_only = true\n"
	const concurrent = "theme = \"other\"\n"
	_, err := editFile(t.Context(), path, func(scratch string) error {
		if err := os.WriteFile(path, []byte(concurrent), 0o600); err != nil {
			return err
		}
		return os.WriteFile(scratch, []byte(edited), 0o600)
	})
	if editErrorCode(err) != cli.CodeConfigChanged {
		t.Fatalf("conflict error = %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil || string(after) != concurrent {
		t.Fatalf("concurrent content lost: %q, %v", after, readErr)
	}
	assertRejectedEdit(t, path, edited)
}

func TestEditRefusesRedirectedSymlink(t *testing.T) {
	dir := t.TempDir()
	first, second, link := filepath.Join(dir, "first.toml"), filepath.Join(dir, "second.toml"), filepath.Join(dir, "config.toml")
	const original = "read_only = false\n"
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(first, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := editFile(t.Context(), link, func(scratch string) error {
		if err := os.Remove(link); err != nil {
			return err
		}
		if err := os.Symlink(second, link); err != nil {
			return err
		}
		return os.WriteFile(scratch, []byte("read_only = true\n"), 0o600)
	})
	if editErrorCode(err) != cli.CodeConfigChanged {
		t.Fatalf("redirected link accepted: %v", err)
	}
	for _, path := range []string{first, second} {
		after, err := os.ReadFile(path)
		if err != nil || string(after) != original {
			t.Fatalf("target changed: %q, %v", after, err)
		}
	}
}

func TestEditRefusesDanglingSymlinkBeforeEditor(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "config.toml")
	target := filepath.Join(dir, "missing.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	called := false
	_, err := editFile(t.Context(), link, func(scratch string) error {
		called = true
		return os.WriteFile(scratch, []byte("read_only = true\n"), 0o600)
	})
	if err == nil || called {
		t.Fatalf("dangling link accepted: called=%v, err=%v", called, err)
	}
	got, err := os.Readlink(link)
	if err != nil || got != target {
		t.Fatalf("dangling link detached: %q, %v", got, err)
	}
}

func TestEditPreservesSharedDirectoryPermissions(t *testing.T) {
	for _, contents := range []string{"read_only = true\n", "base_url = \"invalid\"\n"} {
		t.Run(contents, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0o750); err != nil { //nolint:gosec // fixture pins preservation of a shared directory's permissions
				t.Fatal(err)
			}
			before, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "config.toml")
			out, editErr := editFile(t.Context(), path, replaceWith(t, contents))
			if strings.Contains(contents, "invalid") {
				if editErrorCode(editErr) != cli.CodeConfigInvalid || out.Changed || out.Created {
					t.Fatalf("invalid edit outcome: %#v, %v", out, editErr)
				}
				assertRejectedEdit(t, path, contents)
			} else {
				if editErr != nil || !out.Changed || !out.Created {
					t.Fatalf("valid edit did not publish: %#v, %v", out, editErr)
				}
				data, err := os.ReadFile(path)
				if err != nil || string(data) != contents {
					t.Fatalf("valid edit bytes lost: %q, %v", data, err)
				}
			}
			after, err := os.Stat(dir)
			if err != nil || !after.IsDir() {
				t.Fatalf("shared directory was not retained: %v, %v", after, err)
			}
			assertEditPOSIXPermissions(t, after.Mode().Perm(), before.Mode().Perm())
		})
	}
}

func TestRejectedEditDoesNotFollowExistingSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	other := filepath.Join(dir, "other.txt")
	const untouched = "unrelated content"
	if err := os.WriteFile(other, []byte(untouched), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, path+".rejected"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	const edited = "base_url = \"invalid\"\n"
	_, err := editFile(t.Context(), path, replaceWith(t, edited))
	if editErrorCode(err) != cli.CodeConfigInvalid {
		t.Fatalf("invalid edit error: %v", err)
	}
	after, err := os.ReadFile(other)
	if err != nil || string(after) != untouched {
		t.Fatalf("rejected edit overwrote unrelated target: %q, %v", after, err)
	}
	assertRejectedEdit(t, path, edited)
}

func assertRejectedEdit(t *testing.T, path, contents string) {
	t.Helper()
	after, err := os.ReadFile(path + ".rejected")
	if err != nil || string(after) != contents {
		t.Fatalf("edited work lost: %q, %v", after, err)
	}
	info, err := os.Stat(path + ".rejected")
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("rejected work is not a regular file: %v, %v", info, err)
	}
	assertEditPOSIXPermissions(t, info.Mode().Perm(), 0o600)
}

func editErrorCode(err error) string {
	input, ok := errors.AsType[*cli.InputError](err)
	if !ok {
		return ""
	}
	return input.Code
}
