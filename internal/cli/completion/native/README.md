# Native clib generator backports

This package carries Bash, PowerShell, Zsh, Fish and Elvish generator fixes from clib v0.7.17
through its supported `complete.RegisterShell` extension boundary. It preserves
the public clib `Generator`, flag/argument metadata, validation, script printing,
installation, and other shell generators. No emitted script is rewritten.

The selected clib model omits the current argument from dynamic callbacks.
Predictor names beginning with `word_` explicitly request the shared literal
`--@word=` transport, followed by public forwarded flags and a separate completed
positional terminator. These callbacks match complete arguments themselves.
Ordinary callbacks retain the published protocol. Fish and Elvish delegate to
the published generators when a model declares no current-word predictor.

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
*   `generate_fish.go` and `generate_elvish.go` retain the same pin with current-word
    transport and native replacement matching. Their upstream SHA-256 values are
    `779af1f8a0a8ee93adf077e549800407a5a9b849ae68069bb4e9abd0a7101750`
    and `c27cd640a8e94e71cedf319b5f4518346720e41757f2c4ee8bd35aff0b20d5ee` respectively.
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

Current-word Bash replies use `printf %q` and disable Readline's automatic
quoting where supported. Automatic filename quoting can choose double quotes
without protecting expansion text when replacing a closed single-quoted CSV
argument. Bash 3.2 lacks `compopt`: its registered wrapper quotes complete replies
and supplies normal spacing explicitly, leaving directories open for the next
path segment. Inline values respect native `COMP_WORDBREAKS`; completed context
tokens are decoded independently without expansion. Directory hints enumerate
names explicitly on that version. Empty contextual callbacks do not fall back
to unrelated files.
Fish CSV replies quote every current field: native ranking otherwise suppresses
quoted comma-bearing fields when unquoted names have a better match rank.
Elvish wraps the selected argument matcher without changing its input batch;
extra admission requires the exact current command, seed and root-matched value.
The wrapper preserves ordinary matching, including other commands. Native
completion guards observe editor insertion and execute literal argv through an
owned observer; callback or `complete -C` output alone is not insertion evidence.
Native command resolution skips known public option values and retains hidden
boolean aliases without advertising them. Directory suggestions suppress an
extra space in the argument. Current-word transport refuses tabs, controls and
invalid UTF-8 across the entire argument, including completed CSV fields.

The current-word extension covers Bash, Fish, Zsh, PowerShell and Elvish. Nushell
retains the published generator and is outside this extension's acceptance scope.

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
Models with dynamic callbacks borrow UTF-8 output decoding for the duration of
the completer and restore the caller's encoding, including custom fallbacks,
through failure and exception paths. This prevents OEM console code pages from
changing Unicode candidate identities or breaking continued directory paths.
The native invocation remains PowerShell-owned; no profile setting is persisted.
The process-shared Console type coordinates encoding scopes across applications
and runspaces. Bounded admission waits retain PowerShell pipeline cancellation;
restoration and lock release run through separate cleanup boundaries.
The guards inspect TabExpansion2's actual replacement and execute independently
validated literal arguments through a controlled native fixture.

Set `JIRA_TEST_PWSH_BINARY` to select an installed PowerShell 7 executable.
An explicitly requested missing or non-PowerShell executable fails the guard.
Linux PowerShell execution checks shell behavior; native Windows runs separately.

CSV directory choices retain an unfinished current field so a second Tab can
complete a leaf in the same argument. Theme choices preserve literal symlink/..
traversal when made absolute and validate the final transport bytes, including
the current directory. Verify these identities in native editors and named guards.

Declare CSV flags with the native comma metadata as well as their current-word
predictor. Fish refuses an ambiguous unquoted separate value that looks like its
active inline option (for example, `--file --file=name`). Quote the whole CSV
field, `--file '"--file=name"'`, to complete that literal filename safely.

PowerShell directory insertion consumes an explicit `--@directory` hint in the
current-word callback description slot. Ordinary cached values ending in a slash
remain complete literal arguments. Keep the hint private to the generator and
verify both directory continuation and slash-ending configuration values. Fish
current-word callbacks reuse the shared version-aware token helper.

Bash versions before 4.3 use the shared legacy spacing path because `noquote`
was added in 4.3. Fish 3 preserves inline option spelling explicitly when its
editor replaces a whole quoted CSV token. Native terminal guards bind Fish
function keys through the selected version API (`bind -k` before 4, named keys
from 4), and explicit shell selections fail if missing.

The Bash capability boundary follows the official
[4.3 release announcement](https://lists.gnu.org/archive/html/info-gnu/2014-02/msg00010.html).
Fish 3 uses the selected version-aware tokenization fallback; its 3.5
[commandline reference](https://fishshell.com/docs/3.5/cmds/commandline.html)
does not document expanded-token `-x`. The executed 3.7.0 probe rejects it.

The Bash legacy spacing wrapper clears filename/default completion options
after the selected callback and before manual quoting. Native file hints must
not quote already quoted replies a second time on Bash 4.0–4.2. Exercise ordinary
file and directory hints alongside the current-word callbacks.

Legacy Bash spacing callbacks enumerate directory hints before clearing native
filename options. Carry directory suffixes through file/extension hints only at
that wrapper boundary; ordinary model generators retain their native behavior.

Bash literal callback decoding normalizes the ANSI-C quoting emitted by
`printf %q` before the selected POSIX lexer. Preserve UTF-8 octets, refuse
unsupported escapes or malformed bytes, and never evaluate expansions. Decode
completed context with the same owner. Guard a Unicode directory with two Tabs
and native executed argv; a single completed basename cannot prove continuation.
