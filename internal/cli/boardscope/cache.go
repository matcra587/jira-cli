package boardscope

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/config"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/credentials"
)

// Resolution retains the board's identity and complete saved filter, including
// ordering. Composition owns splitting ORDER BY from the filter conditions.
type Resolution struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	JQL  string `json:"jql"`
}

// CacheKey isolates sites and selectors inside an account-scoped cache. Names
// and IDs stay separate: a cached name proves the live lookup's uniqueness.
// v2 invalidates the older policy that could accept a partial board-name page.
func CacheKey(site, kind, selector string) string {
	return metacache.BoardSelectorPrefix + site + ":" + kind + ":" + selector
}

// ReadCached accepts an older snapshot deliberately, without a live freshness
// claim. Missing, malformed, future-dated or mismatched records are refused.
// The caller must supply an account-scoped store; no legacy fallback exists.
func ReadCached(store core.Cache, site, name string, id int) (Resolution, time.Time, error) {
	return readCached(context.Background(), store, site, name, id)
}

func readCached(ctx context.Context, store core.Cache, site, name string, id int) (Resolution, time.Time, error) {
	kind, selector := "name", name
	if id > 0 {
		kind, selector = "id", strconv.Itoa(id)
	}
	if store == nil {
		return Resolution{}, time.Time{}, fmt.Errorf("board cache is unavailable")
	}
	raw, written, ok := store.Get(CacheKey(site, kind, selector))
	if ok {
		return decodeCached(raw, written, name, id)
	}
	// Delivered v2 keys retain authored site/name spelling. Inspect only the
	// already-selected account namespace to find equivalent spellings; never
	// read an ID record as evidence that a name was uniquely resolved.
	managed, ok := store.(core.ManagedCache)
	if !ok {
		return Resolution{}, time.Time{}, ErrCachedResolutionMissing
	}
	entries, err := managed.Entries(ctx)
	if err != nil {
		return Resolution{}, time.Time{}, err
	}
	var selected Resolution
	var selectedAt time.Time
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return Resolution{}, time.Time{}, err
		}
		if entry.Malformed {
			// The wrapper cannot prove its logical selector. A read miss in
			// this incomplete inventory is not evidence of absent scope.
			return Resolution{}, time.Time{}, fmt.Errorf("board cache contains a malformed entry; inspect or clear the selected namespace")
		}
		if !matchingKey(entry.Key, site, kind, selector) {
			continue
		}
		raw, written, found := store.Get(entry.Key)
		if !found {
			return Resolution{}, time.Time{}, fmt.Errorf("cached board changed during inspection")
		}
		board, stamp, err := decodeCached(raw, written, name, id)
		if err != nil {
			return Resolution{}, time.Time{}, err
		}
		if selected.ID != 0 && (selected.ID != board.ID || (selectedAt.Equal(stamp) && selected.JQL != board.JQL)) {
			return Resolution{}, time.Time{}, fmt.Errorf("cached board resolutions are ambiguous; refresh the exact selector")
		}
		if selected.ID == 0 || stamp.After(selectedAt) {
			selected, selectedAt = board, stamp
		}
	}
	if selected.ID == 0 {
		return Resolution{}, time.Time{}, ErrCachedResolutionMissing
	}
	return selected, selectedAt, nil
}

// matchingKey permits site spelling and case-insensitive name equivalence
// without changing delivered keys. Try each delimiter because names and URL
// paths can themselves contain the selector delimiter.
func matchingKey(key, site, kind, selector string) bool {
	payload, ok := strings.CutPrefix(key, metacache.BoardSelectorPrefix)
	if !ok {
		return false
	}
	marker := ":" + kind + ":"
	for offset := 0; offset < len(payload); {
		index := strings.Index(payload[offset:], marker)
		if index < 0 {
			return false
		}
		index += offset
		storedSite, storedSelector := payload[:index], payload[index+len(marker):]
		if credentials.NormalizeSite(storedSite) == credentials.NormalizeSite(site) &&
			((kind == "id" && storedSelector == selector) || (kind == "name" && strings.EqualFold(strings.TrimSpace(storedSelector), strings.TrimSpace(selector)))) {
			return true
		}
		offset = index + len(marker)
	}
	return false
}

func decodeCached(raw []byte, written time.Time, name string, id int) (Resolution, time.Time, error) {
	var board Resolution
	if written.IsZero() || written.After(time.Now()) || json.Unmarshal(raw, &board) != nil || board.ID <= 0 || strings.TrimSpace(board.JQL) == "" ||
		(id > 0 && board.ID != int64(id)) || (id == 0 && !strings.EqualFold(strings.TrimSpace(board.Name), strings.TrimSpace(name))) {
		return Resolution{}, time.Time{}, fmt.Errorf("cached board resolution is malformed or does not match the selector")
	}
	return board, written, nil
}

// ErrCachedResolutionMissing permits only a bounded equivalent-site retry.
// Malformed records, inventory errors and cancellation never trigger fallback.
var ErrCachedResolutionMissing = errors.New("no cached board resolution")

// ErrCachedContextInvalid marks missing public namespace, not missing credentials.
var ErrCachedContextInvalid = errors.New("cached board context is invalid")

// ResolveCached uses only the public site/account namespace and retained filter
// owner. It never opens credentials, warms metadata or claims current membership.
func ResolveCached(ctx context.Context, constructor func(site, account string) (core.Cache, error), preferences core.IssuePreferences, name string, id int) (Resolution, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return Resolution{}, time.Time{}, err
	}
	if strings.TrimSpace(preferences.BaseURL) == "" || strings.TrimSpace(preferences.Account) == "" || config.ValidateBaseURL(preferences.BaseURL) != nil {
		return Resolution{}, time.Time{}, fmt.Errorf("%w: valid public base_url and email preferences are required", ErrCachedContextInvalid)
	}
	if constructor == nil {
		return Resolution{}, time.Time{}, errors.New("public board cache is unavailable")
	}
	sites := []string{preferences.BaseURL}
	if canonical := credentials.NormalizeSite(preferences.BaseURL); canonical != preferences.BaseURL {
		// Credential records have historically normalized site spelling. The
		// public file can retain a spelling that selected a different outer
		// namespace. Retry only this known equivalent site and the same exact
		// public account; never enumerate other namespaces or open a store.
		sites = append(sites, canonical)
	}
	for _, site := range sites {
		store, err := constructor(site, preferences.Account)
		if contextErr := ctx.Err(); contextErr != nil {
			return Resolution{}, time.Time{}, contextErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Resolution{}, time.Time{}, err
		}
		if err != nil {
			return Resolution{}, time.Time{}, errors.New("public board cache is unavailable")
		}
		board, written, err := readCached(ctx, store, site, name, id)
		if contextErr := ctx.Err(); contextErr != nil {
			return Resolution{}, time.Time{}, contextErr
		}
		if !errors.Is(err, ErrCachedResolutionMissing) {
			return board, written, err
		}
	}
	return Resolution{}, time.Time{}, ErrCachedResolutionMissing
}
