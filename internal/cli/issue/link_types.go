package issue

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// newLinkTypes returns `jira issue link types`.
//
// **The command that makes `link create`'s refusal actionable.** Naming a
// type the site does not have is refused with the available phrasings in
// `suggestions` — which is the right shape for an agent and a poor one for
// a person who wanted to look before they typed. Without this command the
// only way to enumerate the types was to guess wrong on purpose.
//
// It is also the reason the cached list is safe to have: `--refresh` here
// and on `link create` is the deliberate bust, and this command is where a
// caller can SEE whether what they are looking at is fresh.
func newLinkTypes(deps Deps) *cobra.Command {
	var opts linkTypeOptions

	return command.MustNew(command.Spec[envelope.IssueLinkTypesOutput]{
		Op:    "issue.link.types",
		Use:   "types",
		Short: "List the link types this site has",
		Long: "List every issue link type configured on this site.\n\n" +
			"Each type has a name and two phrasings — `blocks` one way, `is " +
			"blocked by` the other — and `jira issue link create --type` " +
			"accepts any of the three. The phrasing is what picks the " +
			"DIRECTION, so a type shown as `blocks` / `is blocked by` gives you " +
			"two different links depending on which you say.\n\n" +
			"**The list is cached on disk and shared with `link create`.** It " +
			"is site configuration that changes when an admin edits it and not " +
			"otherwise, so re-reading it on every link operation would spend a " +
			"round trip re-learning something that almost never moves. " +
			"`data.cached` says where this answer came from and " +
			"`data.age_seconds` says how old it is.\n\n" +
			"`--refresh` ignores the cache and reads the site, which is what to " +
			"reach for when an admin has just added a type. `--ttl-minutes` " +
			"changes how long a cached list stays usable. You rarely need " +
			"either: `link create` re-reads by itself when you name a type it " +
			"does not recognize, so a list that went stale heals on the one " +
			"command that would have suffered.\n\n" +
			"This command takes no issue key. Link types belong to the site, " +
			"not to an issue.",
		Example: "# what relationships this site has\n" +
			"$ jira issue link types\n\n" +
			"# after an admin adds one\n" +
			"$ jira issue link types --refresh\n\n" +
			"# just the phrasings --type accepts, as json\n" +
			"$ jira issue link types --output=json --jq '.types[] | .outward, .inward'\n\n" +
			"# check whether you are looking at a cached list\n" +
			"$ jira issue link types --output=json --jq '{cached, age_seconds}'",
		Args: command.ArgSpec{},
		Flags: func(c *cobra.Command) {
			addLinkTypeCacheFlags(c, &opts)
		},
		Human: renderLinkTypes,
		Run: func(ctx context.Context, _ command.Invocation) (envelope.IssueLinkTypesOutput, error) {
			return runLinkTypes(ctx, deps, opts)
		},
	})
}

// runLinkTypes reads the list — from the cache when one is fresh, from
// Jira otherwise — and publishes it with its provenance.
//
// **A cached answer builds no client**, which is the whole point: the
// round trip this command exists to spare is the one it does not make. The
// client constructor is therefore called INSIDE readLinkTypes' miss path
// rather than here, so a cache hit never resolves credentials either.
func runLinkTypes(ctx context.Context, deps Deps, opts linkTypeOptions) (envelope.IssueLinkTypesOutput, error) {
	var zero envelope.IssueLinkTypesOutput

	// The site is the cache key, and it comes from the client — so this
	// command does build one even on a hit. That is not the round trip
	// being spared: constructing the adapter resolves config and
	// credentials locally and contacts nothing.
	client, err := deps.JiraClient()
	if err != nil {
		return zero, err
	}
	read, err := readLinkTypes(ctx, client, issueCache(deps), client.BaseURL(), opts)
	if err != nil {
		return zero, err
	}

	out := envelope.IssueLinkTypesOutput{
		Types:  linkTypeViews(read.types),
		Cached: read.cached,
	}
	if read.cached {
		// Truncated to whole seconds rather than rounded: an age is a
		// lower bound on staleness, and rounding up would report a list as
		// older than it is.
		out.AgeSeconds = int64(read.age.Seconds())
	}
	return out, nil
}

// renderLinkTypes writes the human view: one row per type, both phrasings
// beside the name, because the phrasing is what a caller actually passes
// to `--type`.
func renderLinkTypes(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueLinkTypesOutput)
	if !ok {
		return fmt.Errorf("issue.link.types: human renderer received %T, want envelope.IssueLinkTypesOutput", env.Data)
	}
	if len(out.Types) == 0 {
		// Not an error: a site can genuinely have no link types, and
		// saying so beats printing an empty table a reader has to
		// interpret.
		v.Line(cli.Text("This site has no issue link types configured."))
		return nil
	}

	// Through the SHARED column table (columns.go), not a hand-built one:
	// a guardrail requires every issue-scoped table to be defined there,
	// and every cell it renders is Jira-authored, so each goes through a
	// sanitizing sink (axis H4).
	output.Table(v, cli.ResolvedTheme(), issueLinkTypeColumns(), out.Types)

	if out.Cached {
		// Said on the human path too, not just in the envelope: a person
		// looking for a type an admin just added needs to know they may be
		// reading yesterday's list, and the fix.
		v.Hint(fmt.Sprintf("This list is cached (%s old). Use --refresh to read the site again.",
			time.Duration(out.AgeSeconds)*time.Second))
	}
	return nil
}
