package cache

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	xslices "github.com/gechr/x/slices"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

// Deps separates credential-free local management from live metadata priming.
type Deps struct {
	Cache   func() (core.Cache, error)
	Primers PrimeDeps
}

const (
	BoardListKey        = "v1:boards-list"
	BoardSelectorPrefix = "v2:board:"
	LinkTypesPrefix     = "v1:link-types:"
)

// ResourceNames lists the current producers with trusted key classifications.
// Lifecycle selection and completion share this supported inventory.
func ResourceNames() []string {
	return xslices.Map(MetadataResources(), func(resource MetadataResource) string {
		return resource.Name
	})
}

func New(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use: "cache", Short: "Manage scoped local metadata", Args: cobra.NoArgs,
		Long:    "Prime visible metadata or clear disposable local entries for the selected site/account. Previews and local management open no Jira client or credential store; they require explicit public scope. Live primers retain ordinary keyring account resolution.",
		Example: "$ jira cache clear --dry-run --output=json",
	}
	cmd.AddCommand(newClear(deps), newRefresh(deps.Primers), newIssueKeys(deps), newInspect(deps.Primers))
	addPrimers(cmd, deps.Primers)
	return cmd
}

func newClear(deps Deps) *cobra.Command {
	var mut command.MutationFlags
	return command.MustNew(command.Spec[envelope.CacheClearOutput]{
		Op: "cache.clear", MutatesLocalState: true, Use: "clear [RESOURCE]", Short: "Remove selected cached metadata",
		Long: "Remove cached entries for the configured site and account. Other scopes and legacy flat entries remain untouched. " +
			"With no resource, remove every entry in the active namespace, including malformed entries. " +
			"With a resource, remove entries with a valid matching logical key; boards includes saved-filter selectors. " +
			"Unclassifiable entries are reported and require a whole-scope clear. Unrelated files and directories are not recursively removed.\n\n" +
			"--dry-run inspects the same inventory without deleting or creating storage. A live clear requires --force in headless, agent or --no-input mode. " +
			"An interactive terminal proceeds without a prompt. Per-entry acknowledgements survive partial failures and interruption; inspect them before retrying. " +
			"Concurrent readers/writers are not locked; a later fetch can repopulate the cache. This command neither refreshes metadata nor changes Jira.",
		Example: "$ jira cache clear --dry-run --output=json\n\n" +
			"# clear board lists and selector snapshots for the selected account\n" +
			"$ jira cache clear boards --force --no-input\n\n" +
			"# clear all active metadata, including malformed entries\n" +
			"$ jira cache clear --force --output=json",
		Args: command.ArgSpec{Max: 1, Positions: []command.Arg{{Name: "RESOURCE", Predictor: "cacheclearresource"}}},
		Flags: func(cmd *cobra.Command) {
			command.AddMutationFlags(cmd, &mut, command.WithHeadlessForce("clearing cached metadata"))
		},
		Human:         renderClear,
		RetainOnError: func(out envelope.CacheClearOutput) bool { return out.Entries != nil },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.CacheClearOutput, error) {
			return runClear(ctx, deps, inv, mut.Force)
		},
	})
}

func runClear(ctx context.Context, deps Deps, inv command.Invocation, force bool) (envelope.CacheClearOutput, error) {
	out := envelope.CacheClearOutput{MutationState: envelope.MutationState{DryRun: inv.DryRun}, Resource: "all"}
	if len(inv.Args) != 0 {
		out.Resource = inv.Args[0]
	}
	if len(inv.Args) != 0 && !slices.Contains(ClearResourceNames(), out.Resource) {
		err := cli.NewInputError(cli.CodeArgValueInvalid, "unknown cache resource")
		err.Field, err.Suggestions = "RESOURCE", cli.SuggestValues(out.Resource, ClearResourceNames())
		return out, err
	}

	if !inv.DryRun && !inv.Interactive && !force {
		err := cli.NewInputError(cli.CodeFlagRequired, "cache clear requires --force in headless, agent or --no-input mode")
		err.Field = command.FlagForce
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if deps.Cache == nil {
		return out, cacheIO(core.ErrCacheManagementUnsupported)
	}
	store, err := deps.Cache()
	if err != nil {
		return out, cacheIO(err)
	}
	managed, ok := store.(core.ManagedCache)
	if !ok {
		return out, cacheIO(core.ErrCacheManagementUnsupported)
	}
	entries, err := managed.Entries(ctx)
	out.Entries = []envelope.CacheClearEntry{}
	for _, entry := range entries {
		resource := resourceForKey(entry.Key)
		if entry.Malformed || resource == "unknown" {
			out.Unclassified++
			resource = "unknown"
		}
		if out.Resource == "all" || out.Resource == resource {
			out.Entries = append(out.Entries, envelope.CacheClearEntry{ID: entry.ID, Resource: resource})
		}
	}
	out.WouldRemove = len(out.Entries)
	if err != nil {
		return out, cacheIO(err)
	}
	if inv.DryRun {
		return out, nil
	}
	var failures []error
	for i := range out.Entries {
		if err := ctx.Err(); err != nil {
			return out, errors.Join(append(failures, err)...)
		}
		out.Entries[i].Attempted = true
		removed, err := managed.DeleteEntry(ctx, out.Entries[i].ID)
		out.Entries[i].Removed = removed
		if removed {
			out.Removed++
		}
		if err != nil {
			failure := cacheIO(err)
			classified := cli.MapError(failure)
			out.Entries[i].Error = &envelope.Error{Code: classified.Code, Message: classified.Message, Hint: classified.Hint}
			failures = append(failures, failure)
		}
	}
	return out, errors.Join(append(failures, ctx.Err())...)
}

func resourceForKey(key string) string {
	switch {
	case key == IssueKeysKey:
		return "issuekeys"
	case strings.HasPrefix(key, TransitionHintsPrefix):
		issue := strings.TrimPrefix(key, TransitionHintsPrefix)
		if normalized, err := command.ParseIssueKey(issue); err == nil && normalized == issue {
			return "transitions"
		}
	case key == BoardListKey || strings.HasPrefix(key, BoardSelectorPrefix):
		return "boards"
	case strings.HasPrefix(key, LinkTypesPrefix):
		return "linktypes"
	}
	for _, resource := range MetadataResources() {
		if key == resource.Key("") && resource.Name != "linktypes" {
			return resource.Name
		}
	}
	return "unknown"
}

func renderClear(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.CacheClearOutput)
	if !ok {
		return fmt.Errorf("cache.clear: unexpected payload %T", env.Data)
	}
	if out.DryRun {
		v.Line("would remove", strconv.Itoa(out.WouldRemove), "cached entries for", cli.Text(out.Resource))
	} else {
		v.Line("removed", strconv.Itoa(out.Removed), "of", strconv.Itoa(out.WouldRemove), "cached entries for", cli.Text(out.Resource))
	}
	if out.Unclassified > 0 && out.Resource != "all" {
		v.Line(strconv.Itoa(out.Unclassified), "entries have no trusted resource classification; use a whole-scope clear to include them")
	}
	for _, entry := range out.Entries {
		if entry.Error != nil {
			v.Line("failed entry", cli.Text(entry.ID), cli.Text(entry.Error.Message))
		}
	}
	return nil
}

func cacheIO(err error) error {
	var input *cli.InputError
	if errors.As(err, &input) {
		return err
	}
	return fmt.Errorf("%w: %w", cli.NewCodedError(cli.CodeCacheIO, "local cache operation failed"), err)
}
