---
description: >
  Configuration owners, precedence and provenance, file-only updates,
  credential separation, theme and editor selection, and migration boundaries.
paths:
  - "internal/config/**/*.go"
  - "internal/cli/config/**/*.go"
  - "internal/cli/auth/**/*.go"
  - "internal/cli/root/**/*.go"
  - "internal/cli/theme.go"
---

# Config

Named documents use `format_version=1`, `default_profile` and `[profiles.NAME]`
tables. Existing flat files remain readable. One selected profile resolves to
the flat runtime `Config`. `internal/config/load.go` owns koanf resolution;
`internal/config/profile_document.go` owns strict document admission and selection,
and `internal/config/file.go` owns authored file snapshots.
Credentials use the separate `internal/credentials` owner and the root's lazy
credential-store constructor. Do not recreate the old `loader.go`, profile
helpers or `LoadOrInit` API: they are absent from this architecture.

Select through `--profile`/`-P`, then `JIRA_PROFILE`, then the authored default.
Unknown or explicitly empty selectors refuse; reads never manufacture profiles.
Validate all authored tables before overlays. Keep absence, empty, false and zero
distinct. Ordinary environment precedence and transport read-only enforcement
remain unchanged. Public inspection distinguishes selected profile from effective
identity; stored metadata must not replace a named profile's identity before the
binding check.

Profile mutation uses the existing file publication owner, preserves other tables,
and opens no providers. Renames keep credential locations; removal retains secrets.
Keyring `credential_id` is a stable public UUID; `default` retains the old fixed
slot when adding a profile to flat settings. A missing named locator never reads
that slot. Profile creation assigns its locator before login. An authored profile
without a locator must receive a unique UUID before a keyring login can save.

1Password and legacy migration/rollback remain separate delivery requirements.

## Resolution and provenance

`default < file < keyring < env < flag` is the order defined by `config.Layers`.

*   `Load` reads without writing. A missing default file is normal; an absent
    file selected through `--config` is an error for settings reads.
*   `Options.Path` overrides the global `--config` selection, which overrides
    `DefaultPath`. The path selects the file; it is not a setting value.
*   A supplied credential store contributes its complete site, account and
    token tuple. If the effective site differs from that stored site after
    normalization, resolution removes the entire keyring layer and warns.
    Never send a stored token to a site selected from another layer.
*   `JIRA_*` overlays supported keys only. Empty values are unset; unknown
    variables and `JIRA_ALIASES` are ignored. File aliases remain literal keys.
*   `--timeout` supplies the flag layer. Preserve its explicit-set bit:
    `--timeout=0` disables the deadline, while omission retains lower layers.
*   Decode the file independently before merging so environment values cannot
    hide unknown keys or invalid declared types. TOML decoding is strict;
    the merged environment permits the required string-to-type conversions.
*   `Config.Validate` requires complete account settings only where a Jira
    client needs them. Keep local discovery and preference operations usable
    without credentials. This does not waive decode or save-time validation.

`config list` reports effective values, their winning sources, the selected
profile and path. Its credential constructor respects `keyring=false`. `config get KEY`
reads only the authored file, preserving absent versus explicit empty, false
and zero. Both use the configuration owner's redaction, including credential
userinfo in URLs; neither prints a token.

## File selection and publication

`DefaultPath` uses the pinned `x/shell.ConfigDir("jira", "config.toml")`.
An absolute `XDG_CONFIG_HOME` overrides the platform fallback. The fallback is
`~/.config/jira/config.toml` on Unix and `%AppData%\jira\config.toml` on Windows.
Use the owner; never hardcode those paths in command handlers.

*   `ReadFile` refuses an absent explicit path. `OpenFile` permits a missing
    update target and follows existing config links, refusing dangling links.
    Layered loads, snapshots and save re-reads validate an opened regular-file
    descriptor before reading bytes. Unix opens are nonblocking so FIFO inputs
    refuse without waiting for a writer. This does not impose a file-size limit
    or interrupt arbitrary regular-file filesystem I/O.
*   `File.Save` validates the candidate before atomic replacement through the
    pinned `x/os.AtomicWrite`. Changed files have mode `0600`; newly created
    directories are private and existing parent permissions are retained.
    Unchanged bytes leave file metadata untouched.
*   Save checks the original bytes, existence and resolved link target.
    A detected conflict is `ErrFileChanged`. These checks are not a filesystem
    compare-and-swap or a lock against arbitrary external writers.
*   `OpenNewFile` and `File.Create` own initialization. They refuse existing
    names, directories and dangling links without reading their contents.
    Creation stages a private sibling through `AtomicWrite`, then publishes
    through atomic no-clobber `os.Link`. Unsupported filesystems fail; never
    replace that boundary with a clobbering rename or a partial direct write.
*   A creation directory-sync error can occur after publication. Preserve
    `created=true` in the typed error receipt; inspect the destination before
    retrying. Do not infer rollback from a storage error.

## Local commands

`config profile` lists, adds, renames, selects the default and removes named
profiles through the file publication owner. These operations open no providers.

Build commands through `command.Spec` and the shared typed output/View path.
Mark local mutations with `MutatesLocalState`; do not invent a Jira transport
gate or credential dependency for file-only work.

*   `config init` requires explicit `--base-url` and `--email` in every mode.
    Validate both before file I/O. It never prompts, reads credentials or
    contacts Jira. Preview creates no files or directories and persists no
    defaults or environment values; real creation uses the shared owner.
*   `config set` prepares scalar settings through `PrepareSetting` or
    `PrepareSettings`. Credentials are refused and aliases require their
    command-tree validation owner. Preserve other declared values without
    writing the effective merged configuration. Changed saves normalize TOML;
    unchanged typed values preserve authored bytes and comments.
*   `config edit` edits a copy and validates before saving. Editor precedence
    is `JIRA_EDITOR`, the stored `editor`, `VISUAL`, then `EDITOR`, with no
    guessed fallback. An explicit `JIRA_EDITOR` permits repairing invalid TOML.
    Split executable arguments with the pinned shell helper; run without a
    shell. Real edits require a terminal; preview launches nothing.
    Rejected edits retain a recovery-copy receipt. Terminal supervision belongs
    to `command.RunEditor`, including cancellation and mode restoration.
    Read the editor's saved scratch bytes through the raw file snapshot owner.
    Capture regular saved work after cancellation for recovery, then refuse
    publication through the original context. Nonregular scratch input is
    refused; editor, context and native read causes remain inspectable.

Credentials belong to `auth login`, the credential-store adapter and the shared
client construction path. Never add a setting command that persists a token.
The current backend is the OS keyring; environment credentials may override it.
Do not advertise 1Password or provider-to-provider `auth migrate` as implemented.

## Saved queries

`LoadQueries` reads literal `.jql` filename stems in filename order through the
same opened-descriptor reader as settings files. Regular symlinks are supported;
nonregular targets are refused before reading. Unix FIFO refusal does not wait
for a writer. A pathname replacement after opening leaves the read on its
original regular descriptor; this is not a filesystem compare-and-swap.
Preserve native read causes, nil results on failure, metadata and authored JQL.
Check cancellation before reads and before returning; no credentials or Jira
client are needed for local query discovery or `--as-jql`.

## Theme selection

`internal/cli/theme.go` owns theme resolution; `config theme` uses the shared
setting and custom-theme owners. Follow the `configure-theme` embedded guide.

*   Theme environment precedence is `JIRA_THEME`, `CLIB_THEME`, `CLOG_THEME`,
    then `THEME`. A usable environment preset precedes the file preset.
    Library-only names can be consumed at startup; do not promise perfect
    settings provenance for a variable intentionally removed before loading.
*   Custom `theme_path` resolves relative to the selected configuration file.
    Invalid custom palettes warn and retain the preset fallback.
*   Without a selected theme, use `COLORFGBG` and then the dark fallback.
    Do not introduce an active terminal-background probe during startup.
*   Invalid theme names warn at rendering; invalid glyph vocabulary fails
    decoding. Preserve that intentional distinction.

## Changing the contract

Update supported keys, defaults, validators, provenance, typed outputs,
help/schema/completion and executable guide examples together. Preserve strict
file decoding and provide a verified upgrade path for breaking changes.
The embedded guides and runtime schema describe current user workflows;
these rules identify their implementation owners and invariants.
