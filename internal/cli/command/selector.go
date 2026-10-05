package command

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/gechr/x/human"
	"github.com/gechr/x/set"
	xstrings "github.com/gechr/x/strings"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// Selector-flag names, spelled once so registration and every refusal
// name the same strings.
const (
	FlagJQL      = "jql"
	FlagFilter   = "filter"
	FlagFromFile = "from-file"
)

// MaxSelectorKeys is the largest resolved set a selector-driven write
// will act on. Above it the run is REFUSED, never truncated.
//
// The design's rule and its reason: silently acting on the first N of an
// unbounded match is the dishonesty `list --all`'s own visible cap exists
// to avoid, and here the consequence is destructive rather than cosmetic.
// A caller who wrote a query matching 4,000 issues and got 100 deleted
// has learned nothing about the other 3,900, and cannot tell from the
// output that a bound was applied at all.
//
// **Why this number and not --all's 5,000.** The confirmation lists every
// key it is about to act on, because a list is the only form in which
// somebody notices the key they did not mean to match (delete.go's own
// argument). A cap has to be a count a person can still read, and 5,000
// keys is not a prompt, it is a denial of service against the reader. 100
// is already past comfortable and is deliberately the ceiling rather than
// a target: a caller who genuinely means thousands should narrow the
// query, or name the keys through --from-file where they have written
// them out and can see them.
//
// Resolution asks for at most MaxSelectorKeys+1 issues across every necessary
// cursor page. Jira may return a short or empty nonfinal page; only isLast=true
// establishes that the target set is complete.
const MaxSelectorKeys = 100

// SelectorHelp is the paragraph every selector-bearing command's Long
// text carries, so five commands cannot describe one mechanism five
// ways.
//
// `issue delete` deliberately does NOT use it: the same rules matter
// more there, and its own version says so in its own words rather than
// leaving a reader to infer that the shared paragraph means something
// sharper on the destructive command.
//
// It states the four rules a caller can actually act on. It does not
// restate the reasoning — the embedded agent guides own that, and the
// design's §3.5 owns the argument.
const SelectorHelp = "**Selecting issues instead of naming them.** `--jql`, `--filter` and " +
	"`--from-file` choose the issues for you. They are refused alongside " +
	"named keys, and refused alongside each other — one way of saying " +
	"which issues, per command. Because you did not name them, the " +
	"command asks before writing, and the question LISTS the issues it " +
	"resolved rather than echoing your query back; `--force` answers it " +
	"up front, and never skips the listing. A match larger than 100 " +
	"issues is refused rather than trimmed. An incomplete search is also " +
	"refused before any write; short or empty pages do not prove completion.\n\n" +
	"With `--jql` or `--filter`, `--dry-run` cannot tell you which issues " +
	"match: finding out is a read from the site, and a preview never " +
	"contacts Jira. It says so and lists nothing. Add `--validate-remote` " +
	"to resolve the query and list them while still writing nothing. " +
	"`--from-file` needs no site, so a preview lists those keys normally."

// SelectorFlags is the shared bulk-selector surface: three ways to name a
// write's targets without typing their keys.
//
// **These are the most dangerous flags in this CLI.** `issue delete --jql
// "project = X"` is an unbounded destructive operation whose blast radius
// is a server-side query the caller never sees written out, which is why
// the policy below is shared rather than per-command: five commands
// answering "when is this refused, and what does the caller see first"
// separately is how they come to answer it differently, and the
// difference nobody notices is the one that deletes an extra project.
type SelectorFlags struct {
	// JQL is a query naming the targets directly.
	JQL string
	// Filter is a saved-filter ID. Never a name — see filterJQL.
	Filter string
	// FromFile is a path to a file of issue keys, one per line.
	FromFile string
}

// AddSelectorFlags registers the bulk-selector surface on cmd.
//
// A registrar rather than five copies, for AddKeyedFlags' reason with
// more at stake: the flags are the small half, and the policy — resolve
// before confirming, refuse beside positionals, refuse above the cap — is
// the half that must not vary. A per-command copy would be five chances
// to get the confirmation ordering wrong, and the wrong order is silent.
func AddSelectorFlags(cmd *cobra.Command, f *SelectorFlags) {
	AddStringVar(cmd, &f.JQL, FlagJQL, "",
		"Act on every issue matching this JQL query, instead of naming keys",
		FlagOpts{Terse: "Select by JQL", Placeholder: "query"})
	AddStringVar(cmd, &f.Filter, FlagFilter, "",
		"Act on every issue matching this saved filter's query, by filter `ID`",
		FlagOpts{Terse: "Select by filter", Placeholder: "id"})
	AddStringVar(cmd, &f.FromFile, FlagFromFile, "",
		"Act on the issue keys in this file, one per line (`-` for standard input)",
		FlagOpts{Terse: "Keys from file", Placeholder: "path"})
}

// Given reports whether any selector was passed.
func (f SelectorFlags) Given() bool {
	return f.JQL != "" || f.Filter != "" || f.FromFile != ""
}

// kind names which selector was given, and is the string the envelope
// publishes. Empty when none was.
func (f SelectorFlags) kind() string {
	switch {
	case f.JQL != "":
		return FlagJQL
	case f.Filter != "":
		return FlagFilter
	case f.FromFile != "":
		return FlagFromFile
	}
	return ""
}

// query is the value the caller typed for whichever selector they gave.
func (f SelectorFlags) query() string {
	switch {
	case f.JQL != "":
		return f.JQL
	case f.Filter != "":
		return f.Filter
	case f.FromFile != "":
		return f.FromFile
	}
	return ""
}

// needsSite reports whether resolving this selector requires a live read.
//
// This is the whole of what makes --from-file different, and it is a
// property of the selector rather than a special case: a JQL query and a
// saved filter are both answered by the SITE, and a file is answered by
// the filesystem.
func (f SelectorFlags) needsSite() bool {
	return f.JQL != "" || f.Filter != ""
}

// Validate refuses every selector invocation that cannot be resolved,
// before any I/O. keyCount is how many POSITIONAL keys the command
// found — a parameter rather than a read of cmd.Flags().Args(), because
// `issue transition` spends its first positional on the transition name
// and only it knows where its keys start.
//
// Two refusals, both from the design's §3.5:
//
//   - **A selector is refused beside positional keys.** Two ways of
//     naming the target in one invocation is the same ambiguity §7
//     rejects `--key` for, with more at stake. There is no merge rule
//     worth inventing here: a caller who wrote both has a wrong belief
//     about which one the command will use, and acting on either one
//     leaves them with it.
//   - **Two selectors are refused for the identical reason.** `--jql`
//     beside `--filter` is not a narrowing (nothing here composes them
//     with AND) and could only mean one of them is ignored.
//
// A command with neither is refused too, and that refusal is this
// function's job rather than cobra's: registering a selector means
// dropping the ArgSpec minimum to zero, so an empty invocation now
// reaches Validate instead of being caught by the arity check. The
// message has to be at least as good as the one it replaced, which is
// why it names both routes rather than only the positional one.
func (f SelectorFlags) Validate(keyCount int) error {
	given := []string{}
	for _, s := range []struct{ name, value string }{
		{FlagJQL, f.JQL},
		{FlagFilter, f.Filter},
		{FlagFromFile, f.FromFile},
	} {
		if s.value != "" {
			given = append(given, s.name)
		}
	}

	if len(given) > 1 {
		e := cli.NewInputError(cli.CodeFlagConflict, fmt.Sprintf(
			"--%s and --%s both name what to act on, and they are alternatives rather than layers — nothing here combines them, so one of them would have to be ignored; pick the one you meant",
			given[0], given[1],
		))
		e.Field = given[0]
		return e
	}

	if len(given) == 1 && keyCount > 0 {
		e := cli.NewInputError(cli.CodeFlagConflict, fmt.Sprintf(
			"--%s selects which issues to act on, and you also named %s — two ways of naming the target in one command is ambiguous, so drop one of them",
			given[0], pluralKeys(keyCount),
		))
		e.Field = given[0]
		return e
	}

	if len(given) == 0 && keyCount == 0 {
		return cli.NewInputError(cli.CodeArgCountInvalid, fmt.Sprintf(
			"name at least one issue key, or select issues with --%s, --%s or --%s",
			FlagJQL, FlagFilter, FlagFromFile,
		))
	}
	return nil
}

// pluralKeys renders a positional-key count for one sentence of prose.
func pluralKeys(n int) string {
	return human.Pluralize(n, "issue key", "issue keys")
}

// Targets is what a keyed write is about to act on: the key list, and the
// record of how it was chosen.
type Targets struct {
	// Keys is the resolved key list, in the order the run will use.
	//
	// Empty ONLY on an unresolved local preview (Summary.Resolved false).
	// A selector that resolved and matched nothing is refused rather than
	// handed back empty, because a write that acts on nothing is not a
	// success worth reporting and a confirmation naming no issues is not a
	// question anybody can answer.
	Keys []string
	// Summary is the envelope block, nil when the caller named their keys
	// positionally.
	Summary *envelope.SelectorSummary
}

// Resolved reports whether Keys is a real target list. False only for the
// local preview of a selector that needs the site.
func (t Targets) Resolved() bool {
	return t.Summary == nil || t.Summary.Resolved
}

// ResolveTargets settles which issues a keyed write acts on, and decides
// — once, for every command that takes a selector — what a run that
// cannot resolve one does instead.
//
// **The ordering this function exists to enforce: it resolves BEFORE its
// caller confirms.** A confirmation that echoed the query back would ask
// the caller to re-verify the thing they already got wrong, and the count
// is the part they cannot derive by reading it. So the prompt lists the
// actual keys, which means the keys must exist before the prompt does —
// and the only way to guarantee that across five commands is for the key
// list to be this function's return value rather than something each
// command remembers to fetch first.
//
// The branches mirror ResolveAccounts', deliberately: that function
// already settled what a local preview does with a value only the site
// can resolve, and this is the FOURTH application of the same rule (@me,
// link types, download filenames, and now selectors). A second shape for
// the same question would leave callers guessing which commands pay for a
// round trip.
//
//   - No selector: the positional keys are the targets, no client is
//     built, and nothing is published.
//   - --from-file, on any path: resolved LOCALLY, including under a bare
//     --dry-run. The rule that a preview cannot enumerate is a
//     consequence of resolution being a live read, and it is not one
//     here — so the preview lists the keys, because withholding them
//     would be a restriction with no honest reason behind it.
//   - --jql/--filter under a bare --dry-run: NOT resolved. A warning says
//     so and the preview enumerates nothing, because the alternative is
//     inventing a key list the preview never looked up.
//   - --jql/--filter live, or with --validate-remote: resolved for real,
//     and the keys are published whether the run writes or not.
//
// It takes the client CONSTRUCTOR for ResolveAccounts' reason: a caller
// handing over a built client would have paid for credential resolution
// before this function could decide the run needs none. The client it
// built comes back so the caller does not construct a second.
func ResolveTargets(
	ctx context.Context,
	newClient func() (core.JiraClient, error),
	f SelectorFlags,
	args []string,
	dryRun, validateRemote bool,
	stdin io.Reader,
	stdinIsTerminal bool,
) (core.JiraClient, Targets, error) {
	if !f.Given() {
		return nil, Targets{Keys: args}, nil
	}

	summary := &envelope.SelectorSummary{Kind: f.kind(), Query: f.query()}

	if f.FromFile != "" {
		keys, err := readKeyFile(ctx, f.FromFile, stdin, stdinIsTerminal)
		if err != nil {
			return nil, Targets{}, err
		}
		summary.Resolved = true
		return nil, Targets{Keys: keys, Summary: summary}, nil
	}

	if dryRun && !validateRemote {
		// Warned even though nothing failed: a preview that enumerates
		// nothing is showing a run whose whole target list is unknown, and
		// silence would leave that looking like a run with no targets.
		cli.AddWarning(SelectorUnresolvedWarning(f))
		return nil, Targets{Summary: summary}, nil
	}

	client, err := newClient()
	if err != nil {
		return nil, Targets{}, err
	}

	jql := f.JQL
	if f.Filter != "" {
		jql, err = filterJQL(ctx, client, f.Filter)
		if err != nil {
			return nil, Targets{}, err
		}
		// The resolved query is published because the caller cannot
		// otherwise see what they acted on: a saved filter is editable by
		// anyone it is shared with, so the query behind an id is not
		// knowable from the invocation and not stable over time.
		summary.JQL = jql
	}

	keys, err := searchKeys(ctx, client, jql, f.kind())
	if err != nil {
		return nil, Targets{}, err
	}
	summary.Resolved = true
	return client, Targets{Keys: keys, Summary: summary}, nil
}

// filterJQL turns a saved-filter id into the query it holds.
//
// **IDs only, never names**, and this is a decision rather than an
// omission. Jira does not require filter names to be unique — two people
// can each own and share one called "My bugs" — so resolving a name means
// picking one of several matches or refusing, and picking is precisely
// the ambiguity the selector rules refuse everywhere else, on the
// commands where an ambiguous target set is least affordable. A numeric
// id is what `jira issue list --output=json` and the Jira UI's own filter
// URL both give, so the id is available to anyone who can see the filter.
func filterJQL(ctx context.Context, client core.JiraClient, id string) (string, error) {
	if !isFilterID(id) {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s takes a saved filter's numeric ID, and %q is not one — Jira lets two filters share a name, so a name cannot say which one to act on; the ID is in the filter's own URL",
			FlagFilter, id,
		))
		e.Field = FlagFilter
		return "", e
	}

	read, err := Spin(ctx, "Reading filter "+id, func(ctx context.Context) (core.FilterRead, error) {
		return client.GetFilter(ctx, id)
	})
	if err != nil {
		// Deliberately not reworded into "no such filter": Jira answers 404
		// both for a filter that is absent and for one the caller cannot
		// see, and it does that on purpose so nobody can enumerate private
		// filters. Wrapping preserves the taxonomy's own classification and
		// adds only the fact the caller needs to act — which flag, and that
		// visibility is the other possibility.
		return "", fmt.Errorf("resolving --%s %s (a filter must exist AND be shared with you to be readable): %w", FlagFilter, id, err)
	}
	if read.Filter == nil {
		return "", fmt.Errorf("resolving --%s %s: the site returned no filter", FlagFilter, id)
	}
	jql := strings.TrimSpace(read.Filter.Jql.Value)
	if jql == "" {
		// An empty query is not a harmless edge case: Jira reads it as
		// "every issue on the site", so a filter carrying one would turn a
		// selector into an unbounded match against the whole tenant. It is
		// refused rather than run.
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s %s holds no query, and an empty query matches every issue on the site rather than none — refusing rather than acting on all of them",
			FlagFilter, id,
		))
		e.Field = FlagFilter
		return "", e
	}
	return jql, nil
}

// isFilterID reports whether id is a filter id rather than a name: one or
// more digits and nothing else. xstrings.IsDigits is documented as
// "non-empty and consists entirely of ASCII digits", which is this rule
// including the empty case, so the name is kept for the sentence it makes
// at the call site rather than for the logic.
func isFilterID(id string) bool {
	return xstrings.IsDigits(id)
}

// searchKeys resolves the complete matching set before allowing any write.
// One extra issue proves overflow without an approximate count. Cursor faults,
// excessive empty pages and duplicate keys refuse the entire selection rather
// than handing a partial or repeated target set to confirmation and fan-out.
//
// Enhanced search defaults to IDs only. Request key explicitly on every page:
// its top-level location does not make it present in the default response.
// Selectors need no other issue fields before confirmation and fan-out.
func searchKeys(ctx context.Context, client core.JiraClient, jql, kind string) ([]string, error) {
	keys, err := Spin(ctx, "Resolving --"+kind, func(ctx context.Context) ([]string, error) {
		keys := []string{}
		seen := set.New[string]()
		progress := NewSearchProgress("", false)
		token := ""
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			size := MaxSelectorKeys + 1 - len(keys)
			read, err := client.SearchIssues(ctx, jql, []string{"key"}, token, size)
			if err != nil {
				return nil, err
			}
			if err := read.Results.ValidatePage(size); err != nil {
				return nil, cli.NewCodedError(cli.CodeSelectorIncomplete, err.Error())
			}
			issues := read.Results.Issues.Value
			if len(keys)+len(issues) > MaxSelectorKeys {
				e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
					"--%s matched more than %d issues, which is this command's limit — it refuses rather than acting on the first %d; narrow the query, or write the keys you mean into a file and pass --%s",
					kind, MaxSelectorKeys, MaxSelectorKeys, FlagFromFile,
				))
				e.Field = kind
				return nil, e
			}
			for _, issue := range issues {
				key := issue.Key.Value
				if key == "" {
					return nil, cli.NewCodedError(cli.CodeSelectorIncomplete, "the site returned an issue with no key")
				}
				if seen.Contains(key) {
					return nil, cli.NewCodedError(cli.CodeSelectorIncomplete, "the site returned a duplicate issue key")
				}
				seen.Add(key)
				keys = append(keys, key)
			}
			if read.Results.IsLast.Value {
				return keys, nil
			}
			token = read.Results.NextPageToken.Value
			if reason := progress.Advance(token, len(issues)); reason != "" {
				return nil, cli.NewCodedError(cli.CodeSelectorIncomplete, "search stopped before its final page: "+reason)
			}
		}
	})
	if err != nil {
		return nil, fmt.Errorf("resolving --%s: %w", kind, err)
	}
	if len(keys) == 0 {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s matched no issues, so there is nothing to act on", kind,
		))
		e.Field = kind
		return nil, e
	}
	return keys, nil
}

// readKeyFile reads --from-file's key list.
//
// One key per line, blank lines skipped, and `#` comments honored so a
// caller can annotate a list they are about to act destructively on —
// which is the whole reason to keep such a list in a file rather than in
// shell history.
//
// **Nothing here validates that a line looks like an issue key.** It is
// tempting, and it would be wrong for the reason deleteFanOut already
// argues: whether a key exists is knowable only from the site, a bad key
// fails as its own entry rather than stopping the run, and a shape check
// here would refuse a perfectly good key from a site whose project keys
// this CLI did not anticipate. What a malformed line gets is the same
// per-key failure a mistyped positional gets.
func readKeyFile(ctx context.Context, path string, stdin io.Reader, stdinIsTerminal bool) ([]string, error) {
	data, err := ReadInputFileContext(ctx, FlagFromFile, path, stdin, stdinIsTerminal)
	if err != nil {
		return nil, err
	}

	var keys []string
	for line := range strings.Lines(string(data)) {
		if before, _, found := strings.Cut(line, "#"); found {
			line = before
		}
		if key := strings.TrimSpace(line); key != "" {
			keys = append(keys, key)
		}
	}

	if len(keys) == 0 {
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s %s names no issue keys, so there is nothing to act on", FlagFromFile, path,
		))
		e.Field = FlagFromFile
		return nil, e
	}
	if len(keys) > MaxSelectorKeys {
		// The same cap as a query's, and it applies here for a DIFFERENT
		// reason worth stating: the caller can see this list, so the risk
		// is not an unseen blast radius. It is the confirmation — which
		// names every key, and stops being readable at the same size
		// whether the keys came from a query or a file.
		e := cli.NewInputError(cli.CodeFlagValueInvalid, fmt.Sprintf(
			"--%s %s names %d issue keys, which is more than this command's limit of %d — split the file and run it in batches you can still check",
			FlagFromFile, path, len(keys), MaxSelectorKeys,
		))
		e.Field = FlagFromFile
		return nil, e
	}
	return keys, nil
}

// SelectorUnresolvedWarning is what a LOCAL preview publishes in place of
// an enumeration it deliberately did not perform.
//
// MeUnresolvedWarning's sibling, and the same decision: --dry-run is
// local-only and must never contact Jira, so a preview cannot know which
// issues a query matches. Inventing a key list would publish targets the
// preview never looked up; enumerating nothing in silence would show a
// run that looks like it has no targets at all.
//
// Lossy is true here where MeUnresolvedWarning sets it false, and the
// difference is real rather than incidental. An unresolved `@me` is a
// value that is present and will resolve when sent — nothing is missing.
// An unresolved selector means the preview's results[] is EMPTY while a
// live run's would not be: the thing the caller asked to see is the part
// that is absent.
func SelectorUnresolvedWarning(f SelectorFlags) envelope.Warning {
	if !f.needsSite() {
		return envelope.Warning{}
	}
	return envelope.Warning{
		Type: "selector_unresolved",
		Message: fmt.Sprintf(
			"--%s was not resolved in this preview: finding which issues it matches is a read from the site, which --dry-run never does, so nothing is listed below. A live run resolves it; add --%s to resolve it here and list the issues without writing anything.",
			f.kind(), FlagValidateRemote,
		),
		Lossy: true,
	}
}
