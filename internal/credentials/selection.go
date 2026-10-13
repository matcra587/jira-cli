package credentials

import (
	"context"
	"errors"

	"github.com/matcra587/jira-cli/internal/core"
)

// Select binds record operations to one keyring account without opening it.
// Empty IDs retain the existing flat location; SDK and environment providers
// keep their own selected item or reference and never receive a profile ID.
func Select(store core.CredentialStore, id string) core.CredentialStore {
	if store == nil || id == "" {
		return store
	}
	if provider, ok := store.(core.CredentialBackendProvider); ok && provider.CredentialBackend() != core.CredentialBackendKeyring {
		return store
	}
	if selected, ok := store.(*selectedStore); ok {
		store = selected.store
	}
	return &selectedStore{store: store, account: id}
}

type selectedStore struct {
	store   core.CredentialStore
	account string
}

func (*selectedStore) CredentialBackend() core.CredentialBackend {
	return core.CredentialBackendKeyring
}

func (s *selectedStore) Get(ctx context.Context, _ string) (string, error) {
	return s.store.Get(ctx, s.account)
}

func (s *selectedStore) Set(ctx context.Context, _, value string) error {
	return s.store.Set(ctx, s.account, value)
}

func (s *selectedStore) Delete(ctx context.Context, _ string) error {
	return s.store.Delete(ctx, s.account)
}

// UnselectedStore cannot read or delete the legacy flat slot. A handwritten
// named profile must select a durable public locator before storing a token.
type UnselectedStore struct{ core.NoCredentialStore }

func (UnselectedStore) Set(ctx context.Context, _, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("the named profile has no credential_id; set a unique UUID with jira config set credential_id before login")
}
