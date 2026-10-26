package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/output"
)

// MetadataScope selects the same site/account namespace as existing consumers.
// Site supplies the existing site-qualified link type key and inspection scope identity.
type MetadataScope struct {
	Store core.Cache
	Site  string
	Email string
}

// PrimeDeps separates offline scope inspection from authenticated collection.
// Board decoding stays with its owning package, avoiding a dependency cycle.
type PrimeDeps struct {
	Scope        func(ctx context.Context, local bool) (MetadataScope, error)
	JiraClient   func(ctx context.Context) (core.JiraClient, error)
	FetchBoards  func(context.Context, core.JiraClient, bool) (MetadataValue, error)
	DecodeBoards func([]byte) (MetadataValue, error)
}

type primeOptions struct {
	force, unbounded bool
	ttlMinutes       int
	withoutSpinner   bool
}

func addPrimers(cmd *cobra.Command, deps PrimeDeps) {
	// Explicit operations let the source audit attribute the shared renderer.
	cmd.AddCommand(primerCommand(deps, "labels", command.Spec[envelope.CachePrimeOutput]{Op: "cache.labels", Human: renderPrime, Passthrough: true}))
	cmd.AddCommand(primerCommand(deps, "projects", command.Spec[envelope.CachePrimeOutput]{Op: "cache.projects", Human: renderPrime, Passthrough: true}))
	cmd.AddCommand(primerCommand(deps, "epics", command.Spec[envelope.CachePrimeOutput]{Op: "cache.epics", Human: renderPrime, Passthrough: true}))
	cmd.AddCommand(primerCommand(deps, "fields", command.Spec[envelope.CachePrimeOutput]{Op: "cache.fields", Human: renderPrime, Passthrough: true}))
	cmd.AddCommand(primerCommand(deps, "issuetypes", command.Spec[envelope.CachePrimeOutput]{Op: "cache.issuetypes", Human: renderPrime, Passthrough: true}))
	cmd.AddCommand(primerCommand(deps, "linktypes", command.Spec[envelope.CachePrimeOutput]{Op: "cache.linktypes", Human: renderPrime, Passthrough: true}))
	cmd.AddCommand(primerCommand(deps, "boards", command.Spec[envelope.CachePrimeOutput]{Op: "cache.boards", Human: renderPrime, Passthrough: true}))
	cmd.AddCommand(primerCommand(deps, "statuses", command.Spec[envelope.CachePrimeOutput]{Op: "cache.statuses", Human: renderPrime, Passthrough: true}))
	cmd.AddCommand(primerCommand(deps, "priorities", command.Spec[envelope.CachePrimeOutput]{Op: "cache.priorities", Human: renderPrime, Passthrough: true}))
	cmd.AddCommand(primerCommand(deps, "resolutions", command.Spec[envelope.CachePrimeOutput]{Op: "cache.resolutions", Human: renderPrime, Passthrough: true}))
}

func primerCommand(deps PrimeDeps, name string, spec command.Spec[envelope.CachePrimeOutput]) *cobra.Command {
	resource, ok := metadataResource(name)
	if !ok {
		panic("cache: primer resource is not in the metadata catalog")
	}
	var opts primeOptions
	spec.Use, spec.Short = name, "Cache visible "+name+" for the selected account"
	spec.Long = "Read visible " + name + " and store metadata in the configured site/account namespace. " +
		"A validated fresh entry avoids Jira reads; --force bypasses freshness. --ttl-minutes overrides this resource's default; nonpositive uses that default. " +
		"The ordinary live cache can resolve an account held only in keyring.\n\n" +
		"--dry-run inspects local metadata and reports would_fetch without authentication, Jira requests, deletion or storage creation. " +
		"It requires non-secret base_url and email in configuration or environment; keyring-only identity is unavailable locally. " +
		"Neither local previews nor priming change Jira; live priming works in read-only mode without confirmation.\n\n" +
		"Validated native values and pagination facts survive partial reads. Failed reads never replace a previous entry. " +
		"Bounded successful walks can store an explicitly incomplete snapshot; --unbounded removes collection caps and refetches capped snapshots. " +
		"Board project enrichment retains its own bounds and diagnostics. Storage errors retain fetched data with saved=false in an error frame. " +
		"Board lists and link types reuse their existing consumer formats and keys; other metadata uses versioned snapshots."
	spec.Example = "$ jira cache " + name + " --output=json\n\n$ jira cache " + name + " --force --output=json\n\n$ jira cache " + name + " --dry-run --output=json"
	spec.MutatesLocalState = true
	spec.Flags = func(cmd *cobra.Command) {
		var mut command.MutationFlags
		command.AddMutationFlags(cmd, &mut)
		command.AddBoolVar(cmd, &opts.force, command.FlagForce, false, "Bypass cached freshness and fetch this resource again", command.FlagOpts{Terse: "Fetch fresh metadata"})
		command.AddIntVar(cmd, &opts.ttlMinutes, FlagTTLMinutes, int(resource.DefaultTTL/time.Minute), "Cache freshness in minutes; nonpositive uses this resource's default", command.FlagOpts{Terse: "Cache lifetime", Placeholder: "n"})
		if resource.paged || name == "boards" || name == "epics" {
			command.AddBoolVar(cmd, &opts.unbounded, "unbounded", false, "Remove metadata collection caps; board project enrichment remains bounded", command.FlagOpts{Terse: "Remove collection caps"})
		}
	}
	spec.Validate = func() error { return (Options{TTLMinutes: opts.ttlMinutes}).Validate() }
	spec.RetainOnError = func(out envelope.CachePrimeOutput) bool { return out.Resource != "" }
	spec.Warnings = primeWarnings
	spec.Run = func(ctx context.Context, inv command.Invocation) (envelope.CachePrimeOutput, error) {
		return runPrime(ctx, deps, inv, resource, opts)
	}
	return command.MustNew(spec)
}

func runPrime(ctx context.Context, deps PrimeDeps, inv command.Invocation, resource MetadataResource, opts primeOptions) (envelope.CachePrimeOutput, error) {
	ttl := resource.DefaultTTL
	out := envelope.CachePrimeOutput{MutationState: envelope.MutationState{DryRun: inv.DryRun}, Resource: resource.Name}
	out.Items = []json.RawMessage{}
	if err := (Options{TTLMinutes: opts.ttlMinutes}).Validate(); err != nil {
		return out, err
	}
	if opts.ttlMinutes > 0 {
		ttl = time.Duration(opts.ttlMinutes) * time.Minute
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if deps.Scope == nil {
		return out, cacheIO(core.ErrCacheManagementUnsupported)
	}
	scope, err := deps.Scope(ctx, inv.DryRun)
	if err != nil {
		return out, cacheIO(err)
	}
	if scope.Store == nil || scope.Site == "" {
		return out, cacheIO(core.ErrCacheManagementUnsupported)
	}
	value, written, state := InspectMetadata(scope.Store, scope.Site, resource.Name, ttl, deps.DecodeBoards)
	out.CacheState = state
	out.WouldFetch = opts.force || state != "fresh" || opts.unbounded && !value.Complete
	if inv.DryRun || !out.WouldFetch {
		if !written.IsZero() {
			out.WrittenAt = written.UTC().Format(time.RFC3339Nano)
		}
		out.CacheMetadata = value
		out.FromCache = !written.IsZero() && state != "malformed"
		return out, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if deps.JiraClient == nil {
		return out, metadataInvalid("metadata client constructor is unavailable")
	}
	client, err := deps.JiraClient(ctx)
	if err != nil {
		return out, err
	}
	if client == nil {
		return out, metadataInvalid("metadata client constructor returned no client")
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.Attempted = true
	fetch := func(ctx context.Context) (MetadataValue, error) {
		var boards BoardMetadataFetcher
		if deps.FetchBoards != nil {
			boards = func(ctx context.Context, unbounded bool) (MetadataValue, error) {
				return deps.FetchBoards(ctx, client, unbounded)
			}
		}
		return FetchMetadata(ctx, client, boards, resource.Name, opts.unbounded)
	}
	if opts.withoutSpinner {
		out.CacheMetadata, err = fetch(ctx)
	} else {
		out.CacheMetadata, err = command.Spin(ctx, "Reading "+resource.Name+" metadata", fetch)
	}
	if err != nil {
		return out, err
	}
	raw, err := encodeMetadata(resource.Name, out.CacheMetadata, deps.DecodeBoards)
	if err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := scope.Store.Set(resource.Key(scope.Site), raw); err != nil {
		return out, cacheIO(err)
	}
	out.Saved = true
	out.WrittenAt = time.Now().UTC().Format(time.RFC3339Nano)
	return out, ctx.Err()
}

func renderPrime(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.CachePrimeOutput)
	if !ok {
		return fmt.Errorf("cache primer: unexpected payload %T", env.Data)
	}
	if out.CacheState == "" {
		v.Line("could not inspect", cli.Text(out.Resource), "cache")
		return nil
	}
	if out.DryRun {
		if out.WouldFetch {
			v.Line("would fetch", cli.Text(out.Resource), "and store the validated metadata")
		} else {
			v.Line("would reuse", cli.Text(out.Resource), "from the fresh local cache")
		}
		return nil
	}
	if !out.Attempted && !out.FromCache {
		v.Line("could not fetch", cli.Text(out.Resource), "metadata")
		return nil
	}
	if out.Attempted && out.Pages == 0 && !out.Saved {
		v.Line("could not read", cli.Text(out.Resource), "metadata")
		return nil
	}
	verb := "fetched"
	if out.FromCache {
		verb = "reused"
	}
	v.Line(cli.Text(fmt.Sprintf("%s %d %s (%s cache).", verb, out.Count, out.Resource, out.CacheState)))
	if out.Attempted && !out.Saved {
		v.Hint("Metadata was not saved. Inspect the failure before retrying.")
	}
	return nil
}

func primeWarnings(out envelope.CachePrimeOutput) []envelope.Warning {
	if out.Pages > 0 && !out.Complete {
		return []envelope.Warning{{Type: "cache_truncated", Message: "Metadata is incomplete; inspect its resume cursor and truncated_reason. Use --force --unbounded to remove collection caps.", Lossy: true}}
	}
	return nil
}

func metadataResource(name string) (MetadataResource, bool) {
	for _, resource := range MetadataResources() {
		if resource.Name == name {
			return resource, true
		}
	}
	return MetadataResource{}, false
}

// Serialization always uses the pinned preservation-grade wire encoder.
func encodeMetadata(name string, value MetadataValue, boards func([]byte) (MetadataValue, error)) ([]byte, error) {
	if err := validateStoredMetadata(name, value, boards); err != nil {
		return nil, err
	}
	switch name {
	case "boards":
		return value.Payload, nil
	case "linktypes":
		return jira.MarshalNoEscape(value.Items)
	default:
		return jira.MarshalNoEscape(metadataSnapshot{Version: 1, MetadataValue: value})
	}
}
