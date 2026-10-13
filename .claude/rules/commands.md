---
description: >
  The cobra command pattern for jira-cli: NewCommand wiring, command.Add*
  flag metadata, gates (dry-run/read-only/ADF mode), envelope output, help
  text house style, and command-level review points and gotchas.
paths:
  - "internal/cli/**/*.go"
---

# Commands

Typed boolean alternatives stay inside `command.Spec`: `Read` uses
`WhenBoolRead`, and `LocalWrite` uses `WhenBoolLocalWrite`. Each selects its own
registered output, validation, renderer, argument bounds and operation class.
Only one typed alternative is allowed on a path. Local writes require a boolean
`--dry-run` defaulting to false even when the default operation is a read.
Use `SelectedVariantMeta` for root failure attribution, including errors before
handlers. Discovery marks a command read-only only when every declared operation
is a read. Keep ordinary read shapes free of mutation fields.

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
        Args:  command.ArgSpec{Min: 1, Max: 1, Positions: []command.Arg{{Name: "ISSUE_KEY", Predictor: "issuekey"}}},
        Flags: func(c *cobra.Command) {         // the ONLY per-command surface
            command.AddBoolVar(c, &all, "all", false, "...", command.FlagOpts{})
        },

        Human: renderIssueView,

        Validate: func() error { ... },         // reject before any I/O
        Run: func(ctx context.Context, inv command.Invocation) (envelope.IssueViewOutput, error) {
            client, err := deps.JiraClient(ctx)    // deps by CLOSURE, not on Invocation
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
contains credential resolution, a scoped migration-store factory and confirmation
injection, but no Jira client. This property exists without generics; a type parameter on `Spec`
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
(`AddStringVar`, `AddBoolVar`, …), which take the owning command and
`command.FlagOpts`, register the flag and attach clib metadata in one call:

```go
command.AddStringVar(cmd, &opts.Filter, "filter", "",
    "Use a saved query", command.FlagOpts{
        Terse:       "Saved query",
        Placeholder: "name",
        Complete:    "predictor=savedquery",
    })
```

*   Prefer the semantic bundlers for recurring clusters: `AddDryRunFlag`,
    `AddListFlags`, `AddIssueFieldFlags`, …; reach for generic
    helpers only for one-offs.
*   Enum flags carry `Enum`/`EnumTerse`/`EnumDefault` so help renders the
    values and completion offers them.
*   `FlagOpts` owns shorthand, native file/directory hints and forwarding
    values to dynamic callbacks. Keep these declarations at the shared boundary.
*   Completion predictors attach through the same flag/command metadata — see
    Completion below.

## Completion

Shell completion is clib-driven. Two ways to attach it:

*   **Flag value** — `command.FlagOpts{Complete: "predictor=cacheproject"}`
    passed to a `command.Add*` helper; a repeatable/comma flag adds the
    modifier: `"predictor=cachefield,comma"`. Include the required `Terse`
    label alongside completion metadata.
*   **Positional args** — declare each position's `Name` and `Predictor` in
    `command.ArgSpec`. The construction path derives the clib annotation and
    argument validator together; do not hand-write an annotation in a handler.

`completionKinds` in `internal/cli/root/complete.go` is the dispatch owner:
keys are the
predictor names, values emit the candidates. A declared name with no
entry ships a silently-empty completion, which
`TestDeclaredPredictorsAreHandled` fails on. Adding a predictor is one
emitter func plus one map entry, and an `ArgSpec` may name a predictor
only in the change that registers its kind.

*   Emitters run before ordinary command parsing. Resolve the selected settings
    and public site/account scope through the completion owners; never open
    credentials or a Jira client. Missing, malformed or stale local data offers
    no candidates. Preserve identity, reject control-bearing names and sanitize
    descriptions with `cli.Text`. Emitters return candidate lines; the shared
    completion handler owns the stdout callback boundary.
*   **Enum flags self-complete** from `Enum`/`EnumTerse` — never wire a predictor
    for one. `FlagOpts.Hint` (`file`/`dir`/`user`/`host`/`url`/…) gives
    OS-level completion — `user`/`host` mean the OS's, not Jira's.
*   `issuekey` reads recent issue history from the site/account-scoped cache
    through `cache.RecentIssueKeys`. Mixed attachment and transition grammars use
    their contextual predictors, not a whole-command filesystem fallback. Empty
    history offers nothing. `jira user search` remains the explicit user lookup.
*   Transition target hints use per-issue observations from successful explicit
    `issue transition list` reads, never the global status catalog. Require fresh
    five-minute observations; reject malformed, future and cross-scope data.
    Multiple keys need a shared ID with identical known destination metadata.
    Native input paths are forwarded as context only; never read their contents
    during completion. Canceled discovery stops recording, completed failures
    invalidate old hints, and attempted transitions invalidate their snapshot.
*   clib is pinned in `go.mod`; `dynamic-args` / `predictor=` / `FlagExtra` are
    versioned APIs. Verify them against the selected source before changing the
    dependency ([go.md](go.md)).
*   The root registers reviewed native Bash/PowerShell/Zsh/Fish/Elvish backports once through clib's
    `complete.RegisterShell`, before exposing any generator. The owner is
    `internal/cli/completion/native`; it reuses the library's public model,
    validation and installation. Do not rewrite emitted scripts, lose other
    shells, import unfinished proposals, or add a module replacement that breaks
    versioned `go install`. Exercise generated scripts in real shells; a golden
    or a successful callback alone does not prove correct inserted values.
*   A `word_` predictor requests the current literal argument through the shared
    native transport. Its callback matches complete arguments and preserves CSV
    quoting through `command.CSVCompletion`; never split at the last comma.
    Attachment completion follows the actual `--file` grammar: without files,
    the first positional is an issue and subsequent positions are local paths;
    with files, all positionals are issue keys. Only `theme_path` configuration
    values use filesystem candidates. Directory enumeration remains bounded,
    literal and credential-free. Native quoting and insertion belong to the
    registered generator, not a command or an emitted-script rewrite.
    Theme-path suggestions become absolute paths so the selected file retains
    its identity when settings live in another directory. This does not rewrite
    manually authored relative configuration values.

Under the construction path the two attachment points differ:

*   **Flag predictors** — declare `FlagOpts` inside `Spec.Flags` through the
    usual `command.Add*` helpers; those helpers attach clib's `FlagExtra`.
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

*   **Dry-run** — `--dry-run` is per-command (via `AddDryRunFlag`). Ordinary
    mutation previews remain local-only and credential-free. A handler reads it from `Invocation.DryRun`,
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
    write nothing under it, with `data.dry_run: true`. For ordinary previews,
    "local-only" is literal: do not build a Jira client or require a credential. Resolve
    anything auth-dependent (e.g. a profile cache key) from config directly on
    the dry-run path (see `cache refresh`). Authentication previews
    (`auth login --dry-run`, `auth whoami --save --dry-run`) perform real Jira
    verification reads but publish no credentials or settings and create no
    files or directories. Explicit `--validate-remote` retains its documented
    read-validation behavior. No preview authorizes a Jira write. Every human surface narrates a
    dry-run in preview tense — conditional completion lines, preview
    spinner/fanout/`--debug` lifecycles — see [output.md](output.md).
*   **Read-only** — `config.Resolve` supplies `Config.ReadOnly` to the Jira
    adapter. `JIRA_READ_ONLY` overrides the file in both directions, including
    explicit `false` or `0`. Resolution errors stop client construction.
    The transport blocks writes while allowing its classified reads; this
    does not prohibit local settings/cache changes. See [security.md](security.md).
*   **ADF authoring** — local `adf convert` is strict by default and validates
    every candidate against the pinned schema. Dropped content refuses with
    `adf_content_loss`, retaining the candidate and diagnostics; explicit
    `--adf-best-effort` or `--adf-strict=false` accepts reported loss.
    Direct issue/comment `--markdown` routes use the same converter but warn
    and proceed on loss. Native body input uses `command.ResolveADF` validation.
    There is no global `JIRA_ADF_STRICT` policy. For content-sensitive authoring,
    require successful strict conversion, inspect diagnostics, then pass the
    extracted document to the appropriate native body/request input. A schema
    check does not establish Jira field permissions or future write success.
    Existing native documents use anchored `adf edit prepare/apply/merge` and
    the preservation owner, not a render-to-Markdown conversion round trip.
*   **Headless** — `Invocation.Interactive` is false for agents, unavailable
    input/error terminals, or explicit `--no-input`. That flag suppresses
    prompts; it is not a prerequisite or authorization for every mutation.
    A command requiring confirmation must refuse before mutation when no
    answer can be obtained, naming its own bypass: `--force` for issue/comment/
    attachment deletion and local `cache clear`/`alias delete`, or `--yes`
    for `auth logout`, `config migrate` and `config rollback`. Preserve each
    command's declared policy; do not invent a universal force requirement for
    commands that have no confirmation gate. Previews never ask.
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
    enum values come from `FlagOpts` metadata (`Placeholder`, `Enum`), never
    repeated inside the usage text: clib already renders
    `--query <text>  [table, json, yaml]`.

## Review points

*   Verb registered in `verbs.go`; renderer in `registry.go` for rich output.
*   Output struct registration, guide section and schema description updated
    in the same PR as any behavior change.
*   Contract test covers the envelope; guardrail test for any new invariant.
*   Dry-run checked before expensive work and before early returns.
*   New flags use `command.Add*` with complete `FlagOpts` metadata; declared
    predictors have registered handlers.

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
*   **`jira issue edit KEY` with no authored change** currently refuses with
    exit 3. `issue edit prepare` reads a bound native editing artifact; it does
    not launch an editor or publish a replacement. Recoverable draft commands use
    shared editor and preservation owners; protected remote application remains open.

Completion generation removes Cobra's combined boolean `h/help` spec when
Conductor supplies separate short and long help descriptions. Keep this repair
at the shared `WithGenerator` model boundary: duplicate options break Fish's
`argparse` dispatch before a predictor can run. Exercise the generated script.
Completed failed transition discovery invalidates old hints even if another
issue subsequently cancels the command; canceled or unstarted reads do not.

PowerShell generation uses completed literal AST values and scoped valued-flag
metadata; it never evaluates expressions to discover context. Exclude the current
argument and future tokens, keep callback arrays flat, preserve literal inserted
argv and keep descriptions in tooltips. Inline flag values keep their spelling.
Check the actual TabExpansion2 replacement, including comma arguments. Native
PowerShell requires quoting the whole CSV argument for whitespace-bearing members.

Forwarding must consume values of non-forwarded flags in the evolving command
scope. Honor `--` before interpreting flag values. Preserve the caller's
`LASTEXITCODE` and avoid recording errors for an absent exit-status variable.
Bare short inline CSV is a PowerShell parse error; omit unrepairable suggestions
and support quoted whole values instead.

Resolve transition-hint cache scope before read/write fan-out and retain that
public cache for finalization. A later cancellation must not cause another
credential lookup just to invalidate a completed failed read or attempted write.
Canceled/unstarted reads and previews do not publish changed workflow hints.

Migration outputs use shared public core DTOs inside registered typed envelopes.
Keep local migration specs free of Jira dependencies, retain partial recovery
receipts on errors, and declare explicit-source profile completion through flag
metadata rather than reading command flags inside a shell-specific predictor.

Use `command.OptionalString` for declared string flags whose omission differs
from an explicit empty value. The shared owner captures actual parser presence;
handlers receive immutable values, not Cobra commands. `auth login --cloud-id=`
forces classic routing, while omission permits verified scoped discovery.

Resolve public working defaults in `Run`, after pure flag refusals and before
remote factories. Omitted create metadata context may use defaults; explicitly
blank context remains a pure validation refusal. Preserve actual parser presence
for empty board names, invalid zero IDs and selector conflicts. Default scope
provenance belongs in shared `command.JQLInput`, not a bypass around composition.
Use the shared public cached-board owner for both issue lists and local JQL.

After local default-board resolution, live search/count/browser must bind that
snapshot's public site/account to the constructed client's BaseURL and public
`core.JiraAccountClient` account before entry. Refuse mismatched or unavailable
identity, preserve context, and reuse the checked client without another provider
resolution or remote identity request. Local as-jql and missing-cache refusal
remain credential-free; file preferences alone cannot prove live binding.

`config theme --name` and `config set theme` use shared `config.ThemeChoices`.
Advertise the application `auto` selector through the same flag/schema/completion
metadata as concrete presets. The custom palette `base` remains a concrete
clib name; do not reuse application selectors for that separate file grammar.

`config set team_account_ids` takes one JSON-array argument, not CSV or multiple
positional IDs. Advertise it and refresh_interval through shared config-key
metadata. Explicitly describe them as client-preference metadata; they do not
activate polling or issue filters. Keep alias command-tree validation separate.

Pending @me resolution uses core.RequireCurrentUserIdentity before changing any
account field. A populated Go value cannot override native absent/null markers;
blank identities retain current_user_response_invalid. Preserve all fields on
invalid identity or cancellation. Check cancellation before constructing a
client or entering lookup, while literal/no-lookup paths stay lazy. One validated
current-user reply resolves all pending fields. Transport validation alone does
not establish the contract of every injected core.JiraClient implementation.

Cached identity uses flat account_id with independent account_id_site,
account_id_email and account_id_cloud_id binding. Config.CachedAccountID compares
that binding with effective public scope using shared site normalization; missing
site/account or mismatched bindings stay inactive. Empty cloud scope is classic. Scope changes never rewrite bindings.
Validate file and environment metadata before providers. Stored inspection and
replacement can repair invalid values; binding URLs use shared redaction.

Only bare local previews may use core.IssuePreferences.CachedAccountID through
command.ResolveAccounts. Publish account_id_cached as unverified provenance.
Live and --validate-remote paths ignore cached IDs and validate one actual Jira
current-user reply. Literal/no-lookup paths do not read cached preferences.
Preference errors/cancellation preserve fields. Clone previews still do not read
or claim copied source fields. Mapping policy 7 retains selected authored ID and
its public scope; policies 1–6 retain exact original candidate derivation and
unsupported-field refusals. Retained metadata does not authenticate a client.

Verified login uses the typed local mutation and shared settings/publication
owners. Prepare complete public routing, provider selectors and independently
bound account ID before credential mutation. Retain verified identity and actual
credential/settings effects on failure or output filtering; never replay unknown
writes. Environment login verifies configured tokens and publishes public
metadata only. Authentication previews may verify proposed scope and report
blocked publication without changing settings or credentials. See config.md for
provider binding and per-field public-default admission.

Issue description preparation uses core.PrepareIssueDescription and
IssueDescriptionSession over the shared adf.EditSession. Bind the native issue ID
and the constructed client's public site/account/API route; reread by immutable ID so
issue-key moves remain usable. Check scope before and after the read, validate
authored anchors before remote work and preserve original buffers independently.
Native absent descriptions or incomplete identities refuse; explicit null remains
null after a blank editor save. Preserve unknown ADF bytes and current root state.
Updated is diagnostic only. Preparation/merge owns no Jira writer and is not CAS.
`issue edit prepare` is a typed read through command.Spec and shared View; ordinary
edit still needs authored changes. Recoverable editor drafts use the local owner described below; protected remote
application remains an unfinished capability.

Bound description reads admit only final responses inside the configured API
scope with the exact request credential binding. Reuse the authenticated-reply
owner shared with CurrentUser; media redirects cannot supply editing identity.
Strict description decoding requires one complete bounded native object, refuses
duplicates/trailing data and preserves body cancellation causes. Serialized
snapshots keep exact native bytes at description_bytes (base64), independently
of the adjacent JSON view's whitespace compaction. Reopening verifies semantic
agreement and uses the byte preimage. Preserve both fields and all public scope.

A client lacking bound description reads returns `issue_description_unavailable`;
reserve `issue_description_scope_changed` for an actual snapshot/client mismatch.

Editor consumers share `command.ResolveEditor` and `command.RunEditor`. Preserve
JIRA_EDITOR, stored editor, VISUAL, EDITOR precedence, literal argv, early executable
validation and no fallback. Preference reads remain local and credential-free;
terminal/process ownership stays in RunEditor. Do not duplicate selection in a domain.

Recoverable issue description drafts use `internal/issueedit` over the shared
bound description session. `issue edit draft` creates an unused child directory;
`inspect` validates saved text locally unless `--refresh` explicitly requests a
bound native reread; `resume` edits the existing Markdown through the shared
editor supervisor. All three declare native directory completion metadata.
Publish the exact native source and editable Markdown before the manifest, and
retain partial files, saved work and acknowledged effects on failure. Never
clobber existing members or delete the recovery directory automatically. Digests
detect corruption; they do not authenticate a draft or authorize Jira writes.
Editor operations require an interactive terminal before artifacts or credentials
are opened. Previews launch no editor, publish nothing and construct no Jira client.
These commands produce candidates only; protected remote application remains open.

CSV directory choices retain an unfinished current field so a second Tab can
complete a leaf in the same argument. Theme choices preserve literal symlink/..
traversal when made absolute and validate the final transport bytes, including
the current directory. Verify these identities in native editors and named guards.

Declare CSV flags with the native comma metadata as well as their current-word
predictor. Fish refuses an ambiguous unquoted separate value that looks like its
active inline option (for example, `--file --file=name`). Quote the whole CSV
field, `--file '"--file=name"'`, to complete that literal filename safely.

PowerShell directory insertion consumes an explicit `--@directory` hint in the
current-word callback description slot. Ordinary cached values ending in a slash
remain complete literal arguments. Keep the hint private to the generator and
verify both directory continuation and slash-ending configuration values. Fish
current-word callbacks reuse the shared version-aware token helper.

Bash versions before 4.3 use the shared legacy spacing path because `noquote`
was added in 4.3. Fish 3 preserves inline option spelling explicitly when its
editor replaces a whole quoted CSV token. Native terminal guards bind Fish
function keys through the selected version API (`bind -k` before 4, named keys
from 4), and explicit shell selections fail if missing.

The Bash legacy spacing wrapper clears filename/default completion options
after the selected callback and before manual quoting. Native file hints must
not quote already quoted replies a second time on Bash 4.0–4.2. Exercise ordinary
file and directory hints alongside the current-word callbacks.

Legacy Bash spacing callbacks enumerate directory hints before clearing native
filename options. Carry directory suffixes through file/extension hints only at
that wrapper boundary; ordinary model generators retain their native behavior.

Bash literal callback decoding normalizes the ANSI-C quoting emitted by
`printf %q` before the selected POSIX lexer. Preserve UTF-8 octets, refuse
unsupported escapes or malformed bytes, and never evaluate expansions. Decode
completed context with the same owner. Guard a Unicode directory with two Tabs
and native executed argv; a single completed basename cannot prove continuation.
