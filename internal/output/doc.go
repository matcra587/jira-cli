// Package output is the single place output shape is decided: one pure
// Assemble builds the envelope from injected facts (time and request id
// come in as inputs — no I/O, no clocks), and one Render dispatches the
// assembled result to a registry of format renderers. Machine formats
// (json, compact) are contract-grade; human is a gated surface whose
// per-operation renderers register as commands land; later formats
// (ndjson, csv) are new registry entries, never cross-board changes.
package output
