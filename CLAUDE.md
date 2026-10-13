# jira-cli

Agent-first Jira Cloud CLI for shell-resident coding agents — zero-install,
pipeable, composable. The contract serves what Jira is; nothing is silent
(0 magic).

## Commands

*   `mise run check` (fmt + lint + rumdl + vet + unit) · `mise run ci`
    (adds race, the output-conformance audit, tidy-drift, workflow lint,
    govulncheck) — CGO off; only `test:race` sets CGO_ENABLED=1 itself.
*   `mise run audit` — the output-conformance matrix on its own
    (build-tagged, hermetic, stub-driven; runs the real binary).
*   `go generate ./internal/jira ./internal/adf` — regenerate wire types /
    ADF views from the vendored pins; freshness tests fail CI when stale.
*   `mise run upstream-check [--type adf|api] [--check]` — network probe:
    are the vendored pins behind upstream, and what changed inside each
    definition. Read-only; bumps stay deliberate.

## Architecture

`AGENTS.md` is the cross-agent entrypoint for these project standards. Update it
and the relevant `.claude/rules/` owner documentation when a capability changes.

*   **cmd/jira → internal/cli/root** — conductor (cli/cobra) dispatch;
    composition root with lazy Deps (help/version construct no client);
    the error taxonomy in `internal/cli` maps to the 0–8 exit contract
    via `WithExitCode`.
*   **internal/jira** — GENERATED wire types (`cmd/gen-wire`, from the
    sha256-pinned OpenAPI specs in `internal/jira/spec`) with
    raw-capture: `Nullable[T]` keeps absent/null/value distinct (build
    values via `NullableOf`/`NullableNull`, never a bare literal) and
    `Extra` carries unmodeled fields verbatim.
*   **internal/adf** — byte-preserving substrate (parse→marshal is a
    fixed point; unknown nodes and keys survive verbatim) + generated
    typed views + draft-04 validation. `Document.Marshal()` is the
    preservation-grade exit; plain `json.Marshal` re-escapes and breaks
    byte fidelity.
*   **internal/adapters/jira** — REST client. Request bodies serialize
    with `jira.MarshalNoEscape`, preserving raw JSON string spelling;
    `SetEscapeHTML(false)` alone does not preserve line separators on
    Go 1.27. Safe-retry transport: GET/HEAD/OPTIONS
    plus the exact POST `search/jql`, `search/approximate-count`, and
    `jql/parse` reads are the ONLY retryable requests;
    writes are never auto-replayed, redirects on unsafe methods are
    refused, `Retry-After` is clamped and jittered.
*   **internal/cli/completion/native** — licensed backports of reviewed clib
    Bash/PowerShell/Zsh generator repairs through `complete.RegisterShell`. The root registers
    them once before exposing generators. Reuse clib's public metadata and
    validation; do not rewrite printed scripts or break versioned `go install`
    with a module replacement. Other shells use their native library generators.
*   **internal/cli/cache/transitions.go** — bounded per-issue ID/name/destination
    hints from explicit successful workflow reads. The observed read starts the
    five-minute TTL; site/account scope remains adapter-owned. Completion reads
    local hints only and never uses cached availability as mutation permission.

## Hard rules

*   depguard is strict — a new import path needs a `.golangci.yml` allow
    entry, justified in the change.
*   gechr-native is mandatory: if x / clog / clib / conductor provides a
    behavior, hand-rolling it is a defect — verify against the installed
    module's actual API before writing, and replace duplicates on sight.
*   No ticket keys, tenant names, or internal context in code, comments,
    or commits (betterleaks enforces).
*   Commands are built through the one construction path
    (`command.Spec[T]`/`New[T]`, see
    [.claude/rules/commands.md](.claude/rules/commands.md)). A handler
    returns typed data and has no writer in scope; anything that reaches
    for a stream itself is a defect. If the path cannot express a
    command, fix the path — do not go around it.

## Git

*   Signed conventional commits, curated linear history.
*   **Changelog:** a user-facing `feat`/`fix` needs a changie fragment,
    created non-interactively and staged with the change:
    `changie new -k <added|changed|fixed> -b "<user outcome>" --interactive=false`;
    genuinely not user-facing → a `Changelog: skip` trailer. The
    commit-msg hook enforces it.

Area-specific conventions auto-load from `.claude/rules/` — **but those
files still describe the 0.15 architecture and are being rewritten. Where
they contradict this file or the code, they are wrong: verify against the
tree before trusting any path, function, or package they name.**

The embedded guides and runtime schema (`jira agent guide`, `jira agent schema`)
are the authoritative CLI behavior sources. Update them with behavior changes;
keep workflow instructions in the embedded guides rather than copying them here.

Completion generation removes Cobra's combined boolean `h/help` spec when
Conductor supplies separate short and long help descriptions. Keep this repair
at the shared `WithGenerator` model boundary: duplicate options break Fish's
`argparse` dispatch before a predictor can run. Exercise the generated script.
Completed failed transition discovery invalidates old hints even if another
issue subsequently cancels the command; canceled or unstarted reads do not.

Native cache acceptance uses descriptor-relative readers matching Windows delete
sharing, with independent wrapper decoding; see `.claude/rules/tests.md`.

Credential resolution carries command context through the store and lazy
client/cache factories. Keep `config.Load` local and use `config.Resolve` for
credentials; do not store contexts in options or adapters. Follow `.claude/rules/go.md`
for `command.OnceContextValues` and the synchronous OS-keyring limitation.

Cancellation must not become an unavailable credential layer that an environment
token can override. Refuse canceled native keyring calls before entering them;
already-started synchronous writes retain their actual outcome. Resolve public
transition-hint cache scope before remote work so completed failed observations
and attempted writes invalidate stale hints after later cancellation without
reopening credentials or detaching context.

Direct 1Password uses the official SDK and current flat `secret_backend` settings.
Keep initialization lazy, service tokens environment-only, selected-store
provenance and site/account binding intact. Manage only stable owned credential
field ID; never delete items or infer ownership from titles/tags. Follow
`.claude/rules/config.md` for SDK initialization, cancellation and platform limits.
Legacy migration/rollback remains a separate delivery requirement.
