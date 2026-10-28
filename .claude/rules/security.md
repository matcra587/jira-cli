---
description: >
  Security rules: credential boundaries and the never-reveal-token
  posture, the read-only transport gate, untrusted Jira content, exec and
  path discipline, and the supply-chain setup.
paths:
  - "internal/config/**/*.go"
  - "internal/jira/**/*.go"
  - "internal/cli/**/*.go"
  - "internal/adapters/**/*.go"
  - "internal/credentials/**/*.go"
  - "internal/issueedit/**/*.go"
  - "internal/nativepath/**/*.go"
  - "internal/attachmentfiles/**/*.go"
---

# Security

## Credentials: stored once, revealed never

*   Credentials live in the OS keyring, 1Password, or environment fallbacks
    — never in the config file, logs, envelopes, or test fixtures. Profiles
    are metadata-only by design (see [config.md](config.md)).
*   Ordinary settings, receipts and diagnostics never reveal tokens. `auth token`
    deliberately emits only the raw token and newline; `auth status --show-token`
    is the other explicit reveal. Preserve these declared contracts and never
    expose tokens through another code path, flag or debug output. When an operation genuinely needs
    the credential (a raw API call, a new backend), it goes through the
    existing client/store layers — the token value never transits an
    envelope, a log line, or `argv`.
*   gosec `G101` scans identifiers matching
    `passwd|pass|…|token|api_key` with entropy checks — a hardcoded-looking
    secret fails CI (test paths exempt).

## Read-only is a transport property

Read-only mode blocks Jira writes at the shared HTTP boundary in
`internal/adapters/jira`, including raw API writes, while allowing classified
reads such as enhanced-search POSTs. `config.Resolve` supplies the resolved
`Config.ReadOnly` through the composition root; configuration errors stop
client construction. Environment overrides file values in both directions:
`JIRA_READ_ONLY=false` or `0` disables file `read_only=true`. Use
`JIRA_READ_ONLY=true` when the invocation must refuse Jira writes. Preserve
this ordinary configuration precedence and keep enforcement shared; never
route a write around it. This is not a read-only filesystem mode and does not
block local configuration or cache publication.

Scoped authentication uses only the fixed Atlassian gateway and one validated
cloud path segment. Keep site identity separate from API routing. Stored scope
changes cannot reuse a verified token tuple implicitly. An account reply outside
the authenticated API scope, or without the exact supplied Basic pair, does not
verify a credential even when HTTP and its JSON account object look successful.
Public cloud discovery uses no tokens/cookies and no redirects; retain context
and protocol failures through authentication fallback.

## Jira content is untrusted input

Everything Jira returns (summaries, descriptions, comments, field values,
even completion candidates) is third-party text:

*   Route it through the terminal sanitizer before it reaches a terminal —
    completion candidates already are (tab/newline injection); every new
    output path must be too.
*   Never feed Jira-sourced strings into shell commands, file paths, or
    format strings unvalidated.

## Exec and path discipline

*   `exec.Command` with argument slices only — never a concatenated shell
    string. `x/shell.Quote`/`Split` implement POSIX display semantics; they
    are **not** an injection defense.
*   Scope untrusted file paths (attachment names, editor temp files) before
    writing; never join them into a parent directory unchecked. gosec
    `G306` sets the permission bar at `0750`;
    the reviewed `G304` exemption list (stdin, editor roundtrip, cache,
    docs gen, loaders) grows only with the same review.
*   Issue browser opens go through `internal/cli/issue/web.go`; its reviewed
    process boundary receives separate argv entries. URL-only issue reads use
    settings without credentials, and optional local history requires explicit
    site/account scope. Machine modes return the URL without launching a browser.

*   Attachment downloads bound actual streamed bytes before publication, regardless
    of advisory size metadata. Keep the 100 MiB default and positive explicit
    `--max-download-bytes` override. Refuse overflow without writing the probe
    byte, remove partial/staging files and preserve existing overwrite targets.

## Enforcement is layered, not a redaction filter

There is no output-scrubbing utility, and you should not assume one:
protection comes from boundaries — profiles hold no secrets, stores never
print, command specs return typed data through the shared envelope writer, and
depguard/ruleguard ban the escape hatches (`log`, raw pflag, unwrapped
calls — [style.md](style.md)). Treat any secret appearing in a printed
string, envelope field, or command argument as a bug, not a filtering gap.

## Supply chain

GitHub Actions are SHA-pinned and zizmor-audited (pedantic); releases are
GoReleaser-built and cosign-signed. `mise run ci` includes Go lint/gosec and
govulncheck. hk pre-commit runs the ordinary linters; pre-push and `hk check`
also run govulncheck. A new dependency needs justification —
gechr-first (see [go.md](go.md)).

The `security` task runs bare govulncheck (`go tool govulncheck ./...`).
There is no advisory allowlist — fix the code or bump the dependency.

## Gates

`--no-input` suppresses prompts; agent detection and terminal checks also make
an invocation noninteractive. It neither grants permission to mutate nor has
to accompany every headless write. Commands that require confirmation must
refuse without their declared bypass (`--force` or `--yes`) when prompting is
unavailable. Preserve the per-command policy through shared invocation and
confirmation owners; see [commands.md](commands.md) and [prompts.md](prompts.md).
