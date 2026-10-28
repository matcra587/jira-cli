---
slug: dashboard
title: Browse issues in the terminal dashboard
description: Browse saved views and issue details, switch issues and review contextual Jira actions in a persistent terminal view.
when_to_use: When a person wants to browse Jira interactively in a terminal.
commands: [jira tui, jira issue list, jira issue view]
order: 36
---

## Decide

Use `tui` for an interactive issue home screen. Use `issue list` and `issue view`
for scripts, agents, machine output or one-shot reads. Bare `jira` still opens
help or discovery. The dashboard browses Jira and can transition, assign or comment on explicit
issues after review. `JIRA_READ_ONLY=true` disables these actions and retains the
shared transport's write refusal. Local settings and saved searches remain available.

### Before starting

Configure the selected profile and authenticate using the ordinary CLI commands.
Input, output and the prompt stream must be terminals. Agent sessions,
`--no-input`, JSON/compact output and jq filtering refuse before constructing
Jira dependencies. Help and schema discovery remain local and credential-free.

## Run

```sh
jira tui --help
jira agent schema --path=tui --output=json
```

From an interactive terminal, run `jira tui`. To start with an explicit query,
run `jira tui --jql='project = DEMO ORDER BY updated DESC'`. Select a configured
profile with `jira --profile=work tui`.

My work shows issues assigned to the authenticated account outside Done. Project
appears when `default_project` is configured and shows its unfinished issues.
These views use explicit queries; the profile's default board does not narrow
them. Search retains the custom JQL entered during the session.

*   Tab and Shift+Tab switch views.
*   `/` opens the JQL editor. Enter runs it; Esc keeps the previous query.
*   Ctrl+P or Ctrl+K opens a searchable command picker. Enter runs the selected
    action through its ordinary keyboard path; Esc returns to the page.

The command picker floats over the current issue page. Command names and
right-aligned shortcuts have separate columns; the selected command's explanation
appears below its row. Its frame stays anchored
while filtering, and its action hints remain visible while scrolling on smaller
terminals. Closing it preserves the issue selection, preview and query.

### Navigate the issue page

*   Arrow keys or `j`/`k` select an issue. Page Up/Down move a page.
    Click a row to select it; click the selected row again to open its details.
    Mouse wheel scrolling follows the list or preview under the pointer.
*   Ctrl+F filters loaded results as you type or paste, without changing JQL.
    Enter keeps the filter; Esc clears its text. Primer matching uses smart case
    and supports `!` negation, `^` prefix and `$` suffix. Modifier-only input
    matches everything. The searchable text includes keys, summaries, status,
    assignee display names and labels.
*   `f` opens counted status, assignee and label facets. One facet combines with
    the text filter; Clear facet removes it. Account IDs distinguish people with
    the same display name. Missing assignees remain distinct from explicit null.
    Counts cover loaded issues; unseen Jira matches are not locally filtered.
*   `O` hands the selected issue to the configured browser through the ordinary
    CLI browser policy. Success confirms process handoff, not page loading.
    `y` sends its key and `Y` its URL to the terminal clipboard using OSC52;
    terminal support is required, and sending does not confirm clipboard storage.
*   Enter focuses the issue description. Esc returns to the list.
*   `r` refreshes the view; `m` loads another bounded batch of results.
    Forward list navigation loads more automatically near the end. Settings →
    Issue list → Load more as you scroll disables that behavior.
*   Ctrl+O switches to recent issues, current results or an exact issue key.
*   `v` opens saved views and the My work, Updated and Reported lenses.
*   Ctrl+S saves the current JQL as a new `.jql` file under `queries_path`.
    Existing names are refused. The menu also loads existing YAML/TOML-frontmatter
    queries through the shared loader; selecting them keeps their authored JQL.
*   Ctrl+W changes keyboard focus between the list and preview. Enter opens full
    details; Esc returns to the list, including after opening an issue directly.
*   `[` and `]` switch Description, Comments, Activity and Links. Comments load
    only when opened, in pages of 20, with a session display bound of 200.
    In focused comments, `m` loads another page and `r` restarts the read.
*   Activity identifies the returned history page; it does not claim to show all
    activity. Links preserve direction; `o` opens a picker for returned linked issues.
*   `?` opens keyboard help; `q` or Ctrl+C closes the dashboard.

Wide terminals show a list and preview side by side. Smaller terminals open
details separately. A terminal below 40 columns or 12 rows shows a resize notice.
Column headings identify issue keys, summaries and status. My work omits the
redundant assignee column. Status pills share a width and retain their category
color when selected. The dashboard uses the CLI's resolved theme and color policy.
Legacy TUI preference migration remains deferred; the current menu does not
silently consume old `tui.sections` or `tui.lenses` declarations.

### Review Jira actions

Select an issue and press `t` to discover its current workflow transitions,
`a` to choose an assignee, or `c` to draft a new Markdown comment. Each action
shows the exact target issue and chosen value before submission. Confirmation
is separate from picking or editing. Actions also appear in the command picker
when available.

Space marks the selected issue; Ctrl+A marks all loaded issues or clears them,
`X` inverts marks and `x` selects the range from the last Space anchor. Marks
follow issue keys across reordering. A new query clears them, and a refreshed
result drops marks for issues it no longer contains. Select-all, invert and range apply to visible rows only. Marks hidden by a local
filter remain included in the displayed selected count and explicit bulk target
review. Selection never includes unseen Jira matches.

From the list, `t`, `a` and `c` act on marked issues when any are marked. `B`
opens the explicit bulk menu. Focused details retain their single-issue action.
Bulk review lists the fixed target keys and defaults to Cancel. Only workflow
choices with an unambiguous matching name and destination on every target are
offered; each issue retains its own native transition ID. Writes run with at most
four concurrent requests and never automatically retry. Acknowledged targets
lose their marks; unconfirmed and unattempted targets remain marked in the same
query. Bulk comments retain a session draft only for the same target set.

`L` opens the last 100 session operation entries; `/` filters by action, target,
state or error. Enter returns to results; Esc clears the filter. In history, `b` opens every
per-issue outcome of the latest completed batch; `h` chooses an earlier retained
batch. Each retained batch keeps its own outcomes. Page Up/Down scroll long reviews
and results. Results distinguish acknowledged, unconfirmed and not attempted;
an acknowledgement is not an independent readback. Inspect an unconfirmed
attempt before repeating it. History and drafts are not durable across exit.

### Create an issue

Press `n`, including from an empty result list, to start a new issue. Select a
project from the bounded native list or enter an exact key or ID, then select an
issue type from that project's current metadata. `default_issue_type` preselects
an exact ID or a unique name, ignoring name case, while keeping every type
available. An unavailable or ambiguous default leaves explicit selection to you.
The form edits summary and a new Markdown description; Tab switches fields.
F2 opens the literal label list,
F3 chooses an assignee and F4 changes project/type while retaining authored text.
F5 starts a clean draft after a discard confirmation that defaults to Cancel.
If issue types cannot be read or type selection is canceled, `n` returns to
project selection with authored text retained.
Assignment offers Jira's default, explicit unassignment, the authenticated
account, visible active-user search or an exact account ID. Jira decides final
assignability. A missing optional field retains Jira defaults; explicit empty
labels and explicit unassignment remain distinct. F2 followed by Enter on the empty input explicitly
applies its label list, including an empty list; Esc from an unchanged list keeps
Jira's default.

Ctrl+S prepares a review without creating anything. It includes pending label
input, converts new Markdown to native ADF and refuses reported conversion loss.
Review shows project/type IDs and authored values, retaining paragraph and label
line boundaries. Cancel is the default; `y` submits once. Current metadata checks
the authored field IDs and operations again before submission. Values,
requiredness and permissions remain Jira's validation; the standard form does
not author arbitrary custom fields. Use native `issue create` input for those.

Esc retains an unsubmitted session draft; `n` resumes it. Acknowledged creation
opens the returned issue when its identity is usable. History retains native
acknowledgement even when a response identity or followup is unusable. An attempted
create locks its draft against casual resubmission. Press `n` to inspect the
outcome history or the previous draft, including its submitted optional-field
choices. Inspect Jira before deliberately starting a different draft, which
replaces the old session draft. No uncertain create is automatically repeated.
Drafts are not durable across exit.

### Edit summary and labels

Press `e` to choose summary or labels on the focused issue; `l` opens labels
directly. These are single-issue edits even when other issues are marked.
The editor reads current values and binds its review to the returned immutable
issue ID. Incomplete identity refuses with `issue_edit_source_invalid`; missing
fields are not treated as empty editable values.

Enter reviews a changed summary. The label editor separates **Add a label** from
**Labels to keep**, and marks the focused area. While typing, the list has no
selection highlight; Tab focuses it for selection and removal. The compact label
overlay centers its title, fields and footer as one left-aligned group. Small
terminals reclaim the extra spacing. The footer styles
keys separately from their actions and keeps them on one row when space permits;
narrow dialogs wrap complete key/action groups. In labels, type or paste one literal label,
then Enter adds it. Commas and quotes remain part of that label. Tab switches
between the add field and label list; arrows or `j`/`k` select a label, and `x`
removes it. Enter on the empty input or from the list (**Next**) opens
**Confirm labels**, including pending input when focus is in the list.
The confirmation shows counts, current labels (or **None**) and the complete
replacement in a centered block, with bold headings, accented counts and indented
label values. Its title, buttons and key hints are centered
separately, while the lists share a left edge;
Tab or the left/right arrows select Cancel or Apply; Enter confirms the selected
button. Cancel is selected by default. Ctrl+S does not advance the label editor.
Removing every entry explicitly sends an empty label array. Reviews show current
and proposed values and default to Cancel; `y` applies them. Unchanged values
send no write.

Esc keeps changed values and pending label input in an issue-ID-scoped session
draft. Unchanged visits do not override later fresh reads. Acknowledged edits
clear their drafts; uncertain attempts retain them for inspection. Drafts are
not durable across exit, and retaining one never authorizes another write.
Before a write, the shared edit metadata owner checks that the authored field
and set operation are available. Jira still validates values and permissions.
Field replacement has no server-enforced compare-and-swap; concurrent edits can
race the request. Existing description bytes are not authored by these edits.

### Edit existing descriptions

Choose **Description** in the `e` menu to edit anchored Markdown from the native
ADF owner. Keep protected markers and regions intact; unknown ADF survives the
round trip. `Ctrl+S` prepares a review, and replacement defaults to Cancel.
`Esc` retains changed text in an issue-, site- and account-bound session draft.
Unchanged visits do not hide newer server content. `F4` reloads source after
confirmation; a failed reload retains the local edit. Drafts do not survive exit.

The shared edit owner checks description metadata and rechecks the reviewed
native candidate before one explicit write. A changed candidate requires another
review. The write is unconditional: a concurrent edit after the final read can
still be overwritten. An uncertain attempt retains its draft and receipt for
inspection; it is never replayed automatically.

### Transitions, assignments and comments

Transitions recheck the workflow before writing. A transition requiring fields
without returned defaults refuses before the write; use the native transition
command for those fields. Assignment offers the authenticated account, visible
active-user search, an exact account ID, or explicit unassignment. Search is
bounded to the first 50 returned users; refine it to find other matches. An
active account lookup does not prove assignability; Jira decides that on write.

Comments are new Markdown documents. Ctrl+S opens review; Esc keeps the draft
in this session. Unsupported Markdown conversion refuses before posting rather
than silently dropping content. The created comment identity is retained on a
valid acknowledgement. An error after an attempt may follow a stored write:
inspect the issue or comment thread before repeating it. Repainting, navigation
and retries of reads never replay a write. Interrupted sessions also require
inspection before repeating an action. Acknowledgement and a later refresh do
not provide remote compare-and-swap or lost-update protection.

### Customize the dashboard

Press `,` or choose Settings from the command picker. Search with `/`, select
with arrow keys or `j`/`k`, and change a boolean with Enter or Space. Tab and
Shift+Tab switch between All, Appearance, Issue list, Issue preview, Defaults,
Tools, Connection, Authentication and Identity. Search matches config keys,
setting names, groups and descriptions. Enter returns from search to
its results; Esc finishes search before a second Esc closes settings.

Display preferences control the side-by-side preview, compact or comfortable
row density, status, assignee and updated columns, and labels, priority and issue
type in previews. The theme picker groups **Built-in themes** and **Your themes**,
with one row per theme. Colors preview immediately as you move or filter; Enter
keeps the highlighted selection in the settings draft. Esc restores both UI and
syntax colors from before opening the picker. With no filter matches, the last
preview remains until you choose a match or cancel. Ctrl+S in Settings saves
the base preset and custom path together; a built-in selection clears that path.
Columns still adapt to terminal space. Each row shows its effective source; environment overrides
are visible and cannot be edited here. Press `d` to preview the selected
setting's built-in default (`true` for dashboard booleans, `auto` for the theme).

From the theme picker:

*   Ctrl+N creates a named copy of the currently visible palette.
*   Ctrl+D duplicates the highlighted theme. Built-ins and external custom files
    also open as a new copy when you press Ctrl+E; managed custom themes edit in place.
*   Ctrl+I imports a theme TOML file. Choose its name, then save it in the editor;
    the original file is kept.
*   Ctrl+R renames a custom theme without changing its file path or saved selection.
*   Ctrl+X exports the highlighted theme to a new TOML file; existing files are kept.

In the editor, Tab and Shift+Tab switch between Interface, Syntax and Palette.
Type to filter roles; Enter edits the selected color, base preset or entity palette.
The editor uses aligned Role, Color and Source columns, with one row per role.
Color swatches show resolved values, and Base or Custom identifies inheritance.
The selected role highlights in its own color with contrasting text. Terminal
default roles use the normal selection color; swatch and source cells stay stable. Enter a full
`#RRGGBB` color to preview it immediately; clear an override to inherit its base.
Enter keeps the field edit, while Esc restores its previous value. Ctrl+S saves
the theme file and selects it in the Settings draft. Esc in the editor discards
its unsubmitted changes. Save Settings separately to keep the selected theme.
Explicitly saved theme files remain available if you later cancel Settings.

Your themes live in the `themes` directory beside the selected configuration.
New names and exports refuse overwriting existing files. Changes made to a file
after opening it refuse an edit; these snapshot checks are not filesystem
compare-and-swap. Invalid theme files stay visible with an error and do not
replace the current preview. **Theme file (advanced)** retains compatibility
with existing custom paths; the picker also includes the configured external file.

Every public scalar config setting is editable here, including project/type/board
defaults, team account IDs, workday duration, paths, editor, glyphs, timeout,
read-only access, connection and credential-provider metadata, and cached public
identity. Booleans toggle with Enter or Space. Other values open an editor:
Enter keeps the value in the settings draft, Esc cancels that field, and arrow
keys choose enumerated values. Empty strings clear optional values. Team account
IDs use a JSON array. Invalid values stay in the editor with an explanation.

Display preferences and named themes preview in this session. Ctrl+S saves and
reloads the selected profile in the running TUI, including its client, transport
read-only policy and timeout, project tab, query paths, glyphs and custom palette.
Environment and invocation-flag overrides retain their precedence and stay locked
in the menu. Reload does not move credentials, verify identity, or turn metadata
preferences into automatic refresh or other unavailable features. API tokens remain
managed by `jira auth login`; aliases use `jira alias set` or `jira alias import`
for command-tree validation. Their settings entries explain these dedicated
workflows without opening a scalar editor or exposing tokens.

Ctrl+R reloads the selected profile without publishing settings. Save or cancel a
settings draft first. This also picks up external file changes; automatic file
watching is not enabled. Reload and save wait until running Jira writes finish.
The config path, selected profile, captured environment and flag overrides remain
bound to the invocation even if the file's default profile changes.

On successful reload, reads from the prior runtime are canceled and late results
are ignored. Local drafts, creation work and operation history stay scoped to the
site, account and API route; changing scope parks that work until you return.
If publication succeeds but reload fails, the file remains saved. The old
connection remains active for reads, Jira actions are blocked, and a persistent
banner reports the mismatch. Fix the settings or their dependencies, then use
Ctrl+R to retry reload without saving again.

## Save

Closed choices such as Credential provider and Glyph set open a selection menu
with the current value selected. Enter keeps a choice in the draft; Esc cancels.
The Settings heading counts unsaved changes. The area below the list shows the
selected value and its source, including full paths and URLs when space permits.
Display options explain when the current view or terminal width hides their
effect. Successful save and reload reports "Saved and active"; a reload failure
retains the separate saved-but-inactive warning.

Press `p` to inspect the issue page with the draft display settings applied.
Esc returns from that preview to Settings with the draft intact.

The session keeps its query, navigation, comment drafts and up to 20 recent
issues in memory and restores the terminal
when closed. Display changes preview immediately. Ctrl+S saves changed settings
together through the ordinary config file owner; Esc discards changes since the
last successful save. A failed save retains the draft and does not report success.
A separate reload failure does not erase a successful publication.
Inspect the config before retrying after a publication error.

The public boolean keys are `tui_preview`, `tui_status`, `tui_assignee`,
`tui_labels`, `tui_priority`, `tui_issue_type`, `tui_compact`, `tui_updated`
and `tui_auto_page`. They default to `true`, live at
the document root in named-profile files, and also work through `config set`.
Ordinary `JIRA_` environment overrides take precedence. Settings saves preserve
other declarations and profiles and refuse detected snapshot conflicts.

Check the JQL line to see the query being run. The issue heading reports loaded
rows, not an invented total. Forward navigation near the end of the list loads
another bounded batch; the session retains at most 5,000 issues before asking
for a narrower query. Loading is single-flight and keeps the selected issue.
A failure retains the loaded results and waits for an explicit `m` retry.
Local filters apply to loaded rows and do not drain unseen pages by themselves.
Disable `tui_auto_page` in Settings for manual-only loading. Refresh keeps the
selected issue by key when it remains in the results and reloads its preview.
Descriptions render native ADF through the shared display owner; unsupported
nodes remain visible as disclosures. Reading a preview does not edit ADF.

## Preconditions

The dashboard requires a configured Jira account with permission to read the
requested issues. `default_project` enables Project; an unset project leaves
My work and Search available. Board and legacy TUI preferences do not affect
these explicit views. Jira actions additionally require permission for the
selected write; read-only mode suppresses them.

## Recover

An unsuccessful search or detail read shows an error and keeps the dashboard
open. Press `r` to retry a read, or `/` to correct the query. Incomplete pagination
stops loading more; refresh or narrow the query. Explicitly missing/null fields
remain distinct from unavailable fields. No background polling is enabled.

## Next

Use `jira issue view ISSUE_KEY` for a one-shot native issue receipt, or
`jira issue list --jql='...' --output=json` for machine-readable search results.
