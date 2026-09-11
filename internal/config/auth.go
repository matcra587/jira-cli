package config

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"

	xstrings "github.com/gechr/x/strings"
)

// ErrCredentialNotFound is the sentinel every backend returns for a missing
// credential. Callers test it with errors.Is; logout and transactional prior
// reads treat it as benign so an absent credential is not an error.
var ErrCredentialNotFound = errors.New("credential not found")

// SecretRef identifies a credential in a backend. A credential belongs to a
// Jira site and a profile: Host is the site host (the host of the profile's
// base URL) and Profile is the profile name. Two profiles that share a name
// but point at different Jira sites address different credentials, because the
// site host is part of the identity. Account/Vault/Item are the
// backend-specific 1Password coordinates.
//
// Construct a SecretRef with CredentialIdentity so the identity is derived
// consistently and unsafe profile names are rejected.
type SecretRef struct {
	Profile string
	Backend SecretBackend
	Account string
	Vault   string
	Item    string
	// ItemIsDefault reports whether Item is the jira-cli-derived default name
	// rather than a name the profile pinned itself. It records 1Password item
	// ownership: jira-cli owns and may delete an item it named by default, but
	// must never delete an item the user named.
	ItemIsDefault bool
	// Host is the site host of the profile's Jira base URL (e.g.
	// "company.atlassian.net"). It scopes the credential to the site, so the
	// same profile name pointing at two sites does not share an entry.
	Host string
}

// profileNameIsNamespaceSafe reports whether a profile name can be encoded
// into a credential namespace that round-trips uniquely.
//
// The JIRA_TOKEN_* environment-variable key is built by uppercasing the
// profile name, so two names that differ only by letter case would collide on
// the same key. Names containing an uppercase letter are therefore rejected:
// every accepted name is lower-case, which makes the uppercase transform
// injective. Hyphen and underscore do not collide because they are escaped
// distinctly (see encodeEnvSegment). Allowed characters are lower-case
// letters, digits, hyphen, and underscore.
func profileNameIsNamespaceSafe(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// ValidateProfileName reports whether a profile name can be encoded into a
// credential key that round-trips uniquely. It is the input-time guard for the
// same namespace safety CredentialIdentity enforces at store time, so a bad
// name (uppercase, whitespace, a slash, anything outside lowercase letters,
// digits, hyphen, and underscore) is rejected when it is typed or passed —
// not late, after a token has already been sent to Jira. The returned error is
// a validation-class CredentialError.
func ValidateProfileName(name string) error {
	if !profileNameIsNamespaceSafe(name) {
		return namespaceCollisionError(name)
	}
	return nil
}

// CredentialIdentity derives the stable credential identity for a profile.
// The returned SecretRef carries the site host (derived from the profile's
// base URL) and profile name — the credential belongs to that site + profile
// pair — plus the 1Password coordinates (account/vault/item). For the
// 1Password backend the item title defaults to a site-scoped name
// ("jira-cli-<host>-<profile>") when the profile does not pin one, so two
// sites that share a profile name and a vault do not collide on one item.
//
// A profile name that cannot be encoded into a safe keyring/env key is
// rejected with a CredentialError carrying
// ErrorCodeCredentialNamespaceCollision.
func CredentialIdentity(profile Profile) (SecretRef, error) {
	if err := ValidateProfileName(profile.Name); err != nil {
		return SecretRef{}, err
	}
	host := siteHost(profile.BaseURL)
	item := profile.Item
	itemIsDefault := item == ""
	if itemIsDefault {
		item = defaultOnePasswordItem(host, profile.Name)
	}
	return SecretRef{
		Profile:       profile.Name,
		Backend:       profile.SecretBackend,
		Account:       profile.OnePasswordAccount,
		Vault:         profile.Vault,
		Item:          item,
		ItemIsDefault: itemIsDefault,
		Host:          host,
	}, nil
}

// siteHost returns the host of a Jira base URL after NormalizeBaseURL expands
// any shorthand. Different spellings of the same site ("company",
// "company.atlassian.net", "https://company.atlassian.net/") all yield the
// same host, so a credential is reachable however the URL is spelled.
func siteHost(baseURL string) string {
	normalized := NormalizeBaseURL(baseURL)
	if normalized == "" {
		return ""
	}
	u, err := url.Parse(normalized)
	if err != nil || u.Host == "" {
		return normalized
	}
	return u.Host
}

// defaultOnePasswordItem is the jira-cli-owned 1Password item title for a
// site + profile. It is site-scoped so two sites sharing a profile name and a
// vault do not collide on one item.
func defaultOnePasswordItem(host, profile string) string {
	if host == "" {
		return "jira-cli-" + profile
	}
	return "jira-cli-" + host + "-" + profile
}

// KeyringName is the keyring entry name for this credential: the readable
// "<host>/<profile>" pair. There is no digest — the key is the credential's
// site + profile identity verbatim, so it is legible in any keyring browser.
func (r SecretRef) KeyringName() string {
	return r.Host + "/" + r.Profile
}

// EnvTokenKey is the environment variable that overrides this profile's
// credential. The profile name is encoded bijectively: a literal underscore
// in the name is doubled and a hyphen maps to a single underscore, so
// "work-staging" (JIRA_TOKEN_WORK_STAGING) and "work_staging"
// (JIRA_TOKEN_WORK__STAGING) map to distinct keys and cannot bleed into each
// other. Letters are uppercased; namespace-safe profile names are lower-case
// only, so the uppercase transform is injective.
func (r SecretRef) EnvTokenKey() string {
	return "JIRA_TOKEN_" + encodeEnvSegment(r.Profile)
}

// encodeEnvSegment encodes a namespace-safe profile name for use in an
// environment variable name. A hyphen becomes a single underscore; a literal
// underscore is doubled, so the hyphen and underscore cases stay distinct.
// Letters are uppercased and digits pass through. The transform is reversible
// for the lower-case domain profileNameIsNamespaceSafe accepts.
func encodeEnvSegment(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '_':
			b.WriteString("__")
		case r == '-':
			b.WriteByte('_')
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - ('a' - 'A'))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CredentialStore is the backend-agnostic secret store: Get, Put, and Delete a
// credential addressed by a SecretRef. KeyringStore, OnePasswordStore,
// EnvCredentialStore, FileCredentialStore, and MemoryCredentialStore implement
// it. A missing credential is reported with ErrCredentialNotFound.
type CredentialStore interface {
	Get(context.Context, SecretRef) (string, error)
	Put(context.Context, SecretRef, string) error
	Delete(context.Context, SecretRef) error
}

// AuthStatus is the resolved credential state for a profile: whether a usable
// credential was found, which source supplied it (the backend, or "env" for a
// JIRA_TOKEN_* override), a redacted last-four preview, and any sanitized
// error. It is the shape `auth status` serializes and never carries the secret.
type AuthStatus struct {
	Profile  string `json:"profile"`
	Valid    bool   `json:"valid"`
	Source   string `json:"source"`
	Redacted string `json:"redacted"`
	Error    string `json:"error,omitempty"`
}

// MemoryCredentialStore is an in-memory CredentialStore for tests. It holds
// secrets in a map keyed by the full SecretRef identity, so two profiles that
// differ only by site never collide.
type MemoryCredentialStore struct {
	mu      sync.Mutex
	secrets map[string]string
}

// NewMemoryCredentialStore returns an empty in-memory credential store.
func NewMemoryCredentialStore() *MemoryCredentialStore {
	return &MemoryCredentialStore{secrets: make(map[string]string)}
}

// Get returns the stored secret for ref, or ErrCredentialNotFound.
func (s *MemoryCredentialStore) Get(_ context.Context, ref SecretRef) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.secrets[ref.key()]
	if !ok {
		return "", ErrCredentialNotFound
	}
	return v, nil
}

// Put stores secret under ref, overwriting any existing value.
func (s *MemoryCredentialStore) Put(_ context.Context, ref SecretRef, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secrets[ref.key()] = secret
	return nil
}

// Delete removes ref's secret; deleting an absent entry is a no-op.
func (s *MemoryCredentialStore) Delete(_ context.Context, ref SecretRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.secrets, ref.key())
	return nil
}

// ResolveCredential returns the effective credential for a profile: a
// JIRA_TOKEN_* environment override when set, otherwise the value stored in
// the backend. The env override is keyed by SecretRef.EnvTokenKey, which
// encodes the profile name bijectively so an override for one profile cannot
// bleed into a sibling whose name differs only by a hyphen/underscore swap.
//
// Migration must NOT use this function: it would copy an env override rather
// than the configured secret. Use StoredCredential for a store-only read.
func ResolveCredential(ctx context.Context, store CredentialStore, ref SecretRef) (string, error) {
	if v := os.Getenv(ref.EnvTokenKey()); v != "" {
		return v, nil
	}
	return store.Get(ctx, ref)
}

// StoredCredential reads the credential straight from the backend, ignoring
// any JIRA_TOKEN_* environment override. Migration uses this so a backend
// switch copies the configured secret, never a transient env value.
func StoredCredential(ctx context.Context, store CredentialStore, ref SecretRef) (string, error) {
	return store.Get(ctx, ref)
}

// cleanUpMigratedSource removes the old credential after a durable migration.
// A keyring source entry is jira-cli's own and is deleted outright. A
// 1Password source has only its managed credential field stripped — the store
// never destroys a 1Password item — so an informational note naming the item
// is returned. An error is returned only for a genuine delete failure.
func cleanUpMigratedSource(ctx context.Context, m CredentialMigration) (note string, err error) {
	ref := m.SourceRef
	if delErr := m.Source.Delete(ctx, ref); delErr != nil {
		if errors.Is(delErr, ErrCredentialNotFound) {
			return "", nil
		}
		return "", delErr
	}
	if ref.Backend == SecretBackendOnePassword {
		return fmt.Sprintf(
			"the old credential for profile %q was removed from 1Password item %q; the item itself was kept",
			m.Profile, ref.Item,
		), nil
	}
	return "", nil
}

// RevokeProfileCredential removes the credential for a profile from its
// backend. The keyring entry is keyed by site + profile and is jira-cli's own,
// so it is deleted outright. For the 1Password backend the store removes only
// jira-cli's managed credential field and always keeps the item itself — see
// OnePasswordStore.Delete.
//
// It reports whether a credential was removed and, for the 1Password backend,
// an informational note that the item was kept. An absent credential is not an
// error: revocation is idempotent.
func RevokeProfileCredential(ctx context.Context, store CredentialStore, ref SecretRef) (removed bool, note string, err error) {
	if delErr := store.Delete(ctx, ref); delErr != nil {
		if errors.Is(delErr, ErrCredentialNotFound) {
			return false, "", nil
		}
		return false, "", delErr
	}
	if ref.Backend == SecretBackendOnePassword {
		// The store removed only jira-cli's managed credential field; the
		// 1Password item itself is always kept.
		return true, fmt.Sprintf(
			"the 1Password item %q was kept; jira-cli removed only its credential field",
			ref.Item,
		), nil
	}
	return true, "", nil
}

// StoreCredentialTransactionally writes a credential to a backend around a
// metadata save, so a save failure never leaves an orphaned secret. It mirrors
// the migration transaction: the destination is snapshotted, the new
// credential is written (staged), then saveConfig runs. On a save failure the
// write is rolled back — a pre-existing value is restored, or a freshly
// written one is removed — and the save error is returned. A rollback that
// itself fails is surfaced alongside the save error, never swallowed.
func StoreCredentialTransactionally(ctx context.Context, store CredentialStore, ref SecretRef, secret string, saveConfig func() error) error {
	priorSecret, priorErr := store.Get(ctx, ref)
	priorExisted := priorErr == nil
	if priorErr != nil && !errors.Is(priorErr, ErrCredentialNotFound) {
		return fmt.Errorf("read prior credential for profile %q: %w", ref.Profile, priorErr)
	}
	if err := store.Put(ctx, ref, secret); err != nil {
		return fmt.Errorf("store credential for profile %q: %w", ref.Profile, err)
	}
	if saveErr := saveConfig(); saveErr != nil {
		var rbErr error
		if priorExisted {
			rbErr = store.Put(ctx, ref, priorSecret)
		} else {
			rbErr = store.Delete(ctx, ref)
			if errors.Is(rbErr, ErrCredentialNotFound) {
				rbErr = nil
			}
		}
		if rbErr != nil {
			return errors.Join(saveErr, fmt.Errorf("rollback also failed for profile %q (%s): %w",
				ref.Profile, ref.Backend, rbErr))
		}
		return saveErr
	}
	return nil
}

// CredentialIdentitiesDiffer reports whether two SecretRefs address different
// credential storage. It compares the secret backend, the keyring key (site
// host + profile) and, for the 1Password backend, the account, vault, and
// item. When an auth login re-points a profile — a new site, a backend switch,
// a different 1Password account, vault, or item — the re-derived identity
// differs and the credential under the old identity must be revoked.
func CredentialIdentitiesDiffer(a, b SecretRef) bool {
	if a.Backend != b.Backend {
		return true
	}
	if a.KeyringName() != b.KeyringName() {
		return true
	}
	if a.Backend == SecretBackendOnePassword || b.Backend == SecretBackendOnePassword {
		return a.Account != b.Account || a.Vault != b.Vault || a.Item != b.Item
	}
	return false
}

// CredentialMigration describes moving one profile's credential from a source
// backend to a destination backend. The refs are the fully namespaced
// identities produced by CredentialIdentity. ProfileIndex records the
// profile's position in the caller's profile slice so the migration and the
// profile it belongs to are a single ordered source of truth — no parallel
// index map has to be kept in lockstep.
type CredentialMigration struct {
	Profile      string
	ProfileIndex int
	Source       CredentialStore
	Destination  CredentialStore
	SourceRef    SecretRef
	DestRef      SecretRef
}

// CleanupFailure records a source secret that could not be deleted after the
// destination write and config save both succeeded. The migration is durable;
// the old secret is simply still present and must be removed by hand.
type CleanupFailure struct {
	Profile string
	Err     error
}

// CleanupNote records source storage that a successful migration deliberately
// left in place because jira-cli does not own it — a credential read via the
// shared legacy keyring key, or a 1Password item the profile named itself.
// The migration succeeded; the old credential simply still exists and the user
// may remove it by hand. A note is informational, not a failure.
type CleanupNote struct {
	Profile string
	Message string
}

// MigrationReport is the outcome of a MigrateCredentials batch. Cleanup
// failures and cleanup notes are reported, never hidden: the migration
// succeeded but a stale secret may remain in the source backend.
type MigrationReport struct {
	CleanupFailures []CleanupFailure
	CleanupNotes    []CleanupNote
}

// HasCleanupFailures reports whether any source secret could not be deleted.
func (r MigrationReport) HasCleanupFailures() bool {
	return len(r.CleanupFailures) > 0
}

// MigrateCredentials moves a batch of credentials between backends
// transactionally around a single metadata save:
//
//  1. Every destination secret is written first (staging). Before each
//     destination write the destination is snapshotted so rollback can
//     restore a pre-existing value. A write failure aborts the batch; any
//     destination secrets already staged are rolled back and the source
//     secrets are left untouched.
//  2. saveConfig persists the new backend metadata. A save failure rolls back
//     every staged destination secret and returns the save error; no source
//     secret is deleted.
//  3. Only after a durable save is each source credential cleaned up — but
//     only storage jira-cli owns: a keyring entry, or a 1Password item
//     jira-cli named by default. A 1Password item the profile named itself is
//     left in place and recorded as a cleanup note. A delete failure does not
//     fail the migration — it is recorded as a cleanup failure.
//
// Rollback errors are not swallowed: if undoing a staged write fails, the
// returned error names the affected profiles, so the caller is never told a
// failed migration was cleanly rolled back when a destination write actually
// could not be undone.
//
// The staged-write-before-save ordering means a crash between any two steps
// never leaves a profile whose config points at a backend with no secret.
func MigrateCredentials(ctx context.Context, migrations []CredentialMigration, saveConfig func() error) (MigrationReport, error) {
	type staged struct {
		migration CredentialMigration
		// priorDestExisted reports whether the destination already held a
		// credential before staging overwrote it. When true, priorDestSecret
		// is its value and rollback restores it; when false, rollback deletes
		// the staged write.
		priorDestExisted bool
		priorDestSecret  string
	}
	done := make([]staged, 0, len(migrations))

	// rollback undoes staged destination writes in reverse order, restoring a
	// pre-existing value or deleting a freshly staged one. It collects every
	// failure rather than discarding it, so a partly-failed rollback is
	// reported instead of masquerading as a clean one.
	rollback := func() []string {
		var failures []string
		for _, s := range slices.Backward(done) {

			var rbErr error
			if s.priorDestExisted {
				rbErr = s.migration.Destination.Put(ctx, s.migration.DestRef, s.priorDestSecret)
			} else {
				rbErr = s.migration.Destination.Delete(ctx, s.migration.DestRef)
				if errors.Is(rbErr, ErrCredentialNotFound) {
					rbErr = nil
				}
			}
			if rbErr != nil {
				failures = append(failures, fmt.Sprintf("profile %q (%s): %v",
					s.migration.Profile, s.migration.DestRef.Backend, rbErr))
			}
		}
		return failures
	}

	// abort runs rollback and combines the triggering cause with any rollback
	// failures into a single error, so a rollback that itself failed is never
	// hidden behind the original error.
	abort := func(cause error) error {
		if rbFailures := rollback(); len(rbFailures) > 0 {
			return fmt.Errorf("%w; rollback also failed for %s", cause, strings.Join(rbFailures, "; "))
		}
		return cause
	}

	for _, m := range migrations {
		secret, err := m.Source.Get(ctx, m.SourceRef)
		if err != nil {
			return MigrationReport{}, abort(fmt.Errorf("read credential for profile %q: %w", m.Profile, err))
		}
		// Snapshot the destination's prior state so rollback can restore a
		// pre-existing destination credential. A not-found read means the
		// destination held nothing.
		priorDest, priorErr := m.Destination.Get(ctx, m.DestRef)
		priorExisted := priorErr == nil
		if priorErr != nil && !errors.Is(priorErr, ErrCredentialNotFound) {
			return MigrationReport{}, abort(fmt.Errorf("read destination credential for profile %q: %w", m.Profile, priorErr))
		}
		if err := m.Destination.Put(ctx, m.DestRef, secret); err != nil {
			return MigrationReport{}, abort(fmt.Errorf("stage credential for profile %q: %w", m.Profile, err))
		}
		done = append(done, staged{
			migration:        m,
			priorDestExisted: priorExisted,
			priorDestSecret:  priorDest,
		})
	}

	if err := saveConfig(); err != nil {
		return MigrationReport{}, abort(fmt.Errorf("save config after staging credentials: %w", err))
	}

	var report MigrationReport
	for _, s := range done {
		note, err := cleanUpMigratedSource(ctx, s.migration)
		if err != nil {
			report.CleanupFailures = append(report.CleanupFailures, CleanupFailure{
				Profile: s.migration.Profile,
				Err:     err,
			})
			continue
		}
		if note != "" {
			report.CleanupNotes = append(report.CleanupNotes, CleanupNote{
				Profile: s.migration.Profile,
				Message: note,
			})
		}
	}
	return report, nil
}

// CredentialStatus resolves a profile's credential and reports it as an
// AuthStatus: valid with a redacted preview when found, or a sanitized,
// secret-free error when not. Source is reported as "env" when a JIRA_TOKEN_*
// override supplied the value. It resolves through ResolveCredential, so it
// reflects the effective runtime credential, env override included.
func CredentialStatus(ctx context.Context, store CredentialStore, ref SecretRef) AuthStatus {
	secret, err := ResolveCredential(ctx, store, ref)
	status := AuthStatus{Profile: ref.Profile, Source: string(ref.Backend)}
	if err != nil {
		status.Error = SanitizeCredentialError(err)
		return status
	}
	status.Valid = true
	status.Redacted = RedactSecret(secret)
	if os.Getenv(ref.EnvTokenKey()) != "" {
		status.Source = "env"
	}
	return status
}

// SanitizeCredentialError returns a human-safe, secret-free description of a
// credential failure. It branches on typed CredentialError values rather than
// matching error message substrings, so a backend rewording its errors
// cannot change the classification or leak raw text.
func SanitizeCredentialError(err error) string {
	if err == nil {
		return ""
	}
	// Typed errors first: a CredentialError that wraps ErrCredentialNotFound
	// (an env-backend miss naming its JIRA_TOKEN_* variable, a keyring miss
	// naming the profile) carries a more actionable message than the bare
	// sentinel, so the sentinel check must not shadow it.
	if ce, ok := errors.AsType[*CredentialError](err); ok {
		return ce.Message
	}
	if errors.Is(err, ErrCredentialNotFound) {
		return ErrCredentialNotFound.Error()
	}
	return "credential backend failed"
}

// RedactSecret returns a display-safe preview of a credential: the empty string
// for none, "****" for a value too short to partially reveal, otherwise "****"
// plus the last four bytes. It is the only sanctioned way to surface any part
// of a token — never print the raw value.
func RedactSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 4 {
		return "****"
	}
	return fmt.Sprintf("****%s", secret[len(secret)-4:])
}

// ErrCredentialEmpty reports that an explicitly supplied credential was empty
// after delimiter trimming. An empty credential is rejected rather than
// stored, so a blank line never silently becomes a saved secret. ReadSecret
// returns a CredentialError wrapping this sentinel, so callers can recover
// either the typed error or this value.
var ErrCredentialEmpty = errors.New("credential is empty")

// ReadSecret is the canonical reader for a credential supplied to the CLI
// (stdin, an environment variable, or a 1Password CLI field). It strips only
// the trailing CLI record delimiter — a single trailing CR and/or LF — and
// preserves every other byte, including interior and leading whitespace, so a
// token whose own bytes are meaningful is never corrupted.
//
// An input that is empty, or empty once the delimiter is removed, is rejected
// with a CredentialError carrying ErrorCodeCredentialEmpty: an explicitly
// blank credential is an error, not a stored value.
func ReadSecret(raw string) (string, error) {
	secret := strings.TrimRight(raw, "\r\n")
	if xstrings.IsBlank(secret) {
		return "", &CredentialError{
			Type:    ErrorTypeValidation,
			ErrCode: ErrorCodeCredentialEmpty,
			Message: "the supplied credential is empty",
			HintMsg: "supply a non-empty credential",
			Wrapped: ErrCredentialEmpty,
		}
	}
	return secret, nil
}

// key is the in-memory store key. It includes the site host so two profiles
// that share a name but point at different sites never address the same
// in-memory entry.
func (r SecretRef) key() string {
	return string(r.Backend) + ":" + r.Host + ":" + r.Profile + ":" + r.Account + ":" + r.Vault + ":" + r.Item
}

// credentialMissingError reports that no credential is stored for a profile.
// It is a typed CredentialError wrapping ErrCredentialNotFound, so callers can
// match the sentinel with errors.Is while users get an actionable message.
func credentialMissingError(profile string) error {
	return &CredentialError{
		Type:    ErrorTypeAuth,
		ErrCode: ErrorCodeCredentialMissing,
		Message: fmt.Sprintf("credential not found for profile %q — run `jira auth login`", profile),
		HintMsg: "run `jira auth login` to store a credential for this profile",
		Context: ErrorContext{ConfigKey: "profile.name"},
		Wrapped: ErrCredentialNotFound,
	}
}

// namespaceCollisionError reports a profile name that cannot be encoded into
// a safe keyring or environment-variable key.
func namespaceCollisionError(name string) error {
	return &CredentialError{
		Type:    ErrorTypeValidation,
		ErrCode: ErrorCodeCredentialNamespaceCollision,
		Message: fmt.Sprintf("profile name %q cannot be used as a credential key", name),
		HintMsg: "use a profile name containing only lowercase letters, digits, hyphen, and underscore",
		Context: ErrorContext{ConfigKey: "profile.name"},
	}
}
