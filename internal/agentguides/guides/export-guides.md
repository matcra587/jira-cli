---
slug: export-guides
title: Install the CLI's guides as agent skills
description: Export the installed CLI's runbooks as portable or Claude skills with inspectable file receipts.
when_to_use: When configuring an agent harness to discover and execute Jira CLI workflows.
commands: [jira agent export]
order: 50
---

## Decide

Use `agent-skill` for the portable Agent Skills format. Use `claude-skill`
for Claude's native `when_to_use` field. Both export the same embedded runbooks.
Exported skill names and directories use the `jira-` prefix. Use the original
guide slugs when retrieving guides from the CLI.

Start with a preview. Export needs no Jira credentials, network requests or
interactive prompts. Jira read-only mode permits these local file writes.

## Run

```sh
jira agent export --format agent-skill --dir skills --dry-run --output=json
jira agent export --scope project --harness codex --dry-run --output=json
jira agent export --scope user --harness claude-code --dry-run --output=json
```

### Choose the destination

Explicit `--dir` requires `--format`. `--scope project` uses the working directory.
`--scope user` uses the home directory. Codex uses `.agents/skills`.
Claude Code uses `.claude/skills`. A scoped export defaults to the selected
harness's format. An explicit `--format` overrides that default.
Without `--harness`, supported environment markers select the harness.
If detection cannot identify one, choose it explicitly.

### Inspect the plan before writing

Dry-run renders the plan without checking existing destination files or changing
the filesystem. Check the destination requirements in Preconditions.
Inspect the preview, then omit `--dry-run` to write the artifacts.

## Save

Retain `directory`, `format`, `scope`, `harness`, `artifacts`, `written_paths`
and any `attempted_path`. Each artifact identifies its source slug, relative
path, exact SHA-256 digest, byte count and publication state. The complete plan
remains available when the command writes only some of the planned files.
Files use mode `0600`. New directories use `0750`, subject to the process umask.

## Preconditions

Writing needs write access to the selected destination and a filesystem
that supports exclusive file publication. Existing artifacts require `--force`.
New exports need no acknowledgement flag. Directory parents inside the export
root must stay inside that root. The command refuses symlinks that escape it.

## Recover

Inspect the receipt after a failure, cancellation or output error. Published
files remain in place. The command does not roll them back or replay writes.
`--force` permits replacement of the planned artifact entries, including user
edits, without following their file symlinks or changing hard-link targets.
Choose another directory when those edits must be retained. A dry-run does not
prove that the destination is writable, that names are unused or that writing
will succeed.

## Next

Read `discover-cli` for runtime schemas, and `machine-output` before filtering
an export receipt. Update generated skills by exporting from the installed CLI.
The embedded runbooks remain their source.
