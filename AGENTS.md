# Jira CLI development

Agent-first Jira Cloud CLI for shell-resident coding agents: zero-install,
pipeable and composable, with native Jira data and explicit outcomes.
Build on the v0.16 shared owners.

## Read the applicable standards

Start with [.claude/rules/project.md](.claude/rules/project.md) for the architecture
and development workflow, then read the owners relevant to the task below.
These files apply to all coding agents. Codex must open the relevant files;
Claude path scopes and tool names do not configure Codex.

| Work | Required owner rules |
|------|----------------------|
| Go implementation and dependencies | [go.md](.claude/rules/go.md), [style.md](.claude/rules/style.md) |
| Commands, flags, native input, ADF authoring and completion | [commands.md](.claude/rules/commands.md) |
| Envelopes, output modes, human rendering, pagination and error taxonomy | [output.md](.claude/rules/output.md) |
| Configuration, auth, preferences, caches, migration and recovery | [config.md](.claude/rules/config.md) |
| Credentials, HTTP write policy, untrusted content and process/path boundaries | [security.md](.claude/rules/security.md) |
| Interactive input, editors and confirmation | [prompts.md](.claude/rules/prompts.md) |
| Tests, native hosts, owned fixtures and live/differential acceptance | [tests.md](.claude/rules/tests.md) plus the affected domain rules |
| User-facing changes and release notes | [changelog.md](.claude/rules/changelog.md) |

Follow current source and documented contracts when historical paths conflict,
and repair the relevant rule in the same implementation slice. Keep this
entrypoint below 8 KiB and under 200 lines; detailed rules belong to their
scoped owner. CLAUDE.md imports this file rather than maintaining a second copy.

## Shared boundaries

*   Commands use `command.Spec`, typed envelopes and shared View/sinks. Extend
    those owners instead of adding command-specific output or policy bypasses.
*   Keep native Jira fields, absent/null/zero distinctions, and unknown ADF bytes.
    An ADF edit round trip must use the preservation owner and its anchored sessions.
*   A fresh read or three-way merge does not supply remote compare-and-swap. Never
    claim lost-update protection without evidence of a server-enforced condition.
*   Help, schemas and shell completion remain local and credential-free. Declare
    completion behavior through command metadata and shared offline predictors.
*   Eligible transition hints belong to an issue, site and account, expire after
    five minutes, and never authorize a write. Preserve read timestamps and
    invalidate known outdated hints. Native input paths are context, not files to read.
*   The composition root registers the licensed native Bash/PowerShell/Zsh/Fish/Elvish generator backports
    in `internal/cli/completion/native` before exposing generators. Use clib's model
    and registration boundary; do not rewrite emitted shell scripts or add a local
    module replacement that breaks versioned `go install`.
*   Resolve one named profile per invocation, preserving credential/site binding
    and secret-free files, logs, envelopes and argv. Migration retains every
    selected profile and recoverable preimages. Flat files remain readable.
*   Exercise actual generated shell scripts and named failure paths. Cross-compilation
    is not native Windows/macOS behavior evidence; keep those acceptance claims separate.
*   User-facing changes include help/guides, schemas, applicable rules and changie
    fragments. Run appropriate normal, race, matrix and output checks and make
    focused conventional commits with normal hooks. Private session notes stay local.

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

## Development and verification

Development tasks use native mise discovery and Usage metadata. Keep parsed
values in quoted `usage_*` variables, and test ordering/refusal paths through
`mise run test:tasks`. Python tooling runs through uv on Python 3.14.

*   `mise run check`: formatting checks, lint, Markdown lint, vet and untagged tests.
*   `mise run ci`: tidy, build, matrix, output audit, check and race in order;
    then hermetic live guards, hook/release/task checks, workflow lint and security.
*   `mise run audit`: the hermetic output-conformance matrix against the real binary.
*   `mise exec -- go generate ./internal/jira ./internal/adf`: regenerate from
    vendored pins; keep generated source and freshness checks aligned.
*   `mise run upstream-check --type adf --check` (or `--type api`): inspect
    upstream drift without bumping pins.

Ordinary development tasks inherit Go/environment CGO defaults. Release builds
set `CGO_ENABLED=0`; the race task sets `CGO_ENABLED=1`. Run checks appropriate
to the change and report actual failures/skips. Explicitly include these
instruction files when linting them: the ordinary rumdl configuration excludes them.

The embedded guides and runtime schema (`jira agent guide`, `jira agent schema`)
are the authoritative CLI behavior sources. Keep user workflows there and update
them with behavior changes. Instruction-only corrections do not change CLI behavior.

## Delivery

Make focused, signed Conventional Commits with normal hooks and a curated linear
history when committing. User-facing feat/fix changes need a changie fragment;
follow the changelog rule for the non-user-facing exception. Never bypass hooks.
Private session notes stay local.

Selected source and executed checks establish facts. Do not publish, push or
release without the user's authorization.
