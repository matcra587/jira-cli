---
title: jira-cli
description: Manage Jira from your terminal. A CLI for developers and agents.
icon: material/rocket-launch-outline
---

# jira-cli

Manage your Jira issues from the command line.

jira-cli brings Jira into your terminal: read and search issues, create and edit
them, move them through their workflow, and add comments, links, and
attachments, all without switching to the browser. Run individual commands
or compose them in scripts. The persistent dashboard runtime is deferred.

Every command can also return a stable JSON envelope with typed exit codes, so a
script or an AI agent reads the result exactly as a person would. That makes
jira-cli as comfortable to automate as it is to drive by hand.

## First run

Install jira-cli ([all methods](installation.md)), then point it at your site and
make your first read:

```sh
jira config init --base-url https://example.atlassian.net --email you@example.com
jira auth login  # (1)!
jira issue list --assignee me
```

1.  Prompts for your API token and stores it (interactive on a TTY).

See [Authenticate](auth.md) for the 1Password backend and the headless (CI) flow.

## Explore

<div class="grid cards" markdown>

*   :package:{ .lg .middle } [**Install**](installation.md)

    ---

    Install on macOS, Windows, or Linux.

*   :key:{ .lg .middle } [**Authenticate**](auth.md)

    ---

    Create a profile and store an API token in the OS keyring or 1Password.

*   :gear:{ .lg .middle } [**Configure**](config.md)

    ---

    Profiles, defaults, aliases, themes, and the full config reference.

*   :memo:{ .lg .middle } [**Work with issues**](issue/read.md)

    ---

    Read, create, edit, transition, comment, link, and attach.

*   :desktop_computer:{ .lg .middle } [**Dashboard status**](tui.md)

    ---

    The persistent dashboard runtime is deferred during the CLI migration.

*   :mag:{ .lg .middle } [**Search and JQL**](search.md)

    ---

    Find issues with filter flags, raw JQL, or saved queries.

*   :outbox_tray:{ .lg .middle } [**Output and scripting**](output.md)

    ---

    The JSON envelope, exit codes, and compact mode for scripts and agents.

*   :robot:{ .lg .middle } [**Drive it from an agent**](agent.md)

    ---

    Machine-readable command schema, per-workflow runbooks, and the live ADF matrix.

</div>
