package auth

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

const identityDescription = "Read the account Jira authenticates with the resolved credentials.\n\n" +
	"Machine output retains the complete native account in `data.user`, " +
	"including privacy-hidden email, absent fields and additional Jira fields. " +
	"Human output shows the returned account identity, type, time zone and activity. " +
	"This is a read; it does not save account settings or display the API token."

// newWhoami returns the current account with its complete native Jira fields.
func newWhoami(deps Deps) *cobra.Command {
	return command.MustNew(command.Spec[envelope.AuthWhoamiOutput]{
		Op: "auth.whoami", Passthrough: true, Use: "whoami",
		Short: "Show the current Jira account", Long: identityDescription,
		Example: "$ jira auth whoami\n$ jira auth whoami --output=json\n$ jira auth whoami --output=json --jq '.data.user.accountId'",
		Human:   renderWhoamiHuman,
		Run: func(ctx context.Context, _ command.Invocation) (envelope.AuthWhoamiOutput, error) {
			return readIdentity(ctx, deps)
		},
	})
}

// NewMe exposes the same current account read at the root command.
func NewMe(deps Deps) *cobra.Command {
	return command.MustNew(command.Spec[envelope.AuthWhoamiOutput]{
		Op: "me", Passthrough: true, Use: "me",
		Short: "Show the current Jira account", Long: identityDescription,
		Example: "$ jira me\n$ jira me --output=json\n$ jira me --output=json --jq '.data.user.accountId'",
		Human:   renderWhoamiHuman,
		Run: func(ctx context.Context, _ command.Invocation) (envelope.AuthWhoamiOutput, error) {
			return readIdentity(ctx, deps)
		},
	})
}

func readIdentity(ctx context.Context, deps Deps) (envelope.AuthWhoamiOutput, error) {
	site, user, err := currentIdentity(ctx, deps)
	if err != nil {
		return envelope.AuthWhoamiOutput{}, err
	}
	return envelope.AuthWhoamiOutput{BaseURL: site.BaseURL, User: *user.User}, nil
}

func renderWhoamiHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.AuthWhoamiOutput)
	if !ok {
		return fmt.Errorf("auth.whoami: unexpected payload %T", env.Data)
	}
	u := out.User
	fields := []output.Field{
		{Label: "Site", Value: cli.Text(out.BaseURL)},
		{Label: "Account ID", Value: cli.Text(u.AccountID.Value)},
		{Label: "Display name", Value: cli.OrDash(cli.Text(u.DisplayName.Value))},
		{Label: "Email address", Value: cli.OrDash(cli.Text(u.EmailAddress.Value))},
		{Label: "Account type", Value: cli.OrDash(cli.Text(u.AccountType.Value))},
		{Label: "Time zone", Value: cli.OrDash(cli.Text(u.TimeZone.Value))},
	}
	if u.Active.Set && !u.Active.Null {
		fields = append(fields, output.Field{Label: "Active", Value: strconv.FormatBool(u.Active.Value)})
	}
	v.Fields(fields...)
	return nil
}
