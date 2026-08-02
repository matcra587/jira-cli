# jira

Terminal-first Jira CLI for day-to-day developer workflows.

## Install

```sh
go install github.com/matcra587/jira-cli/cmd/jira@latest
```

Homebrew and GoReleaser release archives include release version metadata.
`go install github.com/matcra587/jira-cli/cmd/jira@latest` builds from source
and reports the module version in `jira version` (resolved from Go build
info; a plain `go build` reports a commit-derived `dev` version).
Release archives currently target macOS, Linux, and Windows.

See [docs/installation.md](docs/installation.md) for release archives, the
one-line installer, source builds, and uninstall steps. The examples below
assume `jira` is on `PATH`.

## Quick start

```sh
# 1. Create profile metadata
jira config init --no-input \
  --base-url https://company.atlassian.net \
  --email dev@example.com

# 2. Store an API token and verify Jira accepts it
jira auth login
jira auth status

# 3. List your issues
jira issue list
```

For issue creation, editing, comments, and ADF payloads, start with
[docs/issue/read.md](docs/issue/read.md). For classic API tokens, auth backends, and
1Password, use [docs/auth.md](docs/auth.md).

## Configuration

Configuration lives in `~/.config/jira-cli/config.toml` (on Windows,
`%AppData%\jira-cli\config.toml`). Use metadata-only profiles; credentials
belong in the OS keychain, 1Password, or environment fallbacks.

```sh
jira config init --no-input \
  --profile default \
  --base-url https://company.atlassian.net \
  --email dev@example.com
```

`config init` requires both `--base-url` and `--email`; omitting either exits
before writing the config file.

## Auth

```sh
jira auth status
jira auth login
jira auth token
```

`auth login` uses `auth_type = token` (the only supported value); it covers
both classic and scoped (granular) API tokens. Unsupported auth types are
rejected instead of being stored as fake authenticated profiles.

The 1Password backend uses the Go SDK when `onepassword_account` is configured
for desktop-app auth or `OP_SERVICE_ACCOUNT_TOKEN` is present for service-account
auth. macOS, Linux, and Windows release archives are built without CGO, so use a
CGO-enabled source build for 1Password-backed profiles.

For desktop-app auth, 1Password must be signed in and allowed to serve SDK
requests before `jira` can read items. In the 1Password app, open
Settings > Developer and enable Integrate with other apps. If you want
biometric approval, also enable the OS unlock option under Settings > Security.
Desktop-app SDK authorization is per account and per process, so separate
`jira` invocations may prompt separately even when the app is unlocked. Use the
system keychain backend for prompt-free day-to-day commands.

Further reading:

*   [1Password SDK desktop app integration](https://www.1password.dev/sdks#1password-desktop-app)

```toml
[[profiles]]
name = "work"
base_url = "https://company.atlassian.net"
auth_type = "token"
email = "dev@example.com"
secret_backend = "1password"
onepassword_account = "Team"
vault = "Engineering"
item = "jira-cli-work"
```

### Token support

jira talks to **Jira Cloud only**; Server/Data Center are not supported. The
CLI supports both classic Atlassian API tokens and scoped (granular) API
tokens, and `jira auth login` **detects which you have automatically** — there
are no scoped-specific flags. Classic tokens hit the site REST base URL
(`https://your-site.atlassian.net/...`); scoped tokens are auto-detected at
login (verified against the site, then the gateway) and routed through
Atlassian's gateway (`https://api.atlassian.com/ex/jira/<cloudId>/...`) via a
stored `cloud_id`. See [docs/auth.md](docs/auth.md).

## TUI

The persistent dashboard runtime is deferred during the CLI migration.
`jira tui` and `jira -i` report this explicitly. Use the issue and search
commands for terminal workflows. Existing `[tui]` settings remain stored but
are not executed. See [the dashboard status](docs/tui.md).

## Output

Non-TTY and agent environments emit JSON without prompts. Pick a mode
explicitly with `--output`; valid values are `auto`, `human`, `json`, and
`compact`.

```sh
jira issue list --output=json
jira issue create --json-input payload.json --no-input --dry-run --output=json
jira agent schema --output=compact
```

Where `payload.json` is at minimum:

```json
{"summary": "Fix login", "project_key": "PROJ", "issue_type": "Task"}
```

`project_key` and `issue_type` are required under `--no-input`. See the
embedded agent guide (`jira agent guide`) for richer payload shapes
including ADF descriptions and customfields.

For agent-facing contracts, use the embedded references instead of copying
recipes from the README:

```sh
jira agent guide
jira agent schema --output=compact
jira agent adf-matrix --output=json
jira agent fieldtypes --output=json
```

TTY commands render successful results through `clog` rich output:

```text
INF ℹ️ Listed issues count=0
```

Use `--output=compact` for jq-friendly data-only JSON and
`--output=human` to force `clog` rich text. A built-in `--jq EXPR`
filters the JSON in-process (no external jq needed) — strings print
raw, other results print as JSON per line.

## Commands

| Command | Docs |
|---------|------|
| `auth`, `config` | [auth.md](docs/auth.md), [config.md](docs/config.md) |
| `issue`, `epic` | [issue/read.md](docs/issue/read.md), [epic.md](docs/epic.md) |
| `jql`, `search` | [jql.md](docs/jql.md), [search.md](docs/search.md) |
| `worklog` | [worklog.md](docs/worklog.md) |
| `cache` | [cache.md](docs/cache.md) |
| `alias` | [alias.md](docs/alias.md) |
| `agent` | [agent.md](docs/agent.md) |
| `output`, `troubleshooting` | [output.md](docs/output.md), [troubleshooting.md](docs/troubleshooting.md) |

## JQL

`jira issue list` defaults to recently-updated issues
(`updated >= -365d ORDER BY updated DESC`). Override with `--jql` or builder
flags:

```sh
jira issue list
jira issue list --jql 'project = PROJ AND status = "In Progress"'
jira jql build --project PROJ --assignee me --status "In Progress"
```

See [docs/jql.md](docs/jql.md) for supported builder flags and Atlassian JQL
references.

## Aliases

```sh
jira alias set mine -- issue list --assignee me
jira alias set inbox "issue list --assignee me"
jira alias list
jira alias import aliases.yml
jira alias delete mine
```
