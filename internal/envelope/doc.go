// Package envelope defines the machine output contract: the universal
// frame every command emits ({ok, meta, data, errors, warnings}), the
// domain ref types that keep identity an object rather than a bare
// scalar, the shared embeds for fragments that genuinely repeat across
// operations, and SchemaOf, which derives each operation's published
// JSON Schema from the same Go declaration that produces the wire bytes.
//
// The package sits near the bottom: beyond the standard library it
// imports only internal/jira, for the wire field types a passthrough
// payload carries verbatim (outputs_issue.go) — and internal/jira is
// itself a leaf, importing no internal package at all. So renderers,
// command packages and test harnesses can all share the contract types
// without an import cycle, which is the property this note exists to
// promise. Adding an edge to anything that imports THIS package would
// break it.
package envelope
