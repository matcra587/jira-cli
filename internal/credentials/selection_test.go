package credentials_test

import (
	"context"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

func TestSelectedRecordsRemainIndependent(t *testing.T) {
	t.Parallel()
	store := newStore()
	first := credentials.Select(store, "first-stable-id")
	second := credentials.Select(store, "second-stable-id")
	for _, entry := range []struct {
		store              core.CredentialStore
		site, email, token string
	}{
		{first, "https://first.atlassian.net", "first@example.com", "first-token"},
		{second, "https://second.atlassian.net", "second@example.com", "second-token"},
		{store, "https://flat.atlassian.net", "flat@example.com", "flat-token"},
	} {
		if err := credentials.Save(t.Context(), entry.store, credentials.Record{Site: entry.site, Email: entry.email, Token: entry.token}); err != nil {
			t.Fatal(err)
		}
	}
	// Selecting the same stable locator after a profile rename finds the same
	// record, and clearing it cannot remove another profile or the flat record.
	renamed := credentials.Select(store, "first-stable-id")
	got, err := credentials.Load(t.Context(), renamed)
	if err != nil || got.Email != "first@example.com" || got.Token != "first-token" {
		t.Fatalf("renamed selection: %#v, %v", got, err)
	}
	if err := credentials.Clear(t.Context(), renamed); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.Load(t.Context(), first); !errors.Is(err, credentials.ErrNotStored) {
		t.Fatalf("cleared slot: %v", err)
	}
	for _, selected := range []core.CredentialStore{second, store} {
		if _, err := credentials.Load(t.Context(), selected); err != nil {
			t.Fatalf("unrelated slot changed: %v", err)
		}
	}
}

type selectedSDKStore struct {
	*memStore
	account string
}

func (*selectedSDKStore) CredentialBackend() core.CredentialBackend {
	return core.CredentialBackendOnePassword
}

func (s *selectedSDKStore) Set(ctx context.Context, account, value string) error {
	s.account = account
	return s.memStore.Set(ctx, account, value)
}

func TestSelectedSDKRetainsProviderLocalRecord(t *testing.T) {
	t.Parallel()
	store := &selectedSDKStore{memStore: newStore()}
	selected := credentials.Select(store, "profile-stable-id")
	err := credentials.Save(t.Context(), selected, credentials.Record{Site: "https://example.atlassian.net", Email: "person@example.com", Token: "token"})
	if err != nil || store.account != "default" {
		t.Fatalf("SDK account=%q, error=%v", store.account, err)
	}
}
