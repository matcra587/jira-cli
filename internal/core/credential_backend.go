package core

import (
	"context"
	"errors"
	"strings"
)

// CredentialBackend identifies the selected credential provider, never a secret.
type CredentialBackend string

const (
	CredentialBackendKeyring     CredentialBackend = "keyring"
	CredentialBackendEnvironment CredentialBackend = "env"
	CredentialBackendOnePassword CredentialBackend = "1password"
)

func (b *CredentialBackend) UnmarshalText(text []byte) error {
	switch value := CredentialBackend(strings.ToLower(strings.TrimSpace(string(text)))); value {
	case "":
		*b = CredentialBackendKeyring
	case CredentialBackendKeyring, CredentialBackendEnvironment, CredentialBackendOnePassword:
		*b = value
	default:
		return errors.New("secret_backend must be keyring, env or 1password")
	}
	return nil
}
func (b CredentialBackend) MarshalText() ([]byte, error) { return []byte(b), nil }

// CredentialBackendProvider supplies local provenance without opening a store.
// Stores without this optional metadata retain the historical keyring label.
type CredentialBackendProvider interface{ CredentialBackend() CredentialBackend }

// CredentialStoreError keeps provider causes inspectable without exposing their
// messages, which can include secret references or SDK authentication material.
type CredentialStoreError struct {
	Backend   CredentialBackend
	Operation string
	Cause     error
	Attempted bool
}

func (e *CredentialStoreError) Error() string {
	return string(e.Backend) + " credential " + e.Operation + " failed"
}
func (e *CredentialStoreError) Unwrap() error { return e.Cause }

// EnvironmentCredentialStore reads no storage and refuses persistence explicitly.
// Generic and configured named tokens remain a layer owned by configuration resolution.
type EnvironmentCredentialStore struct{ NoCredentialStore }

func (EnvironmentCredentialStore) CredentialBackend() CredentialBackend {
	return CredentialBackendEnvironment
}

func (EnvironmentCredentialStore) Get(ctx context.Context, _ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", ErrNoCredential
}

func (EnvironmentCredentialStore) Set(ctx context.Context, _, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return &CredentialStoreError{Backend: CredentialBackendEnvironment, Operation: "save", Cause: errors.New("the environment backend does not persist tokens")}
}

func (EnvironmentCredentialStore) Delete(ctx context.Context, _ string) error { return ctx.Err() }
