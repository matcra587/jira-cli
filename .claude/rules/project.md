---
description: >
  Architecture map and cross-cutting concerns: shared owners, command lifecycle,
  dependency construction, output, and development verification.
paths:
  - "internal/**/*.go"
  - "cmd/**/*.go"
  - "tests/**/*.go"
---

# Project

jira-cli is a Go/Cobra CLI for Jira Cloud. This file maps the v0.16 owners;
deep rules live in [commands.md](commands.md), [output.md](output.md),
[config.md](config.md), [security.md](security.md), [style.md](style.md),
[go.md](go.md), and [tests.md](tests.md).

## Shared owners

| Path | Role |
|------|------|
| `cmd/jira/` | signal context and process exit; calls `root.Run` |
| `internal/cli/root/` | Conductor/Cobra composition, lazy injected factories, aliases, completion dispatch, output selection and failure boundary |
| `internal/cli/command/` | typed `Spec`, invocation facts, validation, flag/argument metadata, variants, keyed work and envelope emission |
| `internal/cli/` | stable error classification, detection, theme policy and pure value sinks |
| `internal/cli/issue/` and other command domains | handlers and human layouts using injected capabilities |
| `internal/core/` | consumer interfaces, operation types and shared domain policies |
| `internal/adapters/` | concrete clock and site/account-scoped metadata cache implementations |
| `internal/adapters/jira/` | Jira client satisfying core interfaces, authenticated response admission and retry policy |
| `internal/adapters/keyring/`, `internal/adapters/onepassword/` | credential stores and provider-owned recovery |
| `internal/jira/` | native wire models, nullable fields, JSON admission and request encoding |
| `internal/config/` | named profiles, flat runtime settings, provenance, file publication and migration/recovery transactions |
| `internal/credentials/` | shared credential records and binding |
| `internal/adf/` | preservation, parsing, validation, rendering and anchored editing |
| `internal/issueedit/` | durable local description drafts; no Jira writing capability |
| `internal/nativepath/` | literal absolute paths and existing-object identity; callers own existence and publication policy |
| `internal/envelope/` | registered operation data shapes and derived schemas |
| `internal/output/` | envelope assembly, machine rendering, human View/table primitives and output failure tracking |
| `internal/tui/dashboard/` | issue home screen, Primer widgets and generation-tracked Jira reads; terminal streams stay in shared output |
| `internal/cli/completion/native/` | licensed generator backports registered at clib's model boundary |
| `internal/agentguides/`, `internal/cli/discovery/` | embedded docent guides and discovery from the assembled command tree |
| `internal/cli/agent/` | illustrative native field examples and ADF capability discovery |
| `internal/selfupdate/`, `internal/cli/update/` | installation/release owners and the public update command |
| `ruleguard/rules.go` | custom lint rules enforcing shared boundaries |
| `tests/` | unit, contract, integration, guardrail, flag-matrix, output-audit and opt-in live acceptance |

The module exports no public library API. Core declares consumer seams;
adapters implement them. The composition root supplies lazy constructors to
commands. Extend these owners rather than constructing clients, credential
stores or output policies independently inside handlers.

## Command lifecycle

1.  `cmd/jira/main.go` obtains Conductor's signal context and returns the exit
    code from `root.Run(ctx, args)`.
2.  Root assembles the real tree and runtime, applying native overrides to each
    completion generator. `root/complete.go` handles completion actions
    before Cobra parsing; a handled callback returns without running a command.
3.  Root seeds the failure output selector, expands configured aliases, applies
    their final global flags, and resolves the theme. Command-path checks and
    the shared failure sink cover errors before normal hooks can run.
4.  `PersistentPreRunE` resolves color, output mode and optional jq filtering,
    then publishes invocation context. Enabled hook traversal keeps child
    hooks from silently bypassing this boundary. Configuration and credential
    resolution for Jira remains in lazy factories.
5.  `command.Spec` owns argument/flag declarations and pure validation. Its
    handler receives invocation facts and injected capabilities, returns typed
    data, and never owns the result writer. The shared path emits success or
    retains an explicit failure receipt, including typed warnings and variants.
6.  Output assembly/rendering uses `internal/output` and registered envelope
    types. Human renderers use shared View primitives and value sinks. Root
    classifies failures into the published exit codes 0–8 and preserves output
    destination errors. See [output.md](output.md).

Bare `jira` uses the same typed root spec: human mode renders framework help;
machine modes return discovery of the assembled tree. Explicit selectors and
agent detection follow the shared output rules. Completion scripts are a
documented raw-output exception and emit identical bytes in every output mode.

## Cross-cutting concerns

*   **Mutations** use shared command flags, pure validation and core policies.
    Every mutation spec must declare `--dry-run`. Bare previews remain local;
    explicit `--validate-remote` may perform the documented remote reads. It
    never authorizes a preview write. Verification observes later state after
    acknowledgment; it does not provide server compare-and-swap.
*   **Native input** preserves unknown members and absent/null/zero distinctions.
    Requested-field preflight checks IDs and operations without converting
    values. `agent fieldtypes` examples are not a tenant/plugin encoder registry.
    Explicit local `issue input prepare` uses core's authoring codecs with a
    supplied metadata snapshot; it never changes the default mutation route.
*   **Config and credentials** follow [config.md](config.md): one flat runtime
    configuration resolved from the selected profile, separate public site/account identity and authenticated API scope,
    lazy providers and recoverable migration preimages.
*   **Metadata caching** uses core interfaces and adapters, scoped to public
    site/account identity. The cache command family and offline completion
    predictors share these owners; completion never resolves credentials.
*   **Lossless editing** uses ADF preservation and bound sessions plus durable
    issue drafts. A fresh read, merge or later verification is not evidence of
    server-enforced lost-update protection.
*   **Documentation** follows the real command tree and operation registry.
    Behavior changes update help, embedded guides, schemas, applicable rules
    and changie fragments together. Discovery is built at runtime; no
    `cmd/gen-docs` owner exists in this layout.
*   **TUI runtime** uses `command.Spec.Screen` through the shared output owner.
    The issue home screen saves explicit display preferences through the shared
    configuration file owner, reads/saves named queries through the query owner,
    and reuses issue-domain transition/edit/comment receipts for explicit reviewed
    actions. Its runtime schema declares interactive write capabilities. It does
    not consume legacy TUI preferences;
    explicit migration deferral retains those original declarations for later work.
    View switching never implicitly focuses JQL input. An unused Search stays
    idle until `/` or Enter; empty JQL is not submitted, including after reload.
*   **Versioning** uses clive's injected version and Go's embedded VCS metadata,
    exposed by `internal/cli/version/`. Build flags belong to
    `.mise/tasks/lib/build-env.sh`.

## Workflow

*   mise provisions the selected tools. `mise run build` creates the disposable
    binary under `dist/` with version flags. `mise run install` replaces the
    installed binary; use it only when installation is part of the task.
*   File tasks own native `# [MISE]`/`# [USAGE]` metadata; `tasks.toml` owns
    direct commands and ordered orchestration. Keep parsed values in quoted
    `usage_*` variables. Build through Go's cache without timestamp skipping,
    tidy before building, and keep acceptance builds, lint and race phases
    separate in CI. Finish formatting before Go lint fixes. Python tooling
    uses uv and Python 3.15; exercise `mise run test:tasks`.
*   `tasks.toml` defines `mise run check`, `test`, `test:race`, `test:matrix`,
    `audit`, `test:live:guards`, and the wider `ci` gate. Native platform gates
    have explicit host requirements. Cross-compilation does not prove native
    behavior; record unavailable gates and failures without claiming acceptance.
*   Make coherent Conventional Commits with normal hk hooks. `hk.pkl` defines
    formatting, lint, Markdown and secret checks. Check scoped rules explicitly
    when a tool's default exclusions omit them. Keep private evidence excluded.
*   Local release tags and CI share `.mise/tasks/lib/release-version.sh`.
    Prereleases must neither become stable latest nor update Homebrew/Scoop;
    guard packaging-token creation as well as publication. Keep the main-only
    release policy and exact-commit push CI gate. Exercise `test:release`.
*   Live acceptance uses `JIRA_LIVE_ACCEPTANCE=1`, a selected
    `JIRA_LIVE_BINARY`, and dedicated `JIRA_LIVE_BASE_URL`, `JIRA_LIVE_PROJECT`
    and `JIRA_LIVE_ISSUE_TYPE` fixtures. Follow [tests.md](tests.md) for ownership,
    manifests and independent cleanup checks. `test:live:guards` disables live
    writes and runs the hermetic ownership/cleanup guards.

## Task conventions

Development tasks use native mise discovery and Usage metadata. Keep parsed
values in quoted `usage_*` variables, and test ordering/refusal paths through
`mise run test:tasks`. Python tooling runs through uv on Python 3.15.

## Native representation and transport boundaries

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
    Bash/PowerShell/Zsh/Fish/Elvish generator repairs through `complete.WithShellGenerator`.
    The root configures each generator before exposing it. Reuse clib's public metadata and
    validation; do not rewrite printed scripts or break versioned `go install`
    with a module replacement. Fish and Elvish delegate to their library generators
    for models without current-word predictors. Nushell uses its library generator.
*   **internal/cli/cache/transitions.go** — bounded per-issue ID/name/destination
    hints from explicit successful workflow reads. The observed read starts the
    five-minute TTL; site/account scope remains adapter-owned. Completion reads
    local hints only and never uses cached availability as mutation permission.
