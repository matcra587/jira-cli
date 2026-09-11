package boards

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	clib "github.com/gechr/clib/cli/cobra"
	"github.com/gechr/x/ptr"
	"github.com/spf13/cobra"

	"github.com/matcra587/jira-cli/internal/cache"
	"github.com/matcra587/jira-cli/internal/cli"
	cachereg "github.com/matcra587/jira-cli/internal/cli/cache/registry"
	"github.com/matcra587/jira-cli/internal/cli/cmdutil"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// NewCommand is the parent group for `jira boards <subcommand>`.
// Today only `list` exists; future feature slices may add `boards
// view`, `boards rename`, etc.
func NewCommand() *cobra.Command {
	cmd := cmdutil.GroupCommand("boards", "Browse the boards visible to this profile", "resources")
	cmd.Long = "Browse the agile boards visible to the active profile. `jira boards list` " +
		"shows each board with its id, type, and backing project — the ids that `jira issue " +
		"list --board` resolves against.\n\n" +
		"Boards are cached per profile; run `jira cache refresh boards` to refetch after a " +
		"board is created or renamed."
	cmd.Example = `$ jira boards list

# Machine-readable board ids for scripting
$ jira boards list --output=json`
	cmd.AddCommand(boardsListCommand())
	return cmd
}

// boardsListCommand wires `jira boards list`.
//
// Read-first: serves from the per-profile boards cache when fresh,
// transparently primes via /rest/agile/1.0/board when the cache is
// empty, and re-primes whenever `--refresh` is supplied. The
// envelope `data` block matches contracts/envelope-shapes.md > boards
// list. Truncation surfaces both as `data.truncated` and as a
// `warnings[].type: "cache-truncated"` entry.
//
// `--raw` returns Atlassian's native paged shape verbatim.
func boardsListCommand() *cobra.Command {
	var refresh bool
	var ttlMinutes int
	var unbounded, all bool
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the boards visible to this profile",
		Long: "List Jira boards visible to the active profile. Use it to discover board IDs " +
			"for issue listing, JQL building, and profile defaults.\n\n" +
			"The command serves from the per-profile boards cache when it is fresh. On a " +
			"first run, stale cache, or `--refresh`, it reads Jira's agile board API and " +
			"writes the cache before printing results.\n\n" +
			"Large sites are capped by default to avoid unbounded pagination. Use " +
			"`--unbounded` when you really need every board and are prepared for a longer " +
			"live read.",
		Args: cobra.NoArgs,
		Example: `# Serves from cache, primes on first run
$ jira boards list

# Re-prime from the server before listing
$ jira boards list --refresh

# Fetch every board on a large site
$ jira boards list --refresh --unbounded --output=json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, profile, ok, err := cmdutil.JiraClientForCommand(cmd)
			if err != nil {
				return err
			}
			ttl := time.Duration(ttlMinutes) * time.Minute
			file, fetchedAt, fromCache, cacheSourceState, err := readOrPrimeBoardsCache(cmd, client, cmdutil.CacheKeyForProfile(cmd, profile), ok, ttl, ttlMinutes, refresh, unbounded)
			if err != nil {
				return err
			}

			// Stable ordering by id ascending (nil id sorts as 0). The primer
			// already sorts; cached payloads written by older callers may
			// not, so we re-sort defensively.
			sort.SliceStable(file.Items, func(i, j int) bool {
				return ptr.Deref(file.Items[i].ID) < ptr.Deref(file.Items[j].ID)
			})
			// The cached set is served whole and the window is cut
			// client-side, mirroring attachment list: total always
			// known, isLast honest, no fabricated cursor — --all (or a
			// larger --limit) fetches the rest.
			items := file.Items
			pageSize := limit
			if pageSize <= 0 {
				pageSize = 50
			}
			if !all && len(items) > pageSize {
				items = items[:pageSize]
			}
			boards := boardsListEnvelope(items)
			pagination := &cli.Pagination{
				MaxResults: pageSize,
				Total:      new(len(file.Items)),
				IsLast:     all || len(file.Items) <= pageSize,
			}
			out := envelope.BoardsListOutput{
				Boards:           boards,
				FromCache:        fromCache,
				FetchedAt:        fetchedAt.UTC().Format(time.RFC3339),
				Truncated:        file.Truncated,
				TruncatedReason:  file.TruncatedReason,
				CacheState:       cmdutil.CacheStateForCount(cacheSourceState, len(file.Items)),
				CacheSourceState: cacheSourceState,
				CacheEmpty:       len(file.Items) == 0,
			}
			warnings := boardsTruncationWarnings(file)
			return cmdutil.WriteEnvelopeWithPaginationAndRawWarnings(cmd, "boards.list", out, pagination, warnings)
		},
	}
	cmdutil.AddBoolVar(cmd.Flags(), &refresh, "refresh", false, "Force a re-prime even when the cache is fresh", clib.FlagExtra{Group: "Cache", Terse: "force re-prime"})
	cmdutil.AddIntVar(cmd.Flags(), &ttlMinutes, "ttl-minutes", cachereg.TTLMinutesFor("boards"), "Freshness window before automatic refresh", clib.FlagExtra{Group: "Cache", Placeholder: "N", Terse: "freshness window"})
	cmdutil.AddBoolVar(cmd.Flags(), &unbounded, "unbounded", false, "Walk every page (disables the default 100-page / 10 000-board cap)", clib.FlagExtra{Group: "Pagination", Terse: "fetch all pages"})
	cmdutil.AddIntVar(cmd.Flags(), &limit, "limit", 50, "Maximum boards returned without `--all`; `0` uses the default", clib.FlagExtra{Group: "Pagination", Placeholder: "N", Terse: "page size"})
	cmdutil.AddBoolVar(cmd.Flags(), &all, "all", false, "Return every cached board regardless of `--limit`", clib.FlagExtra{Group: "Pagination", Terse: "return all"})
	// No --dry-run: `boards list` always performs a live read and a
	// cache write, so a "dry-run" flag here could not be honest.
	return cmd
}

// readOrPrimeBoardsCache is the read-first cache resolver that powers
// `boards list`. Returns the parsed BoardsCacheFile, its on-disk
// FetchedAt timestamp, and a `fromCache` marker indicating whether
// this invocation served from disk (true) or primed from API (false).
//
// Empty / stale cache + no --refresh → still primes, because the
// "transparent first-run" UX would otherwise leave new users with an
// empty list. --refresh always primes regardless of staleness.
func readOrPrimeBoardsCache(cmd *cobra.Command, client *jira.Client, cacheProfile string, hasClient bool, ttl time.Duration, ttlMinutes int, refresh, unbounded bool) (jira.BoardsCacheFile, time.Time, bool, string, error) {
	cacheSourceState := cmdutil.CacheStateRefresh
	if !refresh {
		entry, present, stale, readErr := cache.Read(cacheProfile, "boards", ttl)
		switch {
		case readErr != nil:
			cacheSourceState = cmdutil.CacheStateMalformed
		case present && !stale:
			items, err := jira.DecodeBoardsCache(entry.Data)
			if err != nil {
				return jira.BoardsCacheFile{}, time.Time{}, false, cmdutil.CacheStateFresh, fmt.Errorf("boards.list: decode cache: %w", err)
			}
			file := boardsCacheFileFromEntry(entry.Data, items)
			return file, entry.FetchedAt, true, cmdutil.CacheStateFresh, nil
		case present && stale:
			cacheSourceState = cmdutil.CacheStateStale
		default:
			cacheSourceState = cmdutil.CacheStateMissing
		}
	}
	if !hasClient {
		return jira.BoardsCacheFile{}, time.Time{}, false, cacheSourceState, fmt.Errorf("jira base URL is required for boards.list")
	}
	var file jira.BoardsCacheFile
	err := cmdutil.Spin(cmd, "boards.list", func(ctx context.Context) error {
		var spinErr error
		file, _, spinErr = cmdutil.PrimeBoards(ctx, client, ttlMinutes, unbounded)
		return spinErr
	})
	if err != nil {
		return jira.BoardsCacheFile{}, time.Time{}, false, cacheSourceState, err
	}
	body, err := json.Marshal(file)
	if err != nil {
		return jira.BoardsCacheFile{}, time.Time{}, false, cacheSourceState, fmt.Errorf("boards.list: marshal cache: %w", err)
	}
	entry, err := cache.Write(cacheProfile, "boards", body)
	if err != nil {
		return jira.BoardsCacheFile{}, time.Time{}, false, cacheSourceState, fmt.Errorf("boards.list: write cache: %w", err)
	}
	return file, entry.FetchedAt, false, cacheSourceState, nil
}

// boardsCacheFileFromEntry reconstructs the BoardsCacheFile envelope
// from a cache.Entry.Data payload. Tolerates both the wrapped
// {items,...} form (current primer) and the bare-array form (legacy
// resolver-test fixtures) via DecodeBoardsCache.
func boardsCacheFileFromEntry(raw json.RawMessage, items []jira.Board) jira.BoardsCacheFile {
	var file jira.BoardsCacheFile
	// Best-effort: parse the wrapped envelope to recover truncation
	// markers. If the on-disk shape is a bare array, the unmarshal
	// noop's and Truncated/TruncatedReason stay false/"".
	if err := json.Unmarshal(raw, &file); err != nil {
		file = jira.BoardsCacheFile{}
	}
	if items == nil {
		items = []jira.Board{}
	}
	file.Items = items
	return file
}

// boardsTruncationWarnings maps a cached truncation marker onto the
// envelope's warnings[] entry per contracts/envelope-shapes.md.
// Returns nil when nothing was truncated.
func boardsTruncationWarnings(file jira.BoardsCacheFile) []map[string]any {
	if !file.Truncated {
		return nil
	}
	switch file.TruncatedReason {
	case "max_pages":
		return []map[string]any{{
			"type":        "cache-truncated",
			"resource":    "boards",
			"reason":      "max_pages",
			"limit":       100,
			"message":     "boards cache truncated by max_pages; re-run with --unbounded to fetch every board",
			"remediation": "Re-run with --unbounded if you need every board.",
		}}
	case "max_results":
		return []map[string]any{{
			"type":        "cache-truncated",
			"resource":    "boards",
			"reason":      "max_results",
			"limit":       10_000,
			"message":     "boards cache truncated by max_results; re-run with --unbounded to fetch every board",
			"remediation": "Re-run with --unbounded if you need every board.",
		}}
	case "rate_limit":
		return []map[string]any{{
			"type":                "rate-limit-during-paginate",
			"resource":            "boards",
			"pages_fetched":       file.PagesFetched,
			"retry_after_seconds": file.RetryAfterSeconds,
			"message":             "boards cache truncated by rate_limit; re-run after the rate-limit window resets",
			"remediation":         "Re-run `jira cache boards --refresh` after the rate-limit window resets.",
		}}
	}
	return nil
}

// boardsListEnvelope flattens jira.Board into the wire shape
// documented in contracts/envelope-shapes.md > boards list. The
// pointer-typed fields collapse to their zero values when absent so
// agents don't need null-handling for the common board case.
func boardsListEnvelope(items []jira.Board) []envelope.BoardRow {
	out := make([]envelope.BoardRow, 0, len(items))
	for _, b := range items {
		row := envelope.BoardRow{ProjectKeys: []string{}}
		if b.ID != nil {
			row.ID = *b.ID
		}
		if b.Name != nil {
			row.Name = *b.Name
		}
		if b.Type != nil {
			row.Type = *b.Type
		}
		if b.ProjectKeys != nil {
			row.ProjectKeys = b.ProjectKeys
		}
		out = append(out, row)
	}
	return out
}
