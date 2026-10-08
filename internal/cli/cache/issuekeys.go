package cache

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

const (
	IssueKeysKey = "v1:issuekeys"
	IssueKeysCap = 50
)

// RecentIssueKeys reads bounded local history without a freshness window. A
// malformed or unavailable entry supplies no candidates, never a guessed key.
func RecentIssueKeys(store core.Cache) (keys []string, written time.Time, state string) {
	keys = []string{}
	if store == nil {
		return keys, written, "missing"
	}
	if _, disabled := store.(Disabled); disabled {
		return keys, written, "disabled"
	}
	raw, written, ok := store.Get(IssueKeysKey)
	if !ok {
		return keys, time.Time{}, "missing"
	}
	var decoded []string
	if jsonv2.Unmarshal(raw, &decoded) != nil || decoded == nil || len(decoded) > IssueKeysCap {
		return keys, time.Time{}, "malformed"
	}
	seen := make(map[string]bool, len(decoded))
	for _, key := range decoded {
		normalized, err := command.ParseIssueKey(key)
		if err != nil || normalized != key || seen[key] {
			return keys, time.Time{}, "malformed"
		}
		seen[key] = true
	}
	return decoded, written, "present"
}

// RecordIssueKeys moves normalized valid keys to the front in caller order,
// keeps the unique previous tail, and caps history at fifty. Unlike x/slices
// Unique, this merge must validate and normalize each untrusted identity first.
// It is best-effort completion state; callers must preserve operation outcomes
// when storage fails. Concurrent processes can replace each other's history.
func RecordIssueKeys(store core.Cache, keys []string) error {
	if store == nil {
		return nil
	}
	merged := make([]string, 0, IssueKeysCap)
	seen := make(map[string]bool, IssueKeysCap)
	for _, key := range keys {
		key, err := command.ParseIssueKey(strings.ToUpper(strings.TrimSpace(key)))
		if err != nil || seen[key] {
			continue
		}
		seen[key] = true
		merged = append(merged, key)
		if len(merged) == IssueKeysCap {
			break
		}
	}
	if len(merged) == 0 {
		return nil
	}
	previous, _, _ := RecentIssueKeys(store)
	for _, key := range previous {
		if !seen[key] && len(merged) < IssueKeysCap {
			seen[key] = true
			merged = append(merged, key)
		}
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return store.Set(IssueKeysKey, raw)
}

func newIssueKeys(deps Deps) *cobra.Command {
	return command.MustNew(command.Spec[envelope.CacheIssueKeysOutput]{
		Op: "cache.issuekeys", Use: "issuekeys", Short: "Show recent issue keys",
		Long:    "Read the selected site/account's fifty most recent issue keys, newest first. Successful reads and live issue operations record typed identities as best-effort local state; previews do not update history. History has no TTL and is never fetched by cache refresh. Missing or malformed history produces an empty list with its cache state. Local reads and completion require explicit base_url and email in configuration or environment, and open no Jira client or credential store. Use cache clear issuekeys to reset history.",
		Example: "$ jira cache issuekeys --output=json\n\n$ jira cache clear issuekeys --dry-run --output=json",
		Human:   renderIssueKeys,
		RetainOnError: func(out envelope.CacheIssueKeysOutput) bool {
			return out.CacheState != ""
		},
		Run: func(ctx context.Context, _ command.Invocation) (envelope.CacheIssueKeysOutput, error) {
			out := envelope.CacheIssueKeysOutput{IssueKeys: []string{}}
			if err := ctx.Err(); err != nil {
				return out, err
			}
			if deps.Cache == nil {
				return out, cacheIO(core.ErrCacheManagementUnsupported)
			}
			store, err := deps.Cache()
			if err != nil {
				return out, cacheIO(err)
			}
			if err := ctx.Err(); err != nil {
				return out, err
			}
			keys, written, state := RecentIssueKeys(store)
			out.IssueKeys, out.Count, out.CacheState = keys, len(keys), state
			if !written.IsZero() {
				out.WrittenAt = written.UTC().Format(time.RFC3339Nano)
			}
			return out, ctx.Err()
		},
	})
}

func renderIssueKeys(v *output.View, env envelope.Envelope) error {
	out, ok := env.Data.(envelope.CacheIssueKeysOutput)
	if !ok {
		return fmt.Errorf("cache.issuekeys: unexpected payload %T", env.Data)
	}
	if len(out.IssueKeys) == 0 {
		v.Line("no recent issue keys", "("+cli.Text(out.CacheState)+")")
		return nil
	}
	v.Line(strconv.Itoa(out.Count), "recent issue keys, newest first")
	for _, key := range out.IssueKeys {
		v.Line(cli.Text(key))
	}
	return nil
}

// ClearResourceNames includes local history alongside fetched metadata.
// Refresh intentionally uses only ResourceNames: issue keys have no producer.
func ClearResourceNames() []string {
	return slices.Concat(ResourceNames(), []string{"issuekeys", "transitions"})
}
