# Jira CLI development

Scoped tokens use the shared cloud-ID validation and fixed gateway owners, keeping
site/browser/cache identity separate from the authenticated API route. Credential
record version 2 binds cloud scope; version 1 remains classic. Authored scope
changes, including explicit empty, cannot silently reuse a stored token. Migration
derives configuration and credential scope from its recorded mapping policy.
Public tenant discovery reads no tokens/cookies and follows no redirects; context,
timeouts and malformed-account errors retain their outcome through scoped fallback.

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
    and secret-free files, logs, envelopes and argv. Migration retains every
    selected profile and recoverable preimages. Flat files remain readable.
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

Credential operations and credential-backed factories take the command context.
Use explicit `config.Resolve(ctx, options, store)` for credential resolution;
`config.Load(options)` does not consult a store. Preserve lazy initialization
and the existing OS-keyring cancellation limitation described in `.claude/rules/go.md`.

Cancellation must not become an unavailable credential layer that an environment
token can override. Refuse canceled native keyring calls before entering them;
already-started synchronous writes retain their actual outcome. Resolve public
transition-hint cache scope before remote work so completed failed observations
and attempted writes invalidate stale hints after later cancellation without
reopening credentials or detaching context.

Direct 1Password uses the official SDK and the selected profile's `secret_backend`.
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
configuration document validation and acknowledged effects after durability failures.
Configuration restoration alone does not complete credential migration/rollback.
Credential recovery uses opaque provider snapshots and provider-owned backups.
Bind exact keyring namespaces and SDK IDs; preserve original SDK owned-field
metadata and current unmanaged content. Mutation entry is not acknowledgment;
never expose snapshots or persist credential preimages in configuration records.

Use `config.PrepareProfileTransaction` and `ProfileTransaction` for current
migration orchestration; retain `LegacyTransaction` for older journals.
Reopen either kind through `LoadMigrationTransaction`.
Publish immutable configuration recovery and a protected journal
before opening providers. Persist intent before each backup or primary write;
retain unknown file-write entry and non-durable outcomes after cancellation.
Apply once, then use fresh explicit rollback for interrupted operations. Rollback
preflights all resources before restoring either. `config migrate` and
`config rollback` use these owners through typed command specs. Their previews
open no providers and publish nothing; live headless execution requires `--yes`.
Environment-backed migration preserves each profile's public `api_token_env` reference
without reading token values or opening providers. Its `external` credential
state does not claim an environment backup or restoration. Validate references
before runtime provider reads, preserve direct token priority and one cloned
environment snapshot, and normalize names consistently on Windows. Recovery
derives candidates by their recorded policy version, preserving older records.
Remaining runtime preferences and broader native acceptance are separate requirements.
Bind provider backup preimages and expected states with keyed commitments before
writes. Keep random commitment keys inside the provider, never the journal;
reopened rollback must detect changed values and owned-field metadata.

Migration source-profile completion reads only explicit `--source` through the
strict legacy decoder. Runtime profile completion reads the selected public
configuration, never providers. Forward selectors through flag metadata and
never discover old configuration implicitly. Public recovery DTOs belong
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
