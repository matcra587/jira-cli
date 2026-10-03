package keyring

import (
	"errors"
	"fmt"

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
type Store struct{}

// compile-time assertion that the adapter satisfies the seam it exists for.
var _ core.CredentialStore = (*Store)(nil)

// New returns the operating system credential store.
func New() *Store { return &Store{} }

// Get returns the stored credential. EVERY failure to read one — nothing
// stored, a locked keychain, a denied prompt, an absent Secret Service, a
// `security` binary that failed — comes back as core.ErrNoCredential,
// because they all mean the same thing to a caller resolving
// configuration: there is no credential here, fall through to the
// environment.
//
// The alternative was matching backend error text, which is guesswork:
// anything phrased differently took down every command, because every
// command resolves config. Writes still report loudly (see Set) — there,
// "I could not save it" and "I saved it" are not interchangeable.
func (s *Store) Get(account string) (string, error) {
	secret, err := keyring.Get(service, account)
	if err != nil {
		return "", core.ErrNoCredential
	}
	return secret, nil
}

// Set stores the credential for account, replacing any existing entry.
func (s *Store) Set(account, secret string) error {
	if err := keyring.Set(service, account, secret); err != nil {
		return fmt.Errorf("save credential to the system keyring: %w", err)
	}
	return nil
}

// Delete removes the credential. Removing one that is not there is not
// an error: logout's job is to leave you logged out, and a caller that
// runs it twice has not done anything wrong.
func (s *Store) Delete(account string) error {
	err := keyring.Delete(service, account)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return fmt.Errorf("remove credential from the system keyring: %w", err)
}
