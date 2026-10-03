package credentials_test

import (
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
		if err := credentials.Save(entry.store, credentials.Record{Site: entry.site, Email: entry.email, Token: entry.token}); err != nil {
			t.Fatal(err)
		}
	}
	// Selecting the same stable locator after a profile rename finds the same
	// record, and clearing it cannot remove another profile or the flat record.
	renamed := credentials.Select(store, "first-stable-id")
	got, err := credentials.Load(renamed)
	if err != nil || got.Email != "first@example.com" || got.Token != "first-token" {
		t.Fatalf("renamed selection: %#v, %v", got, err)
	}
	if err := credentials.Clear(renamed); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.Load(first); !errors.Is(err, credentials.ErrNotStored) {
		t.Fatalf("cleared slot: %v", err)
	}
	for _, selected := range []core.CredentialStore{second, store} {
		if _, err := credentials.Load(selected); err != nil {
			t.Fatalf("unrelated slot changed: %v", err)
		}
	}
}
