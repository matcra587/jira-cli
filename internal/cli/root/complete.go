package root

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	clibcobra "github.com/gechr/clib/cli/cobra"
	"github.com/gechr/clib/complete"
	"github.com/gechr/clog"
	concobra "github.com/gechr/conductor/cli/cobra"
	xslices "github.com/gechr/x/slices"
	"github.com/spf13/pflag"

	changelog "github.com/matcra587/jira-cli"
	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/agentguides"
	"github.com/matcra587/jira-cli/internal/cli"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/config"
)

// runCompletionAction executes a pre-parse completion action (script
// print/install, or a dynamic `--@complete` callback from a shell)
// when one was requested. It mirrors conductor's own preflight step but
// supplies the dynamic handler conductor hardcodes to nil — that nil is
// the reason this wiring lives here at all.
func runCompletionAction(ctx context.Context, program *concobra.Program, sink *failureSink, args []string) (handled bool, code int) {
	cf, cargs, ok := clibcobra.Preflight()
	if !ok {
		return false, 0
	}
	// Completion bypasses alias expansion. Resolve its selected config before
	// the handler or an early refusal can emit warnings or human diagnostics.
	resolveProgramTheme(program)
	// The boundary renders through the resolved format, and nothing has
	// parsed yet on this path — seed it from the raw argv exactly as the
	// normal dispatch does.
	sink.seed(program.Root, args)
	// A dynamic callback may select its local config. Script printing or
	// installation still accepts no application flags. The early global
	// probe has already selected a prefixed --config; callback argv follows
	// '--' and is resolved by the local predictor below.
	if cf.Complete != "" && !cf.PrintCompletion && !cf.InstallCompletion && !cf.UninstallCompletion {
		cf.UnknownFlags = slices.DeleteFunc(cf.UnknownFlags, func(flag string) bool {
			return flag == "--"+cli.FlagConfig || flag == "--"+cli.FlagProfile || flag == "-P"
		})
	}
	// clib rejects any flag ITS scanner does not know once an
	// install/uninstall/print action is requested, and words it "unknown
	// flag". True in clib's vocabulary, false in ours: --output is a real
	// flag that simply cannot apply to a raw shell script. Reading the
	// flags off Preflight rather than matching clib's message text lets
	// the answer come out in our own words, with a code and an exit the
	// taxonomy owns.
	if len(cf.UnknownFlags) > 0 {
		return true, sink.exitCodeContext(ctx, notApplicableFlags(cf.UnknownFlags))
	}
	if _, err := cf.Handle(program.Generator(), completionHandler, complete.WithArgs(cargs)); err != nil {
		// Installing or printing failed for a reason of its own (an
		// unwritable rc file, an unknown shell). It still leaves through
		// the one boundary, so a machine caller gets a frame instead of
		// prose and an off-contract exit 1.
		return true, sink.exitCodeContext(ctx, err)
	}
	return true, 0
}

// notApplicableFlags builds the typed error for real flags passed
// alongside a completion action. The offenders ride Field and the
// message, never the hint, which stays static like every other row.
func notApplicableFlags(flags []string) error {
	e := cli.NewInputError(cli.CodeFlagNotApplicable,
		"completion actions accept no other flags, but got "+strings.Join(flags, ", "))
	e.Field = strings.TrimPrefix(flags[0], "--")
	return e
}

// completionKinds maps a predictor kind (the `predictor=<name>` half of
// a flag's Complete metadata) to its candidate function. Candidates are
// "value\tdescription" lines. Local guide names come from the embedded
// source. Every published predictor needs a registered function here;
// the guardrail joins command metadata against this dispatch table.
var completionKinds = map[string]func(args []string) []string{
	"adftype": func(_ []string) []string { return adf.SupportIdentities() },
	"release": func(_ []string) []string {
		return xslices.Map(changelog.Releases(), func(release changelog.Release) string {
			return release.Version + "\t" + release.Date
		})
	},
	"guide":              guideCandidates,
	"alias":              aliasCandidates,
	"savedquery":         savedQueryCandidates,
	"configkey":          func(_ []string) []string { return config.Keys() },
	"cacheresource":      func(_ []string) []string { return cachecmd.ResourceNames() },
	"cacheclearresource": func(_ []string) []string { return cachecmd.ClearResourceNames() },
	"issuekey":           issueKeyCandidates,
	"attachmentissuekey": attachmentIssueKeyCandidates,
	"transitionissuekey": transitionIssueKeyCandidates,
	"configsetkey":       func(_ []string) []string { return config.WritableSettingKeys() },
	"configvalue":        configValueCandidates,
}

type completionContext struct {
	files                 []string
	transition, status    string
	jql, filter, fromFile string
}

// completionPositionals separates clib's forwarded valued flags from prior
// positionals. The callback has no current word or future tokens, so ambiguous
// file/status slots offer nothing rather than guessing from an issue-key shape.
func completionPositionals(args []string) (completionContext, []string, error) {
	var ctx completionContext
	probe := pflag.NewFlagSet("completion-context", pflag.ContinueOnError)
	probe.SetOutput(io.Discard)
	probe.Usage = func() {}
	probe.ParseErrorsAllowlist.UnknownFlags = true
	probe.String(cli.FlagConfig, "", "")
	probe.StringP(cli.FlagProfile, "P", "", "") //nolint:gocritic // parse-only probe; public flags use command metadata
	files := probe.StringSlice("file", nil, "")
	transition := probe.String("transition", "", "")
	status := probe.String("status", "", "")
	jql := probe.String(command.FlagJQL, "", "")
	filter := probe.String(command.FlagFilter, "", "")
	fromFile := probe.String(command.FlagFromFile, "", "")
	if err := probe.Parse(args); err != nil {
		return ctx, nil, err
	}
	ctx.files, ctx.transition, ctx.status = *files, *transition, *status
	ctx.jql, ctx.filter, ctx.fromFile = *jql, *filter, *fromFile
	return ctx, probe.Args(), nil
}

func attachmentIssueKeyCandidates(args []string) []string {
	ctx, positionals, err := completionPositionals(args)
	if err != nil || len(ctx.files) == 0 && len(positionals) > 0 {
		return nil
	}
	return issueKeyCandidates(args)
}

func transitionIssueKeyCandidates(args []string) []string {
	ctx, positionals, err := completionPositionals(args)
	if err != nil || ctx.jql != "" || ctx.filter != "" || ctx.fromFile != "" {
		return nil
	}
	if ctx.transition == "" && ctx.status == "" && len(positionals) > 0 {
		return nil
	}
	return issueKeyCandidates(args)
}

func issueKeyCandidates(args []string) []string {
	resolved, err := config.Load(config.Options{Path: globalFlagLayer(args).Path})
	if err != nil || config.ValidateBaseURL(resolved.Config.BaseURL) != nil || strings.TrimSpace(resolved.Config.Email) == "" {
		return nil
	}
	store, err := newScopedDiskCache(resolved.Config.BaseURL, resolved.Config.Email)
	if err != nil {
		return nil
	}
	keys, _, _ := cachecmd.RecentIssueKeys(store)
	return keys
}

func savedQueryCandidates(args []string) []string {
	flags := globalFlagLayer(args)
	if flags.ProfileSet && flags.Profile == "" {
		return nil
	}
	resolved, err := config.Load(config.Options{Path: flags.Path, Profile: flags.Profile})
	if err != nil {
		return nil
	}
	queries, err := config.LoadQueries(context.Background(), resolved.Config.QueriesPath)
	if err != nil {
		return nil
	}
	var candidates []string
	for _, name := range slices.Sorted(maps.Keys(queries)) {
		if cli.Text(name) != name {
			continue
		}
		candidates = append(candidates, name+"\t"+cli.Text(queries[name].Name))
	}
	return candidates
}

func configValueCandidates(args []string) []string {
	// clib forwards preceding positionals, excluding command words and
	// flag values. The second argument's context is the selected key.
	if len(args) != 1 {
		return nil
	}
	return config.SettingValueChoices(args[0])
}

func aliasCandidates(args []string) []string {
	file, err := config.OpenAliasFile(context.Background(), globalFlagLayer(args).Path)
	if err != nil {
		return nil
	}
	aliases := file.Aliases()
	var candidates []string
	for _, name := range slices.Sorted(maps.Keys(aliases)) {
		// A changed completion name would invoke another alias. Drop unsafe
		// names; descriptions can be sanitized without changing identity.
		if cli.Text(name) != name {
			continue
		}
		candidates = append(candidates, name+"\t"+cli.Text(aliases[name]))
	}
	return candidates
}

func guideCandidates(_ []string) []string {
	guides, err := agentguides.Load()
	if err != nil {
		return nil
	}
	var candidates []string
	for g := range guides.All() {
		candidates = append(candidates, g.Slug+"\t"+g.Title)
	}
	return candidates
}

// CompletionKinds lists the predictor kinds completionHandler answers,
// sorted. It reads the dispatch map itself — not a parallel declaration —
// so the guardrail that joins every published `predictor=` against this
// set (tests/guardrails) also catches a handler losing an entry.
//
// A key mapped to a nil function is reported as UNANSWERED. The map key
// alone would otherwise satisfy the guardrail while TAB stayed dead —
// reintroducing, through the guard itself, the exact defect the guard
// exists to catch.
func CompletionKinds() []string {
	kinds := make([]string, 0, len(completionKinds))
	for kind, fn := range completionKinds {
		if fn == nil {
			continue
		}
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	return kinds
}

// completionHandler answers a shell's dynamic completion callback. The
// predictor laws (help standard §4.3) bind it: it never errors and
// never falls back to filenames — an unknown kind or a failed lookup
// prints nothing, so TAB does nothing rather than something wrong.
func completionHandler(_, kind string, args []string) {
	fn, ok := completionKinds[kind]
	if !ok || fn == nil {
		return
	}
	// A predictor that panics mid-loop would otherwise emit a partial
	// candidate list and then crash into the user's shell. The laws say a
	// failed lookup offers nothing; a panic is a failed lookup.
	defer func() {
		if r := recover(); r != nil {
			// Swallowed on purpose, but not silently: the shell discards
			// stderr during a callback, so a debug line costs the user
			// nothing and gives the predictor's author a trail.
			clog.Debug().Str("kind", kind).Msgf("completion predictor panicked: %v", r)
		}
	}()
	// Candidates go to stdout — the one place the calling shell reads —
	// and nothing else may print there during a callback. One candidate
	// per line, so a value carrying a newline would read as two: drop
	// anything that cannot be represented rather than corrupt the list.
	for _, line := range fn(args) {
		if strings.ContainsAny(line, "\n\r") {
			continue
		}
		_, _ = fmt.Println(line)
	}
}
