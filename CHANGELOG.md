# Release Notes


## [0.16.0-beta.0](https://github.com/matcra587/jira-cli/releases/tag/v0.16.0-beta.0) — 2026-10-21

### Breaking Changes

- Preserve the same payload in JSON and compact success output, including explicit nulls and native Jira field names and values. Replace reduced read projections and recursive null stripping with typed operation-specific data; update consumers for new payload shapes and `meta.contract_version`, independently of release version. Preserve absent/null/false/zero/empty distinctions, unknown native members and exact numbers. Help, completion scripts, raw token export and explicit jq projections remain protocol exceptions.
- Replace `version --detailed` human output with `version --output=json` or `--output=compact` for build provenance. Human `version` now matches `-V`; machine fields include commit, dirty state and build date when stamped, plus Go version and target platform. Update consumers of the former branch/build-by/build-time summary fields.
- Keep failure envelopes unfiltered even when `--jq` is supplied, and retain completed operation data when filter evaluation fails. Update scripts that previously filtered error frames inside the CLI.
- Use versioned named profile tables with stable credential references. Select a profile with `--profile` or `JIRA_PROFILE` and a file with `--config`. Existing flat settings remain readable. Convert legacy configurations with `config migrate --source FILE`, retaining all profiles by default; `--source-profile NAME` limits input. Use explicit `api_token_env` references instead of implicit profile-derived token variables.
- Use duration-valued flat timeout settings with an unconfigured default of `0s` (no request timeout), replacing the legacy 30-second default. Set `--timeout 30s` or the flat setting explicitly when that bound is required. Legacy profile migration retains the historical 30-second fallback rather than silently inheriting the new default. The flag now bounds each HTTP request rather than the whole invocation; it no longer imposes a total batch, polling, editor or input deadline. Setting `--timeout 30s` therefore does not restore the former whole-command deadline.
- Change `auth token` from redacted diagnostics to raw token export on stdout, without a JSON envelope. Use `auth status` for credential inspection; do not capture token export in logs.
- Use explicit resource subcommands: `boards list`, `issue comment add`, `issue weblink add` and `issue transition list`. Replace `search jql QUERY` with `issue list --jql QUERY` and `search saved NAME` with `issue list --saved NAME`. Replace `issue watch KEY` / `issue unwatch KEY` with `issue watchers add KEY --user @me` / `issue watchers remove KEY --user @me`. Runtime help and schema describe the new paths and operation-specific receipts.
- Treat mixed keyed batches as outer success while retaining ordered per-target outcomes. Inspect every `results[].ok`, retained `data` and `error`, even after exit 0; failed rows can contain acknowledged writes. Total failure remains nonzero.
- Replace legacy watcher name/email `--user` lookup with explicit `--user-query NAME_OR_EMAIL`. `--user` now takes a literal account ID or `@me` and cannot combine with `--user-query`. Explicit `--validate-remote` on an ID checks that exact active, nondeleted account. Query lookup requires a complete unique visible match; it does not prove exact email equality or assignability.
- Retain native Jira `fields`/`update` input, but remove the former flat convenience-key aliases and implicit scalar coercion from mutation payloads. Use explicit wire objects or `issue input prepare` with metadata; numeric-looking strings never imply an ID. Refuse fields supplied by both JSON and flags across either container. Repeatable edit collection flags add values, matching `--remove-*` flags remove them, and native JSON performs whole-list replacement; a value in both add/remove sets is refused.
- Replace whole native issue-link JSON documents with explicit issue keys, `--to` and a link name or directional phrase through `--type`. `issue link create --json-input` now supplies only an optional comment; `type`, `inwardIssue` and `outwardIssue` in that file are refused instead of selecting endpoints. Native link-type IDs are not accepted as name substitutes. Existing scripts using native endpoint/type JSON must change their argv; comment JSON support itself already existed.
- Use `--assignee @me` for current-user resolution instead of `--assignee me`, and `--unassign` instead of `--assignee none` or `--assignee unassigned`. Other write account flags also use `@me`; literal account IDs stay literal. Local previews may use a matching bound cached identity with an unverified warning. The separate JQL-filter grammar keeps its own `me`/`none` meanings.
- Replace `issue attachment download KEY ID --to FILE --force` with `issue attachment download ID --to DIR --as NAME --overwrite`; explicit destination directories may lie outside the current directory under the bound-directory checks. Replace `issue attachment delete KEY ID` with `issue attachment delete ID`. Preserve stdout receipts separately from downloaded bytes; inspect collision/overwrite outcomes before retrying.
- Use canonical `--output` instead of `-o`. Select a named profile with `--profile` or `-P`, independently of the file selected by `--config`. Review command help for renamed flags and explicit operation subcommands before reusing older scripts.
- Change `adf convert` compact success from a bare ADF document to an object containing `document` and diagnostics. Extract `.data.document` in JSON mode or `.document` in compact mode before piping to mutation input, for example `adf convert --input notes.md --output=compact --jq .document`. Strict failures retain the candidate and diagnostics; do not submit an error envelope as a body.
- Change the bounded issue-search drain from 100 pages / 10,000 issues to 50 pages / 5,000 issues. Existing `--unbounded` removes those safety caps explicitly; it is not a new flag. Keep truncation and continuation receipts visible instead of treating the cap as a completed search.
- Refuse CSV flag inputs that discard records or normalize line endings before uploads or mutations. Preserve quoted literal LF/CR only when the codec can represent it unchanged; use repeated flags or a literal input route for otherwise unrepresentable values. Shell quoting/completion does not waive this input contract.
- Change editor precedence from `JIRA_EDITOR`, configured editor, `EDITOR`, `VISUAL`, fallback `vi` to `JIRA_EDITOR`, configured editor, `VISUAL`, `EDITOR`, with no fallback. Configure an executable explicitly when none is selected; preserve literal argv rather than running a shell command. VISUAL support itself already existed.
- Stop automatically normalizing or validating create/edit values through live screen metadata on ordinary writes; Jira validates native values. Explicit `--validate-remote` checks requested field IDs and operation names against complete authenticated context metadata. Preserve payload bytes and report checked IDs without claiming requiredness, permissions, value validity or future write success.

### Added

- Add `--jq-arg` and `--jq-argjson` for binding values without splicing content into filter source. Preserve large integers and bound buffered filter output.
- Add `config list` for every effective setting, its supplying layer and the selected config path, with secret redaction and credential-store availability warnings. Keep `config get` as stored-file inspection, preserving missing versus explicit empty/false/zero values rather than confusing them with effective defaults. Existing config get/set commands are retained under flat keys, not newly introduced.
- Add `config edit` with a validated scratch candidate, changed-source refusal and retained rejected edits for recovery. Its credential-free `--dry-run` reports the selected file/editor without opening anything. Live editing requires an interactive terminal; failed or canceled editors preserve saved work instead of silently replacing the original settings.
- Add `issue view --comments N` for the latest N inline comments: the default is one, and `--comments 0` shows only a count hint. Reuse embedded pages where sufficient; supplemental preview reads are human-output work. Explicit `--comments` takes precedence over comment field selection, and embedded-page shortfalls stay visible instead of claiming a complete thread.
- Add the CLI `glyphs=default|nerd` setting, including Powerline status-pill caps and aligned pill widths. Reject invalid values while accepting case/space-normalized spellings. Existing TUI Nerd Font/icon preferences did not provide this CLI setting.
- Expand create/edit/clone field flags with reporter, due date, components, fix/affects versions and original/remaining estimates; clone accepts the create flags as overrides. Add edit collection add/remove pairs and explicit `--unassign`. Add `--notify=false` for issue/comment edits; its receipt records requested suppression, not proof Jira honored it, because Jira permissions control delivery.
- Add `issue weblink list` and `delete` alongside the existing add operation, and accept full native application, icon, status and extension data through JSON input.
- Add comment-list `--order` for newest-first or oldest-first results. Keep whole-thread `--all` separate from an explicitly selected page size/offset; incompatible combinations refuse instead of silently choosing one. Comment-list `--all` already existed in v0.15.
- Add repeatable `issue clone --replace old=new`, applied in order after field overrides to the final summary only. Warn on unmatched replacements; do not rewrite descriptions or arbitrary custom fields. JSON clone overrides and custom-field copying already existed.
- Add `--jql`, saved-filter-ID `--filter` and `--from-file` selectors on issue edit, delete, transition, clone and comment add. Selectors are exclusive with one another and positional keys. Resolve and display the complete unique target set before confirmation; refuse more than 100 selector targets or incomplete searches. Bare query previews report unresolved targets, while `--dry-run --validate-remote` resolves without writing. File selectors stay local, literal and deduplicated.
- Add explicit platform/Agile `api` methods with local previews, literal repeated queries, exact body bytes, explicit HTTP preconditions and bounded response capture. Retain permitted headers and exact opaque/binary/empty response bytes, incomplete captures and failure receipts without exposing credential/cookie headers. Distinguish HTTP 412 and attempted work from actual HTTP entry. A precondition protects a write only when the remote server enforces it; a later read or local merge is not server-enforced protection.
- Add native asynchronous `issue move` with exact bulk payloads, submission/task receipts, bounded `--wait`/`--max-polls` observation and `issue move status` to resume reads without resubmitting. Validate source/destination identities and retain acknowledgements on polling failure. Use `issue move fields` for the former confirmed field-payload operation; previewing a payload does not establish remote acceptance or completed movement.
- Add local `adf edit prepare`, `apply` and `merge` to preserve protected native ADF through anchored Markdown edits and refuse conflicts without partial candidates. These commands do not write to Jira.
- Add local `adf validate`, `adf lint` and `adf explain-node` for bounded native validation, schema hashes, diagnostic locations and context-definition inspection. Valid native documents may still have projection warnings and exit 0; inspect `projection_available` and diagnostics rather than treating absent projection as a clean one. These commands require no Jira credentials or requests.
- Add create/edit `--generate-json` to inspect complete native screen metadata and produce separate fillable request skeletons without submitting changes. Keep metadata and placeholders distinct from validated input.
- Complete fresh transition IDs offline from explicit transition-list reads. Bind hints to issue, site and account, expire them after five minutes, and expose inspection/clearing through the cache surface. Invalidate completed failed observations and attempted writes without treating cancellation or unstarted reads as completed discovery. Hints never authorize a write.
- Convert legacy or flat configuration to named profiles with `config migrate --source FILE` and restore exact preimages with `config rollback --journal FILE`. Preserve all legacy profiles by default, protected configuration preimages, per-credential effect journals and provider-owned backups.
- Add `issue edit prepare`, `draft`, `inspect` and `resume` for descriptions bound to immutable issue ID, site/account and authenticated API route. Preserve exact source bytes, null and unknown ADF state; keep saved Markdown and source before publishing a manifest and never overwrite existing members. Inspection stays local unless `--refresh` requests a bound reread. Editor operations require a terminal before credentials/artifacts; previews publish nothing and launch no editor. These commands produce candidates, not Jira writes or concurrency protection.
- Add local `issue input prepare` using explicit create/edit metadata. Convert supported scalar authoring into known native shapes with fixed identity meanings; refuse malformed known values, duplicate/wrapper collisions and partial candidates. Recognize exact built-in plugin identities rather than marketplace suffixes or digits. Preserve unsupported plugins, clears, update operations and unchecked values as native data with unverified provenance. Ordinary mutation input remains lossless and performs no implicit coercion.

### Changed

- Render issues, comments and ADF through shared terminal views with status-category pills, Jira priority indicators and width-aware wrapping. Preserve complete text in narrow rows, labeled fields and section titles; align counts and columns by display width. Sanitize untrusted names, text and links while keeping machine data native. Preserve native mentions/emoji, layouts, expanded content and task/decision state rather than presenting their existing basic support as a new feature.
- Retain native page metadata, explicit truncation/continuation and validated partial results after later failures. Walk advancing cursors through empty pages; reject repeated or malformed pages. Refuse selector-driven writes unless their complete unique target set is known. Separate requested output windows, fetched rows, count estimates and authoritative completion; do not infer the end of a collection from a short or empty page alone.
- Identify rank fields through their native schema instead of dropping any custom-field string that resembles a rank token. Preserve ordinary matching text, assignee and eligible custom fields while retaining lifecycle exclusions. Explicit-key cloning no longer requires blanket destructive confirmation; selector-based cloning still confirms its resolved scope. Local previews do not claim copied fields they have not read.
- Keep conversion and verification advisories tied to their target and invocation, with ordered typed results instead of a shared process-wide warning queue. Retain Jira near-limit warnings across retries and failures; successful compact output remains data-only, so use JSON when monitoring warnings.
- Resolve configured default boards only from existing public snapshots in the selected account namespace, preserving site/name spelling and case-insensitive name lookup without guessing IDs. Refuse ambiguous identities, malformed matching records, incomplete inventories and cancellation. Structured list/mine inherits project only without raw/saved query or active board; local JQL keeps its independent grammar. Live use binds the snapshot site/account to the constructed client; local missing-cache refusal remains credential-free.
- Expand native ADF status colors beyond the six named values to exact `#RRGGBB` values, including mixed-case hex. Refuse shorthand, alpha, missing-hash and whitespace-padded colors; preserve native attributes separately from optional Markdown status authoring.
- Retain ordered per-target data, created resource IDs, attempted work and native acknowledgements after failures or interruption across issue resources and worklogs. Keep issue-link directions, attachment/web-link identities and source/new-clone identities explicit. Preserve incomplete usable acknowledgements instead of inventing complete resources or no effect. Inspect uncertain outcomes before further writes; do not replay an entire batch or automatically roll back acknowledged work.
- Describe the assembled command tree and typed output shapes through credential-free runtime discovery, including bare machine-mode invocation. Return guide indexes and documents through the shared JSON contract; retain guide export with explicit collision and partial-publication receipts.
- Expand field-type and ADF capability discovery with native value examples, schema contexts, structural bounds and separate authoring, rendering and preservation capabilities.
- Preserve complete native request members beyond `fields`/`update`, including `properties`, `historyMetadata`, transition data and unknown extensions. Keep explicit empty containers and property-only edits; refuse malformed known containers and field/input collisions before Jira. Native field input existed before, but the old field-only extraction discarded these sibling request members.
- Retain complete native issue, account, epic, comment, attachment, watcher and worklog values instead of reduced read projections. Report paging and completeness separately from successful resource reads.
- Admit explicit JQL validation as a read in read-only mode even though Jira uses POST at `/rest/api/3/jql/parse`; v0.15 refused it through a method-based write guard. This classification does not permit arbitrary POST requests or authorize a mutation.
- Supervise editors and accessible/pipe input with cancellation, exact terminal/descriptor restoration and recoverable saved work. Keep prompt text on stderr for machine modes; refuse incomplete EOF answers and publishing partial answers after cancellation. Preserve literal editor argv, early executable validation and actual process outcomes. Native Windows console/pipe ownership and Unix flags/deadlines remain separate platform paths; cross-compilation alone is not native behavior evidence.
- Protect settings, caches, downloads and drafts with bound file/directory identities and retained publication outcomes. Preserve literal Windows junction paths and trailing-dot/space names, refuse substituted nonregular inputs, and retain existing attachment files after failed downloads or pre-publication overwrite failures. A later durability failure retains the acknowledged new file; inspect its receipt before retrying.
- Retain ordered per-issue rank receipts through sequential native requests of at most 50 issues. Chain successors after the prior last issue and stop them after failure, partial/malformed reply or cancellation. Report acknowledged and unsubmitted moves separately. Large ordered ranking already existed in v0.15; richer receipts do not make an uncertain rank request safe to replay.
- Retain observed archive/Homebrew installation outcomes on failed or interrupted updates, including acknowledged executable publication and retained backups. Bind archive replacement to the resolved executable and verified release bytes; Homebrew updates the verified formula while preserving old kegs and shadowing PATH copies. Dry runs may contact the public release service but do not download/install; managed installers report their own command without GitHub access. Checksummed updates already existed; the change is guarded publication and inspectable outcomes, never automatic replay.
- Scope metadata and recent-issue caches by public site/account rather than legacy profile namespaces. Add local `cache inspect [RESOURCE]` for inventory, malformed-entry and validated freshness facts; inspection completeness does not establish a remote collection walk. Preserve native snapshots and prior entries after failed refreshes, and retain selective-clear/partial-publication receipts. Help, schemas and completion stay credential-free; bare previews inspect local freshness without remote warming.
- Improve Bash, Zsh, Fish, PowerShell and Elvish completion for literal filenames, Unicode, inline values, quoted CSV and directory continuation. Separate candidate identity from display descriptions and transport literal argv without shell evaluation. Include contextual attachment/theme/source paths, nested help, human columns and scoped cached metadata. Handle legacy Bash quoting/spacing, Fish version differences, PowerShell host encoding and Elvish Windows callbacks explicitly. Generator availability does not establish native acceptance on every host or Nushell acceptance.
- Extend Markdown authoring with GitHub alerts for native panels and optional `:status[label]{color=green}` directives. Preserve status text, color, local IDs/styles and explicit attribute presence in structurally checked projections and anchored edits; disclose loss instead of treating Markdown review as a lossless round trip. Render native panels, status labels and merged tables without truncating cell text, and preserve literal escapes/references without changing code content.
- Refuse existing config initialization targets, including files, directories and dangling links, in previews and live execution. Publish a new target exclusively so a competing creator is not overwritten. Retain an acknowledged creation if later durability fails; a failed receipt does not always mean no file exists.
- Retain classic/scoped token and direct 1Password SDK support with profile-selected credential locations, lazy context-aware resolution and explicit provider provenance. Keep tokens bound to their site, account and cloud scope; profile renames retain credential references.
- Bind cached account IDs and stored credentials independently to public site, account and cloud scope. Retain verified public saving with `auth whoami --save` under the new independent scope binding; login publishes routing, provider selectors/reference and the native bound account for fresh-process use, with separate credential/settings receipts. Bare local previews may use only a matching cached ID with unverified provenance; live and `--validate-remote` resolution uses one valid Jira current-user reply. Refuse blank/malformed identities before account-dependent writes; privacy-hidden native email stays native.
- Retain selected project/type/board defaults, client refresh/team metadata, cached account scope and automatic themes through versioned migration policies. Fill only absent create fields across `fields`/`update`, never edit/clone; explicit empty flags suppress defaults. Require typed TOML team arrays or strict JSON string arrays for environment/argv, preserving order, duplicates, commas and explicit empty arrays. Retain legacy nonpositive refresh fallback of 30 seconds while requiring positive current values. Retention adds no implicit polling or team filtering.
- Resolve automatic themes from passive `COLORFGBG` background hints with a fixed dark fallback instead of active terminal queries. Resolve help and syntax palettes together, preserve exact `JIRA_THEME=auto` provenance and environment precedence, and serialize temporary framework selection. Keep custom paths literal: migration anchors relative paths to the explicit source directory without expanding `~` or `$`, while old recovery policies retain their original derivation.
- Retain recoverable Windows credentials larger than the 2,560-byte Credential Manager blob limit through bounded encoded backup pieces and a descriptor published last. Verify count, size and full digest before reopening or restoring. Keep provider-owned snapshots private, preserve exact namespaces/owned-field metadata and report partial effects; the backup protocol is not a provider transaction or compare-and-swap.
- Replace create/edit verification projections with sorted `verified.fields` rows reporting matches, differs or unverified and native observed values. Read requested field/update IDs plus shared status/summary/updated once; distinguish absent/null states and exact numeric spelling. Compare complete membership for collection replacement and supported final membership for add/remove without rejecting untouched members. Unknown/ambiguous updates, unavailable fields and different ADF remain unverified. Retain acknowledged writes and warnings after failed readback; a later difference neither proves a Jira drop nor prevents concurrent changes.
- Retain bare assignee email support while replacing a single search page with a complete bounded visible-user walk. Refuse ambiguous, duplicate, malformed, incomplete or exhausted search windows instead of choosing a partial match. Local previews retain unresolved email with a warning; live or explicit remote validation resolves it. Privacy-hidden email remains native and lookup does not establish exact email equality or assignability.
- Create new 1Password login items as API Credentials. Keep existing item categories, concealed credential records and unrelated fields intact; private migration and rollback backups remain Secure Notes.
- Group the concealed JSON credential in a Jira CLI section and tag new 1Password login items jira-cli. Preserve existing item sections, tags and unrelated fields; organisational metadata never establishes credential ownership.

### Fixed

- Allow one transition invocation to change status and post a comment when the workflow has no transition screen. Report the separate non-atomic comment request, preserve a successful status change if the comment fails, and offer `--atomic-only` to refuse the split.
- Honor browser output for saved queries through `issue list --saved NAME --web`; the former `search saved --web` accepted the flag but ran the search instead.

### Removed

- Remove automatic description submission from bare `issue edit`. Supply authored changes or use explicit description preparation and draft commands; draft editing does not apply changes to Jira.
- Remove the `tui` dashboard and `-i` entry point from this CLI layout. Legacy TUI settings are not executed; `config migrate --defer-tui` retains them in protected recovery for later conversion.
- Remove the former global `--debug` / `-d` HTTP trace surface from this layout. Existing trace-capture scripts must change; JSON envelopes and retained operation receipts are not a replacement for a request/response trace.
- Replace `auth switch` with explicit `--profile` selection or `config profile default`. Remove `auth refresh` and provider-to-provider `auth migrate`; `config migrate` converts a configuration with recovery and does not move credentials between providers.
- Remove the dedicated `--tsv` table-output flag. Use a success-payload `--jq` projection with jq `@tsv` when tab-separated output is required; failures retain their JSON envelopes.

### Security

- Harden authenticated redirects and metadata reads so credential-free or out-of-scope responses cannot establish account, screen or editing identity. Bound redirect chains and retain exact failure causes without automatically replaying writes.
- Build with Go 1.27.3 and require the patched compiler for source installations.

### Dependencies

- Update Go, dependencies and build tools; migrate terminal rendering to the current Gechr APIs and repair CI contract-test races.

## [0.15.2](https://github.com/matcra587/jira-cli/releases/tag/v0.15.2) — 2026-07-28

### Fixed

- Show the sentence each issue's page will render when creating or previewing an issue link, so a backwards link direction is visible before it lands
- Return the created timestamp everywhere it was silently dropped - full and *all payloads now round-trip every Jira system field, created joins the default list and search projection, and issue list gains a created column
- Accept --fields on jira issue view to fetch only the named fields, closing the selector parity gap with search jql

## [0.15.1](https://github.com/matcra587/jira-cli/releases/tag/v0.15.1) — 2026-07-27

### Changed

- Include the issue key in comment-list data and human headings.
- Include issue keys in attachment and watcher list data and headings.
- Keep validated issue mutation context in successful live output.
- Keep validated collaboration context in successful live output.
- Publish typed output schemas for `jira agent adf-matrix` and `jira agent fieldtypes`.

### Fixed

- Report whether Jira screen metadata actually validated fields in `validated_remotely`.

## [0.15.0](https://github.com/matcra587/jira-cli/releases/tag/v0.15.0) — 2026-07-23

### Fixed

- Return output destination failures from machine and human renderers.
- Report data, file, and terminal failures instead of silently continuing.
- Return failed help and diagnostic writes as local output errors.

## [0.14.2](https://github.com/matcra587/jira-cli/releases/tag/v0.14.2) — 2026-07-22

### Fixed

- Shell completion offers values for the agent surface's guide-section, schema-path, export-format, and export-scope flags, which the clib-driven completion path previously left empty
- Agent guide exports use jira-prefixed skill names, scoped exports can explicitly select Claude Code or Codex, and bundled guides accurately describe live and local behavior

## [0.14.1](https://github.com/matcra587/jira-cli/releases/tag/v0.14.1) — 2026-07-22

### Fixed

- Route ADF and custom-field registry lookups from the jira-how-to skill, pre-approve its discovery commands in full, and pin its hard-won never-list; the stale in-repo jira-cli router skill is gone (plugin 0.1.1)
- Close twelve behavior gaps the old agent guide covered and the rewrite lost — screenless-transition recovery, create-screen vs instance-wide issue types, search-only field selectors, worklog workday semantics, wiki-markup normalization, rank chunk resume, scoped-token cloud_id recovery, positional key ranges, and more — and document that `issue move` cannot change project or type on Jira Cloud today (the edit API silently ignores those fields); clone and delete instead

## [0.14.0](https://github.com/matcra587/jira-cli/releases/tag/v0.14.0) — 2026-07-22

### Added

- Browse the embedded agent guides as a human with the new top-level `jira guide` command — themed Markdown rendering on a terminal, raw Markdown for pipes — and export them as agent skills with `jira agent export`
- Ship the jira-how-to router skill under skills/ with a Claude plugin manifest — it steers any Jira task to the right embedded guide and the runtime schema, pre-approving the discovery commands it teaches

### Changed

- Serve the agent surface through docent: nine rewritten workflow guides under `jira agent guide`, and a runtime-derived `jira agent schema` with per-command input/output schemas embedded in the command tree (agent contract 3.0.0; the previous envelope-wrapped schema shape is gone)
- Emit the agent schema structure-first via docent v0.3.0: the full tree carries shape markers, `--path` returns a command's embedded schemas, and `--shapes` captures everything with repeated bodies pooled into $defs. In model-token terms the guide corpus is 87% smaller than the pre-docent surface and the full discovery contract half the size, with no behavior claim lost

## [0.13.1](https://github.com/matcra587/jira-cli/releases/tag/v0.13.1) — 2026-07-21

### Added

- jira agent schema publishes an output schema for every operation, derived from the same typed definitions the CLI emits — previously ~40 operations had no declared output shape

### Changed

- issue delete no longer emits a meaningless result: null on live deletes; the key is simply absent, matching dry-run

### Dependencies

- golang.org/x/text 0.39.0 fixes GO-2026-5970 (infinite loop on invalid input)

## [0.13.0](https://github.com/matcra587/jira-cli/releases/tag/v0.13.0) — 2026-07-20

### Breaking Changes

- data.issue is now always an identity object carrying at least key — across issue edit, transition, comment, worklog, epic, weblink, and delete envelopes — where mutation envelopes previously returned the bare key string; read .data.issue.key everywhere; the identity and dry_run invariants are documented in the core contract guide and enforced by contract tests
- Attachment, link, and watcher envelopes now carry the issue identity as data.issue objects (link create reports inward_issue/outward_issue as objects); alias list wraps the map under data.aliases with a count; live delete, download, and watcher envelopes gained the issue identity and dry_run their dry-run counterparts already carried; worklog add --dry-run renames time_spent_seconds to the native timeSpentSeconds

### Fixed

- issue edit --verify reports an ADF description as unverified instead of returning an empty verification block that implied the field was checked
- auth logout reports the credential backend on live removals, update envelopes carry a managed discriminator on both shapes, and issue rank without a profile reports dry_run

## [0.12.2](https://github.com/matcra587/jira-cli/releases/tag/v0.12.2) — 2026-07-19

### Changed

- Agent detection for --output=auto now follows the cross-tool AGENT/AI_AGENT convention (a falsy value opts out) and recognises Replit; the Aider, Windsurf, Amazon Q, Codeium, and Copilot markers no longer trigger compact output

## [0.12.1](https://github.com/matcra587/jira-cli/releases/tag/v0.12.1) — 2026-07-17

### Added

- TUI new-issue form picks the issue type and offers live assignee and label suggestions, instead of silently defaulting to Task with free-text fields
- TUI shows a footer status trail and an activity-log overlay (L) for your mutations, with the affected issue key as a clickable link
- Choose the target project on the new-issue form; the issue-type list refetches to match the selection.
- Press ctrl+k in the TUI to open a command palette that fuzzy-finds and runs any command available in the current view

### Changed

- TUI create/comment forms keep your draft and show an inline error when a submit fails, mark optional fields, and validate inline
- Redesign the new-issue form with boxed fields and a pinned hint row that stays visible on short terminals.

### Fixed

- TUI Epics tab counts epics instead of the selected epic's child issues
- TUI dialogs wider than the terminal now show a 'terminal too narrow' notice instead of garbled, wrapped borders
- TUI pickers and the create form that open after a background fetch no longer capture keystrokes typed while waiting

## [0.12.0](https://github.com/matcra587/jira-cli/releases/tag/v0.12.0) — 2026-07-13

### Added

- Create issues (n), edit labels (l), and pause auto-refresh (R) from the TUI dashboard — create opens a two-field overlay (summary plus Markdown description), labels edit the full comma-separated list in place
- Ask before discarding edited TUI modal input: esc on a non-empty overlay now prompts discard input? (y)es (n)o
- TUI epics tab: cycle open epics in a strip and triage the active epic's children with the full verb set
- TUI board tab: the profile's default_board becomes its own tab once the boards cache is warm
- Hot-apply theme changes in the TUI: editing theme.name re-skins every view without a restart
- Choose the TUI glyph set with tui.icons: Nerd Font issue-type, priority, and pause icons, or the portable Unicode set (auto opts into Nerd Fonts only via the NERD_FONT convention)
- Interactive TUI Settings tab: pick themes with a live preview as you browse, change icons, preview dock, tabs (with name autocompletion) and more — every change validates, saves atomically, and applies without a restart; press e to edit the raw config.toml with the same validation

### Changed

- TUI issue list renders status as the same reverse-video pills as the detail header and the CLI's plain list
- Render TUI key hints with inline mnemonics — the binding letter is highlighted inside its verb, footer and detail view alike
- Open TUI comments in the in-app overlay first; with an external editor configured, ctrl+e continues the draft there instead of the editor opening unasked
- Style backtick code spans in TUI summary cells and detail headings with the markdown code accent

### Fixed

- TUI status pills use the same category-keyed fixed colors as the CLI's plain issue list, instead of theme colors keyed by guessing English status names
- Clamp TUI chrome rows (tabs, detail pills, hint lines) on narrow terminals instead of wrapping and breaking the layout

## [0.11.2](https://github.com/matcra587/jira-cli/releases/tag/v0.11.2) — 2026-07-13

### Fixed

- Report a misspelled subcommand as an unknown command with suggestions, instead of exiting silently with usage or blaming a trailing flag
- Style every flag mentioned in an error hint consistently — all flags now render as code spans

## [0.11.1](https://github.com/matcra587/jira-cli/releases/tag/v0.11.1) — 2026-07-12

### Fixed

- Apply the resolved --color mode and the standard human-output formatting (omit-empty fields, smart quotes, grouped numbers) to failure-diagnostics summaries, the warning mirror, and per-command list renderers, which previously auto-detected color and skipped those conventions
- Honor NO_COLOR on manually styled output — entity colors, status pills, hyperlinks, themed markdown — and resolve the auto theme from stdout's own state in split-stream invocations; --color=always still overrides both

## [0.11.0](https://github.com/matcra587/jira-cli/releases/tag/v0.11.0) — 2026-07-12

### Added

- Built-in --jq filter runs a jq expression over the JSON output in-process (gojq, gh CLI style): strings print raw, other results print as JSON per line, --output=json is implied, and failure envelopes are filtered with the exit code preserved
- New issue rank command reorders backlog issues headlessly: place keys before or after an anchor issue in LexoRank order, with ranges chunked transparently at the API's 50-issue limit and order preserved end-to-end

### Changed

- The foreign-flag hint no longer competes with its own suggestion: it says to use this CLI's equivalent, and the suggestion names it

## [0.10.15](https://github.com/matcra587/jira-cli/releases/tag/v0.10.15) — 2026-07-12

### Added

- Tab completion for issue KEY arguments offers recently used keys, recorded per profile as a side effect of list, view, search, and mutations; inspect with jira cache issuekeys, reset with jira cache clear issuekeys

## [0.10.14](https://github.com/matcra587/jira-cli/releases/tag/v0.10.14) — 2026-07-12

### Added

- Spinners and the multi-key progress footer show a live countdown of the remaining --timeout budget while a request is in flight
- Completion lines that carry summary fields (lists, mutations, multi-key batches) show the blocking round-trip time as a trailing elapsed field when it reaches one second; fast calls stay clean
- Successful Jira mutations complete with a distinct SCS success line, separating writes that landed from informational reads; dry-run previews and partial failures stay informational
- release-notes pages a full history taller than the screen on interactive terminals, like git log — built-in pager by default, JIRA_PAGER/PAGER honored, --no-pager to print directly; piped, machine-mode, and agent output always streams

### Changed

- Human output picks quote delimiters adaptively (values containing quotes render without backslash escaping) and groups digits in large numbers
- config get and config profile human output renders as syntax-highlighted TOML matching the config file's own shape
- Batch summary lines render succeeded as a gradient-colored fraction (succeeded=12/13) and show failed only when something failed

### Fixed

- Help text wraps at a readable measure (100 columns) on very wide terminals instead of running the full width; narrow terminals are unchanged
- Issue list completion line no longer prints detail=false noise; the detail field appears only when detail mode is on
- Status pills color by category consistently across issue list --detail and multi-key issue view, instead of drifting to arbitrary hues

## [0.10.13](https://github.com/matcra587/jira-cli/releases/tag/v0.10.13) — 2026-07-11

### Fixed

- Stop the auth login verification spinner from corrupting --debug output; verification narrates bare under debug like every other lifecycle
- Color assignee and other entity names with fixed mid-tone hues readable on both light and dark terminals, instead of the theme's background-specific palette

## [0.10.12](https://github.com/matcra587/jira-cli/releases/tag/v0.10.12) — 2026-07-11

### Fixed

- Apply --color=always/never to all human output on stdout (tables, pills, links, human-mode JSON, ADF views, mirrored warnings) — always styles piped output, never suppresses color and hyperlinks even on a terminal; mode values are accepted case-insensitively

### Dependencies

- Upgrade gechr libraries: clog v0.13.1, clib v0.6.6, x v0.2.18, primer v0.1.2

## [0.10.11](https://github.com/matcra587/jira-cli/releases/tag/v0.10.11) — 2026-07-11

### Fixed

- Narrate dry-run fan-outs as previews in --debug lifecycles and progress labels ("previewing issue edit") instead of claiming the mutation ran

## [0.10.10](https://github.com/matcra587/jira-cli/releases/tag/v0.10.10) — 2026-07-11

### Fixed

- Align TUI relative ages with the CLI compact format: sub-minute and future-skewed updates show "now", and 30-34 day ages show "4w"

## [0.10.9](https://github.com/matcra587/jira-cli/releases/tag/v0.10.9) — 2026-07-11

### Fixed

- Phrase dry-run previews in the conditional mood ("Would transition issue") instead of past-tense completion

## [0.10.8](https://github.com/matcra587/jira-cli/releases/tag/v0.10.8) — 2026-07-11

### Fixed

- Drop the redundant 'attachment download: validation:' prefix from the download overwrite refusal; it keeps its validation classification (exit 3) like the other --force gates
- Report the attachment download path-escape refusal as flag_value_invalid naming --to, instead of a generic validation failure
- Render status pills with fixed truecolor fills and luma-based text so contrast survives terminals that remap the 16-color ANSI palette

## [0.10.7](https://github.com/matcra587/jira-cli/releases/tag/v0.10.7) — 2026-07-11

### Added

- Add an env secret backend: jira auth login --backend env reads the profile's JIRA_TOKEN_<PROFILE> variable at run time and stores nothing — for WSL, headless Linux, containers, and op run-style secret injectors

### Fixed

- Offer only usable secret backends in interactive auth login — the keyring is dropped when no Secret Service answers (WSL, headless Linux) and 1Password in no-CGO builds — and fail headless logins early with actionable errors
- Honor the secret backend selected in the interactive auth login form instead of silently keeping the profile's stored backend
- Classify OS keyring failures as a typed keyring_unavailable error that points at the env backend, instead of surfacing raw Secret Service D-Bus text as a generic validation failure
- Report a nonexistent Atlassian site as jira_site_not_found with the offending host named, instead of relaying Atlassian's misleading 'Site temporarily unavailable' 404 — auth login verification and auth status probes now point at the base_url site name
- Shorten the post-login warning about an old credential that could not be revoked to the typed message instead of the full error chain
- Strip ANSI escapes and control bytes from Jira-sourced text in comment list previews, mirrored ADF warning lines, and debug failure reasons

### Dependencies

- Upgrade clive to v0.4.2, dropping go-selfupdate and its deprecated x/crypto/openpgp dependency (GO-2026-5932); the govulncheck advisory allowlist is retired now that no accepted advisories remain

## [0.10.6](https://github.com/matcra587/jira-cli/releases/tag/v0.10.6) — 2026-07-08

### Added

- Show a description and worked examples in --help for each top-level command group (issue, config, cache, auth, and the rest).

### Changed

- Require --force to delete an alias in headless, agent, or --no-input mode, matching cache clear; interactive terminals are unaffected.

## [0.10.5](https://github.com/matcra587/jira-cli/releases/tag/v0.10.5) — 2026-07-08

### Added

- Add --dry-run to the local-state mutations that lacked it: alias set/delete/import, config init, config theme, and cache refresh now preview the change and write nothing under --dry-run, reporting data.dry_run true. cache refresh --dry-run reports which resources are stale without contacting Jira.

### Security

- Build with Go 1.26.5 to pick up its crypto/tls security fixes.

## [0.10.4](https://github.com/matcra587/jira-cli/releases/tag/v0.10.4) — 2026-07-08

### Fixed

- Sharpen error guidance: the generic Jira 404 hint no longer assumes the identifier is an issue key (it also covers boards, attachments, users, comments, and links); a --type value with no match on a project's create screen now exits 3 (validation) with the valid issue types offered as suggestions instead of a misleading not-found; and an unknown search saved name points to the queries directory rather than --help.

## [0.10.3](https://github.com/matcra587/jira-cli/releases/tag/v0.10.3) — 2026-07-08

### Fixed

- Surface write failures (broken pipe, disk quota) on paginated list and search envelopes instead of silently swallowing them.

## [0.10.2](https://github.com/matcra587/jira-cli/releases/tag/v0.10.2) — 2026-07-08

### Added

- Extend shell tab-completion: the issue transition status argument (--transition) now completes status names, and search saved completes saved-query names.

## [0.10.1](https://github.com/matcra587/jira-cli/releases/tag/v0.10.1) — 2026-07-08

### Fixed

- Locally-detected command errors now carry typed classifications instead of being guessed from their message text, which fixes five wrong exit codes. `auth login` with a `--backend` that differs from the profile's stored backend, and `issue edit --assignee me` on a profile with no saved account id, now exit 3 (validation, `flag_value_invalid`) instead of 1 (auth). `auth whoami` against a profile that has no base URL now exits 2 (`profile_incomplete`) instead of 1 (auth). `jira agent guide` with an unknown section and `jira search saved` with an unknown query name now exit 3 (validation, `arg_value_invalid`) instead of 2 (not found) — the lookups are local, not Jira resources. Every `--force` gate keeps its `validation_failed`/exit 3 contract, and a genuinely absent Jira user on watcher commands keeps `not_found`/exit 2.
- The command error classifier no longer guesses an error's category from its message text — a fragile heuristic that misrouted some validation errors to the wrong exit code. A bad `auth_type` (rejected on config load or via `config set`) and `auth login` with the conflicting `--secret-stdin` and `--credential-env` flags now exit 3 (validation) instead of 1 (auth), where before the word "auth" or the flag name "credential-env" pushed them onto the authentication path. Multi-key batch commands that partially fail now carry the exit code of their worst failure directly instead of re-deriving it from the summary line, so mixed-failure batches classify correctly. Any error the CLI does not explicitly classify now defaults to validation (exit 3) rather than being sorted by keyword.

## [0.10.0](https://github.com/matcra587/jira-cli/releases/tag/v0.10.0) — 2026-07-07

### Added

- `issue view` now surfaces the discovery data Jira already returns on the read instead of discarding it: `data.issue.transitions[]` lists the workflow moves valid from the current status, and `data.issue.editmeta.fields` lists the editable fields with their required flag, accepted operations, and allowed values — so an agent or script can plan an edit or transition from a single read. The human view gains a one-line transitions summary.
- The agent contract is now versioned: `jira agent schema` reports the revision at `data.schema_version` and the full `jira agent guide` output stamps the same value, so an agent can detect a breaking change instead of discovering it mid-workflow. The scheme (what a major, minor, or patch bump means for the contract) is documented in the guide's core_contract section.
- Two discovery gaps are closed. A new `jira cache resolutions` command lists the tenant's resolution names (they vary per instance), so a resolve transition that requires a `resolution` field can be planned instead of guessed. Cached statuses now carry `status_category` (`new`, `indeterminate`, or `done`) alongside `id` and `name`, and the guide documents that status ids repeat across workflows — map a name to its category from the cache, and resolve the id per issue via the transition list.
- The `--dry-run`/`--force` safety rails now cover local state mutations, not just Jira writes. `cache clear --dry-run` reports what a live clear would remove without touching any file, `config set --dry-run` runs the full key and value validation and reports the current and new value without writing config, `auth switch --dry-run` resolves the target profile without changing the default, and `auth logout --dry-run` names the credential a live logout would revoke without opening the secret backend. The destructive pair — `cache clear` and `auth logout` — now requires `--force` in agent, non-TTY, or `--no-input` context, matching the gate on destructive Jira mutations; headless scripts that clear caches or log out must add the flag. Interactive terminals proceed without prompts.

### Changed

- `jira update` no longer asks for a yes/no confirmation in an interactive terminal — running the command is the consent, and the self-replace is already checksum-verified and rollback-safe. `--dry-run` still previews without changing anything, and headless, agent, and `--no-input` runs still require `--force`.

## [0.9.2](https://github.com/matcra587/jira-cli/releases/tag/v0.9.2) — 2026-07-07

### Added

- The JSON envelope now carries Jira's own trace id: the `Atl-Traceid` / `X-ARequestId` response header is captured as `meta.upstream_request_id` and on each error entry, so a Jira-side failure can be quoted directly to Atlassian support. The existing `meta.request_id` is generated locally and has no server-side meaning.

### Fixed

- Human output now strips ANSI escape and control bytes from Jira-controlled text (summaries, descriptions, error messages), so a crafted issue or error can no longer inject color codes or corrupt your terminal.
- `jira agent schema` now binds the comment input schema to the `comment add`/`comment edit` leaves (they previously reported no input schema) and publishes output schemas for the read/list commands — comment, attachment, link, worklog, transition, boards, and cache — so agents can discover response shapes without trial calls.
- `issue attachment download --to` is now confined to the working directory: a `..` traversal or an absolute path outside it is rejected before any HTTP call, so an agent (or a crafted instruction) can no longer write downloads outside the tree it was launched in. A new `security_posture` section in `jira agent guide` documents the threat model.

## [0.9.1](https://github.com/matcra587/jira-cli/releases/tag/v0.9.1) — 2026-07-07

### Added

- Running a command with --debug now prints the error's classification on the error line — its stable code and type, plus a retryable marker when the error is retryable — the same fields an agent reads from the JSON envelope.
- Rate-limited commands now say how long to wait: when Jira sends a Retry-After, human output adds a 'retry in Ns' line under the rate-limit hint.

## [0.9.0](https://github.com/matcra587/jira-cli/releases/tag/v0.9.0) — 2026-07-06

### Added

- Errors are now easier to act on: human output prints the next-step hint (and a did-you-mean suggestion for unknown flags or commands), the JSON envelope carries those in a new `suggestions` field, and hints across the CLI were rewritten in plain language.

### Fixed

- error envelopes now carry a complete, guarded taxonomy: read-only refusals emit code read_only, dry-run transport guards dry_run_blocked, wrong-shape ADF payloads adf_invalid (the raw json unmarshal text no longer leaks), issue-key expansion limits, ambiguous users and ambiguous boards their own codes, invalid --output values route through flag_value_invalid, every code ships a non-empty hint, a permanently deleted resource (HTTP 410) now exits 2 (not found) instead of 3, and exits 6 (canceled) / 7 (timeout) plus all emitted error fields are documented in the agent contract
- Several credential and 1Password error hints now point at the correct fix — including a 1Password failure on a build without 1Password support, and a login that can't reach Jira to verify, which previously suggested remediation that did not apply.

## [0.8.4](https://github.com/matcra587/jira-cli/releases/tag/v0.8.4) — 2026-07-06

### Fixed

- issue create/edit gain --verify: after a successful live write the issue is re-fetched and every requested field is diffed against what the server actually applied, so silently dropped labels, parents, assignees, priorities, versions, components, and custom fields surface as field_not_applied warnings with a data.verification block instead of a clean success

## [0.8.3](https://github.com/matcra587/jira-cli/releases/tag/v0.8.3) — 2026-07-06

### Fixed

- alias list human output now renders one name → expansion line per alias (natural-ordered) instead of the collapsed value={...} placeholder; the generic plain fallback also renders any string-keyed map per key, closing the class
- auth login no longer wipes existing profiles when configuring a new one without --config: the fresh-config probe now stats the resolved default path instead of the raw (empty) flag value, so adding a second profile preserves the first

## [0.8.2](https://github.com/matcra587/jira-cli/releases/tag/v0.8.2) — 2026-07-06

### Fixed

- auth login credential-rejection and cloudId discovery errors now name the HTTP status text (e.g. 401 Unauthorized) instead of the bare status code
- key- and name-like candidate lists now sort naturally (PROJ-2 before PROJ-10) — failed-key summaries in the TUI, board project keys, alias names, and JQL completion candidates

## [0.8.1](https://github.com/matcra587/jira-cli/releases/tag/v0.8.1) — 2026-07-06

### Fixed

- search jql/saved --fields now narrows the flat per-issue summary projection instead of silently switching the issues array to Jira's raw wire shape; --full remains the raw-record mode
- worklog add --started now accepts ISO-8601 and relative times (yesterday, 2h ago), normalizes them to the exact timestamp format Jira requires, and rejects unparseable values at --dry-run instead of on submit
- auth logout can now purge a credential whose profile was deleted from config: pass --base-url with the profile's old site and the orphaned keychain token is removed instead of failing with profile not found
- issue attachment download/delete now validate their issue-key argument (a traversal path or malformed key fails fast with exit 3), and negative --timeout, --max-retry-wait, and --limit values are rejected instead of silently falling back; 0 remains the documented default/disabled sentinel
- issue create/edit --json-input now accept flat-string values for object-valued system fields (project, issuetype, parent, priority, assignee, reporter, components, fixVersions, versions), lifting them to the wire shape Jira requires instead of passing --dry-run and failing on submit; explicit wire objects are untouched

## [0.8.0](https://github.com/matcra587/jira-cli/releases/tag/v0.8.0) — 2026-07-05

### Added

- Publish curated release notes on the docs site, assembled per release from change fragments.
- Read jira-cli's own changelog from the CLI with `jira release-notes` (alias `rn`): the notes are embedded in the binary and rendered as Markdown — the full history by default, or a single release with a version argument or `--latest`.

### Fixed

- Honor a named theme set in config (e.g. `dracula`, `nord`) in plain command output — tables and the `release-notes` view — instead of falling back to the dark palette; previously only the opt-in `auto` theme (or `JIRA_THEME`) styled non-TUI output.

## [0.7.7](https://github.com/matcra587/jira-cli/releases/tag/v0.7.7) — 2026-07-05

### Changed

- Detect grapheme-clustering terminals through emulator markers (Ghostty, WezTerm, foot, Contour, Windows Terminal); kitty now measures width by wcwidth, correcting its column drift.

### Fixed

- Keep issue-table columns aligned when a summary contains an inline-code span.

### Dependencies

- Update gechr/clib to v0.5.9
- Update gechr/clive to v0.2.6
- Update gechr/clog to v0.11.15
- Update gechr/x to v0.2.8

## [0.7.6](https://github.com/matcra587/jira-cli/releases/tag/v0.7.6) — 2026-07-04

### Added

- Author Jira checklists from Markdown task lists (`- [ ]` / `- [x]`); the ADF support matrix now covers every node and mark of the pinned Atlassian schema.
- Teach a foreign flag carried over from another Jira CLI, pointing at this CLI's equivalent instead of rejecting it bare.

### Fixed

- Align issue tables on grapheme-clustering terminals (Ghostty, WezTerm, foot) when a summary contains emoji.
- Pad status pills to a uniform width down the column.
- Show an unassigned issue as `unassigned` rather than a raw `map[]`.

### Dependencies

- Update gechr/primer to v0.1.1
- Update gechr/x to v0.2.4
- Update bubbletea to v2.0.8
- Update lipgloss to v2.0.5
- Update google/uuid to v1.6.0

## [0.7.5](https://github.com/matcra587/jira-cli/releases/tag/v0.7.5) — 2026-07-03

### Added

- Accept native Jira REST payloads on `create`, `edit`, `transition`, and `link` via `--json-input`.
- Render bulk operations as a live per-key block.

### Changed

- Validate `--dry-run` against the server and report only the checks it actually ran.
- Emit pagination as a single canonical block with resumable cursors.

### Fixed

- Resolve numeric transition targets against the live transition list under `--dry-run`.


## [0.7.4](https://github.com/matcra587/jira-cli/releases/tag/v0.7.4) — 2026-07-03

### Added

- Carry a comment and field edits atomically with a transition's status change.
- Resolve names and emails to Jira account identities with `user search`.


## [0.7.3](https://github.com/matcra587/jira-cli/releases/tag/v0.7.3) — 2026-07-03

### Added

- Read Markdown bodies from a file or stdin via `--markdown-file`, unified under a single `--markdown` flag.
- Accept both `--json-input` payload shapes on `create` and `edit`.

### Changed

- Normalize pasted Jira wiki markup before Markdown conversion.
- Abort strict ADF mode on content loss only, not on decoration or placement.
- Treat the wire and alias spellings of the project field as one.


## [0.7.2](https://github.com/matcra587/jira-cli/releases/tag/v0.7.2) — 2026-07-03

### Added

- Author ADF ahead of submission with standalone `adf convert` and `adf render` commands.
- Return native ADF bodies from `comment list` for lossless reuse.
- Convert GFM tables, blockquotes, and images (as alt-text links) during Markdown conversion, with source-mapped diagnostics.


## [0.7.1](https://github.com/matcra587/jira-cli/releases/tag/v0.7.1) — 2026-07-03

### Fixed

- Fail closed on an unknown or incomplete `--profile`.


## [0.7.0](https://github.com/matcra587/jira-cli/releases/tag/v0.7.0) — 2026-07-03

### Added

- Update the CLI in place with channel-aware self-update.
- Print a bare version for humans, with a `--detailed` build block.

### Dependencies

- Update gechr/clib to v0.5.8
- Update gechr/clive to v0.2.4
- Update gechr/clog to v0.11.12
- Update gechr/primer to v0.0.16
- Update gechr/x to v0.1.14

## [0.6.4](https://github.com/matcra587/jira-cli/releases/tag/v0.6.4) — 2026-06-30

### Added

- Edit an issue description with `--description-markdown`.
- Install on Windows via a published Scoop manifest.


## [0.6.2](https://github.com/matcra587/jira-cli/releases/tag/v0.6.2) — 2026-06-18

### Added

- Auto-detect and support scoped (granular) API tokens.

### Changed

- Resolve config, cache, and query directories OS-natively.

### Fixed

- Handle ADF that Jira rejects with `INVALID_INPUT`.

### Dependencies

- Update gechr/clib to v0.5.4
- Update gechr/clog to v0.11.2
- Update gechr/x to v0.1.4
- Update glamour to v2.0.1
- Update koanf to v2.3.5
- Update lipgloss to v2.0.4
- Update golang.org/x/sync to v0.21.0

## [0.6.0](https://github.com/matcra587/jira-cli/releases/tag/v0.6.0) — 2026-06-10

### Changed

- Rebuild the TUI as a section-based dashboard, with context-aware JQL autocomplete drawn from the instance's own metadata.

### Dependencies

- Update bubbletea to v2.0.7
- Update glamour to v2.0.0

## [0.5.1](https://github.com/matcra587/jira-cli/releases/tag/v0.5.1) — 2026-06-09

### Added

- Warn on a successful response that is near the rate limit.


## [0.5.0](https://github.com/matcra587/jira-cli/releases/tag/v0.5.0) — 2026-06-08

### Added

- Retry 429/503 responses within a bounded budget, honoring `Retry-After`, opt-in via `--max-retry-wait` and explained under `--debug`.


## [0.4.1](https://github.com/matcra587/jira-cli/releases/tag/v0.4.1) — 2026-06-08

### Added

- Warm every cached resource in one pass with `cache refresh`, backed by a per-resource TTL ladder and versioned entries that self-invalidate on a shape change.


## [0.4.0](https://github.com/matcra587/jira-cli/releases/tag/v0.4.0) — 2026-06-08

### Added

- Adapt output to the terminal background with an opt-in `auto` theme, honored across output and the dashboard.
- Show progress feedback for blocking and fan-out operations.
- Accept `-o` as shorthand for `--output`.

### Changed

- Colour issue-list status pills and the priority column.

### Fixed

- Write machine-mode error envelopes to stdout with a clean message.

### Dependencies

- Update gechr/clib to v0.5.2
- Update gechr/clog to v0.10.2
- Update gechr/x to v0.0.10

## [0.3.3](https://github.com/matcra587/jira-cli/releases/tag/v0.3.3) — 2026-06-03

### Added

- Complete `--status`, `--priority`, `--assignee`, and `--columns` from cached metadata in the shell.
- Validate JQL against Jira's parser (`jql validate`), list instance JQL metadata (`jql reference`), count approximate matches (`--count`), and page `search jql` with `--all`/`--limit`/`--unbounded`.

### Security

- Rebuild with Go 1.26.4 to clear two standard-library advisories on the HTTPS path to Jira: GO-2026-5039 (`net/textproto`) and GO-2026-5037 (`crypto/x509`).


## [0.3.2](https://github.com/matcra587/jira-cli/releases/tag/v0.3.2) — 2026-06-01

### Added

- Filter `issue mine` with the full list surface, plus `--updated`/`--created`/`--resolved` date filters.


## [0.3.1](https://github.com/matcra587/jira-cli/releases/tag/v0.3.1) — 2026-06-01

### Fixed

- Route config and credential writes through a symlink-aware atomic writer.


## [0.3.0](https://github.com/matcra587/jira-cli/releases/tag/v0.3.0) — 2026-05-31

### Added

- Open issues and queries in the browser.
- Transition by status name or positional target.
- Filter status with JQL comparators and negation; select and order columns with `--columns` and `--tsv`; create issues with convenience flags.

### Changed

- Colour issue statuses by workflow category.
- Require a 1Password vault and item on the headless login path, and validate the profile name at login.

### Fixed

- Apply `--order-by` to a custom `--jql` query.
- Write config through a symlinked file instead of clobbering it.

### Dependencies

- Update gechr/clib to v0.4.15
- Update gechr/clog to v0.9.8
- Update gechr/x to v0.0.8

## [0.2.0](https://github.com/matcra587/jira-cli/releases/tag/v0.2.0) — 2026-05-29

### Added

- Fan out multi-key operations with bounded parallelism.

### Dependencies

- Update golang.org/x/sync to v0.20.0

## [0.1.2](https://github.com/matcra587/jira-cli/releases/tag/v0.1.2) — 2026-05-28

### Added

- Accept issue-key ranges (`PROJ-1..PROJ-5`).


## [0.1.1](https://github.com/matcra587/jira-cli/releases/tag/v0.1.1) — 2026-05-27

### Fixed

- Render agent human output as styled JSON.


## [0.1.0](https://github.com/matcra587/jira-cli/releases/tag/v0.1.0) — 2026-05-27

Initial release — a Jira Cloud CLI built for developer and agent workflows.

### Added

- Log in to Jira Cloud with an API token, credentials keyed per site.
- Read, create, edit, and mutate issues, with an honest `--dry-run`.
- Emit machine output modes with agent detection, typed command-line parse failures, and an actionable hint on every Jira failure.
- Validate and preserve submitted ADF, and validate custom fields against the Jira screen schema.
- Page and query with truthful JQL, backed by a site-isolated metadata cache.
