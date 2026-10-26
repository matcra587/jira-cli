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
	"Duplicate native members or case variants fail with current_user_response_invalid before identity saving. " +
	"Machine output retains the complete native account in `data.user`, " +
	"including privacy-hidden email, absent fields and additional Jira fields. " +
	"Human output omits unreturned identity fields and shows '-' for fetched null or empty values. " +
	"Default identity inspection is a read; it does not save account settings or display the API token."

// newWhoami returns the current account with its complete native Jira fields.
func newWhoami(deps Deps) *cobra.Command {
	return command.MustNew(command.Spec[envelope.AuthWhoamiOutput]{
		Op: "auth.whoami", Passthrough: true, Use: "whoami",
		Short: "Show the current Jira account", Long: identityDescription + "\n\n" + identitySaveDescription,
		Example: "$ jira auth whoami\n$ jira auth whoami --output=json\n$ jira auth whoami --save --dry-run\n$ jira auth whoami --save --output=json",
		Flags: func(c *cobra.Command) {
			var save bool
			command.AddBoolVar(c, &save, "save", false, "Save verified account identity with its public site/account/cloud binding", command.FlagOpts{Terse: "Save verified identity"})
			command.AddDryRunFlag(c)
		},
		LocalWrite: command.WhenBoolLocalWrite("save", command.LocalWriteSpec[envelope.AuthWhoamiSaveOutput]{
			Op: "auth.whoami.save", Passthrough: true, Human: renderWhoamiSaveHuman,
			Run: func(ctx context.Context, inv command.Invocation) (envelope.AuthWhoamiSaveOutput, error) {
				return saveIdentity(ctx, deps, inv.DryRun)
			},
			RetainOnError: func(out envelope.AuthWhoamiSaveOutput) bool {
				return out.User.AccountID.Set && !out.User.AccountID.Null
			},
		}),
		Human: renderWhoamiHuman,
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
	return renderIdentityHuman(v, out)
}

func renderIdentityHuman(v *output.View, out envelope.AuthWhoamiOutput) error {
	u := out.User
	fields := []output.Field{
		{Label: "Site", Value: cli.Text(out.BaseURL)},
		{Label: "Account ID", Value: cli.Text(u.AccountID.Value)},
	}
	if u.DisplayName.Set {
		fields = append(fields, output.Field{Label: "Display name", Value: cli.OrDash(cli.Text(u.DisplayName.Value))})
	}
	if u.EmailAddress.Set {
		fields = append(fields, output.Field{Label: "Email address", Value: cli.OrDash(cli.Text(u.EmailAddress.Value))})
	}
	if u.AccountType.Set {
		fields = append(fields, output.Field{Label: "Account type", Value: cli.OrDash(cli.Text(u.AccountType.Value))})
	}
	if u.TimeZone.Set {
		fields = append(fields, output.Field{Label: "Time zone", Value: cli.OrDash(cli.Text(u.TimeZone.Value))})
	}
	if u.Active.Set {
		active := "-"
		if !u.Active.Null {
			active = strconv.FormatBool(u.Active.Value)
		}
		fields = append(fields, output.Field{Label: "Active", Value: active})
	}
	v.Fields(fields...)
	return nil
}
