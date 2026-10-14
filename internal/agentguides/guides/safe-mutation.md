---
slug: safe-mutation
title: Preview and verify a Jira change
description: Inspect a local mutation preview, submit deliberately and verify the outcome.
when_to_use: Before creating, editing, transitioning, cloning, deleting an issue or recording effort.
commands: [jira issue create, jira issue edit, jira issue edit prepare, jira issue edit draft, jira issue edit inspect, jira issue edit resume, jira issue transition, jira issue clone, jira issue delete, jira issue move, jira issue move status, jira issue move fields, jira epic add, jira epic remove, jira worklog add, jira worklog list, jira issue attachment add, jira issue link create, jira issue link list, jira issue link delete, jira issue weblink add, jira issue weblink list, jira issue weblink delete, jira cache clear]
order: 40
---

## Decide

Choose the domain command and explicit targets. For Jira resource mutations,
start with `--dry-run` and inspect the prepared request. A bare mutation preview
changes nothing and sends no Jira request. Where supported, `--validate-remote`
opts a preview into metadata or account reads without submitting the mutation.
Authentication previews differ: login and `auth whoami --save --dry-run` verify
the account against Jira without saving credentials or settings.

Before a live write, check the site, credentials and project requirements in
Preconditions. Choose the workflow below, then keep its complete receipt.
A successful command can contain failed targets. Save and Recover explain how
to inspect them before retrying.

### Select explicit issue targets

### Resolve an assignee or the current account

The `--assignee` flag also accepts a bare email. A live run or
`--validate-remote` resolves the unique visible active user search match before
composing the issue request. A local preview leaves the email unresolved and
adds `account_email_unresolved`. Missing, multiple and incomplete searches refuse
before a write. Privacy may hide the returned email. This lookup does not prove
exact email equality or permission to assign. Use `jira user search` and an
explicit account ID when choosing among candidates. Native JSON account IDs
remain literal.

An `@me` account flag resolves through the authenticated current-account read on
a live run or with `--validate-remote`. Local previews use a matching configured
`account_id` with an `account_id_cached` warning. The ID remains unverified metadata.

`jira auth whoami --save --dry-run` verifies the current account and prepares
bound cached identity without publishing settings. Inspect its native user,
public scope and `data.settings` receipt before running without `--dry-run`.
Ordinary whoami and root me inspection stay read-only. Unknown local publication
requires file inspection before retrying. Saved cached IDs never authorize Jira writes.
Without matching site/account/cloud binding, previews retain `@me` and warn.
`current_user_response_invalid` means that reply cannot identify the current
account. Retry or inspect it before submitting. Resolution failure or cancellation
stops before issue/watcher writes and leaves account fields unresolved.

### Quote comma-separated values

Comma-separated flags accept one CSV record per occurrence. Use CSV quotes
inside the shell argument for literal commas or quotes, and repeat the flag for
additional values. Quoted literal LF and CR values remain unchanged where CSV
can preserve them. Inputs that discard later records, skip blank lines or
normalize CRLF are refused before command execution. Attachment paths can use
positional arguments for literal line endings that CSV cannot preserve. Native
field values can use the command's JSON input route where supported.

## Run

### Preview, submit and verify the selected change

The first examples mix local previews with authenticated Jira reads. The final
cache-clear example removes local entries. Prepare the named input files before
using them. Their formats and the write requirements are explained below.

```sh
jira issue create --project KEY --type Task --summary 'Verify CLI workflow' --dry-run --output=json
jira issue edit KEY-123 --summary 'Inspect the prepared change' --dry-run --output=json
jira issue view KEY-123 --output=json
jira issue move KEY-123 --json-input move.json --dry-run --output=json
jira issue move status 10000 --output=json
jira issue move fields KEY-123 --json-input fields.json --dry-run --output=json
jira epic add KEY-123 KEY-100 --dry-run --output=json
jira epic remove KEY-123 --dry-run --output=json
jira worklog add KEY-123 --time-spent 1h30m --started 2026-01-02T12:30:00Z --markdown 'Investigated the incident.' --dry-run --output=json
jira worklog list KEY-123 --all --output=json
jira issue attachment add KEY-123 notes.md --dry-run --output=json
jira cache clear boards --dry-run --output=json
jira cache clear boards --force --no-input --output=json
```

After inspecting the preview, submit the intended command without `--dry-run`
and verify the affected resource. Use `--verify` where the command supports it.
Add `--force` only where that command requires acknowledgement. Creating an issue
has no `--force` flag. `--no-input` disables interactive prompting but does not
provide a missing acknowledgement. Jira read-only policy refuses remote writes
even with `--force`.

### Check field readback and remote metadata

Create/edit `--verify` makes one later read of every requested field plus
summary/status/time metadata. Inspect `verified.fields` on create and
`results[].data.verified.fields` on edit. Sorted rows report `matches`, `differs`
or `unverified`, their comparison reason and the native `observed` value when
returned. An absent `observed` remains different from explicit null.
Known Jira identities, including cascading-select children, and unordered
collections use identity/membership checks.
Arbitrary native numbers retain their exact spelling. Known collection updates
check final add/remove membership. Collection replacements also reject leftover
members while accepting reordered, expanded identities.
Unsupported operations, ambiguous member aliases and absent fields remain unverified.
Different ADF also remains unverified, including custom rich-text documents.
Jira can normalize ADF. Top-level request properties and followups are outside this field check.

Create/edit `--validate-remote` also checks every requested field ID and
operation against complete create or per-issue edit metadata. Bare previews stay
local. Inspect `field_preflight.fields` on create and
`results[].data.field_preflight.fields` on edit. Native values remain unchanged.
Field types, required values, permissions and top-level request properties are
outside this check. A missing field or unsupported operation refuses before that
context's write. Different edit contexts retain ordered partial results. A
metadata snapshot is neither permission nor protection against another writer.

Field metadata checks admit only complete replies bound to the configured API
scope and credentials. A redirected public response cannot authorize a write.

Full JSON warnings expose `field_value_differs`,
`field_verification_incomplete` and `verification_failed`. Compact success keeps
the comparison in operation data. Inspect its rows even when no frame warnings
are present. Readback failure or differences retain the acknowledged creation
identity or successful edit. Inspect the later values before further changes.
Repeating create makes another issue. A difference does not identify its cause,
and matching values do not prevent a later concurrent write.

### Prepare an existing description without losing native data

Prepare an existing description with `jira issue edit prepare KEY-123`. This
read works in read-only mode and returns `data.snapshot`, `data.markdown` and
`data.regions` (compact omits `data`). Keep the original snapshot with its
anchors. It binds the native issue ID and public site/account/API route.
It retains exact ADF bytes at `snapshot.description_bytes` (base64), including whitespace and
unknown nodes and root extensions. Keep the complete snapshot. Its adjacent native JSON view may compact whitespace.
Reopening uses the retained original bytes.
Explicit null stays null.
An absent description or incomplete native identity is refused. Protected
regions must remain unchanged and present once. Preparation creates no editing
artifacts, opens no editor and changes no Jira fields. A timestamp, fresh read
or successful merge never establishes a server-enforced write condition.

### Discover live input for create or edit

Configured `default_project` and `default_issue_type` fill only absent create
members across both native `fields` and `update`. Flags override defaults.
Explicit empty project/type flags suppress them and refuse missing context.
Authored null, empty, zero, operation and future ADF values remain unchanged.
`--generate-json` uses defaults only for omitted project/type context. Edit and
clone never inherit create defaults. Help/schema/completion do not read these
preferences or open credentials.

Discover live input before composing a request when fields depend on the Jira
context:

```sh
jira issue create --generate-json --project KEY --type Task --output=json
jira issue edit KEY-123 --generate-json --output=json
```

Create discovery requires a project key or ID and issue-type name or ID, supplied
by explicit flags or configured defaults when those flags are omitted. Explicit
empty flags refuse. No summary is needed.
It resolves actual native identities and collects every create metadata page. Edit discovery retains each issue's edit metadata and
skeleton separately. Both reads require authentication, work in read-only mode,
and ask no mutation confirmation. Edit also supports `--jql`, `--filter` or
`--from-file` through complete selection bounded at 100 issues.

The operations are `issue.create.input` and `issue.edit.input`. Create JSON
retains the skeleton at `data.payload`. Edit JSON retains it at
`data.results[].data.payload`. With `--output=compact`, those paths omit only
the outer `data` wrapper.
Use compact `--jq '.payload'` for create, or
`--jq '.results[0].data.payload'` for one explicit edit target, to extract native
input. Native metadata includes actual field IDs, requiredness, allowed values,
schemas, defaults, operations and future members. No value or default is chosen.
Null payload values are placeholders. Fill wanted values, keep required create
fields, and remove unwanted optional fields or update actions before submitting
through `--json-input`. A create `complete: true` means collection finished,
not that the unfilled payload is ready to submit.

Discovery refuses authored-input and mutation options, including explicitly
empty or false options, `--dry-run`, `--verify`, `--force` and `--notify` where
registered. Keep local composition previews separate from live metadata reads.
`issue_type_unknown` and `issue_type_ambiguous` retain native candidates at
`data.issue_types`. Choose an actual ID rather than guessing from a name.
`issue_metadata_response_invalid` reports incomplete or unusable native metadata,
while `issue_metadata_unavailable` reports missing client capability. Retry or
inspect discovery before constructing input. Metadata remains a snapshot and
does not guarantee a later write will be accepted.

`issue delete` requires `--force` in scripts and retains ordered outcomes after
total failure or interruption. Jira refuses parents with subtasks unless
`--delete-subtasks` is explicit. That flag requests deletion of the subtasks too.
On a live run, per-key `data: {}` records a validated HTTP 204 acknowledgement,
including when accompanied by an error. It does not independently prove absence.
Inspect all requested issues and subtasks before repeating an uncertain write.
Local previews delete nothing. `--dry-run --validate-remote` can resolve selectors
through reads without deleting or confirming anything.

### Discover and apply a transition

Discover workflow choices with `jira issue transition list KEY-123` before
choosing a transition. This read requests native screen field metadata and
works in read-only mode. Machine results retain the native response at
`results[].data`, including field schemas, allowed values, defaults and
extensions. Human output marks returned required fields. Metadata is a
snapshot. An omitted field does not establish that Jira accepts it.
`transition_read_invalid` means discovery returned unusable data. Retry the
read or inspect the native workflow with `jira api get`. Do not use incomplete
metadata to authorize a write.

Transition results remain ordered after total failure, timeout or interruption.
Per-key `attempted` means the client write operation was invoked.
`applied` records an HTTP
204 acknowledgement, including one joined after interruption. Neither proves a
status readback. `transition_response_invalid` requires inspecting the current
issue before repeating a move. Screenless comments use a separate POST. If
`comment_posted=failed`, inspect the comments before adding another. The write
may already be stored. `comment_error_code` and `comment_error_hint` preserve
its classification and recovery advice. An acknowledged move remains separate
from an uncertain comment outcome.
`comment_id` retains any validated separate-comment identity even when an error
accompanies it. Read that resource to inspect its body before adding another.

### Submit native transition input

When using `--json-input`, provide a file path or `-` for standard input. An
explicit empty value is refused before input reads or Jira requests. Omitting
the flag preserves the command's other input routes. Empty values inside a
native request remain governed by that request's schema.

For a full native transition request, write `transition.json` with the target and
native containers, then preview it:

```json
{"transition":{"id":"31"},"fields":{},"update":{},"historyMetadata":{"type":"workflow.change"}}
```

```sh
jira issue transition KEY-123 --json-input transition.json --dry-run --output=json
jira issue transition KEY-123 --json-input transition.json --dry-run --validate-remote --output=json
```

Resolve the example ID against current metadata. A payload target makes every
positional an issue key. An agreeing `--transition` is allowed. Different targets
are refused. Full native requests preserve fields, operations, empty containers
and request extensions, including writable and unknown target members. A flat
field object with a payload or CLI target remains supported. Use explicit
request sections to distinguish native extensions from flat field values.
Duplicate JSON members and colliding field/comment inputs are refused before
reads. Read-only workflow metadata inside the target is not write input.
`data.payload` contains the locally composed request. Prepared per-key
`results[].data.payload` contains the resolved ID and exact request retained on
write failure. A local preview cannot prove screen state or required-field
compatibility. Remote preview checks returned screen fields and required values
or defaults. Duplicate destination names require selecting an ID. Explicitly
unavailable transitions are refused.

Populated native fields, update operations and properties are refused on a
screenless transition with `transition_screen_required`. Choose a screened
transition or submit the intended changes separately. Empty fields/update
objects and an empty properties array retain their authored presence. A 204 can
acknowledge the status change while silently discarding a
property. A top-level ADF `comment` is a convenience input with the same separate
fallback as `--comment`. Native `update.comment` stays in one request. Supply a
comment once. Neither a 204 nor the retained payload proves arbitrary extension
values were stored. Verify the intended resource independently.

### Submit and monitor a native bulk move

For a native move, author `move.json` with the exact source set and destination
mapping. Destination keys use `projectId,issueTypeId` or include a third parent
ID. Resolve current metadata before choosing them. For example:

```json
{"sendBulkNotification":false,"targetToSourcesMapping":{"10000,10001":{"issueIdsOrKeys":["KEY-123"],"inferClassificationDefaults":true,"inferFieldDefaults":true,"inferStatusDefaults":true,"inferSubtaskTypeDefault":true}}}
```

The IDs above are placeholders. Local preview performs no reads. Numeric source
IDs remain unbound until `--validate-remote` resolves them against the positional
keys. Remote validation checks source identity only. Jira validates destination,
workflow and mandatory-field compatibility. A live move requires `--force` in
scripts. Its acknowledgement identifies an asynchronous task, not moved issues.
Use `--wait` for bounded observations, or resume using the returned `data.task.id`
with `issue move status`. A job still running at 100 percent is not complete.
Inspect native processed, failed and inaccessible outcomes, then independently
read each issue's project and type. Cancellation, read failure or
`poll_limit_reached=true` requires resuming task reads, never another submission.
Valid task reads succeed even when the reported job failed.

### Change move-related fields through issue edit

The historical field-write move workflow is available as `issue move fields`.
Author `fields.json` as a native issue payload, for example:

```json
{"fields":{"issuetype":{"id":"10001"}}}
```

This route sends confirmed issue-edit PUTs. Jira determines which fields can
change through that endpoint. A successful write does not prove project
migration. Use the native bulk move mapping for project migration. Previews
remain local. Live field writes require `--force` in scripts and retain ordered
per-key acknowledgements. Unknown native values and ADF survive composition.

### Add or remove epic membership

For epic assignment, the last positional key is the destination epic. Child
keys accept repeated arguments, comma lists and inclusive ranges, bounded to
1,000 distinct targets. The destination type is checked before writes.
Removal checks each child's current parent and refuses subtasks or non-epic
parents. Already unparented issues need no PUT. Local removal previews do not
establish the current parent. Add `--validate-remote` for read-only checks.
These checks and parent writes are separate requests, without an atomic
compare-and-set guarantee. Jira remains responsible for hierarchy constraints.

### Record effort and inspect worklogs

Worklog effort accepts integer `d`, `h` and `m` components. `1d` uses the flat
`workday_seconds` setting, default 28,800 seconds (eight hours). Set
`JIRA_WORKDAY_SECONDS` or edit the config file to change it. This does not change
elapsed durations such as `--started '1d ago'`, which uses 24 hours. Start times
accept ISO dates/times, `now`, `yesterday` and a duration followed by `ago`.
Explicit offsets are preserved. Local times use the machine's zone. An omitted
start captures the current time once for all targets.

Worklog `--json-input` accepts a whole native request with `timeSpentSeconds`,
`started`, native ADF `comment` and optional native extensions. Legacy
`time_spent`, `timeSpent` duration text and `comment_markdown` remain accepted.
Effort/start flags explicitly override their JSON values. Markdown flags conflict
with whole JSON input. Previews retain the composed request and invent no stored
record. A Jira 201 with an unusable receipt keeps `applied=true` and exposes
`receipt_error`. Inspect the list before repeating a POST. List results expose
per-issue pagination and retain validated earlier pages when a later read fails.
`--all` stops after 50 pages or 5,000 entries per issue. Inspect `complete`,
`truncated_reason` and `read_error` before treating the result as exhaustive.

### Upload attachments and retain created IDs

Attachment uploads check local regular-file metadata before client construction.
Each file and the batch's combined content must fit within 100 MiB
(104857600 bytes), excluding multipart framing. Local symlinks to regular files
are accepted. `--dry-run` reads no contents and contacts no Jira service.
`data.files` retains authored paths and `data.file_details` adds sizes, including
zero, and MIME types inferred from extensions. Inspection cannot prove
readability or freeze an input. Live requests open and recheck every input and
stream within the same content ceiling. Every successful key reports one
distinct created attachment ID per file. An unusable acknowledgement reports
`attachment_response_invalid`. Inspect attachments before retrying because
Jira may already have stored files. Repeating an upload can create duplicates.
After uploads begin, total failures and cancellation retain authored
input metadata and ordered per-key outcomes in the full failure frame. Keep
completed attachment IDs and inspect uncertain failed targets before retrying.

Attachment deletion requires `--force` outside an interactive terminal. A local
preview contacts no Jira service. Only a validated HTTP 204 acknowledges
deletion. That receipt survives cancellation or timeout in the full failure
frame. Without an acknowledgement, no deletion data is published. Inspect the
resource before retrying an uncertain delete. Writes are never replayed
automatically. The acknowledgement does not independently prove absence.

### Select watchers and inspect readback

Watcher add/remove resolves `--user @me` on live runs. Local previews use matching
cached identity with an unverified warning, or leave it unresolved.
`--validate-remote` permits the actual account lookup in a preview. For literal
`--user` IDs it checks one exact active, undeleted native account before any
issue request, also on live preflight. Malformed replies, unknown activity or a
different account ID refuse. Surrounding input whitespace is trimmed for this
check. One lookup serves every issue. It does not establish watcher permission
or issue existence. Bare previews and ordinary literal live writes make no
account lookup.
`--no-readback` skips the post-write GET. A failed GET keeps the acknowledged
write successful and publishes `data.results[].data.readback_error`. Inspect
watcher state to recover that read instead of replaying the write. Watcher
list/add/remove retains ordered outcomes on total failure or interruption.

Use `--user-query` to select one visible active name/email search match instead
of supplying `--user`. The flags conflict even when authored empty. Missing,
ambiguous, malformed or incomplete searches refuse before issue requests.
Duplicate native row members and case variants of accountId/active/deleted
refuse before typed decoding, preventing overwritten identity or activity from
authorizing a write. An invalid later row publishes none of that page. A
match does not prove exact email/name identity or watcher permission. Bare
previews retain `data.user_query` with an unresolved warning and empty
`data.account_id`, without opening a client or preferences. Live and explicit
remote previews resolve once for every key and retain the original query beside
the native account ID. Query text `@me` is a search, not the current-user sentinel.

Comment add retains the authored body and ordered outcomes when every target
fails or the run is interrupted. Keep completed comment IDs. An add or edit
with an unusable acknowledgement reports `comment_response_invalid`. Inspect
the issue's comment list before retrying because Jira may have stored the write.
An edit retains any validated receipt returned by the joined write even when
the command ends canceled or timed out. Comment deletion requires `--force`
when no one can answer the confirmation. A validated HTTP 204 deletion receipt
also survives interruption: retained live data identifies the acknowledged
deletion even when the full frame has `ok=false`. An unacknowledged failure
has no deletion data. Inspect the comment before retrying an uncertain write.

### Create or delete issue relationships

Issue-link creation resolves the relationship once for all explicit sources.
Local previews leave its type unresolved. `--validate-remote` reads site types
without writing. Native `--json-input` can supply an optional comment. The
positional sources, `--to` and `--type` select the link ends and relationship.
Creation and list retain joined outcomes on total failure or interruption.
The create endpoint returns no link ID. Read `issue link list` to discover it
before an explicit `issue link delete LINK_ID`. Inspect uncertain targets
before repeating creation, because Jira permits duplicate relationships.
Deleting a link removes the relationship from both ends. It takes no issue key
and no `--force`. Previews and read-only enforcement still apply.

### Add or delete web links

Web-link add requires an absolute HTTP or HTTPS URL. An omitted or empty
`--title` uses the URL as its effective title, visible in both preview and live
results. `--global-id` replaces a matching link on that issue and clears omitted
summary and relationship fields. Without it, repeated adds create duplicates.
List and add retain ordered outcomes on total failure or interruption. Add also
retains the composed request. Unusable lists or write acknowledgements report
`weblink_response_invalid`. Inspect stored links before retrying an uncertain
write. Successful live add outcomes carry a positive link ID and `created`.
Delete takes both the issue key and its link ID, with no `--force`. Local
previews and read-only enforcement still apply.

`issue weblink add --json-input FILE` reads a complete native remote-link
request. Use `-` for standard input. Supply `object.url` and `object.title`,
with optional application, icon, status, and extension data. Explicit flags
can fill omitted members, but a member supplied by both routes is refused,
including an empty or null member. Native input preserves authored values.
The URL-as-title fallback applies to the flag-only route. `data.payload`
retains the complete composed request in previews and write outcomes.

Ranking sends at most 50 distinct explicit issues in one ordered request, with
exactly one `--before` or `--after` anchor outside that source list. The argument
order is the move order; there is no `--parallelism`. Local preview validates
these constraints without contacting Jira. A native multi-status must identify
every requested issue exactly once. Total failure or interruption retains the
anchor and ordered outcomes. `rank_response_invalid` means the acknowledgement
cannot be trusted; inspect the backlog before retrying because some moves may
already have landed. Ranking writes are never replayed automatically.

### Choose native ADF or Markdown authoring

Choose native `--json-input` when fields or ADF must be preserved. Markdown is
a convenience conversion route and can report loss. Parse/marshal preservation
does not establish lossless editing, three-way merge or optimistic concurrency.

Markdown bodies accept [GitHub alerts](https://docs.github.com/en/get-started/writing-on-github/getting-started-with-writing-and-formatting-on-github/basic-writing-and-formatting-syntax#alerts)
as native panels. Put the marker on the first line of a top-level blockquote:

```markdown
> [!WARNING]
> **Keep** this information visible.
```

The five alert kinds map to Jira panel types: `NOTE` to `info`, `TIP` to `tip`,
`IMPORTANT` to `note`, `WARNING` to `warning`, and `CAUTION` to `error`.
Ordinary quotes, unknown markers, escaped markers, code, and nested alerts stay
quoted or literal content. Panels retain headings, paragraphs, lists, task lists,
code blocks and rules. Tables move after the panel with an explicit downgrade
warning. Nested quotes flatten with a warning. `adf convert` uses the same
converter and refuses dropped content, such as raw HTML, by default. Issue and
comment Markdown routes publish conversion diagnostics in their output.
Top-level panels of those five types project back to alert syntax when the entire
subtree survives structural round-trip comparison. Native IDs, extra attributes,
unknown child state, unsupported panel types and nested panels retain explicit
loss diagnostics. Use native ADF to preserve that state. This local conversion
does not establish safe concurrent editing of an existing issue.

### Validate ADF before submission

Inspect a native document locally before submitting it:

```sh
jira adf validate --input document.json --output=json
jira adf lint --input document.json --jq '.data.lossy_constructs'
jira adf explain-node paragraph_with_no_marks_node --output=json
```

`validate` checks the pinned full schema and exits 3 for invalid JSON or schema
content, retaining instance/schema pointers in its report. `lint` also names
Markdown projection risks. Valid rich documents with warnings still exit zero.
`projection_available=false` means the input could not be analyzed, not that it
is safe to convert. `explain-node` accepts exact runtime node/mark types or
context definitions and reports structural bounds and local capabilities.
Use `agent adf-matrix` to discover every accepted identity. These read commands
require no credentials and never modify the source. They do not prove that Jira
accepts a document, that Markdown editing preserves it, or that writes prevent
concurrent updates.

### Preserve native create, edit and clone requests

Create and edit accept a whole native request object. Top-level `properties`,
`historyMetadata`, `transition` and future members survive along with field
values. Property-only requests reach Jira, which can require `fields` or
`update` even for those changes. Present `fields` and `update` containers
must be objects. Explicit empty objects remain present. Jira
validates native values and extension members. Duplicate JSON members and
multiple case spellings of one fields/update container are refused locally.
Clone layers field overrides
onto its copied fields and retains explicit top-level input members. It does
not copy those members from the source issue response. A local clone preview
does not read the source or publish the final composed request.

Clone reads all source fields and carries assignee plus eligible custom and
unknown values, preserving null, false and zero. Identity, source workflow,
reporter, timestamps, time tracking, aggregate progress, resource collections
and rank are excluded. Project, issue type, priority and assignee use minimal
native references. Custom-field schemas identify rank. Rank-shaped ordinary
text remains data. Invalid source fields or metadata yield
`issue_clone_source_invalid` before creating that source. Jira validates copied
values against the destination. Inspect ordered outcomes before retrying.

Clone previews expose known nonempty account-ID overrides at
`data.account_overrides.assignee` and `.reporter`. These are the prepared flag
values after cached or remote identity resolution, not copied source fields.
The source remains unread in a preview, including with `--validate-remote`.

Creation and clone retain HTTP 201 acknowledgements independently of command
failure or usable identity. `applied=true` can therefore accompany an error
without a new key. Preserve the complete native `receipt`, the attempted create
payload and every ordered clone outcome after failure or interruption.
`issue_create_response_invalid` means inspection is needed before repeating a
create. `issue_create_followup_failed` retains the created key and native
transition/watchers outcome. Retry only the failed follow-up. Repeating a create
or clone makes another issue. No creation write is automatically replayed.

### Clear local metadata

`cache clear` inspects and removes only the selected site/account namespace.
Select that scope with `base_url` and `email` in configuration or environment
(`JIRA_BASE_URL`, `JIRA_EMAIL`). Keyring-only identity cannot select a local
scope. Ordinary read-through metadata can still resolve its stored account.
`cache clear` opens no Jira client or credential store. A local preview creates no storage
and deletes nothing. Live headless, agent and `--no-input` runs require `--force`.
An interactive terminal proceeds without a prompt. Jira read-only policy permits
this local operation.

`boards` includes cached board lists and saved-filter selector snapshots.
Other account/site namespaces and legacy flat entries remain untouched. A named
resource clear reports unclassifiable entries. Use a whole-scope preview before
including those entries. Unknown files and directories are not removed
recursively. Older flat caches become cold under the managed namespace and are
not migrated or deleted implicitly.

`would_remove` counts the selected plan, `removed` counts acknowledged removals,
and each entry reports `attempted`, `removed` and any classified error. An entry
already absent reports attempted=true and removed=false. Partial failures and
interruption retain completed and queued outcomes. Inspect those receipts before
retrying. Concurrent readers and writers are not locked. A later metadata read
can repopulate the cache. Clearing metadata does not refresh it or change Jira.

### Inspect login writes independently

Verified login prepares public settings before storing credentials. Inspect
`data.credential` and `data.settings` independently: acknowledged credentials
can survive a later settings or output failure. An `unknown` write may have
completed. Inspect its destination before retrying. Environment login stores
public metadata only. Previews verify but publish nothing.

## Save

Keep the result identity, prepared payload and verification facts. Preserve
per-key results in their input order. For a compound operation, inspect each
completed step and any failed follow-up before deciding what to repeat.

## Preconditions

Live changes need a configured site, valid credentials, supported project
metadata and permission to change the selected resources. Project issue types
and required fields vary. Do not assume another project's choices apply.

## Recover

### A write failed or its outcome is uncertain

Inspect typed errors, hints and retained receipts. A failure, timeout,
cancellation or output error can follow an applied write. Read the resource
before retrying and repeat only the remaining intended step. The CLI never
automatically replays writes or broadens a failed selector.

### Create and recover a description draft

`jira issue edit draft ISSUE_KEY --directory DIR` reads a bound native description
and creates `DIR` itself. That path must be unused and its parent must exist.
Pass the same `DIR` to inspect or resume the saved draft. It contains
`source.json` with exact native description bytes and public identity,
`description.md` with editable text and protected anchors, and `manifest.json`
with corruption checks. Treat every file as private issue content. Preserve the
source and manifest. Edit only the Markdown outside its protected anchors.

Add `--edit` to open the configured editor after publication. The editor uses
`JIRA_EDITOR`, stored editor, `VISUAL`, then `EDITOR`, with literal arguments.
Interactive editor operations refuse headless execution before opening artifacts
or credentials. An editor failure or cancellation retains the draft and saved
work. Inspect the reported paths and publication receipt after any failure. A
partial directory is not necessarily a ready draft. Never overwrite an existing
draft directory to retry preparation.

### Inspect or refresh an existing draft

`jira issue edit inspect --directory DIR` validates and prepares a native
candidate locally, without credentials. `--refresh` explicitly reads the same
immutable issue ID under the recorded site/account/API route and merges current
native state with saved edits. Changed scope, damaged anchors and conflicts refuse
without a candidate. Digests detect corruption, not authentic provenance.

### Resume editing without writing to Jira

`jira issue edit resume --directory DIR` reopens the existing Markdown in the
shared editor. Invalid saved text can be repaired. Changed native backups refuse.
Draft/resume `--dry-run` validates local admission and editor selection where
applicable, but launches no editor, creates no files and opens no Jira client.
None of these commands writes to Jira. A prepared or refreshed candidate is not
server-enforced lost-update protection. Protected remote application remains unavailable.

## Next

Use `discover-cli` to inspect command-specific flags and constraints, and
`machine-output` to interpret full failure frames and partial results.
