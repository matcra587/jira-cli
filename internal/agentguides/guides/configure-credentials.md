---
slug: configure-credentials
title: Select and verify credential storage
description: Configure the OS keyring, environment or direct 1Password SDK without persisting tokens in settings.
when_to_use: When choosing a credential backend or recovering from a credential-store failure.
commands: [jira config get, jira config set, jira config list, jira config profile, jira auth login, jira auth status, jira auth token, jira auth logout]
order: 65
---

## Decide

Choose where the CLI obtains its token. Settings hold public coordinates and
preferences. Keep token values out of settings and command arguments.
Login previews verify against Jira. They do not save credentials or settings.

### Select a profile

Use `--profile NAME` or `-P NAME` for one invocation. Otherwise `JIRA_PROFILE`
selects the name, followed by the file's `default_profile`. Unknown names fail.
`config profile default NAME` changes the default for later invocations.
Environment settings still override the selected profile; `config list` shows
the selected name alongside effective values and their sources.

Create a profile with `config profile add NAME --base-url URL --email EMAIL`,
then use `auth login --profile NAME`. Existing flat files remain readable.
Adding a profile to one retains its settings and credential reference under
`default`. Renaming a profile keeps its credential. Removing one keeps its
credential too; log out first if you want that credential removed.

Named configuration uses this shape:

```toml
format_version = 1
default_profile = "work"

[profiles.work]
base_url = "https://example.atlassian.net"
email = "person@example.com"
secret_backend = "env"
read_only = true
timeout = "30s"
```

Theme, glyph, query-directory and alias settings belong at the document root.
An editor can be global or overridden by a profile. `config get` and `config set`
use the selected profile for account and workflow settings. Local profile
listing shows configured metadata without checking authentication.

### Use the OS keyring or environment

The default `secret_backend=keyring` stores a site/account/token record in the
OS keyring at the selected profile's stable `credential_id`. Profile creation
assigns that locator; an authored profile without one needs a unique UUID before
a keyring login can save. `keyring=false` disables that backend. `secret_backend=env` opens no
persistent store and refuses token saves; supply `JIRA_BASE_URL`, `JIRA_EMAIL`
and `JIRA_API_TOKEN` through your environment.

### Use 1Password

Choose `secret_backend=1password` for the official SDK, without invoking `op`.
Set `onepassword_vault` and `onepassword_item` to exact IDs or unique exact names.
An environment `OP_SERVICE_ACCOUNT_TOKEN` authorizes a service account.
Otherwise, `onepassword_account` must select a desktop account. Keep both
service-account and Jira tokens out of configuration files and command arguments.

## Run

### Preview public settings

```sh
jira auth login --help
jira config get secret_backend --output=json
jira config set secret_backend 1password --dry-run --output=json
jira config set onepassword_vault 'Selected Vault' --dry-run --output=json
jira config set onepassword_item 'Jira Credential' --dry-run --output=json
```

Inspect previews, then save the selected coordinates without `--dry-run`.
For desktop authorization also save the selected `onepassword_account`.
Provide service-account authorization through the environment when applicable.

### Verify the token and save the account

Pipe the Jira token from a trusted source into `jira auth login --with-token`,
with `--site` and `--email` on first login. Login verifies it against Jira before
saving; `--dry-run` verifies without saving. Run `jira auth status` to verify the
resolved account and inspect `token_source`.

## Save

Settings store only public backend coordinates and preferences. The selected
store holds the selected profile's JSON credential record. For 1Password,
login creates or updates the concealed field with ID `jira-cli-credential`.
Titles and tags never establish ownership. Existing item metadata and unmanaged
fields remain intact. Only explicit login creates a missing selected item;
reads and logout never create it.

## Preconditions

### Check which operations open credentials

Local help, schemas, stored `config get`, `config set` and completion never
initialize the SDK or open credentials. `config list` resolves the selected
store to report effective identity and provenance; it can initialize the SDK.
Credential reads initialize lazily. Environment Jira
tokens override stored tokens. Stored tokens cannot follow a different site or
account; changing only the email drops the stored record. Explicit environment
email/token pairs retain precedence.

### Check 1Password platform requirements

Service accounts work in no-CGO builds. Linux/macOS desktop integration requires
a CGO-enabled build and the desktop application. Windows uses native loading.
Compilation alone does not establish native authorization acceptance.

## Recover

### The credential store failed or a write is uncertain

`auth_store_unavailable` does not prove credential absence. Check backend
selection, authorization and exact coordinates before replacing credentials.
Ambiguous names are refused. Select exact IDs. SDK error messages are sanitized.
A failed write attempt may have completed. Inspect the store before retrying.
Cancellation stops queued operations and refuses new writes, but cannot guarantee
interruption of upstream cold initialization or desktop IPC. A started write's
acknowledgment remains its actual outcome.

### Log out or inspect the remaining token source

`jira auth logout --yes` removes only the owned field in 1Password, retaining the
item and unmanaged content. Other clients using that field also lose it. Logout
does not revoke the Jira token or clear environment variables. `auth token`
deliberately prints the raw token and newline. Handle its output as a secret.
Legacy configuration migration and rollback remain a separate workflow.

## Next

Use `machine-output` to inspect failure frames and `safe-mutation` before writes.
