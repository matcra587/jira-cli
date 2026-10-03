package cache

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

type cacheStub struct {
	raw           []byte
	written       time.Time
	present       bool
	setErr        error
	sets, forgets int
}

func (c *cacheStub) Get(string) ([]byte, time.Time, bool) { return c.raw, c.written, c.present }
func (c *cacheStub) Set(_ string, raw []byte) error {
	c.sets++
	if c.setErr != nil {
		return c.setErr
	}
	c.raw = raw
	c.written = time.Now()
	c.present = true
	return nil
}
func (c *cacheStub) Forget(string) error { c.forgets++; c.present = false; return nil }

func TestReadValueFreshnessCorruptionAndClockSkew(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw, state string
		present          bool
		age              time.Duration
		cached           bool
	}{{"fresh", "42", "fresh", true, time.Minute, true}, {"missing", "", "missing", false, 0, false}, {"stale", "42", "stale", true, 48 * time.Hour, false}, {"future", "42", "stale", true, -time.Hour, false}, {"corrupt", "{", "malformed", true, 0, false}, {"invalid", "0", "malformed", true, 0, false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := &cacheStub{raw: []byte(tc.raw), written: time.Now().Add(-tc.age), present: tc.present}
			calls := 0
			read, err := ReadValue(t.Context(), store, "resource", Options{}, func(v int) bool { return v > 0 }, func(ctx context.Context) (int, error) {
				calls++
				if ctx != t.Context() {
					t.Error("context changed")
				}
				return 99, nil
			})
			if err != nil || read.Cached != tc.cached || read.SourceState != tc.state || !read.Saved || calls != map[bool]int{true: 0, false: 1}[tc.cached] {
				t.Fatalf("cache state changed: %+v %v calls=%d", read, err, calls)
			}
		})
	}
}

func TestReadValueRefreshFailureAndStorageFailure(t *testing.T) {
	t.Parallel()
	store := &cacheStub{raw: []byte(`42`), written: time.Now(), present: true}
	cause := errors.New("offline")
	read, err := ReadValue(t.Context(), store, "resource", Options{Refresh: true}, func(int) bool { return true }, func(context.Context) (int, error) { return 9, cause })
	if !errors.Is(err, cause) || read.Value != 9 || read.Cached || read.Saved || store.present || store.sets != 0 || store.forgets != 1 {
		t.Fatalf("refresh cached failure or retained stale data: %+v %v", read, err)
	}
	store.setErr = errors.New("full disk")
	read, err = ReadValue(t.Context(), store, "resource", Options{}, func(int) bool { return true }, func(context.Context) (int, error) { return 7, nil })
	if err != nil || read.Value != 7 || read.Saved || store.sets != 1 {
		t.Fatalf("storage failure lost live answer: %+v %v", read, err)
	}
}

func TestCacheTTLBounds(t *testing.T) {
	t.Parallel()
	for _, minutes := range []int{-1, 0, 1, 1440} {
		opts := Options{TTLMinutes: minutes}
		if opts.Validate() != nil || opts.Lifetime() <= 0 {
			t.Fatalf("supported lifetime refused: %d", minutes)
		}
	}
	if int64(math.MaxInt) > math.MaxInt64/int64(time.Minute) {
		opts := Options{TTLMinutes: math.MaxInt}
		if opts.Validate() == nil || opts.Lifetime() != DefaultTTL {
			t.Fatal("duration overflow accepted or wrapped")
		}
	}
}
