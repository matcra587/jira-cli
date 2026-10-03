package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
	"github.com/matcra587/jira-cli/internal/selfupdate"
)

// Source freezes release discovery and downloads only that observed release.
type Source interface {
	Latest(context.Context) (selfupdate.Release, error)
	Download(context.Context, selfupdate.Release, string, string) (*selfupdate.Candidate, error)
}

// Target owns an existing executable across discovery and publication.
type Target interface {
	Publish(context.Context, *os.File, string) (selfupdate.Publication, error)
	Close() error
}

// BrewUpdater retains actual installation facts even after failed upgrades.
type BrewUpdater interface {
	Update(context.Context) (selfupdate.BrewOutcome, error)
}

// Deps contains only update adapters. No Jira client or credential capability
// can enter this command; constructors do no work until execution.
type Deps struct {
	Detect func() (selfupdate.Install, error)
	Source func() (Source, error)
	Target func(context.Context, string) (Target, error)
	Brew   func(context.Context, string) (BrewUpdater, error)
}

// DefaultDeps wires anonymous release reads and local installation owners.
func DefaultDeps() Deps {
	return Deps{
		Detect: selfupdate.DetectInstall,
		Source: func() (Source, error) { return selfupdate.NewGitHubSource(nil) },
		Target: func(ctx context.Context, path string) (Target, error) { return selfupdate.OpenTarget(ctx, path) },
		Brew:   func(ctx context.Context, path string) (BrewUpdater, error) { return selfupdate.OpenBrew(ctx, path) },
	}
}

// New constructs update/up without inspecting installations or reading releases.
func New(deps Deps) *cobra.Command {
	defaults := DefaultDeps()
	if deps.Detect == nil {
		deps.Detect = defaults.Detect
	}
	if deps.Source == nil {
		deps.Source = defaults.Source
	}
	if deps.Target == nil {
		deps.Target = defaults.Target
	}
	if deps.Brew == nil {
		deps.Brew = defaults.Brew
	}
	var mut command.MutationFlags
	return command.MustNew(command.Spec[envelope.UpdateOutput]{
		Op: "update", Use: "update", Aliases: []string{"up"},
		Short: "Update jira-cli through its installation owner",
		Long: "Inspect the latest published jira-cli release and update this executable. No Jira authentication is needed.\n\n" +
			"Published archives are checksum-verified and replace the running executable's resolved path. Homebrew upgrades only " +
			"the verified matcra587/tap/jira formula; old kegs and shadowing PATH copies are preserved. Scoop, mise and go install " +
			"installations return their own update command without contacting GitHub. Unknown installations are refused.\n\n" +
			"--dry-run reads release availability without downloading or installing. Unlike Jira mutation previews, it contacts " +
			"the public release service. A live update without an interactive terminal requires --force, including agent sessions " +
			"and --no-input. An explicit terminal invocation needs no additional prompt.\n\n" +
			"Failures retain observed installation facts. A published executable may still accompany a timeout or durability error; " +
			"inspect the receipt and installed version before retrying. Updates are never replayed automatically.",
		Example: "$ jira update --dry-run\n$ jira update\n$ jira up --force --output=json",
		Args:    command.ArgSpec{Max: 0}, MutatesLocalState: true,
		Flags: func(cmd *cobra.Command) {
			command.AddMutationFlags(cmd, &mut, command.WithHeadlessForce("updating the executable"))
		},
		RetainOnError: func(out envelope.UpdateOutput) bool { return out.Current != "" || out.Channel != "" || out.Attempted },
		Human:         renderHuman,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.UpdateOutput, error) {
			return run(ctx, deps, inv, mut.Force)
		},
	})
}

func run(ctx context.Context, deps Deps, inv command.Invocation, force bool) (out envelope.UpdateOutput, err error) {
	out.Current, out.DryRun = inv.Version, inv.DryRun
	if err := ctx.Err(); err != nil {
		return out, err
	}
	installation, err := deps.Detect()
	if err != nil {
		return out, updateFailure(err)
	}
	out.Channel, out.Hint, out.Managed = string(installation.Channel), installation.Hint, installation.Managed()
	if out.Managed {
		return out, nil
	}
	if installation.Channel != selfupdate.ChannelArchive && installation.Channel != selfupdate.ChannelBrew {
		return out, cli.NewCodedError(cli.CodeUpdateUnsupported, "the installation method is unknown; no executable was changed")
	}
	if !inv.DryRun && !inv.Interactive && !force {
		err := cli.NewInputError(cli.CodeFlagRequired, "updating this executable without an interactive terminal requires --force")
		err.Field = command.FlagForce
		return out, err
	}
	var target Target
	var brew BrewUpdater
	switch installation.Channel {
	case selfupdate.ChannelArchive:
		target, err = deps.Target(ctx, installation.Executable)
		if err == nil {
			defer func() {
				if closeErr := target.Close(); closeErr != nil {
					err = errors.Join(err, updateFailure(closeErr))
				}
			}()
		}
	case selfupdate.ChannelBrew:
		brew, err = deps.Brew(ctx, installation.Executable)
	}
	if err != nil {
		return out, updateFailure(err)
	}
	source, err := deps.Source()
	if err != nil {
		return out, updateFailure(err)
	}
	release, err := source.Latest(ctx)
	if err != nil {
		return out, updateFailure(err)
	}
	out.Latest = release.Version
	available, err := selfupdate.Newer(inv.Version, release.Version)
	if err != nil {
		return out, updateFailure(err)
	}
	out.UpdateAvailable = &available
	if !available || inv.DryRun {
		return out, nil
	}
	if brew != nil {
		result, updateErr := brew.Update(ctx)
		out.Attempted, out.Updated, out.Uncertain = result.Attempted, result.Updated, result.Uncertain
		out.Brew = &envelope.BrewUpdate{Before: brewInstallation(result.Before), Shadow: result.Shadow}
		if result.After != nil {
			after := brewInstallation(*result.After)
			out.Brew.After = &after
		}
		return out, updateFailure(updateErr)
	}
	candidate, err := source.Download(ctx, release, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return out, updateFailure(err)
	}
	defer func() {
		if closeErr := candidate.Close(); closeErr != nil {
			err = errors.Join(err, updateFailure(closeErr))
		}
	}()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.Attempted = true
	publication, err := target.Publish(ctx, candidate.File, candidate.SHA256)
	out.Publication = &envelope.UpdatePublication{
		Published: publication.Published, Durable: publication.Durable, Uncertain: publication.Uncertain,
		Backup: publication.Backup, Bytes: publication.Bytes, SHA256: publication.SHA256,
	}
	out.Updated, out.Uncertain = publication.Published, publication.Uncertain
	return out, updateFailure(err)
}

func brewInstallation(in selfupdate.BrewInstallation) envelope.UpdateInstallation {
	return envelope.UpdateInstallation{Executable: in.Executable, Version: in.Version, SHA256: in.SHA256, Head: in.Head}
}

func updateFailure(err error) error {
	if err == nil {
		return nil
	}
	code := cli.CodeUpdateFailed
	switch {
	case errors.Is(err, selfupdate.ErrBrewOwnership):
		code = cli.CodeUpdateOwnership
	case errors.Is(err, selfupdate.ErrIntegrity):
		code = cli.CodeUpdateIntegrity
	case errors.Is(err, selfupdate.ErrTargetChanged):
		code = cli.CodeUpdateChanged
	case errors.Is(err, selfupdate.ErrReleaseUnavailable):
		code = cli.CodeUpdateUnavailable
	}
	return errors.Join(cli.NewCodedError(code, "jira-cli update failed"), err)
}

func renderHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.UpdateOutput)
	if !ok {
		return fmt.Errorf("update: human renderer received %T, want envelope.UpdateOutput", env.Data)
	}
	v.Fields(output.Field{Label: "Installation", Value: cli.Text(out.Channel)}, output.Field{Label: "Current", Value: cli.Text(out.Current)},
		output.Field{Label: "Latest", Value: cli.Text(out.Latest)})
	switch {
	case out.Uncertain:
		v.Line("Installation outcome is unconfirmed; inspect the installed executable before retrying.")
	case out.Updated:
		v.Line("Executable update observed.")
	case out.Attempted:
		v.Line("Update attempted; no changed executable was observed.")
	case out.Managed && out.DryRun:
		v.Line("Preview only; update through the installation owner: " + cli.Text(out.Hint))
	case out.Managed:
		v.Line("Update through the installation owner: " + cli.Text(out.Hint))
	case out.UpdateAvailable != nil && *out.UpdateAvailable && out.DryRun:
		v.Line("A newer release is available; preview installed nothing.")
	case out.UpdateAvailable != nil && !*out.UpdateAvailable:
		v.Line("No newer published release is available.")
	default:
		if out.Hint != "" {
			v.Line(cli.Text(out.Hint))
		}
	}
	if out.Brew != nil && out.Brew.Shadow != "" {
		v.Fields(output.Field{Label: "PATH copy", Value: cli.Text(out.Brew.Shadow)})
		v.Line("This preserved copy shadows the Homebrew executable.")
	}
	if out.Brew != nil && out.Brew.After != nil {
		v.Fields(output.Field{Label: "Installed", Value: cli.Text(out.Brew.After.Version)})
	}
	if out.Publication != nil && out.Publication.Backup != "" {
		v.Fields(output.Field{Label: "Retained backup", Value: cli.Text(out.Publication.Backup)})
	}
	return nil
}
