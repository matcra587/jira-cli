package issue

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// NewOpen returns the top-level issue browser shortcut.
func NewOpen(deps Deps) *cobra.Command {
	return command.MustNew(command.Spec[envelope.IssueOpenOutput]{
		Op:       "open",
		Identity: true,
		Use:      "open ISSUE_KEY",
		Short:    "Open an issue in a browser",
		Long: "Resolve one issue's browse URL from the configured site without " +
			"fetching the issue or opening credentials.\n\n" +
			"Human output starts the configured browser and always prints the URL. " +
			"A missing or unstartable browser produces a warning; the URL remains usable. " +
			"Machine modes and the default agent mode return the URL without launching " +
			"a browser. `opened` reports successful process handoff, not page loading.\n\n" +
			"A single-key expression may contain duplicate keys or a one-key range. " +
			"An expression resolving to several different keys is refused.",
		Example: "$ jira open KEY-123\n\n" +
			"# print the URL without launching a browser\n" +
			"$ jira open KEY-123 --output=json\n\n" +
			"# select a site from an explicit settings file\n" +
			"$ jira open KEY-123 --config settings.toml --output=compact",
		Args: command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Human: func(v *output.View, env envelope.Envelope) error {
			return renderWebURL(v, env.Data.(envelope.IssueOpenOutput).WebURL)
		},
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueOpenOutput, error) {
			return runOpen(ctx, deps, inv.Args, execOpener)
		},
	})
}

func runOpen(ctx context.Context, deps Deps, args []string, open browserOpener) (envelope.IssueOpenOutput, error) {
	var zero envelope.IssueOpenOutput
	keys, err := command.ParseIssueKeys(args)
	if err != nil {
		return zero, err
	}
	if len(keys) != 1 {
		e := cli.NewInputError(cli.CodeArgValueInvalid, "open takes exactly one distinct issue key; use issue view KEY --web for each wanted issue")
		e.Field = "ISSUE_KEY"
		return zero, e
	}
	target, err := issueBrowseTarget(ctx, deps, keys[0])
	if err != nil {
		return zero, err
	}
	return envelope.IssueOpenOutput{
		Issue:   envelope.IssueRef{Key: keys[0]},
		WebLink: envelope.WebLink{WebURL: target},
		Opened:  openForHuman(ctx, open, target),
	}, nil
}
