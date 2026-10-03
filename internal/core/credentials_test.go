package core_test

import (
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

// TestNoCredentialStoreRefusesWrites pins the one thing a disabled store
// must never do: report that it saved a token.
//
// Reads answer "nothing stored", which is true and lets resolution fall
// through to the environment. A write has no such honest silent answer —
// succeeding would tell someone their credential is safe somewhere it
// is not, and they would find out at the next command on a new machine.
func TestNoCredentialStoreRefusesWrites(t *testing.T) {
	t.Parallel()
	var store core.CredentialStore = core.NoCredentialStore{}

	if _, err := store.Get("default"); !errors.Is(err, core.ErrNoCredential) {
		t.Errorf("Get returned %v, want ErrNoCredential so resolution falls through to the environment", err)
	}
	if err := store.Set("default", "a-token"); err == nil {
		t.Error("Set reported success with nowhere to store the token")
	}
	// Delete succeeds: logout's contract is the end state, and with no
	// store there is nothing stored.
	if err := store.Delete("default"); err != nil {
		t.Errorf("Delete returned %v, want nil", err)
	}
}
