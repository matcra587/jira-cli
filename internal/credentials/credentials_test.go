package credentials_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

// memStore is an in-memory credential store. Tests must never reach the
// developer's real keyring, which is why the seam exists at all.
type memStore struct {
	secrets map[string]string
	getErr  error
}

func newStore() *memStore { return &memStore{secrets: map[string]string{}} }

func (m *memStore) Get(ctx context.Context, account string) (string, error) {
	if m.getErr != nil {
		return "", m.getErr
	}
	if s, ok := m.secrets[account]; ok {
		return s, nil
	}
	return "", core.ErrNoCredential
}

func (m *memStore) Set(ctx context.Context, account, secret string) error {
	m.secrets[account] = secret
	return nil
}

func (m *memStore) Delete(ctx context.Context, account string) error {
	delete(m.secrets, account)
	return nil
}

// TestUnusableRecordsAreDistinctFromMissingOnes is the finding this
// package existed without a test for.
//
// A record that cannot be read must report ErrUnreadable, NOT a bare
// error: config resolution treats the sentinel as "no usable
// credential" and carries on, and without it every command fails —
// including the `jira auth logout` the message tells you to run, which
// leaves an OS keyring tool as the only way out.
func TestUnusableRecordsAreDistinctFromMissingOnes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, stored string }{
		{name: "not json at all", stored: "not-a-record"},
		{name: "a bare token from some older shape", stored: `"ATATT-just-a-string"`},
		{name: "written by a newer build", stored: `{"version":99,"site":"https://x","email":"a@b.c","token":"t"}`},
		{name: "truncated", stored: `{"version":1,"site":"https://x"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := newStore()
			if err := store.Set(t.Context(), "default", tt.stored); err != nil {
				t.Fatal(err)
			}

			_, err := credentials.Load(t.Context(), store)
			if err == nil {
				t.Fatal("an unusable record loaded successfully")
			}
			if !errors.Is(err, credentials.ErrUnreadable) {
				t.Errorf("error is %v, want ErrUnreadable — anything else takes down every command", err)
			}
			// Not "nothing stored": there IS something there, and
			// conflating them would tell someone to log in while the
			// broken record stayed and did it again.
			if errors.Is(err, credentials.ErrNotStored) {
				t.Error("an unreadable record reported as no record at all")
			}
			// The message has to name the way out.
			if !contains(err.Error(), "logout") {
				t.Errorf("error does not name the remedy: %v", err)
			}
		})
	}
}

// TestMissingRecordReportsNotStored pins the ordinary pre-login state.
func TestMissingRecordReportsNotStored(t *testing.T) {
	t.Parallel()
	if _, err := credentials.Load(t.Context(), newStore()); !errors.Is(err, credentials.ErrNotStored) {
		t.Errorf("error is %v, want ErrNotStored", err)
	}
}

// TestSaveRefusesAnEmptyToken pins that the record cannot be written
// without the one field it exists to carry. A record holding a site and
// an account but no secret would resolve as "logged in" and then fail
// against Jira.
func TestSaveRefusesAnEmptyToken(t *testing.T) {
	t.Parallel()
	store := newStore()

	if err := credentials.Save(t.Context(), store, credentials.Record{
		Site: "https://example.atlassian.net", Email: "person@example.com",
	}); err == nil {
		t.Fatal("a record with no token was stored")
	}
	if _, err := credentials.Load(t.Context(), store); !errors.Is(err, credentials.ErrNotStored) {
		t.Error("a refused save left something behind")
	}
}

// TestSaveStampsTheVersion pins that every written record carries one,
// whatever the caller passed. A record without a version cannot be
// migrated later, and it outlives the build that wrote it.
func TestSaveStampsTheVersion(t *testing.T) {
	t.Parallel()
	store := newStore()

	if err := credentials.Save(t.Context(), store, credentials.Record{
		Version: 0, Site: "https://example.atlassian.net", Email: "a@b.c", Token: "t",
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := credentials.Load(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Version != credentials.Version {
		t.Errorf("version = %d, want %d", rec.Version, credentials.Version)
	}
}

// TestSiteIsNormalizedBothWays pins that a trailing slash is trimmed on
// the way in AND on the way out, so a record written before the rule
// existed still reads cleanly instead of needing a fresh login.
func TestSiteIsNormalizedBothWays(t *testing.T) {
	t.Parallel()
	store := newStore()

	if err := credentials.Save(t.Context(), store, credentials.Record{
		Site: "https://example.atlassian.net/", Email: "a@b.c", Token: "t",
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := credentials.Load(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Site != "https://example.atlassian.net" {
		t.Errorf("site = %q, want it trimmed", rec.Site)
	}

	// And a record already stored with the slash, as one was.
	if err := store.Set(t.Context(), "default", `{"version":1,"site":"https://example.atlassian.net/","email":"a@b.c","token":"t"}`); err != nil {
		t.Fatal(err)
	}
	rec, err = credentials.Load(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Site != "https://example.atlassian.net" {
		t.Errorf("previously stored site = %q, want it trimmed on read", rec.Site)
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// TestSiteCaseFoldsSchemeAndHostOnly pins the comparison that decides
// whether a stored credential is reused.
//
// A host that differs only in spelling is the same Jira, and comparing
// "https://COMPANY..." against a lowercase stored site dropped a valid
// credential while warning about two hosts that look identical on
// screen. The path is NOT folded — Jira paths are case-sensitive.
func TestSiteCaseFoldsSchemeAndHostOnly(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ name, in, want string }{
		{name: "host", in: "https://Example.Atlassian.NET", want: "https://example.atlassian.net"},
		{name: "scheme", in: "HTTPS://example.atlassian.net", want: "https://example.atlassian.net"},
		{name: "both, with a trailing slash", in: "HTTPS://Example.Atlassian.NET/", want: "https://example.atlassian.net"},
		{name: "already normal", in: "https://example.atlassian.net", want: "https://example.atlassian.net"},
		// The path rides through untouched: a folded path would point
		// at a different Jira resource, or none.
		{name: "path is left alone", in: "https://Example.Atlassian.NET/Jira/Core", want: "https://example.atlassian.net/Jira/Core"},
		// No scheme means url.Parse finds no Host, so the fold is
		// skipped entirely and the input is only trimmed. Documented
		// here rather than fixed: config.validateBaseURL requires a
		// scheme, so a schemeless site never reaches a comparison.
		{name: "schemeless is trimmed but not folded", in: "Example.Atlassian.NET/", want: "Example.Atlassian.NET"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := credentials.NormalizeSite(tt.in); got != tt.want {
				t.Errorf("NormalizeSite(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
