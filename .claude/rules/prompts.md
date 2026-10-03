---
description: >
  Interactive prompts and confirmations: when to ask, when never to ask,
  what the question must name, and how the title is styled. Covers huh
  forms, destructive confirmations, and the cancel path.
paths:
  - "internal/cli/**/*.go"
---

# Prompts and confirmations

A prompt is the one thing a command can do that an agent cannot answer.
Every rule here follows from that. For the command pattern see
[commands.md](commands.md); for output see [output.md](output.md).

## When to ask

Ask before **removing or replacing state the caller cannot re-derive** —
a stored credential, a file only this command wrote. Do not ask before a
read, before something Jira can undo, or before a `--dry-run`.

A preview destroys nothing, so it never confirms. Gating a preview on a
question is how people learn to answer questions without reading them.

## Never ask without someone to answer

Use `command.Invocation.Interactive`, resolved by the shared construction
path. It requires terminal stdin and stderr, no detected agent and no explicit
`--no-input`. Do not duplicate those probes in a handler. `--no-input` is an
optional promise that the command will not ask; it is not write authorization
or a prerequisite for every headless mutation.

*   **Agent detection beats a TTY.** An agent harness that allocates a PTY
    passes every terminal check and then sits in front of a form it cannot
    drive. `agent.Is()` already knows; consult it.
*   **Stderr, not stdout.** The prompt is drawn on stderr, so that is the
    stream that must be a terminal. Testing stdout refuses
    `jira x --output=json > out.json` from a real terminal — the exact
    invocation the stderr routing exists to allow.
*   A non-interactive caller gets an **error naming the flag** that skips
    the question (`--force` or `--yes`, as declared by that command), never a
    wait. A command that blocks on input nobody can supply is worse than one
    that fails.

## What the question must name

*   **Name the thing, not the act.** "Are you sure?" is unanswerable.
    "Remove the stored token for `<account>` on `<site>`?" can be checked.
*   **Read the identity from the source of truth**, not from merged
    config. `JIRA_EMAIL` outranks the keyring, so resolved config once
    had logout asking about one account and deleting another.
*   **Resolve once, thread it through.** The value shown in the prompt
    and the value acted on must be the same variable. Resolving separately
    in the confirmation and in the action is two chances to disagree —
    and on a macOS keychain, two prompts.
*   **Say what will still be true afterwards.** Removing a stored token
    does not revoke it, and does not remove one in the environment or the
    config file. Reporting success while the next command still works is
    the most misleading thing a confirmation can do.

## Styling the title

Roles, both rendered explicitly:

```go
text, accent := titleStyles()
title := text.Render("Remove the stored token for ") +
    accent.Render(account) +
    text.Render(" on ") + accent.Render(site) + text.Render("?")
```

*   **Style every segment, including the plain ones.** A styled span emits
    its own escape sequence and RESETS at the end, so styling only the
    highlights leaves everything after them at the terminal default. That
    produced a title reading indigo, white, indigo, white. `command.Question`
    takes alternating plain and highlighted parts so this is structural
    rather than something each caller has to remember.
*   **Theme the whole dialog, not just the title.** huh ships its own
    palette and applies it to the buttons unless given a theme, so a
    themed question sat above two buttons in a different color scheme.
    `command.Confirm` maps the resolved theme onto huh's styles for
    every dialog; do not assemble a form by hand.
*   **Two roles, not more**: the sentence, and the values in it. The
    accent is the color an inline `` `code` `` span already gets in every
    error hint — a highlighted value is the same kind of thing, a literal
    to read exactly.
*   **Resolve styles per call, never at package scope.** The theme is a
    property of where the output is going, not of the build, so a style
    computed at init is computed before anything knows.
*   **Never probe the terminal to get one.** `terminal.IsDark()` asks
    over OSC 11 and waits for a reply; a terminal that stays quiet costs
    the whole timeout — measured at 5108ms, on every command. Read the
    theme the root command already resolved from the environment
    (`JIRA_THEME` → `CLIB_THEME`/`CLOG_THEME` → `THEME`, then
    `COLORFGBG`), which is why that resolution never returns nil.
*   A qualifying warning goes in the Description, in the quieter register
    (italic + faint), not in the question.

## Cancelling

Closing a form is a **choice, not a failure**. Map `huh.ErrUserAborted`
to `cli.CodeCanceled` with a message saying what did not happen
("logout cancelled — the token is still stored"). Exit stays non-zero so
a script can tell nothing happened; the message does not read like a
crash, and carries no stack or generic "try again" hint.

## Gotchas

*   **Stop spinners before rendering a form** (see commands.md).
*   **`huh` v2 only** — `charm.land/huh/v2`. The v1 packages drag a second
    rendering stack in beside the one clib already brings.
*   **A prompt is not a substitute for a flag.** Every prompt must have a
    non-interactive equivalent (`--with-token`, `--site`, `--yes`), or the
    command is unusable by half its audience.
