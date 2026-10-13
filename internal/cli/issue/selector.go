package issue

import (
	"context"
	"fmt"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// The issue-side glue for bulk selectors. The POLICY lives in
// command.ResolveTargets — what resolves, when, and what a preview does
// instead — and this file is only the two lines each of the five write
// commands would otherwise repeat.
//
// It is deliberately thin. A helper here that also confirmed, or also
// branched on --dry-run, would be a second place the ordering rule
// (resolve, then preview, then confirm) is expressed, and the whole
// safety property of these commands is that the ordering is expressed
// once per command where a reader can see it in the run function.

// selectorInputs is what resolveTargets needs from a command's flags.
// Gathered into a struct because the three values are all bools and a
// string-ish struct, and a call site should not have to get two bools
// right by position — the mistake would be silent and would swap "this
// preview may read the site" with "this run skips its prompt".
type selectorInputs struct {
	selector       command.SelectorFlags
	validateRemote bool
}

// resolveTargets settles which issues a keyed write acts on.
//
// A thin wrapper over command.ResolveTargets that supplies the
// invocation's own facts, so a command names only what it decides. It
// takes the client CONSTRUCTOR rather than Deps for the reason
// ResolveTargets does: a run that needs no site must not pay for
// credential resolution, and only the constructor form can express that.
func resolveTargets(
	ctx context.Context,
	newClient func(ctx context.Context) (core.JiraClient, error),
	inv command.Invocation,
	in selectorInputs,
) (core.JiraClient, command.Targets, error) {
	return command.ResolveTargets(
		ctx, newClient, in.selector, inv.Args,
		inv.DryRun, in.validateRemote, inv.Stdin, inv.StdinIsTerminal,
	)
}

// renderUnresolvedSelector draws the ONE preview that cannot name its
// own targets, and reports whether it drew anything.
//
// A preview whose selector went unresolved has an empty results[], and
// every one of these renderers walks results[] — so without this they
// would each print a heading over nothing, which reads exactly like a
// run that matched no issues. That is the reading the whole selector
// design refuses to allow: "the query was never run" and "the query
// matched nothing" must never look the same, and the second of those is
// refused outright anyway.
//
// verb is the command's own act ("delete", "edit"), so the sentence says
// what would happen rather than something generic.
func renderUnresolvedSelector(v *output.View, sel *envelope.SelectorSummary, verb string) bool {
	if sel == nil || sel.Resolved {
		return false
	}
	v.Line(fmt.Sprintf("Would %s whatever --%s matches, which this preview did not ask the site for.", verb, sel.Kind))
	v.Hint("Nothing was written. Add --validate-remote to resolve it and list the issues, still without writing.")
	return true
}

// oneClient wraps a client constructor so a run builds AT MOST ONE
// client, however many things ask for one.
//
// `issue edit --jql …` is what this exists for: it has two independent
// resolvers — the selector's search and `@me`'s /myself lookup — and both
// take a CONSTRUCTOR by design, so that a run needing neither pays for
// neither.
//
// In production this is belt and braces: root/deps.go already wires
// JiraClient as command.OnceContextValues, and view.go documents callers
// relying on that. It earns its place for the fakes, which hand in a bare
// constructor with no such guarantee — so a test asserting "one client
// per run" would otherwise be asserting something only the production
// wiring provides. Wrapping an already-once function costs one extra
// indirection and nothing else.
//
// Errors are memoized too, which is sync.OnceValues' own contract: a
// second attempt after a failed credential resolution would fail
// identically and only produce a second error to discard.
func oneClient(newClient func(ctx context.Context) (core.JiraClient, error)) func(ctx context.Context) (core.JiraClient, error) {
	return command.OnceContextValues(newClient)
}
