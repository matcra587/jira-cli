// Package jira holds the Tier-1 wire types for the Jira Cloud REST API:
// structs that mirror payloads exactly, never CLI-shaped. Most of the
// package is generated (wire_gen.go, by cmd/gen-wire) from the vendored
// OpenAPI document in spec/; fields.go hand-curates the parts the spec
// leaves untyped and carries the raw-capture machinery that keeps
// unmodeled data alive through a round-trip.
package jira

//go:generate go run github.com/matcra587/jira-cli/cmd/gen-wire -spec spec/platform.json -out wire_gen.go
