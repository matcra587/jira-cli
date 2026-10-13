// Package envelope defines the machine output contract: the universal
// frame every command emits ({ok, meta, data, errors, warnings}), the
// domain ref types that keep identity an object rather than a bare
// scalar, the shared embeds for fragments that genuinely repeat across
// operations, and SchemaOf, which derives each operation's published
// JSON Schema from the same Go declaration that produces the wire bytes.
//
// The package sits near the bottom: beyond the standard library it
// imports internal/jira for wire fields and internal/core for public recovery
// models. Neither owner imports this package. So renderers,
// command packages and test harnesses can all share the contract types
// without an import cycle, which is the property this note exists to
// promise. Adding an edge to anything that imports THIS package would
// break it.
package envelope
