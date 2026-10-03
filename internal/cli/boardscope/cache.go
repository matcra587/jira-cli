package boardscope

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/core"
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
	kind, selector := "name", name
	if id > 0 {
		kind, selector = "id", strconv.Itoa(id)
	}
	if store == nil {
		return Resolution{}, time.Time{}, fmt.Errorf("board cache is unavailable")
	}
	raw, written, ok := store.Get(CacheKey(site, kind, selector))
	if !ok {
		return Resolution{}, time.Time{}, fmt.Errorf("no cached board resolution")
	}
	var board Resolution
	if written.IsZero() || written.After(time.Now()) || json.Unmarshal(raw, &board) != nil || board.ID <= 0 || strings.TrimSpace(board.JQL) == "" ||
		(id > 0 && board.ID != int64(id)) || (id == 0 && !strings.EqualFold(strings.TrimSpace(board.Name), strings.TrimSpace(name))) {
		return Resolution{}, time.Time{}, fmt.Errorf("cached board resolution is malformed or does not match the selector")
	}
	return board, written, nil
}
