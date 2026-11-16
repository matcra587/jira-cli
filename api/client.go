package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/matcra587/jira-cli/auth"
	adapter "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/core"
)

// DefaultMaxResponseBytes bounds ordinary JSON responses to 16 MiB.
const DefaultMaxResponseBytes = core.DefaultMaxAPIResponseBytes

// Credentials selects explicit authentication. When supplied, no saved
// configuration, environment credentials or credential providers are consulted.
type Credentials struct {
	BaseURL  string
	CloudID  string
	Email    string
	APIToken string `json:"-"`
}

// String hides credentials and potentially unvalidated URL contents.
func (Credentials) String() string { return "jira credentials (redacted)" }

// GoString hides private values even with %#v formatting.
func (c Credentials) GoString() string { return c.String() }

// MarshalJSON omits all unvalidated credential input.
func (Credentials) MarshalJSON() ([]byte, error) { return []byte("{}"), nil }

// RetryPolicy controls only automatic retries of safe requests. Writes are
// never automatically replayed. A zero policy disables retries.
type RetryPolicy = core.RetryPolicy

// ClientOptions selects authentication and request policy. Nil Credentials
// resolves the selected saved profile; nonnil Credentials selects explicit mode
// even when incomplete. Constructors do not call Jira.
type ClientOptions struct {
	Profile         string
	ConfigPath      string
	Credentials     *Credentials `json:"-"`
	CredentialStore auth.CredentialStore
	HTTPClient      *http.Client
	// Positive Timeout overrides the saved per-request timeout. Zero retains
	// the saved/caller HTTP-client timeout; negative values are invalid.
	Timeout time.Duration
	// ReadOnly can restrict a client but cannot relax a saved read-only policy.
	ReadOnly bool
	// Zero selects DefaultMaxResponseBytes. Negative values are invalid.
	MaxResponseBytes int64
	// Nil uses the CLI's bounded safe-read policy.
	Retry *RetryPolicy
}

// String omits credentials, local paths and injected implementations.
func (o ClientOptions) String() string {
	raw, err := o.MarshalJSON()
	if err != nil {
		return "jira client options"
	}
	return string(raw)
}

// GoString omits credentials and injected provider state.
func (o ClientOptions) GoString() string { return o.String() }

// MarshalJSON exposes reviewed request-policy metadata only.
func (o ClientOptions) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Profile          string        `json:"profile,omitempty"`
		Timeout          time.Duration `json:"timeout,omitempty"`
		ReadOnly         bool          `json:"read_only"`
		MaxResponseBytes int64         `json:"max_response_bytes,omitempty"`
	}{Profile: o.Profile, Timeout: o.Timeout, ReadOnly: o.ReadOnly, MaxResponseBytes: o.MaxResponseBytes})
}

// Client is an immutable credential and policy snapshot for one application
// check or sync batch. Construct a fresh client to observe changed credentials.
// It never follows a changed saved default during a paginated read.
type Client struct {
	transport *adapter.Client
	identity  auth.Identity
	limit     int64
}

// NewClientWithContext resolves one saved profile or validates explicit
// credentials. It reads no authentication when ctx is already canceled. Native
// providers receive ctx, but an entered OS keychain call may be synchronous.
func NewClientWithContext(ctx context.Context, opts ClientOptions) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	invalidTimeout := opts.Timeout < 0
	invalidResponseBound := opts.MaxResponseBytes < 0 || opts.MaxResponseBytes == 1<<63-1
	if invalidTimeout || invalidResponseBound {
		return nil, errors.New("jira client timeout and response bound must be valid nonnegative values")
	}
	// The adapter composes this explicit policy with its mandatory guards.
	// A connector must not accept identity JSON from a redirected service.
	httpClient := &http.Client{}
	if opts.HTTPClient != nil {
		*httpClient = *opts.HTTPClient
	}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return adapter.ErrUnsafeRedirect }
	cfg := adapter.Config{HTTPClient: httpClient, ReadOnly: opts.ReadOnly, Retry: core.DefaultRetryPolicy()}
	var identity auth.Identity
	if explicit := opts.Credentials; explicit != nil {
		if strings.TrimSpace(explicit.Email) == "" || strings.TrimSpace(explicit.APIToken) == "" {
			return nil, errors.New("jira explicit credentials require an email and API token")
		}
		cfg.BaseURL, cfg.CloudID = explicit.BaseURL, explicit.CloudID
		cfg.Email, cfg.APIToken = explicit.Email, explicit.APIToken
		identity = auth.Identity{BaseURL: explicit.BaseURL, CloudID: explicit.CloudID, Email: explicit.Email}
	} else {
		session, err := auth.Resolve(ctx, auth.Options{
			Profile: opts.Profile, ConfigPath: opts.ConfigPath,
			CredentialStore: opts.CredentialStore,
		})
		if err != nil {
			return nil, err
		}
		identity = session.Identity()
		cfg.BaseURL, cfg.CloudID = identity.BaseURL, identity.CloudID
		cfg.Email, cfg.APIToken = identity.Email, session.Token()
		cfg.Timeout, cfg.ReadOnly = session.Timeout(), cfg.ReadOnly || session.ReadOnly()
	}
	if opts.Timeout > 0 {
		cfg.Timeout = opts.Timeout
	}
	if opts.Retry != nil {
		cfg.Retry = *opts.Retry
	}
	transport, err := adapter.New(cfg)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit := opts.MaxResponseBytes
	if limit == 0 {
		limit = DefaultMaxResponseBytes
	}
	identity.BaseURL = transport.BaseURL()
	return &Client{transport: transport, identity: identity, limit: limit}, nil
}

// Identity returns configured metadata. Construction does not verify the live
// Jira account; read /rest/api/3/myself and bind its native accountId explicitly.
func (c *Client) Identity() auth.Identity { return c.identity }

// String exposes configured metadata without inspecting private transport state.
func (c Client) String() string {
	raw, err := c.MarshalJSON()
	if err != nil {
		return "jira client"
	}
	return string(raw)
}

// GoString keeps %#v from inspecting private credentials.
func (c Client) GoString() string { return c.String() }

// MarshalJSON includes configured identity only.
func (c Client) MarshalJSON() ([]byte, error) { return json.Marshal(c.identity) }
