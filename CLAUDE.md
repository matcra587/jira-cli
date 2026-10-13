# jira-cli

Scoped tokens use the shared cloud-ID validation and fixed gateway owners, keeping
site/browser/cache identity separate from the authenticated API route. Credential
record version 2 binds cloud scope; version 1 remains classic. Authored scope
changes, including explicit empty, cannot silently reuse a stored token. Migration
derives configuration and credential scope from its recorded mapping policy.
Public tenant discovery reads no tokens/cookies and follows no redirects; context,
timeouts and malformed-account errors retain their outcome through scoped fallback.

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
    Bash/PowerShell/Zsh/Fish/Elvish generator repairs through `complete.RegisterShell`. The root registers
    them once before exposing generators. Reuse clib's public metadata and
    validation; do not rewrite printed scripts or break versioned `go install`
    with a module replacement. Fish and Elvish delegate to their library generators
    for models without current-word predictors. Nushell uses its library generator.
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
Legacy migration and rollback use the shared transaction owners described below;
remaining runtime preferences and broader native acceptance remain incomplete.

Use `config.PrepareLegacyMigration` for decode-only legacy profile preparation.
Keep exact original bytes and old source credential namespaces; preserve the
legacy 30-second timeout fallback. Unmapped preferences block candidate
publication. Preparation is not runtime profile selection or completed rollback.
Legacy decoding and declaration tracking must share canonical keys and shapes.
Retain historical credential-host spelling, trimmed editor fallback and literal
query paths; follow `.claude/rules/config.md` for these migration boundaries.
Configuration recovery must publish protected preimages before dependent writes,
reuse shared snapshot/publication owners, and refuse changed postimages. Preserve
ordinary flat-file validation and acknowledged effects after durability failures.
Configuration restoration alone does not complete credential migration/rollback.
Credential recovery uses opaque provider snapshots and provider-owned backups.
Bind exact keyring namespaces and SDK IDs; preserve original SDK owned-field
metadata and current unmanaged content. Mutation entry is not acknowledgment;
never expose snapshots or persist credential preimages in configuration records.

Use `config.PrepareLegacyTransaction` and `LegacyTransaction` for migration
orchestration. Publish immutable configuration recovery and a protected journal
before opening providers. Persist intent before each backup or primary write;
retain unknown file-write entry and non-durable outcomes after cancellation.
Apply once, then use fresh explicit rollback for interrupted operations. Rollback
preflights all resources before restoring either. `config migrate` and
`config rollback` use these owners through typed command specs. Their previews
open no providers and publish nothing; live headless execution requires `--yes`.
Environment-backed migration preserves a public flat `api_token_env` reference
without reading token values or opening providers. Its `external` credential
state does not claim an environment backup or restoration. Validate references
before runtime provider reads, preserve direct token priority and one cloned
environment snapshot, and normalize names consistently on Windows. Recovery
derives candidates by their recorded policy version, preserving older records.
Remaining runtime preferences and broader native acceptance are separate requirements.
Bind provider backup preimages and expected states with keyed commitments before
writes. Keep random commitment keys inside the provider, never the journal;
reopened rollback must detect changed values and owned-field metadata.

Migration profile completion reads only explicit `--source` through the strict
legacy decoder. Forward that source in flag metadata; never add runtime profile
selection or discover old configuration implicitly. Public recovery DTOs belong
to `internal/core`; typed command outputs belong to `internal/envelope`. Preserve
partial receipts on failure and recovery guidance ahead of generic provider errors,
while retaining context and output-failure classification priority.

Absent configuration/recovery targets must reject regular-file ancestors on every
platform before publishing or opening providers. Preserve symlink canonicalization
and allow genuinely missing directory chains; a Windows not-found result alone
does not establish a creatable target.

Live login must refuse a verified cloud scope that conflicts with authored
`cloud_id` before opening the store for publication. Report the verified public
scope for repair, while previews remain free to verify without saving. Never
report a successful login whose token the next resolution would discard.

Preserve historically accepted site prefixes as public browser/cache identity
when a scoped token uses the fixed gateway. Do not reject a ready mapping merely
because the separate site URL has a path. Public discovery still uses the origin
root and strips query/fragment state.

Working defaults use public `core.IssuePreferences` and the existing local
preferences/cache constructors. Create defaults fill only absent native fields
across both `fields` and `update`, before the shared final encoding; explicit
empty flags suppress them. They never alter edit/clone requests. Structured issue
list/mine inherits project only without raw/saved query or active board. Local
JQL build keeps its independent project/date grammar and inherits board only.
Configured board scope uses `boardscope.ResolveCached`, never credential-backed
cache construction or implicit remote warming. Its provenance permits safe
parenthesized raw-query narrowing; explicit board/raw selectors remain exclusive.
Retain mine account scope, ordering precedence and snapshot timestamp in every
output branch. Preference/cache cancellation or load failure cannot broaden a
query. Mapping policy 4 derives older policies 1–3 without adding new defaults.

Retained board selector keys keep delivered v2 site/name spelling. Equivalent
site spelling and case-insensitive name lookup may inspect only the already
selected account namespace through `core.ManagedCache`; names never fall back
to ID records or unscoped caches. Refuse ambiguous board identities, malformed
matching snapshots, partial inventory failures and cancellation. Use the newest
complete matching snapshot only when its retained board identity agrees.

If authored public site spelling misses its outer cache namespace, retained
board scope may retry only the shared canonical site with the same exact public
account. This preserves stored-record producers that normalized site spelling.
Only a proven missing resolution permits that bounded retry; malformed matching
snapshots, inventory failures and cancellation refuse without fallback. Never
scan other outer namespaces or infer an account from credentials.

A malformed disk wrapper with unknown logical identity makes a missing-selector
inventory incomplete. Refuse before canonical namespace retry; do not classify
corruption or a symlink as proof of absence. Exact valid snapshots remain usable
without inspecting unrelated entries.

After local default-board resolution, live search/count/browser must bind that
snapshot's public site/account to the constructed client's BaseURL and public
`core.JiraAccountClient` account before entry. Refuse mismatched or unavailable
identity, preserve context, and reuse the checked client without another provider
resolution or remote identity request. Local as-jql and missing-cache refusal
remain credential-free; file preferences alone cannot prove live binding.

Automatic theme selection belongs to the shared theme owner. `theme=auto` uses
COLORFGBG then the fixed dark palette, never an active terminal query. Resolve
help and syntax colors together; custom palette files may override a file
selector and unavailable palettes retain its fallback. An environment selector
precedes the custom file. Preserve exact JIRA_THEME=auto value/provenance, substituting
its resolved concrete palette solely during the framework library environment
pass and restoring it even after failure. Retain an explicit winner during that
pass so losing light/dark pairs cannot replace syntax colors or warn. Custom palette bases require concrete
clib presets. Mapping policy 5 retains selected legacy automatic themes; policies
1–4 keep their original candidate derivation and unsupported-selector refusal.
Legacy theme paths are literal data. Mapping policy 8 retains names containing
~ or $ and anchors relative names to the explicit legacy source directory; never
expand them through home or environment state. Policies 1–7 retain their original
candidate derivation and special-path refusal. Global TUI mappings remain open.

Serialize framework theme environment passes across concurrent program
constructors. Restore the exact authored value before releasing that boundary;
a constructor must never observe another constructor's temporary selector.

Client-preference metadata uses shared flat configuration owners. Selected legacy
refresh_interval normalizes nonpositive values to the historical 30 seconds.
Current declarations and environment values require positive integer seconds.
team_account_ids is a typed TOML string array; argv/environment use a strict JSON
string array, including [] for explicit clear. Preserve literal IDs, order,
duplicates, commas and empty entries; reject null/nonstrings and never parse CSV.
Validate declared types independently and these environment preferences before
credential reads. Public values use canonical JSON text for the array. Settings
publication remains one candidate through PrepareSetting/PrepareSettings.

These selected-profile fields had no CLI polling/team-filter consumers in the
reviewed v0.15 source; do not invent automatic loops or broaden/narrow searches.
Global TUI refresh and other TUI state are separate unfinished requirements.
Mapping policy 6 retains selected client metadata and authored team presence;
policies 1–5 keep original candidate derivation and unsupported-field refusals.

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
unsupported-field refusals. Verified identity metadata does not authenticate a client.

Verified whoami saving uses a constructor-owned `LocalWrite` alternative with
its own typed output and preview policy. Root failure attribution uses
`SelectedVariantMeta`; discovery is read-only only when all operations are reads.
Snapshot public settings before lookup with `OpenAccountIdentityTarget`, refuse
conflicting authored scope and publish the four bound fields through shared
settings owners. Saving factories alone use `Options.PermitMissingTarget`;
ordinary reads retain missing-explicit-file refusal. Check no-op snapshots,
create absent targets without clobber and retain acknowledged/unknown effects.
Native privacy-hidden email remains native; the binding uses credential account.
Serialized warning-producing tests inspect and drain the process-wide queue.
Other tracked migration requirements remain open.

Verified login prepares its public settings through `OpenLoginIdentityTarget`
before credential mutation. Bind the actual native ID to credential account,
site and verified cloud; publish missing public routing/provider/reference
metadata so a fresh process can use the login. Refuse conflicting authored or
effective scope, changed provider, exact SDK account/vault/item selectors and
changed environment reference before publication. Preserve the complete selection
through interactive forms. Publish effective SDK selectors for fresh-process use
and refuse replacing an authored reference through an environment overlay. Ordinary whoami saving still changes only four bound ID fields.
Public-first login factories validate the target before providers and consult
stored identity only for fields absent from both flags and public defaults.
When another field needs storage, preserve every complete public field and copy
only the missing field; never return the whole stored tuple as login defaults.

Credential publication uses `credentials.SaveWithPublication` and public core
receipts. Known provider admission refusal differs from an entered unknown write;
acknowledged success survives late cancellation. Retain the verified candidate
and actual credential/settings effects on failure or output filtering. Do not
replay, restore or claim no effect from an unknown provider outcome.
Environment login verifies its configured reference without opening a persistence
provider and publishes public metadata only. Live stdin input is refused before
reading it for that backend; independent stdin previews remain verification-only.
Authentication previews may report a blocked settings candidate while still
verifying the proposed tuple. Cached metadata remains unverified local-preview
provenance and never authenticates a live client.

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

Contextual filesystem completion uses one bounded directory-name owner and the
shared command.CSVCompletion codec. Preserve literal prefixes, completed CSV
spelling and quoted delimiters; read no file contents or credentials. The callback
boundary carries the command context and suppresses canceled results. Actual
shell routing and insertion require generated-script acceptance, separately from
pure codec/filesystem guards.

Current-word predictors use the `word_` metadata convention and native
`--@word=` transport inside the existing clib registration boundary. Forward
public flags before a separate completed-positional terminator. Bash replies are
explicitly quoted; Fish CSV replies quote every current field to retain native
match rank. Elvish retains the selected matcher and bounds replacement admission
to the exact command and seed. Test native editor insertion and executed argv
with closed quotes and literal metacharacters; candidate lists are insufficient.
Bash 3.2 spacing belongs to its registered wrapper. Current shells preserve
directory continuation, inline values and hidden boolean alias resolution.
Reject protocol-unrepresentable bytes across the whole argument; dequote
completed Bash context separately from its current token without expansion.
Theme-path suggestions use absolute choices from the literal current-directory
inventory; manually authored relative settings still resolve beside their file.

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

Native field refreshes belong to `jira.Nullable.UnmarshalJSON`, alongside the
shared generated-model decoder. Decode into a fresh value and publish the full
presence/null/value tuple only after success. Null clears its prior value; a
subsequent value clears null. Failed decoding retains the exact earlier tuple
and leaves aliased maps, slices, pointers and raw body bytes untouched. Do not
work around stale native fields independently in commands or adapters.

Tests that load a named IANA location must provide their timezone database as a
test dependency, for example by importing `time/tzdata` in the test file. Native
Windows may provide time.Local without an IANA database. Preserve real DST
transitions in the guard; do not replace them with a fixed offset or skip missing
fixture data. Do not add timezone data to the release CLI merely to repair a test.

Externally selected native update tests keep a renamed installed basename while
preserving `.exe` on Windows. Confirm the original and candidate version commands
actually execute before interpreting publication/download results. Test only
disposable installation copies; preserve selected artifacts and retained backups.

Windows Credential Manager caps each blob at 2,560 bytes. The keyring recovery
owner keeps ordinary/version-2 backups intact and publishes larger private v2
payloads as bounded encoded pieces with a version-3 descriptor last. Derive piece
names only from the validated parent backup ID; verify count, size and full digest
before reopening snapshots or mutating primary entries. Retain partial effects
and native admission on cancellation/failure; this is not a provider transaction
or compare-and-swap. Native tests use fresh random service names, synthetic
records and independent WinCred reads, never enumeration or the user's default
service. Run them in a separate test process so global mocks cannot earn credit.

Native Windows editor acceptance uses the exported shared supervisor inside fresh
hidden consoles. Open the owned CONIN$/CONOUT$ handles explicitly; redirected
standard streams are not proof of terminal allocation. Observe both changed
native modes before releasing the real editor, then verify exact restoration,
process termination, literal argv and separation from result stdout. Cover failed
exit, cancellation, launch failure and second-terminal capture refusal. Run
`mise run test:editor:windows` on Windows; its nativeconsole-tagged gate fails
explicitly on other hosts. This owner evidence does not complete public command
acceptance or native macOS behavior.
