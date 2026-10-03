//go:build outputaudit

package audit

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/tests/guardrails"
)

// This file evaluates all five human-surface axes: H1 (human layout
// built from View primitives — an AST guard, not a byte check; see
// checkH1ViewPrimitives and tests/guardrails/human_view_primitives.go),
// H2 (dry-run speaks preview tense; see checkH2Tense and dryRunTense),
// H3 (consistent humanized formats), H4 (sanitization of Jira-sourced
// text), and H5 (color-forced SGR nesting). H1 reads a read's own
// CategorySuccess+ModeHuman cell (the read-side "bespoke layout" rule —
// applicabilityFor keeps H1 out of the mutation/local-state classes,
// whose dry-run preview is the only human capture they have); H2 reads
// a mutation's or local-state op's CategoryDryRun+ModeHuman cell — the
// converse of H1's own scoping, and the only place H2 CAN attach, since
// a read never dry-runs. H3/H4 read the same ModeHuman capture as
// whichever of H1/H2 applies to the operation's class; H5 reads the
// ModeHumanColor capture at the SAME (category, op) coordinate — see
// applicability.go's cellAxes.

// forbiddenControlRune reports whether r is a byte class H4 exists to
// strip from human output rendered under NO_COLOR (scenarioEnv pins
// NO_COLOR=1 for every capture, human included) — the same rule
// cli/sinks.go's own sanitize() enforces: ESC, any C0 control other than
// \n/\t (a rendered row may legitimately carry either), DEL, or a C1
// control (0x80-0x9F).
//
// This operates on a decoded RUNE, never a raw byte — the distinction is
// load-bearing. A byte-range scan over 0x80-0x9F would misfire on the
// second byte of an ordinary multi-byte UTF-8 character (0x97, the
// continuation byte inside 日's own 3-byte encoding, sits inside that
// exact range) and reject perfectly legitimate content. hostile.go's
// legitimateMultibyte constant exists specifically to make a naive
// byte-scanning implementation of this check fail against this
// package's own fixture — see checkH4Sanitized's doc comment.
func forbiddenControlRune(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20: // C0
		return true
	case r == 0x7f: // DEL
		return true
	case r >= 0x80 && r <= 0x9f: // C1
		return true
	default:
		return false
	}
}

// firstForbiddenRune decodes s as Unicode runes (a Go `for range` over a
// string always does, which is the entire reason this function does not
// need to reach for utf8.DecodeRuneInString itself) and returns the
// first forbidden control/escape rune found, plus a short window of
// surrounding context for the failure message to name. ok is false when
// s carries none.
func firstForbiddenRune(s string) (r rune, context string, ok bool) {
	runes := []rune(s)
	for i, candidate := range runes {
		if !forbiddenControlRune(candidate) {
			continue
		}
		start := max(0, i-15)
		end := min(len(runes), i+15)
		return candidate, string(runes[start:end]), true
	}
	return 0, "", false
}

// checkH4Sanitized is H4: on the human capture cellAxes selects (the
// hostile fixture's own success/dry-run capture — see hostile.go),
// neither Stdout nor Stderr may carry an escape byte a Jira-authored
// string could have smuggled in. Both streams are scanned, not just
// stdout: human STATUS reaches stderr (clog diagnostics, spinner
// finalizers), and anything that echoes response content there is
// exactly as Jira-fed as the rendered view on stdout.
//
// The failure message never dumps the offending bytes raw — that would
// corrupt whatever terminal is running the test, the exact failure mode
// this axis exists to prevent — it names the operation, quotes the
// offending rune escaped (%q, so ESC prints as the four bytes '\x1b',
// never the byte itself), and shows a short escaped window of
// surrounding text as the "field/context" the design calls for, since
// this check has no structural way to name a JSON field path from a
// flat rendered string.
func checkH4Sanitized(cap Capture, m command.Meta) (bool, string) {
	if bad, ctx, found := firstForbiddenRune(cap.Stdout); found {
		return false, fmt.Sprintf("op %q: stdout carries an unsanitized control/escape rune %s near %q — a Jira-sourced field reached the human view without going through a sanitizing sink (cli.Text/Entity/Status/Priority/Time)", m.Op, quoteRune(bad), ctx)
	}
	if bad, ctx, found := firstForbiddenRune(cap.Stderr); found {
		return false, fmt.Sprintf("op %q: stderr carries an unsanitized control/escape rune %s near %q", m.Op, quoteRune(bad), ctx)
	}
	return true, ""
}

// quoteRune formats r the same escaped way its surrounding context is
// quoted, plus its code point, so a reviewer can tell a C1 control from
// a coincidentally similar-looking ESC without decoding hex by hand.
func quoteRune(r rune) string {
	return fmt.Sprintf("%q (U+%04X)", r, r)
}

// rawISOTimestampRe is the wire shape Jira's own timestamps take — the
// task's own regex, matched as a SUBSTRING search (not anchored):
// exactly how the shipped defect this axis exists to catch actually
// looked, a raw ISO stamp sitting inside an otherwise ordinary human
// sentence. A human surface must show relative/humanized time (cli.Time)
// instead, never this shape.
var rawISOTimestampRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`)

// checkH3Formats is H3, two independently-necessary proofs against the
// same human capture cellAxes selects:
//
//  1. No raw ISO-8601 timestamp reaches human output — this is
//     precisely the defect that shipped: cli.TimeFrom's own predecessor
//     failed to parse Jira's colonless numeric offset and fell back to
//     printing the wire value unchanged.
//  2. Ordering stability: capturing the SAME scenario a second time
//     produces byte-identical output. The corpus caches captures per
//     process (corpus.go's sync.Once), so comparing cap against itself
//     would be a tautology — this runs the real binary a SECOND,
//     independent time (checkHumanOrderingStable below) rather than
//     faking the comparison.
//
// The "consistent Label: value shape" third leg the design doc mentions
// is deliberately NOT asserted here: every human renderer in this
// codebase already goes through output.View's own Field/Line primitives
// (view.go, comment.go), so a per-operation regex over rendered prose
// would either duplicate what View already guarantees structurally or
// false-positive on a legitimate multi-word value containing its own
// colon (a URL in a field value, say). Asserting it usefully needs a
// structural check against View's own call sites — closer to H1's own
// "built from View primitives" AST guard than to a byte-level golden
// check — so it is left to that later slice rather than faked here with
// a regex that cannot tell a real violation from a false one.
func checkH3Formats(cap Capture, m command.Meta) (bool, string) {
	text := humanTimestampText(cap.Stdout, cap.Scenario.Category, m)
	if loc := rawISOTimestampRe.FindStringIndex(text); loc != nil {
		return false, fmt.Sprintf("op %q: stdout carries a raw ISO-8601 timestamp %q — a human surface must show relative/humanized time (cli.Time), never the wire value", m.Op, text[loc[0]:loc[1]])
	}
	if loc := rawISOTimestampRe.FindStringIndex(cap.Stderr); loc != nil {
		return false, fmt.Sprintf("op %q: stderr carries a raw ISO-8601 timestamp %q", m.Op, cap.Stderr[loc[0]:loc[1]])
	}
	return checkHumanOrderingStable(cap, m)
}

// humanTimestampText distinguishes presentation from an explicitly labeled
// native request preview. Humanizing a timestamp inside that valid JSON object
// would change the request being inspected. All surrounding text, live reads,
// malformed JSON, and commands without native mutation metadata remain checked.
func humanTimestampText(text string, category Category, m command.Meta) string {
	if category != CategoryDryRun || !m.WritesJira || !m.Passthrough {
		return text
	}
	var checked strings.Builder
	for line := range strings.SplitSeq(text, "\n") {
		if payload, ok := strings.CutPrefix(line, "Payload: "); ok && strings.HasPrefix(payload, "{") && json.Valid([]byte(payload)) {
			continue
		}
		checked.WriteString(line)
		checked.WriteByte('\n')
	}
	return checked.String()
}

// checkHumanOrderingStable re-executes cap's own scenario, in the same
// mode, a second independent time — a fresh stub server (when the
// scenario names one) and a fresh temp workdir, exactly like the
// corpus's own first run (corpus.go's buildCorpus/runScenario) — and
// requires byte-identical Stdout and Stderr. Two runs, not a
// self-comparison: corpus() caches captures for the whole test process,
// so cap already IS the first run, and diffing it against itself would
// prove nothing about ordering stability at all.
func checkHumanOrderingStable(cap Capture, m command.Meta) (bool, string) {
	bin, err := jiraBinary()
	if err != nil {
		return false, err.Error()
	}
	var baseURL string
	if cap.Scenario.Stub != "" {
		handler, ok := stubServers[cap.Scenario.Stub]
		if !ok {
			return false, fmt.Sprintf("scenario %q references unknown stub %q", cap.Scenario.ID, cap.Scenario.Stub)
		}
		srv := httptest.NewServer(handler())
		defer srv.Close()
		baseURL = srv.URL
	}
	second, err := runScenario(bin, cap.Scenario, cap.Mode, baseURL)
	if err != nil {
		return false, fmt.Sprintf("op %q: second run of scenario %q failed: %v", m.Op, cap.Scenario.ID, err)
	}
	if second.Stdout != cap.Stdout {
		return false, fmt.Sprintf("op %q: two independent runs of scenario %q produced different stdout — ordering or content is not stable", m.Op, cap.Scenario.ID)
	}
	if second.Stderr != cap.Stderr {
		return false, fmt.Sprintf("op %q: two independent runs of scenario %q produced different stderr — ordering or content is not stable", m.Op, cap.Scenario.ID)
	}
	return true, ""
}

// sgrCode matches one ANSI SGR escape sequence (CSI ... m) — the only
// escape shape a human_color capture should ever carry once H4 already
// holds: H4 proves the hostile fixture's own injected ESC bytes never
// reach output, so whatever SGR sgrNestingOK finds here is genuinely
// styling, not an unsanitized field.
var sgrCode = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stylingExpected declares, per op, whether its human_color capture is
// KNOWN to render at least one styled span (today: cli.Status,
// cli.Priority, or cli.Entity/EntityAs — any future styling primitive
// joins this reasoning, not this list specifically). This is H5's own
// "color is genuinely on" proof, demanded by the design doc: an op
// recorded true that turns up NO SGR at all means --color=always broke
// silently somewhere between the flag and the render, which would
// otherwise leave H5 vacuously green for a reason that has nothing to do
// with nesting. An op recorded false is an honest "renders no styled
// content today" (a bare version string, a plain settings table, a
// plain preview line) — sgrNestingOK still runs for it, and trivially
// passes when there is nothing to nest.
//
// Every op reaching an H5 cell must have an entry, matching every other
// hand-written per-op table in corpus_test.go (identityRefField,
// passthroughFidelity, dryRunPreview, ...): a missing entry is a red
// cell naming the gap, not a silent default. Verified against the real
// compiled binary for every entry below (both directions) before this
// table was trusted — see this package's own development notes; a
// styling primitive added to an op here without updating its entry
// fails the very next run.
var stylingExpected = map[string]bool{
	"release.notes":      true,
	"version":            false,
	"config.list":        false,
	"config.get":         false,
	"config.set":         false,
	"auth.status":        false,
	"auth.whoami":        false,
	"auth.login":         false,
	"auth.logout":        false,
	"config.edit":        false,
	"issue.view":         true,
	"issue.comment.list": true,
	"issue.list":         true,
	"issue.mine":         true,
	"issue.link.list":    true,
	// The attachment table styles its AUTHOR column through cli.Entity
	// and its SIZE through cli.Bytes, so a --color=always capture
	// genuinely carries SGR bytes.
	"issue.attachment.list": true,
	// The watcher table styles its WATCHER column through cli.Entity, so
	// a --color=always capture genuinely carries SGR bytes. Its other
	// three columns are plain by design (see issueWatchersColumns).
	"issue.watchers.list": true,
	// The comment-add render styles every issue key it names through a
	// sink, and its preview closes with a hint — clog draws that in its
	// own style — so a --color=always capture genuinely carries SGR
	// bytes on both the success and the dry-run coordinate.
	"issue.comment.add": true,
	// Same shape as comment.add: every issue key it names goes through
	// cli.Entity, and both branches close with a hint clog draws in its
	// own style, so a --color=always capture genuinely carries SGR bytes
	// on the success and the dry-run coordinate alike.
	"issue.transition": true,
	// Both field-taking writes style the issue keys they name through
	// cli.Entity, and both draw their preview's payload under a Section —
	// clog's Divider, which is styled — so a --color=always capture
	// carries SGR bytes on the success and dry-run coordinates alike.
	//
	// create is the one that had to EARN the dry-run half, and the note is
	// worth keeping because the obvious guess is wrong: a preview of a
	// create names no key (there is none yet), and the closing hint turns
	// out to carry no SGR of its own — verified, not assumed. The Section
	// is what makes this axis non-vacuous there. Remove it and H5 goes red
	// rather than quietly checking nothing, which is the correct
	// direction.
	// The type table styles its NAME column through cli.Entity, so a
	// --color=always capture carries SGR bytes. The two phrase columns are
	// plain by design: they are the strings a caller retypes into --type,
	// and styling them would invite copying an escape sequence.
	"issue.link.types": true,
	"issue.create":     true,
	"issue.edit":       true,
	// The two single-target comment writes style the comment id and the
	// issue key through cli.Entity on every line they draw, on both the
	// live and the preview coordinate, so a --color=always capture
	// genuinely carries SGR bytes.
	"issue.comment.edit":   true,
	"issue.comment.delete": true,
	// The watcher writes style every issue key they name through
	// cli.Entity, and the preview styles the account id too.
	"issue.watchers.add":    true,
	"issue.watchers.remove": true,
	// The link and weblink writes all style their issue keys and ids
	// through cli.Entity on every line they draw, on both coordinates.
	"issue.link.create":    true,
	"issue.link.delete":    true,
	"issue.weblink.add":    true,
	"issue.weblink.delete": true,
	// The weblink table styles nothing: its four columns are an opaque
	// id, a URL, a title and a free-text relationship, and every one of
	// them is plain by design (see issueWeblinkColumns). Recorded false
	// honestly rather than made true by coloring something for the sake
	// of the axis.
	"issue.weblink.list": false,
	// The attachment writes all style ids and keys through cli.Entity on
	// every line they draw, and the download's live line styles its size
	// through cli.Bytes as well.
	"issue.attachment.add":      true,
	"issue.attachment.download": true,
	"issue.attachment.delete":   true,
	// The three lifecycle writes style every issue key they name through
	// cli.Entity, on both coordinates.
	"issue.delete": true,
	"issue.clone":  true,
	"issue.rank":   true,
}

// sgrNestingOK is H5's own byte-level SGR-nesting discrimination for one
// LINE of output — never the whole multi-line capture at once, because
// two independently-styled rows sitting on different lines of the same
// Fields block (a colored Priority value on one line, a colored
// Assignee value on the next) are exactly as independent as two spans on
// the same line, and the newline between them is already whitespace by
// the rule below; splitting first just keeps that reasoning from having
// to special-case "or a newline" everywhere.
//
// Built by READING internal/cli/issue/comment_test.go's
// resetsStayTrailing (the slice-1 guard that pinned this exact bug in
// comment.go, and the reason this axis exists at all) rather than
// importing or duplicating it verbatim, because that helper's own final
// check — "a reset followed by anything but whitespace, with no further
// escape code at all, is a violation" — is correct ONLY for the one
// shape it is ever run against there: a captured DIVIDER line, which
// clog always closes with its own trailing reset, so "nothing follows
// the last reset" is the true end of that whole styled construct. H5
// runs over arbitrary lines from arbitrary renderers, where a self-
// closed styled value legitimately trails off into plain prose or
// punctuation with NO further escape code at all — that is not the
// divider bug's shape, it is the ordinary "colored value, then more
// text" pattern used throughout internal/cli/issue/view.go. Two real,
// currently-shipping examples from THIS package's own corpus prove the
// difference is not academic — resetsStayTrailing flags both, and both
// are correct output with zero nesting defect:
//
//   - issue view's "Labels:    <color>audit-corpus<reset>, <color>second-label<reset>"
//     — two independently self-closed entity colors joined by a bare
//     ", " (punctuation, not whitespace, sits between the first reset
//     and the second color's open).
//   - issue view's inline comment header "<color>Corpus Commenter<reset> • 2 years ago"
//     (renderCommentsPreview's v.Line, as opposed to comment.go's own
//     v.Section) — one self-closed color followed by plain suffix text,
//     never wrapped by anything, so nothing "breaks" when the color
//     closes early.
//
// The rule kept from resetsStayTrailing: a reset immediately followed by
// a FRESH style-open starts a new, independent span (a divider's own
// rule/title/rule shape, or the two Labels colors above) and is never a
// violation, whatever plain text — even punctuation — sits between them.
// The rule dropped: trailing content after the LAST reset, with no
// further escape code at all before the line ends, is not itself a
// violation — nothing later closes over that content, so no still-open
// OUTER span was ever broken by it. What remains is the divider bug's
// actual, and only, signature: a reset, then visible content, then
// ANOTHER reset with no open in between — the second reset closing a
// span (clog's Divider's own outer Bold) that the first, premature reset
// had already ended, stranding the content between them unstyled.
//
// Proven both directions against the REAL compiled binary before being
// trusted here: planting the pre-fix nested form back into comment.go
// (cli.Entity(author)+" • "+cli.Time(created) passed straight to
// v.Section, instead of the current cli.EntityAs(author, header)) and
// capturing `issue comment list TST-2 --color=always` produces exactly
// the reset-content-reset shape above and this function returns false
// for it; reverting reproduces today's tree and this function returns
// true for every line HumanColor captures for issue.view and
// issue.comment.list, INCLUDING the two "correct but resetsStayTrailing-
// flagged" lines named above.
func sgrNestingOK(line string) bool {
	matches := sgrCode.FindAllStringIndex(line, -1)
	seenReset, last := false, 0
	for _, m := range matches {
		code := line[m[0]:m[1]]
		params := strings.TrimSuffix(strings.TrimPrefix(code, "\x1b["), "m")
		isReset := params == "" || params == "0"
		if isReset && seenReset && strings.TrimSpace(line[last:m[0]]) != "" {
			// A reset, content, then ANOTHER reset with no fresh open
			// between them: the divider-title bug's exact shape.
			return false
		}
		seenReset = isReset
		last = m[1]
	}
	return true
}

// h1Once/h1ByOp/h1Err cache ScanHumanRenderers' result for the whole
// test process, matching jiraBinary's and corpus's own once-per-process
// shape: the scan walks and parses every file under internal/cli, and
// every read-class operation's H1 cell would otherwise re-pay that cost.
var (
	h1Once sync.Once
	h1ByOp map[string][]guardrails.HumanRenderViolation
	h1Err  error
)

// h1Violations runs tests/guardrails' own H1 scan once and indexes its
// result by the operation each violation was attributed to — see
// human_view_primitives.go's ScanHumanRenderers for how that attribution
// works (each entry point is walked from its OWN op, so a violation
// several calls deep still names the op whose Human field reached it).
func h1Violations() (map[string][]guardrails.HumanRenderViolation, error) {
	h1Once.Do(func() {
		root, err := repoRoot()
		if err != nil {
			h1Err = err
			return
		}
		violations, err := guardrails.ScanHumanRenderers(root)
		if err != nil {
			h1Err = err
			return
		}
		h1ByOp = map[string][]guardrails.HumanRenderViolation{}
		for _, v := range violations {
			h1ByOp[v.Op] = append(h1ByOp[v.Op], v)
		}
	})
	return h1ByOp, h1Err
}

// checkH1ViewPrimitives is H1. It is deliberately NOT a check against
// cap — H1's whole premise is that rendered bytes cannot distinguish
// "aligned by View.Fields" from "aligned by hand" (design doc), so this
// axis has no byte-level evidence to read at all. The evidence is
// tests/guardrails' own AST walk (human_view_primitives.go): op's Human
// entry point, followed through its own package-local call-graph
// closure, contains no direct write bypassing a View primitive. That
// guard is package-wide by construction — one scan covers every
// operation in one pass — but each violation it returns already carries
// the OP whose entry point reached it, so per-operation attribution
// here is a filter over the shared result, not a re-implementation of
// the walk: this cell's evidence IS that guard's verdict, scoped to this
// one operation, which is the design doc's own preferred answer to its
// "does H1 belong in the matrix, or does the matrix reference it" open
// question.
//
// See human_view_primitives.go's own doc comment for exactly how far
// the walk reaches (one package deep from each entry point) and what
// that scoping does not cover (a helper package that itself accepted a
// writer, which nothing in this tree does today).
func checkH1ViewPrimitives(_ Capture, m command.Meta) (bool, string) {
	byOp, err := h1Violations()
	if err != nil {
		return false, "H1 scan failed: " + err.Error()
	}
	violations := byOp[m.Op]
	if len(violations) == 0 {
		return true, ""
	}
	details := make([]string, len(violations))
	for i, v := range violations {
		details[i] = v.String()
	}
	return false, strings.Join(details, "; ")
}

// dryRunTense is H2's hand-written per-op tense contract, read off each
// renderer rather than invented: wantProspective is the substring the
// dry-run capture MUST contain (the phrase that renderer's own DryRun
// branch emits), and forbidCompleted lists the substrings that renderer
// emits ONLY on a real write — the completed-tense wording a preview
// must never leak. This is the "rule with teeth" the design doc asks
// for over a tautological "every dry-run contains Would" check: a
// renderer that regressed to printing both branches, or swapped which
// branch DryRun selects, fails this even though the word "would" might
// still appear somewhere in its output.
//
// Read from the renderers themselves (auth/login.go's renderLoginHuman,
// auth/logout.go's renderLogoutHuman, config/edit.go's renderEditHuman):
// all three ARE already tense-consistent — every dry-run branch speaks
// the prospective form, and the corresponding completed form is
// reachable ONLY from that same renderer's non-dry-run branch, never
// alongside the preview wording. auth.login's "Replaced the stored
// credential for" field is not part of this table: its VALUE (not its
// label) is what would carry a completed-tense leak, and login() only
// ever populates that value inside its own `if !dryRun` branch (see
// login's own comment), so a dry-run capture never carries the field at
// all — output.Fields skips an empty one — and there is no completed
// phrase to name because the whole field is structurally absent, not
// merely differently worded.
var dryRunTense = map[string]struct {
	wantProspective string
	forbidCompleted []string
}{
	"auth.login": {
		wantProspective: "Token is valid for",
		forbidCompleted: []string{"Logged in to"},
	},
	"auth.logout": {
		wantProspective: "would remove the stored token for",
		forbidCompleted: []string{"removed the stored token for", "is still in effect"},
	},
	"config.edit": {
		wantProspective: "would open ",
		forbidCompleted: []string{"created ", "updated "},
	},
	"config.set": {
		wantProspective: "would store ",
		forbidCompleted: []string{"stored ", "created config with ", "no changes to "},
	},
	// The first JIRA mutation's tense, and the strictest entry here,
	// because this is the first preview whose completed form would
	// describe a change on someone else's server rather than on this
	// machine. "comment added." is the live renderer's exact per-key
	// line, so naming it forbidden pins the two branches apart by the
	// literal string one of them emits.
	//
	// The two renderers are separate functions with no shared sentence
	// (renderCommentAddPreview / renderCommentAddResults), so this axis
	// is guarding a structural split rather than a habit of wording.
	"issue.comment.add": {
		wantProspective: "Would add a comment to ",
		forbidCompleted: []string{"comment added.", "Added a comment"},
	},
	// The hardest entry in this table, because this op's preview has TWO
	// past-tense forms to keep out and they come from different places.
	//
	// "moved to" is the live per-key line. "Comment added" is the live
	// comment line. Both are emitted by renderTransitionResults, which
	// the preview branch never reaches — the split is structural
	// (renderTransitionPreview is a separate function sharing no
	// sentence with it), and naming both literals here is what pins the
	// split rather than trusting it.
	//
	// The spinner is the third source and the one most likely to leak,
	// since it is drawn before any outcome is known: the remote preview
	// runs an animation while writing nothing, and its label says
	// "Checking", never "Moving" (previewSpinnerLabel). A capture is
	// scanned for the completed phrases on stdout only, so this entry
	// does not catch a spinner leak by itself — that is why the label
	// carries its own doc comment saying so.
	"issue.transition": {
		wantProspective: "Would move ",
		forbidCompleted: []string{"moved to ", "Comment added"},
	},
	// The field-taking writes. Both keep their two renderers as separate
	// functions sharing no sentence (renderCreatePreview /
	// renderCreateResult, renderEditPreview / renderEditResults), so this
	// axis guards a structural split rather than a habit of wording — and
	// the forbidden literals are the live branches' exact strings, which
	// is what pins the split rather than trusting it.
	//
	// "Created " with its trailing space matters: forbidding bare
	// "Created" would also forbid the word inside a Jira-authored summary
	// echoed back, and this axis must fail on the CLI's own tense rather
	// than on the tenant's prose.
	"issue.create": {
		wantProspective: "Would create an issue",
		forbidCompleted: []string{"Created ", "Reads back as"},
	},
	"issue.edit": {
		wantProspective: "Would send this payload to ",
		forbidCompleted: []string{": updated.", ": updated —"},
	},
	// The two single-target comment writes. Each forbids the LITERAL
	// sentence its live renderer emits, which is what pins the split
	// rather than trusting it — the preview and result branches are
	// separate functions sharing no sentence, and naming the string one
	// of them produces is how this axis notices if that ever stops being
	// true.
	"issue.comment.edit": {
		wantProspective: "Would replace comment ",
		forbidCompleted: []string{"comment edited.", "Updated "},
	},
	// The strictest pair of literals in this table, because the two
	// wordings differ by a single tense and nothing else: "Would delete
	// comment" against "Deleted comment". That is exactly the collision
	// H2 exists to catch — a renderer that lost its branch would emit the
	// completed form on a run that deleted nothing.
	"issue.comment.delete": {
		wantProspective: "Would delete comment ",
		forbidCompleted: []string{"Deleted comment "},
	},
	// The watcher writes. Each forbids the other's live line as well as
	// its own, since the two renderers share a function and a leak
	// between them would be a real defect rather than an impossible one.
	"issue.watchers.add": {
		wantProspective: "Would add ",
		forbidCompleted: []string{"watcher added.", "watcher removed."},
	},
	"issue.watchers.remove": {
		wantProspective: "Would remove ",
		forbidCompleted: []string{"watcher removed.", "watcher added."},
	},
	// The link create preview has TWO prospective forms — one when the
	// type resolved and one when it did not — and this entry checks the
	// bare-preview one, matching its recipe. The forbidden phrase is the
	// live renderer's own sentence shape: it prints "KEY-2 blocks KEY-3."
	// with no leading verb, so what must not appear is the relation
	// standing alone as a completed statement.
	"issue.link.create": {
		wantProspective: "Would link ",
		forbidCompleted: []string{"Linked "},
	},
	// The tightest pair in this table alongside comment.delete's: the two
	// wordings differ by one tense and nothing else.
	"issue.link.delete": {
		wantProspective: "Would delete link ",
		forbidCompleted: []string{"Deleted link "},
	},
	// "Would attach" against the live "web link added." / "web link
	// replaced." — both live forms are forbidden, since the renderer
	// picks between them and a leak of either would be the same defect.
	"issue.weblink.add": {
		wantProspective: "Would attach ",
		forbidCompleted: []string{"web link added.", "web link replaced."},
	},
	"issue.weblink.delete": {
		wantProspective: "Would delete web link ",
		forbidCompleted: []string{"Deleted web link "},
	},
	"issue.attachment.add": {
		wantProspective: "Would upload ",
		forbidCompleted: []string{"file attached", "files attached"},
	},
	// The download's preview has TWO prospective forms — "into <dir>"
	// when the name is unresolved, "to <path>" when it is — and this
	// entry checks the bare one, matching its recipe. Both live verbs are
	// forbidden, since the renderer picks between them.
	"issue.attachment.download": {
		wantProspective: "Would write attachment ",
		forbidCompleted: []string{"Wrote ", "Replaced "},
	},
	"issue.attachment.delete": {
		wantProspective: "Would delete attachment ",
		forbidCompleted: []string{"Deleted attachment "},
	},
	"issue.delete": {
		wantProspective: "Would delete ",
		forbidCompleted: []string{": deleted."},
	},
	// The live renderer draws an arrow between the source key and the
	// clone's, which is a shape rather than a sentence — so what is
	// forbidden here is that arrow, the one thing a preview could not
	// honestly produce without a key to point at.
	"issue.clone": {
		wantProspective: "Would clone ",
		forbidCompleted: []string{" → "},
	},
	"issue.rank": {
		wantProspective: "Would move ",
		forbidCompleted: []string{": moved "},
	},
}

// checkH2Tense is H2: a mutation's or local-state op's dry-run human
// capture states the prospective form of dryRunTense's own
// wantProspective, and contains NONE of its forbidCompleted phrases —
// the completed-tense wording that would mean the preview reads as
// though the change already happened.
//
// It also proves the dry-run wrote nothing (Capture.WroteFiles) —
// checkDryRunPreview (M7, corpus_test.go) already reads the identical
// fingerprint, and this is not a redundant re-check by accident: H2's
// whole claim is "this is a preview, not a write," and an axis that
// asserted correct WORDING while silently allowing an actual write
// underneath it would be worse than making no claim at all — a caller
// reading H2 green has to be able to trust both halves of that
// sentence.
func checkH2Tense(cap Capture, m command.Meta) (bool, string) {
	if cap.WroteFiles {
		return false, fmt.Sprintf("op %q: the dry-run changed a file under HOME — a preview must not write, whatever its wording says", m.Op)
	}
	rule, ok := dryRunTense[m.Op]
	if !ok {
		return false, fmt.Sprintf("no dry-run tense expectation written for op %q — add it beside its recipes", m.Op)
	}
	if !strings.Contains(cap.Stdout, rule.wantProspective) {
		return false, fmt.Sprintf("op %q: dry-run human output does not contain the prospective phrase %q — %q", m.Op, rule.wantProspective, cap.Stdout)
	}
	for _, forbidden := range rule.forbidCompleted {
		if strings.Contains(cap.Stdout, forbidden) {
			return false, fmt.Sprintf("op %q: dry-run human output contains the completed-tense phrase %q — a preview must never read as though the change was made — %q", m.Op, forbidden, cap.Stdout)
		}
	}
	return true, ""
}

// checkH5StylingNests is H5: on the human_color capture cellAxes selects
// (the SAME (op, category) coordinate H3/H4 already read for ModeHuman —
// see cellAxes's ModeHumanColor branches), every line of both Stdout and
// Stderr nests its SGR spans per sgrNestingOK, and — the "color is
// genuinely on" proof — an op stylingExpected declares true must show at
// least one SGR sequence somewhere in the capture, or this whole axis
// would be silently checking nothing.
//
// Both streams are scanned, matching H4's own reasoning: human STATUS
// (clog diagnostics, a spinner's finalizer line) reaches stderr, and
// whatever colors that carries is exactly as capable of the same
// nesting defect as the rendered view on stdout.
//
// The failure message never quotes a line raw — %q escapes every byte,
// ESC included, so a failing assertion cannot itself corrupt whatever
// terminal is running the suite.
func checkH5StylingNests(cap Capture, m command.Meta) (bool, string) {
	expectStyled, ok := stylingExpected[m.Op]
	if !ok {
		return false, fmt.Sprintf("no styling expectation written for op %q — add it beside its recipes", m.Op)
	}
	if expectStyled && !sgrCode.MatchString(cap.Stdout) && !sgrCode.MatchString(cap.Stderr) {
		return false, fmt.Sprintf("op %q: human_color capture carries no SGR sequence at all, though this op is declared to style output — either --color=always failed to force color, or the render path stopped calling a styled sink; H5 would otherwise be vacuously green", m.Op)
	}
	for _, stream := range [...]struct {
		name string
		text string
	}{{"stdout", cap.Stdout}, {"stderr", cap.Stderr}} {
		for _, line := range strings.Split(stream.text, "\n") {
			if !sgrNestingOK(line) {
				return false, fmt.Sprintf("op %q: %s carries a line with a reset before another reset and content between them, no fresh style-open in between, near %q — the divider-title nesting bug is back", m.Op, stream.name, line)
			}
		}
	}
	return true, ""
}
