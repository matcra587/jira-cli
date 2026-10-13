---
description: >
  Testing conventions: the suite map (package-adjacent + tests/*), the
  stdlib-testing house style, black-box contract tests against the built
  binary, goldens, guardrail tests, and the live suite.
paths:
  - "**/*_test.go"
---

# Tests

Successful resolver previews publish to the process-wide warning queue. Tests
that produce those warnings must run serially, inspect them and drain the queue
before leaving; mutex protection alone does not isolate concurrent invocations.
Keep unrelated read/envelope tests free of leaked cached-identity provenance.

Verified identity saving needs actual registered dispatch and binary evidence,
including a missing selected target, previews that create no directories,
conflicting authored scope before lookup, native absent/null/unknown fields,
unchanged-candidate snapshot conflicts and retained publication after output
failure. A compiling named guard must reject a wrongly typed preview flag; a
flag merely named dry-run does not establish preview safety. Discovery must
expose the local-write alternative without changing the default native read shape.
Exercise the actual native Windows regular-ancestor refusal before providers,
and require unchanged human previews to say they would keep the identity.

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
| Flag matrix | `tests/matrix/` | `//go:build flagmatrix`; actual binary effects for every owned flag, including hidden aliases; full runs join executed proofs against the live tree |
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

## Flag matrix

Flag matrix cases call `Prove` only after asserting a flag's observable effect.
Use `fwSuccess` before interpreting successful result data; `fwParse` only decodes
and also serves refusal cases, so it does not validate exit or envelope success.
Competing file/stdin contents establish source selection; anchored apply/merge
also assert authored content and protected native data. The full `TestMain` gate
must pass with no missing owned pairs. Filtered runs deliberately skip that gate
and cannot establish complete flag coverage.

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
discarded, and contract binaries compile once per test process through the shared
`tests/contract/version_test.go` owner. When a rule in `.claude/rules/` becomes
mechanically checkable, add a guardrail for it. Give AST analysers a small
invalid source fixture as well as scanning the repository, so the guard itself
cannot silently stop matching.

## Seams and hygiene

*   Packages that own goroutines assert no leaks via
    `goleak.VerifyTestMain(m)` in a package `TestMain` (precedent:
    `internal/cli/command`). Keep the guard when adding tickers or
    background work to such a package.
*   Configuration tests inject credential fakes through
    `config.Resolve(ctx, options, store)` and explicit environment maps.
    `config.Load(options)` is local and never consults a credential store.
    Ordinary tests must not read native credentials. Secret-like redaction fixtures
    must be visibly synthetic; annotate only exact scanner-matched fixture lines
    with a reason rather than disabling a rule or excluding a whole test file.
    Native keyring integration uses synthetic records and isolated storage;
    local discovery and cache readers prove that they never construct a store.
*   Distinguish a missing token from an unavailable credential store. The latter
    retains `auth_store_unavailable` and the authentication exit; it is not proof
    that no stored credential exists. Test both against explicit fixtures, and
    assert that pre-request refusals neither contact Jira nor create issue history.
    A closed D-Bus fixture isolates Linux only; skip that fixture on other systems
    rather than reading an existing native credential store.
*   Time-dependent assertions use an injected-now seam
    (`x/human.FormatTimeAgoCompactFrom`-style), never `time.Now()` in the
    assertion path.
*   Tests run under `-race` in CI; a test that swaps shared/global state
    must not run in parallel with siblings.

For concurrent cache publication, use an independent `os.Root.ReadFile` reader
and decode its wrapper independently. On Windows, pathname `os.ReadFile` omits
`FILE_SHARE_DELETE` and can block the rename under test. Do not convert sharing
violations into retries or accepted misses to make the integrity guard pass.
Run the guard on Windows; Linux execution and cross-compilation do not prove
Windows sharing semantics.

Live workflow discovery also proves offline eligible-ID callbacks from a private
per-run cache, comparing IDs against an independent native-response projection.
Disable credentials and route HTTP through unreachable proxies for callbacks.
Do not clear the user's normal completion cache as a test cleanup shortcut.

Native PowerShell guards use `JIRA_TEST_PWSH_BINARY` for an explicit runtime.
Validate the executable, generated dispatch, callback argv, literal insertion,
actual TabExpansion2 replacement and native argv. A standalone completion string
cannot prove a comma argument assembled correctly. Execute only a validated
single command containing literal fixture arguments; never evaluate arbitrary
candidate expressions. Run native Windows separately from Linux shell checks.

PowerShell guards cover non-forwarded values resembling context flags, `--`,
long/short inline CSV and present/absent caller exit status. Nonliteral-context
guards assert an independent side-effect marker remains absent. Capture drivers
reject redirections, pipelines and expressions before executing fixture arguments;
exercise their rejection predicate with parse-only adversarial inputs.

Live offline completion checks must retain the public account observed by the
authenticated producer. Disabling the credential store also removes identity
resolution from that store; provide the verified public email explicitly while
keeping tokens empty and every proxy unreachable. Never assume a filtered run
with inherited identity proves a clean full-suite invocation.

Clear both `NO_PROXY` and `no_proxy` in offline guards: Go uses the lowercase
value when uppercase is empty. Start with an inherited lowercase wildcard,
apply the offline recipe, then test actual proxy selection in a fresh process.
No HTTP request is needed.

Migration output-audit binaries use the `outputaudit` build tag to initialize the
upstream native keyring mock before command execution. Production and native
acceptance binaries exclude that seam. Exercise actual config migration and
rollback publication in every output mode; checked setup must succeed before
interpreting scenario data. Mask only valid dedicated random backup identifiers
when comparing independent runs. Never use the current host keyring for audit
fixtures, even when migration deliberately ignores ordinary keyring opt-out.

Real Linux keyring acceptance runs production binaries inside an owned private
D-Bus session with synthetic entries and independent `secret-tool` inspection.
Verify exact preimages, historical namespace retention, binding, provider backup
retention and rollback scope across JSON, compact and human output. Native mocks
and cross-compilation do not prove actual SDK or Windows/macOS keychain behavior.

Published migration guide recipes use the separately cached output-audit binary
and owned source/destination/recovery fixtures. The ordinary contract binary stays
production-built for native acceptance. Never execute credential migration examples
against the host keyring merely because ordinary credential resolution is disabled.

Default-only live creation must pin the verified dedicated project/type after
inherited environment values and independently check its effective preview
payload before any write. Hashing an owned configuration file alone does not
establish effective defaults: environment values can override that file. Bind
public account/board defaults too; changed or foreign input refuses creation.
Record attempts before writes and register acknowledged identities for cleanup.

Automatic theme guards exercise passive light/dark/missing/malformed hints,
exact environment provenance after framework construction and panic restoration,
custom palette fallback, actual config publication/migration/rollback and generated
Bash/Fish/Zsh/PowerShell candidates. Retain a delivered policy-4 CLI fixture
independently of the policy-5 mapper; never count a compiler failure as a guard.

Use a coordinated concurrent-constructor guard for framework theme passes,
including exact restoration. Mutex waits are not durable synctest blocking;
never wait for one inside a synctest bubble. A compiling overlapping-pass probe exposed the
process-environment interleaving; elapsed wall time does not prove serialization.

Client-preference guards prove positive seconds and typed arrays through file,
environment, stored get, effective list and publication. Cover malformed arrays,
null/nonstrings, commas/whitespace/duplicates, absent versus empty, source-profile
selection and old nonpositive refresh fallback. Invalid values must refuse before
providers. Retain an independently generated policy-5 CLI fixture for reopen and
exact rollback; test that current mapping never reinterprets older records.

Account-resolution guards exercise native absent/null markers even when their Go
values are populated, plus nil, empty and whitespace-only identities. Assert the
shared protocol error, unchanged fields and one lookup; canceled resolution must
enter no constructor/lookup. Drive create, edit, clone and both watcher commands
through their actual registered handlers with independent write counters. Do not
credit malformed invocation, compiler failure or an unrelated output error as an
identity guard. Valid identities resolve all pending fields once; literal IDs
stay untouched and do not trigger lookup.

Cached-identity guards compare independent site/account/cloud bindings, including
path case, equivalent URL/account spelling, absent/empty ID and scope changes
from environment or credential layers. Invalid declarations refuse before
providers even when later values could mask them. Prove safe inspection and
repair of malformed binding URLs through shared redaction/publication owners.
Drive all five account consumers and the production CLI offline; matching cached
IDs carry an unverified warning, mismatches retain @me. Live and validated previews
still use one actual current-user read and never consult cached preferences.
Retain an independently delivered policy-6 recovery fixture for reopen/rollback;
current mappings must not reinterpret older immutable candidates.

Cached-ID live Jira acceptance first inspects all stored binding declarations against an
independent native identity and verified public site/account/cloud. Clear inherited
cached metadata and every accepted environment spelling before offline previews,
including named token references and both proxy exemption variables. A compiling
adversarial inherited-ID guard must fail by name without that clearing. Clone
previews expose only known nonempty account overrides, so assert both prepared IDs
through typed output instead of unchanged Cobra variables copied into options.

Login acceptance must observe real prepared settings and actual provider effects.
Use owned public files and synthetic/injected credentials, including native
isolated providers; never replace user credentials for acceptance. Prove provider
admission before/after cancellation, acknowledged credential plus failed settings,
unknown entered writes, blocked previews, environment-only metadata publication,
actual selected token-reference provenance, and output failure after publication.
Refactoring a concrete pointer helper behind an interface must keep error returns
as a nil interface; existing target-refusal guards catch typed-nil regressions.

Exercise the actual interactive SDK form and exact account/vault/item selection.
Mixed flag/public defaults must not enter a provider for an already supplied
field. Test public selector publication for a fresh process and refusal of
authored reference replacement, including generic-token precedence.

When one public default needs storage, use a conflicting complete stored tuple
to prove the other public field remains unchanged. A canceled preview after
receipt preparation must fail even when no later provider stage would run.

Literal theme migration must preserve ~, $NAME and ${NAME} bytes and prove the
actual palette selected after migration. Use owned literal files plus environment
and home decoys; inspect rendered colors and exact rollback bytes. Exercise all
recorded policy versions, missing relative origins, cancellation and native
Windows. Decode-only preparation never reads palette files or process expansion.

Bound description guards must distinguish absent, null and empty documents;
retain exact future ADF bytes, large numbers and independent snapshot buffers.
Prove native ID rereads after issue-key moves, site/account/API-route checks on both sides of
reads, cancellation and invalid-anchor refusal before I/O. A blank editor save
must not turn native null into an empty document. Exercise actual registered
preparation in read-only mode and all output formats, then reopen its native
artifact. Fresh reads and merges do not establish server-enforced CAS.

Exercise real foreign redirects, missing/changed request authentication, malformed
HTTP 200 objects, wrong native types, duplicates and trailing bodies. They must
produce typed upstream failures without a candidate. Use whitespace-bearing
native ADF to prove exported byte preimages survive JSON/compact serialization
and reopening. Preserve cancellation/deadline causes from interrupted bodies.

Recoverable draft guards exercise no-clobber publication, manifest-last ordering,
partial acknowledgments, changed backups, bounded strict reopening and saved-work
retention. Drive actual draft/inspect/resume commands in each output mode; editor
acceptance uses real subprocesses and terminals, including cancellation, reaping
and restoration of both borrowed terminals. Mocked editor calls alone do not
establish process behavior. Local inspection must work without credentials or
network access; refreshed inspection binds the immutable issue ID and public scope.

The output audit now captures editor success through real Linux PTYs, including
`config edit`; its former success exemptions no longer apply. Unsupported native
terminal allocation fails explicitly as unrun evidence. Cross-compilation and
Linux PTY receipts do not establish Windows/macOS terminal acceptance. Preserve
all existing passing matrix cells when deliberately updating the baseline.

Native Windows Go OpenRoot currently prevents renaming its directory through a
handle without delete sharing. The directory-move guard must verify the exact
sharing-violation refusal, an unchanged usable candidate and no moved directory;
that receipt does not claim a successful redirection challenge. Platforms that
permit the move must exercise the changed-directory refusal. Preserve these
platform distinctions instead of treating a failed fixture operation as a passing
redirect guard or silently skipping it.
