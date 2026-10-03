package issue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// There is deliberately no `--generate-json` here, and the name is being
// KEPT FREE rather than merely left unimplemented.
//
// A local one was built and removed. It composed the payload and printed
// it instead of sending — which is what `--dry-run` already does, byte for
// byte: the two envelopes differed only in a boolean. A second spelling of
// a flag this CLI already ships is the same fault §7 rejects `--custom`
// for, and the standard has to apply in both directions.
//
// `--dry-run` IS the discovery affordance. It publishes `data.payload`,
// and those bytes feed straight back through `--json-input`, so a caller
// learns the schema from the command they were already going to run.
//
// The name is reserved for the design's §2.1 feature, which is genuinely
// different: a LIVE createmeta/editmeta read that returns a skeleton
// enriched from the site — required flags, allowed values, real custom
// field ids — with a name-to-id resolution and pagination behind it. That
// feature deserves the good name, and it could not have had it if a local
// payload-printer were sitting on it.

// issueWriteFlags is the whole input surface `issue create` and `issue
// edit` share: the three routes of §2.1 plus the discovery flag.
//
// It exists because create and edit differ in their VERB and their
// envelope, not in how a caller describes an issue. Composing the payload
// is a single algorithm with a single conflict rule, and writing it twice
// is how the two commands would come to disagree about which flag beats
// which — the failure §9's "the 14 field flags are their own unit" warns
// about, one level up.
type issueWriteFlags struct {
	// body carries the Markdown route only. Registered with
	// command.WithoutADFRoute because --json-input means something
	// different here than it does on a comment — see jsonInput below.
	body command.BodyFlags
	// fields carries the third route: the §2.1 scalar and repeatable
	// field flags.
	fields command.IssueFieldFlags
	// jsonInput is a path to a file holding a whole ISSUE PAYLOAD — a
	// `fields`/`update` document — and NOT a bare ADF body.
	//
	// This command owns the flag rather than taking the registrar's,
	// because the meaning genuinely differs and help text an agent reads
	// must say which one it is getting. command.WithoutADFRoute's own doc
	// comment argues the case at length.
	jsonInput string

	// cmd is captured at registration so Validate can reach the parsed
	// flag set, which alias folding needs and Spec.Validate is not handed
	// — the same capture newCommentAdd and newWatchersList both make.
	cmd *cobra.Command
}

// register wires the whole input surface onto cmd for one verb.
func (f *issueWriteFlags) register(cmd *cobra.Command, verb command.IssueWriteVerb) {
	f.cmd = cmd
	command.AddBodyFlags(cmd, &f.body, command.WithoutADFRoute())
	command.AddIssueFieldFlags(cmd, &f.fields, verb)
	command.AddStringVar(cmd, &f.jsonInput, command.FlagJSONInput, "",
		"Read the whole native issue payload, including fields, update and other request members, from this JSON file, or `-` for standard input",
		command.FlagOpts{Terse: "Payload from file", Placeholder: "path"})

	// The ADF route's own aliases, registered against the flag THIS
	// command owns. They come from the shared table rather than a second
	// list, so §5 gaining a row reaches these commands too.
	command.AddHiddenAliases(cmd, command.ADFRouteAliases())
}

// validate runs every refusal that can be decided before any I/O.
//
// Ordering is load-bearing and matches the exemplar's: alias folding FIRST,
// so a value that arrived through a hidden spelling is visible to the
// conflict checks, then the checks themselves. `--body x --json-input f`
// slipping past a check that only sees canonical flags is the concrete bug
// this order prevents (see newCommentAdd's own Validate).
//
// The payload-versus-flag collision check is NOT here. It needs the parsed
// payload, and parsing it means reading a file — which Validate is not the
// place for, exactly as BodyFlags splits its own alias folding from its
// Resolve. It runs at the top of compose instead.
func (f *issueWriteFlags) validate() error {
	fs := f.cmd.Flags()
	if err := f.body.ResolveAliases(fs); err != nil {
		return err
	}
	if err := resolvePayloadAliases(fs); err != nil {
		return err
	}
	if err := f.fields.ResolveIssueFieldAliases(fs); err != nil {
		return err
	}
	if err := f.body.Conflict(); err != nil {
		return err
	}
	if err := f.routeConflict(); err != nil {
		return err
	}
	return f.fields.Conflict()
}

// resolvePayloadAliases folds --from-json and --body-adf into
// --json-input, following BodyFlags.ResolveAliases' rule exactly: agreeing
// signals are fine, a disagreement is refused, and the message names only
// the canonical flag because an alias is a spelling help never showed.
//
// Package-level rather than a method, because `issue clone` owns
// --json-input too and folds the same aliases into it. The table is shared
// (command.ADFRouteAliases), so the folding has to be as well — two copies
// of this loop is how one command would start accepting a spelling the
// other refused.
func resolvePayloadAliases(fs *pflag.FlagSet) error {
	for _, a := range command.ADFRouteAliases() {
		if !fs.Changed(a.Flag) {
			continue
		}
		alias := fs.Lookup(a.Flag).Value.String()
		canon := fs.Lookup(command.FlagJSONInput)
		if fs.Changed(command.FlagJSONInput) {
			if got := canon.Value.String(); got != alias {
				e := cli.NewInputError(cli.CodeFlagConflict, fmt.Sprintf(
					"--%s was given two different values (%q and %q) — it was set more than once, so pick the one you meant",
					command.FlagJSONInput, got, alias,
				))
				e.Field = command.FlagJSONInput
				return e
			}
			continue
		}
		if err := canon.Value.Set(alias); err != nil {
			return err
		}
	}
	return nil
}

// routeConflict enforces §2.1's blanket refusal between the ADF route and
// the Markdown route.
//
// **Blanket, not key-level**, and the design says why: an issue's
// description can arrive as `fields.description` OR
// `update.description[{"set": …}]`, so a check that tried to MERGE the two
// routes would have to understand both shapes and decide which wins.
// Refusing outright is simpler to reason about and impossible to get
// subtly wrong. It is the same call BodyFlags.Conflict makes for the same
// reason; it is repeated here only because the ADF flag now lives on this
// command rather than in that bundle.
func (f *issueWriteFlags) routeConflict() error {
	if f.jsonInput == "" {
		return nil
	}
	switch {
	case f.body.Markdown != "":
		return payloadRouteRefusal(command.FlagMarkdown)
	case f.body.MarkdownFile != "":
		return payloadRouteRefusal(command.FlagMarkdownFile)
	}
	return nil
}

func payloadRouteRefusal(markdownFlag string) error {
	e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
		"--%s carries the whole payload including the description, and --%s sets the description too — they are alternative routes rather than layers, so pick one and drop the other",
		command.FlagJSONInput, markdownFlag,
	))
	e.Field = command.FlagJSONInput
	return e
}

// resolveAccounts settles every `@me` before the payload is composed.
//
// Resolution happens BEFORE compose because compose bakes the value into
// the payload: resolving afterwards would publish a payload carrying the
// sentinel while sending one carrying the id, so the preview and the live
// request would disagree about the very field the flag set.
//
// The three-branch policy itself is command.ResolveAccounts' — shared with
// every other command that takes an account id, rather than restated here.
func (f *issueWriteFlags) resolveAccounts(
	ctx context.Context,
	deps Deps,
	dryRun, validateRemote bool,
) (core.JiraClient, error) {
	return command.ResolveAccounts(ctx, deps.JiraClient, dryRun, validateRemote, f.fields.AccountFields())
}

// composedWrite is what compose produces: the request to send and the two
// facts about it the envelope publishes.
type composedWrite struct {
	req   jira.IssueWriteRequest
	route string
	// payload is req marshaled once, so the bytes the envelope publishes
	// and the bytes the transport sends come from ONE encoding rather than
	// two that could differ in escaping. That equality is the whole claim
	// --dry-run makes.
	payload json.RawMessage
}

// compose builds the request from all three routes, in the one order that
// makes the conflict rule enforceable.
//
// **The payload is the BASE and the flags are checked against it, never
// merged over it.** Reading --json-input first is what lets the collision
// check see the whole document before a single flag has written anything —
// if flags were applied first, a colliding key would already have been
// overwritten and there would be nothing left to refuse.
//
// The description is folded in before the field flags for no deep reason:
// neither can collide with the other (the field flags have no
// --description; that spelling is a hidden alias for --markdown), so the
// order is arbitrary and the code reads better with the body nearest the
// payload it might have conflicted with.
func (f *issueWriteFlags) compose(ctx context.Context, stdin io.Reader, stdinIsTerminal bool) (composedWrite, error) {
	var zero composedWrite

	req, err := f.readPayload(ctx, stdin, stdinIsTerminal)
	if err != nil {
		return zero, err
	}

	body, err := f.body.Resolve(ctx, stdin, stdinIsTerminal)
	if err != nil {
		return zero, err
	}

	// The cross-route check, before anything is written. It covers the
	// field FLAGS only; the description needs no check here because
	// validate already refused --json-input beside the Markdown route
	// outright (see routeConflict, and ConflictWithPayload's own doc
	// comment on why a key-level description check would be unreachable).
	if err := f.fields.ConflictWithPayload(req); err != nil {
		return zero, err
	}

	if len(body.ADF) > 0 {
		if err := req.SetField(command.FieldDescription, json.RawMessage(body.ADF)); err != nil {
			return zero, err
		}
	}
	if err := f.fields.Apply(&req); err != nil {
		return zero, err
	}

	// ONE encoding, reused. jira.IssueWriteRequest's own MarshalJSON is
	// what runs here, so the published bytes carry the non-escaped <, >, &
	// and U+2028 an ADF description depends on — which package-level
	// json.Marshal would silently re-escape.
	payload, err := req.MarshalJSON()
	if err != nil {
		return zero, fmt.Errorf("issue write: encoding the composed payload: %w", err)
	}

	route := body.Route
	if f.jsonInput != "" {
		// The ADF route named the whole document, description included, so
		// that is the route the description came from whatever else is in
		// the file.
		route = command.RouteADF
	}
	return composedWrite{req: req, route: route, payload: payload}, nil
}

// readPayload reads and parses --json-input, or returns an empty request
// when the route was not taken.
//
// A parse failure is the CALLER's error with the flag named and exit 3 —
// never a transport error — for the reason BodyFlags.Resolve validates ADF
// before composing anything: a malformed document must fail as the input
// it is, rather than traveling to Jira and coming back as a 400 about a
// payload the caller never wrote.
//
// DisallowUnknownFields is deliberately NOT set. The document is a Jira
// payload and this CLI models two of its members; `historyMetadata`,
// `properties` and `transition` are real and none of them is this
// command's business to reject. Refusing them would make the lossless
// route lossy in the one dimension it exists to protect.
func (f *issueWriteFlags) readPayload(ctx context.Context, stdin io.Reader, stdinIsTerminal bool) (jira.IssueWriteRequest, error) {
	var req jira.IssueWriteRequest
	if f.jsonInput == "" {
		return req, nil
	}
	data, err := command.ReadInputFileContext(ctx, command.FlagJSONInput, f.jsonInput, stdin, stdinIsTerminal)
	if err != nil {
		return req, err
	}
	if err := json.Unmarshal(data, &req); err != nil {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s: %s is not a valid issue payload: %v", command.FlagJSONInput, f.jsonInput, err))
		e.Field = command.FlagJSONInput
		return jira.IssueWriteRequest{}, e
	}
	return req, nil
}

// verifyIssue reads one issue back after a write and narrows it to what
// --verify publishes.
//
// The readback is a SECOND round trip, which is exactly what the flag
// gates: neither write endpoint answers with the stored issue (create
// returns an identity, edit returns nothing), so unlike `comment add`
// there is no free confirmation to publish unconditionally.
//
// It asks for three fields rather than the server's default set. A
// verification does not need the issue's links, attachments or comment
// count, and narrowing the request is the difference between a cheap
// confirmation and a second full fetch.
func verifyIssue(ctx context.Context, client core.JiraClient, key string) (*envelope.VerifiedIssue, error) {
	read, err := client.GetIssue(ctx, key, []string{"summary", "status", "updated"})
	if err != nil {
		return nil, err
	}
	if read.Issue == nil {
		// GetIssue's contract is a non-nil Issue alongside a nil error.
		// Failing loudly rather than trusting it silently mirrors
		// postComment's guard, and the stakes are the same shape: a
		// zero-valued readback would publish a verification claiming an
		// empty summary and no status, which reads as a successful check
		// of an issue that came back blank.
		return nil, fmt.Errorf("issue write: empty readback for %q after a successful write", key)
	}
	fields := read.Issue.Fields.Value
	return &envelope.VerifiedIssue{
		Summary: fields.Summary.Value,
		Status:  fields.Status.Value.Name.Value,
		Updated: fields.Updated.Value,
	}, nil
}
