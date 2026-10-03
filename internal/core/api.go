package core

import "net/url"

// DefaultMaxAPIResponseBytes is the raw CLI's default response capture bound.
// Exceeding a bound must retain a marked partial receipt, never truncate silently.
const DefaultMaxAPIResponseBytes = 16 << 20

// APIRequest is one explicitly addressed platform or Agile request. Endpoint is
// a raw /rest/api/... or /rest/agile/... path relative to the configured site or
// cloud prefix, never an absolute URL. Query values are encoded separately.
type APIRequest struct {
	Method   string
	Endpoint string
	Query    url.Values
	// IfMatch and IfUnmodifiedSince are optional HTTP preconditions. Empty
	// means absent. They do not establish that the endpoint enforces them.
	IfMatch           string
	IfUnmodifiedSince string
	// Body is sent byte-for-byte. Nil means no body; a non-nil empty slice means
	// an explicitly empty body. Callers must not modify it until DoAPI returns.
	Body        []byte
	ContentType string
	// MaxResponseBytes caps captured bytes. Zero explicitly permits an unbounded
	// capture. The CLI supplies DefaultMaxAPIResponseBytes unless overridden.
	MaxResponseBytes int64
}

// APIResponse retains inspectable transport facts even when DoAPI returns an
// HTTP, body-read or capture-limit error. Nil response means no HTTP receipt.
type APIResponse struct {
	StatusCode int
	// Headers contains relevant response metadata, excluding authentication and
	// cookie headers. Request credentials never appear in this result.
	Headers map[string][]string
	Body    []byte
	// Truncated marks a capture bound or failed body read, not a successful
	// complete response. Body retains exactly the bytes captured before stopping.
	Truncated bool
	Retry     RetryInfo
}
