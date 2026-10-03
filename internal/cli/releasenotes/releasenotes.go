package releasenotes

import (
	"context"
	"fmt"
	"strings"

	xslices "github.com/gechr/x/slices"
	"github.com/spf13/cobra"

	changelog "github.com/matcra587/jira-cli"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// New returns the local, authentication-free release-notes command.
func New() *cobra.Command {
	var latest bool
	return command.MustNew(command.Spec[envelope.ReleaseNotesOutput]{
		Op: "release.notes", Use: "release-notes [version]", Aliases: []string{"rn"},
		Short: "Show what's changed in jira-cli",
		Long: "Show jira-cli's own release notes embedded in this binary. No network or authentication is needed.\n\n" +
			"With no arguments, show the full changelog, newest first. Pass a version (with or without v), " +
			"or --latest for only the newest release. Machine output contains structured releases and sections.\n\n" +
			"Human output is Markdown, styled on a color terminal. Long interactive output uses JIRA_PAGER, " +
			"then PAGER, or a built-in pager. Unavailable configured pagers fall back to the next choice; " +
			"a pager that starts and fails returns an error. --no-pager prints directly. Pipes, machine modes, agent sessions " +
			"and --no-input never page. These are tool releases, unrelated to a Jira project's releases.",
		Example:   "$ jira release-notes\n$ jira release-notes --latest\n$ jira rn 0.7.7\n$ jira release-notes --output=json",
		Args:      command.ArgSpec{Max: 1, Positions: []command.Arg{{Name: "version", Predictor: "release"}}},
		PageHuman: true,
		Flags: func(cmd *cobra.Command) {
			command.AddBoolVar(cmd, &latest, "latest", false, "Show only the newest release", command.FlagOpts{Terse: "Newest release"})
		},
		Human: renderHuman,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.ReleaseNotesOutput, error) {
			if err := ctx.Err(); err != nil {
				return envelope.ReleaseNotesOutput{}, err
			}
			var query string
			if len(inv.Args) != 0 {
				query = strings.TrimSpace(inv.Args[0])
			}
			return buildOutput(query, latest)
		},
	})
}

func buildOutput(query string, latest bool) (envelope.ReleaseNotesOutput, error) {
	if query != "" && latest {
		err := cli.NewInputError(cli.CodeArgValueInvalid, "pass a version or --latest, not both")
		err.Field = "version"
		return envelope.ReleaseNotesOutput{}, err
	}
	if query != "" {
		release, ok := changelog.Find(query)
		if !ok {
			versions := xslices.Map(changelog.Releases(), func(release changelog.Release) string { return release.Version })
			err := cli.NewInputError(cli.CodeArgValueInvalid, fmt.Sprintf("no release notes for %q; available: %s", query, strings.Join(versions, ", ")))
			err.Field = "version"
			return envelope.ReleaseNotesOutput{}, err
		}
		return single(release), nil
	}
	releases := changelog.Releases()
	if latest {
		if len(releases) == 0 {
			return envelope.ReleaseNotesOutput{}, cli.NewCodedError(cli.CodeOutputWriteFailed, "no release notes are embedded in this build")
		}
		return single(releases[0]), nil
	}
	return envelope.ReleaseNotesOutput{Releases: releases, Markdown: changelog.Full()}, nil
}

func single(release changelog.Release) envelope.ReleaseNotesOutput {
	return envelope.ReleaseNotesOutput{Releases: []changelog.Release{release}, Markdown: release.Markdown}
}

func renderHuman(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.ReleaseNotesOutput)
	if !ok {
		return fmt.Errorf("release notes: human renderer received %T, want envelope.ReleaseNotesOutput", env.Data)
	}
	v.Block(cli.Markdown(out.Markdown, v.Output().Width()))
	return nil
}
