---
description: >
  Architecture map and cross-cutting concerns: layers, key directories, the
  command lifecycle, where each concern lives, and the development workflow
  (mise, tests, docs generation).
paths:
  - "internal/**/*.go"
  - "cmd/**/*.go"
  - "tests/**/*.go"
---

# Project

jira-cli is a Go/Cobra CLI for Jira Cloud, built agent-first. This file is
the map; deep rules live in [commands.md](commands.md),
[output.md](output.md), [config.md](config.md), [security.md](security.md),
[style.md](style.md), and [go.md](go.md).

## Layers and import direction

```text
cmd/jira             entry point: root.Execute(ctx), owns process exit
cmd/gen-docs         docs generation (reuses root.New — reference site
                     cannot drift from the real command tree)
   ↓
internal/cli/*       cobra commands by domain (issue, epic, auth, config, …)
internal/cli/cmdutil shared command helpers: flags, gates, envelope, fanout
internal/cli/root    root wiring: PersistentPreRunE, help, completion
internal/envelope    operation-owned Output structs and derived schema registry
   ↓
internal/jira        REST client + typed services (+ customfield registry)
internal/adf         ADF parse/validate/render/markdown (+ node registry)
internal/config      koanf loader, profiles, credential store selection
internal/cache       per-profile metadata cache
internal/pipeline    5-stage mutation validation (validate-and-encode)
internal/{browser,editor,issuekey,jql,refresh,version}
internal/tui/components/markdown  shared release-note rendering (dashboard deferred)
```

Every package lives under `internal/` — this module exports no public API.

## Key directories

| Path | Role |
|------|------|
| `cmd/jira/` | entry point |
| `internal/agentguides/` | the `//go:embed` docent guide set (`jira agent guide` / `jira guide`); contract-tested via `docenttest.Validate` |
| `internal/cli/agent/` | the `agent adf-matrix` / `agent fieldtypes` registry commands, mounted under docent's agent group |
| `internal/cli/cmdutil/` | flags+metadata helpers, gates, envelope, fanout, keyed results, help sections |
| `internal/envelope/` | one registered data shape per operation; concrete fixed members, reviewed Dynamic exceptions, derived JSON Schemas |
| `internal/cli/registry.go`, `verbs.go`, `plain_*.go` | human-mode renderer registry and verb phrases |
| `internal/cli/errors.go`, `json.go`, `detector.go` | error mapping, envelope machinery, output-mode detection |
| `internal/jira/customfield/` | field-type registry (encoders/validators) — never branch on field-type strings |
| `internal/adf/` | node registry drives parse/validate/render; golden fixtures |
| `ruleguard/rules.go` | the two custom lint rules (see [style.md](style.md)) |
| `tests/` | `contract`, `unit`, `integration`, `guardrails`; `tests/live/` is `live`-tagged, run via `mise run test:live` |
| `.claude/worktrees/` | live git worktree checkouts — exclude (with `.cache/`, `dist/`) from searches; never edit inside |

## Command lifecycle

1.  `cmd/jira/main.go` calls `root.Execute(ctx)`; main owns process exit and
    maps `ErrCompletionHandled` → 0.
2.  Startup preflight (`internal/cli/startup`, clib `Preflight()`) intercepts
    completion directives **before** parsing — the `completion` command
    deliberately bypasses `PersistentPreRunE`.
3.  Root `PersistentPreRunE` (root/root.go) loads config, wires clog
    (stderr, `SetEnvPrefix("JIRA")`, `--color`), resolves the output mode
    (detector), and seeds the context logger.
4.  The command's `RunE` acquires services via `cmdutil`
    (`JiraClientForCommand`), gates dry-run/read-only/ADF-mode, does the
    work behind a spinner, builds its registered `internal/envelope` Output,
    and returns through `cmdutil.WriteEnvelope`. Stable context is shared by
    dry/live and single/keyed paths; only server outcomes are conditional.
5.  Errors map through `internal/cli/errors.go` to stable codes and exit
    codes 0–8 (see [output.md](output.md)).

Bare `jira` is contract-aware: TTY → help, non-TTY/agent → JSON schema.

## Cross-cutting concerns (where each lives)

*   **Output contract** — operation shapes and schemas in `internal/envelope`;
    rendering rules in [output.md](output.md); user-facing spec in the embedded
    guide.
*   **Mutations** — the validate-and-encode pipeline (`internal/pipeline`);
    `--dry-run` is local-only and never contacts Jira.
*   **Errors** — typed errors + adapters in `internal/cli/errors.go` only.
*   **Config** — [config.md](config.md).
*   **Metadata caching** — `internal/cache`, surfaced by the `cache` command
    family and the completion predictors.
*   **Docs** — `docs/reference/` is generated from the command tree and
    gitignored; never hand-edit. Behavior changes update the embedded agent
    guide + schemas in the same PR — guide/schema drift is a bug class here
    (an `init()` panic and guardrail tests defend parts of it).
*   **TUI widgets** — `github.com/gechr/primer`; check it before adding
    anything under `internal/tui/components/`.
*   **Versioning** — build metadata lives in `internal/version`, injected by
    the `mise run install` ldflags.

## Workflow

*   mise provisions the pinned toolchain. Build with `mise run install`
    (sets the version ldflags a plain `go install` cannot).
*   `mise run ci` is the merge gate (macOS/Linux/WSL only — it fails on
    Windows). Tighter loops: `mise run check` / `test` /
    `test:integration`; `mise run fix` auto-applies fmt/lint/rumdl fixes.
*   PR titles are conventional commits (`feat(issue): …`) — PRs squash, so
    the title becomes the commit on `main`. Commits are signed. Pre-commit
    hooks via `hk install` (globs the working tree, not just staged files).
*   Live verification targets the dedicated probe project configured via
    `JIRA_LIVETEST_PROJECT` — never a real project's board.
