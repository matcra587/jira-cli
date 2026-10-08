# Jira CLI development

Build on the v0.16 shared owners. `CLAUDE.md` and `.claude/rules/` contain the
project standards for all coding agents; Claude-specific tool names do not
configure Codex. Follow current source and documented contracts when historical
paths conflict, and update the relevant rule in the same implementation slice.

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
*   The composition root registers the licensed native Bash/PowerShell/Zsh generator backports
    in `internal/cli/completion/native` before exposing generators. Use clib's model
    and registration boundary; do not rewrite emitted shell scripts or add a local
    module replacement that breaks versioned `go install`.
*   Resolve one named profile per invocation, preserving credential/site binding
    and secret-free files, logs, envelopes and argv. Flat files remain readable.
    Legacy migration and rollback remain separate delivery requirements.
*   Exercise actual generated shell scripts and named failure paths. Cross-compilation
    is not native Windows/macOS behavior evidence; keep those acceptance claims separate.
*   User-facing changes include help/guides, schemas, applicable rules and changie
    fragments. Run appropriate normal, race, matrix and output checks and make
    focused conventional commits with normal hooks. Private session notes stay local.

Selected source and executed checks establish facts. Do not publish, push or
release without the user's authorization.

Completion generation removes Cobra's combined boolean `h/help` spec when
Conductor supplies separate short and long help descriptions. Keep this repair
at the shared `WithGenerator` model boundary: duplicate options break Fish's
`argparse` dispatch before a predictor can run. Exercise the generated script.
Completed failed transition discovery invalidates old hints even if another
issue subsequently cancels the command; canceled or unstarted reads do not.

When testing concurrent cache publication, preserve native Windows delete-sharing
semantics in the independent reader; follow `.claude/rules/tests.md`.
