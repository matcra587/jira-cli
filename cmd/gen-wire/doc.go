// Command gen-wire generates the Tier-1 wire types in
// internal/jira/wire_gen.go from the vendored Jira Platform OpenAPI
// document. It is a repository development tool, never shipped, and runs
// entirely offline against internal/jira/spec/platform.json — regeneration
// must not depend on the network, only on a deliberate spec bump.
package main
