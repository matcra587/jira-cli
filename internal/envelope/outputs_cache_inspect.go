package envelope

// CacheInspectEntry describes stored entry metadata. Malformed entries have no
// trusted key, size or write time. Payload validity is reported separately by
// resource; inventory and resource reads are not an atomic snapshot.
type CacheInspectEntry struct {
	ID         string `json:"id"`
	Key        string `json:"key,omitempty"`
	Resource   string `json:"resource"`
	Malformed  bool   `json:"malformed"`
	SizeBytes  *int64 `json:"size_bytes"`
	WrittenAt  string `json:"written_at,omitempty"`
	AgeSeconds int64  `json:"age_seconds"`
}

// CacheInspectResource reports validated producer freshness and collection
// facts without copying native payloads. TTLMinutes is null for issue history.
type CacheInspectResource struct {
	Resource        string `json:"resource"`
	CacheState      string `json:"cache_state"`
	TTLMinutes      *int   `json:"ttl_minutes"`
	Count           int    `json:"count"`
	Pages           int    `json:"pages"`
	Complete        bool   `json:"complete"`
	TruncatedReason string `json:"truncated_reason,omitempty"`
	NextStart       *int   `json:"next_start,omitempty"`
	NextPageToken   string `json:"next_page_token,omitempty"`
	WrittenAt       string `json:"written_at,omitempty"`
	AgeSeconds      int64  `json:"age_seconds"`
}

// CacheInspectOutput retains inspected entries and resources after failures.
// Complete acknowledges the whole local walk, not the completeness of Jira
// visibility or any cached metadata collection. Bytes exclude entry wrappers.
type CacheInspectOutput struct {
	BaseURL           string                 `json:"base_url"`
	Email             string                 `json:"email"`
	Resource          string                 `json:"resource"`
	Complete          bool                   `json:"complete"`
	Count             int                    `json:"count"`
	KnownPayloadBytes int64                  `json:"known_payload_bytes"`
	Unclassified      int                    `json:"unclassified"`
	Entries           []CacheInspectEntry    `json:"entries"`
	Resources         []CacheInspectResource `json:"resources"`
}

func init() {
	Register("cache.inspect", CacheInspectOutput{}, map[string]any{"description": "Credential-free selected site/account inventory, validated producer freshness and retained partial local observations; never fetches or changes storage."})
}
