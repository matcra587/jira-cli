package cache

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
)

const (
	FlagRefresh    = "refresh"
	FlagTTLMinutes = "ttl-minutes"
	// DefaultTTL preserves the metadata cache's existing one-day freshness.
	DefaultTTL = 24 * time.Hour
)

// Options govern freshness. Nonpositive TTL uses the default; refresh always
// invalidates the old entry before fetching, even when that fetch then fails.
type Options struct {
	Refresh    bool
	TTLMinutes int
}

func (o Options) Lifetime() time.Duration {
	if o.TTLMinutes <= 0 {
		return DefaultTTL
	}
	// Defensive floor for internal callers; public flag validation refuses
	// overflow rather than wrapping it into a negative freshness window.
	if uint64(o.TTLMinutes) > uint64(math.MaxInt64/int64(time.Minute)) {
		return DefaultTTL
	}
	return time.Duration(o.TTLMinutes) * time.Minute
}

func (o Options) Validate() error {
	if o.TTLMinutes > 0 && uint64(o.TTLMinutes) > uint64(math.MaxInt64/int64(time.Minute)) {
		err := cli.NewInputError(cli.CodeFlagValueInvalid, "--ttl-minutes exceeds the supported duration")
		err.Field = FlagTTLMinutes
		return err
	}
	return nil
}

// AddFlags binds caller-owned fields so domains share policy and help metadata
// without having to adopt a particular embedding or private field layout.
func AddFlags(cmd *cobra.Command, refresh *bool, ttlMinutes *int, noun string) {
	command.AddBoolVar(cmd, refresh, FlagRefresh, false, "Ignore any cached "+noun+" and read it from the site", command.FlagOpts{Terse: "Bypass the cache"})
	command.AddIntVar(cmd, ttlMinutes, FlagTTLMinutes, int(DefaultTTL/time.Minute), "How many minutes a cached "+noun+" stays usable; nonpositive uses the default", command.FlagOpts{Terse: "Cache lifetime", Placeholder: "n"})
}

// Read records both the value and cache provenance. A failed fetch retains its
// partial value, but never stores it. Storage failures only cost a future read.
type Read[T any] struct {
	Value       T
	Cached      bool
	Age         time.Duration
	Written     time.Time
	SourceState string
	Saved       bool
}

func ReadValue[T any](ctx context.Context, store core.Cache, key string, opts Options, valid func(T) bool, fetch func(context.Context) (T, error)) (Read[T], error) {
	state := "refresh"
	if opts.Refresh {
		_ = store.Forget(key) //nolint:errcheck // unavailable cache cannot prevent a live read
	} else {
		value, written, source := lookup(store, key, opts.Lifetime(), valid)
		state = source
		if source == "fresh" {
			return Read[T]{Value: value, Cached: true, Age: time.Since(written), Written: written, SourceState: source, Saved: true}, nil
		}
	}
	value, err := fetch(ctx)
	read := Read[T]{Value: value, SourceState: state, Written: time.Now().UTC()}
	if err != nil {
		return read, err
	}
	read.Saved = save(store, key, value)
	return read, nil
}

func lookup[T any](store core.Cache, key string, ttl time.Duration, valid func(T) bool) (T, time.Time, string) {
	var zero T
	if _, disabled := store.(Disabled); disabled {
		return zero, time.Time{}, "disabled"
	}
	raw, written, ok := store.Get(key)
	if !ok {
		return zero, time.Time{}, "missing"
	}
	age := time.Since(written)
	if age < 0 || age > ttl {
		return zero, time.Time{}, "stale"
	}
	var value T
	if json.Unmarshal(raw, &value) != nil || !valid(value) {
		return zero, time.Time{}, "malformed"
	}
	return value, written, "fresh"
}

func save[T any](store core.Cache, key string, value T) bool {
	if _, disabled := store.(Disabled); disabled {
		return false
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return false
	}
	return store.Set(key, raw) == nil
}

// Open degrades a missing or failed constructor to a cache that always misses.
// Commands still return the answer they fetched when storage is unavailable.
func Open(constructor func() (core.Cache, error)) core.Cache {
	if constructor == nil {
		return Disabled{}
	}
	store, err := constructor()
	if err != nil || store == nil {
		return Disabled{}
	}
	return store
}

type Disabled struct{}

func (Disabled) Get(string) ([]byte, time.Time, bool) { return nil, time.Time{}, false }
func (Disabled) Set(string, []byte) error             { return nil }
func (Disabled) Forget(string) error                  { return nil }
