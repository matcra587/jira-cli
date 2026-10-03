// Package contract holds black-box contract tests: the published
// behavior of one command, pinned by executing the real binary and by
// driving the real assembled program in-process. Where the audit suite
// (tests/audit) derives its coverage from metadata, these tests are
// hand-written expectations — the two must agree, and disagreeing is a
// finding, not a flake.
package contract
