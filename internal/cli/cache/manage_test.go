package cache

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
)

type managementStub struct {
	core.Cache
	entries []core.CacheEntry
	readErr error
	deleted []string
	remove  func(context.Context, string) (bool, error)
}

func (s *managementStub) Entries(context.Context) ([]core.CacheEntry, error) {
	return slices.Clone(s.entries), s.readErr
}

func (s *managementStub) DeleteEntry(ctx context.Context, id string) (bool, error) {
	s.deleted = append(s.deleted, id)
	if s.remove != nil {
		return s.remove(ctx, id)
	}
	return true, nil
}

func TestCacheClearPreflightNeverOpensStorage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		inv    command.Invocation
		cancel bool
		code   string
	}{
		{name: "headless", inv: command.Invocation{}, cancel: false, code: cli.CodeFlagRequired},
		{name: "unknown resource", inv: command.Invocation{Args: []string{"boardz"}, DryRun: true}, cancel: false, code: cli.CodeArgValueInvalid},
		{name: "unsupported producer", inv: command.Invocation{Args: []string{"attachments"}, DryRun: true}, code: cli.CodeArgValueInvalid},
		{name: "canceled", inv: command.Invocation{DryRun: true}, cancel: true, code: cli.CodeCanceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			calls := 0
			_, err := runClear(ctx, Deps{Cache: func() (core.Cache, error) { calls++; return &managementStub{}, nil }}, tc.inv, false)
			if calls != 0 || cli.MapError(err).Code != tc.code {
				t.Fatalf("preflight = %v, calls=%d", err, calls)
			}
		})
	}
}

func TestCacheClearPreviewAndResourceSelectionPreserveOtherEntries(t *testing.T) {
	t.Parallel()
	stub := &managementStub{entries: []core.CacheEntry{
		{ID: "list", Key: BoardListKey},
		{ID: "id-selector", Key: BoardSelectorPrefix + "https://example.com:id:12"},
		{ID: "name-selector", Key: BoardSelectorPrefix + "https://example.com:name:Example board"},
		{ID: "links", Key: LinkTypesPrefix + "https://example.com"},
		{ID: "broken", Malformed: true},
	}}
	deps := Deps{Cache: func() (core.Cache, error) { return stub, nil }}
	for _, preview := range []bool{true, false} {
		out, err := runClear(t.Context(), deps, command.Invocation{Args: []string{"boards"}, DryRun: preview}, true)
		if err != nil || out.WouldRemove != 3 || out.Unclassified != 1 || len(out.Entries) != 3 {
			t.Fatalf("resource plan = %+v, %v", out, err)
		}
		for _, entry := range out.Entries {
			if entry.Resource != "boards" || entry.Removed == preview || entry.Attempted == preview {
				t.Fatalf("preview/publication receipt = %+v", entry)
			}
		}
		if preview && len(stub.deleted) != 0 || !preview && !slices.Equal(stub.deleted, []string{"list", "id-selector", "name-selector"}) {
			t.Fatalf("resource selection deleted unrelated entries: %v", stub.deleted)
		}
	}
}

func TestCacheSupportedResourcesHaveTrustedClassification(t *testing.T) {
	t.Parallel()
	keys := map[string]string{}
	for _, resource := range MetadataResources() {
		keys[resource.Name] = resource.Key("https://example.com")
	}
	for _, resource := range ResourceNames() {
		key, ok := keys[resource]
		if !ok || resourceForKey(key) != resource {
			t.Fatalf("advertised resource %q has no supported producer classification", resource)
		}
		delete(keys, resource)
	}
	if len(keys) != 0 {
		t.Fatal("supported producer missing from resource selection/completion")
	}
}

func TestCacheClearRetainsPartialInventoryAndDeletionOutcomes(t *testing.T) {
	t.Parallel()
	entries := []core.CacheEntry{{ID: "first", Key: BoardListKey}, {ID: "failed", Malformed: true}, {ID: "absent", Key: LinkTypesPrefix + "site"}}
	cause := errors.New("owned filesystem failure")
	for _, inventoryFailure := range []bool{true, false} {
		stub := &managementStub{entries: entries, remove: func(_ context.Context, id string) (bool, error) {
			if id == "failed" {
				return false, cause
			}
			return id != "absent", nil
		}}
		if inventoryFailure {
			stub.readErr = cause
		}
		out, err := runClear(t.Context(), Deps{Cache: func() (core.Cache, error) { return stub, nil }}, command.Invocation{}, true)
		if !errors.Is(err, cause) || cli.MapError(err).Code != cli.CodeCacheIO || out.WouldRemove != 3 || len(out.Entries) != 3 {
			t.Fatalf("retained outcomes = %+v, %v", out, err)
		}
		if inventoryFailure {
			if len(stub.deleted) != 0 || out.Removed != 0 {
				t.Fatal("incomplete inventory caused deletions")
			}
		} else if out.Removed != 1 || !out.Entries[0].Removed || out.Entries[1].Error == nil || out.Entries[1].Error.Code != cli.CodeCacheIO || !out.Entries[2].Attempted || out.Entries[2].Removed {
			t.Fatalf("failed/absent entries fabricated acknowledgements: %+v", out)
		}
	}
}

func TestCacheClearCancellationRetainsCompletedAndQueuedEntries(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stub := &managementStub{entries: []core.CacheEntry{{ID: "first", Key: BoardListKey}, {ID: "queued", Malformed: true}}, remove: func(context.Context, string) (bool, error) {
		cancel()
		return true, nil
	}}
	out, err := runClear(ctx, Deps{Cache: func() (core.Cache, error) { return stub, nil }}, command.Invocation{}, true)
	if !errors.Is(err, context.Canceled) || cli.MapError(err).Code != cli.CodeCanceled || out.Removed != 1 || out.WouldRemove != 2 || len(stub.deleted) != 1 || !out.Entries[0].Attempted || !out.Entries[0].Removed || out.Entries[1].Attempted || out.Entries[1].Removed {
		t.Fatalf("canceled clear lost ordered receipts: %+v, %v", out, err)
	}
}

func TestCacheClearRetainsFinalAcknowledgementAfterCancellationOrError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		cancel bool
	}{
		{name: "final cancellation", cancel: true},
		{name: "joined removal error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			cause := errors.New("failure after removal")
			if tc.cancel {
				cause = context.Canceled
			}
			stub := &managementStub{entries: []core.CacheEntry{{ID: "acknowledged", Key: BoardListKey}}, remove: func(context.Context, string) (bool, error) {
				if tc.cancel {
					cancel()
					return true, nil
				}
				return true, cause
			}}
			out, err := runClear(ctx, Deps{Cache: func() (core.Cache, error) { return stub, nil }}, command.Invocation{}, true)
			if !errors.Is(err, cause) || out.Removed != 1 || out.WouldRemove != 1 || len(out.Entries) != 1 || !out.Entries[0].Attempted || !out.Entries[0].Removed {
				t.Fatalf("final acknowledgement lost: %+v, %v", out, err)
			}
			if !tc.cancel && (out.Entries[0].Error == nil || out.Entries[0].Error.Code != cli.CodeCacheIO) {
				t.Fatal("joined deletion failure lost its classified error")
			}
		})
	}
}
