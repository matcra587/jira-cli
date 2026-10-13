package issue

import (
	"context"
	"fmt"
	"strconv"

	xslices "github.com/gechr/x/slices"
	xsync "github.com/gechr/x/sync"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// newWeblink returns the `jira issue weblink` command group.
//
// **A weblink is not a link**, and the two groups sit side by side under
// `issue` without sharing a line of code. `link` joins two Jira issues
// with a type an admin configured; `weblink` attaches an arbitrary URL —
// a runbook, a pull request, a dashboard — to one issue. Different
// endpoint, different resource, different id space, and an id from one
// means nothing to the other.
//
// Named `weblink` rather than `remote-link`, which is Jira's own term:
// the API calls these "remote issue links", but a remote link is what
// the thing is from the SERVER's point of view, and from a caller's it
// is a web address stuck on an issue. 0.15 shipped `weblink` and it
// reads better than the API's own noun, so the departure is deliberate.
func newWeblink(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "weblink",
		Short: "Work with an issue's web links",
		Long: "Read and change the web links on issues.\n\n" +
			"A web link attaches an arbitrary URL to an issue — a runbook, a " +
			"pull request, a dashboard. It is NOT the same thing as `jira issue " +
			"link`, which joins two Jira issues with a typed relationship: " +
			"different resource, different ids, and neither command's ids work " +
			"on the other.\n\n" +
			"`add` and `list` take a list of issue keys. `delete` takes ONE key " +
			"and ONE link id, because a web link's id is scoped to the issue it " +
			"hangs off — which is exactly the opposite of `issue link delete`, " +
			"whose ids are site-wide.\n\n" +
			"`add` is create-or-replace when you give it `--global-id`: a link " +
			"already carrying that id on that issue is replaced rather than " +
			"duplicated, and the envelope reports which happened.",
		Example: "# what is linked from KEY-123\n" +
			"$ jira issue weblink list KEY-123\n\n" +
			"# attach a runbook\n" +
			"$ jira issue weblink add KEY-123 --url https://example.com/runbook --title 'Runbook'\n\n" +
			"# remove one, by id\n" +
			"$ jira issue weblink delete KEY-123 10042",
	}
	cmd.AddCommand(newWeblinkAdd(deps))
	cmd.AddCommand(newWeblinkDelete(deps))
	cmd.AddCommand(newWeblinkList(deps))
	return cmd
}

// newWeblinkList returns `jira issue weblink list`.
func newWeblinkList(deps Deps) *cobra.Command {
	var keyed command.KeyedFlags
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueWeblinkListOutput]{
		Op:       "issue.weblink.list",
		Identity: true,
		Keyed:    true,
		Use:      "list ISSUE_KEY...",
		Short:    "List an issue's web links",
		Long: "List the URLs attached to one or more issues.\n\n" +
			"Each entry carries the link's own id — the handle `jira issue " +
			"weblink delete` takes — alongside the url and title.\n\n" +
			"This is a keyed command: results come back as one entry per key, " +
			"in the order you gave them, at `data.results[]`. A key that fails " +
			"does not fail the others — its entry carries `ok: false` and the " +
			"reason. Partial success succeeds; total failure returns an error. " +
			"Total failure and interruption retain ordered outcomes.\n\n" +
			"There is no paging. Jira's remote-link endpoint takes no paging " +
			"parameters and answers with the whole set, so there is no page to " +
			"be on. Use `--jq` if you want fewer of them.\n\n" +
			"`--web` is refused here. It opens one page and this command reads " +
			"a list of keys.",
		Example: "# what is linked from one issue\n" +
			"$ jira issue weblink list KEY-123\n\n" +
			"# several issues at once\n" +
			"$ jira issue weblink list KEY-123 KEY-124\n\n" +
			"# just the urls, as json\n" +
			"$ jira issue weblink list KEY-123 --output=json --jq '.data.results[].data.weblinks[].url'",
		Args: command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddKeyedFlags(c, &keyed)
		},
		Validate: func() error {
			if err := keyed.Validate(); err != nil {
				return err
			}
			return refuseWebOnKeyedRead(cmd)
		},
		Human:         renderWeblinkList,
		RetainOnError: func(out envelope.IssueWeblinkListOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueWeblinkListOutput, error) {
			return runWeblinkList(ctx, deps, inv.Args, keyed.Parallelism)
		},
	})
}

// runWeblinkList reads every key's web links and assembles the keyed
// payload — pulled out of the command.Spec closure so it is directly
// testable against a fake core.JiraClient, matching runWatchersList's
// own split. Like that function it returns no site: a web link's target
// is its own URL, and the issue keys are the section headers.
func runWeblinkList(ctx context.Context, deps Deps, keys []string, parallelism int) (envelope.IssueWeblinkListOutput, error) {
	var zero envelope.IssueWeblinkListOutput
	client, err := deps.JiraClient(ctx)
	if err != nil {
		return zero, err
	}

	outcomes := make([]weblinkListOutcome, len(keys))
	// ONE spinner around the whole fan-out, for runWatchersList's reason:
	// a spinner owns the terminal's live region and several drawing at
	// once interleave into corruption.
	_, err = command.Spin(ctx, weblinkSpinnerLabel(keys), func(ctx context.Context) (struct{}, error) {
		xsync.Parallel(parallelism, len(keys), func(i int) {
			outcomes[i] = readWeblinks(ctx, client, keys[i])
		})
		return struct{}{}, nil
	})
	results := make([]envelope.KeyedResult[envelope.IssueWeblinkListData], len(outcomes))
	raw := make([]error, len(outcomes))
	for i, o := range outcomes {
		results[i], raw[i] = o.result, o.err
	}
	out := envelope.IssueWeblinkListOutput{}
	out.Results = results
	if err != nil {
		return out, err
	}
	return out, command.KeyedFailure(results, raw)
}

// weblinkListOutcome is one key's result plus the raw error behind a
// failure — watchersOutcome's pairing, for its reason.
type weblinkListOutcome struct {
	result envelope.KeyedResult[envelope.IssueWeblinkListData]
	// err is nil exactly when result.OK.
	err error
}

// weblinkSpinnerLabel is the fan-out's one spinner label. It says how
// many issues are being read rather than which, because the label is
// fixed for the life of the animation while the reads are not.
func weblinkSpinnerLabel(keys []string) string {
	if len(keys) == 1 {
		return "Loading web links for " + keys[0]
	}
	return fmt.Sprintf("Loading web links for %d issues", len(keys))
}

// readWeblinks is one key's whole outcome.
//
// An empty slice is a valid observation. The adapter requires an explicit
// native array; this seam also checks every identity and rejects duplicates
// before publishing the list, including clients supplied by callers.
func readWeblinks(ctx context.Context, client core.JiraClient, key string) weblinkListOutcome {
	read, err := client.GetRemoteLinks(ctx, key)
	if err != nil {
		return failedWeblinkListResult(key, err)
	}
	if err := core.ValidateRemoteLinkList(read.Links); err != nil {
		return failedWeblinkListResult(key, err)
	}
	data := envelope.IssueWeblinkListData{Weblinks: weblinkViews(read.Links)}
	// The identity carries only the key, for readWatchers' reason: this
	// endpoint answers with the links alone and names no issue id or
	// self, so IssueRef omits both rather than emitting them empty.
	return weblinkListOutcome{result: envelope.KeyedResult[envelope.IssueWeblinkListData]{
		Issue: envelope.IssueRef{Key: key},
		OK:    true,
		Data:  &data,
	}}
}

// failedWeblinkListResult records one key's failure, resolved through
// the SAME taxonomy the envelope's own errors[] entries use.
func failedWeblinkListResult(key string, err error) weblinkListOutcome {
	class := cli.MapError(err)
	return weblinkListOutcome{
		result: envelope.KeyedResult[envelope.IssueWeblinkListData]{
			Issue: envelope.IssueRef{Key: key},
			OK:    false,
			Err: &envelope.KeyError{
				Code:    class.Code,
				Message: class.Message,
				Hint:    class.Hint,
			},
		},
		err: err,
	}
}

// weblinkViews narrows one issue's wire remote links to their CLI-shaped
// view, FLATTENING the wire's `object` wrapper — see envelope.WeblinkView
// for why the url and title come up a level rather than riding the
// grouping Jira sends them in.
func weblinkViews(links []jira.RemoteIssueLink) []envelope.WeblinkView {
	// Never nil: an issue with no web links publishes an empty array, so
	// a caller iterating data.results[].data.weblinks needs no null check.
	out := make([]envelope.WeblinkView, 0, len(links))
	for _, l := range links {
		object := val(l.Object)
		out = append(out, envelope.WeblinkView{
			// The wire types the id as an integer and every other id in
			// this contract is a string, so it is converted once, here, at
			// the boundary — see envelope.WeblinkView.ID.
			ID:           strconv.FormatInt(l.ID.Value, 10),
			URL:          object.URL.Value,
			Title:        object.Title.Value,
			Summary:      object.Summary.Value,
			Relationship: l.Relationship.Value,
			GlobalID:     l.GlobalID.Value,
			Application:  val(l.Application).Name.Value,
		})
	}
	return out
}

// renderWeblinkList writes the human view: one section per key, in the
// order the caller gave them, exactly as renderWatchersList does and for
// the same reason — a keyed read's whole shape is per key, and a single
// merged table would lose which issue each link belongs to.
func renderWeblinkList(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueWeblinkListOutput)
	if !ok {
		return fmt.Errorf("issue.weblink.list: human renderer received %T, want envelope.IssueWeblinkListOutput", env.Data)
	}
	for i, r := range out.Results {
		if i > 0 {
			v.Line()
		}
		// cli.Text, not a styled sink: Section hands its title to clog's
		// Divider, which bolds whatever it is given, and a pre-styled title
		// would strand its own reset mid-span (axis H5).
		v.Section(cli.Text(r.Issue.Key))
		renderWeblinkResult(v, r)
	}
	return nil
}

// renderWeblinkResult draws one key's section body: its failure, its
// emptiness, or its links.
func renderWeblinkResult(v *output.View, r envelope.KeyedResult[envelope.IssueWeblinkListData]) {
	if !r.OK {
		// The message is Jira-authored in the common case, so it goes
		// through cli.Text; the hint is CLI-authored and rides the message
		// path — renderWatchersResult's own split.
		v.Line(cli.Text(r.Err.Message))
		v.Hint(r.Err.Hint)
		return
	}
	if len(r.Data.Weblinks) == 0 {
		v.Line(cli.Text("No web links."))
		return
	}
	output.Table(v, cli.ResolvedTheme(), issueWeblinkColumns(), r.Data.Weblinks)
}

// weblinkKeys renders the issue keys of a keyed weblink write for one
// sentence of prose. Shared by the add renderer's two branches so the
// preview and the live view cannot disagree about how they are styled.
func weblinkKeys[T any](results []envelope.KeyedResult[T]) []string {
	return xslices.Map(results, func(r envelope.KeyedResult[T]) string {
		return cli.Entity(r.Issue.Key)
	})
}
