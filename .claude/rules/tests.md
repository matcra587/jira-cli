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

*   Route command execution through the compile-once `jiraBinary` helper
    (`tests/contract/version_test.go`), never `go run`/`go build` the CLI per
    test — a per-test compile dominates suite wall time.
*   Assert envelope fields **structurally** — unmarshal into a target and
    check fields; never string-match whole JSON blobs.
*   Error paths assert the error envelope arrives on **stdout** with the
    right code and exit code — use the shared binary and envelope helpers in
    `tests/contract/`.
*   The exact command inventory (`tests/audit/inventory_test.go`) reads the live
    command metadata — a new command surfaces there automatically; register its
    real recipes and required cells, then keep the inventory green.
*   Never contact a real Jira from contract/integration tests — `httptest`
    stubs only. Real-tenant coverage belongs in `tests/live/`.

Explicit issue preparation contracts author independent known-family fixtures,
then pass the actual typed preparation payload to the native issue writer. Check
exact received field tokens, large integers, numeric-string spelling and unknown
plugin bytes. Preview and malformed-value refusal must send no requests; each
successful write has one native acknowledgement. Do not derive expected values
from the production codec or example registry. Native Windows fixtures preserve
the selected executable basename, including `.exe`.

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
Windows child environments must retain `PATHEXT`: PowerShell uses it to execute
native callbacks and literal `.exe` observer paths. Keep profile/config/cache
directories owned by the fixture. Check actual child exits and receipts before
interpreting empty completion output as a product defect.
Compile-once contract, flag-matrix and startup-benchmark outputs keep `.exe` on
Windows before invoking `go build -o`. Their child environments bind
`USERPROFILE`, `APPDATA` and `LOCALAPPDATA` to owned paths and retain `PATHEXT`.
Native Go acceptance exercises the ordinary build helper; record external-binary
selection separately. Use a source filesystem supporting Go's module file locks.
Editor previews select an owned executable with literal arguments, never an
assumed Unix utility. Home overrides also bind Windows profile directories;
assert the selected user destination independently of the project destination.
Exercise UTF-8 callbacks under OEM and Latin-1 host encodings, including custom
fallbacks, native failures, empty replies, refused context and exceptions. Keep
these Windows fixtures in owned hidden consoles so they cannot change the user's
console code page. Read native observer UTF-8 receipts directly; a PowerShell
stdout round trip can corrupt correct observed argv under a non-UTF-8 host.
Verify Unicode metadata and two-step directory continuation through actual
generated dispatch and literal executed argv.
Use independent generated applications in concurrent runspaces with callback
barriers. Verify ordered ownership, exception release and cancellation of a
waiting completer while another callback remains active; then admit fresh work.
Directory continuations use the platform's path separator when the shell
supplies it; keep authored slash prefixes and non-path values exact. Do not
normalize all observed argv to hide unrelated identity changes.
Validate the executable, generated dispatch, callback argv, literal insertion,
actual TabExpansion2 replacement and native argv. A standalone completion string
cannot prove a comma argument assembled correctly. Execute only a validated
single command containing literal fixture arguments; never evaluate arbitrary
candidate expressions. Run native Windows separately from Linux shell checks.

Elvish callback guards load generated scripts in the real editor namespace of
an owned native console. Apply its registered matcher before comparing candidate
stems; positional completers also emit flag names for the editor to filter.
Exercise positional, current-word and ordinary dynamic flags, reload and literal
CSV identities. Callback/matcher execution does not prove Tab insertion; test
that separately through the real editor. Keep descriptions out of inserted values.
Actual Windows Elvish insertion uses an owned native console and key records:
submit the complete line, Tab, then a bound marker key before Enter. Assert exact
observer argv and a successful joined shell exit. External callbacks use the
real CLI; the accepted command records arguments without performing a mutation.
Bound runtime and admission, preserve selected configuration and literal CSV,
and keep these guards distinct from direct callback/matcher tests.
Include ordinary cached field CSV insertion with descriptions, an already selected
identity and separate/inline flags, alongside current-word filesystem CSV roles.
Dynamic comma-list guards cover described and bare rows, selected-value exclusion
and both separate and inline flags. Assemble the final identity before producing
rich candidates; descriptions must not participate in CSV concatenation.

Every new watcher selector flag needs a proving matrix case for each write verb.
User-query cases assert the complete lookup precedes keyed writes, exact resolved
account identity and authored query receipts; an unregistered contract test does
not satisfy the matrix's owned command/flag coverage gate.
Keep new taxonomy codes in the explicit keyed severity ladder, including rank
zero for account preflight failures before fan-out. Migration output audit must
require `deferred` and exercise nonempty declaration names in both machine modes
without publishing their retained values.

Native Windows browser guards launch only an owned executable through `BROWSER`.
Record exact URL argv, check helper exit through an independent process handle,
and join cleanup after failed assertions. Machine output must suppress handoff;
an HTTP trap establishes that URL-only paths perform no Jira reads. Handoff is
not GUI rendering or proof that a page loaded.
Browser-gating unit fixtures select an owned BROWSER value; default-launcher
guards assert the platform executable and every literal argument independently.
A download directory-swap fixture blocked by native Windows delete sharing must
prove original identity, denied rename, preserved stream failure and partial-file
cleanup. Keep actual redirected-directory proof separate; never count a failed
fixture rename as a successful replacement.
Capture the pre-fault Windows file ID through an opened handle, then close it
before exercising the writer; pathname Stat loads identity lazily.

Native Windows release-note paging guards use fresh hidden consoles with explicit
CONIN$/CONOUT$ handles, an independent pager stdin/argv receipt and owned key
events. Verify built-in navigation, Control-C/Control-Break, exact mode restoration
and return to the original screen. Read bounded CHAR_INFO rectangles and verify
the complete copied region; character counts differ for wide diagnostic glyphs.
Console cells establish display fidelity, including their explicit padding
projection; they do not establish original write bytes. Keep exact Markdown
stdin and piped-byte contracts separate. The native flag matrix must prove
release-notes --no-pager rather than dropping the required pair.
Assign the console helper to an owned kill-on-close job before admitting CLI
construction. Parent cancellation must terminate and join the actual descendant
and leave zero active job processes; killing only the helper is insufficient.
Configured-pager receipts must record deliberate changes to both borrowed modes
before the CLI restores them. Normal built-in quit restores exact original cells,
attributes and cursor; cancellation may add its diagnostic but no pager residue.

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

Bash generated-script fixtures carry literal prefixes through a quoted environment
value. Native MSYS startup can expand a caller's argv before the Bash driver runs;
verify input admission before attributing a completion failure. Recognize the
Windows System32 WSL launcher by file identity: default discovery skips it and
explicit selection fails. Actual Windows Bash acceptance selects a native shell.
Keep unsupported star/backslash filenames as named Windows skips; preserve space
filenames, caller IFS, all static literal values and the complete Unix inventory.

Update candidate cleanup guards prove a nonempty positioned read succeeds before
publication and returns `os.ErrClosed` after the owner returns. `File.Stat` on
Windows reports the native invalid handle after close; do not mistake that error
for an open descriptor or accept every error as evidence of closure. An omitted
close mutation must compile, enter and fail the named cleanup guard.

Config-editor selection fixtures use the executing owned binary and shared
shell quoting. Distinguish winners through literal argv markers, preserving
complete Windows executable paths without Unix utility dependencies. Keep saved
and rejected bytes, outcome/context and regular-file assertions on every host.
Only POSIX mode assertions live in named child subtests skipped on Windows;
a permission skip must not skip its parent's recovery checks. Windows file modes
do not establish ACL protection.

Guide-export tests retain exact rendered bytes, qualified paths, collision refusal,
hard-link target preservation and cancellation receipts on every platform. Put
POSIX mode checks in named subtests: Windows regular-file modes do not establish
POSIX permissions or ACL protection. Attempt actual symbolic links and skip only
the native Windows unavailable-privilege error, keeping every other error fatal.
Keep invalid-path validation separate so a link skip cannot hide its assertions.
Neither skipped link cases nor hard-link passes prove symbolic-link acceptance.

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

The output audit captures editor success through real Linux PTYs and owned
Windows amd64 consoles, including `config edit`; its former success exemptions
do not apply. Unsupported native terminal allocation fails explicitly as unrun
evidence. Cross-compilation and Linux PTY receipts do not establish Windows/macOS
terminal acceptance. Preserve all required matrix cells.

Native Windows Go OpenRoot currently prevents renaming its directory through a
handle without delete sharing. The directory-move guard must verify the exact
sharing-violation refusal, an unchanged usable candidate and no moved directory;
that receipt does not claim a successful redirection challenge. Platforms that
permit the move must exercise the changed-directory refusal. Preserve these
platform distinctions instead of treating a failed fixture operation as a passing
redirect guard or silently skipping it.

Contextual file completion requires real generated-script and insertion evidence
for each supported shell, including nested directories, literal metacharacters,
spaces, quoted commas, doubled quotes, flags and terminators. Pure directory and
CSV codec guards establish their owner contracts only; they do not establish
shell routing or insertion. Lookup failure and cancellation must emit no partial
candidate list, and the current word must remain literal rather than evaluated.
Drive native editor tests through a bounded terminal and an owned argv observer;
completion callbacks alone execute the real CLI. Never execute uploads or
settings writes as the observer. A bound editor key marks the end of Tab handling
instead of a scheduling sleep. Give coverage-instrumented observers their own
`GOCOVERDIR`, and fail explicitly when a requested shell binary is unavailable.
Verify inline values, completed quoted context, directory continuation, hidden
boolean aliases and repeated Elvish sourcing. Bash 3.2 spacing uses its registered
wrapper because changing completion options during a callback cannot affect that
callback's already-selected specification.

CSV directory choices retain an unfinished current field so a second Tab can
complete a leaf in the same argument. Theme choices preserve literal symlink/..
traversal when made absolute and validate the final transport bytes, including
the current directory. Verify these identities in native editors and named guards.

PowerShell directory insertion consumes an explicit `--@directory` hint in the
current-word callback description slot. Ordinary cached values ending in a slash
remain complete literal arguments. Keep the hint private to the generator and
verify both directory continuation and slash-ending configuration values. Fish
current-word callbacks reuse the shared version-aware token helper.

Bash versions before 4.3 use the shared legacy spacing path because `noquote`
was added in 4.3. Fish 3 preserves inline option spelling explicitly when its
editor replaces a whole quoted CSV token. Native terminal guards bind Fish
function keys through the selected version API (`bind -k` before 4, named keys
from 4), and explicit shell selections fail if missing.

The Bash legacy spacing wrapper clears filename/default completion options
after the selected callback and before manual quoting. Native file hints must
not quote already quoted replies a second time on Bash 4.0–4.2. Exercise ordinary
file and directory hints alongside the current-word callbacks.

Legacy Bash spacing callbacks enumerate directory hints before clearing native
filename options. Carry directory suffixes through file/extension hints only at
that wrapper boundary; ordinary model generators retain their native behavior.

Bash literal callback decoding normalizes the ANSI-C quoting emitted by
`printf %q` before the selected POSIX lexer. Preserve UTF-8 octets, refuse
unsupported escapes or malformed bytes, and never evaluate expansions. Decode
completed context with the same owner. Guard a Unicode directory with two Tabs
and native executed argv; a single completed basename cannot prove continuation.

Native field refreshes belong to `jira.Nullable.UnmarshalJSON`, alongside the
shared generated-model decoder. Decode into a fresh value and publish the full
presence/null/value tuple only after success. Null clears its prior value; a
subsequent value clears null. Failed decoding retains the exact earlier tuple
and leaves aliased maps, slices, pointers and raw body bytes untouched. Do not
work around stale native fields independently in commands or adapters.

Tests that load a named IANA location must provide their timezone database as a
test dependency, for example by importing `time/tzdata` in the test file. Native
Windows may provide time.Local without an IANA database. Preserve real DST
transitions in the guard; do not replace them with a fixed offset or skip missing
fixture data. Do not add timezone data to the release CLI merely to repair a test.

Externally selected native update tests keep a renamed installed basename while
preserving `.exe` on Windows, including disposable flag-matrix and audit
installation copies. Managed-update audit recipes must actually execute their
owned copies in every output mode and retain the installation-owner result.
Version/formatter fixture expectations and selectors use the executing runtime
OS and architecture; retain exact output quoting, delimiters and field order.
Confirm the original and candidate version commands
actually execute before interpreting publication/download results. Test only
disposable installation copies; preserve selected artifacts and retained backups.

Windows Credential Manager caps each blob at 2,560 bytes. The keyring recovery
owner keeps ordinary/version-2 backups intact and publishes larger private v2
payloads as bounded encoded pieces with a version-3 descriptor last. Derive piece
names only from the validated parent backup ID; verify count, size and full digest
before reopening snapshots or mutating primary entries. Retain partial effects
and native admission on cancellation/failure; this is not a provider transaction
or compare-and-swap. Native tests use fresh random service names, synthetic
records and independent WinCred reads, never enumeration or the user's default
service. Run them in a separate test process so global mocks cannot earn credit.

Native Windows editor acceptance uses the exported shared supervisor inside fresh
hidden consoles. Open the owned CONIN$/CONOUT$ handles explicitly; redirected
standard streams are not proof of terminal allocation. Observe both changed
native modes before releasing the real editor, then verify exact restoration,
process termination, literal argv and separation from result stdout. Cover failed
exit, cancellation, launch failure and second-terminal capture refusal. Run
`mise run test:editor:windows` on Windows; its nativeconsole-tagged gate fails
explicitly on other hosts. It also executes real `config edit` and `issue edit
resume` commands with success, failed editor exits, invalid saved input and
actual Control-C and Control-Break cancellation. Register the isolated driver for interrupts
and make the editor ignore them before readiness; only the CLI supervisor may
end the editor. Send the event from that fresh hidden console, never the caller's.
Require exit 6 and `canceled`, restored modes, exact retained work, no candidate
and termination before return, with a generous five-second return watchdog.
Require exact configuration publication or rejected-work retention, unchanged
native draft backup, preserved authored Markdown, zero extra Jira requests and
the selected typed operation. Native macOS behavior remains separate acceptance.
An externally selected standard production
artifact must actually execute; disclose test-only binary selection and never
count a refused executable or cross-compilation as a native pass.

Complete owned console fixture preparation before capturing the modes borrowed
by the editor. Fixture subprocesses can change that shared console. Require both
actual editor mode changes and exact restoration to this invocation boundary;
never weaken the postcondition to accommodate stale fixture measurements.

Core comparisons are untagged results; publish verification rows through
envelope.VerifiedField at the shared CLI boundary. Both ordinary and terse
--verify help describe later observation after acknowledgement.

Requested-field verification belongs to core.CompareIssueWrite and the shared
create/edit readback owner. Request authored fields/update IDs plus existing
summary/status/updated once. The production adapter retains exact native field bytes separately from typed
presentation and admits bounded complete authenticated JSON before comparison; malformed or unavailable readbacks retain
acknowledged writes with verify_failed. Typed verified.fields rows preserve
native observed absent/null/value and report matches/differs/unverified in sorted
field order. Exact numeric spelling stays significant. Known identity/collection
checks tolerate response metadata, including identified cascading-select children.
Collection replacements require both requested
and observed membership; add/remove updates may retain untouched members. Unsupported or
ambiguous updates, absent fields and different ADF remain unverified, including
custom-field documents admitted by the shared ADF parser. Derive
warnings from typed Spec results in target order, never the concurrent global
queue. A later difference neither proves a Jira drop nor supplies remote CAS.

Only verified.fields.observed (and its keyed edit path) is native field
passthrough. Keep verification row names under the ordinary output audit.

Collection verification guards must reject leftover members after replacement or
set plus add/remove, while preserving expanded identity and untouched-member
acceptance for updates without set.

Explicit create/edit --validate-remote checks requested field IDs and native
operation names against complete context-bound metadata through shared discovery
and preflight owners. Preserve request values and bytes; never convert, strip or
infer fields. Bare previews stay local; unflagged live requests retain Jira value
validation. Native create ID/name meaning must not be guessed: a display name
that equals another type's ID still selects by name. Project/issuetype create
identities retain their structural screen exemption. Publish only checked field
IDs in envelope.IssueFieldPreflight, per key on edit; values, requiredness,
permissions, top-level members and future write success are outside this check.
Refused or malformed contexts send no write; ordered partial successes remain
real and are never replayed. Cancellation cannot admit a completed metadata read.
Metadata readers must validate the final successful HTTP reply against the client's
authenticated API scope and exact credentials through the shared reply owner.
Credential-free redirects cannot establish project, type, field or edit context.
Keep raw API media redirects usable and preserve ordinary HTTP error classification.

Preflight tests must contrast literal inputs with and without the explicit flag,
exercise all metadata pages and distinct issue contexts, refuse missing fields
and unsupported verbs before writes, and prove native payload identity. Include
ID/name collisions, malformed alternate-client documents, cancellation and
ordered partial previews/live writes. Fixtures must advertise actual required
metadata facts rather than returning an empty successful catchall.

Exercise foreign-origin and same-origin out-of-scope redirects for project, type,
field and edit metadata. Prove refusal before a write, credential stripping, and
continued acceptance of authenticated redirects within the configured scope.

Native Windows command fixtures must bind USERPROFILE, APPDATA and LOCALAPPDATA
to owned temporary paths. Do not inherit the user's stores. Record an explicit
skip for filenames Windows cannot represent; retain the other path cases.

Bare email lookup is opt-in for the assignee flag through command.AccountField.
Use core.ResolveVisibleUser and ActiveUserSearchPage for the same authenticated
visible-search policy as user discovery. Preserve privacy-hidden email; the sole
visible query match is not a verified exact email address or assignability proof.
An observed empty page establishes completion; short pages do not. Missing,
ambiguous, repeated, invalid or capped results cannot authorize a write. Keep
all bound account fields unchanged until every requested remote lookup succeeds, and
check cancellation after reads. Bare previews retain email with an unresolved
warning and open no client or cached preferences unless @me also needs them.
Native payload account IDs are never interpreted as email lookup requests.

Native Windows configuration contracts use representable filenames while retaining
stored-value control and secrecy checks. Windows UTF-16 process arguments cannot
carry invalid UTF-8 bytes: skip only those subprocess cases explicitly and retain
raw-byte pre-I/O validation through in-process registered initialization handlers
on every platform. Keep Unix filename and process coverage. An executable refused
before test entry earns no native acceptance credit; record the exact artifact
separately from other successfully executed artifacts and never change policy.

Watcher add/remove accepts explicit --user-query name/email searches through
command.AccountField.LookupQuery and the shared account resolver. --user stays
literal IDs/@me; the flags conflict even when either was authored empty. Pure
validation rejects blank query before I/O. Query text is never @me or cached
identity, even when spelled @me. Search requires one complete visible active
match and inherits the shared authenticated response, bounds and cancellation
policy. Resolve once before keyed work; a match is not exact email/name proof or
watcher permission. Bare previews retain user_query with account_query_unresolved
and empty account_id, opening no client or preferences for that query. Live and
explicit remote previews publish the resolved ID plus original query context.

Explicit TUI deferral tests must exercise the actual command with and without
`--defer-tui`, not only the mapper. Check typed deferred names without values,
preview absence of directory/provider effects, exact source recovery, no inert
TUI destination keys, and fresh recorded-policy rollback for distinct and same
source/target paths. Preserve unknown-schema and malformed-shape refusal. Keep
an independently delivered policy-8 fixture from the previous committed producer;
only fixture paths and enclosing artifact hashes may be rehomed. Policies 1–8
must retain strict TUI refusal. Future-version refusal uses an unsupported version,
not the next version after the default producer when opt-in policies exist.

The `defer-tui` matrix proof must execute both admission states and check that
neither preview publishes settings or recovery. Human and compact previews must
preserve the deferral boundary without exposing retained TUI values.

Exact watcher account preflight must exercise both add/remove actual commands,
read-only previews, explicitly validated live writes, and unchanged ordinary
literal paths. Check one escaped accountId query before all writes, exact native
reply binding, absent/null activity, inactive/deleted rejection and no partial
field publication after late cancellation. Retain authenticated final scope and
credential guards, bounded reply refusal, privacy-hidden email and native unknown
bytes. Distinct query and exact-ID lookups never share a cached match. Live
acceptance may change only verified self subscriptions on owned issues and must
independently verify cleanup.

Exact-account admission must reject duplicate decoded root members and case
variants of accountId/active/deleted through the shared native object owner
before typed decoding. Include overwritten inactive/deleted values and escaped
duplicate key spellings in adapter and actual CLI no-write guards.

Search protocol guards must exercise discovery, both watcher verbs and
create/edit/clone assignee-email consumers against duplicate ID/activity/deletion
members, case variants and escaped duplicate names. Require one refused search
and zero issue requests. A valid first row followed by an invalid row must not
publish any of the page. Keep genuine native unknown-member/nullable-email
fidelity, paging and authenticated final-reply guards; compile named mutations
that bypass admission and verify they fail.

Current-user native admission guards must refuse duplicate/escaped identity,
case variants, activity/email duplicates and repeated unknown root members.
Exercise actual auth status with --show-token in every output mode, candidate
login before provider/public settings publication, create/edit/clone and watcher
@me writes, explicit remote previews and fresh whoami save/preview paths.
Assert one identity read, no dependent requests or writes, no token disclosure
and no created settings directory. Retain native privacy, interrupted body,
configured-timeout/cancellation, HTTP and retry guards. Mutation credit requires
compilation and a named guard failure; execute native Windows owner and CLI
contracts separately from cross-compilation.

Native Windows pipe guards exercise anonymous, synchronous named and already
Go-wrapped overlapped pipes. Verify non-inherited duplicates, unchanged original
handle flags and pipe modes, repeated cleanup, EOF and closed-handle refusal.
A later original read must actually wait before data arrives; prebuffered reuse
cannot prove its IOCP association survived. Exercise real sockets under Windows
FILE_TYPE_PIPE classification and preserve other borrowed readers. Registered
stdin and accessible text/confirmation guards must observe consumed input,
withhold writer closure, retain canceled/timeout outcomes and reuse the original.
Use an overlapped fixture for native PeekNamedPipe observation; a synchronous
probe may block behind the pending read. Preserve native probe errors instead
of treating every failure as socket exclusion. Restricted readable pipe guards
must prove both data access and the genuine information-query failure.

Native Windows console guards allocate fresh hidden consoles and inject only
owned INPUT_RECORD events with the verified native layout and matching ASCII
virtual key codes. Observe actual consumed text or masked password prefixes,
withhold successful completion, then cancel or expire the caller context.
Assert ordinary canceled/timeout classification, no bound-value publication,
exact original modes and a later pending read through the borrowed handle.
Require an independent non-inherited native console handle and its closure;
a duplicate-only mutant must fail the original pending-read reuse guard.
Include Unicode success, validation retry, repeated fields, cancellation before
kernel entry, panic cleanup and native restoration failure. Keep thread reuse
on the same pinned thread after the owner returns. Run test:input:windows on
Windows; its nativeconsole-tagged guard fails explicitly on other hosts.
Retain helper-entry receipts and failed/refused invocations. An execution policy
refusal or a timed-out reuse assertion never earns native success credit.

Native Windows configuration filesystem acceptance uses owned directory
junctions with independently verified IO_REPARSE_TAG_MOUNT_POINT metadata.
Exercise missing and long target paths, literal trailing-dot/space names with
untouched ordinary siblings, retargeting refusal, exact recovery,
default/explicit initialization and real migration/rollback commands. Preserve
JSON/compact/human output checks and preview no-publication guarantees. Use
NoProfile/NonInteractive PowerShell only to construct literal owned fixtures;
join its exit and remove junctions without deleting target contents.
Run test:config:windows on Windows; nativefilesystem-tagged gates fail explicitly
on other hosts. Junction acceptance does not waive symbolic-link privilege or
macOS requirements, and refused executables earn no execution credit.

Native draft path acceptance uses the same verified junction fixtures. Exercise
literal trailing-dot/space directory creation with untouched ordinary siblings,
exclusive creation races, reopened-identity mismatch, late cancellation receipts
and refusal of junctions at the draft root or each member. Actual draft, inspect
and resume previews cover every output mode, exact native bytes, protected ADF
and no extra requests. Real resume editors cover junction ancestors and literal
names through success, failed/invalid saves and Control-C/Control-Break. Run
`test:draft:windows` and `test:editor:windows` on Windows; wrong-host gates fail
explicitly. A compiled mutant earns credit only when its named guard actually
executes and fails; native policy refusals earn none.

Native attachment directory guards use owned junctions and literal extended
trailing-dot/space targets with untouched ordinary siblings. Verify exact binary
contents, collision/no-request previews, exclusive creation and staged overwrite
through actual commands in every output mode. Native directory completion must
exclude competing regular files and retain literal argv, including metacharacters
and nested paths. Zsh guards cover zero/one/two/unbounded unpredicted positions
and prove that empty actions do not invent filesystem candidates. PowerShell
provider results may omit a final separator; assert `ProviderContainer` and exact
relative identity while admitting only optional `./` and separator spelling.
Run `test:attachment:windows` for actual Windows filesystem and shell acceptance.
Keep native refusal receipts uncredited and separate from Linux PowerShell tests.

Attachment filename operations use the shared nativepath rooted file owner.
Keep literal Windows trailing-dot/space leaves and reopenable extended paths;
never let Win32 cleanup select an ordinary sibling. Retain exclusive creation,
exact collision checks, staging and incomplete-stream cleanup through the same
root handle. Literal reparse traversal refuses; ordinary rooted paths retain
their existing semantics. Native tests cover authored and metadata names,
ordinary/extended directories, failed streams and sibling preservation. Windows
delete-sharing refusal is directory identity evidence, not an executed rename.

Output-audit editor recipes use the owned test-executable fixture, never an
installed editor or Unix no-op command. Require a fresh receipt with literal
argv and an input digest after live entry, and no receipt after previews or
terminal refusal. Keep Windows temporary/profile paths owned. Fixture execution
does not establish native terminal allocation or faithful diagnostic capture.

Linux and Darwin audit invocations share their diagnostic collector over native
PTY allocators. Keep the master nonblocking and owned until its reader joins.
Preserve exact independent stdout and bounded diagnostic bytes; invisible escape
sequences must remain observable. Only Linux's final-slave `EIO` is an ordinary
end of stream. Refuse more than one MiB of diagnostics and terminate/join the owned
command on collector failure. Closing the parent slave does not prove every writer
closed: honor caller cancellation and cap the subsequent drain at five seconds.
Join before closing the master, retain custom cancellation causes and test actual
helpers, failed launches and independently retained writer descriptors.

Darwin PTY allocation uses the selected native grant/unlock/name operations and
the fixed 128-byte name ABI. Keep opaque native buffer calls escape-safe.
The selected `x/sys` version has no typed PTY-name getter: the test-only query
uses its raw ioctl ABI with a narrow deprecation exception. It is not linked into
the release CLI and still requires actual macOS acceptance. Neither
cross-compilation nor Linux execution proves macOS native allocation, EOF behavior
or editor operation; retain actual macOS acceptance as a separate requirement.

Windows amd64 audit capture uses `tests/internal/nativeaudit` and the shared
`tests/internal/nativejob` owner. The coordinator starts a hidden console helper,
assigns its job, waits for native console setup and records that accounting
baseline before admitting the CLI. The helper cannot construct a CLI before
admission. Its environment is an explicit clone, never a process-wide overlay.
Only the parent retains the outer job handle; cancellation before assignment
must also terminate the waiting helper. Verify abrupt owner death against actual
live descendants, joined native handles and the absence of a normal release.
Native job buffer calls preserve the Go pointer escape contract across opaque
`uintptr` wrappers; keep limits and accounting storage off movable stacks.

The observer admits only the selected owned Go build images with matching
toolchains, explicit `CGO_ENABLED=0` build metadata and native file identities.
It follows their threads from startup,
binds the selected system-module entry points and preserves its debug registers
across `NtContinue`. Native structure reads are limited to entry prefixes and the
fixed thread context; never read write payloads or patch executable code. Nonzero
console writes, unclassified handles and unmatched pipes refuse silence. Disk
writes and exact aliases of the retained stdout pipe are distinct. Retain that
pipe's identity through observation, then release every producer before joining
its bounded reader. An unknown writer-release outcome cancels the reader.

Keep a failed debug continuation pending until it succeeds or bounded cleanup
fails. Close every image/DLL event file even on lookup failure, and refuse an
invalidated module binding. A validated receipt must prove the actual selected
CLI exit, complete process/loader admission, balanced event handles and joined
processes/readers. Native console setup and CLI observations have separate
accounting scopes; the parent job must finish empty. A helper exit alone is a
capture failure, never the CLI result. Verify exact stdout bytes independently
and reject noisy or incomplete captures without inventing empty stderr.

Exercise actual `config edit` and `issue edit resume` in every output mode, both
visible and invisible diagnostics, missing admission, bounded control streams
and parent cancellation that terminates the actual supervisor and CLI. Cancellation
during stdout publication retains both the cancellation cause and acknowledged
bytes; recheck cancellation before publication and after joined cleanup. Keep
supervisor restoration after forced termination separate from cooperative CLI
restoration. Native macOS behavior and unsupported architectures remain separate
acceptance requirements.
