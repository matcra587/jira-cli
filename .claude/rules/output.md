---
description: >
  The output contract and its machinery: mode resolution, the JSON
  envelope, the clog printer path, error mapping and exit codes, streams,
  and the keyed-results pattern.
paths:
  - "internal/cli/**/*.go"
  - "cmd/jira/*.go"
---

# Output & Logging

The authoritative implementation rules for the output machinery. Sibling
files link here: [commands.md](commands.md) (command-side usage),
[style.md](style.md) (the lint bans backing this file),
[project.md](project.md) (architecture map). The user-facing contract itself
is specified in the embedded guide (`jira agent guide core-contract`) — do
not duplicate it here; change it there in the same PR.

## Libraries

Three write paths; clog owns two of them.

*   **Machine** (`json`/`compact`) — `internal/output`'s `encodeJSON` uses
    `jira.MarshalNoEscape`, appends one newline and checks the complete write,
    including short writes. Preserve native raw JSON escape spelling and
    literal HTML/line-separator text; a log renderer must never sit in this path.
*   **Human data** (stdout) — `output.View`, pinned to ONE `clog.Output`.
*   **Human status, errors, warnings** (stderr) — clog's logger.

The colour-mode decision is made ONCE, in `internal/cli`'s
`ResolveColorMode` (color.go) — not by clog. It folds `--color` over TTY
state, using `clog.NewOutput(...).IsTTY()` as one input, and mirrors
clog's own `NO_COLOR`/`TERM=dumb` checks itself because the pure value
sinks style through lipgloss with no Output in reach to ask. clog is a
detection primitive here, not the owner. The result is handed to both the
View and the sinks, so every path still obeys one decision.

Do not "fix" the asymmetry: data an agent parses never touches a renderer,
data a human reads always does. TTY
detection comes from `x/terminal`; ANSI strip/width/truncate from `x/ansi`;
flag metadata, themed help, and completions from `clib`, only through
`internal/cli/command`. Do not hand-roll any of those.

## Mode resolution

The published selector is `--output` (`auto` | `human` | `json` | `compact`).
Hidden compatibility aliases `--json`, `--plain`, `--human` and `--compact`
remain functional so familiar human and agent invocations work. Keep their
registration and resolution on the shared `cli.OutputAliases` table; do not
advertise a second canonical selector or remove supported aliases. `--raw`
is not an output alias. `Detect` (`internal/cli/detector.go`) resolves `auto` once: detected
agent env → `compact`, non-TTY stdout → `json`, TTY → human.

## The envelope

`{ok, meta{command, timestamp, request_id, exit_code?, pagination?}, data,
errors[], warnings[]}` — written by `command.WriteEnvelope` only.

IMPORTANT: JSON envelopes must be byte-clean. Machine output goes to stdout
through `command.WriteEnvelope`; never a raw `json.Encoder` in command code.
`internal/output`'s `encodeJSON` is the ONE sanctioned encoder — it turns
HTML escaping off and restores the two line-separator escapes
(`U+2028`/`U+2029`) `encoding/json` forces even so.
In `json`/`compact` modes nothing else may write to stdout.

## Write ownership

The output path is command → `command` → `cli` → Cobra's `io.Writer`.
A command built through the construction path returns DATA; the path owns the envelope write. A hand-written command uses `RunE` and must return every `command.Write*Envelope` result — a destination failure is the command's failure.
`command` chooses the machine or human renderer and passes
`cmd.OutOrStdout()`/`cmd.ErrOrStderr()` down. `cli` owns rendering and tracks
the first destination failure across Clog's void finalisers. Never write
command output through `os.Stdout` or `os.Stderr`.

Dynamic completion candidates use the same injected root writer and report a
failed candidate write after Clib dispatch. Clib's upstream
`--print-completion` action remains the sole deliberate process-stdout
boundary.

Destination failures are `output_write_failed`, `type=io`, exit 8 and
non-retryable. A Jira mutation may already have completed, so never retry it
solely because its result could not be written. If a command error and its
error renderer both fail, root keeps the command error primary and joins the
write failure as secondary context; it does not try the failed stream again.

## Envelope data conventions (contract v3.2)

*   **Issue identity is always an object.** Any issue-scoped `data.issue`
    (and link create's `inward_issue`/`outward_issue`) is a
    `command.IssueRef` — `{id?, key, self?}` — never a bare key string, and
    never an ad-hoc `data.key`. `IssueRef.String()` renders the key for
    human/plain output; richer objects (`issue view`, create's POST echo)
    satisfy the same minimum by carrying `key` at the same place.
*   **Each subcommand owns an extensible data object.** A new resource or
    subcommand gets its own registered Output struct; adding its fields must
    not widen a sibling operation. Fixed members and nested identities are
    concrete structs. Tenant-defined Jira field/update maps and recursive or
    genuinely polymorphic ADF values are the reviewed dynamic boundary.
*   **Every mutation carries `dry_run` on both paths** — `true` on preview,
    `false` on the live write — including no-profile and `--no-readback`
    variants. Validated request context and validation-status facts survive
    the live write; server-created objects, readbacks, verification results
    and file-write outcomes remain conditional.
*   **Shared keyed-result output changes placement, not operation shape.**
    The operation object appears at `data` for one key and
    `data.results[].data` for each successful key; a failed key carries
    `error` in that slot's place and never a half-filled operation object.
    Preserve the documented exceptions: multi-key `issue view` uses
    `data.results[].issue`, and keyed pagination or warnings are folded into
    the child operation object.
*   **A new or changed `data` field must land in the registered Output struct
    in the same change.** Add a registration doc override when its conditional
    meaning or outer schema cannot be expressed by Go tags alone. Schema
    derivation and registration checks live in `internal/envelope/schema_test.go`
    and `registry_test.go`; public discovery checks live in
    `tests/contract/agent_schema_test.go`. Extend the affected operation's
    binary contract tests to bind emitted fields, identity and retained receipts
    to the published shape. Discovery alone does not validate every live payload.
*   **Typed Output structs are the law, not a direction.** Every operation
    has an exported Output struct in `internal/envelope`, registered beside
    its definition; the published schema derives from the struct
    (`envelope.SchemaOf`), prose rides the registration's doc overrides,
    and builders emit the struct on every path. `command.New` refuses
    unregistered operations and mismatched result types; its named
    `TestNewRejectsAnUnregisteredOp` and `TestNewRejectsAMismatchedOutputType`
    guards live in `internal/cli/command/command_test.go`.
    `untypedEnvelopeData` bans map literals at envelope write sites.
    Genuinely shapeless payloads register `envelope.Dynamic` with a reason;
    keep that inventory explicit in review and do not regress fixed members
    to maps or `any`.

*   Envelopes render through `internal/output`'s `encodeJSON`, selected by
    `Render`'s format dispatch (`render.go`). The write tracker captures
    broken-pipe/quota failures and nil short writes that Clog finalisers would
    otherwise hide — the reason raw encoders are banned.
*   Error envelopes go to **stdout** (success and failure alike), even under
    `compact`; human diagnostics go to **stderr** through clog.
*   Warnings carry a taxonomy (`unknown_adf_node`, `lossy_adf_conversion`,
    `rate_limit_near`, …) — reuse existing codes before minting new ones.

## Human output

*   Root wiring (`internal/cli/root/root.go`): clog is pinned to **stderr**
    with `SetEnvPrefix("JIRA")` and colour from `--color`; a context logger is
    seeded so command code uses `clog.Ctx(ctx)`. `quietLevelLabels()` drops
    `PartLevel` so no `ERR`/`INF` column reaches a user — severity is the
    envelope's `errors[].code`, not a prefix in prose.
*   **One human renderer per command, and one emitter for all of them.** A
    command's `Human` func receives an `*output.View` and renders through its
    primitives (`Fields`, `Line`, `Block`, `Section`, `output.Table`). There is
    no per-command renderer package: the 0.15 `plain_*.go` sprawl is exactly
    what this replaced, and a renderer that writes to a stream directly is a
    guardrail failure (`tests/guardrails/human_view_primitives.go`, audit axis
    H1), not a style preference.
*   **Values reach the terminal only through a sink** (`internal/cli/sinks.go`:
    `Text`, `Status`, `Priority`, `PriorityIcon`, `Entity`/`EntityAs`, `Label`,
    `Time`, `Number`, `IssueKey`, `Hint`, `OrDash`). Sinks are pure — value in,
    styled string out, no I/O — so colour is decided at render and never stored
    in data. Every Jira-authored string is sanitised by its sink before it can
    reach a terminal (axis H4).
*   **The View may use clog's message path; it is not off-limits.** Prose and
    structure — hints, and a `Tree` for parent/child collections such as
    subtasks and linked issues — belong on that path, because that is where
    clog's backtick styling, wrapping and sibling connectors live, and
    re-deriving any of them here is the hand-rolling the gechr-first rule
    exists to prevent. Pre-laid-out rows do NOT: a table row or a `Fields`
    block has already had its widths, padding and ANSI computed, and running it
    back through message formatting would re-wrap measured columns and style a
    backtick inside a summary as inline code. Configure the View's own logger
    (parts, symbols, wrap) where the View is built — per-logger, never
    `clog.SetStyles`/`SetSymbols` globally, since `Logger.SetStyles` merges and
    leaves the rest adapting.
*   Blocking Jira calls render a spinner (`command.Spin`); it is `Silent`-
    finalised and non-TTY-silent, so a pipe sees nothing.
*   **A dry-run speaks in preview tense on every human surface** (axis H2) — it
    must never claim the mutation happened, on the completion line, the spinner
    label or any progress output. A dry-run step performing a genuine read is
    the one exception: that read really happens, so it speaks in the present.
*   **Human output carries no escape a caller did not ask for.** With styling
    off (`NO_COLOR`, `--color=never`, a pipe) a value renders as its plain text
    — no colour, no pill padding, no Nerd Font glyph, no trailing whitespace
    (axes H3/H4). Styled spans must not nest: a pre-styled string handed to a
    styling primitive strands a reset mid-span (axis H5) — colour the whole
    string in one `Render` call, as `EntityAs` does for a divider title.
*   The audit enforces all of this per command, per axis
    (`tests/audit/`), so a new command inherits the guarantees rather than
    re-proving them.

## Errors and exit codes

All user-visible failures map through `internal/cli/errors.go`:

*   `MapError` chains `errors.As` adapters for every typed error (CLIInput,
    Prompt, context cancel/deadline, Credential, `jira.APIError`, issuekey,
    board candidates, ambiguous-user). The terminal fallback classifies any
    untyped error as validation (exit 3) without inspecting its message —
    every non-validation class (auth/not_found/rate_limit/server) is typed
    at its source via an adapter or `cli.CodedError`. Never match `err.Error()`
    substrings to pick a class; type the error where it is built.
*   **Never synthesize a fake HTTP status on a hand-built `APIError` to borrow
    a transport class.** A miss the CLI computes locally — a `--type` value
    absent from the fetched issuetypes list, a name not in a cached set — is
    **validation (exit 3)**, typed at its source with a dedicated error
    (`core.IssueTypeResolutionError`), not a `&APIError{StatusCode: 404}` that
    would drag it into not-found (exit 2). Real 404s come only from the
    transport. `internal/cli/errors_test.go` exercises these distinctions in
    `TestMapError`; inspect new error construction sites during review too.
*   Stable snake_case codes with a `hint` that adds action beyond
    `message`; Jira HTTP statuses map to `jira_*` codes and hints in
    lockstep tables guarded by a test.
*   **Writing a hint.** A hint is the next action in the plain words a person
    would use — not a recap of `message`. Say what to do, not what failed. It
    serves both a human (it renders as clog's 💡 line under the error on
    stderr) and an agent (the envelope `hint`), so avoid envelope-only jargon
    like `candidates[]`; when the fix needs a value the caller doesn't have
    yet, name the command that surfaces it (`jira user search`,
    `jira boards list`). Every hint is static — defined in the
    `internal/cli/errors.go` registry keyed by code — and every code carries a
    non-empty one (`TestRegistryIsComplete` enforces it). A hint never interpolates
    a runtime value: if a specific matters (the offending value, a "did you
    mean" suggestion), it belongs in the error `message` or a structured
    envelope field (`field`, `suggestions[]`), never baked into the hint. If
    two error types want different hints, give them different codes rather
    than one code with per-instance text. A **catch-all or multi-resource
    code** must keep its hint resource-neutral: `jira_not_found` maps every
    Jira 404 (issue, board, attachment, user, comment, link, project), so its
    hint says "the identifier", never "the issue key" — naming one resource
    misleads on all the others; put the resource specifics in the `message`.
    Check jargon and resource neutrality during review; registry completeness
    does not establish either prose property. Watch the reuse trap in the other
    direction too: `arg_value_invalid`'s hint sends the
    caller to `--help`, so only use it where `--help` actually lists the
    accepted values. A lookup whose valid values live elsewhere — a saved-query
    name in `queries_path`, an issue type on a project's create screen — needs
    its own code (`saved_query_unknown`, `issue_type_unknown`) with a hint that
    points at the real source, and offers the values via `suggestions[]`.
*   Exit codes are a published contract: `0` ok, `1` auth, `2` not-found,
    `3` validation, `4` rate-limit, `5` server, `6` canceled, `7` timeout,
    `8` local output failure. `main.go` owns process exit and maps
    `ErrCompletionHandled` → 0.

## Keyed results (multi-key commands)

A `KEY…` command returns ordered `data.results[]` — one entry per key the
caller named, **in the order they named them**, never in completion order.
The frame is `envelope.KeyedResults[T]`/`KeyedResult[T]`
(`internal/envelope/keyed.go`); each entry carries the key's own `issue`
ref, an always-serialized `ok`, and exactly one of `data` (the operation
object) or `error` (`code`/`message`/`hint`, the same taxonomy vocabulary
`errors[]` uses). `issue.link.list` is the worked example.

**A failed key is a partial result, not a command failure.** If any key
succeeded, the envelope is `ok: true`, exit 0, and the failures ride inline
in their own entries — throwing away real data because one key was
mistyped serves nobody. If **no** key succeeded there is no partial result
to report, so the command fails with the most severe per-key error
*itself* (`command.KeyedFailure`), keeping its class, exit code and
transport facts — exactly what the same single bad key would have produced
alone. Severity is an explicit ladder over taxonomy codes
(`command.FailureSeverity`), not the numeric exit code, which is an
enumeration rather than a scale.

The registrar and the declaration are cross-checked: a command declaring
`Keyed` must call `command.AddKeyedFlags` and vice versa — `command.New`
refuses either half alone. `--parallelism` (1–16) bounds the fan-out, which
writes `results[i]` by index; a verb whose keys must execute in order opts
out explicitly with `WithoutParallelism(reason)` rather than by omission.

Follow this pattern for any new multi-key verb.

**Keyed pagination follows the resource endpoint.** `worklog list` is the
current example: `--start` and `--limit` apply independently to every issue,
while `--all` drains each issue within the published page/record bounds and
conflicts with explicit start/limit. The per-issue operation at
`data.results[].data` retains pagination and completeness facts, including
validated earlier pages when a later read fails. Keep these facts per issue,
never in a shared `meta.pagination` block. See `internal/cli/worklog/list.go`.

Issue links and attachments are unpaginated collections in this layout.
`issue attachment list` has no `--limit`/`--all`; the legacy flags sliced an
already-fetched array. Do not add page-shaped flags to an endpoint that has
no page, or confuse a local output window with a complete server walk.

## Enforced vs advisory

CI fails on: raw pflag declarations outside command (`rawPflagDeclaration`),
unwrapped blocking Jira calls (`unwrappedBlockingCall`), `log`/`log/slog`
imports (depguard), direct process-stream references in command packages,
discarded output-helper results and `Run` handlers that cannot return errors.
The last three are path-aware AST guardrails rather than ruleguard rules;
`errcheck` separately enforces returned-error handling. Full lint tables are
in [style.md](style.md).
