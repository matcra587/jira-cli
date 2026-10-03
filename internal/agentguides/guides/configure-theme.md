---
slug: configure-theme
title: Select built-in and custom terminal themes
description: Inspect stored theme settings and validate a local palette before publication.
when_to_use: When changing human terminal colors or recovering from an unavailable custom palette.
commands: [jira config theme, jira config get, jira config set, jira config list]
order: 70
---

## Decide

Use `config theme` to inspect the stored preset and custom palette path. This
reads file declarations only. Environment values and defaults never become
stored values. `config list` shows effective values and their sources.
These commands change no Jira resource. Theme inspection and selection require
no credentials; explicit selections work in agent sessions and with `--no-input`.

`--name` selects a bundled clib preset. Selecting a name clears an existing
custom palette unless `--path` is also supplied. An empty name restores fallback
selection. `--path` validates a custom TOML file and stores an absolute path;
an empty path disables the custom palette. `JIRA_THEME` overrides the custom
file. Without an explicit theme, `COLORFGBG` supplies a background hint when
available. Otherwise the fixed dark preset is used. No terminal query runs.

## Run

Preview the selected preset or palette first. For a custom palette, create the
TOML file before running its example. Preconditions explains how paths resolve.

```sh
jira config theme --help
jira agent schema --path 'config theme' --output=json
jira config theme --name dracula --dry-run --output=json
jira config theme --path ./palette.toml --dry-run --output=json
jira config theme --path '' --dry-run --output=json
```

### Prepare a custom palette

Create `palette.toml` before the custom-file example. A minimal palette is:

```toml
base = "light"
entity_colors = ["#2459B3", "#7047B5", "#006D75"]

[colors]
help_command = "#2459B3"
help_section = "#7047B5"
red = "#A11D33"
green = "#256D1B"
markdown_code = "#006D75"

[syntax]
key = "#2459B3"
string = "#256D1B"
```

`base` inherits a bundled preset; omission selects dark. `[colors]` overrides
named clib semantic, help and Markdown styles while retaining their other
attributes. `[syntax]` independently overrides clog syntax colors. All colors
must be six-digit `#RRGGBB` strings. An omitted entity palette inherits the base.
An explicitly empty palette is refused. Entity colors are filtered against
semantic colors after terminal color conversion. If no colors remain, entities
render as plain text. Unknown keys, roles and wrong types are
refused. Files must be regular, valid UTF-8 and at most 64 KiB.

### Choose supported color roles

The `[colors]` roles are `blue`, `bold`, `bold_green`, `dim`, `green`,
`magenta`, `orange`, `red`, `yellow`, `help_alias`, `help_arg`,
`help_arg_optional`, `help_bold_dim`, `help_command`, `help_desc_backtick`,
`help_desc_list`, `help_desc_numbered_list`, `help_desc_unordered_list`,
`help_dim`, `help_enum_default`, `help_flag`, `help_flag_backtick`,
`help_flag_default`, `help_flag_example`, `help_flag_note`,
`help_key_value_separator`, `help_repeat_ellipsis`, `help_section`,
`help_subcommand`, `help_value_placeholder`, `markdown_code` and `markdown_text`.
The `[syntax]` roles are `accent`, `bool_false`, `bool_true`, `comment`,
`foreground`, `key`, `number`, `secondary` and `string`.

## Save

Inspect the preview receipt, then repeat the chosen selection without
`--dry-run`. Changes take effect on the next invocation. `name_present` and
`path_present` distinguish absent declarations from explicitly stored empty
values. `would_change` describes the candidate. `changed` and `created` describe
actual publication. Name/path changes use one save, preserving unrelated stored
settings and aliases. Unchanged typed values retain authored bytes and metadata.

## Preconditions

The selected settings file must have valid syntax and declared types. Selection
validates the same custom palette during preview and publication. Relative
paths authored directly as `theme_path` resolve beside the selected settings
file, independently of the working directory. `config set theme_path VALUE`
stores a scalar path; use `config theme --path VALUE` to validate its palette.

## Recover

An unavailable or invalid custom palette raises `theme_invalid` through the
output contract and retains the preset fallback. Other commands remain usable.
Select a bundled preset or clear the custom path to repair the setting.
Unknown presets fail with `flag_value_invalid` and may suggest a valid name.
After `config_changed`, inspect the current settings and prepare the change again.
A storage error can follow file replacement. Run `config theme` with the same
`--config` selection to read the stored values before retrying. If saving the
settings fails, the result omits the theme publication fields. Missing `changed`
or `created` does not prove that the file stayed unchanged. Snapshot checks do
not lock out an unrelated writer.

## Next

Inspect `config theme`, then run a human command with the selected palette.
`--color=never` suppresses styles and hyperlinks even when a palette is set.
Machine output remains plain data. Custom palettes do not change machine values.
