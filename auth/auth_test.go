package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/auth"
)

const firstID = "11111111-1111-4111-8111-111111111111"

const profiles = `format_version = 1
default_profile = "work"
[profiles.work]
base_url = "https://work.example.invalid"
email = "reader@example.invalid"
credential_id = "11111111-1111-4111-8111-111111111111"
timeout = "15s"
read_only = true
[profiles.other]
base_url = "https://other.example.invalid"
email = "other@example.invalid"
credential_id = "22222222-2222-4222-8222-222222222222"
`

type store struct {
	get   func(context.Context, string) (string, error)
	reads []string
}

func (s *store) Get(ctx context.Context, account string) (string, error) {
	s.reads = append(s.reads, account)
	return s.get(ctx, account)
}

func (*store) Set(context.Context, string, string) error { panic("unexpected credential write") }
func (*store) Delete(context.Context, string) error      { panic("unexpected credential delete") }

func fixture(t *testing.T, contents string) string {
	t.Helper()
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "JIRA_") {
			t.Setenv(name, "")
		}
	}
	path := filepath.Join(t.TempDir(), "profiles.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func record(value string) string {
	return fmt.Sprintf(
		`{"version":2,"site":"https://work.example.invalid","email":"reader@example.invalid","token":%q}`,
		value,
	)
}

func TestResolveFreezesSelectionAndRefreshesOnlyOnNextCall(t *testing.T) {
	path := fixture(t, profiles)
	value := "synthetic-first-credential"
	provider := &store{get: func(_ context.Context, id string) (string, error) {
		if id != firstID {
			t.Fatalf("read unselected credential %q", id)
		}
		// A concurrent settings/environment change cannot retarget this load.
		t.Setenv("JIRA_PROFILE", "other")
		return record(value), nil
	}}
	opts := auth.Options{Profile: "work", ConfigPath: path, CredentialStore: provider}
	first, err := auth.Resolve(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.Identity().Profile != "work" || first.Token() != value {
		t.Fatalf("selected session=%v err=%v", first, err)
	}
	if !first.ReadOnly() || first.Timeout().Seconds() != 15 {
		t.Fatal("saved request policy changed", first)
	}
	value = "synthetic-replaced-credential"
	second, err := auth.Resolve(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if second.Token() != value || first.Token() == second.Token() {
		t.Fatal("fresh resolution reused or changed an existing credential snapshot", err)
	}
	if len(provider.reads) != 2 {
		t.Fatal("resolution did not read the selected credential afresh", provider.reads)
	}
	for _, printable := range []any{first, &first, opts, &opts} {
		raw, err := json.Marshal(printable)
		if err != nil {
			t.Fatal(err)
		}
		for _, output := range []string{
			string(raw),
			fmt.Sprintf("%v", printable),
			fmt.Sprintf("%+v", printable),
			fmt.Sprintf("%#v", printable),
		} {
			if strings.Contains(output, "synthetic-") || strings.Contains(output, path) {
				t.Fatal("public diagnostics exposed credential or provider state")
			}
		}
	}
}

func TestResolveRefusesMisboundCredentialsAndHidesProviderDiagnostics(t *testing.T) {
	path := fixture(t, profiles)
	private := errors.New("synthetic-sensitive-provider-diagnostic")
	for _, tc := range []struct {
		name string
		get  func(context.Context, string) (string, error)
	}{
		{name: "provider error", get: func(context.Context, string) (string, error) { return "", private }},
		{name: "wrong site", get: func(context.Context, string) (string, error) {
			return strings.ReplaceAll(record("synthetic-wrong-site"), "work.example.invalid", "other.example.invalid"), nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &store{get: tc.get}
			session, err := auth.Resolve(t.Context(), auth.Options{ConfigPath: path, CredentialStore: provider})
			if !errors.Is(err, auth.ErrNotAuthenticated) || session.Token() != "" {
				t.Fatalf("unusable authentication accepted: %v", err)
			}
			if tc.name == "provider error" && !errors.Is(err, private) {
				t.Fatal("provider cause lost")
			}
			printed := fmt.Sprintf(
				"%v %+v %#v",
				err,
				err,
				err,
			)
			if strings.Contains(printed, "synthetic-") {
				t.Fatal("provider diagnostic leaked")
			}
		})
	}
}

func TestResolveCanceledBeforeCredentialLookup(t *testing.T) {
	path := fixture(t, profiles)
	provider := &store{get: func(context.Context, string) (string, error) {
		t.Fatal("canceled resolution read credentials")
		return "", nil
	}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := auth.Resolve(ctx, auth.Options{ConfigPath: path, CredentialStore: provider})
	if !errors.Is(err, context.Canceled) || len(provider.reads) != 0 {
		t.Fatal("cancellation lost", err)
	}
}
