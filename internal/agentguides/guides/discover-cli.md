---
slug: discover-cli
title: Discover commands before acting
description: Read the installed CLI's guides, canonical command tree, flags and output shapes.
when_to_use: When starting a Jira CLI task or recovering from an unfamiliar command or flag.
commands: [jira, jira agent guide, jira agent schema, jira alias, jira config, jira auth whoami, jira me, jira open, jira issue attachment, jira release-notes, jira update]
order: 10
---

## Decide

Start with the guide index. Use the runtime schema to choose a command, then
request that command's subtree before composing flags or inspecting its output.
Discovery works without credentials and sends no Jira requests. Bare `jira`
shows native help in human mode and the same default runtime schema in machine
modes. A pipe defaults to JSON. An agent environment defaults to compact.
Explicit `--output` and `--jq` retain their normal meaning.

## Run

### Read the schema and select a workflow

The examples include local discovery, settings previews and an effective-settings
read. `config list` can open the selected credential store to resolve identity.
Prepare `aliases.yaml` for the import example. `aliases.toml` can be absent.
The sections below explain each workflow and its requirements.

```sh
jira --output=json
jira agent guide --output=json
jira agent schema --output=json
jira agent schema --path 'issue create' --jq '.data.schema'
jira agent schema --path open --output=json
jira open KEY-123 --output=json
jira release-notes --latest --output=json
jira rn 0.7.7 --output=compact
jira alias list --config aliases.toml --output=json
jira alias set mine 'issue list --assignee me' --config aliases.toml --dry-run --output=json
jira alias delete mine --config aliases.toml --dry-run --output=json
jira alias import aliases.yaml --config aliases.toml --dry-run --output=json
jira config get read_only --output=json
jira config list --output=json --jq '.data.settings[] | select(.key == "read_only")'
jira config set read_only false --config aliases.toml --dry-run --output=json
jira config init --base-url https://example.atlassian.net --email person@example.com --config new-settings.toml --dry-run --output=json
jira issue attachment download 10042 --to . --as=report.bin --dry-run --output=json
```

Canonical paths include or omit the `jira` prefix. The default full schema
indicates which output shapes exist. `--path` embeds the selected subtree's shapes.
`--shapes` embeds and pools shapes throughout the tree. `--path` and `--shapes`
are mutually exclusive. Resolve local `$ref` values against the extracted schema
object, where its `$defs` live.

Read `extensions.usage`, declared `extensions.arguments`, flags, operation
classes, actual examples and `output_schema`. An argument maximum of `-1` means
an unbounded list. Hidden compatibility flags remain accepted but unadvertised.
Raw request bodies have no invented domain input schema.

### Read the current account

`auth whoami` and `me` read the current Jira account. Machine output preserves the
native account at `data.user` in JSON and `user` in compact mode, including
privacy-hidden email and additional fields. Read `accountId`, `accountType`,
`displayName`, `emailAddress` and `timeZone` from that account rather than
configured settings. Human output shows the returned identity. Use `auth status`
when credential source and token masking are the information needed.

### Open an issue URL

`open ISSUE_KEY` resolves one issue's browse URL from the configured site,
without fetching Jira or accessing credentials. Machine modes return
`issue.key`, `web_url` and `opened=false`. Human output starts the selected
browser and always prints the URL. `BROWSER` names one executable, including
paths with spaces. It is not a shell command or argument list. When unset,
the platform default is used. A failed process start produces a warning and
keeps the usable URL. `opened=true` reports process handoff, not page loading
or issue existence. Exactly one distinct key is required after the normal
comma/range expansion. Equivalent duplicate keys collapse to one key.

URL-only issue view/comment reads with `--web` use the same settings-only
URL handling. Completion history uses only a site and account
explicitly present in configuration. Without that account scope, the URL read
does not update history. No
keyring lookup is performed to record a locally resolved URL. Native browser
appearance and Jira access permissions require their own verification.

### Read CLI release notes

`release-notes` reads jira-cli's embedded changelog without network or credentials.
No arguments select all releases, newest first. `--latest` or one version select
a single release. `rn` is its alias. Machine output contains `releases`, each
with version, tag, heading metadata and ordered change sections. Human output
keeps Markdown when unstyled and renders it with the resolved theme when styled.
Long interactive documents use `JIRA_PAGER`, then `PAGER`, or the built-in pager.
`--no-pager`, `--no-input`, pipes, machine modes and agent sessions never page.
These are tool releases, unrelated to a Jira project's releases.

### Update the installed CLI

`update` (alias `up`) resolves the executable's installation owner and retains
observed installation facts. Start with the `update-cli` guide. Its archive and
Homebrew preview checks the public release service, unlike a local Jira mutation
preview. Headless installation requires `--force`. Managed channels return
their own update command. Discovery and help perform no update checks.

### Create and use command aliases

Aliases are literal shortcuts in the selected config file. Management needs no
Jira credentials. One quoted expansion string is stored verbatim. Several
arguments are individually quoted, so `jira alias set mine -- issue list
--jql 'project = DEMO'` retains the query as one argument. Use `--` before
expansion arguments containing flags. Expansion uses POSIX token quoting,
including comments introduced by unquoted `#`, without executing a shell.
Built-in commands take precedence. Caller arguments follow the expanded
command. Repeated flags retain their ordinary last-value precedence.

`jira mine --output=json` runs the stored command through the normal output,
validation, read-only and mutation policies. Its receipt names the actual
operation, such as `issue.list`. Cycles and missing chain targets fail before
client construction. Inspect malformed definitions with `alias list`, then
repair them with `alias set` or remove them with `alias delete NAME --force`.
Headless deletion requires `--force`. Previews do not write.

### Import aliases

Imports accept one YAML string mapping from a file or piped standard input.
Forward chains are validated together. Invalid entries and their dependants
are skipped with reasons. Malformed YAML, duplicate keys and non-string data
refuse the whole import. Existing names are kept unless `--clobber` is set.
An invalid replacement retains the old alias while dependants of that invalid
proposed replacement stay skipped. Changed saves normalize TOML formatting and
preserve declared settings. Previews and no-ops preserve file bytes.

### Create a settings file

`config init --base-url URL --email EMAIL` creates a new settings file using
only the two explicit account values. Both flags are required in every mode.
The command never prompts, accesses credentials or contacts Jira. Environment
values and defaults are not saved. Use `--config PATH` to select the destination
or omit it to use the platform configuration path. Preview with `--dry-run`,
then repeat without it and with `--no-input` to publish. `would_create` describes
the candidate. `created` reports publication and remains false during preview.
Existing files, directories and dangling links are refused with `config_changed`,
including during preview. Use `config set` or `config edit` to update them.
Creation requires filesystem hard-link support and never replaces a competing
creator. On a storage error, inspect the selected file before retrying: a
directory sync can fail after publication, in which case `created=true` remains
in the error receipt. Credentials belong to `auth login`.

### Edit settings in an editor

`config edit --config PATH --dry-run` reports the selected path and configured
editor without opening a terminal or changing the file. Store a preference with
`jira config set editor 'code --wait' --no-input`. Selection uses `JIRA_EDITOR`,
then the stored `editor`, then `VISUAL`, then `EDITOR`. Commands are split into
arguments and run without a shell. Use an editor's wait flag when necessary.
An explicit `JIRA_EDITOR` also permits repairing invalid TOML. A real edit needs
terminal stdin and stderr. It validates exact edited bytes, permits repairing
an invalid existing config, and refuses a changed, removed or redirected file.
Rejected work is saved privately beside the resolved target with a `.rejected`
suffix when possible. Inspect the error for its location or recovery failure.
The editor must leave a regular file or a symlink to one. Nonregular scratch
input is refused without publication. Saved regular bytes remain recoverable
after editor failure or cancellation. Unreadable input retains its read cause
alongside the editor or cancellation error.
This local snapshot check is not an atomic lock against external writers.

### Distinguish stored settings from effective values

`config get KEY` reads the stored file value, without defaults, environment,
flags or keyring credentials. `present=false` distinguishes an absent key from
an explicitly stored empty string, false or zero. A missing default file is
normal. A missing explicit `--config` path is refused. Secret values are redacted.
Use `config list` to inspect effective values and their winning layers instead.

### Preview and save a setting

`config set KEY VALUE` validates a scalar assignment before publication.
`--dry-run` reports stored previous values and the candidate without creating
files or directories. `would_change` describes the candidate. `changed` and
`created` describe actual publication and remain false in a preview. An absent
declaration differs from an explicitly stored empty string, false or zero.
Unchanged typed values preserve bytes and metadata. Real changes normalize TOML
while retaining other declarations. Invalid UTF-8, file syntax/types and unsafe
site URLs are refused. Setting `base_url` can repair a malformed site URL, with
the original value redacted. Use `auth login` for credentials and the alias
commands for validated command shortcuts.

### Download an attachment to a local file

Attachment IDs come from `issue attachment list KEY-123`. Downloads take one
ID, without an issue key, and write content to files rather than stdout.
Use `--to` for an existing directory and `--as` for a plain filename. Without
`--as`, the server filename is reduced to its final name. A local preview
resolves a supplied name. `--validate-remote` reads metadata to resolve the
default name. Resolved previews check the destination without writing. An
unresolved preview cannot check a collision. Live writes check again.

Existing files require `--overwrite`. Failed requests or streams preserve the
original file. A complete download replaces its entry. Successful receipts
carry the actual `bytes_written`, including zero for an empty file, and
`overwrote`. Preview receipts omit both publication fields. Read-only mode
permits downloads but refuses uploads and deletions. Attachment uploads have
no deduplication: inspect per-key outcomes before retrying a failed batch.

## Save

Keep the selected canonical path and contract version. JSON places the artifact
at `data.schema`. Compact mode places it at `schema`. The output schema describes
operation data, while the root's output contract describes the invocation frame.

## Preconditions

## Recover

On an unknown path, execute the offered discovery suggestion and choose a
canonical path from that tree. For unknown guide names, read the index. Do not
substitute a guessed mutation or silently fall back to a raw request.

## Next

Read `machine-output` before writing a jq filter. Read `safe-mutation` before
changing Jira and `raw-api` when a domain command cannot express the request.

### Install shell completion

Shell completion supports Bash, Elvish, Fish, Nushell, PowerShell and Zsh.
Run `jira completion SHELL` to generate a script.
Completion offers shell names and supported sort choices.
Positional suggestions follow each argument’s declared role. Issue-key slots
offer issue keys. Comment and web-link ID slots do not offer issue keys.

Regenerate and load the script after upgrading the CLI. Bash inserts candidate
identities without their display descriptions, including comma-separated values.
Literal choices and filenames retain spaces, quotes and glob characters.
Elvish callbacks use the executing shell's portable null device on Windows and
Unix. Dynamic candidate descriptions stay in the menu. Inserted values retain
only their identities, including selected comma-list values and inline flags.
Zsh retains dynamic suggestions on runnable commands that also have subcommands.
Completion uses the selected local configuration for its offline suggestions.
It does not authenticate or contact Jira. Cached suggestions are hints,
not evidence that a later mutation is permitted.

### Use cached transition suggestions

After `jira issue transition list ISSUE_KEY`, positional target slots can suggest cached
transition IDs with names and destinations. Hints belong to that issue and the
selected site/account. They become stale five minutes after observation.
Stale hints remain inspectable but no longer supply completion suggestions. Multiple keys
need a shared ID with the same known destination. Inspect them with
`jira cache inspect transitions` or remove them with
`jira cache clear transitions --force`.
The cache does not authorize mutations. Native input and unresolved selectors
leave target suggestions empty, and completion never reads payload files.

### Complete help topics

Help topics follow the command namespace: after `jira help issue`, complete an
issue command. After `jira help issue comment`, complete a comment command.
Built-in command aliases use the same paths. Leaf commands and unknown topics
offer no further topic names. These suggestions use the local command tree.

### Quote CSV completion values in PowerShell

PowerShell preserves quoted configuration paths and literal inserted values.
completion descriptions remain tooltips. For CSV values containing spaces or
commas, quote the whole argument, for example `--label 'first,space name'`.
Completions retain CSV field quoting and selected members. Quote short inline
CSV values too (`-f='one,two'`). Bare short inline CSV is invalid PowerShell
syntax. Regenerate the script after upgrading to receive native generator fixes.
