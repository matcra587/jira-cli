# jira

A Jira Cloud CLI for developers and coding agents. Manage issues, search,
boards, epics and worklogs from your terminal, with human-readable or
structured output.

## Start here

*   [Install jira](docs/installation.md)
*   [Find a workflow](internal/agentguides/guides)
*   [Set up profiles and authentication](internal/agentguides/guides/configure-credentials.md)
*   [Use jira with a coding agent](skills/jira-how-to/SKILL.md)
*   [Read release notes](CHANGELOG.md)
*   [Contribute](CONTRIBUTING.md)

## Help for your installed version

The CLI includes its own help and workflow guides. These commands work without
Jira credentials:

```sh
jira --help
jira guide
jira guide discover-cli
```

For command flags and output schemas, use `jira agent schema --output=json`.
See [Output & exit codes](docs/output.md) for the
machine-output contract.
