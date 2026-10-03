package issue

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/core"
)

// These private adapters preserve the issue domain's existing option/read
// layout. All cache behavior and flag metadata live in the shared owner.
const (
	flagRefresh      = metacache.FlagRefresh
	flagTTLMinutes   = metacache.FlagTTLMinutes
	DefaultConfigTTL = metacache.DefaultTTL
)

type cacheOptions struct {
	refresh    bool
	ttlMinutes int
}

func addCacheFlags(cmd *cobra.Command, opts *cacheOptions, noun string) {
	metacache.AddFlags(cmd, &opts.refresh, &opts.ttlMinutes, noun)
}

type cachedRead[T any] struct {
	value  T
	cached bool
	age    time.Duration
}

func readCached[T any](ctx context.Context, store core.Cache, key string, opts cacheOptions, valid func(T) bool, fetch func(context.Context) (T, error)) (cachedRead[T], error) {
	read, err := metacache.ReadValue(ctx, store, key, metacache.Options{Refresh: opts.refresh, TTLMinutes: opts.ttlMinutes}, valid, fetch)
	return cachedRead[T]{value: read.Value, cached: read.Cached, age: read.Age}, err
}
func issueCache(deps Deps) core.Cache { return metacache.Open(deps.Cache) }

type noCache = metacache.Disabled
