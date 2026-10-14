---
slug: migrate-configuration
title: Convert and recover a legacy configuration
description: Convert legacy or flat settings to named profiles with retained configuration and credential preimages.
when_to_use: When retaining legacy profiles in the current format or recovering an interrupted conversion.
commands: [jira config migrate, jira config rollback, jira config get, jira auth status]
order: 67
---

## Decide

Select an explicit source file. Conversion retains all legacy profiles and their
authored default. Use `--source-profile NAME` only when you want a subset.
A flat source becomes a named `default` profile and retains its existing
credential reference. Global `--profile` selects runtime settings; it does not
select migration input. Keep the entire original source in protected recovery.

### Check whether the source can be converted

Preview first. `data.unmapped` lists declarations whose runtime mappings remain
unfinished. `data.ready=false` blocks live conversion. For CLI-only conversion,
explicit `--defer-tui` retains recognized global TUI settings in exact source
recovery bytes. `data.deferred` lists their declaration names without values.
Those declarations do not become active settings. Unknown declarations and
invalid declaration types or structures still block conversion. Without this
option, TUI declarations remain unmapped.

Legacy theme paths retain literal ~ and $ characters.
Relative names resolve against the explicit legacy source directory.
Historical `JIRA_TOKEN_<profile>` names
become explicit `api_token_env` references for every selected backend.
Previews never inspect credentials, create directories or contact Jira.

## Run

The live conversion example needs an unused recovery directory and `--yes` in
headless mode. Read Preconditions before conversion or rollback.

```sh
jira config migrate --source=old.toml --defer-tui --config=settings.toml --dry-run --output=json
jira config migrate --source=old.toml --defer-tui --config=settings.toml --recovery-dir=recovery --yes --no-input --output=json
jira config rollback --journal=recovery/migration.json --dry-run --output=json
jira config rollback --journal=recovery/migration.json --yes --no-input --output=json
```

Provide an unused explicit recovery directory for live conversion. Supply
`--legacy-keyring-service` when the old installation used a custom
`JIRA_KEYRING_SERVICE`. The default is `jira-cli`. Environment token values are
never copied into configuration or recovery. Live headless operations require
`--yes`. `--no-input` explicitly disables prompts but does not authorize the
operation. A preview needs neither confirmation nor a credential provider.

## Save

### Keep the recovery files and provider backups

Retain `configuration.json`, `migration.json` and provider-owned UUID backups.
The configuration artifact preserves the exact original and expected bytes. The journal
contains public identities, hashes and effect facts. Credential values, original
owned SDK field metadata and random integrity keys stay inside their provider.
Environment-backed profiles need only configuration recovery and the journal.
For each profile, `receipt.profiles[].credential_state=external` means its token
was never inspected or backed up. That profile opens no provider, and rollback
never restores environment variables. Other profiles in the same conversion may
use provider backups. Retain externally managed tokens separately.

### Understand the recorded mapping policy

Current conversion retains every selected profile in a versioned named document.
Each credential has its own recorded source, destination and effects. Keyring
destinations use stable public locators; old source entries stay intact.
Configuration publishes after the required credential steps. A failure leaves
recovery available for explicit rollback. Physical provider-location collisions
refuse before writes. Older journals below still use their original mappings.

Existing version-1 native recovery
records and version-2 environment recovery remain readable and use their
original mapping when rolling back.

Policy 3 adds `cloud_id`: the selected
profile's validated scope, or an explicit empty classic scope. Credential record
version 2 binds that scope with the site, account and token. Version-1 records
remain classic and cannot invent scoped routing.

Policy 4 adds the selected
profile's `default_project`, `default_issue_type` and `default_board`, including
explicit empty defaults. Policies 1–3 retain their original candidate derivation.
Reopening an older journal does not add working defaults retroactively.

Policy 5
retains the selected `theme.name=auto` as a flat selector using the v0.16 passive
background hint and dark fallback. Policies 1–4 keep their original derivation
and automatic-theme refusal. Ordinary conversion still refuses TUI declarations.

Policy 6 retains selected refresh metadata with its historical nonpositive-to-30-second fallback,
and retains authored team ID arrays without changing IDs or declaration presence.
Policies 1–5 retain their original derivation/refusals. Client preferences do not
activate polling or add a team filter. Global TUI refresh remains separate.

Policy 7 retains an authored selected `account_id` with independent
`account_id_site`, `account_id_email` and `account_id_cloud_id` binding. Policies
1–6 retain their original candidate derivation and account-ID refusal. A matching
cached ID can serve local previews with an unverified warning. Live runs always
read the actual current account. Migration does not authenticate that metadata.

Policy 8 preserves all literal legacy theme paths, including `~`, `$NAME` and
`${NAME}`. It resolves relative names against the legacy source directory without
reading a palette, home directory or environment value during preparation.
Versions 1–7 retain their original candidate derivation and special-path refusal.

Explicit `--defer-tui` uses policy 9, retaining policy-8 CLI mappings while
recording the choice to defer TUI state. Policies 1–8 keep their original TUI
refusal. Reopened recovery derives its candidate by the recorded policy.

### Verify the converted account

Keyring conversion retains historical source slots and backs up each selected
destination. SDK conversion uses the owned field and preserves unmanaged item
content. Missing source credentials never overwrite a destination. Conversion
preserves configured site/account/cloud binding but does not authenticate against Jira.
Use `jira auth status --profile NAME --output=json` afterward for each converted
profile. Verifying the default alone does not verify the others.

## Preconditions

### Check the source and recovery state

Help, schemas and completion remain local and credential-free. `--source-profile` completion
reads only explicit `--source` and offers validated legacy names. Source, target,
journal and immutable artifact snapshots must remain unchanged when their checks
run. Provider backups and expected states must match the integrity checks recorded
for them.

### Check the rollback destination

Rollback uses recorded paths and concrete provider identities. Global `--config`
and current environment settings do not retarget it. Its preview verifies local
configuration only. `credential_inspection_required=true` means a real rollback
must still open the provider and compare its current contents with the recorded
migration result or original state. Changed contents block restoration; an
already-restored state needs no credential write.
On Windows, directory junction targets are resolved before configuration
snapshots are taken. A changed selected junction blocks publication. Rollback
uses its recorded canonical destination. Missing descendant directories are
created only by live publication, never by a preview.

These checks do not supply filesystem or remote compare-and-swap.

## Recover

### Conversion stopped after a possible write

An error can follow a completed write. `receipt.effects` distinguishes pending
intent, acknowledged operations, refused entry and uncertain outcomes.
`journal_durable=false` means the latest in-memory receipt could not be saved.
Reopen the journal before explicit recovery. Cancellation never launches detached
cleanup or automatic apply retries.

### Roll back from the retained journal

Never replay an interrupted apply. Run explicit rollback with the retained
journal and a fresh invocation. It checks all resources before its first write,
restores credentials before exact configuration bytes or prior absence, and
retains all recovery resources. Changed state refuses restoration. Already
restored state is a no-op. You can explicitly retry an interrupted rollback.

### Keep Windows credential backups intact

On Windows, keyring recovery can retain old and new credentials that each fit
Credential Manager independently, even when their combined backup exceeds one
entry. Keep provider recovery entries intact. A failed preparation may retain
partial private backup entries while leaving the primary credential unchanged.
Inspect the migration receipt before retrying.

## Next

Use `configure-credentials` for backend authorization and `machine-output` for
retained failure frames. Unmapped preferences and native platform acceptance
remain separate migration requirements.
