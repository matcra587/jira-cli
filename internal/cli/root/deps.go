package root

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/gechr/clive"
	"github.com/gechr/clive/notify"
	"github.com/gechr/clive/updater"

	"github.com/matcra587/jira-cli/internal/adapters"
	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/adapters/keyring"
	adapteronepassword "github.com/matcra587/jira-cli/internal/adapters/onepassword"
	"github.com/matcra587/jira-cli/internal/cli"
	authcmd "github.com/matcra587/jira-cli/internal/cli/auth"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/cli/command"
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
// globals, no init() — and each credential-backed constructor is memoized by
// command.OnceContextValues, so an invocation shares one lazily built instance.
type Deps struct {
	// BindContext connects lazy legacy constructors to Cobra's execution context.
	BindContext   func(func() context.Context)
	EditorCommand func(context.Context) ([]string, error)
	EditFile      func(context.Context, []string, string) error

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
	JiraClient func(ctx context.Context) (core.JiraClient, error)

	// Cache returns the process's metadata cache — a disk cache, because
	// the reads it spares span invocations rather than repeating inside
	// one (a CLI runs once and exits, so an in-process map could never
	// help). Constructed on first use like the client above.
	Cache func(ctx context.Context) (core.Cache, error)

	// BuildPreferences and CacheFor serve local JQL and cache management without
	// opening the credential store or constructing a Jira client.
	BuildPreferences func() (jqlcmd.BuildPreferences, error)
	CacheFor         func(site, account string) (core.Cache, error)

	// Credentials constructs the selected credential store. A
	// constructor so nothing opens credentials until a command that
	// needs it runs — and so tests substitute one instead of writing to
	// the developer's real store.
	Credentials     func() (core.CredentialStore, error)
	MigrationStores config.MigrationStores

	// JiraClientFor builds a client for an explicit account and token
	// rather than the resolved configuration. `auth login` needs it:
	// verifying against resolved config would test the OLD token and
	// report success for a credential nobody checked.
	JiraClientFor func(site, email, token, cloudID string) (core.JiraClient, error)
	// IdentitySaveSite and IdentitySaveClientFor use the ordinary resolution and
	// transport owners while admitting an absent explicitly selected save target.
	IdentitySaveSite      func(context.Context) (authcmd.SiteInfo, error)
	IdentitySaveClientFor func(site, email, token, cloudID string) (core.JiraClient, error)
	LoginSite             func(context.Context, authcmd.LoginDefaults) (authcmd.SiteInfo, error)
	LoginCredentials      func(context.Context, core.CredentialLocation) (core.CredentialStore, error)
	Site                  func(context.Context) (authcmd.SiteInfo, error)
	SiteWithoutStore      func(context.Context) (authcmd.SiteInfo, error)
	PrepareLoginLocation  func(context.Context, core.CredentialLocation) (core.CredentialLocation, core.AccountIdentityPublication, error)
	LoginTarget           func(context.Context, core.LoginIdentityBinding) (core.AccountIdentityTarget, error)
	IdentityTarget        func(context.Context, core.AccountIdentityScope) (core.AccountIdentityTarget, error)
	ResolveConfig         func(context.Context) (*config.Resolved, error)
	WorkdaySeconds        func() (int, error)
	ValidateLoginScope    func(context.Context, string) error

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
	settings := newInvocationSettings()
	store := sync.OnceValues(func() (core.CredentialStore, error) {
		ctx, err := settings.currentContext()
		if err != nil {
			return nil, err
		}
		resolved, err := settings.public(ctx, false)
		if err != nil {
			return nil, err
		}
		return credentialStoreFromResolved(resolved), nil
	})
	resolved := command.OnceContextValues(func(ctx context.Context) (*config.Resolved, error) {
		opts, err := settings.options(ctx, false)
		if err != nil {
			return nil, err
		}
		selected, err := store()
		if err != nil {
			return nil, err
		}
		return config.Resolve(ctx, opts, selected)
	})
	deps := &Deps{
		BindContext:   func(contextFor func() context.Context) { settings.contextFor = contextFor },
		EditorCommand: editorCommand,
		EditFile:      command.RunEditor,
		NotifyTool:    selfupdate.ReleaseTool{},
		Update:        updatecmd.DefaultDeps(),
		JiraClient: command.OnceContextValues(func(ctx context.Context) (core.JiraClient, error) {
			configuration, err := resolved(ctx)
			if err != nil {
				return nil, err
			}
			return jiraClientFromResolved(configuration)
		}),
		// OnceContextValues for JiraClient's reason: resolving the cache
		// directory is filesystem work, and several commands in one run
		// must not each repeat it.
		Cache: command.OnceContextValues(func(ctx context.Context) (core.Cache, error) {
			configuration, err := resolved(ctx)
			if err != nil {
				return nil, err
			}
			return newScopedDiskCache(configuration.Config.BaseURL, configuration.Config.Email)
		}),
		BuildPreferences:      settings.buildPreferences,
		CacheFor:              newScopedDiskCache,
		Credentials:           store,
		MigrationStores:       migrationStore,
		JiraClientFor:         settings.clientFor(false),
		IdentitySaveSite:      settings.identitySaveSite,
		IdentitySaveClientFor: settings.clientFor(true),
		LoginSite:             settings.loginSite,
		LoginCredentials:      settings.loginCredentials,
		Site: func(ctx context.Context) (authcmd.SiteInfo, error) {
			configuration, err := resolved(ctx)
			if err != nil {
				return authcmd.SiteInfo{}, err
			}
			if err := credentialReadFailure(configuration); err != nil {
				return authcmd.SiteInfo{}, err
			}
			return siteFromResolved(configuration), nil
		},
		SiteWithoutStore:     settings.siteWithoutStore,
		PrepareLoginLocation: settings.prepareLoginLocation,
		LoginTarget:          settings.loginTarget,
		IdentityTarget:       settings.identityTarget,
		ResolveConfig:        resolved,
		WorkdaySeconds:       settings.workdaySeconds,
		ValidateLoginScope:   settings.validateLoginScope,
		Detect: func() cli.Detection {
			return cli.Detect(os.Stdout)
		},
	}
	return deps
}

// editorCommand uses the selected public settings file, never a credential
// store or token environment reference. An explicit override can repair work
// even when that settings file cannot be decoded.
func editorCommand(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	configured := ""
	if os.Getenv("JIRA_EDITOR") == "" {
		file, err := config.ReadFile(ctx, "")
		if err != nil {
			return nil, err
		}
		setting, err := file.Setting("editor")
		if err != nil {
			return nil, err
		}
		configured = setting.Value
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return command.ResolveEditor(configured)
}

func resolveIdentitySaveSite(ctx context.Context, opts config.Options, newStore func(config.Config) core.CredentialStore) (authcmd.SiteInfo, error) {
	// Windows may report a regular-file ancestor as not-found. Missing-target
	// loading alone cannot prove creation is valid; check the shared owner
	// before entering any credential provider.
	if _, err := config.OpenFile(ctx, opts.Path); err != nil {
		return authcmd.SiteInfo{}, err
	}
	resolved, err := config.Resolve(ctx, opts, nil)
	if err != nil {
		return authcmd.SiteInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return authcmd.SiteInfo{}, err
	}
	return siteFrom(ctx, opts, newStore(resolved.Config))
}

func resolveLoginSite(ctx context.Context, opts config.Options, defaults authcmd.LoginDefaults, newStore func(config.Config) core.CredentialStore) (authcmd.SiteInfo, error) {
	if _, err := config.OpenFile(ctx, opts.Path); err != nil {
		return authcmd.SiteInfo{}, err
	}
	resolved, err := config.Resolve(ctx, opts, nil)
	if err != nil {
		return authcmd.SiteInfo{}, err
	}
	if resolved.Config.SecretBackend != core.CredentialBackendEnvironment &&
		(defaults.Site && strings.TrimSpace(resolved.Config.BaseURL) == "" || defaults.Email && strings.TrimSpace(resolved.Config.Email) == "") {
		if err := ctx.Err(); err != nil {
			return authcmd.SiteInfo{}, err
		}
		stored, err := siteFrom(ctx, opts, newStore(resolved.Config))
		if err != nil {
			return authcmd.SiteInfo{}, err
		}
		public := siteFromResolved(resolved)
		if defaults.Site && strings.TrimSpace(public.BaseURL) == "" {
			public.BaseURL = stored.BaseURL
		}
		if defaults.Email && strings.TrimSpace(public.Email) == "" {
			public.Email = stored.Email
		}
		return public, nil
	}
	return siteFromResolved(resolved), nil
}

func resolveLoginCredentials(ctx context.Context, opts config.Options, selected core.CredentialLocation, newStore func(config.Config) core.CredentialStore) (core.CredentialStore, error) {
	if _, err := config.OpenFile(ctx, opts.Path); err != nil {
		return nil, err
	}
	resolved, err := config.Resolve(ctx, opts, nil)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.CredentialSelection(*resolved) != selected {
		return nil, cli.NewCodedError(cli.CodeConfigChanged, "selected credential provider changed during login; inspect the configuration before retrying")
	}
	return newStore(resolved.Config), nil
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
	return jqlcmd.BuildPreferences{BaseURL: resolved.Config.BaseURL, Account: resolved.Config.Email, CachedAccountID: resolved.Config.CachedAccountID(), DefaultProject: resolved.Config.DefaultProject, DefaultIssueType: resolved.Config.DefaultIssueType, DefaultBoard: resolved.Config.DefaultBoard}, nil
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
func newCache(ctx context.Context, store core.CredentialStore) (core.Cache, error) {
	// Existing read-through consumers can resolve an account stored only in
	// keyring. Local management uses configuredCache instead.
	resolved, err := config.Resolve(ctx, config.Options{}, store)
	if err != nil {
		return nil, err
	}
	return newScopedDiskCache(resolved.Config.BaseURL, resolved.Config.Email)
}

// configuredCache shares the explicit-scope factory with offline JQL. It never
// guesses an account or opens the credential-backed read-through constructor.
func configuredCache(ctx context.Context, deps *Deps) (core.Cache, error) {
	scope, err := metadataCacheScope(ctx, deps, true)
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
func jiraClientFrom(ctx context.Context, opts config.Options, store core.CredentialStore) (core.JiraClient, error) {
	resolved, err := config.Resolve(ctx, opts, store)
	if err != nil {
		return nil, err
	}
	return jiraClientFromResolved(resolved)
}

func jiraClientFromResolved(resolved *config.Resolved) (core.JiraClient, error) {
	if err := credentialReadFailure(resolved); err != nil {
		return nil, err
	}
	if err := resolved.Config.Validate(); err != nil {
		return nil, err
	}
	return adapterjira.New(adapterjira.Config{
		BaseURL:  resolved.Config.BaseURL,
		CloudID:  resolved.Config.CloudID,
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

// credentialStore selects a credential provider without opening it.
//
// The setting is resolved from a load that does NOT consult the store,
// because deciding whether to open it cannot depend on having opened it.
func credentialStore() (core.CredentialStore, error) {
	resolved, err := config.Load(config.Options{})
	if err != nil {
		return nil, err
	}
	return credentialStoreFromResolved(resolved), nil
}

func credentialStoreFromResolved(resolved *config.Resolved) core.CredentialStore {
	if resolved.NamedProfiles && resolved.Config.SecretBackend == core.CredentialBackendKeyring && resolved.Config.CredentialID == "" {
		return core.NoCredentialStore{}
	}
	return credentialStoreFor(resolved.Config)
}

// siteInfoWithoutStore resolves as siteInfo does but with the credential
// store withheld, which answers "what would still authenticate if the
// stored record were gone?".
func siteInfoWithoutStore(ctx context.Context) (authcmd.SiteInfo, error) {
	return siteFrom(ctx, config.Options{}, nil)
}

// siteInfo resolves the configured site, account, and token, and reports
// which layer supplied the token.
//
// The source is the interesting part. An environment variable silently
// beating the keyring is the most confusing state `auth status` can
// find, and provenance is already how this CLI answers "why is it this?"
// — so the answer comes from the loader's own layer tracking rather than
// being guessed at the call site.
func siteInfo(ctx context.Context) (authcmd.SiteInfo, error) {
	store, err := credentialStore()
	if err != nil {
		return authcmd.SiteInfo{}, err
	}
	return siteFrom(ctx, config.Options{}, store)
}

// siteFrom resolves configuration and reports the identity plus the
// layer the token came from.
func siteFrom(ctx context.Context, opts config.Options, store core.CredentialStore) (authcmd.SiteInfo, error) {
	resolved, err := config.Resolve(ctx, opts, store)
	if err != nil {
		return authcmd.SiteInfo{}, err
	}
	if err := credentialReadFailure(resolved); err != nil {
		return authcmd.SiteInfo{}, err
	}
	return siteFromResolved(resolved), nil
}

func siteFromResolved(resolved *config.Resolved) authcmd.SiteInfo {
	return authcmd.SiteInfo{
		BaseURL:     resolved.Config.BaseURL,
		CloudID:     resolved.Config.CloudID,
		Email:       resolved.Config.Email,
		Token:       resolved.Config.APIToken,
		TokenSource: string(resolved.Source("api_token")),
		Location:    config.CredentialSelection(*resolved),
	}
}

func credentialReadFailure(resolved *config.Resolved) error {
	if resolved.CredentialError == nil || strings.TrimSpace(resolved.Config.APIToken) != "" {
		return nil
	}
	return &cli.CodedError{
		Code:    cli.CodeAuthStoreUnavailable,
		Message: "the selected credential store could not be read; stored credentials were not verified",
		Cause:   resolved.CredentialError,
	}
}

// validateLoginScope prevents a successful login from saving a token that the
// same authored configuration would immediately discard on the next command.
func validateLoginScope(ctx context.Context, cloudID string) error {
	return validateLoginScopeFrom(ctx, config.Options{PermitMissingTarget: true}, cloudID)
}

func validateLoginScopeFrom(ctx context.Context, opts config.Options, cloudID string) error {
	resolved, err := config.Resolve(ctx, opts, nil)
	if err != nil {
		return err
	}
	if resolved.Source("cloud_id") == config.LayerDefault || resolved.Source("cloud_id") == config.LayerUnknown || resolved.Config.CloudID == cloudID {
		return nil
	}
	input := cli.NewInputError(cli.CodeConfigInvalid, fmt.Sprintf(
		"verified cloud_id is %q but configured cloud_id differs; set cloud_id to the verified scope (or clear it for a classic token), then repeat login; nothing was stored", cloudID))
	input.Field = "cloud_id"
	return input
}

// metadataCacheScope keeps live keyring resolution in the established cache
// constructor while previews use only explicit non-secret scope preferences.
func metadataCacheScope(ctx context.Context, deps *Deps, local bool) (cachecmd.MetadataScope, error) {
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
		store, err = deps.Cache(ctx)
	} else {
		err = core.ErrCacheManagementUnsupported
	}
	return cachecmd.MetadataScope{Site: prefs.BaseURL, Email: prefs.Account, Store: store}, err
}

// credentialStoreFor selects a provider using only local public preferences.
// SDK construction and authentication are deferred until a credential operation.
func credentialStoreFor(settings config.Config) core.CredentialStore {
	switch settings.SecretBackend {
	case core.CredentialBackendOnePassword:
		return adapteronepassword.New(adapteronepassword.Config{
			Account: settings.OnePasswordAccount, Vault: settings.OnePasswordVault,
			Item: settings.OnePasswordItem, IntegrationVersion: clive.Current(),
		})
	case core.CredentialBackendEnvironment:
		return core.EnvironmentCredentialStore{}
	default:
		if !settings.Keyring {
			return core.NoCredentialStore{}
		}
		return credentials.Select(keyring.New(), settings.CredentialID)
	}
}

// Migration uses recorded public scope, independently of current settings and
// selected-store overlays. Construction is lazy and opens no native credentials.
func migrationStore(ctx context.Context, location core.CredentialLocation) (core.CredentialRecoveryStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch location.Backend {
	case core.CredentialBackendKeyring:
		return keyring.NewForService(location.Service), nil
	case core.CredentialBackendOnePassword:
		return adapteronepassword.New(adapteronepassword.Config{
			Account: location.SDKAccount, Vault: location.Vault, Item: location.Item,
			IntegrationVersion: clive.Current(),
		}), nil
	default:
		return nil, core.ErrCredentialRecoveryUnsupported
	}
}

func siteFromDeps(ctx context.Context, deps *Deps) (authcmd.SiteInfo, error) {
	if deps.Site != nil {
		return deps.Site(ctx)
	}
	if deps.Credentials == nil {
		return siteInfo(ctx)
	}
	store, err := deps.Credentials()
	if err != nil {
		return authcmd.SiteInfo{}, err
	}
	return siteFrom(ctx, config.Options{}, store)
}
