---
slug: configure-credentials
title: Select and verify credential storage
description: Configure the OS keyring, environment or direct 1Password SDK without persisting tokens in settings.
when_to_use: When choosing a credential backend or recovering from a credential-store failure.
commands: [jira config get, jira config set, jira config list, jira config profile, jira auth login, jira auth status, jira auth whoami, jira auth token, jira auth logout]
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
api_token_env = "JIRA_TOKEN_WORK"
read_only = true
timeout = "30s"
```

Theme, glyph, query-directory and alias settings belong at the document root.
An editor can be global or overridden by a profile. `config get` and `config set`
use the selected profile for account and workflow settings. Local profile
listing shows configured metadata without checking authentication.

### Use the OS keyring or environment

The default `secret_backend=keyring` stores a site/account/token record for the
selected profile in the OS keyring. Its public `credential_id` stays stable when
the profile is renamed. Login records a missing locator before saving the token,
so a later invocation can find a credential even if login is interrupted.
Credential record version 2 also binds the public cloud scope.
Existing version-1 records keep classic site routing. `keyring=false` disables that backend.
`secret_backend=env` opens no persistent store and refuses token saves.
Supply `JIRA_BASE_URL`, `JIRA_EMAIL`
and `JIRA_API_TOKEN` through your environment. Run `auth login` without
`--with-token` to verify the selected environment token and save public metadata.
Live environment login refuses stdin token input before reading it.

Use public `api_token_env` metadata for a specific `JIRA_TOKEN_` variable.
The suffix must contain uppercase letters, digits and underscores. An empty
reference keeps ordinary `JIRA_API_TOKEN` behavior. A direct `JIRA_API_TOKEN`
always takes priority. An effective named token uses the configured site/account
without opening unrelated stored credentials. Missing or empty named values keep
the selected-store fallback. Token values remain environment-only.

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
jira config set cloud_id fixture-cloud --dry-run --output=json
jira config set api_token_env JIRA_TOKEN_WORK --dry-run --output=json
jira config set default_project KEY --dry-run --output=json
jira config set default_issue_type Task --dry-run --output=json
jira config set default_board 'Team board' --dry-run --output=json
jira config set refresh_interval 45 --dry-run --output=json
jira config set team_account_ids '["team-1","team-2"]' --dry-run --output=json
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
saving. `--dry-run` verifies without saving or creating directories. Login also
publishes public routing, selected provider coordinates and four bound account-ID
fields for use by a fresh process. Conflicting authored/effective scope or
provider references prevent saving. Repair those declarations first. A
preview may verify proposed scope while reporting blocked settings. Run
`jira auth status` to verify the resolved account.
Inspect `token_source` and optional `cloud_id`.

Verification requires one complete native account object with exact member
names. Duplicate decoded members, including escaped duplicates, and case
variants refuse with `current_user_response_invalid` before login saves
credentials, whoami saves identity or `@me` authorizes an account write. Native
privacy-hidden values and absent or false activity remain valid for current-user
authentication. Verification waits for the response to end and honors the
configured timeout and cancellation.

### Select classic or scoped-token routing

Login first verifies classic site routing. Only a real 401/403 refusal can trigger
public `/_edge/tenant_info` discovery, followed by verification through the fixed
`https://api.atlassian.com/ex/jira/<cloudId>` gateway. Public discovery sends no
token or cookies and follows no redirects. The site remains the browser/cache
identity. Use `--cloud-id=YOUR_CLOUD_ID` to verify a known gateway directly, or
`--cloud-id=` to force classic routing without discovery. Token bytes never
establish token type. See Atlassian's [scoped-token routing](https://support.atlassian.com/atlassian-cloud/kb/401-unauthorized-error-when-service-account-accesses-jira-or-confluence-api/)
and [public cloud-ID lookup](https://support.atlassian.com/jira/kb/retrieve-my-atlassian-sites-cloud-id/).

If authored `cloud_id` differs from the verified scope, live login refuses before
saving and reports the verified ID. Set `cloud_id` to that scope, or clear it for
a classic token, then repeat login. `--dry-run` can verify independently without
changing configuration or credentials.

### Set client preferences

Client-preference metadata is available through `config get`, `config list` and
`config set`. `refresh_interval` is positive seconds, defaulting to 30. It does
not start CLI polling. `team_account_ids` retains literal IDs without adding an
issue filter. TOML uses a string array. Command arguments and `JIRA_TEAM_ACCOUNT_IDS` use one
JSON array. Order, duplicates, spaces and commas within IDs remain intact.

Use `[]` to clear the array. Stored reads distinguish absence from an explicitly
empty array. Environment values never become stored preferences.

### Cache an account ID for local previews

`account_id` is public cached metadata. Its independent `account_id_site`,
`account_id_email` and `account_id_cloud_id` binding must match effective
`base_url`, `email` and `cloud_id` before a local preview may use it. Migration
retains this binding from the selected old profile. Changing a public scope does
not rebind an existing cached ID. Bare previews emit `account_id_cached` to mark
its unverified source. Live runs and `--validate-remote` always read Jira instead.
An ID without complete matching site/account binding remains inactive. Ordinary
authentication inspection remains read-only. To populate the binding from Jira,
run `jira auth whoami --save --dry-run`, inspect the verified native user and
public scope, then run `jira auth whoami --save`. The preview verifies remotely
and creates no files or directories. Conflicting authored site/account/cloud
refuses before lookup. Repair those settings first. A missing selected settings
file can be created without replacing another file. Native privacy-hidden email
stays unchanged. The binding uses the credential account.

```sh
jira auth whoami --save --dry-run --output=json
```

### Check credential and settings outcomes separately

`data.credential` reports provider admission and acknowledgment separately from
`data.settings`. A verified candidate is not proof that both publications
succeeded. Environment credentials report `external` and their actual public
reference, never a persisted token. A credential can be saved even if settings
publication or output filtering later fails. Inspect both receipts before retrying.
No credential rollback or write replay happens automatically.

`data.settings` separates intent from publication. `saved=true` confirms success.
When present, `data.locator_publication` separately reports a new keyring locator.
It can remain saved after a later credential or identity-settings failure.
For `status=unknown`, inspect the file before retrying.
The `written` status records acknowledged creation whose durability failed. Snapshot conflicts refuse
publication, including a candidate that was initially unchanged. Root `jira me`
remains read-only. Saved IDs still serve only matching bare local previews.

## Save

Settings store only public backend coordinates and preferences. The selected
store holds the selected profile's JSON credential record. For 1Password,
login creates or updates the concealed field with ID `jira-cli-credential`.
Titles and tags never establish ownership. Existing item metadata and unmanaged
fields and the existing item category remain intact, including older Secure Notes.
Only explicit login creates a missing selected item, using the API Credentials
category, a Jira CLI section and a `jira-cli` tag. The concealed field contains
the complete JSON credential record. Separate site/email/token fields are not
assembled into a record. The section and tag organise new items and never
establish field ownership. Reads and logout never create items. Existing item
sections and tags stay intact. Private recovery backups remain Secure Notes.

## Preconditions

### Interactive and headless login

Interactive login validates form input and masks the token. Caller
cancellation or deadline refuses publication of the collected values. Linux and
Windows interrupt active accessible password reads and restore the borrowed
terminal modes. Windows accessible text and confirmation reads use the same
console cancellation handling. Other platforms check the context before and
after native password input. A failed read or restoration remains an error.
Use the explicit headless flags when no person can answer the form.

### Check which operations open credentials

Local help, schemas, stored `config get`, `config set` and completion never
initialize the SDK or open credentials. `config list` resolves the selected
store to report effective identity and provenance. It can initialize the SDK
when no effective named token supplies credentials.
Credential reads initialize lazily. Environment Jira
tokens override stored tokens. Stored tokens cannot follow a different site or
account/cloud scope. Changing only the email or explicitly changing/clearing
`cloud_id` can make the stored record inapplicable. The CLI skips that record,
warns, and resolves the remaining configuration sources. It does not delete the
record. Omitted scope inherits the bound record.
An explicit environment token uses authored routing when supplied. Explicit environment
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
does not revoke the Jira token or clear environment variables. `remaining_source`
names only a layer still supplying a nonempty token. `env_token_remains` also
covers an environment credential exposed by removing the stored record. An empty
or default layer does not mean authentication remains. `auth token`
deliberately prints the raw token and newline. Handle its output as a secret.
Use `migrate-configuration` to retain legacy profiles and explicitly roll back.

## Next

Use `machine-output` to inspect failure frames and `safe-mutation` before writes.
