// Package core defines the operation-level types the CLI runs on — retry
// policy, retry outcome, result shapes — plus the small consumer-side
// interfaces every I/O adapter implements. It sits above the wire types
// (internal/jira) and below the adapters (internal/adapters/...): core
// declares the seams it needs, adapters satisfy them, and nothing here
// touches the network, the filesystem, or the clock directly.
//
// Primary entry points: RetryPolicy, RetryInfo, IssueRead, and the
// JiraClient / Cache / Clock seams. Deliberate non-goal: speculative
// breadth — the package holds only what the issue-read tracer consumes and
// grows per command, not ahead of one. (An Operation identity type with a
// writes-to-Jira predicate arrives with the mutation pipeline, which is
// its first real consumer.)
package core
