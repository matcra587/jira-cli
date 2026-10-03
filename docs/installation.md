---
title: Install
description: Install jira-cli on macOS, Linux, or Windows via Homebrew, Scoop, mise, the one-line installer, go install, or pre-built binaries.
icon: material/tray-arrow-down
---

# :package: Install

jira-cli ships as a single static Go binary. Pick a method below; each tab is
self-contained. Working on jira itself? See
[Contributing](https://github.com/matcra587/jira-cli/blob/main/CONTRIBUTING.md) instead.

## Install methods

=== "Homebrew"

    Recommended on macOS (Apple Silicon) and Linux (amd64/arm64).

    ```sh
    brew tap matcra587/tap
    brew install matcra587/tap/jira
    ```

    The formula lives in
    [`matcra587/homebrew-tap`](https://github.com/matcra587/homebrew-tap)
    and tracks the latest GitHub release.

    ??? tip "No Homebrew?"
        Use the one-line installer:

        ```sh
        curl -fsSL https://matcra587.github.io/jira-cli/install.sh | sh
        ```

    ??? note "Intel macOS / build from source"
        No `darwin/amd64` bottle is published. `brew install --HEAD
        matcra587/tap/jira` compiles the latest `main` with the Go toolchain
        and embeds version metadata. It's the one Homebrew path that works on
        Intel macOS.

=== "Scoop"

    Recommended on Windows (amd64/arm64). Add the bucket once, then install:

    ```powershell
    scoop bucket add matcra587 https://github.com/matcra587/scoop-bucket
    scoop install matcra587/jira
    ```

    The manifest lives in
    [`matcra587/scoop-bucket`](https://github.com/matcra587/scoop-bucket)
    and tracks the latest GitHub release.

=== "mise"

    Works anywhere [mise](https://mise.jdx.dev) runs. It downloads the release
    binary from GitHub Releases:

    ```sh
    mise use -g github:matcra587/jira-cli@latest
    ```

    List versions with `mise ls-remote github:matcra587/jira-cli`. mise pulls the
    release archives, so there's no Intel-macOS build; for checksum and signature
    verification, use the **Pre-built binaries** tab.

=== "One-line installer"

    For Linux (amd64/arm64) and Apple Silicon macOS. A POSIX-sh installer
    auto-detects OS and architecture, resolves the release tag, verifies the
    SHA-256 against the published `checksums.txt` (and the cosign signature when
    `cosign` is on `PATH`), and installs into `$HOME/.local/bin`.

    ```sh
    curl -fsSL https://matcra587.github.io/jira-cli/install.sh | sh
    ```

    Pin a version with the tag: `… | sh -s v0.5.9`. Native Windows isn't covered
    (the installer is POSIX-sh). Use the **Scoop** or **mise** tab, or run it
    under [WSL](https://learn.microsoft.com/windows/wsl/).

    ??? info "Environment overrides"
        The positional version argument is the common case; everything else is
        controlled by environment variables.

        | Variable | Effect |
        |---|---|
        | `JIRA_CLI_VERSION` | Pin a release tag (same effect as the positional argument; the positional argument wins when both are set). |
        | `JIRA_CLI_INSTALL_DIR` | Install directory. Default: `$HOME/.local/bin`. Use `/usr/local/bin` for a system-wide install. |
        | `JIRA_CLI_NO_VERIFY` | Set to `1` to skip cosign verification when cosign is unavailable. SHA-256 still runs. |

        Example, system-wide install:

        ```sh
        curl -fsSL https://matcra587.github.io/jira-cli/install.sh | JIRA_CLI_INSTALL_DIR=/usr/local/bin sudo -E sh
        ```

    ??? warning "What `curl | sh` actually runs"
        The installer's source lives at [`docs/install.sh`](https://github.com/matcra587/jira-cli/blob/main/docs/install.sh), about 170 lines of POSIX sh. Inspect it before piping into `sh` if you don't already trust the source:

        ```sh
        curl -fsSL https://matcra587.github.io/jira-cli/install.sh | less
        ```

=== "go install"

    Any Go platform supported by jira's dependencies, including Intel macOS.

    ```sh
    go install github.com/matcra587/jira-cli/cmd/jira@latest
    ```

    Installs the latest tagged release into `$(go env GOBIN)` (or
    `$(go env GOPATH)/bin`).

    ??? warning "Version metadata"
        Release builds inject the version through a compile-time `-X` ldflag
        that `go install` does not supply, so this path falls back to what Go
        records in the binary's build info. For the exact release string, use
        Homebrew, mise, or the pre-built binaries.

=== "Pre-built binaries"

    GitHub Releases ship checksummed archives for Linux (amd64/arm64) and macOS
    arm64 as `.tar.gz`, and Windows (amd64/arm64) as `.zip`. Reach for these for
    a scripted pipeline, a stricter audit trail, or to avoid piping `curl` into
    `sh`.

    The [GitHub CLI](https://cli.github.com) resolves the latest release and
    fetches the archive plus checksums (Linux amd64 shown):

    ```sh
    gh release download --repo matcra587/jira-cli \
        --pattern 'jira_*_linux_amd64.tar.gz' \
        --pattern 'checksums.txt'
    sha256sum -c checksums.txt --ignore-missing
    tar xzf jira_*_linux_amd64.tar.gz
    sudo install jira /usr/local/bin/
    ```

    Swap the arch and OS in `--pattern` for your platform (Windows ships a
    `.zip`). To check the signature too, add `--pattern
    'checksums.txt.sigstore.json'` and follow the **Cosign signature
    verification** note.

    ??? note "Without the GitHub CLI (curl + SHA-256)"

        The archives follow `jira_<version>_<os>_<arch>.tar.gz` (Windows uses
        `.zip`). Resolve the latest tag, download, and verify by hand:

        === "bash · zsh"

            ```bash
            VERSION=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
                https://github.com/matcra587/jira-cli/releases/latest | sed 's#.*/tag/##')
            OS=$(uname -s | tr '[:upper:]' '[:lower:]')
            ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

            curl -fsSLO "https://github.com/matcra587/jira-cli/releases/download/${VERSION}/jira_${VERSION#v}_${OS}_${ARCH}.tar.gz"
            curl -fsSLO "https://github.com/matcra587/jira-cli/releases/download/${VERSION}/checksums.txt"
            grep "_${OS}_${ARCH}.tar.gz$" checksums.txt | sha256sum -c

            tar xzf "jira_${VERSION#v}_${OS}_${ARCH}.tar.gz"
            sudo install jira /usr/local/bin/
            ```

        === "fish"

            ```fish
            set VERSION (curl -fsSLI -o /dev/null -w '%{url_effective}' \
                https://github.com/matcra587/jira-cli/releases/latest | string replace -r '.*/tag/' '')
            set OS (uname -s | string lower)
            set ARCH (uname -m | string replace x86_64 amd64 | string replace aarch64 arm64)
            set NUM (string replace -r '^v' '' $VERSION)
            set TARBALL (printf 'jira_%s_%s_%s.tar.gz' $NUM $OS $ARCH)

            curl -fsSLO "https://github.com/matcra587/jira-cli/releases/download/$VERSION/$TARBALL"
            curl -fsSLO "https://github.com/matcra587/jira-cli/releases/download/$VERSION/checksums.txt"
            grep (printf '_%s_%s.tar.gz$' $OS $ARCH) checksums.txt | sha256sum -c

            tar xzf $TARBALL
            sudo install jira /usr/local/bin/
            ```

    ??? note "Cosign signature verification"

        `checksums.txt` is signed with [cosign](https://github.com/sigstore/cosign)
        keyless signing; a `checksums.txt.sigstore.json` bundle ships next to the
        release artifacts. The one-line installer verifies it automatically when
        `cosign` is on `PATH`. To verify by hand:

        === "bash · zsh"

            ```bash
            curl -fsSLO "https://github.com/matcra587/jira-cli/releases/download/${VERSION}/checksums.txt.sigstore.json"
            cosign verify-blob \
                --bundle checksums.txt.sigstore.json \
                --certificate-identity-regexp "https://github.com/matcra587/jira-cli/" \
                --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
                checksums.txt
            ```

        === "fish"

            ```fish
            curl -fsSLO "https://github.com/matcra587/jira-cli/releases/download/$VERSION/checksums.txt.sigstore.json"
            cosign verify-blob \
                --bundle checksums.txt.sigstore.json \
                --certificate-identity-regexp "https://github.com/matcra587/jira-cli/" \
                --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
                checksums.txt
            ```

        The archives themselves are not individually signed; the checksum bundle
        covers them transitively.

## Verify the install

```sh
jira version                 # the bare version string
jira version --output=json   # the same, plus build provenance
```

The human form is one line: the version. The machine modes carry the rest —
`commit`, `dirty`, and `build_date` where the binary was built with VCS
metadata, plus the Go toolchain and target platform. See
[Output & exit codes](output.md#per-command-output) for the exact shape.

If `jira: command not found`, the binary isn't on `PATH`. Homebrew and mise
handle this for you; the one-line installer prints a PATH-fix hint; for
`go install` make sure `$(go env GOBIN)` (or `$(go env GOPATH)/bin`) is on your
`PATH`.

## Update jira

There is no self-update command. Update through whichever channel installed
the binary:

=== "Homebrew"

    ```sh
    brew upgrade jira
    ```

=== "Scoop"

    ```powershell
    scoop update jira
    ```

=== "mise"

    ```sh
    mise up github:matcra587/jira-cli
    ```

=== "One-line installer"

    Re-run the installer command:

    ```sh
    curl -fsSL https://matcra587.github.io/jira-cli/install.sh | sh
    ```

=== "go install"

    ```sh
    go install github.com/matcra587/jira-cli/cmd/jira@latest
    ```

=== "Pre-built binaries"

    Re-download the latest archive with `--clobber` to overwrite the existing
    files (Linux amd64 shown), then reinstall over the binary on your `PATH`:

    ```sh
    gh release download --repo matcra587/jira-cli --clobber \
        --pattern 'jira_*_linux_amd64.tar.gz'
    tar xzf jira_*_linux_amd64.tar.gz
    sudo install jira /usr/local/bin/
    ```

## Uninstall jira

The binary is all there is to remove: on this branch `jira` stores no
credentials and writes no config or cache.

=== "Homebrew"

    ```sh
    brew uninstall jira
    ```

=== "Scoop"

    ```powershell
    scoop uninstall matcra587/jira
    ```

=== "mise"

    ```sh
    mise uninstall github:matcra587/jira-cli
    mise use -g --remove github:matcra587/jira-cli
    ```

=== "One-line installer"

    ```sh
    rm -f "$HOME/.local/bin/jira"  # (1)!
    ```

    1.  Installed to a custom `JIRA_CLI_INSTALL_DIR`? Remove it there instead,
        e.g. `sudo rm -f /usr/local/bin/jira`.

=== "go install"

    ```sh
    rm -f "$(go env GOBIN)/jira" 2>/dev/null \
      || rm -f "$(go env GOPATH)/bin/jira"
    ```

=== "Pre-built binaries"

    ```sh
    sudo rm -f /usr/local/bin/jira  # (1)!
    ```

    1.  Installed somewhere other than `/usr/local/bin`? Remove it from there
        instead, e.g. `rm -f "$HOME/.local/bin/jira"`.

??? note "Left over from an earlier release?"
    Releases before the rebuild stored credentials in the OS keyring or a
    1Password vault, and wrote config under
    `${XDG_CONFIG_HOME:-$HOME/.config}/jira-cli` (on Windows,
    `%AppData%\jira-cli`) with a cache under
    `${XDG_CACHE_HOME:-$HOME/.cache}/jira-cli` (`%LocalAppData%\cache\jira-cli`).
    Uninstalling the binary leaves those behind — delete the directories, and
    remove the keyring or 1Password item through the OS keychain app or
    `op item delete`.
