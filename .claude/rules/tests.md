---
description: >
  Testing conventions: the suite map (package-adjacent + tests/*), the
  stdlib-testing house style, black-box contract tests against the built
  binary, goldens, guardrail tests, and the live suite.
paths:
  - "**/*_test.go"
---

# Tests

*   **Framework**: standard library `testing`, table-driven, lowercase named
    subtests. testify is used only in `tests/live`; no other assertion
    library anywhere.
*   **Test behavior, not implementation** — envelopes, exit codes, rendered
    output, not internal call sequences.
*   `mise run test` (unit, fast, with coverage) · `mise run test:integration`
    · `mise run ci` chains everything and runs the race detector.

## Suite map

| Suite | Location | Covers |
|-------|----------|--------|
| Unit | package-adjacent `*_test.go` under `internal/**` | package logic (external `_test` packages for public-API tests) |
| Black-box unit | `tests/unit/` | cross-package behavior without a server |
| Contract | `tests/contract/` | the built binary's agent contract: envelopes, exit codes, flag behavior |
| Guardrails | `tests/guardrails/` | repo invariants (see below) |
| Output audit | `tests/audit/` | `//go:build outputaudit`; per-command output axes (M1–M12 machine, H1–H5 human) against captured fixtures. `go test -tags outputaudit ./tests/audit/...` — the decisive check that an output refactor is byte-identical |
| Integration | `tests/integration/` | binary end-to-end against `httptest` (no build tag) |
| Live | `tests/live/` | `//go:build live`; real tenant via `mise run test:live` + `JIRA_LIVETEST_PROJECT` (a dedicated probe project); `JIRA_LIVETEST_BINARY` selects a frozen executable for comparisons; excluded from `go test ./...` |

## Contract tests are black-box

Contract tests execute the real binary (`runJira(t, args...)`-style helpers
returning stdout/stderr/exit code) against `httptest.NewServer` Jira stubs,
then unmarshal stdout as the envelope:

*   Route command execution through the compile-once `buildJiraBinary` helper
    (`tests/contract/binary_test.go`), never `go run`/`go build` the CLI per
    test — a per-test compile dominates suite wall time. Enforced by
    `tests/guardrails/contract_compiles_once_test.go`.
*   Assert envelope fields **structurally** — unmarshal into a target and
    check fields; never string-match whole JSON blobs.
*   Error paths assert the error envelope arrives on **stdout** with the
    right code and exit code — shared helpers live in
    `tests/contract/envelope_invariants_test.go`.
*   The command inventory test (`command_inventory_test.go`) reads the live
    `agent schema` output — a new command surfaces there automatically; keep
    it green, don't edit around it.
*   Never contact a real Jira from contract/integration tests — `httptest`
    stubs only. Real-tenant coverage belongs in `tests/live/`.

## Goldens

*   ADF fixtures live alongside `internal/adf` tests, including the explicit
    Markdown, rendering and preservation cases. Published schema goldens use
    `internal/cli/root/agent_schema_test.go` and its `testdata/` directory.
    Update fixtures deliberately and review the diff; changed expectations
    must follow a verified behavior change.
*   TUI runtime remains a separate milestone; this tree has no
    `internal/tui/goldens` suite to execute.

## Guardrail tests

Invariants get guardrail tests (`tests/guardrails/`): every declared
completion predictor is handled, error code/hint tables stay in lockstep,
the embedded guides satisfy the Agent Guide Standard
(`docenttest.Validate` in internal/cli/root), command packages never use
process streams or non-returning `Run` handlers, output-helper results are not
discarded, and the contract suite never `go run`/`go build`s the CLI per test
(`contract_compiles_once_test.go`). When a rule in `.claude/rules/` becomes
mechanically checkable, add a guardrail for it. Give AST analysers a small
invalid source fixture as well as scanning the repository, so the guard itself
cannot silently stop matching.

## Seams and hygiene

*   Packages that own goroutines assert no leaks via
    `goleak.VerifyTestMain(m)` in a package `TestMain` (precedent:
    `internal/cli/command`). Keep the guard when adding tickers or
    background work to such a package.
*   `internal/config`'s `TestMain` clears `JIRA_KEYRING_SERVICE` so keyring
    tests can never touch a real credential store — mirror the pattern for
    any new ambient-env dependency.
*   Time-dependent assertions use an injected-now seam
    (`x/human.FormatTimeAgoCompactFrom`-style), never `time.Now()` in the
    assertion path.
*   Tests run under `-race` in CI; a test that swaps shared/global state
    must not run in parallel with siblings.
