package credentials

import (
	"errors"

	"github.com/matcra587/jira-cli/internal/core"
)

// Select binds record operations to one durable keyring account without
// opening native storage. Empty IDs retain the existing flat location.
func Select(store core.CredentialStore, id string) core.CredentialStore {
	if store == nil || id == "" {
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

func (s *selectedStore) Get(_ string) (string, error) { return s.store.Get(s.account) }
func (s *selectedStore) Set(_, value string) error    { return s.store.Set(s.account, value) }
func (s *selectedStore) Delete(_ string) error        { return s.store.Delete(s.account) }

// UnselectedStore cannot read or delete the legacy flat slot. A handwritten
// named profile must select a durable public locator before storing a token.
type UnselectedStore struct{ core.NoCredentialStore }

func (UnselectedStore) Set(_, _ string) error {
	return errors.New("the named profile has no credential_id; set a unique UUID with jira config set credential_id before login")
}
