package root

import (
	"github.com/gechr/clive/notify"
	"github.com/gechr/clive/updater"
	"github.com/gechr/conductor/cli/cobra"
	"github.com/gechr/x/agent"
	cobralib "github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/output"
	"github.com/matcra587/jira-cli/internal/selfupdate"
)

// notificationDisplay keeps Clive 0.4.9's TTY-only hyperlink decision behind
// the CLI's resolved style policy, including --color=never and NO_COLOR.
type notificationDisplay struct{ updater.Tool }

func (tool notificationDisplay) VersionLink(value string) string {
	if !cli.StyleEnabled() {
		return cli.Text(value)
	}
	return tool.Tool.VersionLink(value)
}

// notificationOptions preserves Clive's cache, refresh and hint throttles while
// suppressing hints outside the resolved human output surface. Conductor runs
// Check before output resolution, but Clive evaluates this comparator at flush,
// after the command has completed. Background refreshes never read sink state.
func notificationOptions(deps *Deps, sink *failureSink) []notify.Option {
	opts := append([]notify.Option(nil), deps.NotifyOptions...)
	return append(opts,
		notify.WithChannel("stable"),
		notify.WithOutdatedHintCommand("jira update"),
		notify.WithComparator(func(current, latest string) bool {
			if !sink.resolved || sink.format != output.FormatHuman {
				return false
			}
			newer, err := selfupdate.Newer(current, latest)
			return err == nil && newer
		}),
	)
}

// guardNotifications runs before Conductor's notify hook. Previews and local
// cache inspection must not write notification storage or check releases;
// explicit headless and agent runs also suppress the entire check. Clive
// otherwise deliberately refreshes even with non-TTY
// stderr, while its printer and the comparator above gate the hint itself.
func guardNotifications(program *cobra.Program) {
	preRun := program.Root.PersistentPreRunE
	tool := program.Runtime.App.Updater
	program.Root.PersistentPreRunE = func(cmd *cobralib.Command, args []string) error {
		defer func() { program.Runtime.App.Updater = tool }()
		noInput, err := cmd.Root().PersistentFlags().GetBool(cli.FlagNoInput)
		if err != nil {
			return err
		}
		var dryRun bool
		if cmd.Flags().Lookup(cli.FlagDryRun) != nil {
			dryRun, err = cmd.Flags().GetBool(cli.FlagDryRun)
			if err != nil {
				return err
			}
		}
		// Conductor's NotifySkip matches only the leading verb, so a nested
		// local inspection needs its canonical command path checked here.
		localInspection := cmd.CommandPath() == cmd.Root().Name()+" cache inspect"
		if dryRun || noInput || agent.Is() || localInspection {
			program.Runtime.App.Updater = nil
		} else {
			program.Runtime.App.Updater = tool
		}
		return preRun(cmd, args)
	}
}
