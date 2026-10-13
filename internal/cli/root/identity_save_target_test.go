package root

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
)

func TestIdentitySaveScopeRefusesNonDirectoryAncestorBeforeProvider(t *testing.T) {
	t.Parallel()
	parent := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(parent, []byte("regular ancestor"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "settings.toml")
	providers := 0
	_, err := resolveIdentitySaveSite(t.Context(), config.Options{Path: path, PermitMissingTarget: true, Environ: func() []string {
		return []string{"JIRA_BASE_URL=https://example.invalid", "JIRA_EMAIL=public@example.invalid", "JIRA_API_TOKEN=synthetic-token"}
	}}, func(config.Config) core.CredentialStore {
		providers++
		return nil
	})
	if err == nil || providers != 0 {
		t.Fatalf("invalid target entered credential provider: providers=%d err=%v", providers, err)
	}
	contents, readErr := os.ReadFile(parent)
	if readErr != nil || string(contents) != "regular ancestor" {
		t.Fatal("target refusal changed regular ancestor")
	}
}

func TestIdentitySaveScopeCancellationPrecedesProvider(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := resolveIdentitySaveSite(ctx, config.Options{Path: filepath.Join(t.TempDir(), "settings.toml"), PermitMissingTarget: true}, func(config.Config) core.CredentialStore { t.Fatal("canceled saving opened provider"); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled target resolution lost cause", err)
	}
}
