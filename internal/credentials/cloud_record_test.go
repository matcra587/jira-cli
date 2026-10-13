package credentials_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/credentials"
)

func TestVersionOneRecordCannotInventCloudRouting(t *testing.T) {
	t.Parallel()
	store := newStore()
	if err := store.Set(t.Context(), "default", `{"version":1,"site":"https://example.invalid","email":"person@example.invalid","token":"synthetic-cloud-record-token","cloud_id":"different-cloud"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := credentials.Load(t.Context(), store); !errors.Is(err, credentials.ErrUnreadable) {
		t.Fatal("version-1 credential silently accepted new routing semantics", err)
	}
}

func TestCloudRecordVersionsAndPrivateParserFailures(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"version":1,"site":"https://example.invalid","email":"person@example.invalid","token":"synthetic-private-cloud-record","cloud_id":""}`,
		`{"version":2,"site":"https://example.invalid","email":"person@example.invalid","token":"synthetic-private-cloud-record","cloud_id":null}`,
		`{"version":2,"site":"https://example.invalid","email":"person@example.invalid","token":"synthetic-private-cloud-record","cloud_id":".."}`,
		`{"version":2,"site":"https://example.invalid","email":"person@example.invalid","token":"synthetic-private-cloud-record","cloud_id":"cloud-a","cloud_id":"cloud-b"}`,
		`{"version":2,"site":"https://example.invalid","email":"person@example.invalid","token":"synthetic-private-cloud-record","synthetic-private-member":true}`,
	} {
		store := newStore()
		if err := store.Set(t.Context(), "default", raw); err != nil {
			t.Fatal(err)
		}
		_, err := credentials.Load(t.Context(), store)
		if !errors.Is(err, credentials.ErrUnreadable) || strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), "synthetic-private") {
			t.Fatal("invalid private record was accepted or rendered", err)
		}
	}
	store := newStore()
	classic := `{"version":1,"site":"https://example.invalid","email":"person@example.invalid","token":"synthetic-old-classic"}`
	if err := store.Set(t.Context(), "default", classic); err != nil {
		t.Fatal(err)
	}
	record, err := credentials.Load(t.Context(), store)
	if err != nil || record.CloudID != "" || record.Version != 1 || store.secrets["default"] != classic {
		t.Fatal("classic read changed original v1 record", err)
	}
	if err := credentials.Save(t.Context(), store, credentials.Record{Site: "https://example.invalid", Email: "person@example.invalid", Token: "synthetic-new-scoped", CloudID: " cloud-a "}); err != nil {
		t.Fatal(err)
	}
	record, err = credentials.Load(t.Context(), store)
	if err != nil || record.Version != 2 || record.CloudID != "cloud-a" {
		t.Fatal("scope did not persist canonically with token", err)
	}
}
