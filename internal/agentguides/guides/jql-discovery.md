---
slug: jql-discovery
title: Discover JQL metadata and validate explicit queries
description: Compose queries locally, read live metadata and distinguish parser diagnostics from command failures.
when_to_use: Before writing, saving or running a JQL query, or when discovering custom field names and functions.
commands: [jira jql build, jira jql reference, jira jql validate, jira issue list, jira issue mine]
order: 35
---

## Decide

Use `jql build` for local structured composition without credential access.
Use `jql reference` for the configured site's visible fields, functions and
reserved words. Use `jql validate` to parse one or more explicit quoted queries.
`jql reference` and `jql validate` read Jira. Both work under read-only policy
without `--force`. They need the site and credentials described in Preconditions.
Local board composition instead needs an existing cached scope and public identity.

## Run

```sh
jira jql build --project KEY --assignee me --updated=-7d --output=json
jira jql build --key KEY-1..KEY-3 --order-by key --desc=false --output=json
jira jql reference --output=json
jira jql validate 'project = KEY' --output=json
jira jql validate 'project = (' --mode=warn --output=json
jira jql validate 'project = KEY' 'created >= -7d' --mode=none --output=json
```

### Choose the validation mode

`strict` performs semantic validation and omits the syntax tree when validation
fails. `warn` may retain a formed syntax tree despite semantic errors. `none`
skips semantic validation. Malformed syntax still produces diagnostics. Put
flags before `--` when a query begins with a dash.

## Save

### Save the composed query

For local composition, retain `jql`, `url`, `precedence` and `board_scope`.
No filters defaults to `updated >= -365d ORDER BY updated DESC`.
`--order-by=none` removes ordering. `--desc=false` selects ascending order.
Safe custom field names and `cf[12345]` identifiers are supported.
Date filters accept signed durations, absolute dates, comparisons and inclusive
`A..B` ranges with optional endpoints. Positive durations such as `+2w` render
as `2w`, which Jira accepts.

### Check the validation result

For validation, retain `submitted_queries`, `mode`, `queries` and `parsed`.
Jira can normalize query text. `queries[].valid` means no parser errors.
Warnings alone do not make it false. `parsed` preserves the complete native
response, nullable diagnostics, raw syntax trees and future members.

### Keep native reference metadata

For reference, retain `fields`, `functions`, optional `reserved_words` and
the complete native `reference`. Native flags such as `searchable` and
`isList` are strings. Native optional/null values and extensions remain intact.

## Preconditions

### Live validation and metadata reads

Configure the intended Jira site and credentials. Query validation and metadata
visibility are scoped to the current account. Metadata does not prove that a
query is valid, has matches or grants permission to change matching issues.

### Local composition with cached board scope

Local build does not require authentication. `--board` or `--board-id` uses
the exact saved filter cached by `issue list` with the same selector, site and
account. Omitted selectors inherit `default_board`. `--board=` suppresses it.
Explicit selectors override it. Names match case-insensitively. Numeric-looking
names remain names. Previously cached selectors keep their original spelling. Lookup can match
equivalent selector spellings only within the selected account cache.
If no snapshot exists for the configured site spelling, lookup can try the
canonical site with the same exact public account. This finds snapshots stored
under a normalized site without crossing accounts.
Local build accepts older snapshots and reports `cached_at`. Missing, malformed
or ambiguous retained scope refuses rather than broadening or fetching remotely.
`jql build` does not inherit `default_project` or `default_issue_type`.
Set non-secret `base_url` and `email` preferences or `JIRA_BASE_URL` and
`JIRA_EMAIL` for offline lookup. Keyring-only identity is unavailable locally.

## Recover

### The command succeeded but the query is invalid

A successful parser call exits zero even when a query has syntax or semantic
errors. Inspect `queries[].valid`, `errors` and `warnings`. Transport,
authentication, rate-limit, server and malformed-response failures are nonzero
command errors. Follow the error code and hint. Missing metadata does not prove
that a field or function is absent. Validation in `none` mode does not establish
semantic validity. These commands do not automatically run or repair a query.

### The requested board scope is unavailable

`board_cache_unavailable` means local composition cannot use a board snapshot.
Run `issue list` with an explicit matching `--board` or `--board-id` and
`--refresh --as-jql`, then retry the original command. For an inherited `default_board`,
pass its name explicitly as `--board NAME`.
Local build never fetches or silently drops a requested scope.
Explicit board selectors on `issue list --as-jql` use normal authenticated board
resolution and can read Jira. An inherited `default_board` uses only its local
snapshot for structured queries. Explicit `--jql` and `--saved` override it
before cache lookup. `--refresh` does not fetch an inherited snapshot.
The browser URL is optional and does not launch a browser. Built queries and
cached scope do not prove current board membership or server validity.

## Next

### Run the inspected query

Run an inspected query with `issue list --jql`. For structured filters, inspect
`jql build` or `issue list --as-jql` before searching. Assignee/reporter values `me`
and `currentUser()` mean the current account. `none`, `empty` and `unassigned`
mean an empty field. Positive status names and category comparisons (`<`, `<=`,
`>`, `>=` against To Do, In Progress and Done) combine with OR. Status exclusions
(`!NAME`) combine with AND. Use raw JQL for literal names starting with these
operators. Empty values and control characters are refused before any lookup.
Consult `machine-output` for
envelopes and `raw-api` for native requests beyond these domain commands.

Repeated positive values on one filter combine with OR; status exclusions and
different filters combine with AND. Values are quoted before they enter JQL. Board scope can be narrowed with
structured filters. Ambiguous board names or a non-final name-lookup page refuse;
use an explicit `--board-id`. `--refresh` bypasses an explicit board's cache.

### Walk and resume search results

`--limit` bounds the total across pages. `--all` drains within safety caps and
refuses an explicit `--limit`. `--all --unbounded` lifts collection and empty-page
caps; cancellation and missing or repeated cursors still stop the walk. Results
are collected in memory. `is_last` establishes completion; a truncation reason
means the search was stopped, not exhausted. Keep `next_page_token` to resume.

Invalid page shapes, repeated issue keys and oversized pages fail with
`search_response_invalid`. Later failures retain earlier validated issues and
page metadata; `--jq` does not filter that failure receipt. Human `--columns`
only selects and orders table columns; requesting an unfetched field is refused.

### Limit results to your assigned issues

Use `issue mine` when every result must be assigned to the current account.
`default_project` fills an omitted structured project only without raw/saved
query or active board. `default_board` uses the retained named-board filter for
structured list/mine queries. Explicit `--jql` and `--saved` override it without
requiring a board cache. Explicit board/raw selectors
remain exclusive. Machine `board.precedence=default_board` and `cached_at`
identify this snapshot. Human `--as-jql` stays a bare pipeable query.
Human `--web` prints one browser URL line. Human `--count` prints one result line.
Machine frames identify the snapshot used. Before live search/count/browser,
the constructed client must match the snapshot's public site/account. A different
stored credential identity refuses with `config_invalid`. Align public preferences
with `auth status`. Local previews do not authenticate or claim live binding.

Its structured filters and raw `--jql` conditions narrow a required
`assignee = currentUser()` scope. Raw `OR` conditions stay parenthesized.
Their trailing `ORDER BY` wins. Without raw JQL or board scope, ordering
defaults to updated ascending. `--reverse` reverses it. Ordinary `issue list`
still refuses raw JQL alongside structured filters.
With raw JQL or board scope, `--reverse` needs an explicit `--order-by`.
Raw ordering still takes precedence. A missing sort is refused before lookup.
Unbalanced groups or unclosed quoted literals are refused before a scoped
query reaches Jira. This prevents raw text from closing the required wrapper.

```sh
jira issue mine --project KEY --status '!Done' --as-jql --output=json
jira issue mine --jql 'project = KEY OR project = OTHER ORDER BY key ASC' --all --output=json
```

### Select fields and inspect search pages

Both issue search routes support explicit keys/ranges, epic parent keys and
created/updated/resolved date expressions. They share field projection, human
columns, cursor pagination and retained pages when a later request fails.
Use `--fields '*all'` for all native fields. `--detail` also requests the
list expansion preset: renderedFields, names, schema, transitions, operations
and changelog. `--expand names,schema` selects explicit metadata and may be
combined with a narrowed `--fields`. `--expand=` disables expansions.
Inspect `.issues[].details` for native issue metadata and `.search_pages[]`
for each accepted page's native completion, cursor, names, schema and warnings.
Later failures retain these page records, including validated empty pages.
Use `--count` for an approximate count and `--next-page-token` to resume the
same inspected query. Count and browser routes refuse detail/expansion flags.

### Run a saved local query

`issue list --saved NAME` restores the local named-query capability. Configure
`queries_path` with `config set`, or use `JIRA_QUERIES_PATH`. Its default retains
the historical `jira-cli/queries` directory under the platform config root.
Each `NAME.jql` file contains JQL, optionally preceded by YAML `---` or TOML
`+++` frontmatter with `name`, `description` and `project` string metadata.
Unknown metadata is ignored. The project metadata never adds a hidden filter.
Empty JQL, malformed frontmatter and path-shaped names are refused.
Use regular files or symlinks to regular files. Other input types are refused.
Unix FIFO inputs do not wait for a writer. Each query is read from one opened
regular file, so replacing its pathname does not change that read's contents.

```sh
jira config set queries_path ./queries --no-input
jira issue list --saved inbox --as-jql --output=json
jira issue list --saved inbox --all --output=json
jira issue mine --saved inbox --all --output=json
```

The filename stem is literal and case-sensitive. Completion uses local files
without credentials. `--as-jql` reads those files but never contacts Jira.
`--saved`, `--jql` and board selectors are alternatives. Ordinary lists retain
raw JQL's refusal of structured filters. `issue mine` instead groups the saved
query beneath its required current-account scope and retains its raw ordering.
Keep `.saved_query` with results: it records the source name, metadata and
original JQL, including when later pages fail. Shared limit, drain, resume,
field/detail, count and browser behavior applies. `saved_query_unknown` offers
available filename stems. Retry with one of those names. `saved_query_invalid`
requires repairing the local directory or named definition before searching.
