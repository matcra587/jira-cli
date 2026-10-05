package cache

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	xstrings "github.com/gechr/x/strings"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func newInspect(deps PrimeDeps) *cobra.Command {
	return command.MustNew(command.Spec[envelope.CacheInspectOutput]{
		Op: "cache.inspect", Use: "inspect [RESOURCE]", Short: "Inspect the selected local cache scope",
		Long: "Inspect stored entries and validated metadata freshness for the explicitly configured site and account. " +
			"Requires public base_url and email in configuration or environment; opens no credential store or Jira client and never creates, refreshes or removes cache storage. " +
			"With a resource, select its inventory and producer summary; boards includes saved-filter selector entries. Unknown or malformed entries appear in the whole-scope inventory. " +
			"Accepted resources: " + strings.Join(ClearResourceNames(), ", ") + ".\n\n" +
			"Resource freshness uses each producer's default TTL and actual payload validator. Issue-key history has no TTL. " +
			"Inventory entries describe stored wrappers; their presence does not prove payload validity. Malformed wrappers have no trusted key, size or write time. " +
			"Resources retain collector bounds and resume facts. Use a resource primer with --dry-run to inspect native values. " +
			"Reads are not locked or atomic with other processes; a concurrent write or clear can change the later resource observation. " +
			"complete acknowledges this local inspection, not complete cached collections or Jira visibility. Failures retain completed observations with complete=false.",
		Example:       "$ jira cache inspect --output=json\n\n$ jira cache inspect boards --output=json",
		Args:          command.ArgSpec{Max: 1, Positions: []command.Arg{{Name: "RESOURCE", Predictor: "cacheclearresource"}}},
		Human:         renderInspect,
		RetainOnError: func(out envelope.CacheInspectOutput) bool { return out.Entries != nil },
		Run: func(ctx context.Context, inv command.Invocation) (envelope.CacheInspectOutput, error) {
			return runInspect(ctx, deps, inv)
		},
	})
}

func runInspect(ctx context.Context, deps PrimeDeps, inv command.Invocation) (envelope.CacheInspectOutput, error) {
	out := envelope.CacheInspectOutput{Resource: "all"}
	names := ClearResourceNames()
	if len(inv.Args) != 0 {
		out.Resource = inv.Args[0]
		if !slices.Contains(names, out.Resource) {
			err := cli.NewInputError(cli.CodeArgValueInvalid, "unknown cache resource")
			err.Field, err.Suggestions = "RESOURCE", cli.SuggestValues(out.Resource, names)
			return out, err
		}
		names = []string{out.Resource}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if deps.Scope == nil {
		return out, cacheIO(core.ErrCacheManagementUnsupported)
	}
	scope, err := deps.Scope(true)
	if err != nil {
		return out, cacheIO(err)
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	managed, ok := scope.Store.(core.ManagedCache)
	if !ok || xstrings.AnyEmpty(scope.Site, scope.Email) {
		return out, cacheIO(core.ErrCacheManagementUnsupported)
	}
	out.BaseURL, out.Email = scope.Site, scope.Email
	entries, err := managed.Entries(ctx)
	out.Entries = []envelope.CacheInspectEntry{}
	out.Resources = []envelope.CacheInspectResource{}
	for _, entry := range entries {
		resource := resourceForKey(entry.Key)
		if entry.Malformed || resource == "unknown" {
			out.Unclassified++
			resource = "unknown"
		}
		if out.Resource != "all" && out.Resource != resource {
			continue
		}
		row := envelope.CacheInspectEntry{ID: entry.ID, Resource: resource, Malformed: entry.Malformed}
		if !entry.Malformed {
			row.Key, row.SizeBytes = entry.Key, &entry.SizeBytes
			row.WrittenAt, row.AgeSeconds = inspectionTime(entry.Written)
			out.KnownPayloadBytes += entry.SizeBytes
		}
		out.Entries = append(out.Entries, row)
	}
	out.Count = len(out.Entries)
	if failure := errors.Join(err, ctx.Err()); failure != nil {
		return out, cacheIO(failure)
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		row := envelope.CacheInspectResource{Resource: name}
		var written time.Time
		if name == "issuekeys" {
			keys, timestamp, state := RecentIssueKeys(scope.Store)
			written, row.CacheState, row.Count, row.Complete = timestamp, state, len(keys), state == "present"
		} else {
			resource, _ := metadataResource(name)
			ttlMinutes := int(resource.DefaultTTL / time.Minute)
			row.TTLMinutes = &ttlMinutes
			value, timestamp, state := InspectMetadata(scope.Store, scope.Site, name, resource.DefaultTTL, deps.DecodeBoards)
			written, row.CacheState = timestamp, state
			row.Count, row.Pages, row.Complete = value.Count, value.Pages, value.Complete
			row.TruncatedReason, row.NextStart, row.NextPageToken = value.TruncatedReason, value.NextStart, value.NextPageToken
		}
		row.WrittenAt, row.AgeSeconds = inspectionTime(written)
		out.Resources = append(out.Resources, row)
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.Complete = true
	return out, nil
}

func inspectionTime(written time.Time) (string, int64) {
	if written.IsZero() {
		return "", 0
	}
	return written.UTC().Format(time.RFC3339Nano), max(int64(time.Since(written).Seconds()), 0)
}

func renderInspect(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.CacheInspectOutput)
	if !ok {
		return fmt.Errorf("cache.inspect: unexpected payload %T", env.Data)
	}
	v.Line("cache scope", cli.Text(out.BaseURL), cli.Text(out.Email))
	v.Line(strconv.Itoa(out.Count), "cached entries for", cli.Text(out.Resource), "with", strconv.FormatInt(out.KnownPayloadBytes, 10), "known payload bytes")
	if !out.Complete {
		v.Line("inspection incomplete; retained observations may omit entries or resources")
	}
	for _, row := range out.Resources {
		v.Line(cli.Text(row.Resource), cli.Text(row.CacheState), strconv.Itoa(row.Count), "cached items")
		if row.TruncatedReason != "" {
			v.Line("collection bounded by", cli.Text(row.TruncatedReason))
		}
	}
	for _, row := range out.Entries {
		if row.Resource == "unknown" {
			v.Line("unclassified entry", cli.Text(row.ID))
		} else {
			v.Line("stored entry", cli.Text(row.Key), "written", cli.Text(row.WrittenAt))
		}
	}
	return nil
}
