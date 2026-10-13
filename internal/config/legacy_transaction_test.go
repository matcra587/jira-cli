package config

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/core"
)

const transactionFixture = `# Exact recovery includes the unselected profile.
default_profile='work'
[[profiles]]
name='other'
email='other@example.invalid'
[[profiles]]
name='work'
base_url='https://work.invalid/'
email='work@example.invalid'
timeout=0
`

type transactionSnapshot struct {
	location core.CredentialLocation
	value    string
	present  bool
	metadata string
}

func (s *transactionSnapshot) Secret() (string, bool)            { return s.value, s.present }
func (s *transactionSnapshot) Location() core.CredentialLocation { return s.location }
func (s *transactionSnapshot) Commitment() string {
	data, err := jsonv2.Marshal(struct {
		Location core.CredentialLocation `json:"location"`
		Value    string                  `json:"value"`
		Present  bool                    `json:"present"`
		Metadata string                  `json:"metadata"`
	}{s.location, s.value, s.present, s.metadata})
	if err != nil {
		return ""
	}
	return core.CredentialCommitment(bytes.Repeat([]byte{1}, 32), data)
}

func (s *transactionSnapshot) Matches(other core.CredentialSnapshot) bool {
	right, ok := other.(*transactionSnapshot)
	return s != nil && ok && right != nil && *s == *right
}
func (*transactionSnapshot) String() string   { return "credential snapshot" }
func (*transactionSnapshot) GoString() string { return "credential snapshot" }

type transactionPair struct{ before, expected transactionSnapshot }

type transactionProvider struct {
	values       map[core.CredentialLocation]transactionSnapshot
	backups      map[string]transactionPair
	mutations    []string
	factories    int
	onMutation   func(string, bool) error
	onReadBackup func()
}

type transactionStore struct {
	provider *transactionProvider
	location core.CredentialLocation
}

func (p *transactionProvider) store(ctx context.Context, location core.CredentialLocation) (core.CredentialRecoveryStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.factories++
	return &transactionStore{provider: p, location: location}, nil
}

func (s *transactionStore) Capture(ctx context.Context, account string) (core.CredentialSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	location := s.location
	location.Account = account
	value := s.provider.values[location]
	value.location = location
	return &value, nil
}

func (*transactionStore) Replacement(before core.CredentialSnapshot, value string) (core.CredentialSnapshot, error) {
	old := *before.(*transactionSnapshot)
	old.value, old.present, old.metadata = value, true, "converted owned field"
	return &old, nil
}

func (s *transactionStore) Backup(ctx context.Context, name string, before, expected core.CredentialSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, found := s.provider.backups[name]; found {
		return core.ErrCredentialBackupExists
	}
	current, err := s.Capture(ctx, before.Location().Account)
	if err != nil || !before.Matches(current) {
		return core.ErrCredentialChanged
	}
	return s.mutate("backup", func() {
		s.provider.backups[name] = transactionPair{*before.(*transactionSnapshot), *expected.(*transactionSnapshot)}
	})
}

func (s *transactionStore) ReadBackup(ctx context.Context, name string) (core.CredentialSnapshot, core.CredentialSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	pair, found := s.provider.backups[name]
	if !found || pair.before.location != s.location {
		return nil, nil, core.ErrCredentialRecoveryInvalid
	}
	if s.provider.onReadBackup != nil {
		s.provider.onReadBackup()
	}
	return &pair.before, &pair.expected, nil
}

func (s *transactionStore) Apply(ctx context.Context, account, name string) (bool, error) {
	return s.replace(ctx, account, name, false)
}

func (s *transactionStore) Restore(ctx context.Context, account, name string) (bool, error) {
	return s.replace(ctx, account, name, true)
}

func (s *transactionStore) replace(ctx context.Context, account, name string, restore bool) (bool, error) {
	before, expected, err := s.ReadBackup(ctx, name)
	if err != nil {
		return false, err
	}
	want, from, operation := expected, before, "apply"
	if restore {
		want, from, operation = before, expected, "restore"
	}
	current, err := s.Capture(ctx, account)
	if err != nil {
		return false, err
	}
	if current.Matches(want) {
		return false, nil
	}
	if !current.Matches(from) {
		return false, core.ErrCredentialChanged
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	err = s.mutate(operation, func() { s.provider.values[s.location] = *want.(*transactionSnapshot) })
	var providerError *core.CredentialStoreError
	if errors.As(err, &providerError) {
		return providerError.Attempted, err
	}
	return true, err
}

func (s *transactionStore) mutate(operation string, write func()) error {
	if s.provider.onMutation != nil {
		if err := s.provider.onMutation(operation, false); err != nil {
			return &core.CredentialStoreError{Backend: s.location.Backend, Operation: operation, Cause: err}
		}
	}
	s.provider.mutations = append(s.provider.mutations, operation)
	write()
	if s.provider.onMutation != nil {
		if err := s.provider.onMutation(operation, true); err != nil {
			return &core.CredentialStoreError{Backend: s.location.Backend, Operation: operation, Cause: err, Attempted: true}
		}
	}
	return nil
}

type transactionTest struct {
	transaction *LegacyTransaction
	provider    *transactionProvider
	sourcePath  string
	targetPath  string
	original    []byte
	source      transactionSnapshot
	target      transactionSnapshot
}

func newTransactionTest(t *testing.T, backend core.CredentialBackend, targetKind string) *transactionTest {
	t.Helper()
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "old.toml")
	fixture := transactionFixture
	if backend == core.CredentialBackendOnePassword {
		fixture += "secret_backend='1password'\nonepassword_account='desktop'\nvault='vault-id'\nitem='item-id'\n"
	}
	if backend == core.CredentialBackendEnvironment {
		fixture += "secret_backend='env'\n"
	}
	writeTransactionFile(t, sourcePath, []byte(fixture))
	targetPath, original := sourcePath, []byte(fixture)
	if targetKind != "in place" {
		targetPath, original = filepath.Join(dir, "new.toml"), nil
	}
	if targetKind == "existing" {
		original = []byte("# Preserve spelling.\nread_only=true\n")
		writeTransactionFile(t, targetPath, original)
	}
	if targetKind == "candidate" {
		migration, err := PrepareLegacyMigration(t.Context(), []byte(fixture), sourcePath, "")
		if err != nil {
			t.Fatal(err)
		}
		original, err = migration.Candidate()
		if err != nil {
			t.Fatal(err)
		}
		writeTransactionFile(t, targetPath, original)
	}
	source, err := OpenFile(t.Context(), sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	target, err := OpenFile(t.Context(), targetPath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareLegacyTransaction(t.Context(), source, target, "", "old-keyring-service")
	if err != nil {
		t.Fatal(err)
	}
	recoveryDir := filepath.Join(dir, "private", "recovery")
	if _, err := os.Stat(filepath.Dir(recoveryDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preparation created recovery directories")
	}
	transaction, published, err := plan.Create(t.Context(), recoveryDir)
	if err != nil || !published || transaction == nil {
		t.Fatalf("create recovery: published=%v err=%v", published, err)
	}
	provider := &transactionProvider{values: map[core.CredentialLocation]transactionSnapshot{}, backups: map[string]transactionPair{}}
	sourceCredential := transactionSnapshot{location: transaction.record.SourceCredential, value: "private-test-legacy-value", present: true, metadata: "original owned field metadata"}
	targetCredential := transactionSnapshot{location: transaction.record.TargetCredential, value: "private-test-destination-value", present: true, metadata: "original destination metadata"}
	if backend == core.CredentialBackendOnePassword {
		targetCredential = sourceCredential
	}
	provider.values[sourceCredential.location], provider.values[targetCredential.location] = sourceCredential, targetCredential
	return &transactionTest{transaction, provider, sourcePath, targetPath, original, sourceCredential, targetCredential}
}

func writeTransactionFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *transactionTest) reload(t *testing.T) *LegacyTransaction {
	t.Helper()
	transaction, err := LoadLegacyTransaction(t.Context(), f.transaction.file.requested)
	if err != nil {
		t.Fatal(err)
	}
	return transaction
}

func (f *transactionTest) assertRestored(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile(f.targetPath)
	if len(f.original) == 0 {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal("rollback did not restore destination absence")
		}
	} else if err != nil || !bytes.Equal(data, f.original) {
		t.Fatal("rollback lost exact config preimage")
	}
	if f.provider.values[f.target.location] != f.target {
		t.Fatal("rollback lost exact credential value, absence or metadata")
	}
	if f.source.location != f.target.location && f.provider.values[f.source.location] != f.source {
		t.Fatal("migration changed historical source namespace")
	}
}

func TestLegacyTransactionCredentialBeforeConfigurationAndExactRollback(t *testing.T) {
	t.Parallel()
	for _, backend := range []core.CredentialBackend{core.CredentialBackendKeyring, core.CredentialBackendOnePassword} {
		for _, targetKind := range []string{"in place", "absent", "existing", "candidate"} {
			t.Run(string(backend)+"/"+targetKind, func(t *testing.T) {
				f := newTransactionTest(t, backend, targetKind)
				f.provider.onMutation = func(operation string, _ bool) error {
					journal := f.reload(t)
					if len(journal.record.Effects) == 0 {
						t.Fatal("provider entry preceded durable intent")
					}
					last := journal.record.Effects[len(journal.record.Effects)-1]
					if last.Status != "pending" || last.Operation != "credential_"+operation {
						t.Fatal("provider entry preceded durable matching intent")
					}
					if operation == "backup" || operation == "apply" {
						data, err := os.ReadFile(f.targetPath)
						if !bytes.Equal(data, f.original) || len(f.original) != 0 && err != nil {
							t.Fatal("configuration changed before credential acknowledgment")
						}
					}
					return nil
				}
				receipt, err := f.transaction.Apply(t.Context(), f.provider.store)
				if err != nil || receipt.Status != "applied" || !receipt.JournalDurable || len(receipt.Effects) != 3 {
					t.Fatalf("apply: status=%s err=%v", receipt.Status, err)
				}
				for _, effect := range receipt.Effects {
					expectedAttempt := effect.Operation != migrationConfigApply || targetKind != "candidate"
					if effect.Status != "acknowledged" || effect.Attempted == nil || *effect.Attempted != expectedAttempt {
						t.Fatal("successful mutation lost acknowledged entry")
					}
				}
				var stored struct {
					Version int    `json:"version"`
					Site    string `json:"site"`
					Email   string `json:"email"`
					Token   string `json:"token"`
				}
				if err := jsonv2.Unmarshal([]byte(f.provider.values[f.target.location].value), &stored); err != nil ||
					stored.Version != 2 || stored.Site != "https://work.invalid" || stored.Email != "work@example.invalid" || stored.Token != f.source.value {
					t.Fatal("legacy raw value was not converted into the bound credential record")
				}
				data, err := os.ReadFile(f.targetPath)
				if err != nil || ValidateFile(data) != nil || bytes.Contains(data, []byte("other@example.invalid")) {
					t.Fatal("selected flat configuration was not established")
				}
				if _, err := f.reload(t).Apply(t.Context(), f.provider.store); !errors.Is(err, ErrLegacyTransactionStarted) {
					t.Fatal("completed migration replayed")
				}
				receipt, err = f.reload(t).Rollback(t.Context(), f.provider.store)
				if err != nil || receipt.Status != "rolled_back" || !receipt.JournalDurable {
					t.Fatalf("rollback: %v", err)
				}
				f.assertRestored(t)
				count := len(f.provider.mutations)
				if _, err := f.reload(t).Rollback(t.Context(), f.provider.store); err != nil || len(f.provider.mutations) != count {
					t.Fatal("already restored transaction rewrote credentials")
				}
				if len(f.provider.backups) != 1 {
					t.Fatal("rollback discarded its provider backup")
				}
			})
		}
	}
}

func TestLegacyTransactionAbsentSourceDoesNotOverwriteDestination(t *testing.T) {
	t.Parallel()
	for _, backend := range []core.CredentialBackend{core.CredentialBackendKeyring, core.CredentialBackendOnePassword} {
		t.Run(string(backend), func(t *testing.T) {
			f := newTransactionTest(t, backend, "in place")
			delete(f.provider.values, f.source.location)
			receipt, err := f.transaction.Apply(t.Context(), f.provider.store)
			if err != nil || receipt.CredentialState != "absent" || len(f.provider.mutations) != 0 || f.provider.factories != 1 || len(receipt.Effects) != 1 {
				t.Fatal("absent source caused credential writes or destination lookup")
			}
			if backend == core.CredentialBackendKeyring && f.provider.values[f.target.location] != f.target {
				t.Fatal("absent legacy credential overwrote destination")
			}
			if _, err := f.reload(t).Rollback(t.Context(), nil); err != nil {
				t.Fatal("credential-free rollback opened a provider")
			}
		})
	}
}

func TestLegacyTransactionRecordsAndReceiptsConcealSecrets(t *testing.T) {
	t.Parallel()
	f := newTransactionTest(t, core.CredentialBackendKeyring, "in place")
	receipt, err := f.transaction.Apply(t.Context(), f.provider.store)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{receipt.Journal, receipt.Configuration} {
		data, err := os.ReadFile(path)
		if err != nil || bytes.Contains(data, []byte(f.source.value)) || bytes.Contains(data, []byte(f.target.value)) {
			t.Fatal("credential material entered filesystem recovery")
		}
		if xos.IsUnix() {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatal("recovery record is not private")
			}
		}
	}
	data, err := jsonv2.Marshal(receipt)
	if err != nil || strings.Contains(string(data)+fmt.Sprintf("%+v %#v", receipt, receipt), f.source.value) {
		t.Fatal("receipt rendered a credential")
	}
	*receipt.Effects[0].Attempted = false
	receipt.Effects[0].Status = "corrupt"
	if f.transaction.Receipt().Effects[0].Status != "acknowledged" || !*f.transaction.Receipt().Effects[0].Attempted {
		t.Fatal("receipt allowed mutation of journal state")
	}
}
