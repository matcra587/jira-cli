# Contributing

Notes for working on jira itself: building from a checkout, running the local CI gauntlet, and the tooling expectations on commits.

End users don't need anything here. If you just want a working binary, see the [installation guide](https://matcra587.github.io/jira-cli/installation/).

## Before you start

For anything substantial, open an issue before you build. I may have opinions on priority, scope, or how it's implemented, and it's better to sort that out up front than after you've written the PR.

Even for an obvious fix (a typo, a doc correction, a clear bug), raise an issue and the PR that closes it.

Two things hold for every change: CI must pass (`mise run ci`), and review comments get addressed before merge.

## Prerequisites

*   [mise](https://mise.jdx.dev) provisions the pinned toolchain (Go, the linters, everything else). Nothing else to install by hand.
*   Nothing else. The tree builds with `CGO_ENABLED=0`.

## Setup

```sh
gh repo clone matcra587/jira-cli
mise --cd jira-cli run install --locked
jira version
```

`--cd` runs in the freshly cloned directory, so there's no separate `cd`. `mise run install` auto-provisions the pinned toolchain before the task (`--locked` requires it to be pre-resolved in `mise.lock`), then runs `go install ./cmd/jira` with the version ldflag set, so `jira version` reports the exact `git describe` build. Without that ldflag a build falls back to whatever Go records in its build info, which yields a `-dev` version string.

> [!TIP]
> `mise run build` writes a release-shaped binary to `./dist/jira-<goos>-<goarch>`. Use it to inspect the exact shape the release pipeline produces.

## Project layout

*   `cmd/jira/` is the entry point; `cmd/gen-wire/` generates the Jira wire types, `cmd/check-changie/` is the commit-msg changelog gate.
*   `internal/cli/` holds the command tree, the output selector, and the error taxonomy that maps to the exit-code contract.
*   `internal/output/` and `internal/envelope/` are the output core: the envelope frame, the renderers, and the jq filter.
*   `internal/adapters/jira/` is the REST client; `internal/jira/` holds the generated wire types.
*   `internal/adf/` parses, validates, and renders Atlassian Document Format.
*   `tests/` holds the `contract`, `unit`, `guardrails`, and `audit` suites. The audit suite is build-tagged `outputaudit` and runs the real binary — `mise run audit`.

## Tests and checks

One task runs the full local equivalent of CI. Run it before you push or open a PR; the same checks run in GitHub Actions on every PR:

```sh
mise run ci
```

It chains `check` (fmt, lint, rumdl, vet, tests), the race suite, the output-contract audit, a `go mod tidy` drift check, the GitHub Actions workflow linter, and the security scan.

> [!WARNING]
> `mise run ci` is macOS/Linux only for now. The full `ci` task currently fails on Windows; run it on macOS, Linux, or WSL before pushing.

For tighter loops while editing:

| Task | What it does |
|---|---|
| `mise run check` | fmt-check + lint + rumdl + vet + tests |
| `mise run test` | tests with coverage (fast) |
| `mise run test:race` | the same suite under the race detector |
| `mise run audit` | the output-contract audit on its own; hermetic, stub-driven, runs the real binary |
| `mise run fix` | auto-apply fmt + lint + rumdl fixes |

Run a single package or test with `go test` directly:

```sh
go test ./internal/adf/... -run TestValidate
```

## Code style

`gofumpt` for formatting, `golangci-lint` for the rest, `rumdl` for Markdown. `mise run fix` applies every auto-fix in one go; `mise run check` is the read-only gate CI runs.

US spelling in code, comments, and identifiers: the linter's misspell rule is locale `US`, so British spellings (`behaviour`, `colour`) fail the build.

## Error hints

Every error code carries a `hint` — the next action, in the plain words a person would use, not a recap of what failed. A hint is read both by a human (it prints as the 💡 line under the error) and by an agent (the JSON envelope's `hint`), so keep it jargon-free: don't point at envelope fields, and when the fix needs a value the caller doesn't have, name the command that surfaces it. Every hint is static, defined in the taxonomy registry in `internal/cli/errors.go` keyed by code — a hint never embeds a runtime value. If a specific matters (the offending value, a "did you mean" suggestion), put it in the error `message` or a structured envelope field like `field` or `suggestions[]`, not the hint; if two error types want different hints, give them different codes. `TestRegistryIsComplete` fails the build if any code is missing its row or ships an empty hint.

## Changelog

User-facing changes are recorded as small [changie](https://changie.dev) fragments — one file per change — and assembled into `CHANGELOG.md` at release time. This way the release notes never depend on anyone reconstructing them from the git log after the fact.

If your change is something a user would notice (a new flag, changed output, a fixed bug), add a fragment as part of the change:

```sh
changie new
```

It prompts for a kind (`Added`, `Changed`, `Fixed`, …) and a one-line summary, then writes a file under `.changes/unreleased/`. Write the summary for the person reading the release notes — the user-visible outcome, not the commit subject — and stage it alongside your change.

`.changes/unreleased/` is a staging area: fragments accumulate there, one per change, between releases. Cutting a release (`mise run release-notes <version>`) batches every pending fragment into a single dated `.changes/<version>.md` section and empties the directory — the `.gitkeep` just keeps it tracked while empty — and `changie merge` rebuilds `CHANGELOG.md` from the header and every version file. So a fragment is transient scaffolding that lives in `unreleased/` only until the next release folds it in.

Only `feat` and `fix` commits need a fragment; the commit-msg hook rejects one that's missing and prints how to fix it, so you can't get stuck. Every other type (`docs`, `chore`, `ci`, `build`, `style`, `refactor`) is exempt. A `feat`/`fix` that genuinely isn't user-facing takes a `Changelog: skip` trailer instead:

```text
This feat/fix needs a changelog fragment: run 'changie new' and stage it,
or add a 'Changelog: skip' trailer if it isn't user-facing.
```

Don't hand-edit `CHANGELOG.md`; changie assembles it from the fragments, and the docs site's Release Notes page is a one-line include of it.

## Publishing releases

Releases use `mise run release -- <version> [<commit>]` from a clean `main` checkout. The target must already be on `origin/main`; its push CI gate must pass before GitHub publishes the artifacts. Batch the release notes with `mise run release-notes <version>` and commit them before creating the tag.

Use a prerelease version such as `v0.16.0-beta.0` or `v0.16.0-rc.0` to publish a GitHub prerelease. Prereleases leave the stable latest release, Homebrew formula and Scoop bucket unchanged. Stable tags such as `v0.16.0` update all three. The version may omit the leading `v` in the local task; pushed tags require it. Numeric version components and numeric prerelease identifiers cannot contain leading zeros; release tags do not accept build metadata.

```sh
mise run release -- --dry-run 0.16.0-beta.0
```

The dry run performs the full local preflight but creates and pushes no tag. `mise run test:release` checks release routing with disposable local repositories and builds fixture archives with publishing disabled.

## Opening a pull request

1.  Fork the repo and branch off `main`.
2.  Make your change, with tests.
3.  Run `mise run ci` and make sure it passes (`mise run fix` first to auto-fix formatting and lint).
4.  Push and open a PR against `main`.

Before you open it:

*   **Use a conventional-commit PR title.** PRs are squash-merged, so the PR title becomes the single commit on `main`: `feat(issue): …`, `fix(adf): …`, `docs: …`, `chore: …`. (Branch commit messages get squashed away, but the pre-commit hook still checks their format.)
*   **Sign your commits.** `git commit -S` (SSH or GPG). Signed commits are required on every PR.
*   **Keep spec-tool jargon out of messages.** spec-kit, superpowers and the like generate internal IDs for plans and tasks (`T046`, `FR-011`, `RED→GREEN`). Those mean nothing in a `git log`, so leave them out of commit messages and the PR title.

By opening a PR, you agree your contribution is released under the project's [MIT licence](LICENSE).

## AI-assisted contributions

jira-cli is built for agents, so AI-assisted PRs are welcome. The bar is the same either way: you own what you submit. Read the diff, understand it, make sure it passes CI, and don't open a PR you couldn't explain yourself. Flag significant AI assistance in the PR description.

## Pre-commit hooks

**Optional**, but it catches what CI checks locally, before you raise the PR. The repo ships an [`hk`](https://github.com/jdx/hk) hook configuration in `hk.pkl`; on commit it runs `gofumpt`, `go vet`, `golangci-lint`, `rumdl`, and `betterleaks`, plus `actionlint` and `zizmor` over the GitHub Actions workflows. On push it adds `govulncheck`. On the message it checks the conventional-commit format, scans it for leaked identifiers, and requires that a user-facing `feat`/`fix` ships a changelog fragment (see [Changelog](#changelog)). Install it once per checkout:

```sh
hk install
```

The hook globs the working tree, not just staged files, so a stale file elsewhere in the repo can fail the commit.

## Docs site

Source pages live in `docs/`; the site is [zensical](https://zensical.org), configured in `zensical.toml`. Build or serve it directly:

```sh
uvx --from zensical zensical build -f zensical.toml
uvx --from zensical zensical serve -f zensical.toml
```

The mise wrappers and the generated command reference under `docs/reference/` were pruned with `cmd/gen-docs`; they return when the generator is rebuilt for the current command tree. Until then, `docs/` documents only what the binary actually ships — a page describing an unbuilt command does not belong in the tree.

## Getting help

Stuck, or not sure whether something's a bug? Open an [issue](https://github.com/matcra587/jira-cli/issues). Questions are welcome, not just bug reports. For a bug, include your `jira version` output, your OS, and the steps to reproduce.
