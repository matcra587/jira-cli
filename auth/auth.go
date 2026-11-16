package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters/credentialstore"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
)

// ErrNotAuthenticated identifies unavailable or unusable saved authentication.
var ErrNotAuthenticated = errors.New(
	"jira authentication unavailable; check the selected profile with jira auth status",
)

// ErrMigrationRequired identifies a recognized legacy configuration.
var ErrMigrationRequired = config.ErrMigrationRequired

// ErrNoCredential is returned by credential stores for an absent record.
var ErrNoCredential = core.ErrNoCredential

// CredentialStore is the context-aware store used by the CLI. Injection keeps
// applications and tests independent of native keychains and secret managers.
type CredentialStore = core.CredentialStore

// Options selects a saved profile. An empty Profile follows JIRA_PROFILE and
// the saved default. ConfigPath selects a file instead of the platform default.
type Options struct {
	Profile         string
	ConfigPath      string
	CredentialStore CredentialStore
}

// String omits private config paths and provider implementations.
func (o Options) String() string { return "jira auth options" }

// GoString keeps provider state out of diagnostic formatting.
func (o Options) GoString() string { return o.String() }

// MarshalJSON omits private config paths and provider implementations.
func (o Options) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Profile string `json:"profile,omitempty"`
	}{Profile: o.Profile})
}

// Identity is configured routing metadata, not proof of a live Jira identity.
// Verify the authenticated account through Jira before binding application data.
type Identity struct {
	Profile string `json:"profile"`
	BaseURL string `json:"base_url"`
	Email   string `json:"email"`
	CloudID string `json:"cloud_id,omitempty"`
}

// Session is one credential snapshot. Resolve again for each check or sync batch
// to observe changed credentials; an existing session never changes accounts.
type Session struct {
	identity Identity
	token    string
	timeout  time.Duration
	readOnly bool
}

// Identity returns configured metadata without credentials.
func (s Session) Identity() Identity { return s.identity }

// Token explicitly exposes the credential for constructing an authenticated
// client. Never log, persist or pass this value to a subprocess.
func (s Session) Token() string { return s.token }

// Timeout is the selected per-request timeout; zero means no deadline.
func (s Session) Timeout() time.Duration { return s.timeout }

// ReadOnly reports the effective profile policy.
func (s Session) ReadOnly() bool { return s.readOnly }

// String omits credential values from normal formatting.
func (s Session) String() string {
	raw, err := s.MarshalJSON()
	if err != nil {
		return "jira auth session"
	}
	return string(raw)
}

// GoString omits credentials even with %#v formatting.
func (s Session) GoString() string { return s.String() }

// MarshalJSON includes configured identity only.
func (s Session) MarshalJSON() ([]byte, error) { return json.Marshal(s.identity) }

type resolutionError struct{ cause error }

func (e resolutionError) Error() string    { return ErrNotAuthenticated.Error() }
func (e resolutionError) String() string   { return e.Error() }
func (e resolutionError) GoString() string { return e.Error() }
func (e resolutionError) Unwrap() []error  { return []error{ErrNotAuthenticated, e.cause} }

// Resolve captures one profile/configuration/environment snapshot and reads its
// credential through the same providers as the CLI. It never changes defaults,
// starts a login flow or calls Jira. Synchronous keychain calls cannot always be
// interrupted once entered. Wrapped provider causes are sensitive diagnostics.
func Resolve(ctx context.Context, opts Options) (Session, error) {
	captured, err := config.CaptureOptions(ctx, config.Options{Profile: opts.Profile, Path: opts.ConfigPath})
	if err != nil {
		return Session{}, resolutionFailure(ctx, err)
	}
	local, err := config.Load(captured)
	if err != nil {
		return Session{}, resolutionFailure(ctx, err)
	}
	store := opts.CredentialStore
	if store == nil {
		store = credentialstore.FromResolved(local)
	}
	resolved, err := config.Resolve(ctx, captured, store)
	if err != nil {
		return Session{}, resolutionFailure(ctx, err)
	}
	if resolved.CredentialError != nil && strings.TrimSpace(resolved.Config.APIToken) == "" {
		return Session{}, resolutionFailure(ctx, resolved.CredentialError)
	}
	if err := resolved.Config.Validate(); err != nil {
		return Session{}, resolutionFailure(ctx, err)
	}
	cfg := resolved.Config
	return Session{
		identity: Identity{Profile: resolved.Profile, BaseURL: cfg.BaseURL, Email: cfg.Email, CloudID: cfg.CloudID},
		token:    cfg.APIToken, timeout: cfg.Timeout, readOnly: cfg.ReadOnly,
	}, nil
}

func resolutionFailure(ctx context.Context, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return resolutionError{cause: cause}
}
