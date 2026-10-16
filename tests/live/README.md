# Live acceptance

These opt-in tests run the rebuilt binary with the existing OS keyring. They
write to a dedicated disposable project. The configured site must match the
explicit expected site before any write; environment tokens are disabled.
After preflight, the test's child processes explicitly allow writes. The
read-only refusal scenario overrides that setting for its own invocation. No
persistent configuration is changed.

Build the binary, then provide the private fixture settings locally:

```sh
mise exec -- go build -o /tmp/jira-live-binary ./cmd/jira
export JIRA_LIVE_ACCEPTANCE=1
export JIRA_LIVE_BINARY=/tmp/jira-live-binary
export JIRA_LIVE_BASE_URL=https://example.atlassian.net
export JIRA_LIVE_PROJECT=TEST
export JIRA_LIVE_ISSUE_TYPE=Task
export JIRA_LIVE_READ_KEY=TEST-1
export JIRA_LIVE_BOARD_ID=17
export JIRA_LIVE_BOARD_NAME='Acceptance board'
mise exec -- go test -v -tags=live -count=1 -timeout=15m ./tests/live
```

The read fixture is only read. Every mutation target must belong to this run's
ownership manifest; partial failure uses an issue created and deleted by the
same run. Selectors and arbitrary issue payloads are refused by the harness until
their indirect target ownership can be verified. Native comment bodies are allowed
only for owned issues and only from ADF files authored, validated and registered
by this run. The status-color test checks both named and six-digit hex colors
in posted responses and independently listed stored comments. Writes are never
replayed.

ADF inspection acceptance posts one independently authored native rich comment on
an owned issue, reads its stored body through the raw API, and runs local
`adf validate`, `adf lint` and `adf explain-node` against that body. Schema
validity, projection warnings, context bounds and source/remote preservation
are checked independently. This does not establish lossless editing or remote
concurrency protection.

Issue deletion acceptance creates a parent, a metadata-selected subtask, a
sibling and a missing-target fixture in the dedicated project. Cascade requests
require an independent complete native observation matching registered owned
children. Previews, read-only refusal, missing confirmation and Jira's ordinary
parent refusal preserve the fixtures. Explicit cascade and partial/total batches
compare typed ordered acknowledgements and failures; independent native reads
confirm each deleted issue absent and the sibling preserved until its own delete.
Interruption, unusual success statuses and output failures use hermetic tests.

Watcher acceptance binds the authenticated current account through a read-only
native identity request, then changes only its subscriptions on two fresh owned
issues. The guard requires active explicit targets, that account or `@me`, and
authored options; foreign accounts, selectors and indirect targets are refused.
Independent raw reads compare identities, count and current-account state with
domain lists and default readbacks. Local and remotely validated previews and
read-only refusal preserve subscriptions. Repeated add/remove operations retain
the same stored set; `--no-readback` omits observed state. Parent cleanup deletes
both issues. Failure/cancellation and failed-readback receipts use hermetic fault
cases rather than replaying writes against Jira.

Comment lifecycle acceptance edits and deletes a registered comment on a fresh
owned issue. Native edit inputs are bound to the comment, parent and unchanged
authored bytes. Read-only current-account/project-role metadata selects a role
the server confirms the account belongs to; no role or membership is changed.
Independent raw reads and complete domain lists verify different native bodies,
setting a role restriction, preserving it when omitted, and clearing it.
Previews, read-only writes and missing deletion confirmation preserve storage.
Explicit deletion is confirmed absent before parent cleanup. Malformed responses,
interruption and uncertain write outcomes use hermetic guards, not live replay.

Comment read acceptance creates three registered comments on one fresh owned
issue. Independent raw and domain requests compare native page windows, oldest
and newest order, default order, complete walks, beyond-end empty pages, native
body bytes, authorship and timestamps. Explicit middle-comment deletion preserves
the siblings; each deletion is confirmed absent before the empty thread and
parent cleanup are checked. All comparison reads run with read-only enforcement.
Failed pages, malformed pagination, repeated identities and joined interruption
receipts are exercised through hermetic binary and owner guards.

HTTP precondition discovery registers a description-only native PUT payload and
one exact header flag/value alongside the owned route and payload digest. It
creates a controlled intervening update before attempting an older body with
`--if-match` or `--if-unmodified-since`, on both issue descriptions and comments.
Independent domain reads distinguish HTTP 412 refusal from a successful stale
replacement. Observations are recorded privately; a passing discovery test does
not establish optimistic concurrency. The tested Cloud endpoints applied all
four stale replacements despite the requested preconditions and exposed neither
ETag nor Last-Modified in source receipts. The editor's stale-write requirement
remains unresolved.

Anchored ADF editing acceptance reads a newly authored native comment from Jira,
then applies local Markdown insertion, deletion and reordering while retaining
a protected panel. A second comment stores the candidate; an independent list
compares its complete structure and confirms the source comment stayed unchanged.
A separate Markdown comment exercises link-title conversion through the rebuilt
binary and checks the stored title. This covers the local editing primitives and
native storage, not an issue editor or remote optimistic concurrency.

Markdown alert acceptance authors all five GitHub alert kinds through the
rebuilt comment command. An independently authored native document pins panel
types and bold body formatting. Raw comment reads and a complete domain list
verify stored structure; only the run's disposable issue and comment are changed.

Three-way merge acceptance updates that run's owned native comment between the
source snapshot and the merge. The current panel's native identity proves its
correspondence after its attributes and content change. A conflicting local
paragraph edit is refused; disjoint local structural changes retain the current
rich subtree and paragraph edit. An independently authored expected document
pins the candidate and the final stored body. These controlled raw PUTs are
unconditional and do not prove stale-write rejection or issue-editor acceptance.

Native issue acceptance edits properties on one newly created owned issue,
including a property-only request and one with explicit empty `fields` and
`update` objects. Preview and read-only refusal leave each property absent.
The site's issue endpoint refuses the request without either container; its
typed failure retains the unchanged request, and the property stays absent.
Independent API reads check stored null, empty-array, false, zero and markup
values after submitting the request with containers. The issue-input guard admits
only fixed authoring workflows, an exact edit command, one active owned target and a registered byte
digest. Altered files, alternate targets, selectors and extra options remain
refused. Deleting the parent fixture also removes its properties. This does not
establish server acceptance for arbitrary future members or history metadata.

Epic acceptance rechecks available create-metadata issue types, then creates an
owned Epic and child. A registered native parent input binds both active owned
identities and the exact input bytes; deleting either identity invalidates it.
An independent issue read verifies the stored parent and status. Recent edits
can precede enhanced-search index visibility, so two consecutive matching raw
and domain reads establish visibility before comparing aggregate snapshots.
Only reads are repeated. Listing and board counts are then compared with
independent raw searches, including the owned child and tenant status names.
Both fixtures are deleted and absence is confirmed during cleanup.

The read-only search test checks bounded results, cursor resume to a distinct
issue, and a complete `--all` walk with accurate counts and no duplicate or
foreign keys. It creates no resources and requires the designated read fixture
to appear in the complete result.

Saved-query acceptance writes only local .jql files under its evidence directory.
An independent complete native search establishes project issue order and
summaries before comparing plain, YAML and TOML local queries. It checks metadata
without an implicit project constraint, bounded cursor resume and a missing local
name. Every Jira request remains read-only; no Jira resources are created.

Raw API acceptance reads the designated issue through the platform API, the
configured board through the Agile API, and the same issue through a native
search POST. These invocations explicitly enable read-only mode and create no
resources.

Alias acceptance creates and imports a forward chain in a private local config
file, with credentials disabled during management. A preview preserves exact
file bytes. The chain then reads the designated existing issue through `issue
view` with read-only mode enabled and compares identity, summary and status
against an independent native API read. Local deletion acknowledges its file
update. This workflow creates no Jira issues, comments or jobs; hermetic binary
tests separately exercise quoting, cycles, mutation safeguards and cancellation.

Account discovery reads the authenticated account's visible display name, then
compares `user search` with independent raw user-search pages. It checks a complete
window walk, one-row offsets, account identities and nullable email visibility.
Every request stays read-only; no users or issues are created. The live account
must have enough visibility to find itself. Hermetic tests separately prove empty
permission-limited results, excluded rows, protocol failures and the 1,000-match
ceiling; this live query does not establish directory-wide coverage.

Board acceptance compares typed adapter reads with independent raw API reads
through the selected binary. It drains visible boards and projects associated
with the configured board, then checks one-row pages at offsets zero and one.
Every request stays read-only, and the designated board/project must be visible.
The typed adapter uses the preflighted keyring and checks the configured host
before sending credentials. This proves the paging boundary, not a public
`boards list` command or its cache policy. Hermetic tests additionally exercise
absent/null totals, malformed pages, duplicate identities and transport failures.

Public board listing separately compares a refreshed whole snapshot against
independent raw board and associated-project drains. It checks cached reuse and
a one-row display window, with explicit completeness and failure metadata.
The configured board/project relation must appear. Requests stay read-only;
the command's derived metadata cache is refreshed without changing Jira.

Field-type discovery acceptance executes the local catalog, then reads its
published field-list, edit-metadata and paginated create-metadata routes using
the designated read fixture's current project and issue type. These requests
stay read-only. Illustrative values are not submitted to arbitrary custom fields;
transport tests separately prove native-value preservation through composition
and writes to a hermetic server.

Raw mutation acceptance creates an owned issue, then adds, updates and deletes
one native ADF comment using POST, PUT and DELETE. Independent domain reads
check the stored document, comment identity and absence; a raw not-found read
must retain its HTTP failure receipt. Preview and forced read-only refusal
leave comments unchanged. These routes follow the pinned platform spec and the
[Jira Cloud comment reference](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-comments/).

The issue-link lifecycle compares native site types with domain discovery,
creates concurrent forward relationships with native ADF comments and checks
an inward sentence against both stored endpoint identities. Complete native
issue-link arrays and domain lists must agree in count, ID, far issue, type and
direction. Compound comments are compared independently across the complete
owned threads. Local/resolved previews and read-only refusals preserve state.
Each confirmed link ID is registered with both active owned ends before
explicit deletion. The grammar also binds unchanged native payload bytes,
verified relationship phrasings and authored options. Deletion must disappear
from its native resource and both issue lists; parent cleanup independently
closes any remaining registered link resources.

The web-link lifecycle uses two fresh owned issues and compares complete native
lists and individual resources with domain results. It verifies concurrent
creation, global-ID replacement preserving identity and clearing omitted
fields, intentional duplicates, URL-default titles, local previews, read-only
refusal and exact deletions preserving siblings. The guard binds registered
recipes to explicit active owned parents and observed issue-scoped link IDs.
Each resource must return not-found before its deletion is marked complete;
fresh-context parent cleanup also verifies remaining registered resources.
Malformed responses and interrupted writes use hermetic fault guards.

Ranking acceptance reads the configured board's filter and rank field, then
creates three fresh owned issues. Each move binds the exact source order,
direction and active owned anchor to a registered recipe. Local previews and
read-only refusal preserve the initial order. Before and after moves are
verified through two consecutive matching complete native and domain searches,
including native issue identities. Only reads repeat while indexing converges;
ranking writes are never replayed. Foreign or deleted targets, changed recipes,
selectors and extra options are refused by hermetic ownership guards. Native
partial failures, malformed receipts and interruption use hermetic fault cases.

Paired rank acceptance covers both placement directions with one source and
with 51 sources plus a distinct owned anchor. Every creation has a unique
ordinal summary and retained request file. Each lane starts from the same
observed relative order, changes it, then restores it before the next lane.
Controlled-server comparisons separately execute 51/100/101-source requests
through both frozen binaries and validate exact authentication, native batch
boundaries, ordered source identities and chained anchors. Local JSON/compact
previews construct no remote requests. The harness ceiling of 101 sources is
independent of the native endpoint limit of 50 per request.

The raw-write guard admits exact owned comment routes and separately registered
description-only issue PUT probes, with explicit force and one input authored
by the run. Its method, endpoint and byte digest are checked before invoking
the binary. Conditional probes also bind one exact registered precondition
flag and value. Extra options, arbitrary issue payloads, indirect targets and
unregistered comment IDs remain refused. Confirmed comment IDs and
removal state persist under their parent issue in the ownership manifest;
fresh-context issue cleanup also closes descendant resources after confirming
the parent's absence.

Each run creates a distinct persistent directory under the OS temporary
directory, or under `JIRA_LIVE_EVIDENCE_DIR` when set. Its manifest records create
attempt markers, confirmed identities, binary digest and cleanup progress.
The selected binary must expose the rebuilt machine contract. Raw stdout and
stderr remain there, including failed or ambiguous receipts. These files may
contain tenant data: keep them local and out of commits or external reviews.

Cleanup uses a fresh bounded context because Go cancels the test context before
cleanup functions run. It deletes only confirmed issues from the current run
and verifies their absence with a not-found read.
An ambiguous creation without a confirmed identity remains recorded for manual
inspection; the harness never guesses its key or repeats the creation.

Without the live build tag, this package is excluded. With the tag but without
the explicit opt-in, only hermetic ownership and cleanup guards run. CI uses
`mise run test:live:guards`, which explicitly disables opt-in even when the
parent environment enables it. Skipped live tests do not establish acceptance.

Transition discovery compares the complete native metadata with an independent
raw API read, then proves both offline target callbacks from an isolated cache.
Expected IDs come from an independent native-response projection. Credential
storage is disabled and HTTP proxies are unreachable during callbacks; this
checks that completion uses the observation without contacting Jira.

Requested-field verification acceptance creates one fresh owned issue and checks
its project/type/summary/ownership label readback. It then adds and removes only
one synthetic label while preserving the ownership label, verifying the summary
and final label membership. Independent raw native reads confirm the observed
state. Malformed/unavailable readbacks and ignored changes use hermetic fixtures;
no live write is replayed to plant those faults. This is a post-write observation,
not a server-enforced precondition or attribution of a later difference.

`TestLiveIssueFieldPreflight` checks complete create and issue-bound edit metadata
with the normal CLI. A read-only create preview and its owned live creation keep
the same native payload; an admitted edit retains its checked field IDs. An
unavailable field refuses on the owned issue, followed by an independent native
state read. This tests screen/operation admission, not value validation, future
permissions or server-enforced concurrency protection.
