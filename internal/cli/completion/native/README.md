# Native clib generator backports

This package carries reviewed Bash, PowerShell and Zsh generator fixes from clib v0.7.17
through its supported `complete.RegisterShell` extension boundary. It preserves
the public clib `Generator`, flag/argument metadata, validation, script printing,
installation, and other shell generators. No emitted script is rewritten.

The fixes retain contextual callbacks for runnable Zsh parents and preserve
dynamic values while keeping display descriptions out of inserted Bash values.
Static Bash choices, including comma lists, also preserve spaces, quotes and
glob characters; literal values are prefix-matched without shell word re-expansion.
File and extension suggestions retain literal paths, and completion temporaries
do not change caller variables. Comma completion gates Bash 4 builtins by version.
The upstream implementation is retained because the published pin does not
implement those behaviors correctly. A module `replace` would break the supported
versioned `go install` route; this in-module backport preserves that route.

Provenance:

*   Upstream: <https://github.com/gechr/clib/tree/v0.7.17/complete>.
*   `generate_bash.go`, `generate_pwsh.go` and `generate_zsh.go` retain the upstream implementations
    with repaired callback state and dynamic value/description handling, under
    the `native` package declaration.
*   `helpers.go` contains only their private dependency closure from upstream
    `complete.go`. Public model, sorting and validation remain library-owned.
*   The adjacent `LICENSE` retains upstream's MIT terms and copyright.

Registration occurs once before the composition root exposes any generator;
there is no package initializer or per-invocation registry mutation. Do not
register additional generators at runtime while another program can print one.

Keep this package narrow. Remove a backport only after a published dependency
passes the same real-shell guards and preserves contextual and insertion behavior.
Do not import unfinished or rejected work from an experimental source checkout.

The execution guards accept `JIRA_TEST_BASH_BINARY` to select another installed
Bash version. Running Bash 3.2 on Linux checks shell compatibility; it does not
replace native macOS acceptance.

PowerShell reads completed literal AST values without evaluating expressions,
classifies valued flags in their current command scope, and excludes the current
word and future tokens. Argument arrays remain flat. Inserted parameter values
are quoted while descriptions remain tooltips; prefix matching is literal.
Inline long and short flag values retain their authored spelling. CSV suggestions
retain field quoting and already-selected identities. PowerShell replaces only
the final member of a bare comma argument; members needing quoting are offered
when the whole CSV argument is quoted, so completion never inserts broken argv.
Bare short inline CSV, such as `-f=one,two`, is a PowerShell parse error;
quote its whole value (`-f='one,two'`) or use the long flag.
Forwarding consumes every valued flag in its command scope. The completer honors
`--` and restores the caller's native exit status without adding errors.
The guards inspect TabExpansion2's actual replacement and execute independently
validated literal arguments through a controlled native fixture.

Set `JIRA_TEST_PWSH_BINARY` to select an installed PowerShell 7 executable.
An explicitly requested missing or non-PowerShell executable fails the guard.
Linux PowerShell execution checks shell behavior; native Windows runs separately.
