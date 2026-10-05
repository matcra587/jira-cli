---
slug: machine-output
title: Select and inspect machine output
description: Choose stable output modes, extract data and preserve failure receipts.
when_to_use: When scripting Jira CLI commands, using jq, or interpreting errors and partial outcomes.
commands: [jira issue view, jira issue list, jira issue mine, jira adf convert, jira adf render, jira agent adf-matrix, jira agent fieldtypes, jira user search, jira boards list, jira issue delete, jira issue attachment list, jira issue attachment delete]
aliases: [output-contract]
order: 20
---

## Decide

Choose `--output=json` for a full invocation frame. Choose `--output=compact`
for bare operation data on success. Failures retain full frames in both modes.
Auto mode selects compact output for detected agents, JSON for other pipes and
human output at a terminal. Explicit modes override detection.

Keep the full receipt before filtering it. Exit zero does not establish success
for every batch target. Save explains how to inspect individual outcomes.

## Run

The ADF examples need `notes.md` and `document.json` as described in Preconditions.
ADF conversion and inspection are local. User and board reads use the configured
Jira account.

```sh
jira version --output=json
jira version --output=compact
jira version --output=json --jq '.data.version'
jira adf convert --input notes.md --output=json
jira adf convert --input notes.md --jq '.data.document'
jira adf render --input document.json --output=json
jira adf render --input document.json --jq '.data.markdown'
jira agent adf-matrix --output=json
jira agent adf-matrix --jq '.data.entries[] | select(.capabilities.author) | .definition'
jira agent fieldtypes --output=json
jira agent fieldtypes --jq '.data.types[] | select(.name == "user") | .example'
jira user search sam --output=json
jira user search sam --output=compact
jira user search sam --jq '.data.users[].account_id'
jira boards list --output=json
jira boards list --all --output=compact
jira boards list --refresh --all --jq '.data.boards[].id'
```

### Read human output

Human `adf render`, issue descriptions and comment bodies share native terminal
rendering: panel frames, status badges and tables with merged cells. Cell text
wraps without truncation. Section titles also wrap at the terminal width, keeping
complete titles and native styling. Human rows and labeled fields also wrap to
the terminal width. Fields keep aligned value columns when space permits.
Otherwise, the whole row wraps. Redirected rows retain their existing layout.
Very narrow or malformed tables use labeled authored cells, retaining their
attributes and full content. Layout columns appear in authored order with width
labels. Human ADF reads also show mention/emoji text, expanded content and
task/decision state. Media, cards and unsupported future nodes remain explicit
placeholders with a native-input recovery hint. Native read results stay
unchanged. JSON and compact `adf render` still return the Markdown review
projection with its list of losses. Human rendering is not an edit source. It
does not establish rich authoring, lossless editing or that a Jira field accepts
every node in the full schema.

### Select the correct JSON and jq paths

Full JSON uses `ok`, `data`, `meta`, `warnings` and `errors`. Empty warning and
error lists are arrays, not null.
Compact success starts directly at the operation object. Write jq expressions
against the selected shape: `.data.issue.key` for full JSON, `.issue.key` for
compact data. Unset output plus `--jq` resolves to JSON, including for agents.
`--output=human` conflicts with `--jq`.

### Preserve native issue data

Issue reads retain native metadata at `.issue.details` or `.issues[].details`.
Identity and fields keep their existing slots. `issue view` requests
renderedFields, names, schema, transitions, operations and editmeta by default.
Explicit `--fields` uses renderedFields, names and schema. `--expand` replaces
the preset, including `--expand=` to disable it. Search `--detail` requests
all fields and the full list expansion preset. Each accepted search page's
native metadata remains in `search_pages`, with issues collected separately.
Inspect page warnings as well as the CLI's own truncation/error receipts.

Raw JSON values retain authored escape spelling in machine output. Ordinary
string values emit literal HTML and line separators. `--jq` transforms parsed
values and does not preserve the original JSON spelling.

### Check board pagination and project enrichment

`boards list` returns sorted numeric identities and independently fetched
`project_keys`. The cache is isolated by site and configured account. `--limit`
controls the output window. `--all` returns the fetched snapshot. Neither removes
the 100-page / 10,000-board fetch caps. `--unbounded` removes those caps. Project enrichment
keeps its own bounds. `pagination.total` counts the snapshot, and
`pagination.complete` proves the board walk ended. Inspect `truncated`,
`next_start`, `project_failures` and each row's `projects_complete` before treating
results as exhaustive. Refresh after permission or board changes, or to retry
partial enrichment. Empty results do not prove boards are absent.

### Convert Markdown and inspect content loss

ADF conversion and review are local reads. Conversion uses the shared body
converter and validates its candidate against the pinned stable schema. Strict
conversion refuses dropped content with `adf_content_loss`. Its failure frame
retains the candidate and diagnostics. Explicit `--adf-best-effort` accepts
reported loss. Shape downgrades still warn. The candidate is at `document`, and
`diagnostics` remains visible in compact data. Extract the native document and
place it in the command's native request body before using `--json-input`.
For an issue description, put the document at `fields.description` in the
create/edit request. A bare ADF document is not a complete mutation request.
A local preview can accept a bare document, so a successful preview does not
confirm correct placement.

Plain Markdown text resolves punctuation escapes and character references once.
For example, `\*literal\* &amp;` authors `*literal* &` as native text. Code
spans and blocks keep those spellings literal. Native Markdown projections
escape ampersands so stored entity spellings survive conversion without another
decode. Unchanged anchored regions still preserve their original native bytes.

### Author and review status nodes

Optional `:status[plain label]{color=green}` directives author native status
nodes through the same converter. Omitted color defaults to `neutral`. Supported
colors are `neutral`, `purple`, `blue`, `red`, `yellow`, `green` and six-digit
`#RRGGBB`. Optional `localId` and `style` string attributes retain native values
and explicit empty strings. For example,
`:status[READY]{color=green localId="owned-id" style=""}`. Single or double quotes
around attribute values are accepted. Labels
resolve Markdown punctuation escapes and character references once. Code,
escaped colons and URL contents remain ordinary Markdown. A directive can follow
a completed link, code span, status or formatting span without a separating
space. For example, `[open](https://example.test):status[READY]{color=green}`.
Formatting inside a URL token does not activate a directive in its remaining
contents. Unsupported
attributes, formatted labels or inherited formatting retain the directive's
literal spelling with lossy diagnostics. Strict `adf convert` refuses them.
Issue/comment Markdown body routes retain their existing warning policy and can
submit the literal fallback. Validate through strict local conversion before
placing its extracted document in the native request's appropriate field. Existing
native statuses project as directives when complete structural comparison
preserves text, color and optional ID/style values. Status paragraphs also pass
comparison of the whole block before reporting no loss. Plain text immediately
before a status may use character references to preserve adjacent text and URL
boundaries. Unknown fields, unsupported marks, invalid values and native text
segmentation that Markdown cannot retain remain lossy and protected by anchored
editing. Unchanged anchored regions preserve exact original bytes. These local
proofs do not establish an issue editor or server concurrency protection.

### Keep the original ADF when editing

ADF rendering returns a Markdown review projection with `lossy` and sorted
`lossy_constructs`. Future nodes retain child content and labeled placeholders.
Keep the original native document. This projection cannot provide lossless edits.

### Check ADF and native field capabilities

`agent adf-matrix` lists every pinned node/mark definition and context variant.
`author` reports Markdown emission, `render` a dedicated Markdown projection, and
`terminal_render` the human issue-view renderer. `structural` projections can still
approximate attributes, marks or whitespace. `approximate` projections are lossy.
Placeholders retain labels and child content. Unsupported marks are dropped with
diagnostics. Preservation is separate from editing. `edit` remains false.
Structural bounds summarize the pinned catalog. Use full-document validation for
enum values, recursive constraints and context. Schema conformance does not
establish that a Jira field accepts a node.

`agent fieldtypes` describes the native `--json-input` route and illustrates
familiar field families. Rows are field values, not whole request documents or
tenant metadata. Wrap a value in `fields` under its actual field ID before
submitting it. The CLI preserves unknown types, numeric tokens, nulls and empty
arrays. It performs no scalar conversion or type-specific validation on this
route. Jira decides validity. Read field IDs, allowed values and operations
through the metadata paths reported by the command. Create metadata is paginated.

### Select a user from paginated results

`user search` returns visible active candidates at `users[]`. Each identity has
`account_id` and `display_name`. Email preserves absent, null and empty states.
Choose the intended account explicitly. `--start` and `--limit` map to server
paging. `--all` conflicts with either explicit flag and walks from zero.
Offsets advance by fetched rows, including locally excluded inactive, deleted
and unknown-active rows counted in `skipped`. A short page is not proof of
completion: `pagination.next_start` permits another probe. Completion means an
empty page was observed within the endpoint's window. At Jira's 1,000-match
ceiling, `truncated` and `truncated_reason: server_limit` require a narrower query.
Missing permission can return an empty successful search. Privacy can hide email.

## Save

Keep exit status with stdout. Retain the complete failure frame and any receipt
in its `data`. Stderr prose is not the machine contract. Identity fields are
objects such as `issue.key`. Multi-key results retain input order and report
per-key success or failure. Inspect every entry before deciding what remains.

### A create or clone failed after a possible write

`issue create` retains the attempted payload and any returned identity on
failure or interruption. `attempted` means the client operation was invoked.
It does not prove Jira received the request. `applied` records HTTP 201 even
when no usable identity was returned. `receipt` preserves one complete native
creation object, including optional transition/watchers and future members.
`issue clone` retains ordered source outcomes on total failure or interruption.
Failed rows can still carry `data.applied`, `data.clone` and `data.receipt`.
`issue_create_response_invalid` and `issue_create_followup_failed` exit `5`.
A failed native followup keeps the created identity. Inspect before retrying.
Repeat only the failed follow-up to avoid creating another issue.

### Keep attachment observations and deletion receipts

Attachment lists return the whole native attachment array per issue, without
an output window. Total failure and interruption retain ordered outcomes in
the full failure frame. A failed entry can retain a validated observation in
`data`. Inspect its `ok` and `error` before treating the read as complete.
`attachment_response_invalid` reports unusable native observations, including
missing arrays or repeated resource identities, rather than an empty issue.
Jira can resolve an old issue key to its current key. Results publish that
canonical identity. Deletion data requires a local preview or a validated
HTTP 204 acknowledgement. Inspect the resource after an uncertain write.

### An issue deletion has an uncertain outcome

`issue delete` retains every resolved key's outcome, selector provenance and
`delete_subtasks` request on total failure, cancellation or timeout. On a live
run, empty per-key `data` records a validated HTTP 204 acknowledgement. It may
accompany an error when both were observed. Missing `data` means deletion was
not acknowledged, not that the issue remains. A preview's empty data proves no
write. `issue_delete_response_invalid` exits `5`. Inspect affected issues and
subtasks before retrying. No deletion write is automatically replayed.

## Preconditions

The ADF recipes need `notes.md` containing GFM Markdown and `document.json`
containing a native ADF document. `--input -` reads piped stdin and refuses a
terminal immediately. Input uses the shared body size cap and requires UTF-8.

## Recover

### Identify the failure category

Exit codes are `0` success, `1` authentication, `2` not found, `3` validation,
`4` rate limit, `5` server, `6` canceled, `7` timeout and `8` local output failure.

### Input was canceled or timed out

Interactive forms in rich terminals honor the command context: cancellation
returns exit `6`. An expired deadline returns exit `7`. Machine stdout remains
separate from the form on stderr. On Unix, accessible text and confirmation
input interrupts active terminal reads when the command context expires and
preserves borrowed descriptors and their flags. Linux accessible password input
also interrupts active masked reads and preserves terminal modes, descriptor
flags and the borrowed input's Go read deadlines. Interrupted
login and logout return before verification or credential changes. Incomplete
answers at EOF and input cleanup failures return errors. They cannot authorize
a confirmation. Accessible text input on non-Unix platforms and password input
outside Linux currently check context before and after input but cannot interrupt
a blocked read. The HTTP `--timeout` flag bounds requests; it does not set a form
deadline.

`auth login --with-token` interrupts piped standard input on Unix when the
command context expires. Cancellation and deadlines return the same typed errors
before verification or credential storage. The read retains the 8 KiB token
limit and leaves borrowed file descriptors open, restoring Unix pipe flags.
Other native file reads, including non-Unix standard input, currently check the
context before and after reading but cannot interrupt a blocked read. The HTTP
`--timeout` flag does not set an input deadline.
Follow each error's hint and executable suggestions. Successful partial batches
can exit `0` while individual results failed.

### A user search stopped before completion

User-search page failures retain earlier validated candidates and the failed
page's offset in `pagination.next_start`. `user_search_response_invalid` means
Jira returned malformed data or repeated identities. It exits `5`, not an input
validation failure. Inspect retained candidates before repeating the read.

### An issue search stopped before completion

Issue search requires explicit completion and issue-array fields, unique issue
keys, and pages within the requested count. `search_response_invalid` (exit 5)
reports malformed or repeated pages. Later failures retain validated issues in
the error envelope in both JSON and compact modes. `--jq` does not filter that
receipt. Sparse empty pages still follow advancing cursors. If page and item
bounds coincide, `truncated_reason` reports `max_issues`. Selector searches
refuse before writes when their response cannot establish a complete target set.

### Epic counts or membership need verification

Epic reads use the same search boundary. `epic list --project KEY --all`
returns issue identities in `epics`. `epic board --project KEY --all` adds
child status counts. Counts retain the site’s status names, including zero baseline
buckets for To Do, In Progress and Done. Check `counts_complete`, epic
pagination and every row's `children` state before using totals. Unknown statuses
and incomplete child searches remain explicit. Unattempted rows have null counts.
A satisfied epic limit does not establish exhaustive totals. Both commands
accept repeated project flags and `--next-page-token` for the same query.
Jira's search index can lag recent parent or status edits even after an exhausted
search. Counts describe the visible index snapshots.

Epic membership commands publish the fixed parent request in `payload` and
ordered child receipts in `results`. `applied` means Jira acknowledged
the PUT. Previews and already unparented removals keep it false.
`remote_validated` means the destination epic was checked for assignment,
or the current relation was checked for removal. It does not validate every
editable child field. With `--verify`, `parent` records an immediate
issue read as absent, null or value. A mismatched or failed readback reports
`verify_failed` and `verification_error` while retaining the successful
write receipt. Inspect the issue before retrying.

### A comment listing stopped before completion

Comment listing reads native offset pages. `--start` must be zero or greater.
`--limit` must be positive. `--all` retains validated pages if a later read fails,
is interrupted or returns an unusable page. Inspect the full failure frame's
`data.comments`, `pagination`, `truncated` and `truncated_reason`. `read_failed`
and `response_invalid` are errors, while `max_pages` and `max_comments` are
successful safety caps. Suspect pages are excluded. Retry reads as needed.
Offset pagination does not guarantee a stable snapshot during concurrent edits.

### Filtering or writing output failed

Jq evaluation failures retain the unfiltered receipt when available. An output
destination failure can leave partial bytes. Neither outcome proves a mutation
did not happen. Inspect Jira before retrying. Writes are never replayed
automatically. The error frame is not itself passed through a jq filter.

## Next

Use `discover-cli` to inspect the selected operation's output shape. Use
`safe-mutation` or `raw-api` before submitting or retrying a write.
