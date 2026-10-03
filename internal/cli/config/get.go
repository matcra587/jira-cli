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

func newGet() *cobra.Command {
	return command.MustNew(command.Spec[envelope.ConfigGetOutput]{
		Op:    "config.get",
		Use:   "get KEY",
		Short: "Show one stored setting",
		Long: "Read one supported key from the selected configuration file. " +
			"Environment variables, flags, defaults and credentials do not supply " +
			"its value. Use `config list` for effective values and their sources.\n\n" +
			"An absent key is reported with `present=false`; an explicitly stored " +
			"empty string, false or zero remains present. Secrets are redacted.",
		Example: "$ jira config get read_only\n\n" +
			"# read a stored value from a specific file\n" +
			"$ jira config get timeout --config=settings.toml --output=json\n\n" +
			"# extract a stored value for a script\n" +
			"$ jira config get timeout --output=json --jq '.data.value'",
		Args:  command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "KEY", Predictor: "configkey"}}},
		Human: renderGetHuman,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ConfigGetOutput, error) {
			var zero envelope.ConfigGetOutput
			if err := ctx.Err(); err != nil {
				return zero, err
			}
			key := inv.Args[0]
			keys := cfg.Keys()
			if !slices.Contains(keys, key) {
				err := cli.NewInputError(cli.CodeArgValueInvalid, fmt.Sprintf("unknown config key %q", key))
				err.Field = "KEY"
				err.Suggestions = cli.SuggestValues(key, keys)
				return zero, err
			}
			file, err := cfg.ReadFile(ctx, "")
			if err != nil {
				return zero, localIO("read config", err)
			}
			setting, err := file.Setting(key)
			if err != nil {
				if errors.Is(err, cfg.ErrFileInvalid) {
					return zero, configError(cli.CodeConfigInvalid, err.Error())
				}
				return zero, err
			}
			profile, err := file.Profile()
			if err != nil {
				return zero, settingFileError(err)
			}
			return envelope.ConfigGetOutput{
				Profile: profile,
				Path:    file.Path(), FileExists: file.Exists(), Key: setting.Key,
				Value: setting.Value, Present: setting.Present, Secret: setting.Secret,
			}, nil
		},
	})
}

func renderGetHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ConfigGetOutput)
	if !ok {
		return fmt.Errorf("config.get: unexpected payload %T", env.Data)
	}
	if out.Present {
		v.Line(cli.Text(out.Key), shownValue(out.Value))
	} else {
		v.Line(cli.Text(out.Key), "(not stored)")
	}
	v.Line("config file: " + cli.Text(out.Path))
	return nil
}
