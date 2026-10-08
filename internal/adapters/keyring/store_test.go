package keyring_test

import (
	"errors"
	"fmt"
	"testing"

	native "github.com/zalando/go-keyring"

	"github.com/matcra587/jira-cli/internal/adapters/keyring"
	"github.com/matcra587/jira-cli/internal/core"
)

// The library mock replaces its process-wide provider. These tests run serially
// and leave an empty mock installed so no test can reach the real keyring.
func TestStoreReadDistinguishesAbsenceAndBackendFailure(t *testing.T) {
	t.Cleanup(native.MockInit)
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "absent", err: native.ErrNotFound},
		{name: "wrapped absent", err: fmt.Errorf("lookup: %w", native.ErrNotFound)},
		{name: "denied", err: errors.New("fixture backend denied access")},
		{name: "locked", err: errors.New("fixture backend is locked")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native.MockInitWithError(tc.err)
			secret, err := keyring.New().Get("default")
			if secret != "" {
				t.Fatal("failed lookup returned a secret")
			}
			if errors.Is(tc.err, native.ErrNotFound) {
				if !errors.Is(err, core.ErrNoCredential) {
					t.Fatalf("absent entry did not retain ordinary absence: %v", err)
				}
			} else if errors.Is(err, core.ErrNoCredential) || !errors.Is(err, tc.err) {
				t.Fatalf("backend failure became absence or lost its cause: %v", err)
			}
		})
	}
}

func TestStoreReadReturnsStoredCredential(t *testing.T) {
	native.MockInit()
	t.Cleanup(native.MockInit)
	store := keyring.New()
	const fixture = "synthetic-keyring-record"
	if err := store.Set("default", fixture); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("default")
	if err != nil || got != fixture {
		t.Fatal("successful lookup lost the stored record")
	}
	if err := store.Delete("default"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("default"); !errors.Is(err, core.ErrNoCredential) {
		t.Fatalf("deleted record remains readable: %v", err)
	}
}
