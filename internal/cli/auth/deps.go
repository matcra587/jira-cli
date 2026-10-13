package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/cli/prompt"
	"github.com/matcra587/jira-cli/internal/core"
)

// LoginDefaults identifies public fields not supplied by login flags. A provider
// may supply a field only when that same public default is also missing.
type LoginDefaults struct {
	Site  bool
	Email bool
}

// Deps is what the auth commands need from the composition root, passed
// as function values so this package never imports root.
//
// Constructors rather than instances: nothing opens a credential store
// or a network client until a RunE that needs one runs, which is what
// keeps help and completion free of both.
type Deps struct {
	// Credentials opens the credential store.
	Credentials func() (core.CredentialStore, error)
	// ClientFor builds a client for a specific account and token, rather
	// than for the resolved configuration.
	//
	// login needs exactly this: the whole point is to check credentials
	// that are NOT stored yet — on a first run the site itself is new —
	// and a client built from resolved config would test the OLD values,
	// reporting success for a login nobody verified.
	ClientFor func(site, email, token, cloudID string) (core.JiraClient, error)
	// Site returns the configured base URL and account email, and
	// whether a token is already resolvable.
	Site func(ctx context.Context) (SiteInfo, error)
	// SiteForLogin validates a publication target and public defaults first.
	// Stored identity is read only when missing flags/public defaults require it.
	SiteForLogin func(ctx context.Context, defaults LoginDefaults) (SiteInfo, error)
	// CredentialsForLogin binds construction to the complete public selection.
	// Nil uses Credentials for injected callers; previews never call either.
	CredentialsForLogin func(context.Context, core.CredentialLocation) (core.CredentialStore, error)
	// PrepareLoginLocation durably reserves a missing public keyring locator
	// after verification and before credentials are saved. Previews never call it.
	PrepareLoginLocation func(context.Context, core.CredentialLocation) (core.CredentialLocation, core.AccountIdentityPublication, error)
	// LoginTarget prepares complete public metadata before credential mutation.
	// Nil uses IdentityTarget for callers supplying their own checked target.
	LoginTarget func(context.Context, core.LoginIdentityBinding) (core.AccountIdentityTarget, error)
	// SiteForSave permits a missing explicitly selected publication target.
	// Nil uses Site, preserving injected ordinary resolution in embedders/tests.
	SiteForSave func(ctx context.Context) (SiteInfo, error)
	// ClientForSave uses the same transport owner with publication-target options.
	// Nil uses ClientFor. Neither factory publishes or writes Jira.
	ClientForSave func(site, email, token, cloudID string) (core.JiraClient, error)
	// SiteWithoutStore resolves the same thing with the credential store
	// withheld, so logout can report what would still authenticate once
	// the stored record is gone.
	SiteWithoutStore func(ctx context.Context) (SiteInfo, error)
	// ValidateLoginScope checks authored routing before publishing a verified
	// token. It reads public settings only and must not open a credential store.
	ValidateLoginScope func(ctx context.Context, cloudID string) error
	// IdentityTarget snapshots and checks a public settings target before lookup.
	// Ordinary identity reads never call it; preparation publishes nothing.
	IdentityTarget func(context.Context, core.AccountIdentityScope) (core.AccountIdentityTarget, error)
	// Confirm asks a yes/no question and reports the answer.
	//
	// Injected for the same reason the rest of this struct is, and for
	// one more: it is the only input here a person supplies at the
	// moment of asking. Called directly it reaches the terminal, which
	// makes the branch that deletes a credential and the branch that
	// keeps it equally impossible to test. A test supplies the answer;
	// the composition root supplies the dialog.
	Confirm func(context.Context, prompt.Confirm) (bool, error)
}

// SiteInfo is the configured identity a command reports against.
type SiteInfo struct {
	// BaseURL is the Jira site root.
	BaseURL string
	// CloudID is the resolved, bound API routing scope; empty is classic.
	CloudID string
	// Email is the account credentials belong to.
	Email string
	// Token is the resolved API token, empty when none is stored.
	Token string
	// TokenSource names the layer the token came from, so status can say
	// "the environment is overriding your keyring" instead of leaving
	// someone to discover it.
	TokenSource string
	// Location captures the selected provider and its public coordinates.
	Location core.CredentialLocation
}

// verify calls Jira as the given credentials and returns the account it
// authenticated as.
func verify(ctx context.Context, client core.JiraClient) (core.UserRead, error) {
	if err := ctx.Err(); err != nil {
		return core.UserRead{}, err
	}
	// Behind a spinner: verification is a network round-trip, and both
	// login and status wait on it. One wrap here covers both.
	return command.Spin(ctx, "Verifying credentials", func(ctx context.Context) (core.UserRead, error) {
		if err := ctx.Err(); err != nil {
			return core.UserRead{}, err
		}
		user, err := client.CurrentUser(ctx)
		if err != nil {
			return core.UserRead{}, err
		}
		if err := ctx.Err(); err != nil {
			return core.UserRead{}, err
		}
		if err := core.RequireCurrentUserIdentity(user.User); err != nil {
			return core.UserRead{}, err
		}
		return user, nil
	})
}

// currentIdentity resolves credentials and reads the account Jira authenticates.
// Status and identity inspection share the same validation and transport owner.
func currentIdentity(ctx context.Context, deps Deps) (SiteInfo, core.UserRead, error) {
	site, err := identitySite(ctx, deps)
	if err != nil {
		return SiteInfo{}, core.UserRead{}, err
	}
	user, err := identityAt(ctx, deps, site)
	if err != nil {
		return SiteInfo{}, core.UserRead{}, err
	}
	return site, user, nil
}

func identitySite(ctx context.Context, deps Deps) (SiteInfo, error) {
	if err := ctx.Err(); err != nil {
		return SiteInfo{}, err
	}
	site, err := deps.Site(ctx)
	if contextErr := ctx.Err(); contextErr != nil {
		return SiteInfo{}, contextErr
	}
	if err != nil {
		return SiteInfo{}, err
	}
	if missing := missingSite(site); missing != "" {
		return SiteInfo{}, cli.NewCodedError(cli.CodeAuthMissing,
			fmt.Sprintf("not authenticated: %s is not set — run `jira config edit`, then `jira auth login --with-token`", missing))
	}
	if strings.TrimSpace(site.Token) == "" {
		return SiteInfo{}, cli.NewCodedError(cli.CodeAuthMissing,
			"not authenticated: no API token — pipe one in with `jira auth login --with-token`")
	}
	return site, nil
}

func identityAt(ctx context.Context, deps Deps, site SiteInfo) (core.UserRead, error) {
	if err := ctx.Err(); err != nil {
		return core.UserRead{}, err
	}
	client, err := deps.ClientFor(site.BaseURL, site.Email, site.Token, site.CloudID)
	if contextErr := ctx.Err(); contextErr != nil {
		return core.UserRead{}, contextErr
	}
	if err != nil {
		return core.UserRead{}, err
	}
	return verify(ctx, client)
}

// missingSite names the first configured setting identity reads require.
// The setting is a config key rather than a login flag; see missingIdentity
// for validation of credentials supplied to login itself.
func missingSite(site SiteInfo) string {
	switch {
	case strings.TrimSpace(site.BaseURL) == "":
		return "base_url"
	case strings.TrimSpace(site.Email) == "":
		return "email"
	}
	return ""
}
