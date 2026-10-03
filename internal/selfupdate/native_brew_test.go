package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xos "github.com/gechr/x/os"
)

// TestMain also supplies a native fake brew executable by copying this test
// binary into an owned PATH. It intercepts only that copy, before test flags
// are parsed, so probes use the production LookPath and subprocess owner.
func TestMain(m *testing.M) {
	if os.Getenv("JIRA_OWNED_BREW_HELPER") == "1" && filepath.Base(os.Args[0]) == "brew" {
		if err := runOwnedBrew(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(17)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runOwnedBrew() error {
	root := os.Getenv("JIRA_OWNED_BREW_ROOT")
	if root == "" || len(os.Args) < 2 {
		return errors.New("missing owned brew root or operation")
	}
	for _, name := range []string{"HOMEBREW_NO_ENV_HINTS", "HOMEBREW_NO_AUTO_UPDATE", "HOMEBREW_NO_INSTALL_CLEANUP"} {
		if os.Getenv(name) != "1" {
			return fmt.Errorf("missing subprocess policy %s", name)
		}
	}
	prefix := filepath.Join(root, "opt", "jira")
	args := os.Args[1:]
	if len(args) != 0 && args[0] != "update" && args[len(args)-1] != "matcra587/tap/jira" {
		return errors.New("unexpected owned formula")
	}
	switch args[0] {
	case "info":
		_, err := fmt.Fprintln(os.Stdout, `{"formulae":[{"full_name":"matcra587/tap/jira","installed":[{"version":"owned"}]}]}`)
		return err
	case "--prefix":
		_, err := fmt.Fprintln(os.Stdout, prefix)
		return err
	case "list":
		keg, err := filepath.EvalSymlinks(prefix)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(os.Stdout, filepath.Join(keg, "bin", "jira"))
		return err
	case "update":
		_, err := fmt.Fprintln(os.Stdout, "owned brew refresh output")
		return err
	case "upgrade":
		keg := filepath.Join(root, "Cellar", "jira", "1.2.3")
		if err := os.MkdirAll(filepath.Join(keg, "bin"), 0o700); err != nil {
			return err
		}
		binary := filepath.Join(keg, "bin", "jira")
		if err := xos.CopyFile(os.Getenv("JIRA_OWNED_BREW_CANDIDATE"), binary); err != nil {
			return err
		}
		if err := os.Chmod(binary, 0o700); err != nil { //nolint:gosec // owned executable fixture requires its owner execute bit
			return err
		}
		staged := filepath.Join(root, "opt", "jira.next")
		if err := os.Symlink(keg, staged); err != nil {
			return err
		}
		if err := os.Rename(staged, prefix); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(os.Stdout, "owned brew upgrade output"); err != nil {
			return err
		}
		switch os.Getenv("JIRA_OWNED_BREW_OUTCOME") {
		case "failure":
			return errors.New("owned failure after publication")
		case "deadline":
			time.Sleep(time.Minute)
		}
		return nil
	default:
		return fmt.Errorf("unexpected owned operation %s", args[0])
	}
}

func TestNativeBrewUpdatePublication(t *testing.T) {
	original := os.Getenv("JIRA_NATIVE_UPDATE_ORIGINAL")
	candidate := os.Getenv("JIRA_NATIVE_UPDATE_CANDIDATE")
	if original == "" || candidate == "" {
		t.Skip("requires externally built original and candidate CLI binaries")
	}
	if xos.IsWindows() {
		t.Skip("Homebrew native process fixture requires Unix symlinks")
	}
	for _, mode := range []string{"success", "failure", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			oldKeg := filepath.Join(root, "Cellar", "jira", "0.15.3")
			for _, dir := range []string{bin, filepath.Join(root, "opt"), filepath.Join(oldKeg, "bin")} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			testBinary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			brewPath := filepath.Join(bin, "brew")
			oldBinary := filepath.Join(oldKeg, "bin", "jira")
			shadow := filepath.Join(bin, "jira")
			for _, pair := range []struct{ source, destination string }{
				{source: testBinary, destination: brewPath}, {source: original, destination: oldBinary}, {source: original, destination: shadow},
			} {
				if err := xos.CopyFile(pair.source, pair.destination); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(pair.destination, 0o700); err != nil { //nolint:gosec // owned executable fixtures require their owner execute bit
					t.Fatal(err)
				}
			}
			if err := os.Symlink(oldKeg, filepath.Join(root, "opt", "jira")); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("JIRA_OWNED_BREW_HELPER", "1")
			t.Setenv("JIRA_OWNED_BREW_ROOT", root)
			t.Setenv("JIRA_OWNED_BREW_CANDIDATE", candidate)
			t.Setenv("JIRA_OWNED_BREW_OUTCOME", mode)
			t.Setenv("JIRA_NO_UPDATE_CHECK", "1")
			brew, err := OpenBrew(t.Context(), oldBinary)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := OpenBrew(t.Context(), shadow); !errors.Is(err, ErrBrewOwnership) {
				t.Fatalf("stray installation accepted: %v", err)
			}
			ctx := t.Context()
			if mode == "deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
			}
			out, err := brew.Update(ctx)
			if mode == "success" && err != nil {
				t.Fatal(err)
			}
			if mode == "failure" && (err == nil || !strings.Contains(err.Error(), "owned failure after publication")) {
				t.Fatalf("lost native failure: %v", err)
			}
			if mode == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lost native deadline: %v", err)
			}
			if !out.Attempted || !out.Updated || out.Uncertain || out.After == nil || out.After.Version != "v1.2.3" || out.After.SHA256 == out.Before.SHA256 || out.Shadow != shadow {
				t.Fatalf("native installation receipt = %+v, error = %v", out, err)
			}
			for _, retained := range []string{oldBinary, shadow} {
				target, err := OpenTarget(t.Context(), retained)
				if err != nil {
					t.Fatal(err)
				}
				if digest := fmt.Sprintf("%x", target.digest); digest != out.Before.SHA256 {
					t.Fatalf("retained copy changed: %s", retained)
				}
				if err := target.Close(); err != nil {
					t.Fatal(err)
				}
			}
			data, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("verified native brew publication: %s", data)
		})
	}
}
