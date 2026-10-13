package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestCredentialBackendTextRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		input string
		want  core.CredentialBackend
	}{
		{name: "default", want: core.CredentialBackendKeyring},
		{name: "keyring", input: "keyring", want: core.CredentialBackendKeyring},
		{name: "environment", input: "env", want: core.CredentialBackendEnvironment},
		{name: "onepassword", input: "1password", want: core.CredentialBackendOnePassword},
		{name: "normalized", input: " \t1PASSWORD\n", want: core.CredentialBackendOnePassword},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var backend core.CredentialBackend
			if err := backend.UnmarshalText([]byte(tc.input)); err != nil || backend != tc.want {
				t.Fatalf("decode backend: value=%q error=%v want=%q", backend, err, tc.want)
			}
			encoded, err := backend.MarshalText()
			if err != nil || string(encoded) != string(tc.want) {
				t.Fatalf("marshal backend: value=%q error=%v want=%q", encoded, err, tc.want)
			}
			var decoded core.CredentialBackend
			if err := decoded.UnmarshalText(encoded); err != nil || decoded != tc.want {
				t.Fatalf("backend round trip: value=%q error=%v want=%q", decoded, err, tc.want)
			}
		})
	}
}

func TestCredentialBackendRejectsUnknownWithoutDisclosingInput(t *testing.T) {
	t.Parallel()
	const rejected = "synthetic-private-backend-coordinate"
	backend := core.CredentialBackendEnvironment
	err := backend.UnmarshalText([]byte(rejected))
	if err == nil || backend != core.CredentialBackendEnvironment {
		t.Fatalf("invalid backend accepted or changed prior value: backend=%q error=%v", backend, err)
	}
	if strings.Contains(err.Error(), rejected) {
		t.Fatal("invalid backend error disclosed supplied input")
	}
	if encoded, err := json.Marshal(core.CredentialBackendOnePassword); err != nil || string(encoded) != `"1password"` {
		t.Fatalf("backend JSON spelling changed: value=%s error=%v", encoded, err)
	}
}

func TestCredentialStoreErrorSanitizesCauseAndPreservesIdentity(t *testing.T) {
	t.Parallel()
	for _, backend := range []core.CredentialBackend{core.CredentialBackendKeyring, core.CredentialBackendEnvironment, core.CredentialBackendOnePassword} {
		t.Run(string(backend), func(t *testing.T) {
			t.Parallel()
			cause := errors.New("synthetic-private-provider-token")
			provider := &core.CredentialStoreError{Backend: backend, Operation: "save", Cause: cause, Attempted: true}
			wrapped := fmt.Errorf("credential operation: %w", provider)
			if provider.Error() != string(backend)+" credential save failed" || strings.Contains(wrapped.Error(), cause.Error()) {
				t.Fatal("provider error disclosed its private cause or lost public operation")
			}
			var recovered *core.CredentialStoreError
			if !errors.Is(wrapped, cause) || !errors.As(wrapped, &recovered) || recovered != provider || !recovered.Attempted {
				t.Fatal("sanitized error lost inspectable cause, identity or attempt state")
			}
		})
	}
}

func TestEnvironmentCredentialStoreNeverPersistsTokens(t *testing.T) {
	t.Parallel()
	store := core.EnvironmentCredentialStore{}
	if store.CredentialBackend() != core.CredentialBackendEnvironment {
		t.Fatal("environment store reported another backend")
	}
	secret, err := store.Get(t.Context(), "synthetic-account")
	if secret != "" || !errors.Is(err, core.ErrNoCredential) {
		t.Fatal("environment store invented a stored credential")
	}
	err = store.Set(t.Context(), "synthetic-account", "synthetic-private-token")
	var provider *core.CredentialStoreError
	if !errors.As(err, &provider) || provider.Backend != core.CredentialBackendEnvironment || provider.Operation != "save" || provider.Attempted {
		t.Fatalf("environment persistence refusal lost backend or falsely claimed attempt: %v", err)
	}
	if strings.Contains(err.Error(), "synthetic-account") || strings.Contains(err.Error(), "synthetic-private-token") {
		t.Fatal("environment refusal disclosed account or token")
	}
	if err := store.Delete(t.Context(), "synthetic-account"); err != nil {
		t.Fatalf("logout from storage-free environment backend failed: %v", err)
	}
	if secret, err := store.Get(t.Context(), "synthetic-account"); secret != "" || !errors.Is(err, core.ErrNoCredential) {
		t.Fatal("refused persistence created a credential")
	}
}

func TestEnvironmentCredentialStorePreservesCancellation(t *testing.T) {
	t.Parallel()
	for _, expired := range []bool{false, true} {
		name := "canceled"
		if expired {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if expired {
				var done context.CancelFunc
				ctx, done = context.WithDeadline(t.Context(), time.Unix(0, 0))
				defer done()
			} else {
				cancel()
			}
			store := core.EnvironmentCredentialStore{}
			secret, readErr := store.Get(ctx, "synthetic-account")
			if secret != "" || !errors.Is(readErr, ctx.Err()) {
				t.Errorf("canceled environment read lost context: has_secret=%t error=%v", secret != "", readErr)
			}
			if err := store.Set(ctx, "synthetic-account", "synthetic-private-token"); !errors.Is(err, ctx.Err()) {
				t.Errorf("canceled environment save lost context: %v", err)
			}
			if err := store.Delete(ctx, "synthetic-account"); !errors.Is(err, ctx.Err()) {
				t.Errorf("canceled environment delete reported success: %v", err)
			}
		})
	}
}
