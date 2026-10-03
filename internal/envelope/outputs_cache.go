package envelope

import "encoding/json"

// CacheClearEntry reports a planned entry and its acknowledged deletion.
// ID is an opaque local filename, never a caller-supplied path. Unknown
// resources include malformed entries whose logical key cannot be trusted.
// Attempted distinguishes preview/queued entries from invoked deletions.
// Removed retains an acknowledgement even when an error accompanies it.
type CacheClearEntry struct {
	ID        string `json:"id"`
	Resource  string `json:"resource"`
	Attempted bool   `json:"attempted"`
	Removed   bool   `json:"removed"`
	Error     *Error `json:"error,omitempty"`
}

// CacheClearOutput retains the selected inventory even after partial deletion
// or interruption. WouldRemove is the plan; Removed counts acknowledgements.
type CacheClearOutput struct {
	MutationState
	Resource     string            `json:"resource"`
	WouldRemove  int               `json:"would_remove"`
	Removed      int               `json:"removed"`
	Unclassified int               `json:"unclassified"`
	Entries      []CacheClearEntry `json:"entries"`
}

func init() {
	Register("cache.clear", CacheClearOutput{}, map[string]any{"description": "Selected site/account cache removal, including local previews and retained partial outcomes."})
}

// CacheMetadata preserves native values and the collector's completion facts.
// Payload holds the existing enriched board snapshot, including project errors.
// Items hold exact native JSON values for every other metadata resource.
type CacheMetadata struct {
	Items           []json.RawMessage `json:"items"`
	Payload         json.RawMessage   `json:"payload,omitempty"`
	Count           int               `json:"count"`
	Pages           int               `json:"pages"`
	Complete        bool              `json:"complete"`
	TruncatedReason string            `json:"truncated_reason,omitempty"`
	NextStart       *int              `json:"next_start,omitempty"`
	NextPageToken   string            `json:"next_page_token,omitempty"`
}

// CachePrimeOutput distinguishes a local preview, reuse, collection and storage
// acknowledgement. Saved means this invocation acknowledged a cache write.
type CachePrimeOutput struct {
	MutationState
	CacheMetadata
	Resource   string `json:"resource"`
	TTLMinutes int    `json:"ttl_minutes"`
	Force      bool   `json:"force"`
	Unbounded  bool   `json:"unbounded"`
	WouldFetch bool   `json:"would_fetch"`
	Attempted  bool   `json:"attempted"`
	FromCache  bool   `json:"from_cache"`
	CacheState string `json:"cache_state"`
	Saved      bool   `json:"saved"`
	WrittenAt  string `json:"written_at,omitempty"`
	AgeSeconds int64  `json:"age_seconds"`
}

// CacheRefreshOutput preserves every selected resource in request order,
// including queued plans and late storage acknowledgements after interruption.
type CacheRefreshOutput struct {
	MutationState
	Parallelism int                                `json:"parallelism"`
	Results     []ResourceResult[CachePrimeOutput] `json:"results"`
}

func init() {
	Register("cache.refresh", CacheRefreshOutput{}, map[string]any{"description": "Ordered metadata resource refresh with bounded concurrency, offline previews and retained per-resource failures."})
	for _, op := range []string{"cache.labels", "cache.projects", "cache.epics", "cache.fields", "cache.issuetypes", "cache.linktypes", "cache.boards", "cache.statuses", "cache.priorities", "cache.resolutions"} {
		Register(op, CachePrimeOutput{}, map[string]any{"description": "Scoped metadata priming with local previews, native values, freshness and retained partial collection/storage outcomes."})
	}
}
