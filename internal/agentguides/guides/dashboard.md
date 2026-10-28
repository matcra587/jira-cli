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
help or discovery. The dashboard browses Jira and can transition, assign or comment on one explicit
issue after review. `JIRA_READ_ONLY=true` disables these actions and retains the
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

The command picker floats over the current issue page. Its frame stays anchored
while filtering, and its action hints remain visible while scrolling on smaller
terminals. Closing it preserves the issue selection, preview and query.

### Navigate the issue page

*   Arrow keys or `j`/`k` select an issue. Page Up/Down move a page.
*   Enter focuses the issue description. Esc returns to the list.
*   `r` refreshes the view; `m` loads another bounded batch of results.
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
Shift+Tab switch between All, Appearance, Issue list and Issue preview. Search
matches setting names, groups and descriptions. Enter returns from search to
its results; Esc finishes search before a second Esc closes settings.

Display preferences control the side-by-side preview, compact or comfortable
row density, status, assignee and updated columns, and labels, priority and issue
type in previews. The theme row opens a preset picker and previews the selected
palette; saving a preset also clears the custom palette path in the same update. Columns still adapt
to terminal space. Each row shows its effective source; environment overrides
are visible and cannot be edited here. Press `d` to preview the selected
setting's built-in default (`true` for booleans, `auto` for the theme).

## Save

Press `p` to inspect the issue page with the draft display settings applied.
Esc returns from that preview to Settings with the draft intact.

The session keeps its query, navigation, comment drafts and up to 20 recent
issues in memory and restores the terminal
when closed. Settings changes preview immediately. Ctrl+S saves changed settings
together through the ordinary config file owner; Esc discards changes since the
last successful save. A failed save retains the draft and does not report success.
Inspect the config before retrying after a publication error.

The public boolean keys are `tui_preview`, `tui_status`, `tui_assignee`,
`tui_labels`, `tui_priority`, `tui_issue_type`, `tui_compact` and `tui_updated`. They default to `true`, live at
the document root in named-profile files, and also work through `config set`.
Ordinary `JIRA_` environment overrides take precedence. Settings saves preserve
other declarations and profiles and refuse detected snapshot conflicts.

Check the JQL line to see the query being run. The issue heading reports loaded
rows, not an invented total. More results require an explicit `m`; the session
retains at most 5,000 issues before asking for a narrower query. Refresh keeps the
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
