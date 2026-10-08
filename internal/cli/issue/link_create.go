package issue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

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

// This command's own flag names, spelled once so registration and every
// refusal below name the same strings.
const (
	flagTo   = "to"
	flagType = "type"
)

// There is deliberately no `--generate-json` here, and the reasoning is
// worth recording because acli has one (matrix:394) and the gap is real.
//
// The name is RESERVED for the design's §2.1 live-discovery feature and
// must not be spent on a local one — `create` and `edit` shipped a local
// version, found it byte-identical to `--dry-run`, and removed it (see
// write.go). A link version would land on one of two branches and neither
// earns the name:
//
//   - LOCAL — compose the link body and print it. That is what `--dry-run`
//     does here already, and the same duplicate §7 rejects `--custom` for.
//   - LIVE — enrich the skeleton from the site. For a link, the only
//     site-resolved value is the link TYPE, and this CLI already ships two
//     ways to learn it: `jira issue link types` lists every phrasing, and
//     `--dry-run --validate-remote` resolves the one you typed and reports
//     what it became. A third spelling would add no capability.
//
// The genuine gap acli's flag covers on `create`/`edit` is createmeta —
// required fields, allowed values, real custom field ids. The link endpoint
// has no equivalent: its body is a type, two keys and an optional comment,
// all of which the help text names outright.

// linkPayloadOwnedKeys are the members of a link body that this command
// composes itself, so a `--json-input` document naming one is refused.
//
// The two ends are owned because the POSITIONALS and `--to` already say
// which issues are involved, and this is a keyed command: one document
// cannot name the ends for N sources without meaning something different
// per key. `type` is owned because `--type` sets it, and §2.1's rule is
// that two routes setting one value is a refusal rather than a precedence
// puzzle.
//
// What the document is FOR is what remains: `comment`, which the flag
// surface cannot express at all.
var linkPayloadOwnedKeys = []struct {
	key string
	// why names the route that already sets this member, so the refusal
	// tells a caller what to drop AND what sets it instead.
	why string
}{
	{"inwardIssue", "the issue keys you pass as arguments are one end of the link"},
	{"outwardIssue", "--" + flagTo + " is the other end of the link"},
	{"type", "--" + flagType + " is the relationship"},
}

// newLinkCreate returns `jira issue link create`.
//
// THE FIRST WRITE WHOSE REQUEST SHAPE DEPENDS ON SITE CONFIGURATION
// rather than on the issues involved. Two decisions carry this file and
// both are argued where they are made:
//
//   - the caller writes a SENTENCE and never learns Jira's inward /
//     outward vocabulary (resolveLinkType, and jira.LinkIssueRequest for
//     what the vocabulary actually means);
//   - resolving that sentence needs a live read, so a bare `--dry-run`
//     reports what it CANNOT know rather than guessing — the same split
//     `issue transition` already makes for screen state (design §3.1).
func newLinkCreate(deps Deps) *cobra.Command {
	var keyed command.KeyedFlags
	// WithValidateRemote and nothing else. --force has nothing to skip
	// (linking is additive and reversible with `link delete`), --verify
	// has no readback to gate — the endpoint answers 201 with no body, so
	// there is nothing to confirm without a second list call — and
	// --no-readback would suppress a readback this command never makes.
	// The remote flag earns its place because the link TYPE is knowable
	// only from the site, so a preview that wants to report the resolved
	// relationship has to be allowed to ask.
	var mut command.MutationFlags
	var to, linkType, jsonInput string
	var linkTypes linkTypeOptions
	var cmd *cobra.Command

	return command.MustNew(command.Spec[envelope.IssueLinkCreateOutput]{
		Op:         "issue.link.create",
		WritesJira: true,
		Identity:   true,
		// Identity is declared even though nothing sits at data.issue,
		// because on a KEYED command the identity slot is
		// data.results[].issue — the same placement every other keyed
		// command uses, and what the audit's M1 axis checks. It does NOT
		// refer to data.to, which is the link's other end rather than the
		// operation's subject.
		Keyed: true,
		Use:   "create ISSUE_KEY...",
		Short: "Link issues to another issue",
		Long: "Create a typed link from one or more issues to another.\n\n" +
			"**You write the relationship as a sentence, and never learn Jira's " +
			"own vocabulary for it.** `jira issue link create KEY-123 --to " +
			"KEY-456 --type blocks` reads left to right: KEY-123 blocks KEY-456. " +
			"Underneath, Jira names the two ends of a link \"inward\" and " +
			"\"outward\" and leaves it to the caller to work out which is which; " +
			"this command does that for you and prints the sentence it made so " +
			"you can see it landed the way round you meant.\n\n" +
			"`--type` takes either a link type's NAME (`Blocks`) or either of " +
			"its two phrasings (`blocks`, `is blocked by`), matched without " +
			"regard to case. The phrasing is what picks the direction: `--type " +
			"'is blocked by'` links the other way round from `--type blocks` " +
			"with the same type. Giving the bare name reads in the type's " +
			"forward direction, which is the one Jira's own UI lists first.\n\n" +
			"Link types are configured per site, so the list of what `--type` " +
			"accepts comes from your site rather than from this CLI. A type it " +
			"does not have is refused with the available phrasings listed, " +
			"rather than sent and rejected by Jira with a message about a " +
			"payload you did not write.\n\n" +
			"This is a keyed command: one entry per key at `data.results[]`, in " +
			"the order you gave them. Every key links to the SAME `--to` issue, " +
			"which is the shape that makes a list of keys mean something — " +
			"\"these three all block the release ticket\". A key that fails does " +
			"not fail the others, and the envelope names which ones took the " +
			"link so a retry does not double up.\n\n" +
			"Total failure, cancellation and timeout retain the resolved " +
			"relationship and all joined per-key outcomes in the full failure " +
			"frame. Inspect failed or uncertain targets before retrying; " +
			"Jira permits duplicate links.\n\n" +
			"`--json-input` takes a native link payload, and it exists for one " +
			"member the flags cannot express: `comment`, which posts a comment " +
			"as the link is made, in the same request. It takes a file path, or " +
			"`-` to read the document from standard input. The link's two ends " +
			"and its type are NOT read from the document — the issue keys, " +
			"`--to` and `--type` set those — and a document naming any of them " +
			"is refused rather than silently overridden.\n\n" +
			"Each entry's `data` is empty, and that is Jira's doing: its link " +
			"endpoint answers with no body at all, so the new link has no id " +
			"until you read it back with `jira issue link list`. That id is what " +
			"`jira issue link delete` takes.\n\n" +
			"`--dry-run` composes the request and stops without contacting " +
			"Jira, so on its own it can report the issues and the type you TYPED " +
			"but not what that type resolves to — link types live on the site. " +
			"`data.type_resolved` says which of those you are looking at. Add " +
			"`--validate-remote` and the preview reads the type list and fills " +
			"in the relationship, still writing nothing.\n\n" +
			"`--web` is refused here. It opens one page and this command writes " +
			"to a list of keys.",
		Example: "# one issue blocks another\n" +
			"$ jira issue link create KEY-123 --to KEY-456 --type blocks\n\n" +
			"# the same link the other way round, with the same type\n" +
			"$ jira issue link create KEY-123 --to KEY-456 --type 'is blocked by'\n\n" +
			"# several issues all blocking one release ticket\n" +
			"$ jira issue link create KEY-123 KEY-124 KEY-125 --to KEY-999 --type blocks\n\n" +
			"# by type name, which reads in the type's forward direction\n" +
			"$ jira issue link create KEY-123 --to KEY-456 --type Relates\n\n" +
			"# link, and say why in a comment posted with it\n" +
			"$ jira issue link create KEY-123 --to KEY-456 --type blocks --json-input link.json\n\n" +
			"# the same, reading the payload from standard input\n" +
			"$ echo '{\"comment\":{\"body\":{\"type\":\"doc\",\"version\":1,\"content\":[]}}}' | jira issue link create KEY-123 --to KEY-456 --type blocks --json-input -\n\n" +
			"# check the relationship resolves before writing anything\n" +
			"$ jira issue link create KEY-123 --to KEY-456 --type blocks --dry-run --validate-remote\n\n" +
			"# just the keys that took the link, as json\n" +
			"$ jira issue link create KEY-123 KEY-124 --to KEY-999 --type blocks --output=json --jq '.data.results[] | select(.ok) | .issue.key'",
		Args: command.ArgSpec{Min: 1, Max: command.AnyArgs, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
		Flags: func(c *cobra.Command) {
			cmd = c
			command.AddKeyedFlags(c, &keyed)
			command.AddMutationFlags(c, &mut, command.WithValidateRemote())
			command.AddStringVar(c, &to, flagTo, "",
				"The issue at the other end of the link",
				command.FlagOpts{Terse: "Link target", Placeholder: "key", Complete: "predictor=issuekey"})
			command.AddStringVar(c, &linkType, flagType, "",
				"The relationship, as a link type name or either of its phrasings",
				command.FlagOpts{Terse: "Relationship", Placeholder: "type", Complete: "predictor=cachelinktype"})
			// The type list this resolves against is cached, so the two
			// flags that govern the cache belong here as well as on
			// `link types` — a caller who suspects a stale list wants to
			// bust it from the command that refused them, not from a
			// different one.
			addLinkTypeCacheFlags(c, &linkTypes)
			// The §2.1 ADF route. Same flag name and same shared alias table
			// as every other command carrying it, so a caller who learned it
			// on `issue create` reaches for it here and it works.
			command.AddStringVar(c, &jsonInput, command.FlagJSONInput, "",
				"Read a native link payload — for the link `comment` the flags cannot express — from this JSON file, or `-` for standard input",
				// "Link payload" rather than create/edit's "Payload from
				// file": the flag name is shared across the tree but the
				// DOCUMENT is not, and this terse is what completion shows
				// beside the flag. Two commands offering --json-input with
				// an identical one-line description, one taking an issue
				// payload and one a link body, is the drift worth spending
				// two words to avoid.
				command.FlagOpts{Terse: "Link payload", Placeholder: "path", Hint: "file"})
			command.AddHiddenAliases(c, command.ADFRouteAliases())
		},
		Validate: func() error {
			if err := keyed.Validate(); err != nil {
				return err
			}
			if err := resolvePayloadAliases(cmd.Flags()); err != nil {
				return err
			}
			if err := requireLinkTarget(to); err != nil {
				return err
			}
			if err := requireLinkType(linkType); err != nil {
				return err
			}
			return refuseWebOnKeyedWrite(cmd)
		},
		Human:         renderLinkCreate,
		RetainOnError: func(out envelope.IssueLinkCreateOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueLinkCreateOutput, error) {
			return runLinkCreate(ctx, deps, inv, linkCreateOptions{
				to:             to,
				typeInput:      linkType,
				jsonInput:      jsonInput,
				linkTypes:      linkTypes,
				validateRemote: mut.ValidateRemote,
				parallelism:    keyed.Parallelism,
			})
		},
	})
}

// linkCreateOptions is what runLinkCreate needs from the flag set,
// gathered into one struct so the handler's signature does not grow a
// parameter per flag — commentAddOptions' own reasoning, and --dry-run
// is absent for its reason too: it arrives on the Invocation.
type linkCreateOptions struct {
	// linkTypes carries --refresh and --ttl-minutes, which govern the
	// cached type list this command resolves against.
	linkTypes linkTypeOptions
	to        string
	typeInput string
	// jsonInput is a path to a file holding a native link payload, or "-"
	// for standard input. It exists for the `comment` member, which the
	// flag surface cannot express; every other member of a link body is
	// composed by this command and refused in the document.
	jsonInput      string
	validateRemote bool
	parallelism    int
}

// readLinkPayload parses --json-input into the one member this command
// does not otherwise compose: the link's comment.
//
// **A native link body, checked rather than merged.** The document is the
// endpoint's own shape — the same one acli and 0.15 accept — so a caller
// with a link body already written can pass it here. Every member this
// command composes itself is refused rather than silently overridden,
// which is §2.1's conflict rule applied to a payload whose keys are fixed
// and few enough to name individually.
//
// Unknown members are ACCEPTED and ignored, matching
// issueWriteFlags.readPayload's reasoning: this is a Jira payload and
// rejecting what the CLI does not model would make the route lossy in the
// dimension it exists to protect.
func readLinkPayload(ctx context.Context, path string, stdin io.Reader, stdinIsTerminal bool) (*jira.AddCommentRequest, error) {
	if path == "" {
		return nil, nil
	}
	data, err := command.ReadInputFileContext(ctx, command.FlagJSONInput, path, stdin, stdinIsTerminal)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s: %s is not a valid link payload: %v", command.FlagJSONInput, path, err))
		e.Field = command.FlagJSONInput
		return nil, e
	}
	for _, owned := range linkPayloadOwnedKeys {
		if _, ok := raw[owned.key]; !ok {
			continue
		}
		e := cli.NewInputError(cli.CodeFlagConflict, fmt.Sprintf(
			"--%s names %q, which this command composes itself — %s. Drop it from the document and let the command set it.",
			command.FlagJSONInput, owned.key, owned.why,
		))
		e.Field = command.FlagJSONInput
		return nil, e
	}
	body, ok := raw["comment"]
	if !ok {
		// A document naming nothing this command can use is a caller error
		// rather than a no-op: they meant something by passing it, and
		// silently ignoring the file would make a typo look like a success.
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s: %s sets nothing this command uses. The link's ends and type come from the arguments, --%s and --%s, so `comment` is what the document is for.",
			command.FlagJSONInput, path, flagTo, flagType,
		))
		e.Field = command.FlagJSONInput
		return nil, e
	}
	var comment jira.AddCommentRequest
	if err := json.Unmarshal(body, &comment); err != nil {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s: the `comment` in %s is not a valid comment: %v", command.FlagJSONInput, path, err))
		e.Field = command.FlagJSONInput
		return nil, e
	}
	if len(comment.Body) == 0 {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s: the `comment` in %s has no `body`, and Jira rejects a comment without one", command.FlagJSONInput, path))
		e.Field = command.FlagJSONInput
		return nil, e
	}
	return &comment, nil
}

// requireLinkTarget refuses an invocation naming no other end.
func requireLinkTarget(to string) error {
	if to != "" {
		return nil
	}
	e := cli.NewInputError(cli.CodeFlagRequired, fmt.Sprintf(
		"a link joins two issues — name the other one with --%s", flagTo,
	))
	e.Field = flagTo
	return e
}

// requireLinkType refuses an invocation naming no relationship.
//
// There is no default, deliberately. "Relates" is the obvious candidate
// and it is still wrong to assume: a link the caller did not ask for is
// a relationship somebody else will read as deliberate, and the type
// list is per site so even "Relates" is not guaranteed to exist.
func requireLinkType(linkType string) error {
	if linkType != "" {
		return nil
	}
	e := cli.NewInputError(cli.CodeFlagRequired, fmt.Sprintf(
		"say how the issues relate with --%s — a name like `Blocks` or a phrase like `blocks` or `is blocked by`", flagType,
	))
	e.Field = flagType
	return e
}

// resolvedLinkType is what the caller's `--type` turned into: the site's
// own type, the phrase the link reads with from the named issue toward
// the target, and whether the two ends swap in the request.
//
// A value rather than three returns, because the three are one answer
// and separating them is how a caller uses the phrase from one type with
// the swap from another.
type resolvedLinkType struct {
	view envelope.IssueLinkTypeView
	// relation is the phrase the link reads with, from the named issue
	// toward the target — exactly what IssueLinkView.Relation publishes
	// on the read side for the same link.
	relation string
	// swapped reports that the caller's phrase was the type's INWARD
	// description, so the request's two ends are the reverse of the
	// sentence's. See requestFor, which is the only place it is read.
	swapped bool
}

// requestFor builds the wire request for one source issue.
//
// **This is the whole inward/outward mapping, in one place.** A link
// reads `inwardIssue <outward-phrase> outwardIssue` (jira.LinkIssueRequest
// records how that was derived from the read side), so the sentence
// "SOURCE relation TARGET" puts SOURCE on the inward side when the
// caller's phrase was the type's OUTWARD one — and the other way round
// when they typed the inward phrase, which is what makes `--type 'is
// blocked by'` mean the reverse of `--type blocks`.
//
// One function, so a future reader changing the mapping changes it once
// and the tests that pin the sentence catch a reversal immediately.
// The comment, when --json-input carried one, rides along unswapped: it
// is posted on whichever issue Jira attaches it to, and reversing the ends
// does not change which comment was written.
func (r resolvedLinkType) requestFor(source, target string, comment *jira.AddCommentRequest) jira.LinkIssueRequest {
	req := jira.LinkIssueRequest{
		Type:         jira.LinkTypeRef{Name: r.view.Name},
		InwardIssue:  jira.LinkIssueRef{Key: source},
		OutwardIssue: jira.LinkIssueRef{Key: target},
		Comment:      comment,
	}
	if r.swapped {
		req.InwardIssue, req.OutwardIssue = req.OutwardIssue, req.InwardIssue
	}
	return req
}

// runLinkCreate resolves the relationship once, then applies it to every
// key.
//
// **Resolve once, link N times**, which is runCommentAdd's compose-once
// with a sharper edge: the type list is a SITE read, so resolving per
// key would pay for the same answer N times and could — on a site
// reconfigured mid-run — give two keys different relationships.
//
// **The bare preview builds no client**, runCommentAdd's property
// unchanged, with runTransition's one documented exception: `--dry-run
// --validate-remote` does build one, because the question that
// combination exists to answer (what does this type resolve to?) is
// answerable only by the site. It still writes nothing — the remote
// preview reads the type list and stops, and nothing on that path can
// reach LinkIssues.
func runLinkCreate(ctx context.Context, deps Deps, inv command.Invocation, opts linkCreateOptions) (envelope.IssueLinkCreateOutput, error) {
	var zero envelope.IssueLinkCreateOutput

	out := envelope.IssueLinkCreateOutput{
		MutationState: envelope.MutationState{DryRun: inv.DryRun},
		To:            envelope.IssueRef{Key: opts.to},
		TypeInput:     opts.typeInput,
	}

	// Read before the preview returns, so a malformed document or one
	// naming a member this command owns is refused by `--dry-run` too —
	// which is the run a caller makes to find exactly that out. It reads a
	// FILE and contacts nobody, so the local-only preview is intact.
	comment, err := readLinkPayload(ctx, opts.jsonInput, inv.Stdin, inv.StdinIsTerminal)
	if err != nil {
		return zero, err
	}

	if inv.DryRun && !opts.validateRemote {
		// Nothing resolved and nothing claimed. TypeResolved stays false,
		// which is what tells a caller that an absent type means "not
		// checked" rather than "not found" — and the warning says the same
		// thing to a consumer that reads warnings[] rather than branching
		// on that field.
		//
		// Both, not either. The design's §8 item 0 asks for the warning,
		// and the machine-readable flag is what a caller BRANCHES on;
		// publishing only the flag left a consumer watching warnings[] with
		// nothing to see, which is the case the ruling exists to cover.
		cli.AddWarning(linkTypeUnresolvedWarning(opts.typeInput))
		out.Results = linkPreviewResults(inv.Args)
		return out, nil
	}

	client, err := deps.JiraClient()
	if err != nil {
		return zero, err
	}
	resolved, err := resolveLinkType(ctx, client, issueCache(deps), client.BaseURL(), opts.typeInput, opts.linkTypes)
	if err != nil {
		return zero, err
	}
	out.TypeResolved = true
	out.Type = &resolved.view
	out.Relation = resolved.relation

	if inv.DryRun {
		// The remote preview stops here: it has answered the only question
		// it exists to answer, and the code that writes is not on this
		// path.
		out.Results = linkPreviewResults(inv.Args)
		return out, nil
	}

	out.Results, err = linkCreateFanOut(ctx, client, inv.Args, opts.to, resolved, comment, opts.parallelism)
	return out, err
}

// linkTypeUnresolvedWarning is the heads-up a bare preview publishes in
// place of the type lookup it deliberately did not perform.
//
// Shaped after command.MeUnresolvedWarning and
// envelope.WarningFilenameUnresolved, which is the point rather than
// coincidence: the design's §8 item 0 settles this once for every value
// only the site can resolve, and a caller who has met one of them should
// recognize the next without reading its documentation.
//
// Lossy is false for their reason too: nothing was dropped, the value is
// simply not known yet and will be when the command runs for real.
func linkTypeUnresolvedWarning(typeInput string) envelope.Warning {
	return envelope.Warning{
		Type: envelope.WarningLinkTypeUnresolved,
		Message: fmt.Sprintf(
			"--%s is left as %q in this preview: resolving it reads the site's link types, which --dry-run never does. A live run resolves it; add --%s to resolve it here.",
			flagType, typeInput, command.FlagValidateRemote,
		),
		Lossy: false,
	}
}

// resolveLinkType turns what the caller typed into the site's own type
// and a direction.
//
// **Three spellings are accepted and they do not rank equally.** A
// type's OUTWARD phrase and its NAME both read forward, so either gives
// the sentence the caller wrote; the INWARD phrase reverses it. Matching
// is case-insensitive because Jira's own UI title-cases what an admin
// typed and nobody should have to guess which casing was stored.
//
// **An ambiguous match is refused rather than resolved by order.** Two
// different types can share a phrase on a site with custom link types —
// a stock "Blocks" (inward "is blocked by") beside a custom type whose
// outward phrase is the same words — and picking whichever came first in
// the response would make the link's direction depend on the order Jira
// happened to list its configuration in. Refusing names both, which is
// the only answer that lets the caller say which they meant.
// **The list is CACHED, and an unknown type busts the cache before it
// refuses.** The site's link types are configuration that changes when an
// admin edits it and not otherwise, so re-reading them on every link
// creation is a round trip spent re-learning a list that almost never
// moves (see DefaultLinkTypeTTL). But a cache over configuration has one
// characteristic failure: an admin adds a type, and the caller who uses it
// that afternoon is told their perfectly good type does not exist.
//
// So a miss against a CACHED list is not final. The one thing that
// distinguishes "this type does not exist" from "this cache is stale" is a
// fresh read, and the moment a caller names a type we do not recognize is
// exactly the moment worth paying for one. A second failure is a real
// refusal. This costs a round trip only on the path that was about to
// error anyway, and it is what makes the cache safe to have: the staleness
// it can cause is the staleness it heals itself.
func resolveLinkType(
	ctx context.Context,
	client core.JiraClient,
	cache core.Cache,
	site, input string,
	opts linkTypeOptions,
) (resolvedLinkType, error) {
	read, err := readLinkTypes(ctx, client, cache, site, opts)
	if err != nil {
		return resolvedLinkType{}, err
	}
	resolved, err := matchOneLinkType(read.types, input)
	if err == nil || !read.cached {
		return resolved, err
	}
	// Cached and unmatched: re-read before believing the refusal.
	fresh, freshErr := readLinkTypes(ctx, client, cache, site, linkTypeOptions{cacheOptions: cacheOptions{refresh: true, ttlMinutes: opts.ttlMinutes}})
	if freshErr != nil {
		// The re-read failed, so the cached verdict is the only one we
		// have. Returning the ORIGINAL error rather than the network one
		// keeps the message about the caller's input, which is still the
		// likeliest explanation.
		return resolvedLinkType{}, err
	}
	return matchOneLinkType(fresh.types, input)
}

// matchOneLinkType picks the single type answering to what the caller
// typed, or explains why it cannot.
func matchOneLinkType(available []jira.IssueLinkType, input string) (resolvedLinkType, error) {
	var matches []resolvedLinkType
	for _, t := range available {
		if m, ok := matchLinkType(t, input); ok {
			matches = append(matches, m)
		}
	}
	switch len(matches) {
	case 0:
		return resolvedLinkType{}, unknownLinkType(input, available)
	case 1:
		return matches[0], nil
	}
	return resolvedLinkType{}, ambiguousLinkType(input, matches)
}

// matchLinkType reports whether one type answers to what the caller
// typed, and which direction that reading implies.
//
// Outward before inward before name, and the order matters for exactly
// one case: a symmetric type like "Relates" phrases both directions the
// same way ("relates to"), so both branches would match and the forward
// one is the stable answer. For every asymmetric type the three are
// distinct and the order is irrelevant.
func matchLinkType(t jira.IssueLinkType, input string) (resolvedLinkType, bool) {
	view := envelope.IssueLinkTypeView{
		ID:      t.ID.Value,
		Name:    t.Name.Value,
		Inward:  t.Inward.Value,
		Outward: t.Outward.Value,
	}
	switch {
	case matchesPhrase(view.Outward, input), matchesPhrase(view.Name, input):
		return resolvedLinkType{view: view, relation: view.Outward}, true
	case matchesPhrase(view.Inward, input):
		return resolvedLinkType{view: view, relation: view.Inward, swapped: true}, true
	}
	return resolvedLinkType{}, false
}

// matchesPhrase compares one of a type's spellings against the caller's
// input, ignoring case and surrounding space. Empty never matches: a
// type with no inward phrase must not answer to `--type ""`, which
// Validate has already refused anyway.
func matchesPhrase(candidate, input string) bool {
	return candidate != "" && strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(input))
}

// unknownLinkType refuses a relationship the site does not have, naming
// the ones it does.
//
// The available phrasings ride Suggestions rather than the message,
// which is the taxonomy's own split (the offender rides Field, the
// candidates ride Suggestions) and matters here for unknownTransition's
// reason: what a site accepts is its own configuration, so the list IS
// the answer to "then what can I say?" and an agent should be able to
// read it without parsing prose.
//
// Both phrasings of every type are offered, not just the names, because
// the phrasing is what picks the direction — a caller shown only
// "Blocks" would have no way to discover that "is blocked by" links the
// other way.
func unknownLinkType(input string, available []jira.IssueLinkType) error {
	e := cli.NewInputError(cli.CodeArgValueInvalid, fmt.Sprintf(
		"this site has no link type matching %q", input,
	))
	e.Field = flagType
	e.Suggestions = linkTypePhrases(available)
	return e
}

// linkTypePhrases lists every spelling --type accepts on this site, in
// the order Jira returned the types, each type's forward phrasing before
// its reverse so the list reads as pairs.
func linkTypePhrases(available []jira.IssueLinkType) []string {
	out := make([]string, 0, len(available)*2)
	for _, t := range available {
		for _, phrase := range []string{t.Outward.Value, t.Inward.Value} {
			if phrase != "" {
				out = append(out, phrase)
			}
		}
	}
	return out
}

// ambiguousLinkType refuses a phrase that two of the site's types answer
// to. See resolveLinkType for why this is a refusal rather than a
// first-match.
func ambiguousLinkType(input string, matches []resolvedLinkType) error {
	names := xslices.Map(matches, func(m resolvedLinkType) string { return m.view.Name })
	e := cli.NewInputError(cli.CodeArgValueInvalid, fmt.Sprintf(
		"%q matches more than one link type on this site (%s), so the direction it means is not decidable — name the type instead of the phrase",
		input, strings.Join(names, ", "),
	))
	e.Field = flagType
	e.Suggestions = names
	return e
}

// linkPreviewResults builds the per-key entries for a preview: one per
// key, in argv order, each successful and each carrying an empty
// operation object.
//
// Empty on the preview AND on the live path, which is unusual enough to
// restate: IssueLinkCreateData has no members at all, because Jira's
// link endpoint answers 201 with no body. So unlike every other
// preview in this CLI, this one is not hiding anything a live run would
// have filled in.
func linkPreviewResults(keys []string) []envelope.KeyedResult[envelope.IssueLinkCreateData] {
	return xslices.Map(keys, func(key string) envelope.KeyedResult[envelope.IssueLinkCreateData] {
		return envelope.KeyedResult[envelope.IssueLinkCreateData]{
			Issue: envelope.IssueRef{Key: key},
			OK:    true,
			// A pointer to an empty struct rather than nil, for
			// previewResults' reason: the contract says a successful key
			// carries `data`, and a preview's key succeeded.
			Data: &envelope.IssueLinkCreateData{},
		}
	})
}

// linkCreateFanOut links every key to the target and assembles the keyed
// payload.
//
// The keyed partial-failure policy is commentAddFanOut's, unchanged and
// for its reasons — with one thing worth saying that is specific to
// links: re-running a failed key is safe, but re-running a SUCCEEDED one
// is not, because Jira permits two identical links between the same pair
// of issues and has no idempotency key to collapse them. results[] is
// therefore not just convenient here, it is the only thing standing
// between a partial failure and a duplicated link.
func linkCreateFanOut(
	ctx context.Context,
	client core.JiraClient,
	keys []string,
	to string,
	resolved resolvedLinkType,
	comment *jira.AddCommentRequest,
	parallelism int,
) ([]envelope.KeyedResult[envelope.IssueLinkCreateData], error) {
	outcomes := make([]linkCreateOutcome, len(keys))
	// ONE spinner around the whole fan-out, for commentAddFanOut's
	// reason: a spinner owns the terminal's live region and several
	// drawing at once interleave into corruption.
	_, err := command.Spin(ctx, linkCreateSpinnerLabel(keys, resolved.relation, to), func(ctx context.Context) (struct{}, error) {
		xsync.Parallel(parallelism, len(keys), func(i int) {
			outcomes[i] = linkOne(ctx, client, keys[i], to, resolved, comment)
		})
		return struct{}{}, nil
	})
	results := make([]envelope.KeyedResult[envelope.IssueLinkCreateData], len(outcomes))
	raw := make([]error, len(outcomes))
	for i, o := range outcomes {
		results[i], raw[i] = o.result, o.err
	}
	if err != nil {
		// Keep every joined acknowledgement or refusal without replacing
		// the command's interruption with a per-key classification.
		return results, err
	}
	return results, command.KeyedFailure(results, raw)
}

// linkCreateOutcome is one key's result plus the raw error behind a
// failure — commentAddOutcome's pairing, for its reason.
type linkCreateOutcome struct {
	result envelope.KeyedResult[envelope.IssueLinkCreateData]
	// err is nil exactly when result.OK.
	err error
}

// linkOne makes one key's link. It returns a result rather than an error
// because a failure here is data — the entry the caller reads at
// data.results[] — and not the command's own failure.
func linkOne(ctx context.Context, client core.JiraClient, key, to string, resolved resolvedLinkType, comment *jira.AddCommentRequest) linkCreateOutcome {
	if _, err := client.LinkIssues(ctx, resolved.requestFor(key, to, comment)); err != nil {
		return failedLinkCreateResult(key, err)
	}
	return linkCreateOutcome{result: envelope.KeyedResult[envelope.IssueLinkCreateData]{
		Issue: envelope.IssueRef{Key: key},
		OK:    true,
		// An empty operation object, for the reason
		// envelope.IssueLinkCreateData records: the 201 carries no body,
		// so there is genuinely nothing per key to report.
		Data: &envelope.IssueLinkCreateData{},
	}}
}

// failedLinkCreateResult records one key's failure, resolved through the
// SAME taxonomy the envelope's own errors[] entries use, so a caller
// branches on one vocabulary of codes wherever a failure appears.
func failedLinkCreateResult(key string, err error) linkCreateOutcome {
	class := cli.MapError(err)
	return linkCreateOutcome{
		result: envelope.KeyedResult[envelope.IssueLinkCreateData]{
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

// linkCreateSpinnerLabel is the live fan-out's one spinner label.
// Present tense, and correctly so: runLinkCreate returns before this
// fan-out on both preview paths, so no animation is ever drawn over a
// run that writes nothing.
func linkCreateSpinnerLabel(keys []string, relation, to string) string {
	if len(keys) == 1 {
		return fmt.Sprintf("Linking %s %s %s", keys[0], relation, to)
	}
	return fmt.Sprintf("Linking %d issues %s %s", len(keys), relation, to)
}

// renderLinkCreate writes the human view.
//
// Preview tense is structural, not a phrasing habit (axis H2): the
// preview and the live view are separate functions with no shared
// sentence, so a future edit to the live wording cannot leak a
// past-tense claim into the preview.
func renderLinkCreate(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.IssueLinkCreateOutput)
	if !ok {
		return fmt.Errorf("issue.link.create: human renderer received %T, want envelope.IssueLinkCreateOutput", env.Data)
	}
	if out.DryRun {
		renderLinkCreatePreview(v, out)
		return nil
	}
	renderLinkCreateResults(v, out)
	return nil
}

// renderLinkCreatePreview draws the dry-run view.
//
// It says the SENTENCE when the type resolved and the raw input when it
// did not, rather than papering over the difference with one phrasing
// that reads the same either way. A preview whose central fact is
// unverified has to say so — that is the whole reason type_resolved
// exists in the envelope.
func renderLinkCreatePreview(v *output.View, out envelope.IssueLinkCreateOutput) {
	// Each key is styled on its own — cli.Entity, one Render call per key
	// — and the plain connectives assembled around them, as SIBLING spans
	// on one line rather than nested (axis H5).
	keys := xslices.Map(out.Results, func(r envelope.KeyedResult[envelope.IssueLinkCreateData]) string {
		return cli.Entity(r.Issue.Key)
	})
	if out.TypeResolved {
		v.Line(fmt.Sprintf("Would link %s %s %s.",
			joinKeys(keys), cli.Text(out.Relation), cli.Entity(out.To.Key)))
		v.Hint("Nothing was sent. Drop --dry-run to apply it.")
		return
	}
	v.Line(fmt.Sprintf("Would link %s to %s as %s.",
		joinKeys(keys), cli.Entity(out.To.Key), cli.Text(out.TypeInput)))
	v.Hint("Link types live on the site, so this preview cannot say which relationship that resolves to — add --validate-remote to find out.")
	v.Hint("Nothing was sent. Drop --dry-run to apply it.")
}

// renderLinkCreateResults draws the live view: one line per key, in argv
// order, each stating the link as a sentence so a reversed direction is
// visible immediately rather than only in the Jira UI later.
//
// A failed key's message is Jira-authored in the common case, so it goes
// through cli.Text while the hint rides the message path —
// renderCommentAddResults' own split.
func renderLinkCreateResults(v *output.View, out envelope.IssueLinkCreateOutput) {
	target := cli.Entity(out.To.Key)
	for _, r := range out.Results {
		key := cli.Entity(r.Issue.Key)
		if !r.OK {
			v.Line(key + cli.Text(": "+r.Err.Message))
			v.Hint(r.Err.Hint)
			continue
		}
		v.Line(key + cli.Text(" "+out.Relation+" ") + target + cli.Text("."))
	}
}
