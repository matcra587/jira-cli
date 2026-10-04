package issue

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// defaultCommentLimit is `issue comment list`'s default page size — big
// enough to show a real conversation in one call, small enough that a
// long-lived issue's thread never comes back as one unbounded response.
const defaultCommentLimit = 50

// maxAllCommentPages and maxAllComments are `--all`'s own safety cap, the
// same pair and the same reasoning `issue list`'s own --all carries
// (list.go's maxAllPages/maxAllIssues): a drain with nothing stopping it
// is the shape that exhausts memory one page at a time, and a thread on a
// long-lived incident issue is exactly where that would happen.
//
// Hitting either stops the walk and SAYS SO — Truncated/TruncatedReason
// on the envelope, and a hint naming --start for a human — which is the
// part that matters. A truncated result reported as complete is the
// failure mode; a truncated result reported as truncated is just a
// bounded read.
//
// Unlike issue list, there is no --unbounded escape hatch here and none
// is needed: this endpoint is OFFSET-paginated, so `--start` already
// continues from wherever the cap stopped. `issue list`'s cursor has no
// such property, which is precisely why that command needed a flag and
// this one does not.
const (
	maxAllCommentPages = 50
	maxAllComments     = 5000
)

// maxCommentRequestPageSize bounds any SINGLE page request the --all walk
// makes, independent of how many comments the whole walk wants —
// maxRequestPageSize's own reasoning (list.go), and the endpoint's own
// documented maxResults default is 100 (the vendored spec's
// `/issue/{issueIdOrKey}/comment` GET), so this asks for exactly what the
// server already considers a page.
const maxCommentRequestPageSize = 100

// Flag names this command spells more than once, kept as constants so
// registration and every refusal below name the same strings.
const (
	flagCommentAll   = "all"
	flagCommentOrder = "order"
	flagCommentStart = "start"
	flagCommentLimit = "limit"
)

// The --order values a CALLER types. Deliberately not the wire's own
// `-created`/`+created` (core.CommentOrder, which is what those mean):
// nobody should have to know that a leading minus means newest, and the
// two words say the whole of what the flag does — there is only one
// sortable field on this endpoint, so the choice is a direction and
// nothing else (see core.CommentOrder for the spec citation).
const (
	orderNewest = "newest"
	orderOldest = "oldest"
)

// commentOrderFromFlag maps --order's own word onto the wire value,
// refusing anything else by naming the whole accepted set rather than
// letting Jira answer with a 400 about a parameter the caller never
// typed.
//
// An empty value means the flag was not given, which becomes
// CommentOrderDefault — "no preference" — rather than an explicit
// newest-first. The two reach the same query (the adapter resolves it),
// and keeping them distinct is what lets this function stay honest about
// what the caller actually said.
func commentOrderFromFlag(value string) (core.CommentOrder, error) {
	switch value {
	case "":
		return core.CommentOrderDefault, nil
	case orderNewest:
		return core.CommentOrderNewestFirst, nil
	case orderOldest:
		return core.CommentOrderOldestFirst, nil
	default:
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s takes %s or %s, not %q — this endpoint sorts only by creation date, so the choice is a direction",
			flagCommentOrder, orderNewest, orderOldest, value))
		e.Field = flagCommentOrder
		return "", e
	}
}

// validateCommentAll refuses --all alongside an explicit --start or
// --limit.
//
// The same rule `issue list`'s validateAllLimit applies to the same pair
// of ideas, and for the same reason: --all already asks for every comment
// there is, so a page size or a page offset can only disagree with it,
// never sharpen it. "Changed, not the value" again — --all beside
// --limit's own untouched default is not a competing request, only the
// flag's zero-effect default sitting there unread.
func validateCommentAll(all, startChanged, limitChanged bool) error {
	if !all {
		return nil
	}
	switch {
	case startChanged:
		e := cli.NewInputError(cli.CodeFlagNotApplicable,
			"--start names which page to begin at, but --all drains the whole thread from the "+
				"beginning — combining them is refused rather than silently picking a winner; "+
				"drop --start, or drop --all and page with --start/--limit yourself")
		e.Field = flagCommentStart
		return e
	case limitChanged:
		e := cli.NewInputError(cli.CodeFlagNotApplicable,
			"--limit sets one page's size, but --all already drains every page there is — "+
				"combining them is refused rather than silently picking a winner; drop --limit, "+
				"or drop --all and page with --start/--limit yourself")
		e.Field = flagCommentLimit
		return e
	default:
		return nil
	}
}

// newComment returns the `jira issue comment` command group.
func newComment(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment",
		Short: "Work with an issue's comments",
		Long: "Read an issue's comment thread, and change it.\n\n" +
			"A comment thread can run long, so every read here is paginated " +
			"against Jira's own API rather than fetched in full and cut down " +
			"afterward — a huge thread never has to be pulled into memory just " +
			"to show its first page.\n\n" +
			"`add`, `edit` and `delete` write, so each carries `--dry-run` like " +
			"every mutation: the preview composes the whole request and reports " +
			"it without contacting Jira.\n\n" +
			"`add` takes a list of issue keys — one comment posted to each. " +
			"`edit` and `delete` take ONE key and ONE comment id, because a " +
			"comment id belongs to the issue it was posted on and means nothing " +
			"on another. `comment list` is where the ids come from.\n\n" +
			"`delete` is the only command here that cannot be undone, and the " +
			"only one that asks before acting.",
		Example: "# the latest page of comments\n" +
			"$ jira issue comment list KEY-123\n\n" +
			"# add one\n" +
			"$ jira issue comment add KEY-123 --markdown 'Deployed to staging.'\n\n" +
			"# change one, by id\n" +
			"$ jira issue comment edit KEY-123 10042 --markdown 'Deployed to production.'\n\n" +
			"# remove one, without a prompt\n" +
			"$ jira issue comment delete KEY-123 10042 --force",
	}
	cmd.AddCommand(newCommentAdd(deps))
	cmd.AddCommand(newCommentDelete(deps))
	cmd.AddCommand(newCommentEdit(deps))
	cmd.AddCommand(newCommentList(deps))
	return cmd
}

// newCommentList returns `jira issue comment list`.
func newCommentList(deps Deps) *cobra.Command {
	var start, limit int
	var all bool
	var order string
	// resolvedOrder is --order's own parsed value, carried from Validate
	// to Run so a bad word is refused before any request is made rather
	// than mid-walk.
	var resolvedOrder core.CommentOrder
	// cmd is captured so Run can ask it isWeb(cmd) — --web is a
	// PERSISTENT flag registered on the `issue` group (issue.go), not
	// here, so this command only ever READS it back off its own
	// *cobra.Command once cobra has merged the parent's persistent flags
	// in — see isWeb's own doc comment (web.go).
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueCommentListOutput]{
		Op:            "issue.comment.list",
		Identity:      true,
		Passthrough:   true,
		RetainOnError: func(out envelope.IssueCommentListOutput) bool { return out.Comments != nil },
		Use:           "list ISSUE_KEY",
		Short:         "List an issue's comments",
		Long: "List one page of an issue's comments, newest first.\n\n" +
			"This is a dedicated, API-paginated read: `--start`/`--limit` map " +
			"straight to Jira's own startAt/maxResults, so a long thread is " +
			"never fetched in full just to show a page of it. The response " +
			"carries a pagination block alongside the comments — there is more " +
			"to see when `start_at + returned` is less than `total`.\n\n" +
			"`--all` walks the whole thread instead, one server page at a " +
			"time behind a single spinner, up to a safety cap (see its own " +
			"help). It still PAGES — it does not fetch everything and cut it " +
			"down afterward — so a huge thread costs steady requests rather " +
			"than one enormous response. It is refused alongside `--start` or " +
			"`--limit`, which can only disagree with draining everything.\n\n" +
			"`--order` reads the thread `newest`-first (the default) or " +
			"`oldest`-first. It is a direction and not a field, because this " +
			"endpoint sorts only by creation date.\n\n" +
			"Valid observed pages survive failed or interrupted reads in the " +
			"full failure frame, with `truncated` and `truncated_reason` naming " +
			"the incomplete read. Malformed pagination or repeated comment " +
			"identities reports `comment_response_invalid`; suspect pages are " +
			"not published. Offset pagination is not a stable snapshot while " +
			"other users change the thread.",
		Example: "# the latest 50 comments\n" +
			"$ jira issue comment list KEY-123\n\n" +
			"# the next page\n" +
			"$ jira issue comment list KEY-123 --start 50\n\n" +
			"# a smaller page, as json\n" +
			"$ jira issue comment list KEY-123 --limit 10 --output=json\n\n" +
			"# the whole thread, oldest first\n" +
			"$ jira issue comment list KEY-123 --all --order oldest",
		Args: command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "ISSUE_KEY"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddIntVar(c, &limit, flagCommentLimit, defaultCommentLimit,
				"Maximum comments to return in this page; must be greater than zero",
				command.FlagOpts{Terse: "Page size", Placeholder: "n"})
			command.AddIntVar(c, &start, flagCommentStart, 0,
				"Offset of the first comment to return; must be zero or greater",
				command.FlagOpts{Terse: "Page offset", Placeholder: "n"})
			command.AddBoolVar(c, &all, flagCommentAll, false,
				fmt.Sprintf("Fetch every page of the thread, up to a safety cap of %d pages / "+
					"%d comments; refused alongside --start or --limit",
					maxAllCommentPages, maxAllComments),
				command.FlagOpts{Terse: "Whole thread"})
			command.AddStringVar(c, &order, flagCommentOrder, "",
				fmt.Sprintf("Read the thread %s-first (the default) or %s-first",
					orderNewest, orderOldest),
				command.FlagOpts{Terse: "Thread order", Placeholder: "direction"})
		},
		Validate: func() error {
			if start < 0 {
				e := cli.NewInputError(cli.CodeFlagValueInvalid, "--start must be zero or greater")
				e.Field = flagCommentStart
				return e
			}
			if limit <= 0 {
				e := cli.NewInputError(cli.CodeFlagValueInvalid, "--limit must be greater than zero")
				e.Field = flagCommentLimit
				return e
			}
			resolved, err := commentOrderFromFlag(order)
			if err != nil {
				return err
			}
			resolvedOrder = resolved
			return validateCommentAll(all,
				cmd.Flags().Changed(flagCommentStart), cmd.Flags().Changed(flagCommentLimit))
		},
		Human: renderCommentList,
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueCommentListOutput, error) {
			key := inv.Args[0]
			if isWeb(cmd) {
				// --web's own short-circuit: comments live on the issue
				// page, so this skips runCommentList (and its
				// GetComments call) entirely for the SAME browse URL
				// `issue view --web` resolves — see runCommentListWeb's
				// own doc comment. execOpener is the production
				// browserOpener; a test calls runCommentListWeb directly
				// with a fake one instead.
				return runCommentListWeb(ctx, deps, key, execOpener)
			}
			if all {
				return drainCommentList(ctx, deps, key, resolvedOrder)
			}
			return runCommentList(ctx, deps, key, start, limit, resolvedOrder)
		},
	})
}

// runCommentListWeb answers `issue comment list KEY --web`: comments
// live on the issue's own page, so this opens the SAME browse URL
// `issue view --web` does — literally the same code now
// (openIssueBrowseTarget, web.go, which owns the shared reasoning) — and
// makes no GetComments call. All that differs is the Output shape below:
// Comments stays empty and Pagination at its zero value, which
// IssueCommentListOutput's own WebLink field documents as --web's
// contract.
func runCommentListWeb(ctx context.Context, deps Deps, key string, open browserOpener) (envelope.IssueCommentListOutput, error) {
	var zero envelope.IssueCommentListOutput
	target, err := openIssueBrowseTarget(ctx, deps, key, open)
	if err != nil {
		return zero, err
	}
	out := envelope.IssueCommentListOutput{WebLink: envelope.WebLink{WebURL: target}}
	out.Issue.Key = key
	return out, nil
}

// runCommentList fetches one page of key's comments and assembles the
// payload — pulled out of the command.Spec closure so it is directly
// testable against a fake core.JiraClient, matching runView's own split.
func runCommentList(ctx context.Context, deps Deps, key string, start, limit int, order core.CommentOrder) (envelope.IssueCommentListOutput, error) {
	var zero envelope.IssueCommentListOutput
	client, err := deps.JiraClient()
	if err != nil {
		return zero, err
	}
	read, err := command.Spin(ctx, "Loading comments for "+key, func(ctx context.Context) (core.CommentRead, error) {
		return client.GetComments(ctx, key, start, limit, order)
	})
	if read.Page == nil && err != nil {
		return zero, err
	}
	if pageErr := core.ValidateCommentPage(read.Page, start, limit); pageErr != nil {
		return zero, errors.Join(err, fmt.Errorf("issue.comment.list %q: %w", key, pageErr))
	}
	out := buildCommentListOutput(key, read)
	if err != nil {
		markCommentReadIncomplete(&out, err)
	}
	return out, err
}

// drainCommentList answers `--all`: every page of key's thread, walked
// page by page behind one spinner.
//
// **It PAGES, and that is the whole point.** The obvious cheap
// implementation — ask for maxResults=5000 once, or fetch everything and
// slice — is exactly what this command's own doc comment rules out, and
// what got 0.15's `attachment list --limit/--all` dropped: those sliced
// an array the CLI had already pulled in full, so the flag described a
// cost the caller was paying anyway and the honesty of "paginated read"
// was a fiction. This walk holds only what it has decided to keep, one
// server page at a time, and stops on its own caps.
//
// The walk lives HERE rather than behind the seam for the reason
// core.JiraClient.GetComments' own doc comment gives: a seam that walked
// every page would have to hold the whole thread before it could return
// anything at all, which is the failure its single-page contract exists
// to prevent. issue list's autoPaginate makes the same choice one command
// over.
//
// ONE spinner around the whole walk, not one per page — command.Spin's
// own rule, and autoPaginate's own shape: a human sees a single progress
// line however many requests it takes underneath.
//
// The returned Pagination describes the WHOLE walk rather than its last
// page: StartAt stays 0 because that is genuinely where this read began,
// Returned counts everything collected, and Total is the server's own,
// carried from the last page seen. That differs from issue list, whose
// cursor pagination must report the last page's resume point — an offset
// reader resumes from StartAt+Returned, which this shape gives directly.
func drainCommentList(ctx context.Context, deps Deps, key string, order core.CommentOrder) (envelope.IssueCommentListOutput, error) {
	var zero envelope.IssueCommentListOutput
	client, err := deps.JiraClient()
	if err != nil {
		return zero, err
	}

	out := envelope.IssueCommentListOutput{}
	out.Issue.Key = key
	seen := make(map[string]struct{})

	walk := func(ctx context.Context) (envelope.IssueCommentListOutput, error) {
		pages := 0
		for {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			read, err := client.GetComments(ctx, key, len(out.Comments), maxCommentRequestPageSize, order)
			if read.Page == nil && err != nil {
				return out, err
			}
			if pageErr := core.ValidateCommentPage(read.Page, len(out.Comments), maxCommentRequestPageSize); pageErr != nil {
				return out, errors.Join(err, fmt.Errorf("issue.comment.list %q: %w", key, pageErr))
			}
			page := read.Page
			if page.StartAt != len(out.Comments) {
				return out, errors.Join(err, fmt.Errorf("%w: comment walk offset changed before completion", core.ErrCommentResponseInvalid))
			}
			for _, comment := range page.Comments {
				if _, exists := seen[comment.ID.Value]; exists {
					return out, errors.Join(err, fmt.Errorf("%w: comment walk repeats an earlier identity", core.ErrCommentResponseInvalid))
				}
			}
			for _, comment := range page.Comments {
				seen[comment.ID.Value] = struct{}{}
			}
			pages++
			if out.Comments == nil {
				out.Comments = []envelope.CommentView{}
			}
			out.Comments = append(out.Comments, commentViews(page.Comments)...)
			out.Pagination = envelope.OffsetPagination{
				StartAt:    0,
				MaxResults: page.MaxResults,
				Total:      page.Total,
				Returned:   len(out.Comments),
			}
			if err != nil {
				return out, err
			}

			switch {
			case len(out.Comments) >= page.Total:
				// The server's own total reached: the thread is complete.
				return out, nil
			case pages >= maxAllCommentPages:
				out.Truncated = true
				out.TruncatedReason = envelope.TruncatedReasonMaxPages
				return out, nil
			case len(out.Comments) >= maxAllComments:
				out.Truncated = true
				out.TruncatedReason = envelope.TruncatedReasonMaxComments
				return out, nil
			}
		}
	}

	joined, err := command.Spin(ctx, "Loading every comment for "+key, walk)
	if err != nil {
		markCommentReadIncomplete(&joined, err)
	}
	return joined, err
}

// markCommentReadIncomplete distinguishes failed observations from successful
// safety caps. Pagination still comes only from the last validated page.
func markCommentReadIncomplete(out *envelope.IssueCommentListOutput, err error) {
	out.Truncated = true
	out.TruncatedReason = envelope.TruncatedReasonReadFailed
	if errors.Is(err, core.ErrCommentResponseInvalid) {
		out.TruncatedReason = envelope.TruncatedReasonResponseInvalid
	}
}

// buildCommentListOutput assembles the payload from one comment page:
// the identity slot from the key the caller asked for (this endpoint's
// own response carries no issue-level id/self, unlike GetIssue's — see
// the doc comment on identityRefField in tests/audit/corpus_test.go),
// each comment narrowed to its CLI-shaped view, and the pagination block
// built from the server's own startAt/maxResults/total plus how many
// this page actually returned.
func buildCommentListOutput(key string, read core.CommentRead) envelope.IssueCommentListOutput {
	out := envelope.IssueCommentListOutput{}
	out.Issue.Key = key
	page := read.Page
	if page == nil {
		// Unreachable from the production caller: runCommentList fails
		// loudly on a nil Page before it ever gets here, the same way
		// runView guards buildOutput's nil Issue (see its doc comment).
		// Kept so this stays total for a direct test caller, and returns
		// the identity it does know rather than panicking on a shape the
		// server contract says cannot arrive.
		return out
	}
	out.Comments = commentViews(page.Comments)
	out.Pagination = envelope.OffsetPagination{
		StartAt:    page.StartAt,
		MaxResults: page.MaxResults,
		Total:      page.Total,
		Returned:   len(page.Comments),
	}
	return out
}

// commentViews narrows a page's wire comments to their CLI-shaped view —
// shared by this command's own page (buildCommentListOutput above) and
// issue view's --comments N inline preview (view.go's runView), so the
// wire-to-view narrowing exists in exactly one place.
func commentViews(comments []jira.Comment) []envelope.CommentView {
	out := make([]envelope.CommentView, len(comments))
	for i, c := range comments {
		author := c.Author.Value
		out[i] = envelope.CommentView{
			ID:      c.ID.Value,
			Created: c.Created.Value,
			Updated: c.Updated.Value,
			Body:    c.Body.Value,
			Author: envelope.AuthorRef{
				AccountID:   author.AccountID.Value,
				DisplayName: author.DisplayName.Value,
			},
		}
	}
	return out
}

// renderCommentList writes the human view: a "Comments" section, then
// per comment an author/time header and its rendered ADF body, blank
// lines between entries, and — when this page is not the whole thread —
// a closing hint naming how to see more. A thread with no comments at
// all gets one plain line rather than an empty section, so "nothing
// here" is never silent.
//
// Field to sink: the author's display name is a repeated identity
// across the thread, so its color is chosen the same way cli.Entity
// chooses one for assignee/reporter — but applied to the WHOLE header
// (author + " • " + relative time) via cli.EntityAs, not just the name.
// v.Section hands the header straight to clog's Divider, which
// unconditionally bolds whatever title it is given; coloring only the
// name and concatenating the plain time would leave the name's own
// self-closing color span reset mid-title, silently dropping the bold
// from everything after it (a real, byte-verified regression — see
// cli.EntityAs's own doc comment). Building one plain header string and
// coloring it in a single Render call emits exactly one reset, at the
// very end, so nothing nests. The timestamp is still sanitized with
// cli.Text before cli.Time, because cli.Time's own unparsable-input
// fallback returns its argument AS GIVEN — a hostile or malformed
// `created` value must not reach the terminal unsanitized through that
// path, independently of the header-level sanitization EntityAs applies
// to the assembled string. The body is Jira ADF, walked and sanitized by
// the shared renderBody/command.RenderADF (internal/cli/issue/render.go)
// exactly as renderView's description is.
func renderCommentList(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueCommentListOutput)
	if !ok {
		return fmt.Errorf("issue.comment.list: human renderer received %T, want envelope.IssueCommentListOutput", env.Data)
	}

	if out.WebURL != "" {
		// --web's own short-circuit: runCommentListWeb never fetched the
		// thread, so Pagination.Total is genuinely 0 here for a reason
		// that has nothing to do with an empty thread — check this
		// FIRST, before the ordinary "No comments." fallback below could
		// misread it as one.
		return renderWebURL(v, out.WebURL)
	}

	if out.Pagination.Total == 0 && !out.Truncated {
		v.Line(cli.Text("No comments."))
		return nil
	}

	// Each comment's own author • time IS its section rule — no outer
	// "Comments" header, because this command's whole output is comments,
	// so the label would only restate the verb. This is deliberately NOT
	// what issue view's inline --comments preview does: there the comments
	// sit among other sections (fields, description), so a single
	// "Comments" rule earns its place as the thing that separates them.
	// The two surfaces share the body renderer, not the header shape.
	for _, c := range out.Comments {
		created := cli.Text(c.Created)
		header := c.Author.DisplayName + " • " + cli.Time(created)
		v.Section(cli.EntityAs(c.Author.DisplayName, header))
		if body := renderBody(v, c.Body); body != "" {
			v.Block(body)
		}
		v.Line()
	}

	// "More to see" is StartAt+Returned < Total, NOT Returned < Total: on
	// any page past the first, Returned alone answers "is this page
	// short of a full load" rather than "is this the end of the thread"
	// — a last page of 5 comments (--start 40 --limit 50 of a 45-total)
	// has Returned(5) < Total(45) despite 40+5 already being the end.
	//
	// Built from cli.Number and static CLI-authored prose only — no
	// Jira-authored content reaches this line, so unlike the per-comment
	// header above it, there is no cli.Text sanitizing sink to run
	// afterward (mirroring nextPageHint's own identical reasoning,
	// internal/cli/issue/list.go): wrapping the ALREADY-colored result in
	// cli.Text would strip cli.Number's own SGR bytes right back out,
	// since sanitize() runs every string it is given through
	// x/ansi.Strip.
	if out.Truncated || out.Pagination.StartAt+out.Pagination.Returned < out.Pagination.Total {
		v.Line(commentPageHint(out))
	}
	return nil
}

// commentPageHint words the "there is more" line, and the reason it is
// its own function is that --all's own cap needs a DIFFERENT sentence
// from an ordinary short page.
//
// An ordinary page is short because the caller asked for one page; the
// action is --start/--limit, which is what the line has always said. A
// capped drain is short because this command stopped a walk the caller
// asked to run to completion, and saying "use --start/--limit to see
// more" there would report a deliberate cap as though it were the
// caller's own paging choice — the same dishonesty issue list's own
// nextPageHint avoids by naming the cap outright.
//
// Both branches are built from cli.Number and static CLI-authored prose
// only, so — as in the function above — there is no cli.Text sink to run
// afterward: wrapping the already-colored result would strip cli.Number's
// own SGR bytes right back out.
func commentPageHint(out envelope.IssueCommentListOutput) string {
	shown, total := cli.Number(out.Pagination.Returned), cli.Number(out.Pagination.Total)
	if out.TruncatedReason == envelope.TruncatedReasonReadFailed || out.TruncatedReason == envelope.TruncatedReasonResponseInvalid {
		return fmt.Sprintf("Comment read incomplete. Showing %s of %s; inspect the thread before continuing with `--start %d`",
			shown, total, out.Pagination.StartAt+out.Pagination.Returned)
	}
	if out.Truncated {
		return fmt.Sprintf("Showing %s of %s — stopped after `--all`'s %s; continue with `--start %d`",
			shown, total, commentCapDescription(out.TruncatedReason), out.Pagination.Returned)
	}
	return fmt.Sprintf("Showing %s of %s — use --start/--limit to see more", shown, total)
}

// commentCapDescription names the cap that actually stopped the walk,
// mirroring maxAllCommentPages/maxAllComments so the number in the
// message can never drift from the constant that produced it —
// truncatedCapDescription's own shape and reasoning, one command over
// (list.go).
func commentCapDescription(reason string) string {
	switch reason {
	case envelope.TruncatedReasonMaxPages:
		return fmt.Sprintf("page cap (%s pages)", cli.Number(maxAllCommentPages))
	case envelope.TruncatedReasonMaxComments:
		return fmt.Sprintf("comment cap (%s comments)", cli.Number(maxAllComments))
	default:
		return "safety cap"
	}
}
