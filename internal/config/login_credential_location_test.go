package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestLoginCredentialLocationIsDurableAndRetainedAfterInterruption(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	original := "format_version=1\ndefault_profile='work'\n[profiles.work]\nbase_url='https://example.invalid'\nemail='person@example.invalid'\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, err := CaptureOptions(t.Context(), Options{Path: path, Environ: func() []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	selected := core.CredentialLocation{Backend: core.CredentialBackendKeyring}
	location, receipt, err := PrepareLoginCredentialLocation(t.Context(), opts, selected)
	if err != nil || !receipt.Saved || !receipt.Changed || location.Account == "" || location.Account == "default" {
		t.Fatalf("locator publication: %+v, %+v, %v", location, receipt, err)
	}
	// A fresh process must discover the location even if login stopped before
	// writing a token or publishing the verified account ID.
	fresh, err := Load(Options{Path: path, Environ: func() []string { return nil }})
	if err != nil || CredentialSelection(*fresh) != location || fresh.Config.AccountID != "" || fresh.Config.APIToken != "" {
		t.Fatalf("fresh process lost reserved public locator: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	again, publication, err := PrepareLoginCredentialLocation(t.Context(), Options{Path: path}, location)
	if err != nil || again != location || publication.Changed || publication.Status != "" {
		t.Fatalf("existing locator was allocated again: %+v, %+v, %v", again, publication, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("existing locator changed file bytes")
	}
}

func TestLoginCredentialLocationRefusesCancellationAndChangedSnapshot(t *testing.T) {
	t.Parallel()
	for _, changed := range []bool{false, true} {
		name := "canceled"
		if changed {
			name = "changed snapshot"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.toml")
			original := "format_version=1\ndefault_profile='work'\n[profiles.work]\nbase_url='https://example.invalid'\nemail='person@example.invalid'\n"
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			opts, err := CaptureOptions(t.Context(), Options{Path: path, Environ: func() []string { return nil }})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := error(context.Canceled)
			if changed {
				original += "default_project='DEMO'\n"
				if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
					t.Fatal(err)
				}
				want = ErrFileChanged
			} else {
				cancel()
			}
			_, receipt, err := PrepareLoginCredentialLocation(ctx, opts, core.CredentialLocation{Backend: core.CredentialBackendKeyring})
			if !errors.Is(err, want) || receipt.Changed || receipt.Saved {
				t.Fatalf("refusal: %+v, %v", receipt, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != original {
				t.Fatal("refused allocation changed config")
			}
		})
	}
}
