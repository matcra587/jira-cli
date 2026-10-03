---
description: >
  Go style and the lint roster golangci-lint enforces (depguard bans, the
  two ruleguard rules, gosec posture).
paths:
  - "**/*.go"
---

# Go Style & Lint

Formatting is tooling's job: gofumpt (`extra-rules: true`) via `mise run fix`,
gated by `mise run check`. See [output.md](output.md) for the output contract
and [commands.md](commands.md) for the command pattern.

## Code style (judgment calls the tools can't make)

*   US spelling in code, comments, and identifiers — misspell runs locale
    `US`, so `behaviour`/`colour` fail the build.
*   Comments state rationale and constraints — why this design, what breaks
    without it, which library function a local implementation deliberately
    is not — never what the next line does. This repo's comment density is
    high and deliberate; match the surrounding file.
*   When inserting a function ABOVE an existing one, check the doc comment
    between them: a new `// Name …` block with no blank line before it
    ANNEXES the comment above it, leaving the original function
    undocumented and its paragraph attached to the wrong symbol. This
    recurs, survives green gauntlets, and reads fine in a diff — `go doc`
    on the older symbol is the check.
*   Every package carries its `// Package <name> …` comment in a dedicated
    `doc.go` (package comment + package clause only) — a new package starts
    with one. Never put the package comment atop a code file; keeping it in
    `doc.go` keeps package-doc diffs isolated and the doc easy to find.
    Precedents: `internal/cli/doc.go`, `internal/adf/doc.go`.
*   Handle errors and edge cases first (early return); drop `else` when the
    `if` body returns; keep the happy path at minimal indentation.
*   Composite literals use field names, never positional fields.
*   No stuttering across package boundaries: `cache.New`, not
    `cache.NewCache`.

## Do / Don't

| Do | Don't |
|----|-------|
| `command.AddStringVar(cmd, …, command.FlagOpts{…})` | raw `fs.StringVar` (ruleguard-banned) |
| return typed data from `command.Spec.Run` | `json.NewEncoder(out).Encode(...)` for envelopes |
| `clog.Ctx(ctx).Info()…Msg()` | `log`, `log/slog`, `fmt.Print*` for status |
| `errors.As` adapter in `internal/cli/errors.go` | matching on `err.Error()` substrings |
| spinner/progress around Jira calls | bare blocking calls (ruleguard-banned) |
| cobra `Args` validators | `len(args)` checks inside `RunE` |
| `x/os.AtomicWrite` | hand-rolled temp+rename |
| gechr primitive (see [go.md](go.md)) | local reimplementation without a documented exception |

## Enforced: golangci-lint (`.golangci.yml`)

`default: standard` plus an explicit enable list — core correctness
(bodyclose, errcheck, errorlint, govet, gosec, ineffassign, misspell,
staticcheck, unused), robustness (contextcheck, containedctx, durationcheck,
sqlclosecheck, nolintlint), directive hygiene (gocheckcompilerdirectives),
testifylint, and gocritic **solely to host ruleguard** (all built-in gocritic
checks are disabled).

### depguard bans

| Banned | Use instead |
|--------|-------------|
| `log` | `github.com/gechr/clog` |
| `log/slog` | `github.com/gechr/clog` |
| `io/ioutil` | `io` / `os` (deprecated since Go 1.16) |

### ruleguard rules (`ruleguard/rules.go`, failOn: all)

| Rule | Banned | Use instead |
|------|--------|-------------|
| `rawPflagDeclaration` | raw pflag setter methods (`StringVar`, `BoolVar`, …) outside `internal/cli/command/` | `command.Add*` helpers, which attach clib metadata in the same call |
| `unwrappedBlockingCall` | Jira client calls not wrapped in a spinner/progress | `clog.Spinner(...)`/fanout progress |

The `internal/cli/command/` exemption exists **because** command is the
register-and-extend wrapper layer — its `flags.go` and bundlers call the raw
setters on purpose. Do not widen that exemption.

### gosec posture

All the classic includes (G1xx–G5xx) with `audit: enabled`, plus config that
matters when writing code:

*   `G101` scans identifiers matching
    `passwd|pass|password|pwd|secret|private_key|token|api_key` — name
    variables holding non-secrets accordingly (e.g. `tokenType`, fine;
    `token = "literal"`, flagged).
*   `G306` sets the write-permission bar at `0750`.
*   Test files, `internal/testutil/`, and `tests/live/` are exempt from
    G101/G204/G304/G306; a handful of production files carry reviewed G304
    exemptions (stdin, editor roundtrip, cache, docs gen, alias/config
    loaders) — additions to that list need the same review.

## nolint discipline

`nolintlint` is on: every `//nolint` needs a specific linter and a reason.
The repo carries ~17, mostly reviewed gosec on browser/URL-open paths — keep
it that way.
