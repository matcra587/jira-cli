package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	xslices "github.com/gechr/x/slices"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// Body-flag names, spelled once so registration, alias resolution and
// every refusal below name the same strings. The canonical set is the
// design's §2.1: an ADF route, a Markdown route, and — on transition
// only — the same pair renamed.
const (
	FlagJSONInput    = "json-input"
	FlagMarkdown     = "markdown"
	FlagMarkdownFile = "markdown-file"
)

// StdinPath is the --markdown-file value that means standard input. A
// lone dash is the convention every CLI that reads a file shares, and
// naming it here keeps the reader and the help text from disagreeing.
const StdinPath = "-"

// maxBodyBytes caps what a body route will read from a file or stdin.
//
// Jira's own comment limit is 32,767 characters, so a megabyte is far
// past any body the server would accept and still small enough that a
// caller who redirects the wrong file into stdin gets a refusal instead
// of a process eating their disk. The cap exists to bound THIS process;
// the server enforces its own limit and says so in its own words, which
// is a better error than one guessed at here.
const maxBodyBytes = 1 << 20

// BodyFlags is the shared body surface: the three input routes of the
// design's §2.1, bound as one struct so a command registers them with
// one call and resolves them with one more.
//
// The three are NOT fallbacks for one another, and the distinction is
// the whole reason this is a bundle rather than a single --body string.
// ADF (--json-input) is the lossless route and the only one that reaches
// a field this CLI has no flag for. Markdown (--markdown,
// --markdown-file) exists because nobody hand-writes ADF for a
// paragraph. A command's own scalar flags are the third route and are
// not this struct's business.
type BodyFlags struct {
	// JSONInput is a path to a file holding a native ADF document.
	JSONInput string
	// Markdown is body text given inline on the command line.
	Markdown string
	// MarkdownFile is a path to a file holding Markdown, or StdinPath to
	// read standard input.
	MarkdownFile string

	// names carries this bundle's ACTUAL flag spellings, which differ on
	// transition (--comment/--comment-file, per §3.1). Populated by
	// AddBodyFlags so that Resolve's refusals name the flags the caller
	// actually typed rather than the default spellings — an error naming
	// --markdown on a command that has no --markdown would send the
	// reader looking for a flag that is not there.
	names bodyNames
}

// bodyNames is one command's body-flag spellings.
type bodyNames struct {
	jsonInput string
	markdown  string
	file      string
	jsonHelp  string
	jsonTerse string
}

// BodyOption customizes AddBodyFlags. Functional options for the reason
// KeyedOption is: the commands carrying a body do not all spell it the
// same way, and a registrar that forced one spelling would re-ship the
// undiscoverable flag name §3.1 exists to fix.
type BodyOption func(*bodyNames)

// WithJSONInputHelp declares the JSON surface of a compound input owner.
// It changes registration only; that owner must parse its native request and
// call ResolveADF for any already-read ADF document it contains.
func WithJSONInputHelp(help, terse string) BodyOption {
	return func(n *bodyNames) {
		n.jsonHelp, n.jsonTerse = help, terse
	}
}

// AsComment renames the Markdown pair to --comment/--comment-file.
//
// `issue transition` is the case this exists for, and the reasoning is
// the design's §3.1 rather than a preference. 0.15 shipped the
// capability as --markdown, named for its ENCODING rather than its
// purpose, and users could not find it — the flag was not missing, it
// was unfindable. On a command whose subject is the status change, the
// thing being attached is a comment; that it happens to be written in
// Markdown is an implementation fact about the value, not what the flag
// is for.
//
// --json-input is deliberately NOT renamed: it names its own encoding
// because that IS its purpose — it is the route for callers who have an
// ADF document, and there is no better word for what it takes.
func AsComment() BodyOption {
	return func(n *bodyNames) {
		n.markdown = "comment"
		n.file = "comment-file"
	}
}

// WithoutADFRoute suppresses --json-input and its two aliases, leaving this
// bundle registering the Markdown route alone.
//
// `issue create` and `issue edit` are what this exists for, and the reason
// is that --json-input means something GENUINELY DIFFERENT on them. On a
// comment it names a file holding an ADF document — a body, which is the
// whole of what a comment is. On create and edit it names a file holding
// an ISSUE PAYLOAD: a `fields`/`update` document whose description happens
// to be one member among fourteen (design §2.1, whose table says "the
// WHOLE payload, every field").
//
// So the two are not one flag with two encodings; they are two flags that
// share a spelling because they play the same ROLE — the lossless,
// agent-facing route — on different subjects. Registering the comment
// version on create would validate an issue payload as ADF and reject
// every correct document, and describing it in help as "a native ADF
// document" would tell an agent something false about what to send.
//
// The Markdown pair is untouched and still means exactly what it means
// everywhere else: the body, which on an issue is its description. That is
// precisely why this is an opt-OUT of one route rather than a reason for
// create and edit to skip the registrar — the two routes they keep are the
// registrar's, and only the third is theirs.
//
// A command taking this option owns the whole of --json-input: registering
// it, aliasing it, reading it, and refusing it beside the Markdown route.
// Conflict below cannot do that last part for it, because a suppressed
// route leaves JSONInput permanently empty and nothing for the check to
// see.
func WithoutADFRoute() BodyOption {
	return func(n *bodyNames) {
		n.jsonInput = ""
	}
}

// AddBodyFlags registers the §2.1 body surface on cmd, binding into f.
//
// It registers the canonical flags with their clib metadata and the
// hidden aliases of §5 with none — the same split AddListFlags makes,
// for the same reason: nothing renders a hidden flag, so nothing needs a
// Terse to render it with.
//
// A caller MUST follow this with a Resolve call from its Spec.Validate.
// This function runs at construction, before cobra has parsed anything,
// so it can neither fold an alias into its canonical flag nor see which
// flags were given — the same timing constraint AddListFlags and
// AddKeyedFlags both document.
func AddBodyFlags(cmd *cobra.Command, f *BodyFlags, opts ...BodyOption) {
	names := bodyNames{
		jsonInput: FlagJSONInput, markdown: FlagMarkdown, file: FlagMarkdownFile,
		jsonHelp: "Read a native ADF document from this JSON file — the lossless route", jsonTerse: "ADF from file",
	}
	for _, opt := range opts {
		opt(&names)
	}
	f.names = names

	AddStringVar(cmd, &f.Markdown, names.markdown, "",
		"Body text as Markdown, converted to ADF",
		FlagOpts{Terse: "Body as Markdown", Placeholder: "text"})
	AddStringVar(cmd, &f.MarkdownFile, names.file, "",
		"Read the Markdown body from this file, or `-` for standard input",
		FlagOpts{Terse: "Body from file", Placeholder: "path"})
	if names.jsonInput != "" {
		AddStringVar(cmd, &f.JSONInput, names.jsonInput, "",
			names.jsonHelp,
			FlagOpts{Terse: names.jsonTerse, Placeholder: "path"})
	}

	AddHiddenAliases(cmd, bodyAliasesFor(names))
}

// ADFRouteAliases returns the hidden aliases that mean --json-input.
//
// A narrowing of BodyFlagAliases rather than a second list, so the ONE
// table still drives both ends (design §2, "one table, both ends") even on
// the commands where the ADF flag moved out of this registrar and onto the
// command itself — `issue create` and `issue edit` own --json-input
// themselves (see WithoutADFRoute) and register its aliases from this
// same table.
func ADFRouteAliases() []HiddenAlias {
	return xslices.Filter(BodyFlagAliases, func(a HiddenAlias) bool {
		return a.Canonical == FlagJSONInput
	})
}

// BodyFlagAliases is the ONE table AddBodyFlags (registration) and
// Resolve (resolution) both read, so neither half can drift from the
// other — cli.OutputAliases' own shape and ListFlagAliases' own
// reasoning.
//
// Every entry is a spelling a caller trained on acli, jcli or 0.15 would
// reach for (design §5). The point is that such an invocation WORKS
// rather than erroring at someone who typed a reasonable thing.
//
// --description is the one that needs its reasoning recorded at the
// table rather than in the design doc, because it is the one that looks
// like a mistake: it aliases the BODY route, never --summary. acli's
// --description is "plain text or ADF" while this one is Markdown→ADF,
// so plain prose survives it and an ADF document passed through it would
// be re-encoded as literal text — which is why --json-input exists and
// is documented as the ADF route. It is aliased anyway because
// --description is the likeliest flag an LLM emits on a body-bearing
// command, and quietly accepting it beats refusing a caller who meant
// exactly what this route does.
var BodyFlagAliases = []HiddenAlias{
	{Flag: "body", Canonical: FlagMarkdown},
	{Flag: "description", Canonical: FlagMarkdown},
	{Flag: "body-file", Canonical: FlagMarkdownFile},
	{Flag: "template", Canonical: FlagMarkdownFile},
	{Flag: "description-file", Canonical: FlagMarkdownFile},
	{Flag: "from-json", Canonical: FlagJSONInput},
	{Flag: "body-adf", Canonical: FlagJSONInput},
}

// bodyAliasesFor maps the shared alias table onto one command's actual
// spellings.
//
// On a bare registration this is the identity. On transition, where
// AsComment renamed the pair, --body and --description resolve into
// --comment rather than into a --markdown that command does not have —
// and 0.15's own --markdown becomes an alias too, which is §3.1's
// stated bargain: the old spelling keeps working, unlisted, so renaming
// the flag costs no caller their invocation.
func bodyAliasesFor(n bodyNames) []HiddenAlias {
	out := make([]HiddenAlias, 0, len(BodyFlagAliases)+2)
	for _, a := range BodyFlagAliases {
		switch a.Canonical {
		case FlagMarkdown:
			out = append(out, HiddenAlias{Flag: a.Flag, Canonical: n.markdown})
		case FlagMarkdownFile:
			out = append(out, HiddenAlias{Flag: a.Flag, Canonical: n.file})
		case FlagJSONInput:
			// Dropped entirely when the ADF route is suppressed
			// (WithoutADFRoute). The command that took that option
			// registers --json-input and these two aliases itself, with its
			// own payload meaning; leaving them here would declare each
			// alias twice on one flag set and panic.
			if n.jsonInput == "" {
				continue
			}
			out = append(out, HiddenAlias{Flag: a.Flag, Canonical: n.jsonInput})
		}
	}
	if n.markdown != FlagMarkdown {
		// The renamed case only. Registering these unconditionally would
		// declare --markdown twice on every other command and panic.
		out = append(out,
			HiddenAlias{Flag: FlagMarkdown, Canonical: n.markdown},
			HiddenAlias{Flag: FlagMarkdownFile, Canonical: n.file})
	}
	return out
}

// Body is a resolved body: the ADF document to send, which route
// produced it, and what that route could not carry.
type Body struct {
	// ADF is the document, ready to send. Empty when no body was given
	// at all, which Resolve permits and the command decides about — a
	// comment requires one, an edit may not.
	ADF []byte
	// Route names where it came from, for the preview to report. A
	// caller reading a dry-run should be able to see which of the three
	// routes the CLI decided it was on.
	Route string
	// Lossy carries the constructs the Markdown route could not represent
	// in ADF, ready to be published as envelope warnings. Always empty on
	// the ADF route, which is lossless by definition — that difference is
	// the entire reason §2.1 keeps the two as separate routes rather than
	// one flag.
	//
	// envelope.Warning, not the converter's own warning type: these exist
	// to be published, and typing them as what they become keeps adf's
	// vocabulary out of the CLI contract. toEnvelopeWarnings maps across.
	//
	// Already PUBLISHED by the time a caller sees them — ConvertMarkdown
	// raises each one through cli.AddWarning, so they ride the envelope
	// whether or not anything here is read. They are returned as well
	// because publishing and DECIDING are different: a future --adf-strict
	// refuses on Lossy: true entries, and it needs them typed and in hand,
	// not drained into a process-wide sink. Whether a given loss is
	// acceptable stays the command's judgment; that the caller is told is
	// not negotiable, which is why one is automatic and the other is not.
	//
	// Not every entry lost something. Lossy: false means the content
	// survives in another shape (an image rendered as a link), and the bit
	// is carried across from the converter rather than assumed.
	Lossy []envelope.Warning
}

// Route names, published because a dry-run envelope carries one and a
// caller may branch on it.
const (
	RouteADF      = "adf"
	RouteMarkdown = "markdown"
	RouteNone     = ""
)

// Resolve folds the hidden aliases in, enforces §2.1's conflict rule,
// and reads whichever route was chosen into an ADF document.
//
// It must run AFTER cobra has parsed — a command's Spec.Validate is
// where the alias folding belongs — but it also performs I/O (reading a
// file, draining stdin), which Validate is not the place for. So the
// two halves split: ResolveAliases and Conflict below run from Validate,
// and Resolve itself runs at the top of Run, where stdin is reachable
// through the Invocation and a failure is an ordinary handler error.
//
// stdin is passed rather than taken from the process so a test supplies
// a buffer, exactly as auth login's token read does.
func (f *BodyFlags) Resolve(ctx context.Context, stdin io.Reader, stdinIsTerminal bool) (Body, error) {
	if err := ctx.Err(); err != nil {
		return Body{}, err
	}
	switch {
	case f.JSONInput != "":
		data, err := ReadInputFileContext(ctx, f.names.jsonInput, f.JSONInput, stdin, stdinIsTerminal)
		if err != nil {
			return Body{}, err
		}
		// Validated here, before anything composes a request around it.
		// A malformed ADF document must fail as the caller's input — with
		// the flag named and exit 3 — rather than travel to Jira and come
		// back as a 400 whose message is about a payload the caller never
		// wrote.
		return ResolveADF(f.names.jsonInput, f.JSONInput, data)

	case f.Markdown != "":
		return ConvertMarkdown(f.names.markdown, f.Markdown)

	case f.MarkdownFile != "":
		data, err := ReadInputFileContext(ctx, f.names.file, f.MarkdownFile, stdin, stdinIsTerminal)
		if err != nil {
			return Body{}, err
		}
		return ConvertMarkdown(f.names.file, string(data))
	}
	return Body{Route: RouteNone}, nil
}

// ResolveADF validates already-read native input without reading a stream twice.
// Compound request owners use it for ADF documents inside their payloads too.
func ResolveADF(flag, source string, data []byte) (Body, error) {
	if err := adf.Validate(data); err != nil {
		e := cli.NewInputError(cli.CodeFlagValueInvalid,
			fmt.Sprintf("--%s: %s is not a valid ADF document: %v", flag, source, err))
		e.Field = flag
		return Body{}, e
	}
	return Body{ADF: data, Route: RouteADF}, nil
}

// ConvertMarkdown converts one Markdown route and publishes what the
// conversion could not carry, so a caller whose content changed shape is
// told about it in the envelope.
//
// It sits here, between Resolve and encodeMarkdown, for one reason: every
// body-taking command (`comment add`, `comment edit`, `transition
// --comment`, `create`, `edit`) reaches its body through Resolve, so
// publishing at this single seam means none of them has to remember to.
// The alternative — each command walking Body.Lossy after its own Resolve
// call — is five chances to forget, and the sixth command to be written
// would forget. That is not hypothetical: the warnings were computed and
// returned on Body.Lossy for the whole of the port and nothing ever read
// them, so a Markdown body that lost an image posted silently.
//
// cli.AddWarning is the route rather than a return value because a
// Spec.Run handler has no warning sink — its own doc comment
// (internal/cli/warnings.go) records why, and this is the same mechanism
// `transition` and the `@me` resolver use.
//
// Body.Lossy is still returned, unchanged. Publishing is what EVERY
// command wants; deciding to refuse on a loss is what one future command
// (`--adf-strict`) will want, and that decision needs the typed warnings
// in hand rather than a slice already drained into a process-wide sink.
func ConvertMarkdown(flag, markdown string) (Body, error) {
	body, err := encodeMarkdown(flag, markdown)
	if err != nil {
		return Body{}, err
	}
	for _, w := range body.Lossy {
		cli.AddWarning(w)
	}
	return body, nil
}

// encodeMarkdown converts Markdown to ADF — or reports that it cannot.
//
// **Where the lossy warnings go is DECIDED, not left open.** The
// converter reports the Markdown constructs ADF cannot represent, and
// they belong in the envelope's top-level warnings[] under the
// converter's own taxonomy code — ONE entry per construct, for the whole
// run, never one per key. ConvertMarkdown above is where that happens.
//
// That placement is a real choice and the reason is compose-once. A
// keyed write resolves the body a SINGLE time before the fan-out starts
// (see issue.comment.add's runCommentAdd), so N keys share one document
// and one conversion. There is exactly one set of losses, and folding it
// into each results[] entry would republish one fact N times — the same
// argument that keeps data.body at the top of that envelope rather than
// inside each result.
//
// It is deliberately the OPPOSITE of the keyed-pagination rule, which
// folds into the child object at results[].data. Pagination is genuinely
// per key: each key has its own page. A body conversion is not. If a
// future keyed write ever converts a DIFFERENT body per key, its
// warnings move into the child object and this reasoning is what says
// so.
//
// Refusing a lossy conversion outright was considered and rejected: a
// dropped footnote does not justify refusing to post a comment somebody
// wrote. That is a per-command decision (a future --adf-strict), which is
// why Body still hands the typed warnings back after publishing them.
//
// Two things it must NOT do. It must not hand-roll a converter: a
// paragraph-only encoder would silently drop a caller's lists, tables
// and code blocks, and the dozen writes that copy this command would
// copy the silence. And it must not marshal the resulting document with
// json.Marshal — adf's own writer emits <, >, & and U+2028/U+2029 raw on
// purpose (writeJSONString, pinned by
// TestAuthoredTextKeepsLineSeparatorsRaw), while encoding/json compacts
// a Marshaler's output through its HTML escaper, so the outer call would
// re-escape exactly the bytes the inner one preserved and no later
// encoder could undo it. Call the document's own MarshalJSON.
//
// A conversion failure is the CALLER's fault, not the converter's: the
// only way FromMarkdownLossy errors is input it cannot parse, so this
// reports it as a bad flag value naming the flag actually typed, rather
// than an internal error the caller can do nothing with.
func encodeMarkdown(flag, markdown string) (Body, error) {
	doc, lossy, err := adf.FromMarkdownLossy(markdown)
	if err != nil {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s could not be converted to ADF: %v", flag, err,
		))
		e.Field = flag
		return Body{}, e
	}

	encoded, err := doc.Marshal()
	if err != nil {
		// Not a caller error: the converter built a document this package
		// then failed to serialize, which is a defect in one of the two,
		// not something a different --%s value would fix.
		return Body{}, fmt.Errorf("command: encoding converted markdown: %w", err)
	}

	return Body{ADF: encoded, Route: RouteMarkdown, Lossy: toEnvelopeWarnings(lossy)}, nil
}

// toEnvelopeWarnings maps the converter's losses onto the type they are
// published as, keeping adf's vocabulary out of the CLI contract (see
// Body.Lossy).
//
// Lossy is CARRIED ACROSS, not asserted. The converter raises two kinds
// (adf.Warning's own doc comment argues the split): warn means content
// the author wrote is gone from the document, warnDowngrade means it
// survives in a different shape — an image as a link, a table hoisted out
// of a list item. Flattening both to true, which this did, told a caller
// their table had been dropped when every cell of it was still there, and
// left the one caller who genuinely lost content indistinguishable from
// them. envelope.Warning serializes the field even when false precisely so
// a consumer can branch on it, so the bit has to be true.
//
// Path is dropped rather than folded into the message. It is a Markdown
// source position ("line 3, col 1"), which is meaningful for --markdown
// typed at a shell and misleading for --markdown-file, where the line
// belongs to a file the envelope never names — and the converter's own
// message already source-maps when it knew a position.
func toEnvelopeWarnings(lossy []adf.Warning) []envelope.Warning {
	return xslices.Map(lossy, func(w adf.Warning) envelope.Warning {
		return envelope.Warning{Type: w.Type, Message: w.Message, Lossy: w.Lossy}
	})
}

// ReadInputFileContext reads one flag's file, or standard input for StdinPath,
// under the shared size cap and terminal refusal. Cancellation closes an owned
// pipe duplicate or another closable stream to interrupt blocked reads. Borrowed
// pipe descriptors remain open and retain their original flags. Non-closable
// readers must unblock themselves; file opening retains OS semantics.
func ReadInputFileContext(ctx context.Context, flag, path string, stdin io.Reader, stdinIsTerminal bool) ([]byte, error) {
	return readInputFileContext(ctx, flag, "--"+flag, path, stdin, stdinIsTerminal)
}

// ReadArgumentFileContext applies the same size, cancellation and terminal
// rules to a positional file argument, without inventing a flag in errors.
func ReadArgumentFileContext(ctx context.Context, name, path string, stdin io.Reader, stdinIsTerminal bool) ([]byte, error) {
	return readInputFileContext(ctx, name, name, path, stdin, stdinIsTerminal)
}

func readInputFileContext(ctx context.Context, field, label, path string, stdin io.Reader, stdinIsTerminal bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == StdinPath {
		if stdinIsTerminal {
			// Draining a terminal would sit there with no prompt, which
			// reads as a hang rather than as a request for input — the same
			// refusal auth login's token read makes, for the same reason.
			e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
				"%s %s reads from standard input, but standard input is a terminal — pipe or redirect the text in, or name a file",
				label, StdinPath,
			))
			e.Field = field
			return nil, e
		}
		return readCappedContext(ctx, field, label, "standard input", stdin)
	}
	file, err := os.Open(path) //nolint:gosec // the path is the caller's own, named on their own command line
	if err != nil {
		e := openFailure(field, label, path, err)
		e.Field = field
		return nil, e
	}
	defer file.Close() //nolint:errcheck // read-only; a close failure loses nothing
	return readCappedContext(ctx, field, label, path, file)
}

func readCappedContext(ctx context.Context, field, label, source string, reader io.Reader) (data []byte, err error) {
	reader, cleanup, err := preparePipeInput(reader)
	if err != nil {
		failure := cli.NewInputError(cli.CodeLocalIO, fmt.Sprintf("%s: cannot prepare %s: %v", label, source, err))
		failure.Field = field
		return nil, failure
	}
	if cleanup != nil {
		defer func() {
			if cleanupErr := cleanup(); cleanupErr != nil {
				failure := cli.NewInputError(cli.CodeLocalIO, fmt.Sprintf("%s: cannot release %s: %v", label, source, cleanupErr))
				failure.Field = field
				err = errors.Join(err, failure)
			}
		}()
	}
	if closer, ok := reader.(io.ReadCloser); ok {
		stop := context.AfterFunc(ctx, func() {
			_ = closer.Close() //nolint:errcheck // cancellation owns the outcome; close only interrupts IO
		})
		defer stop()
	}
	data, err = readCapped(field, label, source, reader)
	if canceled := ctx.Err(); canceled != nil {
		return nil, canceled
	}
	return data, err
}

// openFailure classifies a failure to open one route's file, and the
// SPLIT it makes is the point.
//
// A file that is not there means the VALUE is wrong — the caller named
// something that does not exist — so it is a bad flag value, whose static
// hint ("that value isn't the type the flag expects, run with --help")
// actually fits. Anything else (a permission denial, a genuine I/O error)
// really is a local file operation failing, and CodeLocalIO's hint about
// free space and permissions fits that and only that.
//
// They were one code before, and the miss was not academic. The commonest
// mistake on this flag is passing INLINE JSON where a path belongs —
// `--json-input '{"fields":…}'` — which produced "A local file operation
// failed; check free space and the permissions on the config directory".
// Free space and the config directory have nothing to do with it, and a
// hint that sends someone to look at their disk is worse than none.
//
// The MESSAGE carries the recovery the static hint cannot: it names the
// two things the flag accepts, so the likeliest mistake becomes
// self-correcting. Hints are static per code by design (they never
// interpolate runtime values), which is exactly why the actionable half
// has to live here.
func openFailure(field, label, path string, err error) *cli.InputError {
	if errors.Is(err, fs.ErrNotExist) {
		if field == label {
			return cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf("%s takes a path to a file, or '-' for standard input; %q is not a file", label, path))
		}
		return cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"%s takes a path to a file, or `%s` to read standard input, and %q is neither — pass a filename, or pipe the content in with `%s %s`",
			label, StdinPath, path, label, StdinPath,
		))
	}
	return cli.NewInputError(cli.CodeLocalIO, fmt.Sprintf("%s: cannot read %s: %v", label, path, err))
}

// readCapped reads at most maxBodyBytes, refusing anything larger rather
// than silently truncating it — a body cut in half is worse than a body
// refused, because the caller would find out only by reading what Jira
// stored.
func readCapped(field, label, source string, r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBodyBytes+1))
	if err != nil {
		e := cli.NewInputError(cli.CodeLocalIO, fmt.Sprintf("%s: cannot read %s: %v", label, source, err))
		e.Field = field
		return nil, e
	}
	if len(data) > maxBodyBytes {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"%s: %s is larger than the %d-byte body limit", label, source, maxBodyBytes,
		))
		e.Field = field
		return nil, e
	}
	return data, nil
}

// ResolveAliases folds every hidden body alias into its canonical flag:
// the shared mechanism (ResolveHiddenAliases, aliases.go) applied to
// THIS bundle's own spellings.
//
// It passes the MAPPED table rather than the package-level one, and that
// is the whole reason this wrapper exists: on transition, AsComment
// renamed the pair, so --body means --comment there and folding it into
// a --markdown that command does not have would fail looking the
// canonical flag up.
func (f *BodyFlags) ResolveAliases(fs *pflag.FlagSet) error {
	return ResolveHiddenAliases(fs, bodyAliasesFor(f.names))
}

// Conflict enforces §2.1's rule: the three routes are alternatives, so
// two of them together is a refusal rather than a merge.
//
// A blanket refusal between ADF and Markdown, not a key-level one. The
// body can arrive as fields.description OR update.description in a
// native payload, so a check that tried to merge would have to
// understand both shapes and decide which wins; refusing outright is
// simpler to reason about and impossible to get subtly wrong.
//
// Run from Spec.Validate, AFTER ResolveAliases — an alias folded into
// its canonical flag must be visible to this check, or --body alongside
// --json-input would slip through.
func (f *BodyFlags) Conflict() error {
	switch {
	case f.JSONInput != "" && f.Markdown != "":
		return f.refuse(f.names.jsonInput, f.names.markdown)
	case f.JSONInput != "" && f.MarkdownFile != "":
		return f.refuse(f.names.jsonInput, f.names.file)
	case f.Markdown != "" && f.MarkdownFile != "":
		return f.refuse(f.names.markdown, f.names.file)
	}
	return nil
}

// refuse builds the two-route refusal. Field names the FIRST flag rather
// than either arbitrarily: the routes are listed most-canonical-first in
// Conflict above (ADF is the lossless route, inline Markdown before its
// file form), so the field points at the one a caller is likelier to
// have meant to keep.
func (f *BodyFlags) refuse(a, b string) error {
	e := cli.NewInputError(cli.CodeFlagConflict, fmt.Sprintf(
		"--%s and --%s both set the body, and it can only come from one place — pick one and drop the other",
		a, b,
	))
	e.Field = a
	return e
}

// Given reports whether any body route was chosen. A command that
// requires a body asks this from Validate, so "no body at all" is
// refused before any client is built rather than discovered mid-run.
func (f *BodyFlags) Given() bool {
	return f.JSONInput != "" || f.Markdown != "" || f.MarkdownFile != ""
}

// RequireBody refuses a command invocation that named no body route at
// all, naming every route the command actually offers — which is why it
// asks FlagNames rather than listing three spellings of its own: a bundle
// built with WithoutADFRoute offers two, and telling a caller to reach for
// a third would send them after a flag that does not exist.
func (f *BodyFlags) RequireBody(what string) error {
	if f.Given() {
		return nil
	}
	routes := xslices.Map(f.FlagNames(), func(n string) string { return "--" + n })
	e := cli.NewInputError(cli.CodeFlagRequired, fmt.Sprintf(
		"%s needs a body — give one with %s or %s",
		what, strings.Join(routes[:len(routes)-1], ", "), routes[len(routes)-1],
	))
	e.Field = f.names.markdown
	return e
}

// FlagNames returns this bundle's canonical spellings, for a command that
// must name them in its own prose. Ordered as Conflict lists them, and
// carrying only the routes actually REGISTERED — a bundle built with
// WithoutADFRoute has no --json-input, and naming one in a refusal or a
// resolution loop would point at a flag that is not there.
func (f *BodyFlags) FlagNames() []string {
	names := make([]string, 0, 3)
	if f.names.jsonInput != "" {
		names = append(names, f.names.jsonInput)
	}
	return append(names, f.names.markdown, f.names.file)
}

// String renders the bundle's spellings for a doc comment or a test
// failure. Not part of the contract; a convenience so a message about
// this struct can say which command's spellings it holds.
func (f *BodyFlags) String() string { return strings.Join(f.FlagNames(), ",") }
