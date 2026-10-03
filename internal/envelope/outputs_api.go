package envelope

import "encoding/json"

// APIOutput reports an explicitly addressed request and its HTTP receipt.
// Base64 is the canonical byte representation; optional JSON and UTF-8 text
// views are conveniences and never replace the captured bytes.
type APIOutput struct {
	Request APIRequest `json:"request"`
	// Attempted means the client operation was invoked, not that HTTP bytes
	// reached Jira. A nil response means there is no HTTP receipt to inspect.
	Attempted bool         `json:"attempted"`
	Response  *APIResponse `json:"response,omitempty"`
}

// APIMutationOutput adds preview state for methods that can write. POST search
// endpoints remain transport reads, although the POST command can also write.
type APIMutationOutput struct {
	MutationState
	APIOutput
}

// APIRequest contains caller-authored request facts, excluding authentication.
// A missing body differs from an explicitly empty input file.
type APIRequest struct {
	Method            string      `json:"method"`
	Endpoint          string      `json:"endpoint"`
	Query             []APIValues `json:"query"`
	IfMatch           string      `json:"if_match,omitempty"`
	IfUnmodifiedSince string      `json:"if_unmodified_since,omitempty"`
	BodyBase64        *string     `json:"body_base64,omitempty"`
	ContentType       string      `json:"content_type"`
	MaxResponseBytes  int64       `json:"max_response_bytes"`
}

// APIValues keeps arbitrary query/header names as values rather than CLI keys.
// Entries are sorted by name; repeated values retain their original order.
type APIValues struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// APIResponse is an HTTP receipt, including alongside a command failure.
// Truncated means capture is incomplete, never that the operation was undone.
type APIResponse struct {
	StatusCode int             `json:"status_code"`
	Headers    []APIValues     `json:"headers"`
	BodyBase64 string          `json:"body_base64"`
	BodyText   *string         `json:"body_text,omitempty"`
	JSON       json.RawMessage `json:"json,omitempty"`
	Truncated  bool            `json:"truncated"`
	Attempts   int             `json:"attempts"`
	WaitedMS   int64           `json:"waited_ms"`
}

func apiSchemaOverrides() map[string]any {
	// RawMessage emits arbitrary JSON, not its underlying byte-slice layout.
	responseSchema := SchemaOf(APIResponse{}, map[string]any{
		"properties": map[string]any{"json": map[string]any{
			"type":        []string{"object", "array", "string", "number", "boolean", "null"},
			"description": "Present only for a complete valid JSON body. Exact bytes remain in body_base64.",
		}},
	})
	return map[string]any{
		"description": "An explicit platform or Agile API request and its inspectable HTTP receipt. A receipt does not establish whether a failed mutation took effect.",
		"properties":  map[string]any{"response": responseSchema},
	}
}

func init() {
	overrides := apiSchemaOverrides()
	for _, op := range []string{"api.get", "api.head", "api.options"} {
		Register(op, APIOutput{}, overrides)
	}
	for _, op := range []string{"api.post", "api.put", "api.patch", "api.delete"} {
		Register(op, APIMutationOutput{}, overrides)
	}
}
