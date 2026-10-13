package keyring

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"

	"github.com/matcra587/jira-cli/internal/core"
)

// service is the keyring entry name every credential is filed under.
//
// Named profiles use stable account locators inside this service. Profile
// names do not change the namespace, so renaming keeps the stored credential.
const service = "jira-cli"

// Store keeps the API token in the operating system's credential store —
// Secret Service on Linux, Keychain on macOS, Credential Manager on
// Windows.
//
// What this does and does not protect against is worth being plain
// about. It keeps the token off disk in cleartext, so a stolen backup or
// a synced dotfiles repo does not carry it. On macOS the Keychain can
// also gate access per application; on Linux, Secret Service cannot —
// once the session is unlocked, any process running as the same user can
// read it. Convenience storage with real at-rest protection, not an
// isolation boundary.
// The upstream keyring API is synchronous and has no context parameter. This
// adapter rejects canceled contexts before entering the native call, but does
// not claim that an in-flight OS-keyring call can be canceled. A native mutation's
// actual return remains authoritative even if cancellation arrives during it.
type Store struct {
	service string
	native  nativeOperations
}

// The upstream mock is process-global. These private native boundaries support
// precise failure/cancellation tests without changing other stores or contexts.
type nativeOperations struct {
	get    func(string, string) (string, error)
	set    func(string, string, string) error
	delete func(string, string) error
}

// compile-time assertion that the adapter satisfies the seam it exists for.
var _ core.CredentialStore = (*Store)(nil)

// New returns the operating system credential store.
func New() *Store { return &Store{} }

// NewForService selects an explicit provider namespace for historical migration
// or isolated native acceptance. Construction never opens the keyring or reads env.
func NewForService(name string) *Store { return &Store{service: strings.TrimSpace(name)} }

func (s *Store) serviceName() string {
	if s.service != "" {
		return s.service
	}
	return service
}

// Get distinguishes an absent entry from a store that cannot be read.
// Configuration owns fallback to explicit environment credentials; the
// adapter must retain the cause so authentication does not mistake a locked
// or unavailable store for an account that has never logged in.
func (s *Store) Get(ctx context.Context, account string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	get := keyring.Get
	if s.native.get != nil {
		get = s.native.get
	}
	secret, err := get(s.serviceName(), account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", core.ErrNoCredential
	}
	if err != nil {
		return "", fmt.Errorf("read credential from the system keyring: %w", err)
	}
	return secret, nil
}

// Set stores the credential for account, replacing any existing entry.
func (s *Store) Set(ctx context.Context, account, secret string) error {
	err, attempted := s.set(ctx, account, secret)
	if err != nil {
		return &core.CredentialStoreError{Backend: core.CredentialBackendKeyring, Operation: "save", Cause: err, Attempted: attempted}
	}
	return nil
}

func (s *Store) set(ctx context.Context, account, secret string) (error, bool) {
	if err := ctx.Err(); err != nil {
		return err, false
	}
	set := keyring.Set
	if s.native.set != nil {
		set = s.native.set
	}
	return set(s.serviceName(), account, secret), true
}

// Delete removes the credential. Removing one that is not there is not
// an error: logout's job is to leave you logged out, and a caller that
// runs it twice has not done anything wrong.
func (s *Store) Delete(ctx context.Context, account string) error {
	err, attempted := s.delete(ctx, account)
	if !attempted {
		return err
	}
	if err != nil {
		return fmt.Errorf("remove credential from the system keyring: %w", err)
	}
	return nil
}

func (s *Store) delete(ctx context.Context, account string) (error, bool) {
	if err := ctx.Err(); err != nil {
		return err, false
	}
	remove := keyring.Delete
	if s.native.delete != nil {
		remove = s.native.delete
	}
	err := remove(s.serviceName(), account)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil, true
	}
	return err, true
}
