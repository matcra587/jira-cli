---
slug: update-cli
title: Inspect and update the installed CLI
description: Resolve the installation owner, preview release availability and retain update outcomes.
when_to_use: When updating jira-cli itself or recovering from a failed executable update.
commands: [jira update, jira version]
order: 75
---

## Decide

Use `update` (alias `up`) for jira-cli itself. It needs no Jira credentials.
Installation ownership determines what can change. Published archives replace
the running executable's resolved path. Homebrew upgrades the verified
`matcra587/tap/jira` formula. Scoop, mise and `go install` installations return
their own update command without checking releases or running the installer.
Unknown installations refuse an automatic update.

### Understand passive update hints

Ordinary Jira commands can show a cached update hint after human terminal output.
The anonymous release refresh runs in the background with a two-second bound.
It does not delay command startup. Refreshes and printed hints have separate
24-hour cooldowns. Non-terminal runs may refresh the cache but print no hint.
Machine output prints no hint even with terminal stderr. Agent sessions,
`--no-input`, previews, updates and local discovery skip the check entirely.
Set `JIRA_NO_UPDATE_CHECK=1` to disable all passive checks. Explicit `update`
still works. A first run can be silent while the cache refreshes.

## Run

### Preview availability

```sh
jira update --help
jira agent schema --path update --output=json
jira update --dry-run --output=json
```

`--dry-run` inspects availability without downloading or installing. For archive
and Homebrew installations, it reads the public release service. This differs
from a Jira mutation's local preview. Neither workflow sends a Jira write.
Managed installations return their local hint even in a preview.

### Authorize installation

An explicit terminal invocation authorizes an update without another prompt.
Pipes, agent sessions and `--no-input` require `--force` on live archive or
Homebrew updates. `--yes` is the hidden compatibility alias. These flags do not
bypass installation ownership, checksums or an intervening executable change.

The following command authorizes a live update:

```sh
jira up --force --output=json
```

Then verify the executable that was updated. For Homebrew, use
`brew.after.executable` when present in the receipt; otherwise inspect the
formula's installed executable. Another copy can still win on PATH. For archive
installations, use the resolved path of the executable that ran the update.
Run the following only when `jira` resolves to that installed executable:

```sh
jira version --output=json
```

## Save

### Was a newer release found?

Retain the full JSON receipt, especially on failure. `update_available` is
present only after a successful release comparison. Missing availability does
not mean the installation is current. `latest` is the discovered published
release, not an independently observed installed version.

### Did the installation change?

`attempted` records an executable publication or started Homebrew upgrade.
Homebrew's earlier metadata refresh is separate. Archive `publication` retains
`published`, `durable`, `uncertain`, `bytes`, `sha256` and any recovery `backup`.
`updated` follows observed archive publication or changed Homebrew executable
bytes. `brew.before` and optional `brew.after` retain independently inspected
paths, versions, digests and HEAD status. An absent after observation leaves
`uncertain=true`. Do not treat that as proof that the installation stayed unchanged.

## Preconditions

### Archive installations

Archive releases require a matching platform asset and checksum manifest.
Downloads are anonymous and use the IDs observed during release discovery.
The installer does not resolve latest again. Ownership and the original target
are checked before publication. These snapshot checks do not create an atomic
lock against unrelated filesystem writers.

### Homebrew installations

Homebrew ownership requires the tap-qualified installed formula, its active keg
and a listed executable matching the running path. HEAD kegs keep their HEAD
upgrade channel. Old kegs and shadowing PATH copies remain intact. When
`brew.shadow` is present, that preserved copy still wins on PATH. Invoke or
select the formula's executable before concluding that normal `jira` runs use
the new installation.

## Recover

### Installation stopped or its outcome is uncertain

Failures retain typed data in full JSON and compact error frames. Cancellation,
timeout, a subprocess failure or an unconfirmed directory sync can accompany an
already published executable. Inspect the receipt and run `version` from the
observed installed path before retrying. A retained backup is a recovery
artifact. Preserve it until you establish the installation's state.

### The update was refused

`update_integrity` rejects the downloaded bytes. `update_ownership` refuses the
Homebrew target. `update_changed` reports an intervening replacement.
`update_unavailable` means no published release was found. Other update failures
include their original cause. No failed update is automatically replayed.

## Next

Verify the executable that PATH resolves and its version. Then return to the
original Jira workflow. Updating the tool changes no Jira issue.
