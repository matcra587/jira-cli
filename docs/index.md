---
title: jira-cli
description: Terminal-first Jira Cloud CLI for developer and agent workflows — currently mid-rebuild, with the output contract landed first.
icon: material/rocket-launch-outline
---

# jira-cli

Terminal-first Jira Cloud CLI for developer and agent workflows.

!!! warning "This branch is a rebuild in progress"

    jira-cli is being rewritten from the ground up. The Jira-facing commands
    are not back yet. What is documented here is what the binary on this
    branch actually does — nothing is described ahead of shipping, because
    documentation that runs ahead of the code is worse than no documentation
    at all.

## What ships today

Two commands, plus the root surface and the flags every command shares.

```sh
jira version      # version, and build provenance where the binary carries it
jira completion fish
```

`jira version` prints the bare version string for a person and the full
build detail — commit, dirty state, build date, Go toolchain, target
platform — in the machine modes. `jira completion <shell>` writes a
completion script to stdout for `bash`, `elvish`, `fish`, `nu`, `pwsh`, or
`zsh`; run it with `--help` for the per-shell install line.

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

## What is not here yet

Issues, search, boards, epics, worklogs, config, auth, the TUI — none of it
is on this branch. Earlier releases shipped those commands; their
documentation was removed rather than left standing, because it described a
binary you cannot install from here. It returns page by page as each command
lands, generated from the live command tree so it cannot drift again.

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
