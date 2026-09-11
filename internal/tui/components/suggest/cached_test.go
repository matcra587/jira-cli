package suggest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clock is a fake, race-safe monotonic clock. Its nanosecond field is atomic
// so a background fetch reading now() cannot race a test advancing it; tests
// still only advance between synchronized Gets to keep expiry deterministic.
type clock struct {
	ns atomic.Int64
}

func (c *clock) now() time.Time { return time.Unix(0, c.ns.Load()) }

func (c *clock) advance(d time.Duration) { c.ns.Add(int64(d)) }

// countingSource returns a fixed value and counts how many times it ran, so a
// cache hit is observable as an unchanged call count.
func countingSource(value string) (func(context.Context, string) (string, error), *atomic.Int64) {
	var calls atomic.Int64
	return func(_ context.Context, _ string) (string, error) {
		calls.Add(1)
		return value, nil
	}, &calls
}

func TestGet(t *testing.T) {
	ctx := context.Background()
	const ttl = time.Minute

	t.Run("hit within ttl calls source once", func(t *testing.T) {
		clk := &clock{}
		source, calls := countingSource("bug")
		c := New(ttl, source, clk.now)

		for range 3 {
			got, err := c.Get(ctx, "PROJ")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != "bug" {
				t.Fatalf("got %q, want %q", got, "bug")
			}
		}
		if n := calls.Load(); n != 1 {
			t.Fatalf("source called %d times, want 1", n)
		}
	})

	t.Run("expiry refetches", func(t *testing.T) {
		clk := &clock{}
		source, calls := countingSource("bug")
		c := New(ttl, source, clk.now)

		if _, err := c.Get(ctx, "PROJ"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// One tick before expiry is still a hit; reaching storedAt+ttl is a
		// miss, per the >= expiry boundary.
		clk.advance(ttl - time.Nanosecond)
		if _, err := c.Get(ctx, "PROJ"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n := calls.Load(); n != 1 {
			t.Fatalf("source called %d times before expiry, want 1", n)
		}
		clk.advance(time.Nanosecond)
		if _, err := c.Get(ctx, "PROJ"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n := calls.Load(); n != 2 {
			t.Fatalf("source called %d times after expiry, want 2", n)
		}
	})

	t.Run("distinct keys independent", func(t *testing.T) {
		clk := &clock{}
		var calls atomic.Int64
		source := func(_ context.Context, key string) (string, error) {
			calls.Add(1)
			return "types-for-" + key, nil
		}
		c := New(ttl, source, clk.now)

		for range 2 {
			a, err := c.Get(ctx, "AAA")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a != "types-for-AAA" {
				t.Fatalf("got %q, want %q", a, "types-for-AAA")
			}
			b, err := c.Get(ctx, "BBB")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if b != "types-for-BBB" {
				t.Fatalf("got %q, want %q", b, "types-for-BBB")
			}
		}
		// Two keys, each fetched once and then served from cache.
		if n := calls.Load(); n != 2 {
			t.Fatalf("source called %d times, want 2", n)
		}
	})

	t.Run("error not cached", func(t *testing.T) {
		clk := &clock{}
		wantErr := errors.New("jira unreachable")
		var calls atomic.Int64
		source := func(_ context.Context, _ string) (string, error) {
			// Fail the first call, succeed thereafter.
			if calls.Add(1) == 1 {
				return "", wantErr
			}
			return "story", nil
		}
		c := New(ttl, source, clk.now)

		if _, err := c.Get(ctx, "PROJ"); !errors.Is(err, wantErr) {
			t.Fatalf("got error %v, want %v", err, wantErr)
		}
		// A failure is not stored, so the very next Get retries (no clock
		// advance) rather than serving a cached error.
		got, err := c.Get(ctx, "PROJ")
		if err != nil {
			t.Fatalf("unexpected error on retry: %v", err)
		}
		if got != "story" {
			t.Fatalf("got %q, want %q", got, "story")
		}
		if n := calls.Load(); n != 2 {
			t.Fatalf("source called %d times, want 2", n)
		}
	})

	t.Run("stale discard newer value stands", func(t *testing.T) {
		clk := &clock{}
		// Two channels fully sequence the two fetches with no sleeps or
		// timers. entered[n] is closed when the n-th source call begins (so
		// the test knows its seq is reserved); release[n] gates its
		// completion (so the test controls finish order). We make the OLD
		// (first-started, lower-seq) fetch complete LAST and assert it does
		// not clobber the newer value.
		entered := []chan struct{}{make(chan struct{}), make(chan struct{})}
		release := []chan struct{}{make(chan struct{}), make(chan struct{})}
		var started atomic.Int64
		source := func(_ context.Context, _ string) (string, error) {
			n := started.Add(1) // 1 for the old fetch, 2 for the new one
			close(entered[n-1])
			<-release[n-1]
			if n == 1 {
				return "old", nil
			}
			return "new", nil
		}
		c := New(ttl, source, clk.now)

		var wg sync.WaitGroup
		wg.Go(func() {
			// Old, slow fetch: starts first (reserves the lower seq) but is
			// released last.
			if _, err := c.Get(ctx, "PROJ"); err != nil {
				t.Errorf("old get error: %v", err)
			}
		})

		// Block until the old fetch has entered source and reserved its seq
		// before starting the new one, so start order is deterministic.
		<-entered[0]

		wg.Go(func() {
			if _, err := c.Get(ctx, "PROJ"); err != nil {
				t.Errorf("new get error: %v", err)
			}
		})
		<-entered[1]

		// Finish the NEW fetch first (commits "new"), then the OLD fetch
		// (must be discarded as stale).
		close(release[1])
		close(release[0])
		wg.Wait()

		// The cached value must be the newer fetch's result, even though the
		// older fetch wrote to the slot afterward.
		got, err := c.Get(ctx, "PROJ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "new" {
			t.Fatalf("got %q, want %q — stale fetch clobbered the newer value", got, "new")
		}
	})
}
