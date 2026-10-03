//go:build outputaudit

// Package audit is the metadata-driven anti-gap suite: the machinery
// that makes a contract gap fail the build instead of relying on human
// diligence. It joins the REAL assembled command tree (hidden surfaces
// included) against the operation-metadata and envelope-schema
// registries into an exact full-leaf inventory; derives the expected
// op × scenario-category × mode × axis cell set independently of which
// recipes happen to exist; runs the real binary against scenario
// recipes under hermetic captures (no profile, no credentials, no
// network — an unroutable proxy kills accidental egress); and ratchets
// every evaluated cell against the committed matrix baseline with ZERO
// non-exempt red cells allowed. Exemptions are a bounded, dated,
// audited list with its own guardrail. Build-tagged `outputaudit`; run
// via `mise run audit` (chained into ci).
package audit
