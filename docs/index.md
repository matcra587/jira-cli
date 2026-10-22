---
title: jira-cli
description: Install jira-cli and find guides for Jira workflows, authentication and structured output.
icon: material/rocket-launch-outline
---

# jira-cli

A Jira Cloud CLI for developers and coding agents. Manage issues, search,
boards, epics and worklogs from your terminal, with human-readable or
structured output.

## Get started

[Install jira](installation.md), then use the help and guides built into your
installed version. Discovery works without Jira credentials:

```sh
jira --help
jira guide
jira guide configure-credentials
```

The guide index lists the available workflows. Start with
`configure-credentials` to set up profiles and authentication, `discover-cli`
to choose commands, or `safe-mutation` to preview changes and check their
outcomes. Run `jira guide SLUG` with a name from the index to read a guide.

For exact flags and output schemas, use `jira agent schema --output=json`.
The linked guide sources follow `main`. Use the installed help, schemas and
guides for instructions that match your version.

## Explore

<div class="grid cards" markdown>

*   :package:{ .lg .middle } [**Install**](installation.md)

    ---

    Choose a package manager, release archive or source build for your platform.

*   :book-open-variant:{ .lg .middle } [**Workflow guides**](https://github.com/matcra587/jira-cli/tree/main/internal/agentguides/guides)

    ---

    Browse the guide sources, or use `jira guide` to read your installed version.

*   :robot:{ .lg .middle } [**Agent instructions**](https://github.com/matcra587/jira-cli/blob/main/skills/jira-how-to/SKILL.md)

    ---

    Route coding agents to the CLI's workflow guides and command schemas.

*   :outbox_tray:{ .lg .middle } [**Output & exit codes**](output.md)

    ---

    Read output modes, JSON envelopes, exit codes and jq filtering behavior.

</div>

See [Release notes](changelog.md) for changes and
[Contributing](https://github.com/matcra587/jira-cli/blob/main/CONTRIBUTING.md)
for development setup and checks.
