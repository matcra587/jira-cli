package root

import (
	"context"
	"errors"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	authcmd "github.com/matcra587/jira-cli/internal/cli/auth"
	"github.com/matcra587/jira-cli/internal/cli/command"
	jqlcmd "github.com/matcra587/jira-cli/internal/cli/jql"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
)

// invocationSettings freezes authored settings and environment at the first
// consumer. Publication factories reopen that same path and selected profile
// to detect changes; ordinary consumers share the captured values.
type invocationSettings struct {
	capture    func(context.Context) (config.Options, error)
	contextFor func() context.Context
}

func (s *invocationSettings) currentContext() (context.Context, error) {
	if s.contextFor == nil {
		return nil, errors.New("invocation configuration is not bound to an execution context")
	}
	ctx := s.contextFor()
	if ctx == nil {
		return nil, errors.New("invocation configuration has no execution context")
	}
	return ctx, ctx.Err()
}

func newInvocationSettings() *invocationSettings {
	return &invocationSettings{capture: command.OnceContextValues(func(ctx context.Context) (config.Options, error) {
		return config.CaptureOptions(ctx, config.Options{PermitMissingTarget: true})
	})}
}

func (s *invocationSettings) options(ctx context.Context, saving bool) (config.Options, error) {
	opts, err := s.capture(ctx)
	opts.PermitMissingTarget = saving
	return opts, err
}

func (s *invocationSettings) public(ctx context.Context, saving bool) (*config.Resolved, error) {
	opts, err := s.options(ctx, saving)
	if err != nil {
		return nil, err
	}
	return config.Resolve(ctx, opts, nil)
}

func (s *invocationSettings) clientFor(saving bool) func(string, string, string, string) (core.JiraClient, error) {
	return func(site, email, token, cloudID string) (core.JiraClient, error) {
		ctx, err := s.currentContext()
		if err != nil {
			return nil, err
		}
		resolved, err := s.public(ctx, saving)
		if err != nil {
			return nil, err
		}
		return adapterjira.New(adapterjira.Config{
			BaseURL: site, CloudID: cloudID, Email: email, APIToken: token,
			ReadOnly: resolved.Config.ReadOnly, Timeout: resolved.Config.Timeout,
			Retry: core.DefaultRetryPolicy(),
		})
	}
}

func (s *invocationSettings) buildPreferences() (jqlcmd.BuildPreferences, error) {
	ctx, err := s.currentContext()
	if err != nil {
		return jqlcmd.BuildPreferences{}, err
	}
	resolved, err := s.public(ctx, false)
	if err != nil {
		return jqlcmd.BuildPreferences{}, err
	}
	c := resolved.Config
	return jqlcmd.BuildPreferences{BaseURL: c.BaseURL, Account: c.Email, CachedAccountID: c.CachedAccountID(), DefaultProject: c.DefaultProject, DefaultIssueType: c.DefaultIssueType, DefaultBoard: c.DefaultBoard}, nil
}

func (s *invocationSettings) workdaySeconds() (int, error) {
	ctx, err := s.currentContext()
	if err != nil {
		return 0, err
	}
	resolved, err := s.public(ctx, false)
	if err != nil {
		return 0, err
	}
	return resolved.Config.WorkdaySeconds, nil
}

func (s *invocationSettings) siteWithoutStore(ctx context.Context) (authcmd.SiteInfo, error) {
	resolved, err := s.public(ctx, false)
	if err != nil {
		return authcmd.SiteInfo{}, err
	}
	return siteFromResolved(resolved), nil
}

func (s *invocationSettings) identitySaveSite(ctx context.Context) (authcmd.SiteInfo, error) {
	opts, err := s.options(ctx, true)
	if err != nil {
		return authcmd.SiteInfo{}, err
	}
	return resolveIdentitySaveSite(ctx, opts, credentialStoreFor)
}

func (s *invocationSettings) loginSite(ctx context.Context, defaults authcmd.LoginDefaults) (authcmd.SiteInfo, error) {
	opts, err := s.options(ctx, true)
	if err != nil {
		return authcmd.SiteInfo{}, err
	}
	return resolveLoginSite(ctx, opts, defaults, credentialStoreFor)
}

func (s *invocationSettings) loginCredentials(ctx context.Context, selected core.CredentialLocation) (core.CredentialStore, error) {
	opts, err := s.options(ctx, true)
	if err != nil {
		return nil, err
	}
	opts.Snapshot = nil
	return resolveLoginCredentials(ctx, opts, selected, credentialStoreFor)
}

func (s *invocationSettings) prepareLoginLocation(ctx context.Context, selected core.CredentialLocation) (core.CredentialLocation, core.AccountIdentityPublication, error) {
	opts, err := s.options(ctx, true)
	if err != nil {
		return selected, core.AccountIdentityPublication{}, err
	}
	return config.PrepareLoginCredentialLocation(ctx, opts, selected)
}

func (s *invocationSettings) loginTarget(ctx context.Context, binding core.LoginIdentityBinding) (core.AccountIdentityTarget, error) {
	opts, err := s.options(ctx, true)
	if err != nil {
		return nil, err
	}
	opts.Snapshot = nil
	return config.OpenLoginIdentityTarget(ctx, opts, binding)
}

func (s *invocationSettings) validateLoginScope(ctx context.Context, cloudID string) error {
	opts, err := s.options(ctx, true)
	if err != nil {
		return err
	}
	opts.Snapshot = nil
	return validateLoginScopeFrom(ctx, opts, cloudID)
}

func (s *invocationSettings) identityTarget(ctx context.Context, scope core.AccountIdentityScope) (core.AccountIdentityTarget, error) {
	opts, err := s.options(ctx, true)
	if err != nil {
		return nil, err
	}
	return config.OpenAccountIdentityTargetFor(ctx, opts, scope)
}
