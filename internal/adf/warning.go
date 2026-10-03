package adf

// Warning is a structured non-fatal diagnostic from a best-effort
// conversion. It exists because Markdown is strictly larger than the ADF
// node set: raw HTML has no representation at all, and several constructs
// (an image, a table inside a list item) only survive in a different
// shape. A converter that returned the document alone would make those
// two cases indistinguishable from a clean conversion, and the CLI would
// ship silently mangled content.
//
// Lossy is the whole point of the split, and callers gate on it: true
// means content the author wrote is GONE from the document, which a
// strict mutation mode aborts on; false means the content survives in a
// different shape (an image as a link, a hoisted table), which is loud in
// the envelope but must not block a write. Every field below feeds a
// caller decision, so a new warning site fills them in rather than
// leaving a bare message.
type Warning struct {
	// Type groups warnings by producer, e.g. "markdown_lossy_conversion".
	Type string `json:"type"`
	// Message is the human sentence, already source-mapped when the
	// producer knew a position.
	Message string `json:"message"`
	// Field names the payload key that carried the content, when a caller
	// tagged it after conversion.
	Field string `json:"field,omitempty"`
	// Path locates the offending input. For Markdown conversion it is a
	// source position ("line 3, col 1"), NOT a JSON path — an author can
	// act on the line they wrote, not on a document they never saw.
	Path string `json:"path,omitempty"`
	// NodeType names the construct, in the producer's vocabulary
	// ("raw HTML block", "table inside a list item").
	NodeType string `json:"node_type,omitempty"`
	// MarkType names the mark involved, when the warning is about one.
	MarkType string `json:"mark_type,omitempty"`
	// Lossy reports whether content was dropped rather than reshaped.
	Lossy bool `json:"lossy"`
}
