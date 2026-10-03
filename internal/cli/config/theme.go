package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	cfg "github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func newTheme() *cobra.Command {
	var name, path string
	var nameSet, pathSet bool
	var cmd *cobra.Command
	return command.MustNew(command.Spec[envelope.ConfigThemeOutput]{
		Op: "config.theme", MutatesLocalState: true, Use: "theme", Short: "Inspect or select a terminal theme",
		Long: "Inspect the stored theme selection without defaults, environment values, Jira or credentials. " +
			"Use `jira config list` for effective values and sources.\n\n" +
			"--name selects a built-in preset and clears an existing custom path unless --path is also supplied. " +
			"An empty name restores fallback selection. --path selects and validates a custom TOML palette, " +
			"stored as an absolute path; an empty path disables the custom palette. Its base preset and named " +
			"RGB overrides affect help, value sinks and syntax highlighting on the next invocation. " +
			"JIRA_THEME overrides the custom file. Relative paths authored directly in settings resolve " +
			"against the selected settings file's directory.\n\n" +
			"Name/path changes use one validated candidate and one save. --dry-run performs the same validation " +
			"without creating or replacing settings. Equal stored values preserve original bytes and metadata. " +
			"Snapshot conflicts refuse publication; this does not lock out external writers. " +
			"A custom file that becomes unavailable later produces a warning and retains the preset fallback.",
		Example: `$ jira config theme

# Select a bundled preset without prompts
$ jira config theme --name dracula --no-input

# Validate a custom palette without writing settings
$ jira config theme --path ./palette.toml --dry-run --output=json

# Stop using a custom palette
$ jira config theme --path '' --no-input`,
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddStringVar(c, &name, "name", "", "Select a built-in preset; empty restores fallback selection", command.FlagOpts{Terse: "Preset name", Placeholder: "name", Enum: cfg.SettingValueChoices("theme")})
			command.AddStringVar(c, &path, "path", "", "Select a custom palette TOML file; empty disables it", command.FlagOpts{Terse: "Custom palette", Placeholder: "file", Hint: "file"})
			command.AddDryRunFlag(c)
		},
		Validate: func() error {
			nameSet, pathSet = cmd.Flags().Changed("name"), cmd.Flags().Changed("path")
			if nameSet {
				if err := cfg.ValidateSettingValue("theme", name); err != nil {
					input := cli.NewInputError(cli.CodeFlagValueInvalid, err.Error())
					input.Field = "name"
					input.Suggestions = cli.SuggestValues(name, cfg.SettingValueChoices("theme"))
					return input
				}
			}
			return nil
		},
		Human: renderThemeHuman,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ConfigThemeOutput, error) {
			return runTheme(ctx, inv.DryRun, name, path, nameSet, pathSet)
		},
	})
}

func runTheme(ctx context.Context, dryRun bool, name, path string, nameSet, pathSet bool) (envelope.ConfigThemeOutput, error) {
	var zero envelope.ConfigThemeOutput
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if pathSet && path != "" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return zero, localIO("resolve custom palette", err)
		}
		if _, err := cfg.LoadThemeFile(ctx, absolute); err != nil {
			if errors.Is(err, cfg.ErrThemeFileInvalid) {
				input := cli.NewInputError(cli.CodeFlagValueInvalid, err.Error())
				input.Field = "path"
				return zero, input
			}
			return zero, localIO("validate custom palette; settings were not changed", err)
		}
		path = absolute
	}
	var file *cfg.File
	var err error
	if nameSet || pathSet {
		file, err = cfg.OpenFile(ctx, "")
	} else {
		file, err = cfg.ReadFile(ctx, "")
	}
	if err != nil {
		return zero, localIO("open config", err)
	}
	storedName, err := file.Setting("theme")
	if err != nil {
		return zero, settingFileError(err)
	}
	storedPath, err := file.Setting("theme_path")
	if err != nil {
		return zero, settingFileError(err)
	}
	out := envelope.ConfigThemeOutput{
		MutationState: envelope.MutationState{DryRun: dryRun}, Path: file.Path(), FileExists: file.Exists(),
		Name: storedName.Value, NamePresent: storedName.Present, ThemePath: storedPath.Value, PathPresent: storedPath.Present,
	}
	assignments := map[string]string{}
	if nameSet {
		assignments["theme"] = name
		if !pathSet {
			assignments["theme_path"] = ""
		}
	}
	if pathSet {
		assignments["theme_path"] = path
	}
	if len(assignments) == 0 {
		return out, ctx.Err()
	}
	prepared, err := file.PrepareSettings(assignments)
	if err != nil {
		return zero, settingFileError(err)
	}
	for _, change := range prepared.Changes {
		if change.Next.Key == "theme" {
			out.Name, out.NamePresent = change.Next.Value, change.Next.Present
		} else {
			out.ThemePath, out.PathPresent = change.Next.Value, change.Next.Present
		}
	}
	out.WouldChange = prepared.WouldChange()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if !dryRun {
		out.Changed, err = prepared.Save(ctx)
		if err != nil {
			return zero, settingFileError(err)
		}
		out.Created = out.Changed && !file.Exists()
		out.FileExists = file.Exists() || out.Created
	}
	return out, nil
}

func renderThemeHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ConfigThemeOutput)
	if !ok {
		return fmt.Errorf("config.theme: unexpected payload %T", env.Data)
	}
	switch {
	case out.DryRun && out.WouldChange:
		v.Line("would select theme")
	case out.Changed:
		v.Line("stored theme selection")
	default:
		v.Line("stored theme")
	}
	v.Line("preset:", shownValue(out.Name))
	v.Line("custom palette:", shownValue(out.ThemePath))
	v.Line("config file:", cli.Text(out.Path))
	return nil
}
