---
description: >
  Changelog fragments for agents: every user-facing feat/fix ships a changie
  fragment, created non-interactively; the commit-msg gate that enforces it;
  the Changelog: skip escape (CI/workflow and other non-user-facing changes);
  and how fragments become CHANGELOG.md. Applies to any change, since the gate
  fires on the commit type, not the files touched.
---

# Changelog

User-facing changes are recorded as [changie](https://changie.dev) fragments
under `.changes/unreleased/`, assembled into root `CHANGELOG.md` at release
time. `docs/changelog.md` is a one-line snippet include of `CHANGELOG.md`.

*   **Any user-facing `feat`/`fix` needs a fragment.** Create it
    **non-interactively** — you cannot answer `changie new`'s prompts — and
    stage it, as part of the change:

    ```sh
    changie new -k <added|changed|fixed> -b "<user-visible outcome>" --interactive=false
    git add .changes/unreleased/
    ```

    `-k` takes the lowercase kind **key** (not the label): `added`, `changed`,
    `fixed`, `breaking`, `removed`, `deprecated`, `security`, `dependencies`. The
    release notes render the label (`### Added`, `### Breaking Changes`). Use
    `dependencies` for dependency-version bumps (renders under `### Dependencies`).

    That is the changie binary, so it behaves the same under PowerShell, bash,
    or sh. The gate itself runs automatically at commit time (the `check-changie`
    mise task, a cross-platform Go program) — you don't invoke it yourself.
*   **Write the body for the release-notes reader**: the user-visible outcome,
    imperative, no emoji, no commit SHAs or PR numbers, scope prefix stripped.
    `CHANGELOG.md` and the `.changes/v*.md` files are the style exemplar.
*   **Describe the net change from the previous release.** For this rebuild,
    compare with the last v0.15 release, v0.15.2. An existing command returning
    in the new layout is not an added feature. Describe its changed behavior,
    payload, workflow or safety contract; omit unchanged capability inventories
    and internal construction milestones. Consolidate repairs to unreleased
    regressions into the final outcome instead of presenting them as v0.15 bugs.
    Reserve `fixed` for established released defects and `added` for genuinely
    new capabilities. Record incompatible layouts, settings, payloads and exit
    policies under `breaking`, and unavailable former workflows under `removed`.
    Preserve limitations: readback and local merges do not establish protected
    remote writes, and local tests do not establish native platform acceptance.
*   **Only `feat`/`fix` are gated.** Everything else — `docs`, `chore`, `ci`
    (workflows), `build`, `style`, `refactor` — is exempt; just use the honest
    type. A `feat`/`fix` that genuinely isn't user-facing takes a
    `Changelog: skip` trailer.
*   **Never hand-edit `CHANGELOG.md`** (changie assembles it), and never bypass
    the gate with `git commit --no-verify`. Releases at or before v0.7.7 are
    frozen as `.changes/v*.md` files; don't rewrite them.
