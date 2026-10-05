package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

type issueKeysReadStore struct {
	core.Cache
	gets     int
	afterGet func()
}

func (s *issueKeysReadStore) Get(key string) ([]byte, time.Time, bool) {
	s.gets++
	raw, written, ok := s.Cache.Get(key)
	if s.afterGet != nil {
		s.afterGet()
	}
	return raw, written, ok
}

func TestCacheIssueKeysCancellationPreservesOnlyCompletedRead(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"before construction", "during construction", "during read"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(cli.WithFormat(t.Context(), output.FormatJSON))
			defer cancel()
			var memory adapters.MemoryCache
			if err := RecordIssueKeys(&memory, []string{"DEMO-1"}); err != nil {
				t.Fatal(err)
			}
			store := &issueKeysReadStore{Cache: &memory}
			if phase == "before construction" {
				cancel()
			}
			if phase == "during read" {
				store.afterGet = cancel
			}
			factories := 0
			cmd := newIssueKeys(Deps{Cache: func() (core.Cache, error) {
				factories++
				if phase == "during construction" {
					cancel()
				}
				return store, nil
			}})
			cmd.SetContext(ctx)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{})
			err := cmd.Execute()
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation reported success or lost its cause: %v", err)
			}
			wantFactories, wantGets := 1, 0
			if phase == "before construction" {
				wantFactories = 0
			}
			if phase == "during read" {
				wantGets = 1
			}
			if factories != wantFactories || store.gets != wantGets {
				t.Fatalf("factories=%d reads=%d, want %d/%d", factories, store.gets, wantFactories, wantGets)
			}
			var retained *cli.ResultError
			if phase != "during read" {
				if errors.As(err, &retained) {
					t.Fatal("cancellation before reading fabricated a retained receipt")
				}
				return
			}
			if !errors.As(err, &retained) {
				t.Fatalf("completed read lost its retained result: %v", err)
			}
			out, ok := retained.Data.(envelope.CacheIssueKeysOutput)
			if !ok || !slices.Equal(out.IssueKeys, []string{"DEMO-1"}) || out.Count != 1 || out.CacheState != "present" || out.WrittenAt == "" {
				t.Fatalf("completed read receipt changed: %#v", retained.Data)
			}
		})
	}
}

func TestRecentIssueKeysMergeNormalizesDeduplicatesAndCaps(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	store := &primerStore{Cache: &memory}
	if err := RecordIssueKeys(store, []string{" demo-2 ", "DEMO-1", "DEMO-2", "", "bad\nkey", "DEMO-0"}); err != nil {
		t.Fatal(err)
	}
	keys, _, state := RecentIssueKeys(store)
	if !slices.Equal(keys, []string{"DEMO-2", "DEMO-1"}) || state != "present" {
		t.Fatalf("initial normalized keys=%v state=%s, want first distinct observation order", keys, state)
	}
	if err := RecordIssueKeys(store, []string{"DEMO-3", "demo-1", "DEMO-3"}); err != nil {
		t.Fatal(err)
	}
	keys, _, state = RecentIssueKeys(store)
	if !slices.Equal(keys, []string{"DEMO-3", "DEMO-1", "DEMO-2"}) || state != "present" {
		t.Fatalf("recent keys=%v state=%s, want newest incoming order and unique old tail", keys, state)
	}
	var many []string
	for n := range 70 {
		many = append(many, fmt.Sprintf("DEMO-%d", n+10))
	}
	if err := RecordIssueKeys(store, many); err != nil {
		t.Fatal(err)
	}
	keys, _, _ = RecentIssueKeys(store)
	if len(keys) != 50 || keys[0] != "DEMO-10" || keys[49] != "DEMO-59" {
		t.Fatalf("history cap/order=%v, want first fifty distinct incoming keys", keys)
	}
	if err := RecordIssueKeys(store, []string{"DEMO-20"}); err != nil {
		t.Fatal(err)
	}
	keys, _, _ = RecentIssueKeys(store)
	if len(keys) != 50 || keys[0] != "DEMO-20" || keys[1] != "DEMO-10" || keys[49] != "DEMO-59" {
		t.Fatalf("moving an existing key lost tail/order: %v", keys)
	}
	sets := store.sets
	if err := RecordIssueKeys(store, []string{"", "invalid", "DEMO-0"}); err != nil || store.sets != sets {
		t.Fatalf("invalid identities wrote storage: err=%v sets=%d, want %d", err, store.sets, sets)
	}
}

func TestRecentIssueKeysRefusesMalformedLocalIdentities(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`null`, `{}`, `[null]`, `["DEMO-1","DEMO-1"]`, `["demo-1"]`, `["DEMO-01"]`, `["DEMO-0"]`, `["DEMO-1\nsecond"]`, `["bad"]`, `["DEMO-1",2]`} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			var memory adapters.MemoryCache
			if err := memory.Set(IssueKeysKey, []byte(raw)); err != nil {
				t.Fatal(err)
			}
			keys, written, state := RecentIssueKeys(&memory)
			if keys == nil || len(keys) != 0 || !written.IsZero() || state != "malformed" {
				t.Fatalf("untrusted keys=%v written=%v state=%s, want explicit empty malformed result", keys, written, state)
			}
			if err := RecordIssueKeys(&memory, []string{"DEMO-8"}); err != nil {
				t.Fatal(err)
			}
			keys, _, state = RecentIssueKeys(&memory)
			if !slices.Equal(keys, []string{"DEMO-8"}) || state != "present" {
				t.Fatalf("history failed to recover after a valid observation: %v %s", keys, state)
			}
		})
	}
	var memory adapters.MemoryCache
	keys := make([]string, 51)
	for n := range keys {
		keys[n] = fmt.Sprintf("DEMO-%d", n+1)
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Set(IssueKeysKey, raw); err != nil {
		t.Fatal(err)
	}
	if keys, _, state := RecentIssueKeys(&memory); len(keys) != 0 || state != "malformed" {
		t.Fatalf("oversized local history accepted: keys=%v state=%s", keys, state)
	}
}

func TestRecentIssueKeysHasNoTTLAndFailedStorageKeepsPrevious(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	if err := RecordIssueKeys(&memory, []string{"DEMO-1"}); err != nil {
		t.Fatal(err)
	}
	written := time.Now().Add(-365 * 24 * time.Hour)
	original := errors.New("storage full")
	store := &primerStore{Cache: &memory, written: written, setErr: original}
	if keys, gotWritten, state := RecentIssueKeys(store); !slices.Equal(keys, []string{"DEMO-1"}) || gotWritten != written || state != "present" {
		t.Fatalf("old recency was treated as expired: %v %v %s", keys, gotWritten, state)
	}
	if err := RecordIssueKeys(store, []string{"DEMO-2"}); !errors.Is(err, original) {
		t.Fatalf("write cause lost: %v", err)
	}
	if keys, _, _ := RecentIssueKeys(store); !slices.Equal(keys, []string{"DEMO-1"}) {
		t.Fatalf("failed write changed history: %v", keys)
	}
	if resourceForKey(IssueKeysKey) != "issuekeys" || !slices.Contains(ClearResourceNames(), "issuekeys") || slices.Contains(ResourceNames(), "issuekeys") {
		t.Fatal("local history must be classified and clearable, never advertised as a remote refresh producer")
	}
}
