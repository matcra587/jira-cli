---
description: >
  Go conventions and the gechr-first library policy: the x quick map,
  documented exceptions, upgrade discipline, errors, context/portability,
  concurrency, and testing.
paths:
  - "**/*.go"
---

# Go conventions

IMPORTANT: before writing any helper, check whether a gechr library already
provides it. This repo does not hand-roll what its libraries provide: `x`
below, `clog`/`clib` for output and flags (see [output.md](output.md)),
`primer` for the TUI (see [project.md](project.md)).

## gechr/x quick map

| Need | Use |
| ---- | --- |
| Atomic writes, file/dir checks | `github.com/gechr/x/os` |
| Shell quote/split, XDG dirs, completion paths | `x/shell` |
| Bytes/plurals/time-ago/duration formatting | `x/human` |
| Blank/truncate/natural-sort/closest strings | `x/strings`, `x/slices` |
| Path resolution | `x/filepath` |
| Retryable HTTP status classification | `x/http` |

### Adoption policy (decided once — do not re-litigate per PR)

*   **`xstrings.{Any,All}{Empty,NonEmpty}`** — adopt for predicates over
    **two or more** strings of the same polarity (`a == "" || b == ""` →
    `xstrings.AnyEmpty(a, b)`). Keep plain `== ""` for a single value, and
    keep explicit conditions when polarities mix (`a == "" && b != ""`).
*   **`xslices.Map`** — adopt when the loop's only job is constructing each
    output element from the corresponding input element: length-preserving,
    no filtering, no side effects, no index use beyond the element, no
    partial initialization. Loops that `append` conditionally, mutate in
    place, or pre-seed structs for later mutation stay loops — as do loops
    whose element type cannot be named cleanly (an anonymous wire struct
    would put a struct literal in the closure signature).
*   **`lipgloss.LightDark(isDark)`** returns `func(light, dark) color.Color` —
    adopt for any dark/light colour pair instead of a hand-written `if dark`.
    Its parameter order is **(light, dark)**; store pairs in that order so
    they pass straight through (`internal/cli/sinks.go`'s `colorPair`).
*   **`x/human.Plural(n, singular, plural)`** for singular/plural selection.
*   **`clib`'s `theme.Dark()`/`theme.Light()` are CONSTRUCTORS, not
    accessors** — each allocates a full theme (~35 styles, 25KB, 34 allocs,
    measured). Memoize any fallback that builds one with `sync.OnceValue`,
    lazily rather than at package init so `applyThemeName` still runs first
    (`internal/cli/theme.go`'s `darkFallback`/`lightFallback`). Never call
    one per row or per inline span.
*   **`xos.Exists` stays out of sites that inspect the error** — decided
    against: where the caller distinguishes `os.ErrNotExist` from real
    failures or propagates the original error (config loader, attachment
    writes, auth store), `os.Stat` is the correct API per the x guide's own
    probe rule. Audits should not re-report these.

### Look-alikes that are NOT substitutes (checked — do not re-propose)

| Tempting | Why it is wrong | Use instead |
|----------|-----------------|-------------|
| `xstrings.SplitLines` | delegates to `SplitBy`, which trims each part and DROPS empty ones — deletes every blank line from rendered content | `xstrings.SplitLinesRaw` (lossless, normalizes CRLF) |
| `xstrings.Pad{Left,Right,Center}` | pads by RUNE count, not display cells — misaligns CJK and emoji | `ansi.StringWidth` arithmetic |
| `lipgloss.Complementary` | 180° hue rotation, not a readable-contrast pick (`isDarkColor` is unexported) | keep the local YIQ luminance pick |
| `lipgloss.Style.Width` | WRAPS content wider than the width | keep floor-only padding local |
| clib `theme.RenderTimeAgo` | hardcodes `time.Now().UTC()`, no injectable instant | keep `cli.TimeFrom(iso, now)`'s test seam |

Package names that get guessed wrong: it is **`x/terminal`** (not `x/term`),
and **`primer` is its own module** `github.com/gechr/primer` (not
`clib/primer`). `go list -m all` FAILS in this repo (`gechr/coauthor` — repo
not found, git exit 128); resolve a module's source with
`go list -m -f '{{.Dir}}' <module>` instead.

Check the module's docs (pkg.go.dev or its source) before concluding a
primitive is missing — a gap is an upstream candidate, not a license to
hand-roll. A
deliberately different local implementation is allowed when its doc comment
says why and names the library function it is not. For example,
`internal/issueedit` needs rooted no-clobber publication, which the selected
`x/os.AtomicWrite` replacement primitive does not provide; see Recoverable
description publication below.

gechr deps are pre-1.0: version bumps are deliberate, dedicated changes with
an API-diff review — never a side effect of unrelated work. The pinned
version often trails the library; check what the pin actually provides.

The licensed `internal/cli/completion/native` Bash/PowerShell/Zsh backports are a specific
exception for demonstrated defects in the published clib generators. They use
`complete.RegisterShell`, retain the reviewed native implementations, and delegate
public model/validation/sorting to the pin. Preserve provenance and the MIT
license. This is not permission to hand-write new scripts or duplicate working
library behavior. Remove the backports when a published version passes the same
real-shell guards without regressing contextual completion or versioned installation.

## Errors

*   Wrap with `fmt.Errorf("context: %w", err)`; error strings lowercase,
    no trailing punctuation.
*   User-visible failures are typed errors mapped in
    `internal/cli/errors.go` — add an `errors.As` adapter with a stable
    snake_case code and hint, never a substring match.
*   Log or return, never both (the single-handling rule).
*   Production code handles every returned error. `errcheck.check-blank` is
    enabled; a deliberate best-effort cleanup or probe needs a narrow
    `//nolint:errcheck` with the reason on that line. Tests stay linted, with
    only explicit blank error assignments excluded.
*   `panic` only for programmer/build invariants (e.g. embedded-guide
    drift), never on a path user input can reach.

## Context and portability

*   Credential reads/writes and credential-backed client/cache factories take
    the command's context explicitly. `config.Load` stays local; use
    `config.Resolve(ctx, options, store)` for the credential layer. Do not put
    contexts in options or adapters, or replace a command context with a detached
    background context.
*   Preserve lazy initialization with `command.OnceContextValues` when a factory
    needs context. Its first context belongs to one command invocation; results,
    errors and panics retain `sync.OnceValues` semantics. Cheap constructors and
    explicit-scope metadata caches remain local and store-free.
*   The current OS-keyring library is synchronous and context-free internally.
    Check cancellation before native reads or mutations; do not claim in-flight
    cancellation or start a detached worker to simulate it. A native mutation
    already entered retains its actual return; cancellation does not prove no write.
    Credential recovery distinguishes admission from acknowledgment: the shared
    recovery interface's mutation bool records actual native entry, including
    failed writes. Opaque snapshots must conceal secrets in JSON and fmt output;
    provider-specific metadata/preimages stay inside the selected secret provider.
*   Every Jira call takes the command's context, preserving caller cancellation
    and deadlines. `--timeout` configures the HTTP client; it does not create
    a command-wide deadline. No `context.Background()` on command paths.
*   The CLI and 1Password service-account SDK path build with `CGO_ENABLED=0`.
    Linux/macOS desktop authorization requires CGO; Windows uses native loading.
    SDK initializer admission and store queues honor context. Cold upstream WASM
    loading uses a background context internally; do not claim full interruption.
    Never silently substitute `op` or treat synthetic refusal as live SDK success.
*   Windows is a release target: `filepath` for paths, no Unix-only
    assumptions (separators, `/proc`, signals).

## Concurrency

*   Multi-key fan-out goes through the shared executor honoring
    `--parallelism` (1-16); do not spawn ad-hoc goroutine pools in
    commands.
*   CI runs the race detector; keep new code `-race` clean. Packages that
    own goroutines use goleak in `TestMain`.

## Testing

*   Standard library `testing`, table-driven, lowercase named subtests.
    testify only in `tests/live`.
*   Unit tests sit package-adjacent; cross-cutting behavior lives in
    `tests/{contract,unit,integration,guardrails}`.
*   Invariants get guardrail tests — when a rule in these files becomes
    mechanically checkable, add one under `tests/guardrails`.
*   Contract tests pin envelope shapes; update them deliberately, never
    loosen one to unblock a change.
*   Live verification targets the dedicated probe project configured via
    `JIRA_LIVETEST_PROJECT` — never create test issues, comments, or
    transitions in a real project's board.

Editor consumers share `command.ResolveEditor` and `command.RunEditor`. Preserve
JIRA_EDITOR, stored editor, VISUAL, EDITOR precedence, literal argv, early executable
validation and no fallback. Preference reads remain local and credential-free;
terminal/process ownership stays in RunEditor. Do not duplicate selection in a domain.

## Recoverable description publication

The pinned `x/os.AtomicWrite` replaces an existing target and does not offer a
rooted no-clobber publication primitive. `internal/issueedit` therefore stages
immutable bytes under `os.Root` and publishes with `Root.Link`, refusing existing
members. Remove the private stage before making the published inode read-only:
Windows read-only attributes affect every hard link. Retain operation admission,
acknowledgment and durability separately, including failures after publication.
Rooted reads reject symlinks/nonregular members, use bounded reads and recheck
identity. Unix opens also use no-follow/nonblocking flags to refuse substituted
FIFOs without hanging. Filesystem calls check context at their boundaries; this
does not claim interruption of a synchronous filesystem operation. Only Unix
successful syncs claim `synced`; other platforms report `platform_unconfirmed`.
Reopening observes readiness and reports `not_checked` durability, without
claiming publication effects from the current invocation.

Filesystem completion enumerates only names in one explicitly selected literal
directory through the bounded local owner. It reads no file contents, opens no
credentials and never uses a glob to interpret authored metacharacters. Failed,
incomplete or canceled inventory offers nothing; synchronous filesystem calls
still have their native cancellation limit. Preserve the original directory
prefix and mark directories for further completion.

Use the shared command.CSVCompletion codec for an incomplete CSV argument. It
retains completed fields byte-for-byte and decodes the unfinished field using
strict CSV quoting, including quoted commas and doubled quotes. x/strings.SplitCSV
trims/drops values and clib's last-comma split cannot parse this grammar. The
encoder uses encoding/csv and represents a single empty field explicitly.

CSV directory choices retain an unfinished current field so a second Tab can
complete a leaf in the same argument. Theme choices preserve literal symlink/..
traversal when made absolute and validate the final transport bytes, including
the current directory. Verify these identities in native editors and named guards.

PowerShell directory insertion consumes an explicit `--@directory` hint in the
current-word callback description slot. Ordinary cached values ending in a slash
remain complete literal arguments. Keep the hint private to the generator and
verify both directory continuation and slash-ending configuration values. Fish
current-word callbacks reuse the shared version-aware token helper.

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

Literal watcher preflight uses `AccountField.ValidateRemoteID` through the shared
`ResolveAccounts` owner only under explicit `--validate-remote`. Bare previews
and ordinary literal writes remain lazy; @me/query lookup keeps its own policy.
`core.ResolveActiveAccount` and the native `GetUser` adapter both enforce exact
account binding, native identity/activity markers and deleted-state validity.
Preserve authenticated final API scope/credential binding, bounded capture,
HTTP/retry/context classification and unknown native members. Missing/null
activity is an invalid reply; inactive/deleted accounts refuse. One exact-ID
read is shared across all issue keys, separately from query-search matches.
Resolve all requested fields before publishing any; no lookup proves watcher
permission or issue existence. New Jira seams belong in shared failing test
stubs, never silent successful unused methods.

User search validates every raw native row through `jira.DecodeNativeObject`
before typed decoding or page publication. Reject duplicate decoded root members
and case variants of accountId/active/deleted, including escaped duplicates;
permissive typed JSON decoding cannot establish identity or activity. Preserve
unknown member bytes, absent/null email, fetched-row paging, completed prior
pages and complete unique-active resolution. An invalid later row publishes no
part of its page and cannot authorize an account write. Context checks precede
transport entry and publication. Use the same shared admission for discovery,
watcher queries and assignee email; never add per-command decoders.

Current-user authentication admits raw JSON through `jira.DecodeNativeObject`
with generated `UserDetails` properties before typed decoding. Reject duplicate
decoded root names and case variants of native members before authentication,
credential/settings publication or @me writes. Preserve the existing streamed
EOF confirmation and its configured timeout/cancellation, authenticated scope,
HTTP taxonomy, safe-read retry receipt, opaque IDs and native unknown bytes.
Current-user authentication does not require active=true; absent/false activity
and privacy-hidden nullable values retain their existing semantics. Keep the
injected-client identity guard independent of transport admission.
