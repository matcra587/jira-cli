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
	ClientFor func(site, email, token string) (core.JiraClient, error)
	// Site returns the configured base URL and account email, and
	// whether a token is already resolvable.
	Site func() (SiteInfo, error)
	// SiteWithoutStore resolves the same thing with the credential store
	// withheld, so logout can report what would still authenticate once
	// the stored record is gone.
	SiteWithoutStore func() (SiteInfo, error)
	// Confirm asks a yes/no question and reports the answer.
	//
	// Injected for the same reason the rest of this struct is, and for
	// one more: it is the only input here a person supplies at the
	// moment of asking. Called directly it reaches the terminal, which
	// makes the branch that deletes a credential and the branch that
	// keeps it equally impossible to test. A test supplies the answer;
	// the composition root supplies the dialog.
	Confirm func(prompt.Confirm) (bool, error)
}

// SiteInfo is the configured identity a command reports against.
type SiteInfo struct {
	// BaseURL is the Jira site root.
	BaseURL string
	// Email is the account credentials belong to.
	Email string
	// Token is the resolved API token, empty when none is stored.
	Token string
	// TokenSource names the layer the token came from, so status can say
	// "the environment is overriding your keyring" instead of leaving
	// someone to discover it.
	TokenSource string
}

// verify calls Jira as the given credentials and returns the account it
// authenticated as.
func verify(ctx context.Context, client core.JiraClient) (core.UserRead, error) {
	// Behind a spinner: verification is a network round-trip, and both
	// login and status wait on it. One wrap here covers both.
	return command.Spin(ctx, "Verifying credentials", func(ctx context.Context) (core.UserRead, error) {
		return client.CurrentUser(ctx)
	})
}

// currentIdentity resolves credentials and reads the account Jira authenticates.
// Status and identity inspection share the same validation and transport owner.
func currentIdentity(ctx context.Context, deps Deps) (SiteInfo, core.UserRead, error) {
	site, err := deps.Site()
	if err != nil {
		return SiteInfo{}, core.UserRead{}, err
	}
	if missing := missingSite(site); missing != "" {
		return SiteInfo{}, core.UserRead{}, cli.NewCodedError(cli.CodeAuthMissing,
			fmt.Sprintf("not authenticated: %s is not set — run `jira config edit`, then `jira auth login --with-token`", missing))
	}
	if strings.TrimSpace(site.Token) == "" {
		return SiteInfo{}, core.UserRead{}, cli.NewCodedError(cli.CodeAuthMissing,
			"not authenticated: no API token — pipe one in with `jira auth login --with-token`")
	}
	client, err := deps.ClientFor(site.BaseURL, site.Email, site.Token)
	if err != nil {
		return SiteInfo{}, core.UserRead{}, err
	}
	user, err := verify(ctx, client)
	if err != nil {
		return SiteInfo{}, core.UserRead{}, err
	}
	return site, user, nil
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
