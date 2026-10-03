---
description: >
  Layered configuration: the koanf loader, precedence, the profile model
  and secret backends, JIRA_* env vars, theme resolution, and the rules for
  changing the config shape.
paths:
  - "internal/config/**/*.go"
  - "internal/cli/config/**/*.go"
  - "internal/cli/auth/**/*.go"
---

# Config

Named documents use `format_version=1`, `default_profile` and `[profiles.NAME]`
tables. Existing flat files remain readable. One selected profile resolves to
the flat runtime `Config`. `internal/config/load.go` owns koanf resolution;
`internal/config/profile_document.go` owns strict document admission and selection,
and `internal/config/file.go` owns authored file snapshots.

Select through `--profile`/`-P`, then `JIRA_PROFILE`, then the authored default.
Unknown or explicitly empty selectors refuse; reads never manufacture profiles.
Validate all authored tables before overlays. Keep absence, empty, false and zero
distinct. Ordinary environment precedence and transport read-only enforcement
remain unchanged. Public inspection distinguishes selected profile from effective
identity; stored metadata must not replace a named profile's identity before the
binding check.

## Precedence (last wins)

`default < file < keyring < env < flag` is the order in `config.Layers`.

*   `Load` never writes. File updates preserve authored values without
    persisting the effective environment or credential layer.
*   Decode is **strict** (`ErrorUnused: true`): unknown keys are rejected,
    not ignored. A key rename without migration breaks existing files —
    see "Changing the config shape".

## File

`DefaultPath` uses `x/shell.ConfigDir("jira", "config.toml")`. `--config`
selects a file independently of profile selection. Use the configuration owner
for reads and publication; do not hardcode paths in commands.

## Profiles and credentials

`[profiles.NAME]` holds public account and workflow settings. Theme, glyph,
query-directory and alias settings belong at the root. An editor can be global
or overridden by a profile. Tokens never belong in named profile tables.

The OS keyring stores the site, account and token together. A profile selects its
own stable `credential_id`; `default` selects the existing fixed slot. A named
profile without a locator never falls back to that slot. Give an authored
profile a unique UUID before saving credentials with `auth login`.

`JIRA_API_TOKEN` can supply an environment token. Implicit profile-derived token
variables, 1Password storage and provider-to-provider `auth migrate` are not
implemented here. Legacy conversion and rollback require a separate owner.

## Env vars (prefix `JIRA_`)

| Var | Effect |
|-----|--------|
| `JIRA_PROFILE` | selects an existing profile unless `--profile` is set |
| `JIRA_THEME` (+ `_LIGHT`/`_DARK` via clib's `SetEnvPrefix("JIRA")`) | theme override for help, plain output, and the TUI |
| `JIRA_READ_ONLY` | truthy set (`1/true/yes/on`) blocks mutations; wins on the OFF→ON direction only |
| `JIRA_ADF_STRICT` | ADF mode override (flag > env > path default) |
| `JIRA_MAX_RETRY_WAIT` | rate-limit retry budget (Go duration); unparseable → default, never silently disabled |
| `JIRA_LIVETEST_PROJECT` | live-suite target project (a dedicated probe project) |
| `JIRA_NO_UPDATE_CHECK` | non-empty disables the passive update check (handled inside clive/notify; the name derives from the binary name) |
| `NO_COLOR` | presence disables color and hyperlinks (per no-color.org, unprefixed); `--color=always` overrides. There is no JIRA_NO_COLOR — the JIRA env prefix covers clog/clib theme/level/hyperlink vars, not this |

Agent detection (`CLAUDECODE`, `AI_AGENT`, …) lives in
`internal/cli/detector.go` and selects compact output — it is not config.

## Theme resolution (`internal/config/theme.go`)

*   Selectable names = `"auto"` + clib's presets (`clibtheme.Names()`), no
    jira-cli aliasing. `JIRA_THEME` wins over the config key on every path.
*   `"auto"` (background detection) is **opt-in, never the default** —
    detection misfires silently under tmux/SSH/container exec, and a wrong
    guess is worse than the fixed dark palette. Keep that comment-documented
    rationale intact if you touch it.
*   Validation is write-time only (`config theme --name`); config **load**
    deliberately does not validate, so a theme clib later renames degrades
    to the dark fallback at render instead of blocking every command.

## Changing the config shape

*   A purely additive key (new, optional, defaulted) lands with: the loader
    default, the TOML docs, and the schema surface together — one PR.
*   A breaking change (rename, retype, remove) must keep strict decode from
    rejecting existing files: provide an upgrade path (migration or
    load-time tolerance) and tests proving an old file still loads. There
    is no legacy conversion command yet; named documents use explicit
    `format_version=1`.
*   A profile `editor` overrides the global `editor`. Keep new per-profile
    settings on the same pattern.

## Command boundaries

Configuration reads and credential resolution use the shared configuration
owner through root dependencies. Do not reconstruct the removed `loader.go`,
`LoadOrInit` or command-local profile helpers.
