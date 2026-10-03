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
says why and names the library function it is not (existing examples:
`internal/jira/worklog.go` `ParseDuration` — Jira workday semantics, not
`x/human.ParseDuration`; `internal/tui/components/markdown` — richer than
`primer/render.Markdown`).

gechr deps are pre-1.0: version bumps are deliberate, dedicated changes with
an API-diff review — never a side effect of unrelated work. The pinned
version often trails the library; check what the pin actually provides.

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

*   Every Jira call takes the command's context — cancellation and
    `--timeout` flow from cobra's `cmd.Context()`; no `context.Background()`
    on command paths.
*   CGO exists only for the `1password` backend behind build tags with
    `_nocgo` stubs; everything else must build with `CGO_ENABLED=0`.
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
