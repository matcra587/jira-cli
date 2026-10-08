---
slug: cache-metadata
title: Prime and inspect scoped metadata
description: Store native Jira metadata, inspect freshness locally and refresh explicitly without losing partial outcomes.
when_to_use: Before repeated metadata discovery or when refreshing a cached board list or issue link types.
commands: [jira cache labels, jira cache projects, jira cache epics, jira cache fields, jira cache issuetypes, jira cache linktypes, jira cache boards, jira cache statuses, jira cache priorities, jira cache resolutions, jira cache refresh, jira cache issuekeys, jira cache inspect, jira cache clear]
order: 42
---

## Decide

Use the cache when you need to reuse Jira metadata or supply local shell
suggestions. Entries belong to the selected site and account. A valid, fresh
entry saves a Jira request. `--force` fetches again even if the entry is fresh.

Choose `labels`, `projects`, `epics`, `fields`, `issuetypes`, `linktypes`,
`boards`, `statuses`, `priorities` or `resolutions`. Each command reads metadata
visible to your account and stores it locally. This is called priming the cache.

Use `cache inspect` to check what is already stored. Use `cache refresh` to
check several resources together. Use `cache clear` to remove entries. Clearing
does not refresh them. Recent issue keys and transition hints have separate
rules below.

Previews, inspection and completion need public `base_url` and `email` values
from settings or the environment. Check Preconditions before running the
examples. Fetching new metadata requires authentication. These local reads and
previews do not open credentials.

## Run

### Preview, then store the metadata

Start with a preview of the resource you need. The last two commands below
read local issue-key history and inspect the cache:

```sh
jira cache fields --dry-run --output=json
jira cache boards --dry-run --force --output=json
jira cache linktypes --dry-run --ttl-minutes=60 --output=json
jira cache refresh fields labels --dry-run --parallelism=2 --output=json
jira cache issuekeys --output=json
jira cache inspect --output=json
```

Inspect the preview before removing `--dry-run`. The real command can change local
cache files, works under Jira read-only policy and needs no confirmation.
A failed metadata read leaves the old entry intact.
After a refresh, inspect every resource result before retrying. A successful
command can contain failed resources. Recover explains the result paths.

Without resource names, `cache refresh` checks all ten resources. With names,
its results keep the requested order and use only the first occurrence of each name.
It defaults to one worker. Set `--parallelism`, or `-p`, to a value from 1 to 16
to change the number of workers.

### Check what is available locally

A malformed entry wrapper does not provide a trustworthy key, size or write
time. Whole-cache inspection still shows unknown entries. `unclassified`
counts those entries across the selected site/account cache, even when you
inspect only one resource.

To remove entries, use `cache clear`. A live clear in headless, agent or
`--no-input` mode requires `--force`. A clear preview needs no `--force`.
Removing entries does not collect replacement metadata.

### Use recent issue keys

`cache issuekeys` shows the selected account's fifty most recent issue keys,
newest first. Issue reads and live issue operations record valid typed keys in
the ordinary account-scoped cache. Repeating a key moves it to the front.
Keys from one search enter history together, in Jira's order, ahead of older
entries. A history-recording failure does not fail the
operation that produced the key.

Previews leave history unchanged. History does not expire, and refresh leaves
it alone. Use `cache clear issuekeys --force` to reset it. Completion reads the
same history locally. Missing or malformed history produces no candidates.

### Get shell suggestions from the cache

Completion uses fresh local snapshots of the ten resources. It applies each
resource's default freshness period, rather than fetching or refreshing data.
It never contacts Jira, opens credentials or changes storage.

On supported flags, projects offer keys, fields offer native IDs, and epics
offer issue keys. Named metadata retains its names. Board-name selectors offer
locally unambiguous names. Board-ID selectors offer numeric IDs, including boards
with ambiguous names. For example, `--board` selects names and `--board-id`
selects IDs. Board descriptions include observed
type and project information, with incomplete project enrichment marked.

Missing, stale, future-dated or malformed snapshots produce no suggestions.
A fresh snapshot can still contain only some values, and a value may be
unavailable for the project or operation you intend. Inspect the cache and
explicitly prime or refresh it when needed. CSV flags complete comma-separated
values. Repeated epic `--project` values remain literal.

Attachment completion changes with the input form:

| Input form | Positional suggestions | Flag suggestions |
| --- | --- | --- |
| Without `--file` | Recent issue keys for the first argument; local paths for later arguments | Not applicable |
| With `--file` | Issue keys in every position | CSV file paths for `--file` |

A quoted comma remains part of a filename, and completed CSV fields keep their
spelling. Quote the whole CSV argument when any member needs quoting. This
matters especially when continuing a bare comma expression in PowerShell.
Path suggestions list one bounded local directory. They read neither file
contents nor credentials. Missing or incomplete inventories give no suggestions.

In Fish, an unquoted separate value can be ambiguous when it resembles the
active inline option: `--file --file=name`. Quote the whole CSV field to
complete that literal filename: `--file '"--file=name"'`.

## Save

### Did this command write an entry?

Keep `resource`, `items`, `count`, `pages`, `complete`, `cache_state`,
`from_cache`, `attempted`, `saved` and `written_at` from resource results.
The native objects preserve unknown members and explicit nulls. Boards also
return an enriched native snapshot in `payload`, including associated project
keys and project failures.

`saved` acknowledges a write made by this invocation. Reusing an existing entry
sets `from_cache=true`, `attempted=false` and `saved=false`.
In a preview, `would_fetch` tells you whether the real invocation would collect
new metadata.
Old data can remain available for inspection without being fresh.

### Is the cached collection complete?

Inspection returns `entries[]` and `resources[]`. Keep each resource's
`cache_state`, `ttl_minutes`, count and information needed to resume collection.
There are two different meanings of `complete`: the inspection's value reports
whether the local scan finished. A resource's value describes its cached
collection.

An entry's presence does not prove that its payload is valid. The inventory
scan and resource reads are separate observations, and another process can
change the cache between them. `known_payload_bytes` excludes wrappers and
entries with unknown sizes.

## Preconditions

### Which account does a preview use?

Previews need public `base_url` and `email` values from settings or the
environment. They open no credential store or Jira client and create no cache
storage. An account available only from the keyring cannot select the cache
for an offline preview.

Local history reads, completion and inspection need the same explicit public
site and account values. They do not open the credential store or contact Jira.
Inspection does not change storage.

Commands that fetch metadata use normal configured/keyring authentication.
They share the site/account cache used by existing consumers. Board and
link-type commands store the exact formats used by `boards list` and
`issue link types`. Those readers keep their own freshness policies.

### How long is an entry fresh?

| Resource | Default freshness |
| --- | --- |
| Labels | 1 hour |
| Epics | 4 hours |
| Projects | 7 days |
| Fields | 14 days |
| Boards | 28 days |
| Issue types and statuses | 30 days |
| Link types, priorities and resolutions | 90 days |

`--ttl-minutes` overrides freshness for this invocation. A zero or negative
value uses the default for that resource.

## Recover

Read every refresh row before deciding what to retry. Each `results[]` row has
`resource`, `ok`, `data` and, on failure, `error`. Full JSON puts the rows at
`data.results[]`. Compact success starts at `results[]`.

Refresh returns `results[]` with `resource`, `ok`, `data` and, for failures,
`error`. Inspect every row: mixed success retains failed rows in a successful
frame; total failure or interruption retains all rows in a failure frame.
Failed rows keep their plan, native observations and late storage receipts.
Resource names are metadata identities, not issue references.

Inspect nonzero failure responses too. Failed rows keep their plan, native
observations and any storage write acknowledged late. Do not repeat the whole
refresh just because one resource failed. See `machine-output`'s Save section
for inspecting a saved response.

### Collection or storage stopped partway through

A later collection failure keeps validated earlier pages and information needed
to resume, but never replaces the cache. A storage failure keeps the fetched
metadata with `saved=false`. This means the write was not acknowledged as
successful. A storage error can occur after the file was replaced, so inspect
the cache before deciding what remains to do.

Paged resource commands also have collection limits and can save a snapshot
explicitly marked incomplete. Check `truncated_reason`, `next_start` or
`next_page_token`. Use `--unbounded` to remove collection caps for labels,
projects, epics, boards, priorities and resolutions and to refetch incomplete
snapshots. Board project enrichment stays bounded. Its failures remain in
`payload.project_failures`.

A cache hit or a complete collection does not prove visibility beyond your
permissions.

### Transition suggestions are old or missing

Explicit transition-list reads store issue workflow hints in the same
site/account cache. These hints supply shell completion suggestions.
Hints become stale five minutes after the observed read. Stale hints remain
inspectable but no longer supply completion suggestions. Hints never authorize
a write. Inspect them with `cache inspect transitions`. Remove them with
`cache clear transitions --force`.

A completed failed read invalidates old hints, even if a later issue cancels
the command. An attempted transition write invalidates hints even when its
result is uncertain. Canceled or unstarted reads keep previous observations.

## Next

Use `boards list` or `issue link types` to consume compatible primed entries.
Use `--force` when permissions or site metadata changed. Consult `raw-api` for
native requests beyond these primers and `machine-output` for failure frames.
