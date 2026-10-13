package config

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func newSet() *cobra.Command {
	return command.MustNew(command.Spec[envelope.ConfigSetOutput]{
		Op: "config.set", MutatesLocalState: true, Use: "set KEY VALUE", Short: "Store one validated setting",
		Long: "Validate and store one setting in the selected configuration file. " +
			"A missing file is created only after validation. No Jira client, credentials, " +
			"environment values or built-in defaults are needed or written.\n\n" +
			"Shell completion offers absolute paths to local files for `theme_path`, so the selected file remains usable when settings live elsewhere. Other keys retain their supported values without a filesystem fallback.\n\n" +
			"--dry-run validates the same candidate and reports redacted previous and new " +
			"values without creating files or directories. would_change describes the " +
			"candidate; changed and created describe actual publication. An absent " +
			"declaration differs from an explicitly stored false, zero or empty string.\n\n" +
			"Unchanged typed values retain the original bytes and metadata. Changed files " +
			"use normalized TOML while preserving other declared settings and aliases. " +
			"Invalid file syntax/types are refused; setting base_url can repair a malformed " +
			"site URL, and api_token_env can repair a token-variable reference. " +
			"api_token_env stores only a public JIRA_API_TOKEN or JIRA_TOKEN_ name; " +
			"token values remain environment-only. Snapshot conflicts refuse the save. This is not an atomic lock " +
			"against external writers.\n\n" +
			"default_project and default_issue_type supply omitted create context; default_project also supplies unscoped structured issue list/mine context. " +
			"default_board names a cached saved-filter scope for issue list/mine and jql build. Explicit empty values clear these preferences. " +
			"Their completion uses only existing offline metadata.\n\n" +
			"refresh_interval is positive seconds (default 30); team_account_ids is a JSON array of literal strings, such as [\"team-1\",\"team-2\"]. " +
			"Use [] to clear that array. These retained client preferences are metadata; they do not start polling or add issue filters.\n\n" +
			"account_id is cached public identity for local previews only. Its independent account_id_site, account_id_email and account_id_cloud_id must match effective scope; changing scope never rebinds an existing ID. Live runs verify Jira identity instead.\n\n" +
			"Credentials cannot be stored here; use `jira auth login`. Manage aliases " +
			"through `jira alias set` or `jira alias import` so their command chains are validated.",
		Example: "$ jira config set read_only true --no-input\n\n" +
			"# preview a duration change without writing\n" +
			"$ jira config set timeout 30s --config=settings.toml --dry-run --output=json\n\n" +
			"# explicitly store an empty value\n" +
			"$ jira config set email '' --config=settings.toml --no-input",
		Args: command.ArgSpec{Min: 2, Max: 2, Positions: []command.Arg{
			{Name: "KEY", Predictor: "configsetkey"}, {Name: "VALUE", Predictor: "word_configvalue"},
		}},
		Flags: command.AddDryRunFlag,
		Human: renderSetHuman,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ConfigSetOutput, error) {
			var zero envelope.ConfigSetOutput
			if err := ctx.Err(); err != nil {
				return zero, err
			}
			key, value := inv.Args[0], inv.Args[1]
			keys := cfg.WritableSettingKeys()
			if !slices.Contains(keys, key) {
				err := cli.NewInputError(cli.CodeArgValueInvalid, cfg.ValidateSettingValue(key, value).Error())
				err.Field = "KEY"
				err.Suggestions = cli.SuggestValues(key, keys)
				return zero, err
			}
			if err := cfg.ValidateSettingValue(key, value); err != nil {
				input := cli.NewInputError(cli.CodeArgValueInvalid, err.Error())
				input.Field = "VALUE"
				input.Suggestions = cli.SuggestValues(value, cfg.SettingValueChoices(key))
				return zero, input
			}
			file, err := cfg.OpenFile(ctx, "")
			if err != nil {
				return zero, localIO("open config", err)
			}
			update, err := file.PrepareSetting(key, value)
			if err != nil {
				return zero, settingFileError(err)
			}
			if err := ctx.Err(); err != nil {
				return zero, err
			}
			profile, err := file.Profile()
			if err != nil {
				return zero, settingFileError(err)
			}
			out := envelope.ConfigSetOutput{
				Profile:       profile,
				MutationState: envelope.MutationState{DryRun: inv.DryRun},
				Path:          file.Path(), Key: update.Next.Key, Value: update.Next.Value,
				PreviousValue: update.Previous.Value, PreviousPresent: update.Previous.Present,
				PreviousSecret: update.Previous.Secret, WouldChange: update.WouldChange(),
			}
			if !inv.DryRun {
				out.Changed, err = update.Save(ctx)
				if err != nil {
					return zero, settingFileError(err)
				}
				out.Created = out.Changed && !file.Exists()
			}
			return out, nil
		},
	})
}

func settingFileError(err error) error {
	switch {
	case errors.Is(err, cfg.ErrFileChanged):
		return configError(cli.CodeConfigChanged, "config changed before save; nothing was replaced; inspect the selected file before retrying")
	case errors.Is(err, cfg.ErrFileInvalid):
		return configError(cli.CodeConfigInvalid, err.Error())
	default:
		return localIO("save config; inspect the selected file before retrying", err)
	}
}

func renderSetHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ConfigSetOutput)
	if !ok {
		return fmt.Errorf("config.set: unexpected payload %T", env.Data)
	}
	switch {
	case out.DryRun && out.WouldChange:
		v.Line("would store", cli.Text(out.Key), "as", shownValue(out.Value))
	case out.DryRun:
		v.Line("would keep", cli.Text(out.Key), "as", shownValue(out.Value))
	case !out.Changed:
		v.Line("no changes to", cli.Text(out.Key))
	case out.Created:
		v.Line("created config with", cli.Text(out.Key), "set to", shownValue(out.Value))
	default:
		v.Line("stored", cli.Text(out.Key), "as", shownValue(out.Value))
	}
	if out.PreviousPresent {
		v.Line("previous value:", shownValue(out.PreviousValue))
	} else {
		v.Line("previous value: (not stored)")
	}
	v.Line("config file: " + cli.Text(out.Path))
	return nil
}
