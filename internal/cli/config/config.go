package config

import (
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
)

// Deps is what the config commands need from the composition root.
type Deps struct {
	// Credentials opens the credential store, so `config list` can
	// report the layer it publishes.
	Credentials func() (core.CredentialStore, error)
}

// New returns the `jira config` command group.
//
// Credentials belong to `jira auth`. Inspection redacts any secret material
// already present in a settings file.
func New(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and edit CLI settings",
		Long: "Inspect and edit the settings that change how `jira` behaves.\n\n" +
			"Settings resolve in layers — built-in defaults, the config file, " +
			"then `JIRA_*` environment variables — with later layers winning. " +
			"`config list` shows the EFFECTIVE value of each setting and which " +
			"layer supplied it, so a surprising value can be traced to its source. " +
			"`config init --base-url URL --email EMAIL` creates a new file from " +
			"explicit account metadata without overwriting existing settings. " +
			"`config get KEY` reads only the stored file value, " +
			"`config set KEY VALUE` validates and stores one setting, and " +
			"`config edit` opens the file itself. `config theme` inspects or selects " +
			"a built-in or custom palette.",
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newList(deps), newGet(), newSet(), newInit(), newProfile(), newTheme(), newEdit())
	command.SetMeta(cmd, command.Meta{
		Op:                "config",
		NoSuccessEnvelope: "a bare group prints help, not an operation result",
	})
	return cmd
}
