package cache

import (
	"context"
	"fmt"

	xsync "github.com/gechr/x/sync"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func newRefresh(deps PrimeDeps) *cobra.Command {
	var opts primeOptions
	var keyed command.KeyedFlags
	return command.MustNew(command.Spec[envelope.CacheRefreshOutput]{
		Op: "cache.refresh", Use: "refresh [RESOURCE...]", Short: "Refresh all or selected cached metadata",
		Keyed: true, MutatesLocalState: true, Passthrough: true,
		Long: "Refresh all ten metadata resources in catalog order, or selected resource names in request order. Repeated names run once, at their first position. " +
			"Fresh entries are reused unless --force is set. --ttl-minutes overrides freshness for each resource; nonpositive uses each resource's default. " +
			"--parallelism bounds concurrent resources from 1 to 16 and defaults to 1. --unbounded removes collection caps for paged metadata; board project enrichment retains its bounds.\n\n" +
			"--dry-run inspects the selected scope without credentials, Jira reads or storage creation; configure public base_url and email explicitly. " +
			"Live refresh retains ordinary keyring account resolution and works in read-only mode without confirmation. " +
			"A failed resource never replaces its previous entry. Results retain native values, completion facts and storage acknowledgements, even after failure or cancellation. " +
			"Mixed success returns successful data and failed resource rows; total failure returns the most severe original error with every row. Inspect each row's ok and error before retrying.",
		Example: "$ jira cache refresh --output=json\n\n$ jira cache refresh fields labels --force --parallelism=2 --output=json\n\n$ jira cache refresh --dry-run --output=json",
		Args:    command.ArgSpec{Max: command.AnyArgs, Positions: []command.Arg{{Name: "RESOURCE", Predictor: "cacheresource"}}},
		Flags: func(cmd *cobra.Command) {
			var mut command.MutationFlags
			command.AddMutationFlags(cmd, &mut)
			command.AddKeyedFlags(cmd, &keyed, command.WithParallelismDefault(1), command.WithParallelismShorthand("p"))
			command.AddBoolVar(cmd, &opts.force, command.FlagForce, false, "Fetch selected resources regardless of freshness", command.FlagOpts{Terse: "Fetch fresh metadata"})
			command.AddIntVar(cmd, &opts.ttlMinutes, FlagTTLMinutes, 0, "Cache freshness in minutes; nonpositive uses each resource's default", command.FlagOpts{Terse: "Cache lifetime", Placeholder: "n"})
			command.AddBoolVar(cmd, &opts.unbounded, "unbounded", false, "Remove metadata collection caps; board project enrichment remains bounded", command.FlagOpts{Terse: "Remove collection caps"})
		},
		Validate: func() error {
			if err := keyed.Validate(); err != nil {
				return err
			}
			return (Options{TTLMinutes: opts.ttlMinutes}).Validate()
		},
		Human: renderRefresh, Warnings: refreshWarnings,
		RetainOnError: func(out envelope.CacheRefreshOutput) bool { return len(out.Results) > 0 },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.CacheRefreshOutput, error) {
			return runRefresh(ctx, deps, inv, opts, keyed)
		},
	})
}

func runRefresh(ctx context.Context, deps PrimeDeps, inv command.Invocation, opts primeOptions, keyed command.KeyedFlags) (envelope.CacheRefreshOutput, error) {
	out := envelope.CacheRefreshOutput{MutationState: envelope.MutationState{DryRun: inv.DryRun}, Parallelism: keyed.Parallelism, Results: []envelope.ResourceResult[envelope.CachePrimeOutput]{}}
	if err := keyed.Validate(); err != nil {
		return out, err
	}
	if err := (Options{TTLMinutes: opts.ttlMinutes}).Validate(); err != nil {
		return out, err
	}
	names := inv.Args
	if len(names) == 0 {
		names = ResourceNames()
	}
	selected := make([]MetadataResource, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		resource, ok := metadataResource(name)
		if !ok {
			err := cli.NewInputError(cli.CodeArgValueInvalid, fmt.Sprintf("unknown cache resource %q", name))
			err.Field, err.Suggestions = "RESOURCE", cli.SuggestValues(name, ResourceNames())
			return out, err
		}
		if !seen[name] {
			selected = append(selected, resource)
			seen[name] = true
		}
	}
	// Constructors are resolved once and only when a worker actually needs them.
	// A fully fresh group never constructs a Jira client. Live scope resolution
	// retains credential-store access for an account held only in keyring.
	original := deps
	scope := command.OnceContextValues(func(ctx context.Context) (MetadataScope, error) {
		if original.Scope == nil {
			return MetadataScope{}, core.ErrCacheManagementUnsupported
		}
		return original.Scope(ctx, inv.DryRun)
	})
	deps.Scope = func(ctx context.Context, _ bool) (MetadataScope, error) { return scope(ctx) }
	deps.JiraClient = command.OnceContextValues(func(ctx context.Context) (core.JiraClient, error) {
		if original.JiraClient == nil {
			return nil, metadataInvalid("metadata client constructor is unavailable")
		}
		return original.JiraClient(ctx)
	})
	opts.withoutSpinner = true
	out.Results = make([]envelope.ResourceResult[envelope.CachePrimeOutput], len(selected))
	raw := make([]error, len(selected))
	return command.Spin(ctx, "Refreshing metadata", func(ctx context.Context) (envelope.CacheRefreshOutput, error) {
		xsync.Parallel(keyed.Parallelism, len(selected), func(i int) {
			data, failure := runPrime(ctx, deps, inv, selected[i], opts)
			row := envelope.ResourceResult[envelope.CachePrimeOutput]{Resource: selected[i].Name, OK: failure == nil, Data: &data}
			if failure != nil {
				class := cli.MapError(failure)
				row.Err = &envelope.KeyError{Code: class.Code, Message: class.Message, Hint: class.Hint}
			}
			out.Results[i], raw[i] = row, failure
		})
		return out, command.KeyedFailure(out.Results, raw)
	})
}

func renderRefresh(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.CacheRefreshOutput)
	if !ok {
		return fmt.Errorf("cache refresh: unexpected payload %T", env.Data)
	}
	for _, row := range out.Results {
		if row.Data != nil {
			if err := renderPrime(v, envelope.Envelope{Data: *row.Data}); err != nil {
				return err
			}
		}
		if row.Err != nil {
			v.Line("failed", cli.Text(row.Resource), cli.Text(row.Err.Message))
			v.Hint(cli.Text(row.Err.Hint))
		}
	}
	return nil
}

func refreshWarnings(out envelope.CacheRefreshOutput) []envelope.Warning {
	for _, row := range out.Results {
		if row.Data != nil {
			if warnings := primeWarnings(*row.Data); len(warnings) > 0 {
				return warnings
			}
		}
	}
	return nil
}
