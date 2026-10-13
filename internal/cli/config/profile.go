package config

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func newProfile() *cobra.Command {
	cmd := &cobra.Command{Use: "profile", Short: "Manage named configurations", Args: cobra.NoArgs}
	command.SetMeta(cmd, command.Meta{Op: "config.profile", NoSuccessEnvelope: "a bare group prints help, not an operation result"})
	cmd.AddCommand(newProfileList(), newProfileAdd(), newProfileRename(), newProfileDefault(), newProfileRemove())
	return cmd
}

func newProfileList() *cobra.Command {
	return command.MustNew(command.Spec[envelope.ConfigProfileListOutput]{
		Op: "config.profile.list", Use: "list", Short: "List configured profiles without opening credentials",
		Long: "Show named profiles, their public site/account metadata, and their default and selected state. " +
			"Selection uses --profile, then JIRA_PROFILE, then default_profile. " +
			"These are authored settings; environment overrides may change the effective identity. " +
			"Use config list to inspect effective values and sources. No credential provider or Jira request is used.",
		Example: "$ jira config profile list --output=json",
		Human:   renderProfileList,
		Run: func(ctx context.Context, _ command.Invocation) (envelope.ConfigProfileListOutput, error) {
			var out envelope.ConfigProfileListOutput
			file, err := cfg.ReadFile(ctx, "")
			if err != nil {
				return out, settingFileError(err)
			}
			profiles, err := file.Profiles()
			if err != nil {
				return out, settingFileError(err)
			}
			out.Path, out.FileExists = file.Path(), file.Exists()
			out.Profiles = make([]envelope.ConfigProfile, 0, len(profiles))
			for _, profile := range profiles {
				out.Profiles = append(out.Profiles, envelope.ConfigProfile{
					Name: profile.Name, Default: profile.Default, Selected: profile.Selected,
					BaseURL: cfg.Redacted("base_url", profile.BaseURL), Email: profile.Email, SecretBackend: string(profile.SecretBackend),
				})
			}
			return out, nil
		},
	})
}

func newProfileAdd() *cobra.Command {
	var baseURL, email string
	return command.MustNew(command.Spec[envelope.ConfigProfileChangeOutput]{
		Op: "config.profile.add", Use: "add NAME", Short: "Create a profile from explicit account metadata", MutatesLocalState: true,
		Long: "Create a named profile with --base-url and --email. Existing names are refused. " +
			"The first profile becomes the default. Adding to an existing flat file retains its settings as the default profile. " +
			"A stable public credential reference is stored before any later login. No credentials or environment values are copied. " +
			"Use config set --profile NAME to configure preferences or another credential backend, then auth login --profile NAME. " +
			"--dry-run prepares the same local change without writing.",
		Example: "$ jira config profile add work --base-url=https://example.atlassian.net --email=person@example.com --dry-run",
		Args:    command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "NAME"}}},
		Flags: func(cmd *cobra.Command) {
			command.AddStringVar(cmd, &baseURL, "base-url", "", "Store the Jira site URL", command.FlagOpts{Terse: "Jira site", Placeholder: "URL"})
			command.AddStringVar(cmd, &email, "email", "", "Store the account email", command.FlagOpts{Terse: "Account email", Placeholder: "EMAIL"})
			command.AddDryRunFlag(cmd)
		},
		Validate: func() error { return validateInitialization(baseURL, email) },
		Human:    renderProfileChange, RetainOnError: retainProfileChange,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ConfigProfileChangeOutput, error) {
			return changeProfile(ctx, inv, func(file *cfg.File) (*cfg.ProfileUpdate, error) {
				return file.PrepareProfileAdd(inv.Args[0], baseURL, email)
			})
		},
	})
}

func newProfileRename() *cobra.Command {
	return command.MustNew(command.Spec[envelope.ConfigProfileChangeOutput]{
		Op: "config.profile.rename", Use: "rename NAME NEW_NAME", Short: "Rename a profile without moving credentials", MutatesLocalState: true,
		Long: "Rename one profile and update default_profile when it names that profile. Credential references stay unchanged. " +
			"Existing destination names are refused. --dry-run previews without writing.",
		Example: "$ jira config profile rename work office --dry-run",
		Args:    command.ArgSpec{Min: 2, Max: 2, Positions: []command.Arg{{Name: "NAME", Predictor: "profile"}, {Name: "NEW_NAME"}}},
		Flags:   command.AddDryRunFlag, Human: renderProfileChange, RetainOnError: retainProfileChange,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ConfigProfileChangeOutput, error) {
			return changeProfile(ctx, inv, func(file *cfg.File) (*cfg.ProfileUpdate, error) {
				return file.PrepareProfileRename(inv.Args[0], inv.Args[1])
			})
		},
	})
}

func newProfileDefault() *cobra.Command {
	return command.MustNew(command.Spec[envelope.ConfigProfileChangeOutput]{
		Op: "config.profile.default", Use: "default NAME", Short: "Set the default for later invocations", MutatesLocalState: true,
		Long: "Store the named profile as the default for later invocations. --profile and JIRA_PROFILE still override it. " +
			"--dry-run previews without writing.",
		Example: "$ jira config profile default work --dry-run",
		Args:    command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "NAME", Predictor: "profile"}}},
		Flags:   command.AddDryRunFlag, Human: renderProfileChange, RetainOnError: retainProfileChange,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ConfigProfileChangeOutput, error) {
			return changeProfile(ctx, inv, func(file *cfg.File) (*cfg.ProfileUpdate, error) { return file.PrepareProfileDefault(inv.Args[0]) })
		},
	})
}

func newProfileRemove() *cobra.Command {
	var flags command.MutationFlags
	return command.MustNew(command.Spec[envelope.ConfigProfileChangeOutput]{
		Op: "config.profile.remove", Use: "remove NAME", Short: "Remove profile settings while retaining credentials", MutatesLocalState: true,
		Long: "Remove one profile's local settings. Log out first if you also want to remove its stored credential. " +
			"Choose another default before removing the current default when other profiles remain. " +
			"A live removal requires --force in headless, agent or --no-input mode; an interactive terminal proceeds without a prompt. " +
			"--dry-run previews without writing or opening credentials.",
		Example: "$ jira config profile remove work --dry-run\n$ jira config profile remove work --force --no-input",
		Args:    command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "NAME", Predictor: "profile"}}},
		Flags: func(cmd *cobra.Command) {
			command.AddMutationFlags(cmd, &flags, command.WithHeadlessForce("profile removal"))
		},
		Human: renderProfileChange, RetainOnError: retainProfileChange,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ConfigProfileChangeOutput, error) {
			if !inv.DryRun && !flags.Force && !inv.Interactive {
				err := cli.NewInputError(cli.CodeFlagRequired, "profile removal requires --force in headless, agent or --no-input mode")
				err.Field = command.FlagForce
				return envelope.ConfigProfileChangeOutput{}, err
			}
			return changeProfile(ctx, inv, func(file *cfg.File) (*cfg.ProfileUpdate, error) { return file.PrepareProfileRemove(inv.Args[0]) })
		},
	})
}

func changeProfile(ctx context.Context, inv command.Invocation, prepare func(*cfg.File) (*cfg.ProfileUpdate, error)) (envelope.ConfigProfileChangeOutput, error) {
	var out envelope.ConfigProfileChangeOutput
	file, err := cfg.OpenFile(ctx, "")
	if err != nil {
		return out, settingFileError(err)
	}
	update, err := prepare(file)
	if err != nil {
		return out, settingFileError(err)
	}
	out = envelope.ConfigProfileChangeOutput{
		MutationState: envelope.MutationState{DryRun: inv.DryRun}, Path: file.Path(),
		Name: update.Name, PreviousName: update.PreviousName, DefaultProfile: update.DefaultProfile, WouldChange: update.WouldChange(), Status: "prepared",
	}
	if inv.DryRun {
		return out, nil
	}
	out.Changed, err = update.Save(ctx)
	if err != nil {
		out.Status = "unknown"
		if errors.Is(err, cfg.ErrFileChanged) || errors.Is(err, cfg.ErrFileInvalid) {
			out.Status = "not_saved"
		}
		return out, settingFileError(err)
	}
	out.Status = "unchanged"
	if out.Changed {
		out.Status = "saved"
	}
	return out, nil
}

func retainProfileChange(out envelope.ConfigProfileChangeOutput) bool { return out.Path != "" }

func renderProfileList(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ConfigProfileListOutput)
	if !ok {
		return fmt.Errorf("config.profile.list: unexpected payload %T", env.Data)
	}
	for _, profile := range out.Profiles {
		state := ""
		if profile.Default {
			state += " default"
		}
		if profile.Selected {
			state += " selected"
		}
		v.Line(cli.Text(profile.Name), cli.Text(profile.BaseURL), cli.Text(profile.Email), state)
	}
	v.Line("config file: " + cli.Text(out.Path))
	return nil
}

func renderProfileChange(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ConfigProfileChangeOutput)
	if !ok {
		return fmt.Errorf("config.profile change: unexpected payload %T", env.Data)
	}
	if out.DryRun {
		v.Line("would update profile", cli.Text(out.Name))
	}
	v.Fields(output.Field{Label: "Profile", Value: cli.Text(out.Name)}, output.Field{Label: "Status", Value: out.Status},
		output.Field{Label: "Default", Value: cli.Text(out.DefaultProfile)}, output.Field{Label: "Config file", Value: cli.Text(out.Path)})
	return nil
}
