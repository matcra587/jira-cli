// Package main generates ../nodes_gen.go from the vendored ADF schema
// (../schema/full.json). Offline and deterministic: same schema in, same
// bytes out. Invoked via `go generate ./internal/adf`; the generated file
// is committed, so the build never depends on running this.
package main
