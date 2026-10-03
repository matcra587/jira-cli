---
description: >
  The cobra command pattern for jira-cli: NewCommand wiring, command.Add*
  flag metadata, gates (dry-run/read-only/ADF mode), envelope output, help
  text house style, and command-level review points and gotchas.
paths:
  - "internal/cli/**/*.go"
---

# Commands

The pattern for every cobra command. For the output contract see
[output.md](output.md); for config resolution see [config.md](config.md);
for the architecture map see [project.md](project.md); for interactive
prompts and confirmations see [prompts.md](prompts.md).

## Pattern

A command is built through ONE construction path. It declares what it is,
and returns data. It never writes.

```go
func newView(deps Deps) *cobra.Command {
    var fields []string

    return command.MustNew(command.Spec[envelope.IssueViewOutput]{
        Op:    "issue.view",                    // and its class flags, flat — see below
        Use:   "view ISSUE_KEY",
        Short: "Show one issue",                // mandatory — the help standard gates it
        Args:  command.IssueKeyArg,             // arity + hardening + predictor
        Flags: func(c *cobra.Command) {         // the ONLY per-command surface
            command.AddBoolVar(c, &all, "all", false, "...", command.FlagOpts{})
        },

        Human: renderIssueView,

        Validate: func() error { ... },         // reject before any I/O
        Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueViewOutput, error) {
            client, err := deps.JiraClient()    // deps by CLOSURE, not on Invocation
            ...
            issue, err := client.GetIssue(ctx, inv.Args[0], fields)
            ...
        },
    })
}
```

*   **`Run` returns typed data and has no writer in scope.** That is the
    whole point, and it is a property of the type rather than a discipline
    anyone has to remember: a handler that cannot reach a stream cannot
    bypass the envelope. `--output`, `--jq`, the theme and the colouring
    all follow from the returned value. A command that reaches for
    `fmt.Println` fails the build.
*   **`New` owns everything a command used to have to remember**: `SetMeta`
    from `Op`, the human renderer from `Human`, the envelope write from the
    returned value, and argument hardening from `Args`. Do not do any of
    these by hand — if the path cannot express something, fix the path.
*   **The operation is declared in FLAT fields, not a nested `Meta`.**
    `Op` sits with `Use`/`Short`; the class flags (`WritesJira`,
    `MutatesLocalState`), the shape flags (`Identity`, `Keyed`,
    `Passthrough`) and `NoSuccessEnvelope` are top-level too. `New`
    assembles the internal `Meta` from them — one declaration site, one
    assembler, no `Meta: command.Meta{...}` stutter and no second place
    for the two to drift. This is the cobra idiom: kubectl and gh keep
    command config flat, never a metadata sub-object. The `Meta` type
    still surfaces in one place, `Variants []Meta`.
*   **`Short`, `Long` and `Example` are Spec fields.** They are the whole
    surface the help/completion standard governs, and an empty `Short` is
    a gated bug class. Never mutate the returned `*cobra.Command` to add
    them — that reopens the hole the path exists to close.
*   **`MustNew` panics, `New` returns the error.** Both exist so the
    failure path is testable without `recover()`; the stdlib pairs `Must`
    with a non-panicking sibling for the same reason. Call sites use
    `MustNew`, tests use `New`.
*   **`Op` and `T` are checked against the registry at CONSTRUCTION.**
    Registration is a runtime map keyed by the operation string
    (`internal/envelope/registry.go`), so the language cannot check it: an
    op with no registration, or a registration whose sample type is not
    `T`, panics while the command tree is being built. That is every run
    and every test, before any command executes, and the audit inventory
    builds the whole tree — so CI sees it, not a user. Compile-time was
    claimed here once and was not true; do not restate it.
*   **Only three things are per-command**: the flags, the data shape, and
    the handler body. `issue list` and `issue create` differ from
    `issue view` in those and nothing else. If a conversion needs a fourth,
    that is a finding about the path, not a licence to go around it.
*   **`Validate` runs before `Run`, and that separation is the point.**
    `Validate` rejects bad input BEFORE any I/O happens, so a request
    that cannot succeed never reaches Jira and never pays for a client. A
    check living inside `Run` has already bought what it is about to
    throw away.

    There is deliberately no `Complete` phase yet. kubectl splits
    `Complete` (turn flags and args into concrete inputs) from
    `Validate`, and that split earns its keep once a command has enough
    inputs to be worth assembling separately. Nothing here does yet —
    the handler reads `inv.Args` and its closed-over flags directly. Add
    it when a conversion is genuinely harder without it, not before.
*   **Define and register the command's Output struct in
    `internal/envelope`** before wiring anything. Fixed nested objects use
    named structs. Keep maps only for tenant-defined Jira fields, recursive
    ADF, or a documented polymorphic boundary; a genuinely shapeless
    top-level output registers `envelope.Dynamic` with a specific reason.
    Shared single-key and keyed-result builders emit the same operation
    object, at `data` or `data.results[].data` respectively. Preserve the
    documented `issue view` and keyed pagination/warning exceptions.
*   **Declare arity through `Args`.** An `ArgSpec` carries the arity
    validator AND the positional completion predictor together, so the
    path writes both from one declaration — previously the `dynamic-args`
    annotation was a second, separate thing that had to agree with the
    validator, and nothing checked it did.
*   **An `ArgSpec` STORES cobra's validator; it must never wrap it.**
    Two separate mechanisms read a validator back out, and both break on
    a wrapper however transparent it is:
    1.  `root.isNoArgs` compares against `cobra.NoArgs` by FUNCTION
        POINTER to tell a mistyped subcommand from an argument miscount.
        A wrapper fails that comparison, and every runnable parent
        silently reports a miscount for a bad verb, losing the field that
        named it. Pinned from outside the binary by
        `tests/contract/stray_positional_test.go`.
    2.  clib recovers a command's positional limit by calling the
        validator with 0..32 dummy arguments and regex-matching cobra's
        own error wording. A bespoke message loses shell completion's
        `MaxPositionalArgs`, and `MinimumNArgs`'s wording already matches
        none of those patterns, so variadic commands fall back to parsing
        `Use`.
    Storing preserves identity — verified against cobra v1.10.2: the
    literal holds, a struct field handed back holds, a transparent
    wrapper breaks, and `ExactArgs(0)` breaks despite identical
    behaviour. So hold the validator in one field and the predictor in a
    sibling. Where hardening is needed, it runs AFTER the arity check and
    returns cobra's error untouched.

*   **Blocking Jira calls run behind a clog spinner/progress** — the
    `unwrappedBlockingCall` ruleguard rule fails CI otherwise. Multi-key
    work goes through the fanout executor (`command/fanout.go`) and returns
    keyed results (see [output.md](output.md)).

**Dependencies arrive by CLOSURE, not on `Invocation`.** A command's constructor
takes its domain's `Deps` struct and the handler closes over it, exactly
as `newList(deps)` and `newStatus(deps)` already do. That is what makes
`config list` structurally unable to open a Jira client: `config.Deps`
has one field, `Credentials`, so there is no client to reach for — the
property exists today, without generics, and a type parameter on `Spec`
would only restate it at the cost of a second type argument on every
literal. A shared context object carrying every capability is the container §8.1
forbids; do not add one.

`Invocation` carries only what belongs to this run — stdin, the resolved
dry-run flag, an interactivity probe, the resolved version string. Facts
about this run, not capabilities. The test for anything new: if reading
it lets a command DO something, it is a dependency and belongs in the
closure; if it is inert data, it belongs on `Invocation`.

**`ctx context.Context` is the first parameter, never a field.**
`.golangci.yml` enables `containedctx`, which flags a context field AND
the embedded-interface form — verified by running the repo's own pinned
linter. Handlers stop reading `cmd.Context()`; the path reads it once and
passes it down.

**A command whose output IS the bytes stays hand-written.** `auth token`
prints a bare token so `$(jira auth token)` works, and `completion`
prints a shell script. `Run` returning `(T, error)` cannot express
either, and there is deliberately no `MustNewRaw` sibling: naming the
escape hatch puts it in the brochure, and "why isn't this `MustNew`" is a
far softer review question than "why did you hand-write a
`cobra.Command`". Two commands need this. They declare a non-empty
`Meta.NoSuccessEnvelope`, which the inventory already binds to the
absence of a schema registration.

**Status.** The path lands with the `version` conversion and is then proven
against `config list` and `auth status`/`logout` before `issue view` is
written in it. A command predating the conversion is legacy: convert it,
do not copy it. Conversions must be behaviour-identical — the audit matrix
and the command's existing tests are the equivalence harness, and output
that changes is a bug in the path.

## Flags: metadata is mandatory

Raw `pflag` declarations are banned outside `command` (ruleguard
`rawPflagDeclaration`). Declare flags with the `command.Add*` helpers
(`AddStringVar`, `AddBoolP`, …), which register the flag and attach its
`clib.FlagExtra` in one call:

```go
command.AddStringVar(cmd.Flags(), &opts.Query, "query", "",
    "Filter results by query", clib.FlagExtra{
        Group:       "Filters",           // must exist in help.go flagGroupRank
        Terse:       "query filter",      // short label for completions
        Placeholder: "text",
        Complete:    "predictor=query",   // dynamic completion (completion.go)
    })
```

*   Prefer the semantic bundlers for recurring clusters: `AddDryRunFlag`,
    `ExtendPaginationFlags`, `AddIssueColumnFlags`, …; reach for generic
    helpers only for one-offs.
*   Enum flags carry `Enum`/`EnumTerse`/`EnumDefault` so help renders the
    values and completion offers them.
*   A `Group` not listed in `flagGroupRank` (`command/help.go`) renders near
    the bottom as "obviously unranked" — add new groups to the rank map
    deliberately.
*   Completion predictors attach through the same flag/command metadata — see
    Completion below.

## Completion

Shell completion is clib-driven. Two ways to attach it:

*   **Flag value** — `clib.FlagExtra{Complete: "predictor=cacheproject"}`; a
    repeatable/comma flag adds the modifier: `"predictor=cachefield,comma"`.
*   **Positional args** — the command's clib annotation:
    `Annotations: map[string]string{"clib": "dynamic-args='issuekey'"}` (a
    positional CSV — arg 1's predictor, arg 2's, …). A shared package var is the
    idiom where many commands repeat one (`issueKeyArg`); file-path args use
    `"clib": "complete=path"`.

**Nothing is wired yet.** `completionKinds` in
`internal/cli/root/complete.go` is an EMPTY map, no command declares
`dynamic-args`, and `jira __complete <cmd>` reports an unknown command.
The rules below are the contract to build against, not a description of
what runs — the 0.15 tree has sixteen predictor kinds and twenty-one
declaration sites, and it is the port target.

That map is the single source of truth once it exists: keys are the
predictor names, values emit the candidates. A declared name with no
entry ships a silently-empty completion, which
`TestDeclaredPredictorsAreHandled` fails on. Adding a predictor is one
emitter func plus one map entry, and an `ArgSpec` may name a predictor
only in the change that registers its kind.

*   Emitters run **outside `PersistentPreRunE`** (see Gotchas) — load config
    yourself via `config.Load`, stay **null-safe** (emit nothing on a
    missing/broken cache, never block the shell), and **sanitize** every
    candidate through `cli.SanitizeCompletionField` (Jira text is untrusted).
    Write only to the injected emitter writer and return write failures.
*   **Enum flags self-complete** from `Enum`/`EnumTerse` — never wire a predictor
    for one. `FlagExtra.Hint` (`file`/`dir`/`user`/`host`/`url`/…) gives
    OS-level completion — `user`/`host` mean the OS's, not Jira's.
*   `issuekey` completes from the per-profile recently-used key cache
    (`internal/cache` `IssueKeysResource`), written as a side effect of
    commands that touch keys (`command.RecordIssueKeys`/`RecordIssuesSeen`);
    a profile with no recorded keys emits nothing.
*   User-identifier flags (`--assignee`, `--reporter`, `--user`) complete only
    the `me`/`none` enum values today. Completing arbitrary Jira users needs a
    users cache resource that does not exist yet, so it is deferred — `jira user
    search` is the discovery path meanwhile.
*   clib is pinned in `go.mod`; `dynamic-args` / `predictor=` / `FlagExtra` are
    stable across versions, but bumping clib is a deliberate change ([go.md](go.md)).

Under the construction path the two attachment points differ:

*   **Flag predictors are unchanged** — `FlagExtra` rides the flag, declared
    inside `Spec.Flags` through the usual `command.Add*` helpers.
*   **Positional predictors come from the `ArgSpec`**, not a hand-written
    annotation. `New` sets `Annotations["clib"]` from it, so the live tree
    still carries what `TestDeclaredPredictorsAreHandled` walks; what
    changes is that arity and predictor can no longer disagree.
*   That guard catches a declared predictor with no emitter. It does NOT
    catch the inverse — a positional arg that should have had one and got
    none. With `ArgSpec` that becomes checkable (a bare cobra validator
    where a shared spec exists), and is worth a guard when the path lands.

## Gates

Use the shared command, configuration and transport owners; never resolve
policy by reading env/config ad hoc:

*   **Dry-run** — `--dry-run` is per-command (via `AddDryRunFlag`), local-only,
    and never contacts Jira. A handler reads it from `Invocation.DryRun`,
    resolved by the path from the flag it registered; it never parses the
    flag by name, and never binds a local of its own — `config edit` did
    exactly that during its conversion, so `--dry-run` read false and the
    preview edited the file.

    A mutation with no `--dry-run` flag **cannot be constructed**: `New`
    refuses any spec whose `Op`-class fields say it writes Jira or local
    state and that registers no preview flag. This replaces the older
    rule, which said a guard unable to read its flag should assume
    dry-run is ON. That was the right instinct and the weaker mechanism —
    refusing at construction means the mistake never reaches a tree
    rather than being caught after it shipped. A read is untouched: it
    has nothing to preview. `--dry-run` belongs on **every**
    mutation, not just Jira writes: the local-state ones (`config set`/`init`/
    `theme`, `alias set`/`delete`/`import`, `cache clear`/`refresh`) preview and
    write nothing under it, with `data.dry_run: true`. "Local-only" is literal —
    a dry run must not build a Jira client or require a credential, so resolve
    anything auth-dependent (e.g. a profile cache key) from config directly on
    the dry-run path (see `cache refresh`). Every human surface narrates a
    dry-run in preview tense — conditional completion lines, preview
    spinner/fanout/`--debug` lifecycles — see [output.md](output.md).
*   **Read-only** — `command.ReadOnlyEnabled`: `JIRA_READ_ONLY` env wins on the
    OFF→ON direction only; enforcement lives at the HTTP transport (see
    [security.md](security.md)).
*   **ADF mode** — `command.ADFModeFor(cmd, mutation)`: flags > env
    (`JIRA_ADF_STRICT`) > path default; mutations default strict, reads
    best-effort; conflicting inputs resolve to the safe default.
*   **Headless** — mutations require `--no-input` in agent/non-TTY context;
    destructive commands additionally require `--force`. Do not weaken either.
    This covers **local-state removals**, not just Jira writes: `cache clear`
    and `alias delete` gate the live run behind `--force` off a TTY (the
    `DetectorFromContext` + `NoInputRequested` headless check, refusing with
    `cli.InputForceRequired`), while upserts (`config set`/`init`/`theme`,
    `alias set`/`import`, `cache refresh`, `auth switch`) stay open. `--dry-run`
    is always allowed.
*   **Retry budget** — the composition root supplies `core.DefaultRetryPolicy`:
    at most three attempts, a 500 ms base exponential-backoff window, an 8 s
    cap on that window and a 30 s retry budget. A positive `Retry-After` plus
    jitter may exceed 8 s, provided the delay fits the remaining retry budget.
    Only classified safe reads retry; writes are never replayed.
    An existing request-context deadline can shorten the budget. `--timeout`,
    `JIRA_TIMEOUT` and the file's `timeout` resolve through configuration and
    bound each HTTP request; they are not a command-wide retry deadline.
    No `--max-retry-wait` or `JIRA_MAX_RETRY_WAIT` setting is registered.

## Help text (Short / Long / Example)

Help renders through clib (`command.NewHelpRenderer`); every command defines
`Short`, `Long`, and `Example` — **group parents included**. All three are
`Spec` fields, set at construction; never assigned onto the command
afterwards, which is the bypass the path exists to close.

What actually enforces this today, stated accurately because a rule
citing a guard that does not exist is the defect it is meant to prevent:
**only `Short`, and only through the inventory walk** — `tests/audit/`
`inventory.go` fails a command whose `Short` is empty, because an empty
Short drops it from grouped help (help standard §1.12). `Long` and
`Example` are checked by NOTHING. An earlier version of this section
named a `TestEveryCommandHasHelpText` in `internal/cli/root`; no such
test has ever existed here. House style, by field:

*   **`Long` → double-quoted string**, concatenated with `+` across source
    lines at word boundaries. clib wraps at render time — do not hand-wrap
    prose; use `\n\n` for real paragraph breaks. Backtick special tokens
    directly in the string: sibling commands as the full tree
    (`` `jira epic list` ``), the command's own flags bare (`` `--dry-run` ``),
    env vars and paths (`` `JIRA_READ_ONLY` ``). Agent-relevant behavior
    (what dry-run does, what happens with no profile) belongs in `Long` —
    agents read it via the schema.
*   **`Example` → raw backtick string** of literal shell lines, each command
    `$`-prefixed, with `#` comment lines introducing variants:

    ```go
    Example: `$ jira epic add PROJ-123 PROJ-100

    # Preview the assignment without contacting Jira
    $ jira epic add PROJ-123 PROJ-100 --dry-run`,
    ```

*   **Flag usage strings stay plain prose** — no backticks. Placeholders and
    enum values come from `FlagExtra` metadata (`Placeholder`, `Enum`), never
    repeated inside the usage text: clib already renders
    `--query <text>  [table, json, yaml]`.

## Review points

*   Verb registered in `verbs.go`; renderer in `registry.go` for rich output.
*   Output struct registration, guide section and schema description updated
    in the same PR as any behavior change.
*   Contract test covers the envelope; guardrail test for any new invariant.
*   Dry-run checked before expensive work and before early returns.
*   New flag group added to `flagGroupRank`.

`RunE`, command-stream ownership and output-helper propagation are AST
guardrails. The remaining points require review.

## Gotchas

*   **`completion` bypasses `PersistentPreRunE`** (root.go) — completion code
    paths cannot rely on config/logging setup having run.
*   **Completion never writes to stdout** except the raw candidates; candidates
    are sanitized against tab/newline injection before emission.
*   **Stop spinners before anything interactive** (editor, huh forms).
*   **Confirmations and prompts have their own rules** — when to ask, why a
    non-TTY or agent never gets one, what the question must name, and how
    the title is styled: [prompts.md](prompts.md).
*   **Bare `jira`** is contract-aware: TTY renders help, non-TTY/agent emits
    the JSON schema (root.go) — don't add root-level behavior that breaks this.
*   **`jira issue edit KEY` with no field flags** opens `$EDITOR`; agent/non-TTY
    context refuses with exit 3 and a remediation hint — preserve that guard
    when touching the editor flow.
