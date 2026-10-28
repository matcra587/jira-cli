---
description: >
  Configuration owners, precedence and provenance, file-only updates,
  credential separation, theme and editor selection, and migration boundaries.
paths:
  - "internal/config/**/*.go"
  - "internal/cli/config/**/*.go"
  - "internal/cli/auth/**/*.go"
  - "internal/cli/root/**/*.go"
  - "internal/cli/theme.go"
  - "internal/credentials/**/*.go"
  - "internal/adapters/keyring/**/*.go"
  - "internal/adapters/onepassword/**/*.go"
---

# Config

Logout derives remaining_source and env_token_remains from one observation of
an actual nonempty remaining token, including the environment fallback after a
stored record is cleared. Empty/default provenance alone is not a credential.
Keep fallback resolution lazy and preserve the existing removal outcome.

Named documents use `format_version=1`, `default_profile` and `[profiles.NAME]`
tables. Existing flat files remain readable. One selected profile resolves to
the flat runtime `Config`. `internal/config/load.go` owns koanf resolution;
`internal/config/profile_document.go` owns strict document admission and selection,
and `internal/config/file.go` owns authored file snapshots.
Credentials use the separate `internal/credentials` owner and the root's lazy
credential-store constructor. Do not recreate the old `loader.go`, profile
helpers or `LoadOrInit` API: they are absent from this architecture.

Select through `--profile`/`-P`, then `JIRA_PROFILE`, then the authored default.
Unknown or explicitly empty selectors refuse; reads never manufacture profiles.
Validate all authored tables before overlays. Keep absence, empty, false and zero
distinct. Ordinary environment precedence and transport read-only enforcement
remain unchanged. Public inspection distinguishes selected profile from effective
identity; stored metadata must not replace a named profile's identity before the
binding check. Normal factories share captured file bytes and environment.

Profile mutation uses the existing file publication owner, preserves other tables,
and opens no providers. Renames keep credential locations; removal retains secrets.
Keyring `credential_id` is a stable public UUID; `default` retains the old fixed
slot when converting flat settings. A missing named locator never reads that slot.
Explicit login durably publishes a new locator before writing its credential and
reports that publication separately. Previews allocate no persistent locator.
1Password keeps its provider-local slot and exact vault/item owned-field boundary.
Distinct profile names do not isolate a shared physical credential location.

`PrepareProfileMigration` prepares the selected legacy profiles or a flat source.
`PrepareProfileTransaction` and `ProfileTransaction` publish the named document
and each credential with separate effect receipts. `LoadMigrationTransaction`
dispatches rollback to the recorded journal kind. The legacy owners below retain
shared decoding/recovery primitives and support older single-profile journals.

`PrepareLegacyMigration` is the decode-only v0.15.2 preparation owner, not a
runtime profile loader. Preserve the complete original bytes and old credential
namespace independently of the normalized destination site. Profile selection
uses the requested name or the authored legacy default and never merges profiles.
Preparation reads no environment, files or credentials. The old timeout fallback
is 30 seconds; do not substitute v0.16's zero-deadline default. Keep dormant SDK
coordinates, literal alias names and declared empty preferences.
Canonicalize accepted case-insensitive schema keys and singleton profile tables
before decoding and declaration tracking; reject ambiguous duplicate keys. Keep
user-defined alias/action names literal. Historical credential lookup expands
single-label hosts and retains the old port-discarding behavior independently
of the destination URL. Trim profile editors before global fallback. Both pinned
query loaders expand home and environment paths at runtime; preparation preserves
the authored query path without resolving it.

`LegacyMigration.Candidate` refuses zero preparations and unresolved `Unmapped`
preferences. Missing runtime mappings and unresolved relative path origins must
remain visible and block partial publication. Explicit policy-9 TUI deferral is
the sole exception described below. Original recovery
bytes remain private; internal preparation state is not an envelope payload.
File/credential publication, recoverable preimages and rollback are separate
required migration stages; preparation alone does not deliver the migration CLI.

`PrepareLegacyRecovery` captures source and destination snapshots without creating
directories. Its plan cannot apply configuration. Publish through `Create` before
dependent writes; recovery uses the shared private file validation boundary and
atomic no-clobber creator at mode 0600. Records retain exact recognized preimages,
candidate bytes, paths and hashes, without credentials. JSON decoding rejects
unknown/duplicate members and inconsistent hashes or derived candidates.
Require an explicit recovery path; never select the global/default config path
for a record. Use the shared secret-key set to refuse authored secret declarations,
including empty values, before retaining a destination preimage.

`LegacyRecovery.ApplyConfig` and `RestoreConfig` recheck the immutable record and
expected snapshots. Rollback restores exact bytes or prior destination absence;
an external edit refuses rollback. Keep recovery after success, failure or
cancellation. Already restored preimages are no-ops. Ordinary `File.Save` and
`Create` still validate current configuration; only verified recovery uses the
private preimage validator. Publication/removal directory-sync failures retain
acknowledged effects. These checks are not filesystem compare-and-swap or a
complete credential transaction; CLI/provider orchestration remains required.
Replacement errors can follow a completed rename: a false `File.Save` result
with an error does not prove no write. Transaction receipts must retain that
uncertainty and inspect expected postimages before deciding on recovery.
Resolve absent snapshots through the same recursive missing-parent path owner as
no-clobber creation, retaining symlinked ancestors without creating directories.
Validate every resolved ancestor as a directory when it exists. A child beneath a
regular file is not a valid absent target, even when Windows reports not-found.

`core.CredentialRecoveryStore` owns opaque provider snapshots and dedicated UUID
backup names. Capture preserves absence and raw values; `Replacement` is pure.
Backups contain preimages and expected postimages inside the selected keyring or
concealed SDK field, never configuration/recovery JSON. Snapshot JSON and all
ordinary fmt forms must conceal secrets. Only `Secret` supplies private conversion
input; convert through the existing credential-record owner.

Keyring snapshots bind the exact service and account slot, including historical
host/profile spelling. `NewForService` selects an explicit old/test namespace
without reading environment or opening native storage. SDK snapshots bind the
selected account and actual vault/item IDs, retain the complete modeled owned
field metadata, and preserve current unmanaged item content during restore.
Backup creation refuses an existing resource and never overwrites by title.
SDK ownership still requires the stable owned field ID and a valid bound record;
no whole-item deletion. Recovery of a missing SDK item is not a guessed postimage.

`Apply`/`Restore` compare the current credential with the expected state before
entering the native mutation. Their bool means mutation entered, including errors;
it does not mean acknowledgment. Already applied/restored state is a no-op and
backups remain retained. Preserve actual started-write outcomes after cancellation,
sanitize provider failures while retaining causes, and refuse changed credential
state. These checks are not cross-process/provider compare-and-swap. Full migration
still requires durable effect orchestration, command delivery and acceptance.

`PrepareLegacyTransaction` composes the existing preparation/recovery owners.
Preparation and `Create` open no providers. An explicit recovery directory holds
immutable `configuration.json` and mutable `migration.json`, each protected and
published without clobbering. Journal creation failure retains the configuration
artifact and permits no dependent credential/configuration writes.

`LegacyTransaction.Apply` is single-use. Resolve only the selected historical
source; absent credentials skip destination reads/writes. Present raw credentials
convert through `credentials.Save`, with complete site/account binding. Refuse
JSON-looking values rather than wrapping partially migrated records as tokens.
SDK captures retain concrete IDs for later reopen; keyring retains the exact old
service/slot. `CredentialSnapshot.Matches` compares opaque native state without
exposing it, and `Location` returns public provider identity only.

Persist pending intent before every provider backup, credential mutation and
configuration publication. Provider acknowledgment and postimage verification
are distinct facts. A canceled journal update retains actual in-memory outcome
with `journal_durable=false`; the on-disk pending intent remains unknown. File
false-plus-error entry remains uncertain. Never auto-replay an interrupted apply
or start detached cleanup; retain recovery for explicit later rollback.

`LoadLegacyTransaction` checks strict journal JSON, effect order, provider scope
and immutable configuration hashes. `Rollback` checks all configuration and
possibly changed credential states before its first write, then restores the
credential before exact config bytes or absence. Recheck at each operation,
retain backups and permit fresh rollback retries after partial effects. These
checks are not provider/filesystem CAS. Current commands use the profile owners
above; older journals retain `LegacyTransaction` recovery. Environment-backed
migration is configuration-only: preserve the selected public token-variable name,
record credential state as `external`, and never open providers, read token values,
create credential backups or claim to restore externally owned environment state.
Remaining runtime preferences and broader native acceptance remain unfinished.

Recovery derivation uses the recorded mapping version, not the latest converter.
Version 1 records retain their exact original candidate bytes; version 2 adds the
flat environment reference. Accept legitimate older native records without
rewriting them, and reject environment journals under the older policy.

Credential backup integrity uses per-capture random 32-byte provider-only keys.
Before/expected snapshot HMAC commitments are journal metadata; raw snapshots and
keys remain inside the provider. Reopened rollback verifies both commitments,
including owned-field metadata, before any restore. Snapshot equality excludes
the random key so independent captures still compare native state. Never replace
keyed commitments with public token hashes that permit offline guessing. Backup
format version 2 requires the key; older uncommitted foundation records do not
silently acquire an integrity claim. Journal integrity checks do not authenticate
an attacker rewriting both the provider and the protected journal.

## Resolution and provenance

Scoped-token routing uses public flat `cloud_id` and the shared
`core.ValidateCloudID` / `JiraAPIBaseURL` owner. The fixed Atlassian gateway is an
API route, never the browser/site identity. Credential record version 2 binds
cloud scope with site/account/token; version 1 remains classic and cannot contain
new routing metadata. Decoder causes remain inspectable without private record
bytes in diagnostics. Missing scope declarations may inherit the verified record;
an authored scope change, including explicit empty, drops the stored token before
it can be sent. Capture declaration presence before stored-layer merging. A new
explicit environment token may use declared scope without a stored scope shadow.

Legacy mapping policy 3 adds selected cloud routing and its classic empty default.
Derive policy 1/2 records and their selected credential scope through the recorded
policy, including when reopening a prepared transaction. Do not retroactively
reject historically accepted unselected profiles. Current routing validation
rejects dot segments and URL injection. Backup binding verifies cloud scope too.

`default < file < selected credential store < env < flag` is the precedence
order. `config.Layers` names keyring and 1password provenance; an invocation
selects one store, never both.

*   `Load(options)` reads local settings without writing or consulting a
    credential store. `Resolve(ctx, options, store)` explicitly adds credential
    resolution through the same layer owner and forwards the operation context.
    Cancellation and deadline errors stop resolution even with an explicit token;
    ordinary backend failures retain environment fallback. Neither API stores a
    context in options. A missing default file is normal;
    an absent file selected through `--config` is an error for settings reads.
*   `Options.Path` overrides the global `--config` selection, which overrides
    `DefaultPath`. The path selects the file; it is not a setting value.
*   A supplied credential store contributes its complete site, account and
    token tuple. If the effective site differs from that stored site after
    normalization, resolution removes the entire credential layer and warns.
    Never send a stored token to a site selected from another layer.
*   `JIRA_*` overlays supported keys only. Empty values are unset; unknown
    variables and `JIRA_ALIASES` are ignored. File aliases remain literal keys.
    `api_token_env` is public metadata, accepting empty, `JIRA_API_TOKEN` or a
    `JIRA_TOKEN_` name with a nonempty uppercase/digit/underscore suffix. Direct
    `JIRA_API_TOKEN` retains priority. An effective named token uses configured
    site/account and skips unrelated stored tuples; a missing or empty named
    value keeps selected-store fallback. Capture and clone one environment
    snapshot before callbacks and retain it during rebuilds. Canonicalize JIRA_
    variable names consistently on Windows without changing their values.
    Runtime validates authored references before any environment overlay or
    provider read. File-only inspection can read an invalid public reference;
    replacement/save validates it so `config set` can repair it.
*   `--timeout` supplies the flag layer. Preserve its explicit-set bit:
    `--timeout=0` disables the deadline, while omission retains lower layers.
*   Decode the file independently before merging so environment values cannot
    hide unknown keys or invalid declared types. TOML decoding is strict;
    the merged environment permits the required string-to-type conversions.
*   `Config.Validate` requires complete account settings only where a Jira
    client needs them. Keep local discovery and preference operations usable
    without credentials. This does not waive decode or save-time validation.

`config list` reports effective values, their winning sources and the selected
path. Its credential constructor selects `secret_backend`; `keyring=false`
disables only the default keyring backend. `config get KEY`
reads only the authored file, preserving absent versus explicit empty, false
and zero. Both use the configuration owner's redaction, including credential
userinfo in URLs; neither prints a token.

## File selection and publication

`DefaultPath` uses the pinned `x/shell.ConfigDir("jira", "config.toml")`.
An absolute `XDG_CONFIG_HOME` overrides the platform fallback. The fallback is
`~/.config/jira/config.toml` on Unix and `%AppData%\jira\config.toml` on Windows.
Use the owner; never hardcode those paths in command handlers.

*   `ReadFile` refuses an absent explicit path. `OpenFile` permits a missing
    update target and follows existing config links, refusing dangling links.
    Layered loads, snapshots and save re-reads validate an opened regular-file
    descriptor before reading bytes. Unix opens are nonblocking so FIFO inputs
    refuse without waiting for a writer. This does not impose a file-size limit
    or interrupt arbitrary regular-file filesystem I/O.
*   `File.Save` validates the candidate before atomic replacement through the
    pinned `x/os.AtomicWrite`. Changed files request mode `0600`; on POSIX hosts
    newly created directories are private and existing parent permissions are
    retained. Windows mode bits do not establish ACL protection.
    Unchanged bytes leave file metadata untouched.
*   Save checks the original bytes, existence and resolved link target.
    A detected conflict is `ErrFileChanged`. These checks are not a filesystem
    compare-and-swap or a lock against arbitrary external writers.
*   `OpenNewFile` and `File.Create` own initialization. They refuse existing
    names, directories and dangling links without reading their contents.
    Creation stages a private sibling through `AtomicWrite`, then publishes
    through atomic no-clobber `os.Link`. Unsupported filesystems fail; never
    replace that boundary with a clobbering rename or a partial direct write.
*   A creation directory-sync error can occur after publication. Preserve
    `created=true` in the typed error receipt; inspect the destination before
    retrying. Do not infer rollback from a storage error.

## Local commands

Build commands through `command.Spec` and the shared typed output/View path.
Mark local mutations with `MutatesLocalState`; do not invent a Jira transport
gate or credential dependency for file-only work.

*   `config init` requires explicit `--base-url` and `--email` in every mode.
    Validate both before file I/O. It never prompts, reads credentials or
    contacts Jira. Preview creates no files or directories and persists no
    defaults or environment values; real creation uses the shared owner.
*   `config set` prepares settings through `PrepareSetting` or
    `PrepareSettings`. Credentials are refused and aliases require their
    command-tree validation owner. Preserve other declared values without
    writing the effective merged configuration. Changed saves normalize TOML;
    unchanged typed values preserve authored bytes and comments.
*   `config edit` edits a copy and validates before saving. Editor precedence
    is `JIRA_EDITOR`, the stored `editor`, `VISUAL`, then `EDITOR`, with no
    guessed fallback. An explicit `JIRA_EDITOR` permits repairing invalid TOML.
    Split executable arguments with the pinned shell helper; run without a
    shell. Real edits require a terminal; preview launches nothing.
    Rejected edits retain a recovery-copy receipt. Terminal supervision belongs
    to `command.RunEditor`, including cancellation and mode restoration.
    Read the editor's saved scratch bytes through the raw file snapshot owner.
    Capture regular saved work after cancellation for recovery, then refuse
    publication through the original context. Nonregular scratch input is
    refused; editor, context and native read causes remain inspectable.

Credentials belong to `auth login`, the credential-store adapter and the shared
client construction path. Never add a setting command that persists a token.
`secret_backend` selects `keyring` (default), `env` or `1password`. The environment
backend reads no persistent store; `auth login` cannot save a token there. The
1Password adapter requires explicit `onepassword_vault` and `onepassword_item`
coordinates. Service-account authorization comes only from
`OP_SERVICE_ACCOUNT_TOKEN`; otherwise `onepassword_account` selects desktop
integration. Neither service-account nor Jira tokens belong in config or argv.
Environment Jira credentials retain higher precedence. Cancellation/deadline
causes are terminal even when a higher-priority token exists.
Stored tokens remain bound to the verified site and account. Changing only the
effective email discards the entire stored layer; compare trimmed emails without
case sensitivity. Explicit environment email/token pairs retain precedence.

SDK reads and writes manage only stable field ID `jira-cli-credential`. Never
infer ownership from title/tags or delete a whole item. Names require a unique
exact match; existing IDs select identities directly. New selected items are
created as API Credentials only during explicit login, with a Jira CLI section
and jira-cli tag. Keep the concealed JSON record; section/tag metadata never
establishes ownership. Existing item categories, sections, tags and unmanaged
fields stay intact. Private recovery backups remain Secure Notes.
SDK failures retain inspectable causes but public errors suppress their text.

Selection constructs no SDK client. Stores share a lazy client per invocation.
stored `config get` and local `config set` never open the selected store.
Effective `config list` deliberately resolves it to report identity/provenance;
do not claim all configuration inspection is credential-free.
Queued store operations and SDK initializer admission honor cancellation. The
upstream cold WASM initialization and desktop IPC remain synchronous in parts.
Normalize caller cancellation causes lost by the SDK, preserve acknowledged
started writes, and never start a detached worker to simulate cancellation.
The upstream desktop core pins its first account; refuse another account in the
same process, including after failed initialization. Service accounts work in
no-CGO builds; Linux/macOS desktop authorization requires CGO. Actual platform
acceptance is separate from cross-compilation.

Runtime profile selection does not complete unmapped preferences or native
provider acceptance. Keep those evidence requirements separate.

## Saved queries

`LoadQueries` reads literal `.jql` filename stems in filename order through the
same opened-descriptor reader as settings files. Regular symlinks are supported;
nonregular targets are refused before reading. Unix FIFO refusal does not wait
for a writer. A pathname replacement after opening leaves the read on its
original regular descriptor; this is not a filesystem compare-and-swap.
Preserve native read causes, nil results on failure, metadata and authored JQL.
Check cancellation before reads and before returning; no credentials or Jira
client are needed for local query discovery or `--as-jql`.

## Theme selection

`internal/cli/theme.go` owns theme resolution; `config theme` uses the shared
setting and custom-theme owners. Follow the `configure-theme` embedded guide.

*   Theme environment precedence is `JIRA_THEME`, `CLIB_THEME`, `CLOG_THEME`,
    then `THEME`. A usable environment preset precedes the file preset.
    Library-only names can be consumed at startup; do not promise perfect
    settings provenance for a variable intentionally removed before loading.
*   Custom `theme_path` resolves relative to the selected configuration file.
    Invalid custom palettes warn and retain the preset fallback.
*   Without a selected theme, use `COLORFGBG` and then the dark fallback.
    Do not introduce an active terminal-background probe during startup.
*   Invalid theme names warn at rendering; invalid glyph vocabulary fails
    decoding. Preserve that intentional distinction.

## Changing the contract

Update supported keys, defaults, validators, provenance, typed outputs,
help/schema/completion and executable guide examples together. Preserve strict
file decoding and provide a verified upgrade path for breaking changes.
The embedded guides and runtime schema describe current user workflows;
these rules identify their implementation owners and invariants.

## Migration command delivery

`config migrate` requires an explicit `--source` and retains all legacy profiles
by default. Optional `--source-profile` limits legacy input; global `--profile`
continues to select runtime settings. A flat source becomes one named profile.
`--config` selects the destination; `--legacy-keyring-service` names historical storage without
persisting environment values. Live conversion requires an unused explicit
`--recovery-dir` and headless `--yes`. Validate confirmation gates before input
reads; publish protected recovery and intent before opening providers.

Preview reports mapped/unmapped declarations and readiness without providers,
directory creation or prompts. Unmapped runtime preferences refuse live
conversion. Historical token names map to public flat references. Readiness is not
credential verification. `config rollback --journal` reopens recorded paths and
provider identities; current global configuration and environment cannot retarget
restoration. Its preview explicitly reports required credential inspection.

`core.MigrationReceipt`, effect and recovery-path DTOs expose public identities
and effect facts only. Typed envelope outputs retain meaningful partial receipts
on errors, including non-durable outcomes. Recovery diagnosis owns its remediation
ahead of generic credential-provider fallback; context and output-write failures
keep their existing higher priority. Never infer successful restoration from
preview readiness or a pending effect.

Legacy profile completion uses `LegacyProfileNames` and only the explicit source.
The source flag must be forwarded in completion metadata. Missing, malformed or
secret-bearing input produces no candidates and never opens a provider.

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
cache construction or implicit remote warming. Raw/saved queries override
default_board before cache lookup; explicit board/raw selectors remain exclusive.
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
Legacy theme paths remain literal names. Policy 8 preserves ~ and $ names
without shell expansion; policies 1–7 retain their original special-path refusal.
Remaining global TUI mappings require real runtime consumers.

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

Verified `auth whoami --save` uses `OpenAccountIdentityTarget` before lookup,
then one `PrepareSettings` candidate for all four identity fields. Refuse
conflicting authored site/account/cloud; bind the credential account instead of
replacing a privacy-hidden native email. Missing target resolution uses explicit
`Options.PermitMissingTarget` only in saving factories; ordinary reads still
refuse a missing `--config`. Never mutate the global flag layer or create a
placeholder to make authentication resolve. Use the normal credential and
transport owners, preserving context and provider laziness.
Preflight the selected saving target through `OpenFile` before credential
providers; on Windows a regular-file ancestor can otherwise look absent.

Check the original snapshot even for an unchanged identity candidate before
claiming it is saved. Use no-clobber `SettingsUpdate.Create` for absent targets.
Retain actual publication receipts: `written` can acknowledge creation without
durability, and `unknown` does not mean nothing changed. Local snapshot checks
are not an atomic lock against external writers or Jira compare-and-swap.

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

Legacy theme.path is literal stored metadata in v0.15.2; its renderers used only
theme.name. Mapping policy 8 preserves literal ~ and $ names through the existing
flat theme_path owner, rebasing relative paths to the explicit absolute source
directory. Preparation reads no palette, home or environment. Runtime custom
palettes still use literal paths relative to the selected settings directory.
Never use x/filepath.Expand for these values: it changes their file identity.
Policies 1–7 retain exact previous candidate derivation and special-path refusal
so existing recovery/rollback remains valid. TUI mappings require real consumers.

Windows Credential Manager caps each blob at 2,560 bytes. The keyring recovery
owner keeps ordinary/version-2 backups intact and publishes larger private v2
payloads as bounded encoded pieces with a version-3 descriptor last. Derive piece
names only from the validated parent backup ID; verify count, size and full digest
before reopening snapshots or mutating primary entries. Retain partial effects
and native admission on cancellation/failure; this is not a provider transaction
or compare-and-swap. Native tests use fresh random service names, synthetic
records and independent WinCred reads, never enumeration or the user's default
service. Run them in a separate test process so global mocks cannot earn credit.

Explicit CLI-only conversion uses `LegacyMigrationOptions{DeferTUI: true}` through
the shared preparation, recovery and transaction owners. `config migrate
--defer-tui` records mapping policy 9 and reports recognized global TUI declaration
names in `deferred`. Retain their exact values only in original source/recovery
bytes; never publish inert TUI settings or claim they became active. Ordinary
conversion remains strict policy 8. Policies 1–8 preserve their exact derivation
and TUI refusal; rederivation uses the recorded policy. Unknown declarations and
malformed shapes still refuse. The restored dashboard does not yet consume these
legacy TUI settings; their runtime mapping remains a separate milestone.

Windows configuration paths resolve existing objects through owned native
metadata handles before appending missing descendants. Directory junctions are
not ModeSymlink under the selected Go rules; EvalSymlinks alone cannot bind
their targets. Keep full read/write/delete sharing, join handle-close errors,
preserve required extended DOS/UNC naming syntax and refuse native resolution
failures. Retain literal trailing dots/spaces during absolute-path selection and
recursive parent composition; ordinary Windows full-path normalization changes
those names. Recheck the requested path before publication so retargeted junctions
cannot authorize a different destination. Keep this at the shared configuration
snapshot owner; command handlers must not add independent path workarounds.

Reuse `internal/nativepath` for absolute selection, strict existing-object
resolution and literal leaf composition. Only `nativepath.ErrMissing` from the
initial native open permits configuration's missing-descendant policy. A later
missing-shaped metadata or close error retains its actual outcome; never admit
all `os.ErrNotExist` errors as absence. Draft parents use strict resolution.

## Credential and recovery invariants

Logout remaining-source facts require an actual nonempty token. Derive both
remaining_source and env_token_remains from the same post-removal observation;
include an exposed environment fallback and never label an empty default as a
credential. Preserve lazy fallback resolution and acknowledged removal outcomes.

Scoped tokens use the shared cloud-ID validation and fixed gateway owners, keeping
site/browser/cache identity separate from the authenticated API route. Credential
record version 2 binds cloud scope; version 1 remains classic. Authored scope
changes, including explicit empty, cannot silently reuse a stored token. Migration
derives configuration and credential scope from its recorded mapping policy.
Public tenant discovery reads no tokens/cookies and follows no redirects; context,
timeouts and malformed-account errors retain their outcome through scoped fallback.

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
New login items use API Credentials with a Jira CLI section and jira-cli tag.
Retain the concealed JSON record and ID-based ownership; existing item layout
and private Secure Note backups stay intact.
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
candidate derivation and special-path refusal. Global TUI runtime mappings remain separate; explicit deferral follows the recorded mapping policy.

The selected refresh_interval and team_account_ids fields had no CLI polling/team-filter consumers in the
reviewed v0.15 source; do not invent automatic loops or broaden/narrow searches.
Global TUI refresh and other TUI runtime state remain outside CLI conversion.
Mapping policy 6 retains selected client metadata and authored team presence;
policies 1–5 keep original candidate derivation and unsupported-field refusals.

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

Credential resolution carries command context through the store and lazy
client/cache factories. Keep `config.Load` local and use `config.Resolve` for
credentials; do not store contexts in options or adapters. Follow `.claude/rules/go.md`
for `command.OnceContextValues` and the synchronous OS-keyring limitation.

## Dashboard display preferences

`tui_preview`, `tui_status`, `tui_assignee`, `tui_labels`, `tui_priority` and
`tui_issue_type`, `tui_compact` and `tui_updated` are boolean display preferences, defaulting to true. They are
global declarations at the document root, including named-profile files. Ordinary
environment precedence applies. The dashboard projects these public values and
sources from the invocation's resolved settings; it does not expose credentials.
Its searchable settings page exposes every config key, edits all public writable
scalars through `ValidateSettingValue`, and explains the dedicated auth and alias
owners for tokens and aliases. It projects only writable public values into
screen state; never pass token values or alias definitions to the scalar editor.
Closed scalar choices use a menu with the current value selected; text editors
remain for free-form values. Keep choice admission bound to its Settings page.
The heading counts distinct draft changes. Selected public values and their
source appear beneath the list, using shared redaction and bounded wrapping.
Explain display options hidden by the current view or available list width.
Display preferences and named themes preview immediately. The creation type
picker uses `default_issue_type` to preselect an exact native ID or a unique
case-insensitive name without filtering or accepting the choices.
Missing or ambiguous defaults leave the explicit picker available.
After publication, reopen the pinned selected profile through a fresh composition-root resolver and
ordinary client factory. Adopt its client, read-only policy, timeout, project
scope, query paths, glyphs and prepared palette inside the running Tea session.
Do not imply credential migration/verification or invent consumers for retained
metadata. Ctrl+R retries reload without publication, including explicit external
file changes; no automatic file watcher is enabled.
Give initial and replacement runtimes separate child read contexts; cancel the
old runtime only after a replacement is prepared, and reject old task/timer
messages across model replacements. Keep local drafts/history scoped by public
site/account/API identity. Save/reload cannot overlap an admitted Jira write.
Preserve a successful publication when snapshot reopening or runtime preparation
fails. Keep the old connection for reads, block Jira actions, and show a persistent
saved-versus-active failure indicator until explicit reload succeeds.
The page discards unsaved changes on exit,
and publishes only changed keys together on Ctrl+S using `File.PrepareSettings`.
Keep the captured snapshot until successful publication, then advance it. Failed
publication retains the draft and directs inspection before retrying. Environment and flag
values remain visible but locked. These new keys do not silently migrate deferred
v0.15 preferences or enable polling.

TUI saved searches use `queries_path`, `LoadQueries` and `SaveQuery`. New saves
reuse the shared validated no-clobber publication owner with query admission,
not configuration TOML admission. Never overwrite an existing name or symlink.
Missing query directories yield an empty menu alongside built-in lenses; other
read failures remain visible. Recent issues and comment drafts stay in session
memory. Theme highlighting previews immediately through the existing palette
owner without changing the accepted draft. Enter accepts; picker cancellation
restores the exact palette from before opening it, including an existing draft.
Settings cancellation restores the last saved baseline, including syntax colors.
Settings save publishes the concrete base preset and selected custom path as one
transaction; built-in selection clears the path. Named TOML themes live in
`themes` beside the selected configuration. The optional `name` field is display
metadata; renaming never moves the selected file. Discovery retains invalid
or disappeared entries as visible failures without editable definitions, and
bounds input before snapshot reads and rechecks. Cancellation aborts discovery
without publishing a partial library, including the final read and skipped entries.
Create/import/export use shared validated no-clobber publication; edit uses the
captured snapshot, checking even unchanged candidates and matching post-save
bytes before returning a new snapshot. Preserve publication outcomes after
post-write failures; these checks do not establish filesystem compare-and-swap.
Theme editor Save explicitly publishes its file; cancelling Settings does not
remove an explicitly saved theme. Import retains source files and export refuses
existing paths. Preserve literal path bytes in interactive file input. Retain accepted startup theme-environment ownership
after library cleanup so the interactive row remains honestly locked.
