package issue

import (
	"context"
	"fmt"

	xsync "github.com/gechr/x/sync"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// linkFields is the field set the read narrows to. An issue's links
// arrive inside the issue itself — Jira has no list-an-issue's-links
// endpoint — so this command IS a GetIssue, and asking for one field
// keeps it from dragging the whole issue (description, comments, every
// custom field) across the wire per key. That matters more here than on
// a single-issue read: the cost is paid once per key.
var linkFields = []string{"issuelinks"}

// The two directions a link can point, as the envelope publishes them.
// Jira's own vocabulary: the OTHER issue sits on the inward or the
// outward side of the link, and the type's matching phrase reads the
// link from the asked-about issue toward it.
const (
	directionInward  = "inward"
	directionOutward = "outward"
)

// newLink returns the `jira issue link` command group.
func newLink(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Work with an issue's links",
		Long: "Read and change the links between issues.\n\n" +
			"A link joins two issues with a named, directional relationship — " +
			"`blocks` one way, `is blocked by` the other — so what a link MEANS " +
			"depends on which end you are reading it from. These commands " +
			"always read from the issue you asked about: `relation` is the " +
			"phrase for that direction, and `issue` is whatever sits at the " +
			"other end. `create` writes the same way round — you give it the " +
			"sentence and it works out which end Jira wants where.\n\n" +
			"`delete` takes a link ID and no issue key, because a link id is " +
			"unique across the site. That is the opposite of `jira issue " +
			"weblink delete`, whose ids are scoped to their issue — the two are " +
			"different resources and the commands say so rather than pretending " +
			"to match.\n\n" +
			"These are ISSUE links, joining two Jira issues. For attaching an " +
			"arbitrary URL to an issue, see `jira issue weblink`.",
		Example: "# what KEY-123 is linked to\n" +
			"$ jira issue link list KEY-123\n\n" +
			"# say that one issue blocks another\n" +
			"$ jira issue link create KEY-123 --to KEY-456 --type blocks\n\n" +
			"# what relationships this site has\n" +
			"$ jira issue link types\n\n" +
			"# remove a link, by the id `link list` printed\n" +
			"$ jira issue link delete 10042",
	}
	cmd.AddCommand(newLinkCreate(deps))
	cmd.AddCommand(newLinkDelete(deps))
	cmd.AddCommand(newLinkList(deps))
	cmd.AddCommand(newLinkTypes(deps))
	return cmd
}

// newLinkList returns `jira issue link list` — the CLI's first keyed
// (`KEY…`) command.
func newLinkList(deps Deps) *cobra.Command {
	// keyed carries the shared multi-key surface (--parallelism). The
	// same call marks this command keyed, which command.New cross-checks
	// against the Keyed declaration below — see AddKeyedFlags.
	var keyed command.KeyedFlags
	// cmd is captured so Validate can ask it isWeb(cmd) — --web is a
	// PERSISTENT flag registered on the `issue` group (issue.go), so this
	// command inherits it whether or not it can honor it, and refusing an
	// explicit one is this command's own job (see the refusal below).
	var cmd *cobra.Command
	// site carries the Jira base URL the run's own client resolved, from
	// Run to Human — the same shape issue view's newView uses for its
	// own site variable, and for the same reason: the KEY column
	// hyperlinks through cli.IssueKey, and Human has no deps to
	// construct a client with.
	var site string

	return command.MustNew(command.Spec[envelope.IssueLinkListOutput]{
		Op:       "issue.link.list",
		Identity: true,
		Keyed:    true,
		Use:      "list ISSUE_KEY...",
		Short:    "List an issue's links",
		Long: "List the issues linked to one or more issues, and how.\n\n" +
			"Every link is reported from the perspective of the key you asked " +
			"about: `relation` reads forward from it (`is blocked by`), " +
			"`direction` says which side of the link the other issue sits on, " +
			"and `issue` is that other issue.\n\n" +
			"This is a keyed command: results come back as one entry per key, " +
			"in the order you gave them, at `data.results[]`. A key that fails " +
			"does not fail the others — its entry carries `ok: false` and the " +
			"reason, and the command still succeeds. Only a run where EVERY " +
			"key failed is reported as a failure of the command itself.\n\n" +
			"Total failure, cancellation and timeout retain all joined " +
			"per-key outcomes in the full failure frame.\n\n" +
			"Links have no pagination: Jira has no endpoint that lists them, " +
			"so they arrive whole with the issue they belong to. That is also " +
			"why `--parallelism` is worth having here — the work is one issue " +
			"read per key.\n\n" +
			"`--web` is refused here. It opens one page and this command reads " +
			"a list of keys, so there is no single thing to open: run `jira " +
			"issue view KEY --web` for whichever issue you want to look at.",
		Example: "# what one issue is linked to\n" +
			"$ jira issue link list KEY-123\n\n" +
			"# several issues at once\n" +
			"$ jira issue link list KEY-123 KEY-124 KEY-125\n\n" +
			"# one at a time, as json\n" +
			"$ jira issue link list KEY-123 KEY-124 --parallelism 1 --output=json",
		Args: command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY"}}},
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
		Human: func(v *output.View, env envelope.Envelope) error {
			return renderLinkList(v, env, site)
		},
		RetainOnError: func(out envelope.IssueLinkListOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueLinkListOutput, error) {
			out, resolved, err := runLinkList(ctx, deps, inv.Args, keyed.Parallelism)
			site = resolved
			return out, err
		},
	})
}

// refuseWebOnKeyedRead rejects an explicitly-given --web.
//
// `--web` is PERSISTENT on the `issue` group, so every subcommand
// inherits it — and issue.go's own doc comment states the rule for a
// subcommand that cannot honor it: refuse an explicit one with
// CodeFlagNotApplicable rather than accept it and do nothing.
//
// A keyed read cannot honor it. `--web` means "open the thing this
// command is looking at", and this command is looking at N things: one
// key would open a page, three would open three, and a browse target
// that depends on how many arguments were typed is a worse contract than
// no target at all. The URL is one command away — `jira issue view KEY
// --web` — so nothing is lost but the guessing.
func refuseWebOnKeyedRead(cmd *cobra.Command) error {
	if !isWeb(cmd) {
		return nil
	}
	e := cli.NewInputError(cli.CodeFlagNotApplicable,
		"--web opens one page, and this command reads a list of keys — run `jira issue view KEY --web` for the issue you want to look at")
	e.Field = flagWeb
	return e
}

// runLinkList reads every key's links and assembles the keyed payload.
//
// Pulled out of the command.Spec closure so it is directly testable
// against a fake core.JiraClient, matching runCommentList's own split.
// The resolved site rides back as a second return value — the renderer
// needs it for the KEY column's hyperlink and has no deps of its own —
// exactly as runView hands back its comments preview.
func runLinkList(ctx context.Context, deps Deps, keys []string, parallelism int) (envelope.IssueLinkListOutput, string, error) {
	var zero envelope.IssueLinkListOutput
	client, err := deps.JiraClient()
	if err != nil {
		return zero, "", err
	}
	site := client.BaseURL()

	outcomes := make([]keyOutcome, len(keys))
	// ONE spinner around the whole fan-out, not one per key: a spinner
	// owns the terminal's live region, and several drawing at once
	// interleave into corruption. The label counts keys rather than
	// naming them for the same reason — it must not change per key while
	// the animation is up.
	_, err = command.Spin(ctx, linkSpinnerLabel(keys), func(ctx context.Context) (struct{}, error) {
		// Every key runs regardless of another's failure — that is what
		// makes a bad key a partial result rather than a whole-command
		// one — so the closure returns no error and each outcome is
		// recorded in place instead. Parallel writes outcomes[i] from the
		// goroutine handling key i, which needs no lock: x/sync.Parallel
		// hands each call a distinct index, and the slice was sized up
		// front. Order therefore comes from the ARGV, never from which
		// read finished first, which is the ordering guarantee
		// envelope.KeyedResults publishes.
		xsync.Parallel(parallelism, len(keys), func(i int) {
			outcomes[i] = readLinks(ctx, client, keys[i])
		})
		return struct{}{}, nil
	})
	// The published verdicts and the raw errors behind them, split apart
	// for command.KeyedFailure — which owns the partial-vs-total rule for
	// every keyed command, not just this one.
	results := make([]envelope.KeyedResult[envelope.IssueLinkListData], len(outcomes))
	raw := make([]error, len(outcomes))
	for i, o := range outcomes {
		results[i], raw[i] = o.result, o.err
	}
	out := envelope.IssueLinkListOutput{}
	out.Results = results
	if err != nil {
		// Spin joins producers before results are published. Keep their
		// outcomes while interruption remains the whole-run classification.
		return out, site, err
	}
	return out, site, command.KeyedFailure(results, raw)
}

// keyOutcome is one key's result plus the raw error behind a failure.
//
// The envelope's own KeyError is a narrowed view — code, message, hint —
// and that is the right shape to PUBLISH per key. It is the wrong thing
// to fail the command with: re-raising a failure from it would rebuild a
// generic error out of three strings and drop everything the taxonomy
// reads off the original (a rate limit's Retry-After, the transport's
// attempt count, the field Jira rejected). So the original travels
// alongside, and command.KeyedFailure — which owns that rule for every
// keyed command — hands it back untouched.
type keyOutcome struct {
	result envelope.KeyedResult[envelope.IssueLinkListData]
	// err is nil exactly when result.OK.
	err error
}

// linkSpinnerLabel is the fan-out's one spinner label. It says how many
// issues are being read rather than which, because the label is fixed
// for the life of the animation while the reads are not.
func linkSpinnerLabel(keys []string) string {
	if len(keys) == 1 {
		return "Loading links for " + keys[0]
	}
	return fmt.Sprintf("Loading links for %d issues", len(keys))
}

// readLinks is one key's whole outcome: its links, or why it has none to
// report. It returns a result rather than an error precisely because a
// failure here is data — the entry the caller reads at
// data.results[].data — and not the command's own failure.
func readLinks(ctx context.Context, client core.JiraClient, key string) keyOutcome {
	read, err := client.GetIssue(ctx, key, linkFields)
	switch {
	case err != nil:
		return failedResult(key, err)
	case read.Issue == nil:
		// GetIssue's own contract (internal/adapters/jira/client.go) is a
		// non-nil Issue whenever it returns a nil error. Failing loudly
		// rather than trusting that silently mirrors runView's own guard:
		// a zero-valued payload would read as an issue with no links
		// rather than the failure it actually is.
		return failedResult(key, fmt.Errorf("issue.link.list: empty issue response for %q", key))
	}
	data := envelope.IssueLinkListData{Links: linkViews(val(read.Issue.Fields).Issuelinks.Value)}
	return keyOutcome{result: envelope.KeyedResult[envelope.IssueLinkListData]{
		Issue: envelope.IssueRef{
			ID:   read.Issue.ID.Value,
			Key:  read.Issue.Key.Value,
			Self: read.Issue.Self.Value,
		},
		OK:   true,
		Data: &data,
	}}
}

// failedResult records one key's failure, resolved through the SAME
// taxonomy the envelope's own errors[] entries use (cli.MapError), so a
// caller branches on one vocabulary of codes wherever a failure appears.
//
// The identity carries only the key: a key that could not be read
// resolved no id or self, and IssueRef omits both rather than emitting
// them empty.
func failedResult(key string, err error) keyOutcome {
	class := cli.MapError(err)
	return keyOutcome{
		result: envelope.KeyedResult[envelope.IssueLinkListData]{
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

// linkViews narrows one issue's wire links to their CLI-shaped view,
// answering the question the wire leaves to the caller: which end of
// each link is the issue we asked about.
//
// A link names an inwardIssue and an outwardIssue, and Jira populates
// exactly one of them — the far end; the near end is the issue whose
// payload the link arrived in. So the populated side is what the caller
// wants, and the type's phrasing for the direction it sits in is how the
// link reads from the asked-about issue toward it (see
// envelope.IssueLinkView).
func linkViews(links []jira.IssueLink) []envelope.IssueLinkView {
	// Never nil: an issue with no links publishes an empty array, so a
	// caller iterating data.results[].data.links needs no null check.
	out := make([]envelope.IssueLinkView, 0, len(links))
	for _, l := range links {
		linkType := val(l.Type)
		view := envelope.IssueLinkView{
			ID: l.ID.Value,
			Type: envelope.IssueLinkTypeView{
				ID:      linkType.ID.Value,
				Name:    linkType.Name.Value,
				Inward:  linkType.Inward.Value,
				Outward: linkType.Outward.Value,
			},
		}
		// The far end is whichever side is NOT the issue we asked about.
		// Decided on the SIDE that is populated rather than by comparing
		// keys: Jira sends exactly one of the two per link (the other end
		// being the issue whose payload this is), so presence answers it
		// without depending on the requested key's spelling matching the
		// response's — which a moved or renamed issue would break.
		switch {
		case l.InwardIssue.Set:
			view.Direction = directionInward
			view.Relation = linkType.Inward.Value
			view.Issue = linkedIssueView(val(l.InwardIssue))
		case l.OutwardIssue.Set:
			view.Direction = directionOutward
			view.Relation = linkType.Outward.Value
			view.Issue = linkedIssueView(val(l.OutwardIssue))
		default:
			// Neither side present. The schema marks both required, so
			// this is a response shape Jira does not document sending —
			// kept rather than dropped, because a link the CLI silently
			// omitted would leave a caller who cross-checks the count in
			// the UI hunting for a link that does exist. Direction and
			// relation stay empty, which says exactly what is known.
		}
		out = append(out, view)
	}
	return out
}

// linkedIssueView narrows the issue at the far end of a link. Its fields
// come from the platform's own closed "Fields" schema (jira.Fields), not
// the tenant-dynamic container an issue read returns, so every value
// here is a known one — see envelope.LinkedIssueFields.
func linkedIssueView(li jira.LinkedIssue) envelope.LinkedIssueView {
	f := val(li.Fields)
	it := val(f.Issuetype)
	return envelope.LinkedIssueView{
		IssueRef: envelope.IssueRef{
			ID:   li.ID.Value,
			Key:  li.Key.Value,
			Self: li.Self.Value,
		},
		Fields: envelope.LinkedIssueFields{
			Summary: f.Summary.Value,
			Status: envelope.LinkedIssueStatus{
				Name:     val(f.Status).Name.Value,
				Category: val(val(f.Status).StatusCategory).Key.Value,
			},
			Type: envelope.LinkedIssueType{
				Name:           it.Name.Value,
				HierarchyLevel: it.HierarchyLevel.Value,
			},
			Priority: envelope.LinkedIssuePriority{Name: val(f.Priority).Name.Value},
		},
	}
}

// renderLinkList writes the human view. site is the base URL Run
// resolved, carried in through the Spec's own Human closure because the
// KEY column's hyperlink needs it and nothing in the envelope does.
//
// The layout: one section per key, in the order the caller gave them,
// because a keyed read's whole shape is per-key and a single merged
// table would lose which issue each link belongs to. A key with no links
// gets one plain line rather than an empty table, the same decision
// renderCommentList makes for an empty thread — "nothing here" is never
// silent. A FAILED key renders its message and hint in place: the
// command succeeded, so nothing else would ever tell a person that one
// of the keys they asked about did not answer.
func renderLinkList(v *output.View, env envelope.Envelope, site string) error {
	out, ok := env.Data.(envelope.IssueLinkListOutput)
	if !ok {
		return fmt.Errorf("issue.link.list: human renderer received %T, want envelope.IssueLinkListOutput", env.Data)
	}
	for i, r := range out.Results {
		if i > 0 {
			v.Line()
		}
		// cli.Text, not cli.IssueKey: Section hands its title to clog's
		// Divider, which bolds whatever it is given, and a pre-styled
		// title would strand its own reset mid-span and drop the bold
		// from everything after it (axis H5 — see cli.EntityAs's own doc
		// comment for the byte-verified version of that bug).
		v.Section(cli.Text(r.Issue.Key))
		renderLinkResult(v, site, r)
	}
	return nil
}

// renderLinkResult draws one key's section body: its failure, its
// emptiness, or its links.
func renderLinkResult(v *output.View, site string, r envelope.KeyedResult[envelope.IssueLinkListData]) {
	if !r.OK {
		// The message is Jira-authored in the common case (its own 404
		// prose rides through the taxonomy), so it goes through cli.Text
		// like every other Jira string. The hint is CLI-authored and
		// static, and rides the message path so the flags it names render
		// in clog's inline-code style — the same split nextPageHint
		// (list.go) already makes.
		v.Line(cli.Text(r.Err.Message))
		v.Hint(r.Err.Hint)
		return
	}
	if len(r.Data.Links) == 0 {
		v.Line(cli.Text("No links."))
		return
	}
	output.Table(v, cli.ResolvedTheme(),
		issueLinkColumns(site, statusPillWidth(r.Data.Links, issueRowFromLink)), r.Data.Links)
}
