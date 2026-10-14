package issue

import (
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

// New returns the `jira issue` command group.
func New(deps Deps) *cobra.Command {
	// web is written by cobra when --web is parsed on ANY subcommand under
	// this group (it is a PERSISTENT flag, merged into every child's own
	// flag set — see command.AddPersistentBoolVar's own doc comment), but
	// nothing here reads this variable directly: each subcommand reads the
	// CURRENT invocation's value back off its own *cobra.Command
	// (isWeb(cmd), web.go) at Run time, the same "read from the flag the
	// path itself registered" posture command.Invocation's own DryRun
	// field documents. This local only gives BoolVarP somewhere to write.
	var web bool

	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Work with Jira issues",
		Long: "Read and manage Jira issues.\n\n" +
			"An issue is the unit everything else hangs off — epics and " +
			"subtasks are issues, links join them, and a board is a view over " +
			"them. The subcommands here read and change one or many at a time.\n\n" +
			"`--web` opens the thing a subcommand is looking at in a browser — " +
			"the issue for `view`/`comment list`, the search for `list` — " +
			"instead of fetching it: the target URL is built entirely from the " +
			"issue key or the composed JQL, so no request reaches Jira at all. " +
			"The URL always rides the machine envelope at `data.web_url`, " +
			"whatever `--output` was asked for; the browser itself is only " +
			"launched for a human render, and a failed or missing opener is a " +
			"warning, not a command failure — the URL is printed either way.\n\n" +
			"`--web` opens ONE page, so a multi-key (`KEY…`) subcommand " +
			"refuses it rather than guessing which of the keys you meant or " +
			"opening a tab per key — read the list, then `jira issue view KEY " +
			"--web` the one you want.",
	}
	// PERSISTENT: --web means the same thing on every subcommand under
	// this group, so it registers once here rather than once per leaf —
	// the same reasoning --dry-run's own doc comment (command/flags.go)
	// gives for a mutation flag meaning the same thing everywhere it
	// appears, generalized from "every mutation" to "every command in
	// this group". Every CURRENT leaf (view, comment list, list's own
	// search) supplies a real target — see each command's own runXWeb
	// function. A FUTURE subcommand under this group with no sensible
	// target (nothing to browse to before it exists, say) must not
	// silently accept and ignore it: it should refuse an explicitly-given
	// --web from its own Validate with cli.CodeFlagNotApplicable, the
	// same "an explicit ask beats a silent ignore" rule validateAllLimit
	// and validateCountFlags (list.go) already apply to every other flag
	// combination this CLI cannot honor. Inheriting the registration is
	// not the same as promising an effect.
	// The usage string says "single-key commands only" because it is the
	// SAME string on every subcommand's help, whether or not that
	// subcommand can honor the flag. clib reads a flag's help metadata off
	// the *pflag.Flag itself, and cobra hands every child the SAME flag
	// pointer for an inherited persistent flag — verified, not assumed —
	// so scoping the wording per subcommand is not something the framework
	// can express: extending the inherited flag on one child rewrites it
	// for every sibling. One honest sentence everywhere beats a precise
	// one that is a lie on the keyed commands, which refuse the flag
	// outright (refuseWebOnKeyedRead, link.go).
	command.AddPersistentBoolVar(cmd, &web, flagWeb, false,
		"Open the thing this command is looking at in a browser, in "+
			"addition to reporting its URL (single-key commands only)",
		command.FlagOpts{Terse: "Open in browser"})
	cmd.AddCommand(newView(deps))
	cmd.AddCommand(newComment(deps))
	cmd.AddCommand(newLink(deps))
	cmd.AddCommand(newAttachment(deps))
	cmd.AddCommand(newWatchers(deps))
	cmd.AddCommand(newWeblink(deps))
	cmd.AddCommand(newList(deps))
	cmd.AddCommand(newMine(deps))
	cmd.AddCommand(newClone(deps))
	cmd.AddCommand(newDelete(deps))
	cmd.AddCommand(newRank(deps))
	cmd.AddCommand(newTransition(deps))
	cmd.AddCommand(newCreate(deps))
	edit := newEdit(deps)
	edit.AddCommand(newEditPrepare(deps))
	edit.AddCommand(newEditDraft(deps), newEditInspect(deps), newEditResume(deps))
	cmd.AddCommand(edit)
	cmd.AddCommand(newMove(deps))
	cmd.AddCommand(newInput())
	return cmd
}
