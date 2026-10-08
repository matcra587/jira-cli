package cache

import (
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type transitionHintsWrittenStore struct {
	core.Cache
	written time.Time
}

func (s transitionHintsWrittenStore) Get(key string) ([]byte, time.Time, bool) {
	raw, _, ok := s.Cache.Get(key)
	return raw, s.written, ok
}

func transitionHintFixture(t *testing.T) *jira.Transitions {
	t.Helper()
	var list jira.Transitions
	if err := jsonv2.Unmarshal([]byte(`{"transitions":[
        {"id":"31","name":"Start","hasScreen":true,"to":{"id":"10010","name":"In Progress"},"fields":{"customfield_123":{"required":false,"defaultValue":"opaque-screen-default"}},"extension":"native-only"},
        {"id":"52","name":"Unavailable","hasScreen":false,"isAvailable":false},
        {"id":"78","name":"Finish","hasScreen":false,"isAvailable":true,"to":{"id":"10020","name":"Done"}}
    ]}`), &list); err != nil {
		t.Fatal(err)
	}
	return &list
}

func TestTransitionHintsPreserveEligibleIdentitiesWithoutScreenData(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	observed := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	store := &primerStore{Cache: &memory, written: observed.Add(time.Second)}
	list := transitionHintFixture(t)
	if err := RecordTransitionHints(t.Context(), store, " demo-1 ", list, observed); err != nil {
		t.Fatal(err)
	}
	hints, captured, state := transitionHintsAt(store, "DEMO-1", observed.Add(time.Minute))
	want := []TransitionHint{
		{ID: "31", Name: "Start", StatusID: "10010", Status: "In Progress"},
		{ID: "78", Name: "Finish", StatusID: "10020", Status: "Done"},
	}
	if state != "fresh" || captured != observed || !reflect.DeepEqual(hints, want) {
		t.Fatalf("hints=%#v captured=%v state=%s", hints, captured, state)
	}
	raw, _, _ := memory.Get(TransitionHintsPrefix + "DEMO-1")
	if strings.Contains(string(raw), "opaque-screen-default") || strings.Contains(string(raw), "native-only") || strings.Contains(string(raw), "Unavailable") {
		t.Fatal("hint snapshot copied screen defaults, native extensions or unavailable transitions")
	}
	if list.Transitions.Value[0].Fields.Value["customfield_123"] == nil || list.Transitions.Value[0].Extra["extension"] == nil {
		t.Fatal("hint projection modified the native read result")
	}
	if hints, _, state := transitionHintsAt(store, "DEMO-2", observed.Add(time.Minute)); state != "missing" || len(hints) != 0 {
		t.Fatal("another issue reused the observed workflow")
	}
}

func TestTransitionHintsUseObservationAgeAndRejectFutureWrappers(t *testing.T) {
	t.Parallel()
	observed := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	var memory adapters.MemoryCache
	if err := RecordTransitionHints(t.Context(), &memory, "DEMO-1", transitionHintFixture(t), observed); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, state  string
		now, written time.Time
	}{
		{name: "fresh", now: observed.Add(time.Minute), written: observed.Add(time.Second), state: "fresh"},
		{name: "exact expiry", now: observed.Add(TransitionHintsTTL), written: observed.Add(time.Second), state: "stale"},
		{name: "later file write does not renew", now: observed.Add(time.Hour), written: observed.Add(time.Hour), state: "stale"},
		{name: "future observation", now: observed.Add(-time.Second), written: observed.Add(-time.Second), state: "stale"},
		{name: "future wrapper", now: observed.Add(time.Minute), written: observed.Add(time.Hour), state: "stale"},
		{name: "missing wrapper time", now: observed.Add(time.Minute), state: "stale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := transitionHintsWrittenStore{Cache: &memory, written: tc.written}
			hints, got, state := transitionHintsAt(store, "DEMO-1", tc.now)
			if state != tc.state || got != observed || len(hints) != 2 {
				t.Fatalf("state=%s captured=%v hints=%#v", state, got, hints)
			}
		})
	}
}

func TestTransitionHintsRejectMalformedAndCrossIssuePayloads(t *testing.T) {
	t.Parallel()
	const header = `"version":1,"issue_key":"DEMO-1","observed_at":"2020-01-02T03:04:05Z"`
	for index, raw := range []string{
		`null`, `{}`, `{` + header + `}`, `{` + header + `,"hints":null}`,
		`{` + header + `,"hints":[{"id":""}]}`,
		`{` + header + `,"hints":[{"id":"31"},{"id":"31"}]}`,
		`{` + header + `,"hints":[{"id":"31\nother"}]}`,
		`{` + header + `,"hints":[{"id":"31","status_id":"12\nother"}]}`,
		`{"version":2,"issue_key":"DEMO-1","observed_at":"2020-01-02T03:04:05Z","hints":[]}`,
		`{"version":1,"issue_key":"DEMO-2","observed_at":"2020-01-02T03:04:05Z","hints":[]}`,
		`{` + header + `,"hints":[],"version":1}`,
		strings.Repeat(" ", transitionHintsBytes+1),
	} {
		t.Run(fmt.Sprintf("case %d", index), func(t *testing.T) {
			var memory adapters.MemoryCache
			if err := memory.Set(TransitionHintsPrefix+"DEMO-1", []byte(raw)); err != nil {
				t.Fatal(err)
			}
			hints, written, state := TransitionHints(&memory, "DEMO-1")
			if hints == nil || len(hints) != 0 || !written.IsZero() || state != "malformed" {
				t.Fatalf("malformed hints=%#v captured=%v state=%s", hints, written, state)
			}
		})
	}
}

func TestTransitionHintsFailedSaveRemovesKnownOldObservation(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	list := transitionHintFixture(t)
	observed := time.Now().Add(-time.Minute)
	if err := RecordTransitionHints(t.Context(), &memory, "DEMO-1", list, observed); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("storage full")
	store := &primerStore{Cache: &memory, setErr: cause}
	if err := RecordTransitionHints(t.Context(), store, "DEMO-1", list, observed); !errors.Is(err, cause) {
		t.Fatalf("save cause lost: %v", err)
	}
	if _, _, state := TransitionHints(&memory, "DEMO-1"); state != "missing" {
		t.Fatal("failed save retained known old transition choices")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	sets, forgets := store.sets, store.forgets
	if err := RecordTransitionHints(ctx, store, "DEMO-1", list, observed); !errors.Is(err, context.Canceled) || store.sets != sets || store.forgets != forgets {
		t.Fatal("pre-canceled hint recording touched storage")
	}
}

func TestTransitionHintsAreInspectableAndClearableWithoutRemotePriming(t *testing.T) {
	t.Parallel()
	var memory adapters.MemoryCache
	observed := time.Now().Add(-time.Minute)
	if err := RecordTransitionHints(t.Context(), &memory, "DEMO-1", transitionHintFixture(t), observed); err != nil {
		t.Fatal(err)
	}
	key := TransitionHintsPrefix + "DEMO-1"
	raw, written, _ := memory.Get(key)
	store := &inspectionStore{managementStub: &managementStub{Cache: &memory, entries: []core.CacheEntry{{ID: "workflow", Key: key, Written: written, SizeBytes: int64(len(raw))}}}}
	out, err := runInspect(t.Context(), inspectionDeps(t, store), command.Invocation{Args: []string{"transitions"}})
	if err != nil || !out.Complete || len(out.Resources) != 1 || out.Count != 1 || out.Entries[0].Resource != "transitions" {
		t.Fatalf("transition inspection=%#v err=%v", out, err)
	}
	row := out.Resources[0]
	if row.IssueKey != "DEMO-1" || row.Resource != "transitions" || row.CacheState != "fresh" || row.Count != 2 || !row.Complete || row.TTLMinutes == nil || *row.TTLMinutes != 5 || row.WrittenAt != observed.UTC().Format(time.RFC3339Nano) || store.writes != 0 {
		t.Fatalf("transition producer facts=%#v writes=%d", row, store.writes)
	}
	if !slices.Contains(ClearResourceNames(), "transitions") || slices.Contains(ResourceNames(), "transitions") || resourceForKey(key) != "transitions" || resourceForKey(TransitionHintsPrefix+"invalid") != "unknown" {
		t.Fatal("issue hints lack trusted local lifecycle classification or advertise an unsupported remote primer")
	}
}

func TestTransitionHintsEnforceChoiceAndByteBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name             string
		count, nameBytes int
	}{
		{name: "choice bound", count: transitionHintsCap + 1},
		{name: "byte bound", count: 1, nameBytes: transitionHintsBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var memory adapters.MemoryCache
			observed := time.Now().Add(-time.Second)
			if err := RecordTransitionHints(t.Context(), &memory, "DEMO-1", transitionHintFixture(t), observed); err != nil {
				t.Fatal(err)
			}
			list := &jira.Transitions{}
			hints := make([]TransitionHint, tc.count)
			var native []string
			for index := range hints {
				hints[index] = TransitionHint{ID: fmt.Sprint(index + 1), Name: strings.Repeat("x", tc.nameBytes)}
				raw, err := jsonv2.Marshal(map[string]any{"id": hints[index].ID, "name": hints[index].Name, "hasScreen": false})
				if err != nil {
					t.Fatal(err)
				}
				native = append(native, string(raw))
			}
			if err := jsonv2.Unmarshal([]byte(`{"transitions":[`+strings.Join(native, ",")+`]}`), list); err != nil {
				t.Fatal(err)
			}
			if err := core.ValidateTransitions(list); err != nil {
				t.Fatalf("bound fixture is not otherwise valid: %v", err)
			}
			if err := RecordTransitionHints(t.Context(), &memory, "DEMO-1", list, observed); err == nil {
				t.Fatal("oversized writer observation accepted")
			}
			if _, _, state := TransitionHints(&memory, "DEMO-1"); state != "missing" {
				t.Fatal("oversized write retained old hints")
			}
			raw, err := jsonv2.Marshal(transitionHintsSnapshot{Version: 1, IssueKey: "DEMO-1", ObservedAt: observed, Hints: hints})
			if err != nil {
				t.Fatal(err)
			}
			if err := memory.Set(TransitionHintsPrefix+"DEMO-1", raw); err != nil {
				t.Fatal(err)
			}
			if _, _, state := TransitionHints(&memory, "DEMO-1"); state != "malformed" {
				t.Fatalf("otherwise valid oversized reader observation accepted: %s", state)
			}
		})
	}
}
