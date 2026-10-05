---
description: >
  Security rules: credential boundaries and the never-reveal-token
  posture, the read-only transport gate, untrusted Jira content, exec and
  path discipline, and the supply-chain setup.
paths:
  - "internal/config/**/*.go"
  - "internal/jira/**/*.go"
  - "internal/cli/**/*.go"
  - "internal/cli/issue/web.go"
  - "internal/editor/**/*.go"
---

# Security

## Credentials: stored once, revealed never

*   Credentials live in the OS keyring, 1Password, or environment fallbacks
    — never in the config file, logs, envelopes, or test fixtures. Profiles
    are metadata-only by design (see [config.md](config.md)).
*   The `auth token` redaction boundary is deliberate (it prints redacted
    last-four diagnostics only): never add a code path, flag, or debug
    output that reveals a stored token. When an operation genuinely needs
    the credential (a raw API call, a new backend), it goes through the
    existing client/store layers — the token value never transits an
    envelope, a log line, or `argv`.
*   gosec `G101` scans identifiers matching
    `passwd|pass|…|token|api_key` with entropy checks — a hardcoded-looking
    secret fails CI (test paths exempt).

## Read-only is a transport property

Read-only mode blocks mutations at the HTTP transport
(`internal/jira/client.go`), resolved per-invocation by
`command.ReadOnlyEnabled` (`JIRA_READ_ONLY` env wins OFF→ON only; on
config-load failure it fails *writable* so the real error surfaces instead
of a masking refusal). Keep enforcement at that layer — never add a code
path that routes a write around the transport, and never key the decision
off anything but the shared resolver.

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
