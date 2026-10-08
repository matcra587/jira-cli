package issue

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gechr/x/ansi"
	"github.com/gechr/x/human"
	xslices "github.com/gechr/x/slices"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// newView returns `jira issue view`.
func newView(deps Deps) *cobra.Command {
	var (
		fields, expand []string
		comments       int
		// cmd is the built *cobra.Command, captured by Flags below so Run
		// can ask it c.Flags().Changed("comments") — whether --comments was
		// explicitly typed, as opposed to just holding its zero-value
		// default. Flags runs once, at command-tree construction, before
		// any parsing happens, so Changed cannot be read there; it can only
		// be read from within Run, once cobra has parsed this invocation's
		// actual flags. Same closure-capture shape as fields/comments/
		// preview below, just capturing the command object rather than a
		// flag value.
		cmd *cobra.Command
	)

	// preview carries runView's --comments/--fields fetch result from Run
	// to Human — deliberately NOT part of envelope.IssueViewOutput. The
	// machine envelope must not gain a structured comments block for
	// this slice (comments already ride the existing passthrough at
	// data.issue.fields.comment); --comments/--fields are human-display
	// controls only, so their fetched preview must never serialize.
	//
	// This is the same command-local closure state as the fields/comments
	// flag vars below — one variable, scoped to this command, written
	// during one Execute and read within it. The only difference is the
	// writer: the flag parser fills the flag vars, Run fills this. Safe
	// for the same reason theirs is — a single CLI Execute runs Run to
	// completion, then Human, once, in the same goroutine (command.go's
	// RunE). Its zero value (requested 0, totalKnown false, uncapped
	// false) renders nothing, so even a Human that somehow ran without Run
	// is benign.
	var preview commentsPreview

	// site carries the Jira base URL runView's own successful client
	// construction resolved, from Run to Human — same shape as preview
	// above, for the same reason: Human has no deps and cannot construct
	// a client itself, but the glance line's key (renderView, via
	// cli.IssueKey) needs the site to build its browse link. Left "" on
	// any failure (Run's own error already carries the real failure;
	// Human never runs in that case) or when styling turns out to be off
	// (cli.IssueKey degrades to the plain key regardless of site).
	var site string

	return command.MustNew(command.Spec[envelope.IssueViewOutput]{
		Op:          "issue.view",
		Identity:    true,
		Passthrough: true,
		Use:         "view ISSUE_KEY",
		Short:       "Show one issue",
		Long: "Show an issue's identity and fields.\n\n" +
			"The human view is the scannable answer to \"what is this ticket\": " +
			"key, summary and status on one line, then the who/what/when, then " +
			"the fields. Machine modes carry the full field set — including a " +
			"tenant's custom fields, which ride through verbatim rather than " +
			"being dropped for lacking a typed field.\n\n" +
			"`--fields` narrows what Jira returns; without it the default set " +
			"comes back.\n\n" +
			"`--comments` controls the human view's inline comment preview " +
			"(machine modes always carry the full field set the fetch " +
			"returned): 1 (the default) renders the single latest comment " +
			"inline; N>0 renders the latest N; `--comments 0` shows a count " +
			"hint naming `jira issue comment list` instead — the previous " +
			"default's behavior, still reachable. The latest N is served from " +
			"the issue response's own embedded comment page, with no second " +
			"fetch, whenever that page demonstrably carries the whole thread; " +
			"otherwise it falls back to a dedicated fetch, exactly as an " +
			"explicit --comments N always has.\n\n" +
			"Asking `--fields` for the comment field asks for the comments " +
			"themselves (matching `acli`'s convention): the human view then " +
			"renders every comment the response's own embedded page carried, " +
			"newest first, with no count hint — no second fetch, since the " +
			"field was already in the response. Naming `comment` or its plural " +
			"`comments` counts, " +
			"case-insensitively; so does Jira's own `*all` wildcard (`*navigable` " +
			"does not — Jira's navigable set excludes comments); a minus-" +
			"exclusion (`-comment` or `-comments`) always suppresses the ask, " +
			"even alongside a wildcard, regardless of order. A \"Showing N of " +
			"TOTAL\" banner still appears if the embedded page itself fell short " +
			"of the issue's real total — this path never makes a second fetch to " +
			"complete it; the banner names `jira issue comment list` instead. An " +
			"explicit `--comments N` (even `--comments 0`) always wins over a " +
			"`--fields` comment ask: a cap you typed beats an ask you only " +
			"implied — this holds even though the default is no longer 0, since " +
			"the default is never explicit. Neither --comments N's own fetch " +
			"nor `--fields` comment's own decode happens for a non-human " +
			"render — --output=json/compact skip both entirely, since they " +
			"exist to feed a preview those modes never draw.\n\n" +
			"Native metadata is retained at .issue.details. The default expansion preset is renderedFields,names,schema,transitions,operations,editmeta; narrowed --fields uses renderedFields,names,schema. --expand replaces that preset, and --expand= disables it.",
		Example: "# the issue, with its latest comment inline\n" +
			"$ jira issue view KEY-123\n\n" +
			"# the count hint only, no comment body\n" +
			"$ jira issue view KEY-123 --comments 0\n\n" +
			"# the latest 3 comments, inline\n" +
			"$ jira issue view KEY-123 --comments 3\n\n" +
			"# every comment the response carried, in one fetch\n" +
			"$ jira issue view KEY-123 --fields comments\n\n" +
			"# just the fields you want, as json\n" +
			"$ jira issue view KEY-123 --fields summary,status --output=json\n\n" +
			"# pull one value out (--jq forces json; no --output needed)\n" +
			"$ jira issue view KEY-123 --jq '.data.issue.fields.status.name'",
		Args: command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddStringSliceVar(c, &fields, "fields", nil,
				"Restrict the fields Jira returns (comma-separated); naming "+
					"'comment'/'comments' or Jira's '*all' wildcard renders every "+
					"comment in the response ('-comment'/'-comments' always "+
					"suppresses that, even alongside a wildcard)",
				command.FlagOpts{Terse: "Fields", Placeholder: "list", Complete: "predictor=cachefield,comma"})
			command.AddStringSliceVar(c, &expand, "expand", nil,
				"Native issue expansions (comma-separated); replaces the preset; --expand= disables them",
				command.FlagOpts{Terse: "Expansions", Placeholder: "list", Enum: strings.Split(command.IssueExpansionValues, ",")})
			command.AddIntVar(c, &comments, "comments", 1,
				"Show the latest N comments inline in the human view (1: the "+
					"default; 0: a count hint only; an explicit value always "+
					"wins over --fields naming comment)",
				command.FlagOpts{Terse: "Comments", Placeholder: "n"})
		},
		Validate: func() error {
			if err := command.ValidateIssueExpansions(expand); err != nil {
				return err
			}
			if isWeb(cmd) && cmd.Flags().Changed("expand") {
				e := cli.NewInputError(cli.CodeFlagNotApplicable, "--expand requires an issue read; --web opens its URL")
				e.Field = "expand"
				return e
			}
			return nil
		},
		Human: func(v *output.View, env envelope.Envelope) error {
			return renderView(v, env, preview, site)
		},
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueViewOutput, error) {
			key := inv.Args[0]
			if isWeb(cmd) {
				// --web's own short-circuit: the browse URL is derivable
				// from key alone, so this skips runView (and its GetIssue
				// call) entirely — see runViewWeb's own doc comment for
				// why that is the whole point. execOpener is the
				// production browserOpener; a test calls runViewWeb
				// directly with a fake one instead, so nothing here ever
				// spawns a real browser under `go test`.
				return runViewWeb(ctx, deps, key, execOpener)
			}
			opts := viewOpts{
				n: comments,
				// c.Flags().Changed, NOT commentsN > 0: --comments 0 is a
				// legitimate explicit "just the hint", indistinguishable
				// from "never asked" by value alone.
				explicit:       cmd.Flags().Changed("comments"),
				expand:         expand,
				expandExplicit: cmd.Flags().Changed("expand"),
			}
			out, prev, err := runView(ctx, deps, key, fields, opts)
			preview = prev
			if err == nil {
				// A second deps.JiraClient() call, not a second
				// resolution: root/deps.go wires it as
				// sync.OnceValues(newJiraClient), so every call after the
				// first — including runView's own, just above — returns
				// the SAME cached client at the cost of a mutex check.
				// Reading the site back out this way, rather than
				// threading a return value through runView's signature,
				// keeps that signature (and its ~20 existing direct
				// callers in view_test.go) untouched for a value only
				// Human needs.
				if client, cerr := deps.JiraClient(); cerr == nil {
					site = client.BaseURL()
				}
			}
			return out, err
		},
	})
}

// commentsPreview carries issue view's comment preview from Run to Human
// (see newView's preview variable for why this rides a closed-over
// variable rather than the envelope). requested is --comments's flag
// value as given; total/totalKnown report the issue's comment count when
// it could be learned at all; latest holds the comments to render —
// the latest N when requested > 0, or every comment the response
// carried when uncapped is true. uncapped marks the "--fields named
// comment, and no explicit --comments overrode it" case: render loses
// the requested-N logic entirely (no cap, no "Showing N of TOTAL"
// banner) when it is set.
type commentsPreview struct {
	requested  int
	total      int
	totalKnown bool
	latest     []envelope.CommentView
	uncapped   bool
	// asked marks a preview the caller REQUESTED rather than one they
	// merely got by default — an explicit --comments (any value, per
	// cmd.Flags().Changed) or --fields naming the comment field. It only
	// governs the zero-comment case: an explicit ask that finds nothing
	// still gets an answer ("No comments."), because silence in reply to
	// a typed question is indistinguishable from the flag being ignored,
	// the fetch failing, or the build not supporting it. The default's
	// own silence stays silent — nobody asked.
	asked bool
}

// viewOpts carries issue view's comment and native expansion inputs.
// Comment values stay together
// because the precedence rule couples them: an explicit --comments always
// wins over --fields naming comment. explicit needs its own bit rather
// than inferring "asked" from n > 0, because --comments 0 is a legitimate
// explicit choice (the count hint, same as the unset default) that must
// still beat --fields comment — n alone cannot tell that apart from
// --comments never having been typed at all.
type viewOpts struct {
	n              int
	explicit       bool
	expand         []string
	expandExplicit bool
}

// runView fetches key (narrowed to fields, when set) and assembles the
// issue payload — the Run body proper, pulled out of the command.Spec
// closure above so it is directly testable against a fake
// core.JiraClient, matching how the auth package's status/login extract
// their own Run bodies to a named function.
//
// opts.n is validated ONCE, here, before it can reach anything that
// treats it as a slice bound or a network maxResults argument: a
// negative --comments (`--comments=-1`) used to reach
// `sorted[:opts.n]` unchecked and panic with a raw Go stack trace —
// reproduced live against a real tenant. A negative comment count is a
// user mistake, not a value to silently reinterpret (clamping it to 0
// would answer a question nobody asked), so it is rejected with a
// classified *cli.InputError instead: flag_value_invalid, the same code
// root's own flag-parse-failure path uses for a value a flag's type
// rejects, naming the flag and the offending value so both a human and
// an agent can see exactly what was wrong. sorted[:opts.n] below is
// ALSO bounds-safe on its own terms (min(opts.n, len(sorted))) even
// though this guard means a negative can never reach it — belt and
// braces, because this exact class of bug just shipped once already.
//
// The comment preview follows one precedence rule: --fields asking for
// Jira's comment field (see fieldsRequestComment for exactly which
// tokens count) asks for every comment the response carried, uncapped,
// newest first — read straight from the response's OWN embedded
// fields.comment (see commentPageFromFields) and locally re-sorted (see
// sortCommentsDescending), no second fetch, since it is already in hand.
// An explicit opts.explicit (c.Flags().Changed("comments"), not opts.n >
// 0 — see viewOpts) always overrides that and falls through to the
// ordinary --comments N behavior below.
//
// --comments 0 (the count-hint-only mode, reachable only by typing it
// explicitly now that the flag's own default is 1) reads just the total
// from the same embedded fields.comment via the cheap commentTotalFromFields
// decode — no second call, and no need to parse every embedded comment
// body just for a count.
//
// --comments N>0 (the default, N==1) tries the embedded page FIRST: when
// it demonstrably carries the whole thread — its own comment count
// equals its own total, checked below — the latest N is read locally,
// no second call, which is what keeps the single most common human
// invocation (a bare `issue view`) down to the one GetIssue round trip.
// The embedded page's own delivery order is not trusted for "latest":
// sortCommentsDescending re-sorts it by `created` regardless (verified
// against a live tenant to come back oldest-first — the opposite of
// GetComments' own order). Only when the embedded page is missing,
// undecodable, or itself short of the issue's real total does this fall
// back to a fresh GetComments(ctx,key,0,N) fetch, which orders
// newest-first reliably on its own.
//
// That N>0 resolution — local sort or fallback fetch alike, AND the
// uncapped --fields comment branch above it, which shares the same
// isHuman resolved once below rather than each re-deriving it — is
// skipped entirely when the resolved output mode is not human, because
// it exists only to feed a render that will not run: the CPU cost of
// decoding and re-sorting every embedded comment body would be pure
// waste for a caller who gets the same data raw through the passthrough
// envelope anyway. A comment-FETCH failure (the dedicated GetComments
// call below, reachable only once isHuman is already true) is a
// separate question from whether to attempt it at all, and follows its
// own explicit-vs-default rule — see degradeOrFail.
func runView(ctx context.Context, deps Deps, key string, fields []string, opts viewOpts) (envelope.IssueViewOutput, commentsPreview, error) {
	var zero envelope.IssueViewOutput

	if opts.n < 0 {
		e := cli.NewInputError(cli.CodeFlagValueInvalid,
			fmt.Sprintf("--comments expects a comment count of 0 or more, got %d", opts.n))
		e.Field = "comments"
		return zero, commentsPreview{}, e
	}

	client, err := deps.JiraClient()
	if err != nil {
		return zero, commentsPreview{}, err
	}
	expand := opts.expand
	if !opts.expandExplicit {
		expand = strings.Split(command.IssueViewExpansions, ",")
		if len(fields) > 0 {
			expand = strings.Split(command.IssueFieldExpansions, ",")
		}
	}
	read, err := command.Spin(ctx, "Loading "+key, func(ctx context.Context) (core.IssueRead, error) {
		return client.GetIssue(ctx, key, requestFields(fields), expand...)
	})
	if err != nil {
		return zero, commentsPreview{}, err
	}
	if read.Issue == nil {
		// GetIssue's own contract (internal/adapters/jira/client.go) is
		// a non-nil Issue whenever it returns a nil error. Trusting
		// that silently and calling buildOutput anyway would map a
		// violation of it to an EMPTY IssueViewOutput — envelope.OK,
		// every field zero — which reads as a real (if oddly bare)
		// issue rather than the failure it actually is. Fail loudly
		// instead of guessing what an empty issue would mean.
		return zero, commentsPreview{}, fmt.Errorf("issue.view: empty issue response for %q", key)
	}
	out := buildOutput(read)

	// format comes from the SAME resolution root's PersistentPreRunE
	// already ran for this invocation — cli.WithFormat seeds it before
	// Run is ever called, so this reads the one already-settled answer
	// (aliases folded in, --jq's forced json included) rather than
	// re-deriving TTY/agent detection here. formatOK is false only when
	// that resolution never ran (a wiring bug elsewhere, or a unit test
	// driving runView directly with a bare context), in which case there
	// is no human renderer to feed either, so isHuman stays false and
	// every comment-preview path below is skipped the same way as an
	// explicit non-human mode. Resolved once, reused by BOTH the
	// uncapped --fields comment branch immediately below and the
	// ordinary --comments N>0 path further down, so the two gates can
	// never drift the way they had — the uncapped branch used to decode
	// and sort the whole embedded comment page even under --output=json,
	// discarding the work unread.
	format, formatOK := cli.FormatFromContext(ctx)
	isHuman := formatOK && format == output.FormatHuman

	if !opts.explicit && fieldsRequestComment(fields) {
		if !isHuman {
			return out, commentsPreview{}, nil
		}
		// asked: naming the comment field IS a request for comments, so a
		// thread that turns out to be empty still gets told so.
		preview := commentsPreview{uncapped: true, asked: true}
		if page, ok := commentPageFromFields(out.Issue.Fields); ok {
			preview.latest = sortCommentsDescending(page.Comments)
			preview.total, preview.totalKnown = page.Total, true
		}
		return out, preview, nil
	}

	preview := commentsPreview{requested: opts.n, asked: opts.explicit}
	if total, ok := commentTotalFromFields(out.Issue.Fields); ok {
		preview.total, preview.totalKnown = total, true
	}

	if opts.n == 0 {
		// The count-hint-only mode: the cheap total-only read above is
		// everything it needs. No fetch, and no reason to decode the
		// embedded page's own comment bodies just to throw them away.
		return out, preview, nil
	}

	if !isHuman {
		return out, preview, nil
	}

	if page, ok := commentPageFromFields(out.Issue.Fields); ok && page.Total == len(page.Comments) {
		// The embedded page's own comment count matches its own total:
		// it demonstrably carries the whole thread, so "the latest N"
		// can be resolved locally — no second network call for the
		// default, most common human path.
		sorted := sortCommentsDescending(page.Comments)
		// min, not a bare `sorted[:opts.n]`: opts.n cannot be negative by
		// the time it reaches here (validated at the top of this
		// function), but a slice truncation that CAN panic on its own
		// terms is a defect independent of what currently guards its one
		// caller — belt and braces, since this exact class of bug is
		// what this whole guard exists to stop.
		sorted = sorted[:min(opts.n, len(sorted))]
		preview.latest = sorted
		preview.total, preview.totalKnown = page.Total, true
		return out, preview, nil
	}

	// Embedded page missing, undecodable, or itself short of the
	// issue's real total: fall back to a dedicated fetch exactly as
	// --comments N always has.
	commentRead, err := command.Spin(ctx, "Loading comments for "+key, func(ctx context.Context) (core.CommentRead, error) {
		// CommentOrderDefault: this preview has no order flag of its own
		// and never did — expressing no preference is what keeps it on
		// the newest-first reading it has always shown, resolved once in
		// the adapter rather than restated here.
		return client.GetComments(ctx, key, 0, opts.n, core.CommentOrderDefault)
	})
	if err != nil {
		return degradeOrFail(opts, out, preview, err)
	}
	if commentRead.Page == nil {
		// Mirrors runCommentList's own guard (comment.go): GetComments's
		// contract is a non-nil Page on every nil-error return.
		return degradeOrFail(opts, out, preview, fmt.Errorf("issue.view: empty comment page response for %q", key))
	}
	// The freshly-fetched page's own Total supersedes whatever the
	// issue-embedded field above may have carried — same moment, same
	// call, and the number the "Showing N of TOTAL" banner below must
	// agree with.
	preview.latest = commentViews(commentRead.Page.Comments)
	preview.total, preview.totalKnown = commentRead.Page.Total, true

	return out, preview, nil
}

// runViewWeb answers `issue view KEY --web`: the issue's browse URL, and
// no GetIssue call. Resolving the URL, opening it on a human surface,
// and failing on a key that cannot become one are all
// openIssueBrowseTarget's (web.go) — shared with runCommentListWeb
// rather than written out per command, and documented there. What is
// left here is this command's own answer shape: the browse URL beside a
// bare IssueRef, so a caller reading data.issue.key still gets the key
// it asked about even though nothing was fetched about it.
func runViewWeb(ctx context.Context, deps Deps, key string, open browserOpener) (envelope.IssueViewOutput, error) {
	var zero envelope.IssueViewOutput
	target, err := openIssueBrowseTarget(ctx, deps, key, open)
	if err != nil {
		return zero, err
	}
	return envelope.IssueViewOutput{
		Issue:   envelope.IssueView{IssueRef: envelope.IssueRef{Key: key}},
		WebLink: envelope.WebLink{WebURL: target},
	}, nil
}

// degradeOrFail resolves a comment-fetch failure in runView's fallback
// path (the dedicated GetComments call, reached only when the embedded
// page could not answer "the latest N" locally) against who asked for
// it. The issue itself already fetched successfully by this point — out
// is real, complete data — so a failure here is a display aid breaking,
// not the primary command breaking, and the two outcomes follow the same
// explicit-vs-default rule commentsPreview.asked already applies to the
// zero-comment case:
//
//   - opts.explicit (an explicit --comments N reached this fetch): the
//     caller asked, so they get the truth. Unchanged from before this
//     function existed — the whole request fails, err and all.
//   - the default (nobody asked; a bare `issue view` only reached this
//     fetch because a long thread's embedded page fell short of the
//     issue's own total): a hiccup in a display aid must not take down
//     the issue the caller actually asked to see. out is returned as a
//     success, and the preview degrades to the SAME shape --comments 0
//     renders — requested reset to 0, latest left empty — which is
//     already exactly "a count-hint line when the total is known (it may
//     already have been read from the issue's own embedded field, before
//     this fetch was ever attempted), or nothing at all when it is not."
//     Never "No comments.": that would state a count (zero) this
//     function does not actually know. If preview.total does happen to
//     already be a genuine, known zero (the embedded page's own total
//     decoded even though its comment array itself did not, an edge the
//     fast path above cannot always rule out), the degraded shape still
//     answers correctly: renderCommentsPreview's totalKnown/total==0
//     case takes over and stays silent, exactly as it would for any
//     other known-zero, nobody-asked preview.
func degradeOrFail(opts viewOpts, out envelope.IssueViewOutput, preview commentsPreview, err error) (envelope.IssueViewOutput, commentsPreview, error) {
	if opts.explicit {
		var zero envelope.IssueViewOutput
		return zero, commentsPreview{}, err
	}
	return out, commentsPreview{total: preview.total, totalKnown: preview.totalKnown}, nil
}

// commentField is the literal field name Jira's own field grammar uses
// for an issue's comment thread.
const commentField = "comment"

// commentFieldAlias is the plural jira-cli additionally accepts as a
// synonym for commentField — the natural way to type it, and what
// prompted this whole feature. Jira's own grammar has no such field, so
// anywhere this alias would reach the wire it must first be rewritten to
// commentField (see requestFields) — fieldsRequestComment below only
// decides what the ask MEANS for rendering, never what gets sent.
const commentFieldAlias = "comments"

// commentFieldWildcard is Jira's own wildcard for "every field" — it
// includes the comment field by definition, so it counts as an ask the
// same way naming it directly does. Jira's OTHER wildcard, "*navigable"
// ("every navigable field"), deliberately has no constant here: comment
// is excluded from Jira's navigable set, so it does not count, and
// fieldsRequestComment's switch below simply has no case for it —
// falling through to neither included nor excluded, same as any other
// field name that is not comment.
const commentFieldWildcard = "*all"

// fieldsRequestComment reports whether fields asks Jira for the comment
// field, matched the way Jira's own field grammar works: case-
// insensitively, trimmed, and token-based rather than substring — each
// element of fields is compared whole, never searched as a fragment of a
// larger token. Three shapes count as an ask: the field named directly
// ("comment"), its plural alias jira-cli additionally accepts
// ("comments" — see commentFieldAlias), or Jira's own "every field"
// wildcard ("*all", which includes comment by definition — *navigable
// does NOT count, since Jira's navigable set excludes comment).
//
// A minus-exclusion for the comment field — "-comment" or its plural
// "-comments" — always SUPPRESSES the ask, even alongside a wildcard
// that would otherwise include it, and wins regardless of which token
// comes first in the list: an explicit "don't fetch this" beats an
// implicit "fetch everything," the same precedence Jira's own field
// grammar gives an exclusion.
func fieldsRequestComment(fields []string) bool {
	var included, excluded bool
	for _, f := range fields {
		switch strings.ToLower(strings.TrimSpace(f)) {
		case commentField, commentFieldAlias, commentFieldWildcard:
			included = true
		case "-" + commentField, "-" + commentFieldAlias:
			excluded = true
		}
	}
	return included && !excluded
}

// requestFields rewrites fields for the wire request Jira actually
// receives: Jira's field grammar has no "comments" field, only the
// singular "comment", but fieldsRequestComment above accepts the plural
// as a synonym because it is what a caller naturally types. Sending it
// to Jira unrewritten would ask for a field Jira does not recognize —
// silently ignored, per Jira's own behavior for an unknown field name —
// leaving the response carrying no comment data at all despite
// fieldsRequestComment having just told the caller its ask was honored.
// This is the one place fields travels to GetIssue, so it is the one
// place this rewrite happens; every other token (including an exclusion
// like "-comments", which only ever matters to fieldsRequestComment's
// own local suppression, never to Jira) passes through unchanged.
func requestFields(fields []string) []string {
	if len(fields) == 0 {
		return fields
	}
	out := make([]string, len(fields))
	for i, f := range fields {
		if strings.EqualFold(strings.TrimSpace(f), commentFieldAlias) {
			out[i] = commentField
			continue
		}
		out[i] = f
	}
	return out
}

// commentTotalFromFields reads an issue's total comment count from its
// own embedded fields.comment — present in Jira's default GetIssue
// response (fields.comment.total, riding IssueFields.Extra since
// IssueFields declares no typed "comment" member) without a second
// network call. ok is false, not an error, when the fetched field set
// did not carry it (a caller's own --fields narrowed it away) or the
// embedded value did not parse as the expected page shape — either way
// the human hint simply stays silent rather than guessing a count.
//
// This is the cheap sibling of commentPageFromFields below: an explicit
// --comments 0 (the only way to reach this decode now that the flag's
// own default is 1) only ever needs the count, so it stays on this
// decode rather than paying to parse every embedded comment body it
// will never render.
func commentTotalFromFields(f jira.IssueFields) (int, bool) {
	raw, ok := f.Extra["comment"]
	if !ok {
		return 0, false
	}
	// Decode only the total, not jira.CommentPage: the full shape would run
	// every embedded comment through Comment.UnmarshalJSON (body ADF, author
	// Extra, ...) on the count-hint-only path, parsing content this function
	// does not read.
	var page struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return 0, false
	}
	return page.Total, true
}

// commentPageFromFields decodes an issue's full embedded fields.comment
// page — not just its total (see commentTotalFromFields above, kept as
// the cheap read for the count-hint-only path) — for the two cases that
// need the comments themselves without a second network call: --fields
// naming comment (runView's uncapped branch), and --comments N's own
// fast path, when the embedded page demonstrably carries the whole
// thread. ok is false on the same terms as commentTotalFromFields: the
// field was never fetched, or it did not parse as the expected page
// shape.
func commentPageFromFields(f jira.IssueFields) (jira.CommentPage, bool) {
	raw, ok := f.Extra["comment"]
	if !ok {
		return jira.CommentPage{}, false
	}
	var page jira.CommentPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return jira.CommentPage{}, false
	}
	return page, true
}

// sortCommentsDescending narrows comments to their CLI-shaped views and
// re-sorts them newest first by their own `created` timestamp — the
// shared sort behind issue view's embedded-page fast path (the default
// --comments N, capped by its caller) and its --fields comment uncapped
// read (every comment, same order, capped by nothing): both source the
// SAME embedded fields.comment page, verified against a live tenant to
// come back OLDEST-first — the opposite of GetComments' own
// newest-first order, and the opposite of what "latest" means — so
// neither surface may trust the page's own delivery order, and neither
// may assume that order stays oldest-first on every tenant or endpoint
// version either: the sort is unconditional, not a documented
// assumption about Jira's own behavior.
//
// created is parsed with cli.ParseTime — the same Jira-timestamp layout
// (with its RFC 3339 fallback) cli.TimeFrom already applies when
// rendering it, reused here rather than duplicated. A comment whose
// created fails to parse is neither dropped nor reordered ahead of a
// parseable one: slices.SortStableFunc sends every unparseable comment
// to the end, in their original relative order, keeping the sort
// deterministic without silently discarding data the render would
// still safely show (cli.Text/cli.Time render an unparsable value as
// given, they do not fail).
func sortCommentsDescending(comments []jira.Comment) []envelope.CommentView {
	views := commentViews(comments)
	type entry struct {
		view    envelope.CommentView
		created time.Time
		ok      bool
	}
	entries := xslices.Map(views, func(v envelope.CommentView) entry {
		created, ok := cli.ParseTime(v.Created)
		return entry{view: v, created: created, ok: ok}
	})
	slices.SortStableFunc(entries, func(a, b entry) int {
		if a.ok != b.ok {
			// A parseable created time always sorts ahead of an
			// unparseable one, regardless of the unparseable one's
			// original position — that is the "sort last" half of the
			// documented tie-break.
			if a.ok {
				return -1
			}
			return 1
		}
		if !a.ok {
			// Both unparseable: report them EQUAL so SortStableFunc
			// leaves their original relative order untouched — the
			// "preserve original relative order" half of the tie-break.
			return 0
		}
		// Newest first, so the comparison is reversed: b against a.
		return b.created.Compare(a.created)
	})
	return xslices.Map(entries, func(e entry) envelope.CommentView { return e.view })
}

// buildOutput assembles the payload from the wire bean: the identity
// object from the bean's key/id/self, and the fields verbatim — the
// curated members typed, the tenant's own carried in Extra.
//
// Callers must not pass a read whose Issue is nil: GetIssue's own
// contract is a non-nil Issue on every nil-error return (see
// internal/adapters/jira/client.go), so Run guards that case itself,
// before this is ever called, rather than letting the nil branch below
// quietly hand back a zero-valued IssueViewOutput that would render as
// an empty-but-successful issue.
func buildOutput(read core.IssueRead) envelope.IssueViewOutput {
	out := envelope.IssueViewOutput{}
	if b := read.Issue; b != nil {
		out.Issue = envelope.IssueViewFromBean(*b)
	}
	return out
}

// renderView writes the scannable human view: identity and status on one
// line, then the who/what/when as one aligned block, then labels, then
// the description body when the issue has one, then the comment preview
// — a count hint by default, the latest N inline when --comments asks
// for that, or every comment uncapped when --fields named comment (see
// commentsPreview and runView's precedence rule). The remaining richer
// sections (subtasks, linked issues) are still deferred — separate work
// per the design doc's issue-view reference bar section.
//
// Field to sink: the issue KEY is a plain identifier, never entity or
// semantic — but it is still Jira-sourced, so the glance line's own
// display copy still goes through cli.Text (H4 sanitization) before
// cli.IssueKey wraps it in a browse-URL hyperlink when styling and site
// allow it (see cli.IssueKey's own doc comment); the envelope's
// IssueRef.Key stays the raw wire value. Summary is Jira-authored text
// with no further meaning, so it goes through cli.Text. Status is
// semantic state colored by CATEGORY, never hashed — cli.Status, fed
// both the name and statusCategoryKey(f) (see that function's own doc
// comment for why the category, not the name, decides the color).
// Priority is semantic state the same way, still name-keyed —
// cli.Priority. Issue type is colored by cli.Type, fed both the name and
// its hierarchyLevel (see cli.Type's own doc comment for the structural-
// then-name-then-hash order it resolves a color in). Assignee, Reporter
// and each Label are repeated identities — cli.Entity, which gives the
// same name the same color wherever it recurs. Created/Updated have no
// row here yet, so cli.Time has nothing to wire into for this operation.
// Description is raw ADF (internal/jira.Nullable[json.RawMessage])
// parsed and walked through internal/adf.Render, styled by
// descriptionStyler below — see renderDescription.
//
// Every field is built with its sink already applied; an absent field
// (empty DisplayName, no Priority set) is not filtered here — Fields
// skips it, ANSI-wrapped or not, so this stays a plain list of what the
// row WOULD say rather than a second place that re-decides emptiness.
//
// preview is the comment preview runView resolved (--comments N, or
// --fields comment's uncapped read), carried from Run rather than
// through env.Data — see newView's preview variable and commentsPreview.
// site is the Jira base URL runView's own client resolved, carried the
// same way — see newView's site variable — for cli.IssueKey's browse
// link; "" (no client, or one that never resolved) degrades every use
// of it back to the plain key, which is cli.IssueKey's own job to
// handle, not this function's.
func renderView(v *output.View, env envelope.Envelope, preview commentsPreview, site string) error {
	out, ok := env.Data.(envelope.IssueViewOutput)
	if !ok {
		return fmt.Errorf("issue.view: human renderer received %T, want envelope.IssueViewOutput", env.Data)
	}
	if out.WebURL != "" {
		// --web's own short-circuit: runViewWeb never fetched the issue,
		// so out.Issue.Fields is entirely zero — the ordinary rendering
		// below would draw a glance line with a blank summary and status
		// rather than the one thing this invocation actually resolved.
		return renderWebURL(v, out.WebURL)
	}
	f := out.Issue.Fields

	// Line 1: key, summary, status — the glance answer, kept to one line
	// by fitGlanceSummary so a long summary never runs off the right edge.
	// plainKey is sanitized for display but deliberately never linked: it
	// feeds the width budget below (fitGlanceSummary) and the comment
	// preview's own prose hints (renderCommentsPreview), where a
	// hyperlink's escape bytes have no business sitting inside a sentence
	// meant to be copy-pasted as a shell command — see cli.IssueKey's own
	// doc comment on that scope line. key is the glance line's own
	// standalone field value, which DOES get the hyperlink treatment.
	// Neither is exempt from H4 just because a key usually looks like
	// PROJ-123: nothing stops a malformed or hostile response from
	// putting control bytes there instead, and out.Issue.Key (plus the
	// envelope's own IssueRef.Key set in buildOutput) stays the raw wire
	// value regardless — only these display copies are sanitized.
	plainKey := cli.Text(out.Issue.Key)
	key := cli.IssueKey(site, out.Issue.Key)
	status := cli.Status(val(f.Status).Name.Value, statusCategoryKey(f))
	summary := fitGlanceSummary(cli.Text(val(f.Summary)), plainKey, status, v.Output().Width())
	v.Line(key, summary, status)

	// The who/what/when strip, as one aligned block.
	fields := []output.Field{
		{Label: "Type", Value: cli.Type(val(f.Issuetype).Name.Value, val(f.Issuetype).HierarchyLevel.Value)},
		{Label: "Priority", Value: cli.PriorityWithIcon(val(f.Priority).Name.Value)},
		// Unassigned is a real answer, not a missing one, so it says so
		// rather than dropping the row: an absent Assignee line reads as
		// "this view did not fetch that", which is a different thing.
		{Label: "Assignee", Value: cli.OrDash(cli.Entity(val(f.Assignee).DisplayName.Value))},
		{Label: "Reporter", Value: cli.Entity(val(f.Reporter).DisplayName.Value)},
	}
	if labels := f.Labels.Value; len(labels) > 0 {
		// Each label gets its OWN entity color, not the joined string
		// colored as one identity — otherwise an issue's whole label set
		// would share one arbitrary color instead of each label reading
		// consistently wherever it recurs.
		colored := xslices.Map(labels, cli.Label)
		fields = append(fields, output.Field{Label: "Labels", Value: strings.Join(colored, ", ")})
	}
	v.Fields(fields...)

	// Set && !Null && non-empty: an absent or explicit-null description
	// (both legal — a fresh issue may never have had one) skips the
	// section entirely rather than printing an empty "Description"
	// label with nothing under it.
	if d := f.Description; d.Set && !d.Null && len(d.Value) > 0 {
		renderDescription(v, d.Value)
	}

	renderCommentsPreview(v, plainKey, preview)
	return nil
}

// renderCommentsPreview renders the issue view's comment preview — three
// shapes, chosen by the precedence runView already resolved into preview,
// plus one silence rule that overrides all three:
//
//  0. A KNOWN-ZERO total (preview.totalKnown && preview.total == 0):
//     nothing at all, regardless of requested or uncapped — no "Comments"
//     header, no "No comments." line, no banner. An issue that genuinely
//     has no comments is the quiet, honest outcome whether this preview
//     was reached by the default N, an explicit --comments N, or
//     --fields comment's uncapped read: there is nothing to separate
//     from the rest of the view with a header, so none is drawn.
//  1. --comments 0, explicit (requested == 0 and !uncapped — reachable
//     only by typing it now that the flag's own default is 1): nothing
//     at all unless the issue is known to have comments, in which case
//     one hint line names the count (singular "comment" at exactly 1)
//     and points at the dedicated paginated verb — commentCountHint
//     below. A total that could not be learned at all (totalKnown false
//     — see commentTotalFromFields) is treated the same as zero: silent,
//     never an error. (A known-zero total takes case 0 above instead,
//     which renders identically here — --comments 0's own hint is
//     already silent for it — so this case does not need to repeat it.)
//  2. --comments N>0, default or explicit (with or without --fields
//     comment also present — an explicit --comments always wins, see
//     runView): a "Comments" section header, the latest N each as an
//     author • relative-time line with the shared ADF body renderer
//     below it, then a "Showing N of TOTAL" banner when the total is
//     known to exceed what is shown (simpler than renderCommentList's
//     own StartAt+Returned<Total formula because this preview always
//     starts at the newest comment). At the default N==1, an issue with
//     exactly one comment renders that one comment and no banner (1 is
//     not < 1); an issue with zero comments takes case 0 instead.
//  3. --fields asked for comment (see fieldsRequestComment) and
//     --comments was never explicitly set (preview.uncapped): the same
//     "Comments" section and per-comment rendering as case 2, over every
//     comment the response's own embedded page carried, newest first —
//     no second fetch. This is uncapped by CHOICE, not by construction:
//     Jira's own embedded page can itself be capped for a large thread
//     (unproven but unfalsified — every probed issue had few enough
//     comments that maxResults never bound the page), so the SAME
//     "Showing N of TOTAL" banner as case 2 still appears whenever the
//     page's own total says there is more than it carried. What
//     uncapped changes is only that no SECOND fetch ever happens to
//     "complete" the set — the banner points at the paginated verb,
//     which exists for exactly that.
//
// Cases 2 and 3 both fall back to one line when preview.latest came back
// empty despite NOT being a known-zero total (case 0 already handles
// that) — a decode failure, or an empty embedded array Jira sent
// alongside a positive total: this falls back to the SAME count-hint
// line case 1 uses, naming the paginated verb, since the total itself
// says there is something to see even though nothing rendered. The
// "Comments" header and the quiet per-comment line (rather than
// promoting each to its own section rule) are deliberate in both cases 2
// and 3: unlike issue comment list, whose whole output IS comments, here
// the comments sit alongside other sections, so one header separates
// them. The two surfaces share the body renderer, not the header shape.
//
// plainKey is deliberately the SANITIZED-but-never-linked key (renderView
// passes its own plainKey, not the hyperlinked one v.Line's glance
// row uses): every use here embeds it inside a sentence — a "jira issue
// comment list KEY" hint, a "Showing N of TOTAL" banner — and cli.
// IssueKey's own scope excludes exactly that (see its doc comment); a
// hyperlink's escape bytes have no business riding inside a line meant to
// be read, or copy-pasted as a shell command, as plain prose.
func renderCommentsPreview(v *output.View, plainKey string, preview commentsPreview) {
	if preview.totalKnown && preview.total == 0 {
		// A thread that is genuinely empty. Silent when nobody asked (the
		// default preview should not manufacture a section for an issue
		// with no comments), but an explicit ask — --comments, or --fields
		// naming the comment field — gets an answer: silence in reply to a
		// typed question reads as the flag being ignored rather than as
		// "there are none".
		if preview.asked {
			v.Line()
			v.Line(cli.Text("No comments."))
		}
		return
	}

	if !preview.uncapped && preview.requested <= 0 {
		if !preview.totalKnown {
			return
		}
		v.Line()
		v.Line(commentCountHint(preview.total, plainKey))
		return
	}

	v.Line()
	v.Section("Comments")
	if len(preview.latest) == 0 {
		if preview.totalKnown && preview.total > 0 {
			v.Line(commentCountHint(preview.total, plainKey))
			return
		}
		v.Line(cli.Text("No comments."))
		return
	}

	for _, c := range preview.latest {
		created := cli.Text(c.Created)
		header := cli.Entity(c.Author.DisplayName) + " • " + cli.Time(created)
		v.Line(header)
		if body := renderBody(v, c.Body); body != "" {
			v.Block(body)
		}
		v.Line()
	}

	// cli.Number colors both counts; plainKey is already sanitized by
	// renderView's own cli.Text(out.Issue.Key) call before it ever
	// reaches this function (see plainKey's doc comment there), and the
	// rest of this line is static CLI-authored prose — so, like
	// nextPageHint (internal/cli/issue/list.go) and comment.go's own
	// "Showing N of TOTAL" banner, there is no cli.Text sanitizing sink
	// to run over the ASSEMBLED string: doing so would strip cli.
	// Number's own SGR bytes right back out (sanitize() runs everything
	// through x/ansi.Strip).
	if preview.totalKnown && len(preview.latest) < preview.total {
		v.Line(fmt.Sprintf("Showing %s of %s — jira issue comment list %s",
			cli.Number(len(preview.latest)), cli.Number(preview.total), plainKey))
	}
}

// commentCountHint renders the "N comment(s) — jira issue comment list
// KEY" line shared by renderCommentsPreview's two "nothing rendered
// inline" branches: the ordinary default hint (case 1 above), and the
// case-2/3 fallback for a known-positive total that nonetheless rendered
// nothing (see renderCommentsPreview's doc comment) — one wording, one
// place it is built, so the two can never drift apart. plainKey is
// embedded in prose (see renderCommentsPreview's own doc comment on that
// scope line) and must never be the hyperlinked form. total is colored
// through cli.Number; no cli.Text sanitizing sink runs over the
// assembled string for the same reason renderCommentsPreview's own
// banner above skips it — plainKey arrives already sanitized, and
// wrapping the result would strip cli.Number's SGR bytes back out.
func commentCountHint(total int, plainKey string) string {
	noun := human.Plural(total, "comment", "comments")
	return fmt.Sprintf("%s %s — jira issue comment list %s", cli.Number(total), noun, plainKey)
}

// renderDescription renders raw through the shared renderBody helper
// and, when it renders to anything, appends a blank line, a
// "Description" section label, and the rendered block — in that order,
// so the section reads as a distinct part of the view rather than a
// continuation of the Fields block above it. renderBody's own
// placeholder fallback (malformed ADF) still counts as "anything", so a
// parse failure still shows the section rather than disappearing.
func renderDescription(v *output.View, raw []byte) {
	rendered := renderBody(v, raw)
	if strings.TrimSpace(rendered) == "" {
		return
	}
	v.Line()
	v.Section("Description")
	v.Block(rendered)
}

// fitGlanceSummary truncates the glance line's summary to what width
// leaves after the key, the status, and the two 2-space gutters View.Line
// joins the three cells with (hence -4), so the one-line glance never
// runs off the right edge. width 0 (non-TTY) returns the summary
// unchanged — and the full summary always stays in fields.summary for
// --output=json regardless. key and status are measured by VISIBLE width
// (status carries ANSI once colored), and Truncate itself is ANSI-aware.
func fitGlanceSummary(summary, key, status string, width int) string {
	if width <= 0 {
		return summary
	}
	avail := width - ansi.StringWidth(key) - ansi.StringWidth(status) - 4
	if avail <= 0 {
		return summary
	}
	return ansi.Truncate(summary, avail, "…")
}

// val returns a Nullable's value, or the zero value when it was absent.
func val[T any](n jira.Nullable[T]) T { return n.Value }

// statusCategoryKey reads f.Status.StatusCategory.Key — the wire's
// statusCategory.key (internal/jira.StatusDetails.StatusCategory,
// internal/jira/wire_gen.go), doubly Nullable-wrapped (the status
// itself, then its category) — collapsing three layers of "was this
// even present" down to the one string cli.Status actually wants: "" for
// every absent layer (no status, a status with no category riding along,
// or a category with no key), which is exactly the empty value cli.
// Status documents as its own "no category signal, use the name
// fallback" trigger. Shared by both renderView (the glance line's
// status) and issueListColumns (list.go's STATUS column) — the one
// place either reads this chain, so they cannot drift into reading it
// two different ways.
func statusCategoryKey(f jira.IssueFields) cli.StatusCategory {
	return cli.StatusCategory(val(val(f.Status).StatusCategory).Key.Value)
}
