package config

import (
	"errors"
	"fmt"
	"testing"
)

// CredentialError carries the structured metadata the output layer needs: a broad
// type, a stable normalized code, a display message, a hint, a retryable
// flag, and optional context. Accessors are Code/Hint/Retryable, never
// Get-prefixed.
func TestCredentialErrorExposesStructuredMetadata(t *testing.T) {
	t.Parallel()
	err := &CredentialError{
		Type:        ErrorTypeAuth,
		ErrCode:     ErrorCodeCredentialMissing,
		Message:     "no credential is configured for profile \"work\"",
		HintMsg:     "run jira auth login to store a credential for this profile",
		IsRetryable: false,
		Context:     ErrorContext{Backend: "keyring"},
	}
	if err.Code() != ErrorCodeCredentialMissing {
		t.Fatalf("Code() = %q", err.Code())
	}
	if err.HintMsg == "" {
		t.Fatal("Hint() is empty")
	}
	if err.Retryable() {
		t.Fatal("Retryable() = true, want false")
	}
	if err.Error() == "" {
		t.Fatal("Error() is empty")
	}
}

// The serialized JSON code stays snake_case even though the Go identifier is
// MixedCaps.
func TestErrorCodeSerializesAsSnakeCase(t *testing.T) {
	t.Parallel()
	cases := map[ErrorCode]string{
		ErrorCodeCredentialSourceConflict:     "credential_source_conflict",
		ErrorCodeCredentialMissing:            "credential_missing",
		ErrorCodeCredentialEmpty:              "credential_empty",
		ErrorCodeCredentialBackendUnavailable: "credential_backend_unavailable",
		ErrorCodeCredentialMigrationFailed:    "credential_migration_failed",
		ErrorCodeCredentialCleanupFailed:      "credential_cleanup_failed",
		ErrorCodeCredentialNamespaceCollision: "credential_namespace_collision",
		ErrorCodeOnePasswordItemAmbiguous:     "onepassword_item_ambiguous",
		ErrorCodeOnePasswordUnavailable:       "onepassword_unavailable",
	}
	for code, want := range cases {
		if string(code) != want {
			t.Fatalf("ErrorCode %q serializes as %q, want %q", code, string(code), want)
		}
	}
}

// CredentialError must be recoverable with errors.As and must unwrap to any
// wrapped upstream error so provider-specific callers keep errors.As.
func TestCredentialErrorWrapsUpstream(t *testing.T) {
	t.Parallel()
	upstream := errors.New("op exited with status 1")
	err := fmt.Errorf("context: %w", &CredentialError{
		Type:    ErrorTypeAuth,
		ErrCode: ErrorCodeOnePasswordUnavailable,
		Message: "1Password CLI is unavailable",
		Wrapped: upstream,
	})
	if _, ok := errors.AsType[*CredentialError](err); !ok {
		t.Fatal("errors.As did not recover *CredentialError")
	}
	if !errors.Is(err, upstream) {
		t.Fatal("errors.Is did not reach the wrapped upstream error")
	}
}

// Upstream provider metadata is carried separately from the primary code, not
// collapsed into it.
func TestCredentialErrorCarriesUpstreamProviderMetadata(t *testing.T) {
	t.Parallel()
	err := &CredentialError{
		Type:    ErrorTypeAuth,
		ErrCode: ErrorCodeOnePasswordUnavailable,
		Message: "1Password CLI is unavailable",
		Upstream: &UpstreamProvider{
			Provider:       "onepassword-cli",
			UpstreamCode:   "exit_1",
			UpstreamStatus: 1,
		},
	}
	if err.Code() != ErrorCodeOnePasswordUnavailable {
		t.Fatalf("primary Code() = %q, want onepassword_unavailable", err.Code())
	}
	if err.Upstream == nil || err.Upstream.Provider != "onepassword-cli" {
		t.Fatalf("upstream provider metadata missing: %+v", err.Upstream)
	}
	if err.Upstream.UpstreamStatus != 1 {
		t.Fatalf("upstream status = %d, want 1", err.Upstream.UpstreamStatus)
	}
}

// A namespace-unsafe profile name yields a typed CredentialError with the
// namespace-collision code, recoverable by errors.As — no string matching.
func TestCredentialIdentityRejectionIsTyped(t *testing.T) {
	t.Parallel()
	_, err := CredentialIdentity(Profile{Name: "bad/name", BaseURL: "https://x", SecretBackend: SecretBackendKeyring})
	if err == nil {
		t.Fatal("CredentialIdentity() error = nil for an unsafe name")
	}
	var ce *CredentialError
	if !errors.As(err, &ce) {
		t.Fatalf("namespace rejection is not a *CredentialError: %T", err)
	}
	if ce.Code() != ErrorCodeCredentialNamespaceCollision {
		t.Fatalf("Code() = %q, want credential_namespace_collision", ce.Code())
	}
	if ce.Type != ErrorTypeValidation {
		t.Fatalf("Type = %q, want validation", ce.Type)
	}
}

// An empty credential yields a typed CredentialError with the credential_empty
// code, and the hint must not leak the (empty) secret.
func TestReadSecretEmptyIsTyped(t *testing.T) {
	t.Parallel()
	_, err := ReadSecret("\n")
	if err == nil {
		t.Fatal("ReadSecret() error = nil for an empty credential")
	}
	var ce *CredentialError
	if !errors.As(err, &ce) {
		t.Fatalf("empty-credential rejection is not a *CredentialError: %T", err)
	}
	if ce.Code() != ErrorCodeCredentialEmpty {
		t.Fatalf("Code() = %q, want credential_empty", ce.Code())
	}
}

// classifyCredentialError replaces the strings.Contains classifier: it must
// branch on typed errors, not error message substrings. A keyring backend
// failure becomes a backend-unavailable CredentialError.
func TestClassifyCredentialErrorIsTyped(t *testing.T) {
	t.Parallel()
	backendFail := fmt.Errorf("keyring get %q: %w", "work", errors.New("dbus unavailable"))
	ce := ClassifyCredentialError(backendFail, "keyring")
	if ce == nil {
		t.Fatal("ClassifyCredentialError() = nil")
	}
	if ce.Code() != ErrorCodeCredentialBackendUnavailable {
		t.Fatalf("Code() = %q, want credential_backend_unavailable", ce.Code())
	}
	if ce.Type != ErrorTypeAuth {
		t.Fatalf("Type = %q, want auth", ce.Type)
	}
	// A missing credential classifies distinctly.
	missing := ClassifyCredentialError(ErrCredentialNotFound, "keyring")
	if missing.Code() != ErrorCodeCredentialMissing {
		t.Fatalf("missing credential Code() = %q, want credential_missing", missing.Code())
	}
}

// A redacted, bounded hint and message must never contain a secret-looking
// token, even when the upstream error does.
func TestClassifyCredentialErrorDoesNotLeakSecrets(t *testing.T) {
	t.Parallel()
	leaky := errors.New("backend failed: token super-secret-value-12345")
	ce := ClassifyCredentialError(leaky, "keyring")
	if ce == nil {
		t.Fatal("ClassifyCredentialError() = nil")
	}
	if containsSecretLike(ce.Message) || containsSecretLike(ce.HintMsg) {
		t.Fatalf("classified error leaked an upstream secret: msg=%q hint=%q", ce.Message, ce.HintMsg)
	}
}

func containsSecretLike(s string) bool {
	return len(s) > 0 && contains(s, "super-secret-value-12345")
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
