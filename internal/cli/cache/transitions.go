package cache

import (
	"context"
	jsonv2 "encoding/json/v2"
	"fmt"
	"strings"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

const (
	TransitionHintsPrefix = "v1:issue-transitions:"
	TransitionHintsTTL    = 5 * time.Minute
	transitionHintsCap    = 1000
	transitionHintsBytes  = 256 * 1024
)

// TransitionHint retains only a transition's identity and display context.
// Screen fields, defaults and native extensions stay in the live read result.
// Hints never supply permission or substitute for workflow validation.
type TransitionHint struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	StatusID string `json:"status_id"`
	Status   string `json:"status"`
}

type transitionHintsSnapshot struct {
	Version    int              `json:"version"`
	IssueKey   string           `json:"issue_key"`
	ObservedAt time.Time        `json:"observed_at"`
	Hints      []TransitionHint `json:"hints"`
}

func transitionHintsKey(key string) (string, error) {
	normalized, err := command.ParseIssueKey(strings.ToUpper(strings.TrimSpace(key)))
	if err != nil {
		return "", err
	}
	return TransitionHintsPrefix + normalized, nil
}

// RecordTransitionHints stores a complete successful observation in the caller's
// site/account namespace. It replaces disposable old hints before saving, so a
// failed write does not intentionally retain known outdated choices.
func RecordTransitionHints(ctx context.Context, store core.Cache, key string, list *jira.Transitions, observed time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	storageKey, err := transitionHintsKey(key)
	if err != nil {
		return err
	}
	if store == nil {
		return nil
	}
	if _, disabled := store.(Disabled); disabled {
		return nil
	}
	if err := store.Forget(storageKey); err != nil {
		return err
	}
	if err := core.ValidateTransitions(list); err != nil {
		return err
	}
	if observed.IsZero() || observed.After(time.Now()) || len(list.Transitions.Value) > transitionHintsCap {
		return fmt.Errorf("transition hint observation is invalid or exceeds its bound")
	}
	snapshot := transitionHintsSnapshot{
		Version: 1, IssueKey: strings.TrimPrefix(storageKey, TransitionHintsPrefix),
		ObservedAt: observed.UTC(), Hints: []TransitionHint{},
	}
	for _, transition := range list.Transitions.Value {
		if transition.IsAvailable.Set && !transition.IsAvailable.Value {
			continue
		}
		hint := TransitionHint{ID: transition.ID.Value}
		if transition.Name.Set && !transition.Name.Null {
			hint.Name = transition.Name.Value
		}
		if transition.To.Set && !transition.To.Null {
			if status := transition.To.Value; status.ID.Set && !status.ID.Null {
				hint.StatusID = status.ID.Value
			}
			if status := transition.To.Value; status.Name.Set && !status.Name.Null {
				hint.Status = status.Name.Value
			}
		}
		if !validTransitionHint(hint) {
			return fmt.Errorf("transition hint identity cannot be represented")
		}
		snapshot.Hints = append(snapshot.Hints, hint)
	}
	raw, err := jsonv2.Marshal(snapshot)
	if err != nil {
		return err
	}
	if len(raw) > transitionHintsBytes {
		return fmt.Errorf("transition hint snapshot exceeds its byte bound")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return store.Set(storageKey, raw)
}

func validTransitionHint(hint TransitionHint) bool {
	if strings.TrimSpace(hint.ID) == "" || strings.TrimSpace(hint.ID) != hint.ID || cli.Text(hint.ID) != hint.ID {
		return false
	}
	return strings.TrimSpace(hint.StatusID) == hint.StatusID && cli.Text(hint.StatusID) == hint.StatusID
}

// TransitionHints reads bounded local data without credentials or Jira access.
// Valid stale observations remain inspectable; completion must require fresh.
// The observation timestamp, not a later file write, starts the five-minute TTL.
func TransitionHints(store core.Cache, key string) ([]TransitionHint, time.Time, string) {
	return transitionHintsAt(store, key, time.Now())
}

func transitionHintsAt(store core.Cache, key string, now time.Time) ([]TransitionHint, time.Time, string) {
	empty := []TransitionHint{}
	storageKey, err := transitionHintsKey(key)
	if err != nil || store == nil {
		return empty, time.Time{}, "missing"
	}
	if _, disabled := store.(Disabled); disabled {
		return empty, time.Time{}, "disabled"
	}
	raw, written, ok := store.Get(storageKey)
	if !ok {
		return empty, time.Time{}, "missing"
	}
	var snapshot transitionHintsSnapshot
	if len(raw) > transitionHintsBytes || jsonv2.Unmarshal(raw, &snapshot) != nil || snapshot.Version != 1 ||
		snapshot.IssueKey != strings.TrimPrefix(storageKey, TransitionHintsPrefix) || snapshot.Hints == nil ||
		len(snapshot.Hints) > transitionHintsCap || snapshot.ObservedAt.IsZero() {
		return empty, time.Time{}, "malformed"
	}
	seen := make(map[string]bool, len(snapshot.Hints))
	for _, hint := range snapshot.Hints {
		if !validTransitionHint(hint) || seen[hint.ID] {
			return empty, time.Time{}, "malformed"
		}
		seen[hint.ID] = true
	}
	age := now.Sub(snapshot.ObservedAt)
	if age < 0 || age >= TransitionHintsTTL || written.IsZero() || written.After(now) {
		return snapshot.Hints, snapshot.ObservedAt, "stale"
	}
	return snapshot.Hints, snapshot.ObservedAt, "fresh"
}

// ForgetTransitionHints invalidates disposable issue hints after failed discovery
// or an attempted write. Storage failures must not replace the operation result.
func ForgetTransitionHints(store core.Cache, key string) error {
	storageKey, err := transitionHintsKey(key)
	if err != nil || store == nil {
		return err
	}
	return store.Forget(storageKey)
}

func inspectTransitionHints(ctx context.Context, store core.Cache, entries []core.CacheEntry) ([]envelope.CacheInspectResource, error) {
	rows := []envelope.CacheInspectResource{}
	ttlMinutes := int(TransitionHintsTTL / time.Minute)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return rows, err
		}
		if entry.Malformed || resourceForKey(entry.Key) != "transitions" {
			continue
		}
		key := strings.TrimPrefix(entry.Key, TransitionHintsPrefix)
		hints, observed, state := TransitionHints(store, key)
		row := envelope.CacheInspectResource{
			Resource: "transitions", IssueKey: key, TTLMinutes: &ttlMinutes,
			CacheState: state, Count: len(hints), Complete: state == "fresh" || state == "stale",
		}
		if row.Complete {
			row.Pages = 1
		}
		row.WrittenAt, row.AgeSeconds = inspectionTime(observed)
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		rows = append(rows, envelope.CacheInspectResource{Resource: "transitions", CacheState: "missing", TTLMinutes: &ttlMinutes})
	}
	return rows, ctx.Err()
}
