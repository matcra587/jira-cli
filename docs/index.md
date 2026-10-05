---
title: jira-cli
description: Discover Jira workflows, structured output, safe mutation previews, and the built-in CLI guides.
icon: material/rocket-launch-outline
---

# jira-cli

Terminal-first Jira Cloud CLI for developer and agent workflows.

!!! warning "This branch is a rebuild in progress"

    The v0.16 CLI includes Jira workflows and local discovery, but release
    acceptance is still in progress. Installation methods below select
    published releases; they do not install this development branch.

## What ships today

Use the installed binary's help and guides to discover its commands and
supported inputs:

```sh
jira --help
jira issue --help
jira agent guide discover-cli
jira agent schema --output=json
```

The command tree includes issue reads and mutations, JQL search, boards,
epics, worklogs, configuration, authentication, aliases, caches, release notes,
and updates. `jira api` provides an explicit Jira platform and Agile API
fallback. Local `jira adf` commands convert, render, validate, lint, and explain
native documents. These capabilities do not establish lossless remote editing
or protection against concurrent writes.

The embedded guides describe the installed command tree. Start with
`discover-cli`, then use `safe-mutation` for previews and write outcomes or
`machine-output` for schemas and output modes:

```sh
jira agent guide safe-mutation
jira agent guide machine-output
jira api get --help
jira adf --help
```

`jira version` reports version and available build provenance.
`jira completion <shell>` writes a completion script for `bash`, `elvish`,
`fish`, `nu`, `pwsh`, or `zsh`; its help describes installation.

Every command carries the same flags:

| Flag | What it does |
|------|--------------|
| `--output <mode>` | `auto`, `human`, `json`, or `compact` — see [Output & exit codes](output.md) |
| `--jq <expr>` | Filter machine output through a jq expression, in-process |
| `--jq-arg <name=value>` | Bind a string into the filter as `$name`, repeatable |
| `--jq-argjson <name=json>` | Bind JSON into the filter as `$name`, repeatable |
| `--color <when>` | `auto`, `always`, or `never` |
| `-v`, `--verbose` | Raise log verbosity, repeatable |
| `-q`, `--quiet` | Only show errors |

`-h` prints short help; `--help` prints the long form, including worked
examples. `-V` and `--version` print the bare version string.

## Preview a mutation

An explicit preview assembles the request locally without creating an issue:

```sh
jira issue create --project PROJ --type Task --summary "Preview an issue" --dry-run --no-input --output=json
```

For a real write, use metadata from your project and follow the command's help.
Use `--no-input` in scripts to suppress prompts; destructive operations may also
require explicit confirmation flags. Inspect reported outcomes before retrying a write whose
result is uncertain. The TUI is a separate milestone.

## Explore

<div class="grid cards" markdown>

*   :package:{ .lg .middle } [**Install**](installation.md)

    ---

    Install on macOS, Windows, or Linux — Homebrew, Scoop, mise, the
    one-line installer, `go install`, or a release archive.

*   :outbox_tray:{ .lg .middle } [**Output & exit codes**](output.md)

    ---

    The one `--output` selector, the JSON envelope, the typed exit codes,
    and the built-in jq filter.

</div>
