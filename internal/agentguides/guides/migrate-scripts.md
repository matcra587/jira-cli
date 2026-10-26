---
slug: migrate-scripts
title: Update scripts for the native command layout
description: Map legacy commands, mutation payloads and JSON paths to the current CLI.
when_to_use: After upgrading a script that uses search jql, flat issue payloads or resource-specific cache arrays.
commands: [jira agent schema, jira issue list, jira issue view, jira issue create, jira issue edit, jira cache labels, jira cache refresh]
order: 68
---

## Decide

Use this map when updating scripts from the legacy CLI to the native command
layout. Read the installed command's schema before changing flags or JSON paths.
Configuration migration is separate; see `migrate-configuration`.

## Run

Inspect the commands and preview a query and native create payload:

```sh
jira agent schema --path 'issue list' --output=json
jira agent schema --path 'issue create' --output=json
jira issue list --jql 'project = KEY' --as-jql --output=json
jira issue create --json-input issue.json --dry-run --output=json
```

### Replace commands and flags

| Earlier invocation | Current invocation | Check |
| --- | --- | --- |
| `search jql 'project = KEY'` | `issue list --jql 'project = KEY'` | Raw JQL overrides inherited board scope. |
| `search jql QUERY --cursor TOKEN` | `issue list --jql QUERY --next-page-token TOKEN` | Resume the same query. |
| `search jql QUERY --full` | `issue list --jql QUERY --fields '*all'` | Add `--expand` only for required metadata. |
| `cache labels --refresh` | `cache labels --force` | Other metadata primers use the same flag. |
| `issue view KEY-123 KEY-124` | One `issue view` invocation per key | Use `issue list --key KEY-123,KEY-124 --fields '*all'` when search semantics are suitable. |

Search `--limit` now bounds the total collected across requests. `--all` drains
within safety caps; inspect `pagination.is_last`, `truncated` and its reason.
See `jql-discovery` for query composition and continuation rules.

### Write native mutation bodies

An earlier create body could use CLI aliases at the top level:

```json
{"project_key":"KEY","issue_type":"Task","summary":"Example","labels":["triage"]}
```

The current `issue.json` uses Jira's `fields` object:

```json
{"fields":{"project":{"key":"KEY"},"issuetype":{"name":"Task"},"summary":"Example","labels":["triage"]}}
```

Pass a filename to `--json-input`, or `-` for stdin. It is not an inline JSON
argument. Native `update` operations remain separate from `fields`. Use the
mutation guide for conflict rules, ADF input and remote validation; a local
preview does not establish that Jira will accept the payload.

## Save

These paths assume `--output=json`. Compact success starts directly at the data
object, so omit `.data` there. Compact failure remains a full error envelope.

| Earlier path | Current path | Meaning |
| --- | --- | --- |
| `.data.issues[].summary` on ordinary lists | `.data.issues[].fields.summary` | Native field location. |
| `.data.issues[].status` on ordinary lists | `.data.issues[].fields.status.name` | Native status object. |
| `.data.issue.editmeta` | `.data.issue.details.editmeta` | Request `--expand=editmeta`. |
| `.data.issue.transitions` | `.data.issue.details.transitions` | Request `--expand=transitions`, or use `issue transition list`. |
| `.data.labels` from `cache labels` | `.data.items` | All metadata primers use `items`; boards also expose `payload`. |
| `.data.fetched_at` from a metadata primer | `.data.written_at` | Cache write timestamp; use `from_cache` and `saved` to distinguish reuse from a new write. |

Single-issue identity and fields remain at `.data.issue.key` and
`.data.issue.fields`. View metadata expansions are now opt-in; all native fields
remain available by default. Do not assume that an omitted metadata block means
an issue has no transitions or editable fields.

Cache priming/refresh receipts retain `complete`, truncation reasons and resume
cursors. They omit the former `force`, `unbounded`, `ttl_minutes` and
`age_seconds` echoes. Inspection still reports freshness diagnostics. See
`cache-metadata` for resource outcomes and `machine-output` for failure receipts.

## Preconditions

The commands above are local discovery and previews. Prepare `issue.json` before
running its preview. Real reads require the intended site and credentials;
writes also require the mutation policy and Jira permissions.

## Recover

Check the process exit code and `.ok` before extracting success data. Machine
failures are on stdout, without a duplicate human error on stderr. Read their
error codes and retained outcomes before retrying; a failure can follow an
acknowledged write. Do not retry a mutation just because its output was lost.

An unknown command or flag calls for schema discovery. A missing JSON member
calls for checking output mode, requested fields and the operation's schema.
Do not hide either failure with an empty-list fallback.

## Next

Run scripts against owned fixtures first. Check issue identities, native values,
completion and side effects, then validate the intended live workflow. Preview
success alone does not prove write success or compatibility with site rules.
