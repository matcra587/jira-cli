package config

import (
	"context"
	"fmt"

	xstrings "github.com/gechr/x/strings"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func newInit() *cobra.Command {
	return newInitWithCreator(func(ctx context.Context, update *cfg.SettingsUpdate) (bool, error) {
		return update.Create(ctx)
	})
}

func newInitWithCreator(create func(context.Context, *cfg.SettingsUpdate) (bool, error)) *cobra.Command {
	var baseURL, email string
	return command.MustNew(command.Spec[envelope.ConfigInitOutput]{
		Op: "config.init", Use: "init", Short: "Create configuration from explicit account metadata",
		MutatesLocalState: true,
		Long: "Create a new configuration file from explicit --base-url and --email values. " +
			"Both flags are required in every mode. This command does not prompt, contact Jira, " +
			"access credentials or persist environment values and defaults. Credentials remain " +
			"owned by jira auth login.\n\n" +
			"--config selects the destination; otherwise the platform configuration path is used. " +
			"Existing files, directories and dangling links are refused, including during previews. " +
			"Use config set or config edit to update existing preferences.\n\n" +
			"--dry-run validates the same explicit values and reports the candidate without " +
			"creating files or directories. New files use mode 0600 and new configuration " +
			"directories use mode 0700; existing parent permissions are retained. " +
			"Publication never replaces a competing creator. The filesystem must support " +
			"hard links; unsupported publication fails without writing a partial destination. " +
			"A directory sync error after publication retains created=true; inspect the file " +
			"before retrying. This does not lock parent directories against external replacement.",
		Example: "$ jira config init --base-url=https://example.atlassian.net --email=person@example.com --no-input\n\n" +
			"$ jira config init --base-url=https://example.atlassian.net --email=person@example.com --config=settings.toml --dry-run --output=json",
		Flags: func(c *cobra.Command) {
			command.AddStringVar(c, &baseURL, "base-url", "", "Store the explicit Jira site URL", command.FlagOpts{Terse: "Jira site URL", Placeholder: "URL"})
			command.AddStringVar(c, &email, "email", "", "Store the explicit account email", command.FlagOpts{Terse: "Account email", Placeholder: "EMAIL"})
			command.AddDryRunFlag(c)
			for _, name := range []string{"base-url", "email"} {
				if err := c.MarkFlagRequired(name); err != nil {
					panic(err) // registered above; failure is a command construction error
				}
			}
		},
		Validate: func() error { return validateInitialization(baseURL, email) },
		Human:    renderInitHuman,
		RetainOnError: func(out envelope.ConfigInitOutput) bool {
			return out.Created
		},
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ConfigInitOutput, error) {
			var zero envelope.ConfigInitOutput
			file, err := cfg.OpenNewFile(ctx, "")
			if err != nil {
				return zero, settingFileError(err)
			}
			update, err := file.PrepareSettings(map[string]string{"base_url": baseURL, "email": email})
			if err != nil {
				return zero, settingFileError(err)
			}
			if err := ctx.Err(); err != nil {
				return zero, err
			}
			out := envelope.ConfigInitOutput{
				MutationState: envelope.MutationState{DryRun: inv.DryRun},
				Path:          file.Path(), BaseURL: baseURL, Email: email, WouldCreate: true,
			}
			if !inv.DryRun {
				out.Created, err = create(ctx, update)
				if err != nil {
					return out, settingFileError(err)
				}
			}
			return out, nil
		},
	})
}

func validateInitialization(baseURL, email string) error {
	for _, field := range []struct{ flag, key, value string }{
		{flag: "base-url", key: "base_url", value: baseURL},
		{flag: "email", key: "email", value: email},
	} {
		var err error
		if xstrings.IsBlank(field.value) {
			err = fmt.Errorf("--%s is required; provide both --base-url and --email explicitly", field.flag)
		} else {
			err = cfg.ValidateSettingValue(field.key, field.value)
		}
		if err != nil {
			input := cli.NewInputError(cli.CodeArgValueInvalid, err.Error())
			input.Field = field.flag
			return input
		}
	}
	return nil
}

func renderInitHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ConfigInitOutput)
	if !ok {
		return fmt.Errorf("config.init: unexpected payload %T", env.Data)
	}
	if out.DryRun {
		v.Line("would create config " + cli.Text(out.Path))
	} else if out.Created {
		v.Line("created config " + cli.Text(out.Path))
	}
	v.Fields(output.Field{Label: "Site", Value: cli.Text(out.BaseURL)}, output.Field{Label: "Email", Value: cli.Text(out.Email)})
	return nil
}
