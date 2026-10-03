// Package guardrails holds repo-invariant tests: mechanical conventions
// promoted to build checks, so a rule that matters cannot erode by
// review fatigue. Each guard scans the tree for the one way its
// invariant can be violated and fails naming the offender.
package guardrails
