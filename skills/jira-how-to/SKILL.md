---
name: jira-how-to
description: Guides Jira CLI tasks using the installed workflow guides and command schema. Use when reading or changing Jira data, configuring jira-cli, or choosing commands and flags.
allowed-tools: Bash(jira agent guide *), Bash(jira agent schema *), Bash(jira guide *)
license: MIT
metadata:
  origin: jira-cli
---

# jira how-to

Use the installed jira-cli's embedded guides for workflow instructions and its
runtime schema for exact commands, flags and output shapes. Reuse guides and
schemas already loaded for the current CLI version. Refresh them after an
upgrade or a lookup failure.

## Discover the workflow

If not already loaded, read the current guide index:

```sh
jira agent guide --output=json
```

Choose guides using their descriptions, `when_to_use` and command coverage.
Retrieve each relevant guide by its returned slug:

```sh
jira agent guide SLUG --output=json
```

Read the output-contract guide before parsing receipts, and the mutation guide
before changing Jira. Load domain guides needed for the user's task. Guide
names come from the installed index; do not substitute names remembered from
another release. An unknown slug means refresh the index and choose again.

`jira guide SLUG` exposes the same source through the human-facing door.
Use `jira agent guide SLUG --section SECTION` for a specific part of an already
read guide, or `--all` when the complete corpus is needed.

## Inspect the exact command

Use the command path identified by the guide to request only that subtree
before constructing its arguments or parsing its data:

```sh
jira agent schema --path 'issue create' --output=json
```

If the command path is still unknown, use `jira agent schema --output=json`
to find it in the full tree, then request the selected subtree.

Read declared arguments, flags, examples, operation classes and output shapes.
`--path` includes the selected subtree's shapes; `--shapes` includes shapes for
the entire tree. Those flags are mutually exclusive. Resolve local schema
references against the extracted schema object. Help and discovery need no Jira
credentials and make no Jira requests.

## Follow the selected guide

Use the guide's preview, confirmation, verification and recovery instructions.
A dry run does not prove server acceptance; some explicitly documented previews
perform remote reads. `--no-input` disables prompts and does not authorize a
write. Use `--force` or `--yes` only where the selected command declares it and
the requested action is authorized. Read-only policy still blocks Jira writes.

Read the documented success and failure shapes before filtering stdout.
Compact failures retain a full error frame. Partial batch success requires
inspecting every result; an exit code of zero alone does not prove all targets
succeeded. Inspect uncertain write outcomes before retrying, and never replace
a failed domain write with an automatic raw API mutation.

## Export guides when needed

`jira agent export --format agent-skill --dir skills --dry-run --output=json`
previews portable skills from the same embedded corpus. Read the export guide
from the index for destination, overwrite and publication rules before writing.
Update exported skills from the installed CLI when its guides change. Keep
workflow details in those guides rather than copying them into this router.
