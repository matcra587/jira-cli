# jira

Terminal-first Jira Cloud CLI for developer and agent workflows.

## Install

```sh
go install github.com/matcra587/jira-cli/cmd/jira@latest
```

See [docs/installation.md](docs/installation.md) for Homebrew, Scoop, mise,
release archives, the one-line installer, and uninstall steps. The examples
below assume `jira` is on `PATH`.

## Commands

```sh
jira version           # version, plus build provenance in the machine modes
jira completion fish   # completion script for bash, elvish, fish, nu, pwsh, or zsh
jira agent guide       # credential-free workflow index
jira guide raw-api     # the same embedded guide source, through the human door
jira agent schema --path 'api post' --output=json
jira adf convert --input notes.md --output=json
jira adf render --input document.json --output=json
jira agent adf-matrix --output=json
jira agent fieldtypes --output=json
jira user search sam --output=json
jira boards list --all --output=json
```

`--help` on a command prints the long form with worked examples; `-h` prints the
short one.

`jira config --help` describes initialization, stored settings and effective
configuration provenance. Read `jira guide discover-cli` for configuration and
authentication discovery, and `jira guide configure-theme` for palettes.

ADF conversion uses the shared Markdown body converter and validates the
candidate. Strict mode refuses dropped content; inspect the retained candidate
and `diagnostics` before explicitly accepting loss. `adf render` returns a
Markdown review projection with `lossy_constructs`. Keep original native ADF for
editing; a Markdown projection cannot provide a lossless round trip.

User search returns visible active account candidates. Choose an account explicitly;
email may be hidden by privacy settings. Paging covers Jira's first 1,000 matches.
Read `jira guide machine-output` for continuation and partial-result handling.

Board discovery caches visible identities and associated project keys per site
and configured account. `--all` returns the fetched snapshot; `--unbounded`
removes board fetch caps. Inspect truncation and project failures, and use
`--refresh` after configuration or permission changes.

Prime visible metadata with `jira cache fields`, `cache boards` or another
resource listed by `jira cache --help`. `--dry-run` inspects local freshness
without authentication or writes; it needs explicit public site/account
settings. `--force` fetches again. Primers retain native values and honest
completion/storage receipts; failed reads preserve the previous cache entry.

## Output

One selector, `--output`, on every command: `auto`, `human`, `json`, or
`compact`. Under `auto`, a detected agent gets `compact`, any other non-TTY
gets the full JSON envelope, and an interactive terminal gets human output.

```sh
jira version --output=json
jira version --output=compact
```

Machine modes write one line of JSON to stdout on success *and* failure, so a
consumer parses one stream either way. The human `❌` diagnostic always goes to
stderr. Exit codes are typed, 0 through 8.

A built-in `--jq EXPR` filters that JSON in-process — no external `jq`
required. Strings print raw, everything else prints as JSON, one result per
line. Bind values rather than splicing them in:

```sh
jira version --jq '.data.go_version'
jira version --jq 'select(.data.platform.os == $os)' --jq-arg os=linux
```

The full contract — envelope shape, every exit code, the jq rules — is in
[docs/output.md](docs/output.md).

Embedded guides own current workflow instructions. `jira agent guide --output=json`
returns their index and routing metadata. Read one runbook by slug, select a
section with `--section`, or request the canonical corpus with `--all`.
`jira agent guide raw-api --jq '.data.document'` extracts its Markdown.
Bare `jira` displays native help for human output and the same runtime schema
for machine output. `--jq` filters the normal selected document shape.

The runtime schema describes the assembled command tree, declared arguments,
visible flags, operation classes and output shapes.

`jira agent export --format agent-skill --dir skills --dry-run --output=json`
prepares portable skills from those guides. Omit `--dry-run` to publish them;
existing artifacts require `--force`. `--scope project --harness codex` resolves
the project's `.agents/skills` directory. Read `jira guide export-guides` for
scope, format and partial-receipt behavior.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
