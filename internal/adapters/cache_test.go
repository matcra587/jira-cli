package adapters

import (
	"bytes"
	"sync"
	"testing"
)

func TestMemoryCache(t *testing.T) {
	t.Parallel()

	t.Run("zero value misses then stores", func(t *testing.T) {
		t.Parallel()
		var c MemoryCache
		if _, _, ok := c.Get("k"); ok {
			t.Fatal("empty cache reported a hit")
		}
		if err := c.Set("k", []byte("v")); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got, _, ok := c.Get("k")
		if !ok || !bytes.Equal(got, []byte("v")) {
			t.Fatalf("Get = %q, %v; want %q, true", got, ok, "v")
		}
	})

	t.Run("stored bytes are isolated from the caller", func(t *testing.T) {
		t.Parallel()
		var c MemoryCache
		in := []byte("original")
		if err := c.Set("k", in); err != nil {
			t.Fatalf("Set: %v", err)
		}
		in[0] = 'X' // mutating the input after Set must not reach the cache
		first, _, _ := c.Get("k")
		if !bytes.Equal(first, []byte("original")) {
			t.Fatalf("cache aliased the caller's slice: %q", first)
		}
		first[0] = 'Y' // mutating a Get result must not reach the next reader
		second, _, _ := c.Get("k")
		if !bytes.Equal(second, []byte("original")) {
			t.Fatalf("Get leaked the stored slice: %q", second)
		}
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		t.Parallel()
		var c MemoryCache
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				for range 100 {
					_ = c.Set("k", []byte("v"))
					_, _, _ = c.Get("k")
				}
			})
		}
		wg.Wait()
	})
}
