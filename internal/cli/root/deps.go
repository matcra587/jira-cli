package root

import (
	"os"
	"strings"
	"sync"

	"github.com/gechr/clive/notify"
	"github.com/gechr/clive/updater"

	"github.com/matcra587/jira-cli/internal/adapters"
	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/adapters/keyring"
	"github.com/matcra587/jira-cli/internal/cli"
	authcmd "github.com/matcra587/jira-cli/internal/cli/auth"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	jqlcmd "github.com/matcra587/jira-cli/internal/cli/jql"
	updatecmd "github.com/matcra587/jira-cli/internal/cli/update"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
	"github.com/matcra587/jira-cli/internal/selfupdate"
)

// Deps is the composition root's wiring: every I/O adapter a command can
// need, injected as a constructor so nothing is built until a RunE that
// needs it runs. Manual constructor injection only — no container, no
// globals, no init() — and each constructor is once-per-process via
// sync.OnceValues, so every command in an invocation shares one instance.
type Deps struct {
	// NotifyTool performs anonymous background release reads. A nil tool disables
	// passive checks; NotifyOptions supplies Clive's cache and transport seams.
	NotifyTool    updater.Tool
	NotifyOptions []notify.Option
	// Update owns installation and public release I/O independently of Jira.
	Update updatecmd.Deps
	// JiraClient returns the process's Jira adapter, constructing it on
	// first call. Help, completion, and version paths must never call it
	// — speed is a felt property, and those paths open no network client.
	// TestClientFreePathsConstructNothing pins the contract.
	JiraClient func() (core.JiraClient, error)

	// Cache returns the process's metadata cache — a disk cache, because
	// the reads it spares span invocations rather than repeating inside
	// one (a CLI runs once and exits, so an in-process map could never
	// help). Constructed on first use like the client above.
	Cache func() (core.Cache, error)

	// BuildPreferences and CacheFor serve local JQL and cache management without
	// opening the credential store or constructing a Jira client.
	BuildPreferences func() (jqlcmd.BuildPreferences, error)
	CacheFor         func(site, account string) (core.Cache, error)

	// Credentials opens the operating system credential store. A
	// constructor so nothing touches the keyring until a command that
	// needs it runs — and so tests substitute one instead of writing to
	// the developer's real store.
	Credentials func() (core.CredentialStore, error)

	// JiraClientFor builds a client for an explicit account and token
	// rather than the resolved configuration. `auth login` needs it:
	// verifying against resolved config would test the OLD token and
	// report success for a credential nobody checked.
	JiraClientFor func(site, email, token string) (core.JiraClient, error)

	// Detect resolves the output environment (TTY, agent markers) for
	// --output=auto. It is a seam, not a cache: called at most once per
	// invocation, and only when the mode is actually auto — an explicit
	// --output must not probe the environment it overrides. Tests inject
	// a fabricated Detection instead of fighting the process's real
	// stdout and env.
	Detect func() cli.Detection
}

// defaultDeps wires the production constructors.
func defaultDeps() *Deps {
	return &Deps{
		NotifyTool: selfupdate.ReleaseTool{},
		Update:     updatecmd.DefaultDeps(),
		JiraClient: sync.OnceValues(newJiraClient),
		// OnceValues for JiraClient's reason: resolving the cache
		// directory is filesystem work, and several commands in one run
		// must not each repeat it.
		Cache:            sync.OnceValues(newCache),
		BuildPreferences: buildPreferences,
		CacheFor:         newScopedDiskCache,
		Credentials:      credentialStore,
		JiraClientFor: func(site, email, token string) (core.JiraClient, error) {
			resolved, err := config.Load(config.Options{})
			if err != nil {
				return nil, err
			}
			return adapterjira.New(adapterjira.Config{
				BaseURL:  site,
				Email:    email,
				APIToken: token,
				ReadOnly: resolved.Config.ReadOnly,
				Timeout:  resolved.Config.Timeout,
				Retry:    core.DefaultRetryPolicy(),
			})
		},
		Detect: func() cli.Detection {
			return cli.Detect(os.Stdout)
		},
	}
}

// workdaySeconds resolves only preferences; it never opens the keyring or
// constructs a Jira client, including on a local worklog preview.
func workdaySeconds() (int, error) {
	resolved, err := config.Load(config.Options{})
	if err != nil {
		return 0, err
	}
	return resolved.Config.WorkdaySeconds, nil
}

func buildPreferences() (jqlcmd.BuildPreferences, error) {
	resolved, err := config.Load(config.Options{})
	if err != nil {
		return jqlcmd.BuildPreferences{}, err
	}
	return jqlcmd.BuildPreferences{BaseURL: resolved.Config.BaseURL, Account: resolved.Config.Email}, nil
}

func newScopedDiskCache(site, account string) (core.Cache, error) {
	disk, err := adapters.NewDiskCache()
	if err != nil {
		return nil, err
	}
	return adapters.NewScopedCache(disk, site, account)
}

// newCache builds the metadata cache, isolated by resolved site and account.
//
// A DISK cache, not the in-memory one: a CLI process runs once and exits,
// so an in-process map can only serve a repeated read WITHIN one
// invocation, while the reads worth sparing here (a site's link types)
// repeat ACROSS them. adapters.MemoryCache stays for the former case and
// for tests.
//
// A failure to resolve the cache directory is returned rather than
// swallowed, but callers are expected to treat it as "no cache" rather
// than as a command failure — readLinkTypes' whole design is that every
// cache problem falls through to the read that was going to happen
// anyway.
func newCache() (core.Cache, error) {
	// Existing read-through consumers can resolve an account stored only in
	// keyring. Local management uses configuredCache instead.
	store, err := credentialStore()
	if err != nil {
		return nil, err
	}
	resolved, err := config.Load(config.Options{Credentials: store})
	if err != nil {
		return nil, err
	}
	return newScopedDiskCache(resolved.Config.BaseURL, resolved.Config.Email)
}

// configuredCache shares the explicit-scope factory with offline JQL. It never
// guesses an account or opens the credential-backed read-through constructor.
func configuredCache(deps *Deps) (core.Cache, error) {
	scope, err := metadataCacheScope(deps, true)
	return scope.Store, err
}

// newJiraClient resolves configuration and builds the adapter.
//
// Validation happens HERE rather than at load: a half-configured state
// must stay inspectable by `config current`, and the paths that need no
// client — help, completion, version — must not fail for want of
// credentials they never use. This is the point where the values are
// actually required, so this is where their absence is an error.
//
// Config carries ReadOnly into the transport, which is what makes the
// write gate reachable: an operator sets JIRA_READ_ONLY and every
// command is covered, including ones that never think to check.
func newJiraClient() (core.JiraClient, error) {
	store, err := credentialStore()
	if err != nil {
		return nil, err
	}
	return jiraClientFrom(config.Options{Credentials: store})
}

func jiraClientFrom(opts config.Options) (core.JiraClient, error) {
	resolved, err := config.Load(opts)
	if err != nil {
		return nil, err
	}
	if err := credentialReadFailure(resolved); err != nil {
		return nil, err
	}
	if err := resolved.Config.Validate(); err != nil {
		return nil, err
	}
	return adapterjira.New(adapterjira.Config{
		BaseURL:  resolved.Config.BaseURL,
		Email:    resolved.Config.Email,
		APIToken: resolved.Config.APIToken,
		ReadOnly: resolved.Config.ReadOnly,
		// Resolved through the layer stack, so --timeout, JIRA_TIMEOUT and
		// a `timeout` in the config file all arrive by the same route and
		// `config current` can say which one won.
		Timeout: resolved.Config.Timeout,
		// Retry is ON by default, per the design: a safe request rides
		// through a throttle or a transient 5xx instead of failing at the
		// first one. The fail-fast escape arrives with the flag layer.
		// Writes are never replayed whatever this says — the transport
		// classifies them, not the policy.
		Retry: core.DefaultRetryPolicy(),
	})
}

// credentialStore opens the OS credential store, unless it is turned
// off.
//
// The setting is resolved from a load that does NOT consult the store,
// because deciding whether to open it cannot depend on having opened it.
func credentialStore() (core.CredentialStore, error) {
	resolved, err := config.Load(config.Options{})
	if err != nil {
		return nil, err
	}
	if !resolved.Config.Keyring {
		return core.NoCredentialStore{}, nil
	}
	if resolved.NamedProfiles && resolved.Config.CredentialID == "" {
		return credentials.UnselectedStore{}, nil
	}
	return credentials.Select(keyring.New(), resolved.Config.CredentialID), nil
}

// siteInfoWithoutStore resolves as siteInfo does but with the credential
// store withheld, which answers "what would still authenticate if the
// stored record were gone?".
func siteInfoWithoutStore() (authcmd.SiteInfo, error) {
	return siteFrom(config.Options{})
}

// siteInfo resolves the configured site, account, and token, and reports
// which layer supplied the token.
//
// The source is the interesting part. An environment variable silently
// beating the keyring is the most confusing state `auth status` can
// find, and provenance is already how this CLI answers "why is it this?"
// — so the answer comes from the loader's own layer tracking rather than
// being guessed at the call site.
func siteInfo() (authcmd.SiteInfo, error) {
	store, err := credentialStore()
	if err != nil {
		return authcmd.SiteInfo{}, err
	}
	return siteFrom(config.Options{Credentials: store})
}

// siteFrom resolves configuration and reports the identity plus the
// layer the token came from.
func siteFrom(opts config.Options) (authcmd.SiteInfo, error) {
	resolved, err := config.Load(opts)
	if err != nil {
		return authcmd.SiteInfo{}, err
	}
	if err := credentialReadFailure(resolved); err != nil {
		return authcmd.SiteInfo{}, err
	}
	return authcmd.SiteInfo{
		BaseURL:     resolved.Config.BaseURL,
		Email:       resolved.Config.Email,
		Token:       resolved.Config.APIToken,
		TokenSource: string(resolved.Source("api_token")),
	}, nil
}

func credentialReadFailure(resolved *config.Resolved) error {
	if resolved.CredentialError == nil || strings.TrimSpace(resolved.Config.APIToken) != "" {
		return nil
	}
	return &cli.CodedError{
		Code:    cli.CodeAuthStoreUnavailable,
		Message: "the system credential store could not be read; stored credentials were not verified",
		Cause:   resolved.CredentialError,
	}
}

// metadataCacheScope keeps live keyring resolution in the established cache
// constructor while previews use only explicit non-secret scope preferences.
func metadataCacheScope(deps *Deps, local bool) (cachecmd.MetadataScope, error) {
	if deps.BuildPreferences == nil || local && deps.CacheFor == nil {
		return cachecmd.MetadataScope{}, core.ErrCacheManagementUnsupported
	}
	prefs, err := deps.BuildPreferences()
	if err != nil {
		return cachecmd.MetadataScope{}, err
	}
	field := ""
	if config.ValidateBaseURL(prefs.BaseURL) != nil {
		field = "base_url"
	} else if local && strings.TrimSpace(prefs.Account) == "" {
		field = "email"
	}
	if field != "" {
		input := cli.NewInputError(cli.CodeConfigInvalid, "metadata cache needs a configured base_url; local management and previews also need email in configuration or environment")
		input.Field = field
		return cachecmd.MetadataScope{}, input
	}
	var store core.Cache
	if local {
		store, err = deps.CacheFor(prefs.BaseURL, prefs.Account)
	} else if deps.Cache != nil {
		store, err = deps.Cache()
	} else {
		err = core.ErrCacheManagementUnsupported
	}
	return cachecmd.MetadataScope{Site: prefs.BaseURL, Email: prefs.Account, Store: store}, err
}
