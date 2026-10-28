---
description: >
  Testing conventions: the suite map (package-adjacent + tests/*), the
  stdlib-testing house style, black-box contract tests against the built
  binary, goldens, guardrail tests, and the live suite.
paths:
  - "**/*_test.go"
---

# Tests

Sparse-response guards distinguish absent, null, empty and nonempty values through
the real binary in human, JSON and compact modes. Cover identity and issue headers,
default and explicit issue columns, embedded comments, watcher facts, links,
attachment sizes and create/edit verification metadata. Assert omitted keys and
rows, unavailable messages, and observed false/zero/empty controls. Missing
readback metadata must retain acknowledged writes and unverified-field warnings;
exact native names must win over differently capitalized members. Use synthetic
HTTP responses, check request counts and demonstrate named failures before fixes.

Near-limit warning guards exercise actual auth/issue commands in JSON, compact
and human modes on success and HTTP refusal. Verify true, false, absent and
malformed headers, exact request admission and explicit `lossy:false`. Cover
concurrent write deduplication, retained filter failures, discarded retry replies,
invocation isolation and refusal of credential-free or misbound observations.
Observe a named compiled guard fail before crediting the restored warning.

Live Jira Markdown comment admission permits only fixed independent add/edit documents.
Translate exact authored argv into the existing registered native-body admission;
verify both documents agree, preserve active target/resource and verified-role
bindings, and reject extra flags or indirect targets. File recipes are exact
direct children with regular leaves; lexical containment alone cannot exclude
ancestor symlinks. Exercise a compiled escape defect against synthetic files.
Persist intent and exact returned IDs, bind the native resource to its owned
parent, then register cleanup before behavioral assertions. Distinct edit content,
full inventories, unchanged inputs and unrelated issue fields remain mandatory.
Never replay an uncertain comment write or infer notification delivery from policy.

Comment convenience acceptance compares independently authored Markdown/native
bodies, literal group/role visibility and complete lane receipts through actual
writers. Preserve the explicit legacy bare-add mapping and compact null-member
omission. Current notification suppression must bind both exact native query and
reported request policy. Previews disable credentials, send nothing and preserve
all owned files; live cases may publish only their independently bound history.
Single-target parallelism admission does not prove fan-out or worker bounds.
Keep synthetic and live Jira evidence, failed writes and readback claims separate.

Paging flag acceptance binds exact authenticated request order and independent
native rows to complete per-operation output. User search advances by fetched rows
and requires an empty probe for completion; worklogs use native totals. Preserve
privacy, unknown native values and explicit false. Check first-invalid boundaries
and explicit-default conflicts with exact message, hint and field attribution;
a shared error code alone is insufficient. Refusals disable credentials and leave
HTTP/storage untouched. Successful keyed reads may publish only independently
bound scoped history. Keep controlled fixtures separate from live Jira and failure walks.
Native worklog paging acceptance starts from an acknowledged independently bound
record, then compares exact offset/size native pages and complete current output.
Confirm the stored record survives each read; an empty pre-existing issue alone
cannot prove offset selection. Single-record evidence is not a multipage walk.

Live clone flag acceptance binds immutable source preimages and frozen candidates
before entry, retains intent and returned identities, then registers cleanup from
minimal exact native ownership before asserting requested fields. A broken
assignee, description or extra label must not abandon a uniquely owned clone.
Retain exact lowercase identity/fields after strict validation; a permissive second
struct decode must not replace cleanup targets with mixed-case aliases. Require
complete native field readback, unchanged sources and inputs, full acknowledgements
and independent deletion confirmation. Never replay uncertain creation.

Clone field flags need native writer acceptance because bare clone previews
publish targets without copied fields or resolved candidates. Bind an exact
authenticated source read to independently authored POST fields, native unknown
values, nulls and server-field exclusions. Exercise scalar/collection overrides,
CSV identities and ordered summary replacements, then check full source/clone
receipts and scoped history without collateral file changes. Selector cases must
bind exact filter/search requests before source reads or writes. Remote previews
resolve targets only; bare previews keep unresolved warnings and enter no HTTP.
Both leave the complete owned tree unchanged. Keep controlled-server acceptance
separate from live Jira, concurrent fan-out and failed-write evidence.

Structured-query acceptance uses independently authored per-layout literals for
filters, CSV/repeated values, key ranges, account aliases, status categories and
date grammars. Exercise jql build and both issue list/mine local previews, binding
full JSON/compact data, clause ordering, URLs and the fixed mine account scope.
Disable tokens, trap HTTP and compare the complete owned tree. Positive relative
periods remain explicit differences when legacy emits invalid +2w and current
emits valid 2w: compare both diagnostics with independent live Jira native parsing and
do not credit equivalent query generation. Offline composition, Jira parsing,
live search and cached board scope require separate evidence.

Legacy auth mapping acceptance must execute the old operation, not infer its
meaning from help. Refresh is a local unsupported-flow report, switch publishes
the owned default profile, and migrate previews provider relocation without
opening stores. Compare exact current dispatch refusals separately from these
legacy outcomes; missing commands earn no equivalent-behavior credit. Use only
owned public profiles with disabled tokens, trap HTTP and verify authored profile
metadata plus unrelated files survive. This does not establish live provider
migration, a runtime profile feature or an equivalent flat-layout workflow.

Live convenience-edit cases bind an owned native identity before every write,
persist candidate/preimage intent and retain acknowledgements before assertions.
Use different inline/file descriptions and assignment/unassignment states so
untouched earlier values cannot corroborate an ignored flag. Require complete
bound project/type/label facts and a different admitted summary, description and
assignee before entry; default assignment is not assignment-change evidence.
Compare immutable-ID
native readback, explicit null, all selected untouched fields and complete lane
receipts. Stop dependent cases after failure and never replay uncertain edits.
Preserve source files and independently confirm cleanup; no server CAS or protected
editor application follows from an acknowledged write and later observation.

Convenience-edit previews compare complete independent native fields/update and
per-key results in both machine modes. Map legacy `--assignee=none` to current
`--unassign` explicitly. Retain legacy compact's null-member loss as a presentation
difference; current payloads keep explicit null. Collection flags add/remove and
preserve literal name meanings, CSV commas, duplicates and operation order. Whole
replacement belongs to native JSON input, not an invented flag. Preview tests
disable credentials, trap HTTP and compare the entire unchanged owned tree.
Notification facts report only the request. These tests do not credit a writer,
live Jira changes, remote validation, multi-key selection or all failure variants.

Live convenience-field creates persist a fixed recipe's intent before entry and
reuse the native create ownership binder before additional assertions. Independently
bind the authenticated account; compare project/type identities, summary, exact
label membership, assignee and the full description through an immutable-ID read.
Admit strict original JSON
and exact outer/inner native member names; duplicate or wrong-case fields cannot
earn readback credit. Compare complete legacy preview aliases and current native candidate/acknowledgement data. Keep submitted
label order separate from Jira's observed order, retain original Markdown files,
and stop subsequent cases after failure without replay. Parent hierarchies,
priority, additional current-only fields and remote preflight remain separate
requirements.

Convenience create writer comparisons use an owned authenticated HTTP fixture
separately from local previews. Admit exactly the legacy name-to-ID metadata walk
and one native POST, or the current single POST without implicit metadata reads.
Require the complete independent native body, JSON content type, literal identities,
CSV membership/order and the complete description, plus complete lane acknowledgements.
Capture bounded request bodies and exact ordered URIs before assertions without
credential headers or token values. Preserve unexpected/replayed attempts and
never retry a write. Keep authored Markdown unchanged; legacy metadata-cache
publication is separate from input preservation. A writer-only compiled defect
must fail the actual wire guard even when its preview remains correct. Synthetic
writer evidence does not establish live Jira acceptance or remote preflight.

Create convenience flags require direct selected-binary evidence; native JSON
creates do not cover the corresponding flags. Compare full independent legacy
preview aliases with current native fields and mutation state in JSON/compact.
Keep literal account IDs, parent keys, named priorities and digit-only component
or version names distinct from IDs. Exercise Markdown text and literal file paths
separately, and retain CSV commas, duplicates and order. Bare previews disable
credentials, trap HTTP and leave the entire owned public/input/cache tree unchanged.
Current additional field flags remain current-only. Pin the explicit empty
description_route when no description is authored. Preview evidence does not
establish the actual writer, Jira acceptance, remote preflight or broader failures.

Supplementary public-editor acceptance pins both the clean selected executable
and its independently observed live Jira source artifact. Copy each draft into an owned
case directory; retain the original source and readiness manifest unchanged.
Use independently authored Markdown and complete native candidates, including
protected content. Exercise actual JSON/compact editor success, unchanged saves,
failed exit, invalid text and both graceful and resistant cancellation. Observe
both borrowed terminal modes changed before releasing the editor, then require
restoration and joined termination. Bind literal argv and exact saved bytes;
retain complete typed recovery data and refusal reasons without a fabricated
candidate. Keep editor screens off result stdout, disable credential stores and
trap HTTP. Such evidence covers local candidates derived from a historical live Jira
read, not fresh remote application or a legacy editing counterpart.

Local update acceptance executes digest-verified disposable copies with renamed
basenames, preserving `.exe` on Windows. Execute their version commands before
interpreting update output. Managed mise/Scoop path routing binds both spellings,
preview/live/forced modes and complete independent receipts without HTTP or file
changes. Isolate PATH with owned installer observers, positively execute each with
literal argv and joined termination, then require no observer entry from update.
An unstamped current source build's unknown-install refusal is current-only;
the frozen official legacy release remains an archive install outside managed
paths. Never equate those different build provenances. Retain complete current
installation data, refusal and diagnostic text. Real installer ownership,
archive downloads/publication and native platform execution need separate evidence.

Supplementary archive-update comparisons use separately admitted archive-stamped
builds from the same clean selected current revision; never silently replace the
primary binary or transfer its digest credit. Use the fixed-host owned HTTPS
release fixture, anonymous requests, empty PATH and isolated GitHub configuration.
Opt in to the legacy release-list response explicitly; current discovery keeps
its latest endpoint. Require complete lane receipts/errors, exact request counts,
installed bytes/version, installation members and empty download stages. Persist
disposable copies and receipts in an unused private evidence directory, and
fingerprint selected artifacts before and after. Both original and replacement
version commands must execute. Preserve stripped legacy release versions and
its second discovery before installing. The legacy renamed-executable refusal
and current successful publication are an explicit improvement comparison, not
equivalent native outcomes. Linux evidence does not establish Windows/macOS,
real GitHub release delivery, installer ownership or interactive behavior.

Environment-login acceptance uses only synthetic credentials and owned public
configuration, with native credential stores disabled. Bind exact authenticated
requests, complete lane-specific publication/refusal receipts and complete saved
public configuration through independent TOML decoding. Legacy login also warms
its board cache: bind wrapper and native timestamps to the invocation interval,
respect native timestamp precision, and require a fresh publication on repeat.
Current preview changes no files; repeated live login acknowledges unchanged
settings. Verify saved routing and the named environment reference from a fresh
process with site/email overrides removed. Keep preview/fresh-process checks
current-only. Finite stdin refusal tests prove matching refusal and no HTTP/file
effects; legacy reads supplied input before refusing, so they do not establish
equal consumption order or a current pre-read guard. Real providers, interactive
login, scoped discovery and broader failure variants remain separate evidence.

Auth read comparison uses synthetic environment credentials in isolated public
fixture homes with keyring access disabled. Bind complete lane-specific status
data and exact authenticated empty GET transcripts. Legacy status also probes
permissions; current status verifies identity only. Do not erase that difference.
Legacy token diagnostics are redacted and framed; the current token command emits
literal secret bytes across output modes. Exercise it only with authored synthetic
values, require zero HTTP/file effects and never credit those distinct contracts
as an equivalent-behavior pair. Retain legacy compact null omission explicitly.

Selected Linux native authentication acceptance reuses the private D-Bus/keyring
owner with an explicitly admitted external binary and SHA-256. Execute its
version command before opening the private store and recheck the digest afterward.
Exercise status masking, explicit synthetic-token disclosure, raw token reads,
revoked credentials and malformed native identity in JSON, compact and human
modes. Bind complete independent identity/store facts, original failure classes,
unchanged public settings and independent native credential preimages. After
logout, fresh status, whoami and token processes must refuse without HTTP even
though cached public identity remains. Keep the Linux native keyring evidence
separate from live Jira identity reads, source-owner context guards and native SDK or
other-platform provider claims. Never use a real token for disclosure tests.

Stored-description acceptance binds original HTTP/base64 native bytes, timestamp,
immutable ID and public scope independently of prepared output. Pin complete
Markdown/region descriptors through native source SHA256 and an authored projection;
reopening through production preparation cannot serve as the expected result.
Verify all three regular draft files and independently bind manifest version,
source size/digest and initial Markdown digest before publication credit. Require
complete source/current snapshots, ready-state facts and complete authored local
and merged candidates. Preserve the entire draft tree across preview, refusal,
inspection, refresh and conflict. Bind exact terminal/conflict errors. Positively
execute and join the owned editor observer with literal argv before crediting
its separate absent entry marker; isolate profile/temp stores and retain PATHEXT.
Resume preview/refusal is not interactive editing
acceptance; local candidates and refreshes never establish protected Jira writes.

Current-only native bulk move acceptance records provisional task intent before
every invocation, including previews and read-only refusals. Clear it only after
complete original-JSON no-write admission. Bind required booleans, exact authored
payload/targets and ordered issue identities/outcomes; sparse zero-value decoding
is not receipt evidence. Retain new task IDs before assertions and reject reused
IDs without making their new intent settleable. Join known jobs before cleanup,
observe real type changes and unchanged native fields by immutable ID, then bind
plain/waited status to complete independent terminal progress and processed IDs.
These capabilities have no historical bulk counterpart and earn current-only
credit. An asynchronous acknowledgement never proves movement or remote CAS.

Paired historical field moves translate legacy spelling only for shared owned
admission; execute each lane's original argv. Preserve native bulk recipe
discrimination and restrict literal per-key parallelism to field writes. Bind
registered direct-child regular inputs and exact bytes before every process.
Keep native project/type metadata, immutable issue IDs, hierarchy and unrelated
fields independent of command receipts. Actual type conversion needs a changed
native type observation; an unchanged authored type cannot establish it. Persist
intent before PUTs, retain ordered acknowledgements and restore original native
summary/type preimages through fresh explicit recipes before the next lane.
Legacy single missing-force may read edit metadata before refusal; multi-key
missing-force and current refusal send nothing. Bind complete independent force
and read-only errors. Local previews, synchronous field acknowledgements and
native bulk tasks remain distinct; never invent an applied field or job receipt.

Paired epic membership acceptance admits every child and the destination through
the shared owned mutation grammar before entry. Create the Epic through its
separate metadata-bound setup; do not weaken the homogeneous paired-create type
binding. Persist per-child intent before native mutations, bind ordered lane
receipts and independently read exact issue/parent IDs and unrelated fields.
Require the same native unparented presence preimage before each lane and after
restoration. Preview/refusal steps retain exact parent presence. Legacy single
and multi-key receipts differ; current outcomes always remain keyed. Refusals
bind complete independent top-level and per-key errors. Synthetic transcripts
include every validation GET and PUT, exact query multiplicity and empty read
bodies. Keep legacy preview history effects distinct from zero HTTP. Cleanup
requires native absence, and uncertain writes are never replayed. Parent reads
and writes remain separate requests, without remote compare-and-swap.

Paired issue search acceptance uses independently decoded original native JSON
for exact IDs, keys, summaries, complete labels and assignee presence. Bind the
current account through raw native identity before assigning owned fixtures.
Only missing known index entries may wait; foreign identities, wrong fields or
assignments fail immediately. Pin explicit ordering separately from the legacy
descending and current mine ascending defaults. Keep legacy modeled null omission,
metadata and pagination distinct from current native fields and data pagination.
Each lane walks its own opaque cursors. Saved query files retain exact authored
bytes and descriptive project metadata must never become a filter. Legacy full
search maps to current detail with an explicit empty expansion override; ordinary
list detail uses its six native expansions. Read coverage does not establish all
flags, human output or malformed-response handling.

Paired clone acceptance binds one active owned source and the exact bytes of a
direct-child regular override file before every process. Retain creation intent
and returned identity before assertions, then bind independent native run,
summary, project and type markers before registering cleanup. Unknown outcomes
are never replayed. Native source fields must remain unchanged; preview/refusal
steps require two complete dedicated-project ID/key census observations,
independent of labels a faulty clone might omit. Baseline or unexpected observed
identities never grant cleanup ownership. Bind each fresh native source ID to
the original owned manifest; preserve that binding after every step. Clone
label overrides must differ from the source and match complete native membership. Compare legacy headless
confirmation separately from the current layout, and never claim unread copied
fields from a local preview. Actual copied rich ADF and acknowledgement need
independent native readback; every bound source and clone needs native absence
before cleanup is credited.

Paired rank acceptance binds ordered sources and a distinct anchor through
registered owned recipes before every process. Resolve the designated public
board filter and rank field, then require two complete independent native and
current-domain observations for each promised order. Preview/refusal steps retain
the earlier order; live steps must actually change it. Persist intent before
writes, never replay uncertain outcomes, and restore the same relative preimage
before comparing the next lane. Legacy preview completion-history writes must
stay scoped, ordered and capped; they are not a no-filesystem-effect path.
Both selected CLIs must preserve 51/100/101-source moves across exact native
50-issue request boundaries. The live ownership ceiling is 101 sources, not a
Jira endpoint limit; every source and anchor still requires active ownership and
an exact registered recipe. The 51-source live Jira comparison derives source order
from its observed preimage and restores it between lanes. Give every fixture
creation a unique ordinal summary and retained request file before entry.
Historical locally refused capabilities remain gap evidence for their original
binary; only fresh candidate proof can close them.

Paired issue-link acceptance translates only validated legacy bare-create and
key-plus-ID delete grammar through the shared owned-pair admission. Both ends
must remain distinct active fixtures; a global resource ID alone never grants
test ownership. Retain creation intent and every observed ID before assertions,
bind exact native direction and inverse endpoints, then verify deletion with
native absence on the resource and both issues. Preserve the legacy confirmation
requirement and current confirmation-free layout as an explicit difference.
Stop dependent writes after any failed step; never replay uncertain creation.

Paired attachment acceptance binds every upload to registered owned bytes and
one active native issue. Translate the historical key-plus-ID delete grammar
only after exact parent/resource validation. Persist upload intent and returned
IDs before assertions; independently bind native metadata before registration
and compare native content bytes before credit. Map file-target downloads and
legacy overwrite force into the new directory/name/overwrite layout explicitly.
Keep complete zero-byte receipts, collision preimages, exact native absence and
surviving sibling metadata. Bind explicit owned subprocess directories without
changing the test process directory, and reject collateral download-tree changes.
Compare legacy list totals/completeness with the current unpaginated collection.
A failed upload or delete is never replayed.

Paired web-link acceptance translates only the legacy bare add grammar into
its registered current owner for mutation admission. Preserve explicit targets,
recipes and worker bounds; re-admit before every process. Persist write intent
and returned/native identities before assertions, bind the unique native resource,
and never replay an uncertain add. New list/delete capabilities stay current-only,
even when they inspect a legacy-created owned fixture. Confirm exact deletion
with native absence and preserve unrelated issue fields. Legacy bare operation
attribution must not mistake the issue key for a subcommand.

Explicit help acceptance executes `help` against the independent non-config
command census and named aliases, then compares its exact selected short help
with native `-h`. Bind the canonical Usage path independently so two wrong
routes cannot corroborate each other. Keep credential-free HTTP/workspace guards
and all output modes. Unknown topics currently select their nearest existing
parent; record that limitation explicitly, never as a strict refusal or child
behavior acceptance. Config topics remain outside this acceptance scope.

Raw API acceptance binds all seven current-only methods to exact authenticated
wire requests and complete native receipts, including method aliases, literal
repeated queries, HTTP preconditions, stdin, absent/empty/opaque bodies and byte
bounds. Keep HTTP entry counts independent of attempted: a read-only refusal may
retain entered client/request data without sending HTTP. Preview and missing-force
paths send nothing; incomplete captures and HTTP failures retain receipts without
replaying writes. Test a truncated valid JSON prefix and prove its view stays
absent with an actually compiled defect, then restore source. Preserve permitted
headers and exact base64 bytes while excluding credential/cookie headers. Piped
human checks bind complete literal receipts and reject contradictory delivery
markers. Exercise valid UTF-8 controls separately from opaque binary fallback;
machine bytes remain exact while human text is sanitized.
Owned live Jira API checks re-admit registered bytes immediately before every mutation,
persist POST intent and returned identities before assertions, then bind native
comment inventory before dependent writes. Keep unsupported method receipts explicit.
Controlled-server behavior does not establish live Jira method support, server-enforced
preconditions, native host behavior or a legacy raw API counterpart.

Native ADF acceptance keeps new local commands distinct from legacy mappings.
Use independently authored anchors, native documents and schema pins; do not derive
expected candidates from preparation output. Retain exact original envelope data
before inspecting native bytes: a test-side default JSON encoder can escape HTML.
Bind full candidates, opaque numeric/escape spelling, protected-region refusals,
merge conflicts and named schema locations without credentials, HTTP or file
changes. Stored-source live Jira checks create one strictly owned current fixture,
independently read its native body, disable credentials and trap local commands,
then confirm input bytes and the Jira source remain unchanged. Their candidates
are local artifacts; they do not establish protected remote application or a
before/after counterpart. Human, malformed-input and broader edit variants need
separate evidence.

Paired completion acceptance fingerprints and executes each selected native shell.
Generate both admitted CLI scripts in JSON, compact and human modes with identical
bytes, disabled credentials, an HTTP trap and no CLI-owned file effects. Parse the
actual source and reject a deliberate syntax error; Elvish loads its script in
its real interactive edit namespace. Prime owned scoped history through the actual
open command, then bind generated dispatch to complete successful callback argv
receipts and literal cached candidates without HTTP or cache changes. Decode
PowerShell candidate identity through its native AST, retaining quoted token bytes.
The delivered legacy PowerShell generator nests its empty positional array and
suppresses the open callback; require native registration control and the exact
observed nested array before accepting that explicit baseline difference. Zsh
terminal startup, interruption and exit wait for separate observed prompts, and
all owned native processes are bounded and joined. These callback tests do not
credit editor insertion, native Windows/macOS behavior, all predictor families or
Nushell. Parent help/refusals bind both layouts, exact reasons, attribution and
current diagnostics; help alone never credits generated-script execution.

Refusal adapters pass through shared envelope admission requiring exactly one
structured error before inspecting its row. Named malformed-frame guards cover
absent, null, empty and non-array error collections across both lanes. Validate
review claims against the complete admission path before adding redundant code.

Paired open acceptance keeps normalized literal/padded/duplicate/single-range
keys and site path prefixes explicit. Machine JSON/compact receipts contain no
fetched ID or native fields and report opened=false. Trap all HTTP entry, disable
credentials and verify the producer's exact public scoped history wrapper and
key, publication time and owned file effects. Ambiguous target refusals publish
no history and retain their real reason/field. Live Jira URL acceptance separately
binds an actual native issue/project identity; URL construction alone does not
prove existence. Map the exact top-level open operation independently of its positional key.
Machine suppression does not credit browser callback execution,
process termination, GUI rendering, human output or native platform acceptance.

Release-note parity binds all embedded release fields/sections/changes and exact
raw human Markdown to pinned independent source fixtures. Exercise full history,
latest, explicit/prefixed/trimmed versions and aliases in actual JSON, compact
and piped human modes. Preserve legacy single-release trailing-newline behavior
explicitly. Local runs have no token, keyring, HTTP entry or owned-file changes.
Successful release.notes envelopes and release-notes root failures have distinct
identities. Bind version-conflict/missing-version reasons and typed current field
attribution, not merely exit codes. Piped/no-pager checks do not credit interactive
pagers, styled terminals or native platform acceptance.

Epic list/board acceptance uses independent native rows, child statuses and
literal lane-specific expected output. Bind exact authenticated search bodies,
page sizes, query quoting, continuation and request counts. Legacy list reads
one page; current default walking fills its total limit. Legacy child searches
select their historical default fields; current counts select status and drain.
Keep unknown-status warnings and child completeness explicit. JSON metadata and
legacy compact embedded pagination have distinct locations. Freeze the observed
legacy compact large-number rounding in its own fixture; never normalize current
numeric tokens to match it. Dynamic native oracles must use lossless numbers and
match literal fixtures. Live epic pages are complete and bounded to 50 records.
Compare legacy child
first-page counts separately from current bounded token walks. Child fixture
admission belongs to the board subtest and cannot suppress runnable list checks.
Larger epic walks, new flags, safety caps, failures, cancellation, human output
and membership writes require separate evidence.

Paired worklog creation uses a fixed minute-based recipe on fresh owned issues.
Freeze registered input bytes and admitted artifacts; persist intent before the
subprocess and target-bound receipts before any independent readback. Bind one
native worklog ID, seconds, exact instant and ADF comment without replay. Later
GET metadata is not an exact POST receipt oracle: complete native 201 values
and numeric tokens require literal controlled-server fixtures. Exercise zero
HTTP previews/refusals and exactly one POST for complete and missing-ID receipts.
Current read-only output retains policy and failed-target data; do not weaken
ordinary null-data refusal guards. Keep acknowledged incomplete receipts, warnings
and retry guidance distinct from unknown outcomes. Confirm owned parent cleanup;
this does not establish server-enforced conditional writes. Broader input forms,
batching, estimate adjustments, cancellation and human output need separate cases.

Paired worklog reads bind exact native record IDs, comments, absent/null values
and numeric tokens through independent literal fixtures. Keep legacy reduced
records and single-target metadata cursors explicit; current keyed records retain
complete native values and per-issue paging facts. Bind every requested issue to
one authenticated fixture request, including empty multi-issue cases. Native
oracles reject missing/null page facts and repeated identities. Non-progress,
walk/resume flags, failures, cancellation, history effects and human output need
separate scenarios; a sparse page does not establish a complete walk.

Board primer acceptance inspects the selected native disk payload and public
receipt separately. Legacy read time and wrapper publication time may differ;
current cached read time remains inside its native snapshot. Bind null/absent
type meanings, legacy self metadata, complete associated projects and exact
fixture ordering. Exercise the actual boards-list consumer after changed forced
priming with zero HTTP requests and unchanged files. Live membership normalization
must retain complete stored/output values, presence, ordering and numeric tokens.
Partial enrichment, collection caps,
previews, cancellation and human output need separate evidence.

Epic primer acceptance uses literal native, current-view and legacy summary
fixtures. Preserve absent/null values, unknown native fields and root details.
Bind enhanced-search POST bytes and token walks independently: legacy primes one
page, while current priming drains explicit continuation tokens. Validate stored
values and a subsequent zero-request reuse after changed forced collection.
Native search comparisons bind duplicate-free membership and exact stored/output
order. Collection caps, malformed/canceled pages and human output remain separate.

Batch refresh collection binds independent native values, exact authenticated
requests and owned disk snapshots before admitting row success. Exercise
changed forced data and zero-request cached reuse, including empty values and
repeated names. Preserve legacy history pollution as an explicit effect;
current batches must retain unrelated files. Standalone field primers reuse the
same live membership and stored-order owner. Jira may reorder equivalent field
rows between adjacent reads. Live comparisons bind complete independent
membership and exact persisted/output order; authored fixtures retain exact
order guards. Reject changed values and duplicate identities. Live Jira cached
receipts alone do not establish absence of HTTP entry. Partial failures,
cancellation, collection caps and every-resource batches require separate
evidence.

Native-backed history acceptance binds an independent Jira ID/key to the actual
view producer, then verifies its owned persisted scope and credential-disabled
local consumers. Ordinary issue view is flat in both lanes; keyed attachment
and watcher adapters must not define its shape. Compare only owned cache roots
for history reads and preview/clear/no-op effects; harness receipts must remain
free to change. Producer identity and history effects do not establish complete
view-field parity or server-enforced Jira write protection.

Batch refresh preview acceptance compares resource order and repeated selections
through actual binaries. Legacy keyed output records uppercased resource names
as issue history even on dry runs; retain that observed local write explicitly.
Current previews deduplicate first occurrences, publish typed plans and leave
the entire owned file tree unchanged. Run without an API token and trap all
HTTP entry. Live refresh, cached batches, partial failures and cancellation need
separate evidence; a preview does not credit collection or persistence.

Local cache acceptance uses owned literal storage fixtures and no API token.
Bind history order and malformed-state differences without reading Jira. Clear
previews and refusals preserve the full file tree; live acknowledgements match
exact removals and retain foreign scopes. Keep legacy removal of unrelated files
when clearing a whole scope separate from current classified-entry removal. Legacy preview
removed counts are plans, not acknowledgements. Bind refusal reasons as well as
codes. Empty inspection checks native null TTL and meaningful human receipts;
it does not establish populated inspection, concurrency or partial-failure parity.

Paired metadata primer acceptance uses independently authored native fixtures
and explicit legacy reduced projections. Bind authenticated endpoint, page-size,
offset and body differences; preserve current unknown fields and numeric tokens.
Validate owned disk wrappers and native snapshots before crediting saved output.
Bind legacy profile/schema/path independently. Fresh current timestamps follow
publication; cached timestamps equal the stored write time. Keep refreshed
expectations in independent raw buffers and guard immutable fixture preimages.
Changed forced reads must survive a subsequent zero-request cached invocation;
fresh reuse preserves owned files. Compare empty arrays explicitly. Current force
maps legacy refresh without pretending their cache-source state is identical.
Backdate only an independently verified owned wrapper: the same two-minute-old
snapshot must remain fresh under the resource default and refetch under an
explicit one-minute TTL. Bind the new publication and subsequent zero-request
reuse. Primer previews cover all ten resources with disabled tokens, exact typed
plans, custom/default TTLs and unchanged owned storage. Actual current paged
primers must stop at the page/item bound, retain the incomplete native snapshot,
reuse it without requests, then refetch all 101 pages under --unbounded and reuse
the complete publication. Compare exact cursors, warnings, wire order and disk
values. These controlled-server checks do not establish Jira collection size,
board/epic bound behavior, human rendering or failed-read parity.
Live acceptance reads native resources independently and changes only owned local
caches; keep that evidence distinct from synthetic freshness and bound fixtures.

Paired board discovery binds numeric board order, native type presence and
independently fetched associated projects; location metadata cannot supply the
relation. Exercise real sparse-page requests, local output windows, cache reuse
and refresh with owned directories. Cached reads preserve the original timestamp
and files and send no Jira request. Legacy compact pagination is an explicit
adapter, not an ordinary-envelope relaxation. Keep legacy negative-limit refusal
separate from the current documented nonpositive default. Independent bounded live Jira
walks establish membership; fixed fixtures pin natural project order. Fetch caps,
malformed pages, partial enrichment and human presentation need separate evidence.

Paired identity inspection binds `me` and ordinary `auth whoami` to independent
native account reads. Preserve complete current user metadata, exact numbers and
public site alongside the explicitly reduced legacy summaries. Malformed native
IDs must retain their lane-specific outcome; current refusal publishes no partial
account. Bind root `me` operation attribution independently of following flags.
Unchanged owned-file snapshots prove inspection did not save settings. Keep saving,
human presentation and credential-provider selection as separate acceptance.

Paired user discovery compares independently read native active/deleted rows and
privacy-preserved email presence. Keep legacy flattened strings separate from
current absent/null/value and fetched-row continuation metadata. A short page
does not prove directory completion. Exercise the literal query through argv and
an exact authenticated GET with an empty body. Human acceptance binds meaningful
empty receipts, visible names/identities and each lane's actual summary contract;
nonblank output alone supplies no acceptance. Live Jira positive fixtures must include
the independently admitted active, nondeleted account. Empty visibility proves
neither account absence nor assignment permission.

Paired export acceptance uses independent artifact paths, lengths and digests
from the admitted raw guide sources. Bind the complete prepared/published
receipt to observed files and ordered written paths. Separate owned home and
working directories so project and user scope cannot accidentally coincide.
Verify explicit format and harness precedence, detected harnesses, existing-file
refusal, acknowledged replacement, linked-target effects and unchanged previews.
Keep platform-specific permission checks in named subtests. Exported examples
remain content; local publication supplies no Jira workflow execution credit.

Paired guide acceptance freezes independently extracted source metadata and exact
content hashes from the admitted revisions. Execute every canonical runbook
through both guide doors and every output mode, then check index/all, all six
section selectors, resolution variants, aliases and refusals. Compare complete
current typed data, including omitted optional members; validate original refusal
JSON before decoding. Retain allowed failure stderr separately. Embedded workflow
examples are content, never execution or Jira acceptance evidence.

Local catalog acceptance pins field families and native ADF identities against
independent raw schemas. Compare legacy collapsed membership separately from
current context definitions and their bounds. Legacy human catalog output is a
JSON envelope; admit its array data only for the two exact catalog operations.
Keep ordinary object-envelope checks strict and test every output mode without
credentials or HTTP. Catalog capability declarations are metadata, not evidence
that authoring, rendering or writes work.

Paired discovery checks adapt legacy raw schema artifacts and current typed
wrappers explicitly. Freeze independently admitted source-registration path
sets; counts alone cannot detect
substituted commands. Bind single-token names and parent paths. Check every
declared version property type and required field, including platform members,
and resolve every marked main output shape against the extracted artifact when
testing `--shapes`. Never use production schema builders for expected values.

Pure navigation groups need actual bare/long-help/short-help execution in every
output mode. Bind the selected Usage command line and independently listed child
rows; examples or a shared path prefix cannot establish the selected command.
Keep navigation acceptance separate from runnable-child behavior and current-only
groups separate from paired counts. Synthetic processes use their owned home as
cwd, including project-scoped exports, and compare owned file content/mode
snapshots when no local publication is expected.

Paired parser acceptance prefixes owned runtime controls before caller argv so
`--` retains literal dash-prefixed queries. Verify exactly one validation query
parameter and a complete bounded POST object, retaining authored query order,
duplicates and text separately from Jira's normalized replies. Preserve current
native diagnostics, null/absent/empty arrays and full syntax trees. Legacy
read-only mode refuses this POST; current parsing is a read. Exercise this
difference separately from ordinary successful parsing, with independent live Jira
observations; parser success does not prove matching issues or write permission.

Paired JQL reference discovery executes the authenticated native GET separately
from local composition. Compare legacy string summaries explicitly and preserve
current optional members, full native metadata and exact numbers. Fixed fixtures
require exact array order; independent live Jira reads alone may tolerate reserved-word
and operator reordering. Current summaries must retain their own native order.
Keep synthetic remote checks separate from both zero-HTTP and live Jira counts.

Paired local JQL/ADF/version acceptance requires independent literal JQL, native
documents and Markdown. Validate original JSON for duplicate names before any
projection; retain exact numbers, stdin bytes and both process output streams.
Distinguish legacy compact omissions and explicit current diagnostics. Invalid
ADF roots and strict-loss policies have deliberate lane-specific outcomes;
current refusals must not leak a partial projection. Version expectations come
from the independently admitted artifact via
`JIRA_PARITY_BEFORE_VERSION_DATA` and `JIRA_PARITY_AFTER_VERSION_DATA`, never a
fresh invocation under test. JQL reference is remote discovery, not local build.

Paired local alias acceptance uses disposable per-lane public settings, disabled
credentials and a deny-all HTTP fixture. Preserve exact preview/refusal bytes,
literal alias values and unrelated authored settings. Every delete spelling
must remove an existing alias; test missing-name behavior separately. Validate
current path/change receipts and exact non-null import skip maps. Keep legacy
public preference representations separate from current flat settings. Local
paired checks do not count as live Jira execution.

Legacy targetless `issue transition KEY` is a read that maps to current
`issue transition list KEY`. Compare legacy ID/name/screen output explicitly;
the current lane must preserve the complete native workflow, including fields,
destinations and unknown members. Keep argv/outcome-based operation attribution
and actual-binary fixtures; a shared reduced projection would conceal data loss.

Paired comment-and-close acceptance discovers an available terminal destination
and compares native transition identities across lanes before writing. Preserve
the specific legacy screenless refusal and current separate-comment improvement.
Verify stored status, exactly one complete comment and unrelated native fields;
never replay uncertain writes. Actual-binary fixtures cover screened atomic and
screenless separate delivery, local/validated previews, atomic-only refusal,
failed status writes and failed comments after acknowledged status changes.
Require bound receipts, original error classification and safe inspection hints.

Successful resolver previews publish to the process-wide warning queue. Tests
that produce those warnings must run serially, inspect them and drain the queue
before leaving; mutex protection alone does not isolate concurrent invocations.
Keep unrelated read/envelope tests free of leaked cached-identity provenance.

Verified identity saving needs actual registered dispatch and binary evidence,
including a missing selected target, previews that create no directories,
conflicting authored scope before lookup, native absent/null/unknown fields,
unchanged-candidate snapshot conflicts and retained publication after output
failure. A compiling named guard must reject a wrongly typed preview flag; a
flag merely named dry-run does not establish preview safety. Discovery must
expose the local-write alternative without changing the default native read shape.
Exercise the actual native Windows regular-ancestor refusal before providers,
and require unchanged human previews to say they would keep the identity.

*   **Framework**: standard library `testing`, table-driven, lowercase named
    subtests. testify is used only in `tests/live`; no other assertion
    library anywhere.
*   **Test behavior, not implementation** — envelopes, exit codes, rendered
    output, not internal call sequences.
*   `mise run test` runs the untagged suite, including binary contract tests,
    with coverage. `mise run ci` adds the race detector, tagged hermetic gates
    and the other checks declared in `tasks.toml`. Native gates require their
    selected host and remain separate from cross-compilation.

## Suite map

| Suite | Location | Covers |
|-------|----------|--------|
| Unit | package-adjacent `*_test.go` under `internal/**` | package logic (external `_test` packages for public-API tests) |
| Black-box unit | `tests/unit/` | cross-package behavior without a server |
| Contract | `tests/contract/` | the built binary's agent contract: envelopes, exit codes, flag behavior |
| Guardrails | `tests/guardrails/` | repo invariants (see below) |
| Output audit | `tests/audit/` | `//go:build outputaudit`; per-command output axes (M1–M12 machine, H1–H5 human) against captured fixtures. `go test -tags outputaudit ./tests/audit/...` — the decisive check that an output refactor is byte-identical |
| Flag matrix | `tests/matrix/` | `//go:build flagmatrix`; actual binary effects for every owned flag, including hidden aliases; full runs join executed proofs against the live tree |
| Native integration | `tests/integration/` | isolated Windows credential recovery; `windows && nativekeyring`; `mise run test:keyring:windows` on Windows |
| Live | `tests/live/` | `//go:build live`; owned tenant acceptance via `mise exec -- go test -tags=live -count=1 ./tests/live` with explicit `JIRA_LIVE_*` settings below; excluded from untagged `go test ./...` |

Live writes require `JIRA_LIVE_ACCEPTANCE=1`, `JIRA_LIVE_BINARY` selecting the
frozen executable, and dedicated `JIRA_LIVE_BASE_URL`, `JIRA_LIVE_PROJECT` and
`JIRA_LIVE_ISSUE_TYPE` fixtures. Set these only for an authorized owned run and
follow the ownership, intent and cleanup rules in this file before entry.
`mise run test:live:guards` explicitly sets `JIRA_LIVE_ACCEPTANCE=0`: it runs
the tagged package's hermetic guards and skips tenant writes. It does not
establish live acceptance. There are no `test:live` or `test:integration` tasks.

## Contract tests are black-box

Issue-view header cases distinguish absent, null, empty and populated native
fields through the binary. Bind exact human rows and glance cells, native
JSON/compact field maps, requested fields and the single issue read.

Query precedence guards use an uncached default_board in existing list/mine
raw and saved query fixtures across preview, search, count and browser routes.
Bind exact submitted or URL JQL, mine account scope and absence of board reads
and provenance.

Machine failures keep a single parseable frame when streams are combined; human
mode retains its error and hint.

Link handler tests bind resolved preview sentences in source order with one type
read and no write; bare previews claim none. Failed write receipts retain intent
sentences separately from outcomes.

Published batch-inspection expressions must run against real binary receipts
for all-success, mixed-success, total-failure and acknowledged-write/follow-up
failure cases. Verify every target remains visible and failed-row selection
retains both `data` and `error`. Check zero-exit mixed batches explicitly;
guide text matching alone does not establish an executable recovery recipe.

Contract tests execute the real binary (`runJira(t, args...)`-style helpers
returning stdout/stderr/exit code) against `httptest.NewServer` Jira stubs,
then unmarshal stdout as the envelope:

*   Route command execution through the compile-once `jiraBinary` helper
    (`tests/contract/version_test.go`), never `go run`/`go build` the CLI per
    test — a per-test compile dominates suite wall time.
*   Acquire compiled binaries before starting per-command deadlines. Keep the
    build's own deadline separate so a cold cache cannot exhaust the command's
    runtime budget before execution begins.
*   Assert envelope fields **structurally** — unmarshal into a target and
    check fields; never string-match whole JSON blobs.
*   Error paths assert the error envelope arrives on **stdout** with the
    right code and exit code — use the shared binary and envelope helpers in
    `tests/contract/`.
*   The exact command inventory (`tests/audit/inventory_test.go`) reads the live
    command metadata — a new command surfaces there automatically; register its
    real recipes and required cells, then keep the inventory green.
*   Never contact a real Jira from contract/integration tests — `httptest`
    stubs only. Real-tenant coverage belongs in `tests/live/`.

Explicit issue preparation contracts author independent known-family fixtures,
then pass the actual typed preparation payload to the native issue writer. Check
exact received field tokens, large integers, numeric-string spelling and unknown
plugin bytes. Preview and malformed-value refusal must send no requests; each
successful write has one native acknowledgement. Do not derive expected values
from the production codec or example registry. Native Windows fixtures preserve
the selected executable basename, including `.exe`.

## Flag matrix

Flag matrix cases call `Prove` only after asserting a flag's observable effect.
Use `fwSuccess` before interpreting successful result data; `fwParse` only decodes
and also serves refusal cases, so it does not validate exit or envelope success.
Competing file/stdin contents establish source selection; anchored apply/merge
also assert authored content and protected native data. The full `TestMain` gate
must pass with no missing owned pairs. Filtered runs deliberately skip that gate
and cannot establish complete flag coverage.

## Goldens

Converter tests bind literal-colon text-node boundaries as well as text values.

*   ADF fixtures live alongside `internal/adf` tests, including the explicit
    Markdown, rendering and preservation cases. Published schema goldens use
    `internal/cli/root/agent_schema_test.go` and its `testdata/` directory.
    Update fixtures deliberately and review the diff; changed expectations
    must follow a verified behavior change.
*   Dashboard tests under `internal/tui/dashboard` cover stale queries/details,
    pagination refusal, selection identity, input ownership, field presence and
    exact frame geometry. Command-picker tests cover modal key ownership, replay
    and bounded rendering. Selected status rows must retain padded category colors;
    Primer's generic `list.View` deliberately strips selected-row SGR, so the
    dashboard paints its public visible range while retaining its scroll owner.
    Shared screen tests exercise actual Linux terminals,
    drawing and mode restoration after quit/cancellation. Other native hosts
    require their own execution evidence. There is no `internal/tui/goldens` suite.

## Guardrail tests

Invariants get guardrail tests (`tests/guardrails/`): every declared
completion predictor is handled, error code/hint tables stay in lockstep,
the embedded guides satisfy the Agent Guide Standard
(`docenttest.Validate` in internal/cli/root), command packages never use
process streams or non-returning `Run` handlers, output-helper results are not
discarded, and contract binaries compile once per test process through the shared
`tests/contract/version_test.go` owner. When a rule in `.claude/rules/` becomes
mechanically checkable, add a guardrail for it. Give AST analysers a small
invalid source fixture as well as scanning the repository, so the guard itself
cannot silently stop matching.

## Seams and hygiene

*   Packages that own goroutines assert no leaks via
    `goleak.VerifyTestMain(m)` in a package `TestMain` (precedent:
    `internal/cli/command`). Keep the guard when adding tickers or
    background work to such a package.
*   Configuration tests inject credential fakes through
    `config.Resolve(ctx, options, store)` and explicit environment maps.
    `config.Load(options)` is local and never consults a credential store.
    Ordinary tests must not read native credentials. Secret-like redaction fixtures
    must be visibly synthetic; annotate only exact scanner-matched fixture lines
    with a reason rather than disabling a rule or excluding a whole test file.
    Native keyring integration uses synthetic records and isolated storage;
    local discovery and cache readers prove that they never construct a store.
*   Distinguish a missing token from an unavailable credential store. The latter
    retains `auth_store_unavailable` and the authentication exit; it is not proof
    that no stored credential exists. Test both against explicit fixtures, and
    assert that pre-request refusals neither contact Jira nor create issue history.
    A closed D-Bus fixture isolates Linux only; skip that fixture on other systems
    rather than reading an existing native credential store.
*   Time-dependent assertions use an injected-now seam
    (`x/human.FormatTimeAgoCompactFrom`-style), never `time.Now()` in the
    assertion path.
*   Tests run under `-race` in CI; a test that swaps shared/global state
    must not run in parallel with siblings.

For concurrent cache publication, use an independent `os.Root.ReadFile` reader
and decode its wrapper independently. On Windows, pathname `os.ReadFile` omits
`FILE_SHARE_DELETE` and can block the rename under test. Do not convert sharing
violations into retries or accepted misses to make the integrity guard pass.
Run the guard on Windows; Linux execution and cross-compilation do not prove
Windows sharing semantics.

Live workflow discovery also proves offline eligible-ID callbacks from a private
per-run cache, comparing IDs against an independent native-response projection.
Disable credentials and route HTTP through unreachable proxies for callbacks.
Do not clear the user's normal completion cache as a test cleanup shortcut.

Native PowerShell guards use `JIRA_TEST_PWSH_BINARY` for an explicit runtime.
Windows child environments must retain `PATHEXT`: PowerShell uses it to execute
native callbacks and literal `.exe` observer paths. Keep profile/config/cache
directories owned by the fixture. Check actual child exits and receipts before
interpreting empty completion output as a product defect.
Compile-once contract, flag-matrix and startup-benchmark outputs keep `.exe` on
Windows before invoking `go build -o`. Their child environments bind
`USERPROFILE`, `APPDATA` and `LOCALAPPDATA` to owned paths and retain `PATHEXT`.
Native Go acceptance exercises the ordinary build helper; record external-binary
selection separately. Use a source filesystem supporting Go's module file locks.
Editor previews select an owned executable with literal arguments, never an
assumed Unix utility. Home overrides also bind Windows profile directories;
assert the selected user destination independently of the project destination.
Exercise UTF-8 callbacks under OEM and Latin-1 host encodings, including custom
fallbacks, native failures, empty replies, refused context and exceptions. Keep
these Windows fixtures in owned hidden consoles so they cannot change the user's
console code page. Read native observer UTF-8 receipts directly; a PowerShell
stdout round trip can corrupt correct observed argv under a non-UTF-8 host.
Verify Unicode metadata and two-step directory continuation through actual
generated dispatch and literal executed argv.
Use independent generated applications in concurrent runspaces with callback
barriers. Verify ordered ownership, exception release and cancellation of a
waiting completer while another callback remains active; then admit fresh work.
Directory continuations use the platform's path separator when the shell
supplies it; keep authored slash prefixes and non-path values exact. Do not
normalize all observed argv to hide unrelated identity changes.
Validate the executable, generated dispatch, callback argv, literal insertion,
actual TabExpansion2 replacement and native argv. A standalone completion string
cannot prove a comma argument assembled correctly. Execute only a validated
single command containing literal fixture arguments; never evaluate arbitrary
candidate expressions. Run native Windows separately from Linux shell checks.

Elvish callback guards load generated scripts in the real editor namespace of
an owned native console. Apply its registered matcher before comparing candidate
stems; positional completers also emit flag names for the editor to filter.
Exercise positional, current-word and ordinary dynamic flags, reload and literal
CSV identities. Callback/matcher execution does not prove Tab insertion; test
that separately through the real editor. Keep descriptions out of inserted values.
Actual Windows Elvish insertion uses an owned native console and key records:
submit the complete line, Tab, then a bound marker key before Enter. Assert exact
observer argv and a successful joined shell exit. External callbacks use the
real CLI; the accepted command records arguments without performing a mutation.
Bound runtime and admission, preserve selected configuration and literal CSV,
and keep these guards distinct from direct callback/matcher tests.
Include ordinary cached field CSV insertion with descriptions, an already selected
identity and separate/inline flags, alongside current-word filesystem CSV roles.
Dynamic comma-list guards cover described and bare rows, selected-value exclusion
and both separate and inline flags. Assemble the final identity before producing
rich candidates; descriptions must not participate in CSV concatenation.

Every new watcher selector flag needs a proving matrix case for each write verb.
User-query cases assert the complete lookup precedes keyed writes, exact resolved
account identity and authored query receipts; an unregistered contract test does
not satisfy the matrix's owned command/flag coverage gate.
Keep new taxonomy codes in the explicit keyed severity ladder, including rank
zero for account preflight failures before fan-out. Migration output audit must
require `deferred` and exercise nonempty declaration names in both machine modes
without publishing their retained values.

Native Windows browser guards launch only an owned executable through `BROWSER`.
Record exact URL argv, check helper exit through an independent process handle,
and join cleanup after failed assertions. Machine output must suppress handoff;
an HTTP trap establishes that URL-only paths perform no Jira reads. Handoff is
not GUI rendering or proof that a page loaded.
Browser-gating unit fixtures select an owned BROWSER value; default-launcher
guards assert the platform executable and every literal argument independently.
A download directory-swap fixture blocked by native Windows delete sharing must
prove original identity, denied rename, preserved stream failure and partial-file
cleanup. Keep actual redirected-directory proof separate; never count a failed
fixture rename as a successful replacement.
Capture the pre-fault Windows file ID through an opened handle, then close it
before exercising the writer; pathname Stat loads identity lazily.

Native Windows release-note paging guards use fresh hidden consoles with explicit
CONIN$/CONOUT$ handles, an independent pager stdin/argv receipt and owned key
events. Verify built-in navigation, Control-C/Control-Break, exact mode restoration
and return to the original screen. Read bounded CHAR_INFO rectangles and verify
the complete copied region; character counts differ for wide diagnostic glyphs.
Console cells establish display fidelity, including their explicit padding
projection; they do not establish original write bytes. Keep exact Markdown
stdin and piped-byte contracts separate. The native flag matrix must prove
release-notes --no-pager rather than dropping the required pair.
Assign the console helper to an owned kill-on-close job before admitting CLI
construction. Parent cancellation must terminate and join the actual descendant
and leave zero active job processes; killing only the helper is insufficient.
Configured-pager receipts must record deliberate changes to both borrowed modes
before the CLI restores them. Normal built-in quit restores exact original cells,
attributes and cursor; cancellation may add its diagnostic but no pager residue.

PowerShell guards cover non-forwarded values resembling context flags, `--`,
long/short inline CSV and present/absent caller exit status. Nonliteral-context
guards assert an independent side-effect marker remains absent. Capture drivers
reject redirections, pipelines and expressions before executing fixture arguments;
exercise their rejection predicate with parse-only adversarial inputs.

Live offline completion checks must retain the public account observed by the
authenticated producer. Disabling the credential store also removes identity
resolution from that store; provide the verified public email explicitly while
keeping tokens empty and every proxy unreachable. Never assume a filtered run
with inherited identity proves a clean full-suite invocation.

Clear both `NO_PROXY` and `no_proxy` in offline guards: Go uses the lowercase
value when uppercase is empty. Start with an inherited lowercase wildcard,
apply the offline recipe, then test actual proxy selection in a fresh process.
No HTTP request is needed.

Migration output-audit binaries use the `outputaudit` build tag to initialize the
upstream native keyring mock before command execution. Production and native
acceptance binaries exclude that seam. Exercise actual config migration and
rollback publication in every output mode; checked setup must succeed before
interpreting scenario data. Mask only valid dedicated random backup identifiers
when comparing independent runs. Never use the current host keyring for audit
fixtures, even when migration deliberately ignores ordinary keyring opt-out.

Real Linux keyring acceptance runs production binaries inside an owned private
D-Bus session with synthetic entries and independent `secret-tool` inspection.
Verify exact preimages, historical namespace retention, binding, provider backup
retention and rollback scope across JSON, compact and human output. Native mocks
and cross-compilation do not prove actual SDK or Windows/macOS keychain behavior.

Bash generated-script fixtures carry literal prefixes through a quoted environment
value. Native MSYS startup can expand a caller's argv before the Bash driver runs;
verify input admission before attributing a completion failure. Recognize the
Windows System32 WSL launcher by file identity: default discovery skips it and
explicit selection fails. Actual Windows Bash acceptance selects a native shell.
Keep unsupported star/backslash filenames as named Windows skips; preserve space
filenames, caller IFS, all static literal values and the complete Unix inventory.

Update candidate cleanup guards prove a nonempty positioned read succeeds before
publication and returns `os.ErrClosed` after the owner returns. `File.Stat` on
Windows reports the native invalid handle after close; do not mistake that error
for an open descriptor or accept every error as evidence of closure. An omitted
close mutation must compile, enter and fail the named cleanup guard.

Config-editor selection fixtures use the executing owned binary and shared
shell quoting. Distinguish winners through literal argv markers, preserving
complete Windows executable paths without Unix utility dependencies. Keep saved
and rejected bytes, outcome/context and regular-file assertions on every host.
Only POSIX mode assertions live in named child subtests skipped on Windows;
a permission skip must not skip its parent's recovery checks. Windows file modes
do not establish ACL protection.

Guide-export tests retain exact rendered bytes, qualified paths, collision refusal,
hard-link target preservation and cancellation receipts on every platform. Put
POSIX mode checks in named subtests: Windows regular-file modes do not establish
POSIX permissions or ACL protection. Attempt actual symbolic links and skip only
the native Windows unavailable-privilege error, keeping every other error fatal.
Keep invalid-path validation separate so a link skip cannot hide its assertions.
Neither skipped link cases nor hard-link passes prove symbolic-link acceptance.

Published migration guide recipes use the separately cached output-audit binary
and owned source/destination/recovery fixtures. The ordinary contract binary stays
production-built for native acceptance. Never execute credential migration examples
against the host keyring merely because ordinary credential resolution is disabled.

Default-only live creation must pin the verified dedicated project/type after
inherited environment values and independently check its effective preview
payload before any write. Hashing an owned configuration file alone does not
establish effective defaults: environment values can override that file. Bind
public account/board defaults too; changed or foreign input refuses creation.
Record attempts before writes and register acknowledged identities for cleanup.

Automatic theme guards exercise passive light/dark/missing/malformed hints,
exact environment provenance after framework construction and panic restoration,
custom palette fallback, actual config publication/migration/rollback and generated
Bash/Fish/Zsh/PowerShell candidates. Retain a delivered policy-4 CLI fixture
independently of the policy-5 mapper; never count a compiler failure as a guard.

Use a coordinated concurrent-constructor guard for framework theme passes,
including exact restoration. Mutex waits are not durable synctest blocking;
never wait for one inside a synctest bubble. A compiling overlapping-pass probe exposed the
process-environment interleaving; elapsed wall time does not prove serialization.

Client-preference guards prove positive seconds and typed arrays through file,
environment, stored get, effective list and publication. Cover malformed arrays,
null/nonstrings, commas/whitespace/duplicates, absent versus empty, source-profile
selection and old nonpositive refresh fallback. Invalid values must refuse before
providers. Retain an independently generated policy-5 CLI fixture for reopen and
exact rollback; test that current mapping never reinterprets older records.

Account-resolution guards exercise native absent/null markers even when their Go
values are populated, plus nil, empty and whitespace-only identities. Assert the
shared protocol error, unchanged fields and one lookup; canceled resolution must
enter no constructor/lookup. Drive create, edit, clone and both watcher commands
through their actual registered handlers with independent write counters. Do not
credit malformed invocation, compiler failure or an unrelated output error as an
identity guard. Valid identities resolve all pending fields once; literal IDs
stay untouched and do not trigger lookup.

Cached-identity guards compare independent site/account/cloud bindings, including
path case, equivalent URL/account spelling, absent/empty ID and scope changes
from environment or credential layers. Invalid declarations refuse before
providers even when later values could mask them. Prove safe inspection and
repair of malformed binding URLs through shared redaction/publication owners.
Drive all five account consumers and the production CLI offline; matching cached
IDs carry an unverified warning, mismatches retain @me. Live and validated previews
still use one actual current-user read and never consult cached preferences.
Retain an independently delivered policy-6 recovery fixture for reopen/rollback;
current mappings must not reinterpret older immutable candidates.

Cached-ID live Jira acceptance first inspects all stored binding declarations against an
independent native identity and verified public site/account/cloud. Clear inherited
cached metadata and every accepted environment spelling before offline previews,
including named token references and both proxy exemption variables. A compiling
adversarial inherited-ID guard must fail by name without that clearing. Clone
previews expose only known nonempty account overrides, so assert both prepared IDs
through typed output instead of unchanged Cobra variables copied into options.

Login acceptance must observe real prepared settings and actual provider effects.
Use owned public files and synthetic/injected credentials, including native
isolated providers; never replace user credentials for acceptance. Prove provider
admission before/after cancellation, acknowledged credential plus failed settings,
unknown entered writes, blocked previews, environment-only metadata publication,
actual selected token-reference provenance, and output failure after publication.
Refactoring a concrete pointer helper behind an interface must keep error returns
as a nil interface; existing target-refusal guards catch typed-nil regressions.

Exercise the actual interactive SDK form and exact account/vault/item selection.
Mixed flag/public defaults must not enter a provider for an already supplied
field. Test public selector publication for a fresh process and refusal of
authored reference replacement, including generic-token precedence.

When one public default needs storage, use a conflicting complete stored tuple
to prove the other public field remains unchanged. A canceled preview after
receipt preparation must fail even when no later provider stage would run.

Literal theme migration must preserve ~, $NAME and ${NAME} bytes and prove the
actual palette selected after migration. Use owned literal files plus environment
and home decoys; inspect rendered colors and exact rollback bytes. Exercise all
recorded policy versions, missing relative origins, cancellation and native
Windows. Decode-only preparation never reads palette files or process expansion.

Bound description guards must distinguish absent, null and empty documents;
retain exact future ADF bytes, large numbers and independent snapshot buffers.
Prove native ID rereads after issue-key moves, site/account/API-route checks on both sides of
reads, cancellation and invalid-anchor refusal before I/O. A blank editor save
must not turn native null into an empty document. Exercise actual registered
preparation in read-only mode and all output formats, then reopen its native
artifact. Fresh reads and merges do not establish server-enforced CAS.

Exercise real foreign redirects, missing/changed request authentication, malformed
HTTP 200 objects, wrong native types, duplicates and trailing bodies. They must
produce typed upstream failures without a candidate. Use whitespace-bearing
native ADF to prove exported byte preimages survive JSON/compact serialization
and reopening. Preserve cancellation/deadline causes from interrupted bodies.

Recoverable draft guards exercise no-clobber publication, manifest-last ordering,
partial acknowledgments, changed backups, bounded strict reopening and saved-work
retention. Drive actual draft/inspect/resume commands in each output mode; editor
acceptance uses real subprocesses and terminals, including cancellation, reaping
and restoration of both borrowed terminals. Mocked editor calls alone do not
establish process behavior. Local inspection must work without credentials or
network access; refreshed inspection binds the immutable issue ID and public scope.

The output audit captures editor success through real Linux PTYs and owned
Windows amd64 consoles, including `config edit`; its former success exemptions
do not apply. Unsupported native terminal allocation fails explicitly as unrun
evidence. Cross-compilation and Linux PTY receipts do not establish Windows/macOS
terminal acceptance. Preserve all required matrix cells.

Native Windows Go OpenRoot currently prevents renaming its directory through a
handle without delete sharing. The directory-move guard must verify the exact
sharing-violation refusal, an unchanged usable candidate and no moved directory;
that receipt does not claim a successful redirection challenge. Platforms that
permit the move must exercise the changed-directory refusal. Preserve these
platform distinctions instead of treating a failed fixture operation as a passing
redirect guard or silently skipping it.

Contextual file completion requires real generated-script and insertion evidence
for each supported shell, including nested directories, literal metacharacters,
spaces, quoted commas, doubled quotes, flags and terminators. Pure directory and
CSV codec guards establish their owner contracts only; they do not establish
shell routing or insertion. Lookup failure and cancellation must emit no partial
candidate list, and the current word must remain literal rather than evaluated.
Drive native editor tests through a bounded terminal and an owned argv observer;
completion callbacks alone execute the real CLI. Never execute uploads or
settings writes as the observer. A bound editor key marks the end of Tab handling
instead of a scheduling sleep. Give coverage-instrumented observers their own
`GOCOVERDIR`, and fail explicitly when a requested shell binary is unavailable.
Verify inline values, completed quoted context, directory continuation, hidden
boolean aliases and repeated Elvish sourcing. Bash 3.2 spacing uses its registered
wrapper because changing completion options during a callback cannot affect that
callback's already-selected specification.

CSV directory choices retain an unfinished current field so a second Tab can
complete a leaf in the same argument. Theme choices preserve literal symlink/..
traversal when made absolute and validate the final transport bytes, including
the current directory. Verify these identities in native editors and named guards.

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

Native field refreshes belong to `jira.Nullable.UnmarshalJSON`, alongside the
shared generated-model decoder. Decode into a fresh value and publish the full
presence/null/value tuple only after success. Null clears its prior value; a
subsequent value clears null. Failed decoding retains the exact earlier tuple
and leaves aliased maps, slices, pointers and raw body bytes untouched. Do not
work around stale native fields independently in commands or adapters.

Tests that load a named IANA location must provide their timezone database as a
test dependency, for example by importing `time/tzdata` in the test file. Native
Windows may provide time.Local without an IANA database. Preserve real DST
transitions in the guard; do not replace them with a fixed offset or skip missing
fixture data. Do not add timezone data to the release CLI merely to repair a test.

Externally selected native update tests keep a renamed installed basename while
preserving `.exe` on Windows, including disposable flag-matrix and audit
installation copies. Managed-update audit recipes must actually execute their
owned copies in every output mode and retain the installation-owner result.
Version/formatter fixture expectations and selectors use the executing runtime
OS and architecture; retain exact output quoting, delimiters and field order.
Confirm the original and candidate version commands
actually execute before interpreting publication/download results. Test only
disposable installation copies; preserve selected artifacts and retained backups.

Windows Credential Manager caps each blob at 2,560 bytes. The keyring recovery
owner keeps ordinary/version-2 backups intact and publishes larger private v2
payloads as bounded encoded pieces with a version-3 descriptor last. Derive piece
names only from the validated parent backup ID; verify count, size and full digest
before reopening snapshots or mutating primary entries. Retain partial effects
and native admission on cancellation/failure; this is not a provider transaction
or compare-and-swap. Native tests use fresh random service names, synthetic
records and independent WinCred reads, never enumeration or the user's default
service. Run them in a separate test process so global mocks cannot earn credit.

Native Windows editor acceptance uses the exported shared supervisor inside fresh
hidden consoles. Open the owned CONIN$/CONOUT$ handles explicitly; redirected
standard streams are not proof of terminal allocation. Observe both changed
native modes before releasing the real editor, then verify exact restoration,
process termination, literal argv and separation from result stdout. Cover failed
exit, cancellation, launch failure and second-terminal capture refusal. Run
`mise run test:editor:windows` on Windows; its nativeconsole-tagged gate fails
explicitly on other hosts. It also executes real `config edit` and `issue edit
resume` commands with success, failed editor exits, invalid saved input and
actual Control-C and Control-Break cancellation. Register the isolated driver for interrupts
and make the editor ignore them before readiness; only the CLI supervisor may
end the editor. Send the event from that fresh hidden console, never the caller's.
Require exit 6 and `canceled`, restored modes, exact retained work, no candidate
and termination before return, with a generous five-second return watchdog.
Require exact configuration publication or rejected-work retention, unchanged
native draft backup, preserved authored Markdown, zero extra Jira requests and
the selected typed operation. Native macOS behavior remains separate acceptance.
An externally selected standard production
artifact must actually execute; disclose test-only binary selection and never
count a refused executable or cross-compilation as a native pass.

Complete owned console fixture preparation before capturing the modes borrowed
by the editor. Fixture subprocesses can change that shared console. Require both
actual editor mode changes and exact restoration to this invocation boundary;
never weaken the postcondition to accommodate stale fixture measurements.

Core comparisons are untagged results; publish verification rows through
envelope.VerifiedField at the shared CLI boundary. Both ordinary and terse
--verify help describe later observation after acknowledgement.

Requested-field verification belongs to core.CompareIssueWrite and the shared
create/edit readback owner. Request authored fields/update IDs plus existing
summary/status/updated once. The production adapter retains exact native field bytes separately from typed
presentation and admits bounded complete authenticated JSON before comparison; malformed or unavailable readbacks retain
acknowledged writes with verify_failed. Typed verified.fields rows preserve
native observed absent/null/value and report matches/differs/unverified in sorted
field order. Exact numeric spelling stays significant. Known identity/collection
checks tolerate response metadata, including identified cascading-select children.
Collection replacements require both requested
and observed membership; add/remove updates may retain untouched members. Unsupported or
ambiguous updates, absent fields and different ADF remain unverified, including
custom-field documents admitted by the shared ADF parser. Derive
warnings from typed Spec results in target order, never the concurrent global
queue. A later difference neither proves a Jira drop nor supplies remote CAS.

Only verified.fields.observed (and its keyed edit path) is native field
passthrough. Keep verification row names under the ordinary output audit.

Collection verification guards must reject leftover members after replacement or
set plus add/remove, while preserving expanded identity and untouched-member
acceptance for updates without set.

Explicit create/edit --validate-remote checks requested field IDs and native
operation names against complete context-bound metadata through shared discovery
and preflight owners. Preserve request values and bytes; never convert, strip or
infer fields. Bare previews stay local; unflagged live requests retain Jira value
validation. Native create ID/name meaning must not be guessed: a display name
that equals another type's ID still selects by name. Project/issuetype create
identities retain their structural screen exemption. Publish only checked field
IDs in envelope.IssueFieldPreflight, per key on edit; values, requiredness,
permissions, top-level members and future write success are outside this check.
Refused or malformed contexts send no write; ordered partial successes remain
real and are never replayed. Cancellation cannot admit a completed metadata read.
Metadata readers must validate the final successful HTTP reply against the client's
authenticated API scope and exact credentials through the shared reply owner.
Credential-free redirects cannot establish project, type, field or edit context.
Keep raw API media redirects usable and preserve ordinary HTTP error classification.

Preflight tests must contrast literal inputs with and without the explicit flag,
exercise all metadata pages and distinct issue contexts, refuse missing fields
and unsupported verbs before writes, and prove native payload identity. Include
ID/name collisions, malformed alternate-client documents, cancellation and
ordered partial previews/live writes. Fixtures must advertise actual required
metadata facts rather than returning an empty successful catchall.

Exercise foreign-origin and same-origin out-of-scope redirects for project, type,
field and edit metadata. Prove refusal before a write, credential stripping, and
continued acceptance of authenticated redirects within the configured scope.

Native Windows command fixtures must bind USERPROFILE, APPDATA and LOCALAPPDATA
to owned temporary paths. Do not inherit the user's stores. Record an explicit
skip for filenames Windows cannot represent; retain the other path cases.

Bare email lookup is opt-in for the assignee flag through command.AccountField.
Use core.ResolveVisibleUser and ActiveUserSearchPage for the same authenticated
visible-search policy as user discovery. Preserve privacy-hidden email; the sole
visible query match is not a verified exact email address or assignability proof.
An observed empty page establishes completion; short pages do not. Missing,
ambiguous, repeated, invalid or capped results cannot authorize a write. Keep
all bound account fields unchanged until every requested remote lookup succeeds, and
check cancellation after reads. Bare previews retain email with an unresolved
warning and open no client or cached preferences unless @me also needs them.
Native payload account IDs are never interpreted as email lookup requests.

Native Windows configuration contracts use representable filenames while retaining
stored-value control and secrecy checks. Windows UTF-16 process arguments cannot
carry invalid UTF-8 bytes: skip only those subprocess cases explicitly and retain
raw-byte pre-I/O validation through in-process registered initialization handlers
on every platform. Keep Unix filename and process coverage. An executable refused
before test entry earns no native acceptance credit; record the exact artifact
separately from other successfully executed artifacts and never change policy.

Watcher add/remove accepts explicit --user-query name/email searches through
command.AccountField.LookupQuery and the shared account resolver. --user stays
literal IDs/@me; the flags conflict even when either was authored empty. Pure
validation rejects blank query before I/O. Query text is never @me or cached
identity, even when spelled @me. Search requires one complete visible active
match and inherits the shared authenticated response, bounds and cancellation
policy. Resolve once before keyed work; a match is not exact email/name proof or
watcher permission. Bare previews retain user_query with account_query_unresolved
and empty account_id, opening no client or preferences for that query. Live and
explicit remote previews publish the resolved ID plus original query context.

Explicit TUI deferral tests must exercise the actual command with and without
`--defer-tui`, not only the mapper. Check typed deferred names without values,
preview absence of directory/provider effects, exact source recovery, no inert
TUI destination keys, and fresh recorded-policy rollback for distinct and same
source/target paths. Preserve unknown-schema and malformed-shape refusal. Keep
an independently delivered policy-8 fixture from the previous committed producer;
only fixture paths and enclosing artifact hashes may be rehomed. Policies 1–8
must retain strict TUI refusal. Future-version refusal uses an unsupported version,
not the next version after the default producer when opt-in policies exist.

The `defer-tui` matrix proof must execute both admission states and check that
neither preview publishes settings or recovery. Human and compact previews must
preserve the deferral boundary without exposing retained TUI values.

Exact watcher account preflight must exercise both add/remove actual commands,
read-only previews, explicitly validated live writes, and unchanged ordinary
literal paths. Check one escaped accountId query before all writes, exact native
reply binding, absent/null activity, inactive/deleted rejection and no partial
field publication after late cancellation. Retain authenticated final scope and
credential guards, bounded reply refusal, privacy-hidden email and native unknown
bytes. Distinct query and exact-ID lookups never share a cached match. Live
acceptance may change only verified self subscriptions on owned issues and must
independently verify cleanup.

Exact-account admission must reject duplicate decoded root members and case
variants of accountId/active/deleted through the shared native object owner
before typed decoding. Include overwritten inactive/deleted values and escaped
duplicate key spellings in adapter and actual CLI no-write guards.

Search protocol guards must exercise discovery, both watcher verbs and
create/edit/clone assignee-email consumers against duplicate ID/activity/deletion
members, case variants and escaped duplicate names. Require one refused search
and zero issue requests. A valid first row followed by an invalid row must not
publish any of the page. Keep genuine native unknown-member/nullable-email
fidelity, paging and authenticated final-reply guards; compile named mutations
that bypass admission and verify they fail.

Current-user native admission guards must refuse duplicate/escaped identity,
case variants, activity/email duplicates and repeated unknown root members.
Exercise actual auth status with --show-token in every output mode, candidate
login before provider/public settings publication, create/edit/clone and watcher
@me writes, explicit remote previews and fresh whoami save/preview paths.
Assert one identity read, no dependent requests or writes, no token disclosure
and no created settings directory. Retain native privacy, interrupted body,
configured-timeout/cancellation, HTTP and retry guards. Mutation credit requires
compilation and a named guard failure; execute native Windows owner and CLI
contracts separately from cross-compilation.

Native Windows pipe guards exercise anonymous, synchronous named and already
Go-wrapped overlapped pipes. Verify non-inherited duplicates, unchanged original
handle flags and pipe modes, repeated cleanup, EOF and closed-handle refusal.
A later original read must actually wait before data arrives; prebuffered reuse
cannot prove its IOCP association survived. Exercise real sockets under Windows
FILE_TYPE_PIPE classification and preserve other borrowed readers. Registered
stdin and accessible text/confirmation guards must observe consumed input,
withhold writer closure, retain canceled/timeout outcomes and reuse the original.
Use an overlapped fixture for native PeekNamedPipe observation; a synchronous
probe may block behind the pending read. Preserve native probe errors instead
of treating every failure as socket exclusion. Restricted readable pipe guards
must prove both data access and the genuine information-query failure.

Native Windows console guards allocate fresh hidden consoles and inject only
owned INPUT_RECORD events with the verified native layout and matching ASCII
virtual key codes. Observe actual consumed text or masked password prefixes,
withhold successful completion, then cancel or expire the caller context.
Assert ordinary canceled/timeout classification, no bound-value publication,
exact original modes and a later pending read through the borrowed handle.
Require an independent non-inherited native console handle and its closure;
a duplicate-only mutant must fail the original pending-read reuse guard.
Include Unicode success, validation retry, repeated fields, cancellation before
kernel entry, panic cleanup and native restoration failure. Keep thread reuse
on the same pinned thread after the owner returns. Run test:input:windows on
Windows; its nativeconsole-tagged guard fails explicitly on other hosts.
Retain helper-entry receipts and failed/refused invocations. An execution policy
refusal or a timed-out reuse assertion never earns native success credit.

Native Windows configuration filesystem acceptance uses owned directory
junctions with independently verified IO_REPARSE_TAG_MOUNT_POINT metadata.
Exercise missing and long target paths, literal trailing-dot/space names with
untouched ordinary siblings, retargeting refusal, exact recovery,
default/explicit initialization and real migration/rollback commands. Preserve
JSON/compact/human output checks and preview no-publication guarantees. Use
NoProfile/NonInteractive PowerShell only to construct literal owned fixtures;
join its exit and remove junctions without deleting target contents.
Run test:config:windows on Windows; nativefilesystem-tagged gates fail explicitly
on other hosts. Junction acceptance does not waive symbolic-link privilege or
macOS requirements, and refused executables earn no execution credit.

Native draft path acceptance uses the same verified junction fixtures. Exercise
literal trailing-dot/space directory creation with untouched ordinary siblings,
exclusive creation races, reopened-identity mismatch, late cancellation receipts
and refusal of junctions at the draft root or each member. Actual draft, inspect
and resume previews cover every output mode, exact native bytes, protected ADF
and no extra requests. Real resume editors cover junction ancestors and literal
names through success, failed/invalid saves and Control-C/Control-Break. Run
`test:draft:windows` and `test:editor:windows` on Windows; wrong-host gates fail
explicitly. A compiled mutant earns credit only when its named guard actually
executes and fails; native policy refusals earn none.

Native attachment directory guards use owned junctions and literal extended
trailing-dot/space targets with untouched ordinary siblings. Verify exact binary
contents, collision/no-request previews, exclusive creation and staged overwrite
through actual commands in every output mode. Native directory completion must
exclude competing regular files and retain literal argv, including metacharacters
and nested paths. Zsh guards cover zero/one/two/unbounded unpredicted positions
and prove that empty actions do not invent filesystem candidates. PowerShell
provider results may omit a final separator; assert `ProviderContainer` and exact
relative identity while admitting only optional `./` and separator spelling.
Run `test:attachment:windows` for actual Windows filesystem and shell acceptance.
Keep native refusal receipts uncredited and separate from Linux PowerShell tests.

Attachment filename operations use the shared nativepath rooted file owner.
Keep literal Windows trailing-dot/space leaves and reopenable extended paths;
never let Win32 cleanup select an ordinary sibling. Retain exclusive creation,
exact collision checks, staging and incomplete-stream cleanup through the same
root handle. Literal reparse traversal refuses; ordinary rooted paths retain
their existing semantics. Native tests cover authored and metadata names,
ordinary/extended directories, failed streams and sibling preservation. Windows
delete-sharing refusal is directory identity evidence, not an executed rename.

Output-audit editor recipes use the owned test-executable fixture, never an
installed editor or Unix no-op command. Require a fresh receipt with literal
argv and an input digest after live entry, and no receipt after previews or
terminal refusal. Keep Windows temporary/profile paths owned. Fixture execution
does not establish native terminal allocation or faithful diagnostic capture.

Linux and Darwin audit invocations share their diagnostic collector over native
PTY allocators. Keep the master nonblocking and owned until its reader joins.
Preserve exact independent stdout and bounded diagnostic bytes; invisible escape
sequences must remain observable. Only Linux's final-slave `EIO` is an ordinary
end of stream. Refuse more than one MiB of diagnostics and terminate/join the owned
command on collector failure. Closing the parent slave does not prove every writer
closed: honor caller cancellation and cap the subsequent drain at five seconds.
Join before closing the master, retain custom cancellation causes and test actual
helpers, failed launches and independently retained writer descriptors.

Darwin PTY allocation uses the selected native grant/unlock/name operations and
the fixed 128-byte name ABI. Keep opaque native buffer calls escape-safe.
The selected `x/sys` version has no typed PTY-name getter: the test-only query
uses its raw ioctl ABI with a narrow deprecation exception. It is not linked into
the release CLI and still requires actual macOS acceptance. Neither
cross-compilation nor Linux execution proves macOS native allocation, EOF behavior
or editor operation; retain actual macOS acceptance as a separate requirement.

Windows amd64 audit capture uses `tests/internal/nativeaudit` and the shared
`tests/internal/nativejob` owner. The coordinator starts a hidden console helper,
assigns its job, waits for native console setup and records that accounting
baseline before admitting the CLI. The helper cannot construct a CLI before
admission. Its environment is an explicit clone, never a process-wide overlay.
Only the parent retains the outer job handle; cancellation before assignment
must also terminate the waiting helper. Verify abrupt owner death against actual
live descendants, joined native handles and the absence of a normal release.
Native job buffer calls preserve the Go pointer escape contract across opaque
`uintptr` wrappers; keep limits and accounting storage off movable stacks.

The observer admits only the selected owned Go build images with matching
toolchains, explicit `CGO_ENABLED=0` build metadata and native file identities.
It follows their threads from startup,
binds the selected system-module entry points and preserves its debug registers
across `NtContinue`. Native structure reads are limited to entry prefixes and the
fixed thread context; never read write payloads or patch executable code. Nonzero
console writes, unclassified handles and unmatched pipes refuse silence. Disk
writes and exact aliases of the retained stdout pipe are distinct. Retain that
pipe's identity through observation, then release every producer before joining
its bounded reader. An unknown writer-release outcome cancels the reader.

Keep a failed debug continuation pending until it succeeds or bounded cleanup
fails. Close every image/DLL event file even on lookup failure, and refuse an
invalidated module binding. A validated receipt must prove the actual selected
CLI exit, complete process/loader admission, balanced event handles and joined
processes/readers. Native console setup and CLI observations have separate
accounting scopes; the parent job must finish empty. A helper exit alone is a
capture failure, never the CLI result. Verify exact stdout bytes independently
and reject noisy or incomplete captures without inventing empty stderr.

Exercise actual `config edit` and `issue edit resume` in every output mode, both
visible and invisible diagnostics, missing admission, bounded control streams
and parent cancellation that terminates the actual supervisor and CLI. Cancellation
during stdout publication retains both the cancellation cause and acknowledged
bytes; recheck cancellation before publication and after joined cleanup. Keep
supervisor restoration after forced termination separate from cooperative CLI
restoration. Native macOS behavior and unsupported architectures remain separate
acceptance requirements.

Before/after CLI acceptance lives in `tests/live/parity_*.go`. Bind both selected
binary digests and each invocation without replacing the cleanup binary. Pin
the explicit legacy profile and remove inherited legacy routing/token/test-store
overlays before admission. Verify the selected public site, provider and native
account. Creation intent is not established process or provider entry; retain
returned identities before assertions and bind exact run markers through an
independent native read before ownership registration. Never replay an unknown
creation. Keep legacy receipt omissions distinct from the current acknowledgement
contract. Actual-binary fixtures must prove requests/effects and no-request
previews; compare rich native ADF and unrelated fields, not flattened text.
Missing domain fields must fail from a fresh decode. A passing tracer supplies
only its named scenario evidence, never full command/editor/platform parity.

Paired read adapters select declared legacy/current layouts explicitly. Bind
each result key and establish native resource preimages independently. Missing
arrays, null data and absent keyed rows are not evidence of an empty collection;
keep populated-resource, pagination and partial-failure variants separately pending.
Use owned per-lane cache roots while preserving admitted keyring transport.
Prove warm cached provenance and complete inventory with zero requests, then
change the native inventory and require one actual forced-refresh request.

Paired watcher writes retain strict shared ownership while translating only
authored legacy shortcuts and current-account identifiers. Bind corresponding
complete native preimages across lanes. Actual-binary fixtures assert the entire
HTTP sequence, exact account bodies/queries and lane-specific preview/refusal,
readback and no-readback receipts. Extra opposite writes must fail even when the
final state matches. Stop dependent mutations after any failed step; deliberately
repeated acknowledged idempotent operations are not retries of unknown writes.

Paired field preparation compares the legacy implicit scalar workflow with the
explicit local preparation and native writer. Admit only the verified current
account on a registered owned issue, with exact input bytes bound to that target.
Use fresh native edit metadata and equivalent native preimages. Independent
fixtures must assert the complete accountId payload and HTTP sequence, including
zero requests for preparation, preview and read-only refusal. Preserve unrelated
native fields and rich ADF. One assignee recipe does not establish every field
family, legacy create alias or editor workflow.

Paired comment tests translate the exact legacy bare add invocation before
shared ownership checks. Bind native body bytes to the active issue/comment,
retain add intent and returned identities before assertions, and never replay
uncertain writes. Restriction tests use a freshly verified current-account role.
Check omission, replacement and explicit null through actual native writers;
preview/refusal paths send no requests. Read comparisons map legacy oldest-first
ordering and final-page metadata to explicit current ordering and whole-walk
metadata. Current `--all` owns its page size and refuses an authored `--limit`.
Native reads must preserve the complete rich body and unrelated comments.
Single-comment native checks must not require a singleton thread when other
registered comments exist. Keep exact native ID/body/visibility checks shared,
then compare the full owned inventory separately. Retain failed fixture attempts
and rerun corrected scenarios with fresh resources instead of replaying writes.

## Duplicate comment target acceptance

Paired live Jira duplicate-target controls admit only fixed repeated/CSV spellings of
one active owned parent through the existing fixed Markdown recipe. Persist
intent and IDs before assertions, register independently bound resources before
behavior checks, and require complete native comment inventories to detect extra
writes. Never use a deduplicated output row alone as exactly-once evidence.

Worklog authoring parity uses literal effort seconds and native timestamp
spelling, never the production duration/time parsers for expected values. Bind
inline/file Markdown to independently authored ADF and complete per-lane
receipts; retain exact authenticated request counts, disabled-token no-request
previews and scoped storage effects.

Live worklog flag recipes translate only fixed effort/body/UTC-start argv through
registered native-input admission. Re-admit immediately before entry and retain
intent, returned IDs and acknowledgements first. Require a canonical resource ID
before constructing its native read path; bind ownership independently of desired
behavior. Complete parent inventories detect duplicate writes. Keep Markdown as
an exact owned direct-child regular file, reject leaf/ancestor symlinks and preserve
all observed effects without replay.

## Acceptance boundaries by resource

Paired rank acceptance binds ordered sources and a distinct anchor through
registered owned recipes before every process. Resolve the designated public
board filter and rank field, then require two complete independent native and
current-domain observations for each promised order. Preview/refusal steps retain
the earlier order; live steps must actually change it. Persist intent before
writes, never replay uncertain outcomes, and restore the same relative preimage
before comparing the next lane. Legacy preview completion-history writes must
stay scoped, ordered and capped; they are not a no-filesystem-effect path.
Keep the legacy multi-request boundary distinct from the current native
50-issue request limit. A locally refused legacy capability remains an open
parity gap even when its characterization test passes.

Refusal adapters require shared one-error envelope admission before indexing.
Exercise absent, null, empty and non-array error collections in named guards;
verify review warnings against that complete admission path.

URL-only open acceptance binds normalized keys, retained site prefixes and
explicit machine handoff state without credentials or HTTP. Inspect its native
public scoped history and exact owned file effects. Jira existence, browser
process handoff and GUI/page loading require separate evidence.

Release-note acceptance uses independent pinned source fixtures for complete
catalogs and raw Markdown. Bind aliases, selection and refusal reasons without
credentials or HTTP. Keep successful operation identity separate from root
failure attribution; piped output does not establish interactive pager behavior.

Epic read acceptance binds complete native epic/child observations to exact
legacy projections and current completeness facts. Retain legacy compact numeric
rounding explicitly; current native values stay exact. First-page caps and
search-index timing are separate from complete collection and mutation evidence.

Worklog write acceptance freezes an owned input and persists intent before entry.
Retain target-bound acknowledgements before independent native readback; never
replay an unresolved write. Compare common POST/GET effects separately from exact
controlled-server receipt preservation, including acknowledged incomplete records.

Worklog read acceptance binds independent native records and page facts per issue.
Retain legacy reduced values/cursors and current complete keyed records; exercise
empty targets independently and never infer a complete walk from a sparse page.

Board primer acceptance binds scoped native storage and the actual list consumer.
Keep native read timestamps separate from wrapper/publication acknowledgements;
verify changed forced values survive zero-request consumer reuse.

Epic cache acceptance preserves native fields/details and explicit legacy summaries.
Bind token walks to authenticated enhanced-search requests, then inspect stored
values and cached reuse. Never infer a complete legacy walk from its first page.

Batch collection acceptance binds native values and stored snapshots per resource,
then verifies forced changes, cached reuse, request order and explicit history effects.
Partial failures and cancellation remain separate evidence requirements.

Native cache acceptance binds an independent Jira issue identity, the actual
producer and owned scoped storage before testing local consumers. Keep ordinary
view's flat identity separate from keyed attachment/watcher read adapters.

Batch metadata previews bind resource order, deduplication and exact local file
effects. Retain legacy issue-history pollution as an observed difference; current
previews must leave storage unchanged and never claim a new collection or write.

Local cache acceptance binds complete owned file effects, foreign-scope retention
and exact refusal reasons. Keep preview plans separate from live removals and
retain explicit legacy history/classification differences.

Standalone field primers reuse the live duplicate-free membership and stored-order
owner used by batch collection. Keep fixed fixtures strict about authored order.

Metadata acceptance binds exact native reads and persisted owned snapshots,
retaining explicit legacy projections and current collection/publication facts.
Require changed forced data to survive cache reuse; keep untested variants open.

Board acceptance binds native identities and associated projects through actual
page requests and owned cache effects. Separate output windows from server walks,
retain type presence and timestamps, and never substitute location metadata.

Identity inspection acceptance binds independent native accounts and public
scope, preserves unknown fields and refuses partial current identities. Read
acceptance does not establish identity saving or provider selection.

User discovery acceptance preserves native email presence and fetched-row
continuation. Bind meaningful human receipts and independent visible identity;
empty results and short pages never prove directory completeness or permission.

Export acceptance distinguishes owned home and project directories, compares
exact artifacts and binds receipts to observed publication. Retain legacy
overwrite effects separately from current collision refusal and replacement.

Guide acceptance binds exact content and source metadata through both doors.
Preserve optional-member absence and strict original refusal JSON. Reading an
embedded workflow never proves its commands were executed.

Catalog acceptance uses independent native identities and context bounds.
Legacy catalog arrays have an exact-operation adapter; ordinary envelopes stay
strict. Metadata declarations do not establish rendering or write behavior.

Schema acceptance compares independent canonical path sets and complete version
field types. Legacy raw artifacts and current typed wrappers remain distinct;
resolve pooled shapes against the extracted artifact, never its envelope.

Synthetic acceptance runs in an owned working directory. Navigation checks bind
the exact Usage command and child rows; their help never proves child execution.

Paired JQL parsing keeps literal argv ahead of independent native diagnostics.
Runtime controls precede `--`. Retain the legacy method-based read-only refusal
separately from current read admission; require complete bounded request bodies.

JQL reference acceptance separates exact synthetic transport from independent
live Jira observations. Preserve complete current native metadata and each lane's
summary presence contract; do not normalize fixed-response array order.

Paired local authoring keeps independent literal expectations, strict original
JSON validation and complete process receipts. JQL reference remains remote;
local build/conversion/version checks cannot earn live Jira execution credit.

Local alias parity uses owned public fixtures and no credentials or Jira calls.
Bind receipts to the actual file and verify exact changes and skipped outcomes;
retain local acceptance separately from paired live Jira evidence.

Legacy targetless transition discovery maps to explicit transition list. Preserve
complete current native metadata; declare the legacy reduced projection separately.

Paired compound transitions must compare discovered terminal destinations and
independently verify both status and complete comment storage. Follow
`.claude/rules/tests.md`; one CLI invocation can require two non-atomic requests.

## Native fixture and process constraints

Release-note pager contracts and flag-matrix proofs run on Linux and macOS
using the shared native PTY fixture, including configured pager input, direct
`--no-pager` output, headless refusal to page and terminal-mode restoration.
Darwin cross-compilation does not establish native pager acceptance.

Elvish insertion setup is compiled only where its callers run: Linux and
Windows with `nativeconsole`. Shared helpers must not become unused on macOS
or ordinary Windows builds.

Directory-hint fixtures check both Bash 3.2 direct candidates and Bash 4+
Readline `plusdirs` delegation. Keep exact literal directory membership and file
exclusion checks; a modern-shell option is not required on older Bash. Select
legacy runtimes with `JIRA_TEST_BASH_BINARY` and exercise actual inserted argv.

Bash fixtures transport literal completion words through a quoted environment
value so MSYS startup cannot glob native caller argv. Preserve generated-script
execution, caller state and literal candidates. The Windows WSL launcher supplies
no native Bash evidence; explicit selection of it fails. Keep only unsupported
filename cases as named skips, retaining Windows space-path checks and Unix cases.

Update cleanup guards prove an owned candidate is readable before publication
and refuses a nonempty positioned read after return. Keep the closed-file error
assertion portable; Windows handle-stat errors do not establish os.ErrClosed.
Verify the guard catches an actually compiled omitted-close mutation.

Config-editor fixtures use the executing owned binary and shared shell quoting,
with literal argument markers to distinguish selection precedence. Preserve saved
work, context outcomes and regular-file checks on every host. POSIX permission
checks use named child subtests; Windows modes supply no ACL evidence and cannot
skip parent recovery checks.

Guide-export acceptance checks exact content, collisions and hard-link target
preservation on every host. Keep POSIX mode checks in named platform-specific
subtests; Windows modes do not prove ACL protection. Attempt native symbolic links
and skip only the unavailable Windows privilege, separately from invalid-path
validation. A skipped link case supplies no symbolic-link acceptance evidence.

Linux and Darwin audit terminals share a bounded, cancelable diagnostic collector
over their native PTY allocators. Retain independent stdout bytes, join the command
and collector, and refuse an unfinished drain after five seconds. Native diagnostic
capture is limited to one MiB. Verify retained writers, active cancellation, launch
failure and invisible diagnostics through owned helpers. Darwin compile checks are
preparation only; actual macOS execution remains a separate acceptance requirement.

Native Windows amd64 output-audit terminals use the shared invocation owner,
parent-owned job admission and the selected Go-image write observer. Admit silence
only after complete writer, loader, process and collector evidence. Keep exact
stdout separate; console presentation cannot establish empty diagnostics. Native
console setup precedes CLI admission and its accounting baseline. Job keepers are
never inherited, and cancellation before assignment still kills the waiting helper.
Preserve every required audit case and the named native guards described in
`.claude/rules/tests.md`. Other hosts require their own native acceptance evidence.

Native Windows compile-once command and startup-benchmark fixtures keep `.exe`
and bind public profile directories while retaining `PATHEXT`. Execute ordinary
native Go build helpers before crediting their acceptance; follow `.claude/rules/tests.md`.
Editor-preview fixtures use owned executables, and user-home overrides retain
Windows profile bindings.

When testing concurrent cache publication, preserve native Windows delete-sharing
semantics in the independent reader; follow `.claude/rules/tests.md`.
Hermetic Windows shell fixtures retain `PATHEXT` for native executable discovery
while isolating profile/config/cache directories. Verify callback execution
before crediting completion results; follow `.claude/rules/tests.md`.
Native browser acceptance uses an owned helper with exact URL argv and joined
termination. Keep machine-mode suppression and URL-only no-request assertions;
process handoff does not prove GUI rendering or page loading.
Browser-gating unit fixtures select an owned BROWSER value; default-launcher
guards assert the platform executable and every literal argument independently.
A download directory-swap fixture blocked by native Windows delete sharing must
prove original identity, denied rename, preserved stream failure and partial-file
cleanup. Keep actual redirected-directory proof separate; never count a failed
fixture rename as a successful replacement.
Capture the pre-fault Windows file ID through an opened handle, then close it
before exercising the writer; pathname Stat loads identity lazily.
Native Windows paging acceptance uses fresh hidden consoles, literal pager argv
and independently captured stdin. Verify navigation, cancellation, borrowed modes
and original-screen restoration. Console-cell display is not original write-byte
evidence; retain separate exact-byte contracts and all required matrix pairs.
Assign native paging helpers to an owned job before admitting descendants. Prove
parent cancellation joins the actual CLI and leaves no active job processes.
Elvish callback emission uses `$path:dev-null` from the executing shell, never
a hardcoded Unix path. Separate candidate identity from display descriptions
for every dynamic callback; exercise real matching and editor insertion.
Compose inline and CSV prefixes before rich formatting and exclude selected
values by their identity, including description-bearing metadata candidates.
Native Windows insertion guards use owned console key events and a bound Tab
completion marker, then compare literal observer argv and join the shell exit.

Externally selected native update tests keep a renamed installed basename while
preserving `.exe` on Windows, including disposable flag-matrix installation copies.
Version/formatter fixture expectations and selectors use the executing runtime
OS and architecture; retain exact output quoting, delimiters and field order.
Confirm the original and candidate version commands
actually execute before interpreting publication/download results. Test only
disposable installation copies; preserve selected artifacts and retained backups.

Native Windows editor acceptance uses the exported shared supervisor inside fresh
hidden consoles. Open the owned CONIN$/CONOUT$ handles explicitly; redirected
standard streams are not proof of terminal allocation. Observe both changed
native modes before releasing the real editor, then verify exact restoration,
process termination, literal argv and separation from result stdout. Cover failed
exit, cancellation, launch failure and second-terminal capture refusal. Run
`mise run test:editor:windows` on Windows; its nativeconsole-tagged gate fails
explicitly on other hosts. This owner evidence does not complete public command
acceptance or native macOS behavior.

Native Windows editor acceptance includes actual `config edit` and issue draft
resume in fresh hidden consoles through the existing hermetic binary harness.
Verify both altered/restored modes, literal argv, editor termination and typed
stdout. Failed or invalid saves retain recoverable work without publishing a
candidate; draft resume keeps its exact native source and performs no extra Jira
requests. `mise run test:editor:windows` runs both public commands and the shared
supervisor guards, retaining explicit wrong-host failure. Public cancellation
uses actual Control-C and Control-Break in the isolated console with an interrupt-resistant
editor: require exit 6, `canceled`, prompt termination, restored modes and exact
recoverable saved work. Send no event to the caller's console. Server-enforced
writes and native macOS acceptance remain separate requirements.

Native cache acceptance uses descriptor-relative readers matching Windows delete
sharing, with independent wrapper decoding; see `.claude/rules/tests.md`.

## Differential acceptance ownership

Differential CLI acceptance uses `tests/live/parity_*.go` and the shared live
process/ownership owners. Pin both digests, isolate the selected legacy profile,
verify public scope and native identity, and retain uncertain creation receipts
without replay. Preserve current acknowledgement checks and independent rich
native-field oracles. Follow `.claude/rules/tests.md`; tracer evidence is scoped
to its named scenarios and does not complete broader parity acceptance.
Read comparisons select explicit lane layouts and bind each result key; missing
arrays or keyed rows cannot establish an empty native collection. Pin owned
cache roots per lane and prove warm hits/forced refresh with actual requests.

Watcher parity translations derive canonical ownership from the exact legacy
argv, including shortcut targets and the verified current account. Preserve
legacy/current receipt differences and compare corresponding native preimages.
A failed step stops dependent writes; idempotence never authorizes replay.

Paired comment acceptance binds authored bodies and returned resources to owned
parents, preserves legacy bare-add intent and independently verifies visibility
and native deletion. Follow `.claude/rules/tests.md` for per-lane ordering,
pagination and no-request acceptance; passing cases do not establish full parity.

Dashboard settings acceptance covers filtered selection, input ownership,
environment overrides, preview cancellation, save failure retention, repeated
saves and external-edit refusal through the actual config owner. Check fixed
frame geometry and visible save/back controls at the smallest supported size.
Exercise every writable key through the real executable, file publication,
runtime reload and restart. Verify default-reset cancellation, unchanged unrelated
values and profiles, and the token/alias rows' dedicated-owner guidance. Check
saved connection identity, gateway admission, timeout cancellation, project JQL,
creation type selection, query-directory rebinding and local consumer payloads.
Use synthetic endpoints and providers; never contact a public gateway or mutate
the user's account as evidence. Display tests must observe each rendered feature,
and ownership tests must refuse Enter, Space and default reset for every key.

Exercise TUI issue switching and saved-view selection without changing authored
JQL. Check stale metadata/menu results after navigation, comment-page bounds and
duplicates, draft retention, explicit review, exact target binding and one write
per accepted action. Native synthetic HTTP acceptance covers transition,
assignment and comment payloads and terminal restoration; do not perform those
writes against a user's Jira profile as a development test.

Bulk TUI acceptance must bind the reviewed issue keys and each issue's native
workflow ID, default to cancellation and verify one attempt per accepted target.
Exercise mixed acknowledged/unconfirmed results, marked-selection retention,
query changes during a batch, exact-target draft reuse and bounded searchable
session history. Keep review and history controls visible at 40x12, 80x24 and
159x45. Use synthetic HTTP fixtures for native write acceptance.

Local TUI filter tests must preserve source issue identities across filtered
selection, ranges, refresh, direct switching and empty results. Facets distinguish
absent/null assignees and account IDs, combine with text and count duplicate
labels once per issue. Mouse hit tests include margins, headers, density and
modal focus. Browser tests inject the shared opener; clipboard acceptance uses
a synthetic terminal rather than changing the development host clipboard.

Summary/label TUI edits must read current fields with complete native identity,
bind the reviewed immutable issue ID and reuse shared metadata/edit writers.
Exercise default Cancel, no-op visits followed by remote changes, pending-input
draft retention, exact literal labels, empty-array clearing, unknown response
identity, unavailable metadata operations and uncertain writes without replay.
Native acceptance must verify one write per confirmed field and no unrelated
fields; keep controls visible at every supported terminal size.

TUI creation must use project/type metadata and the shared create submission and
receipt owner. Prove empty-list admission, cancel-default review, frozen native
payloads, omitted/default/null/empty distinctions, strict new Markdown conversion,
stale metadata/review rejection, retained draft inspection and no automatic replay
after uncertain attempts. Native synthetic acceptance verifies the actual create
payload once. Full-page editor tests cover hardware caret and controls at every
supported size. Review/inspection preserves blank lines while stripping terminal
controls; it must expose every authored field from the submitted snapshot.

Dashboard automatic paging acceptance must observe real next-cursor requests from
forward list navigation, one in-flight read, exact selection retention, and manual
retry after failure. Check stale-query rejection, disabled Settings behavior,
mouse navigation, empty/filtered views and the retained result budget. Native
acceptance proves automatic and manual-only loading through the real executable;
settings acceptance covers save, hot reload, restart and environment ownership.

Dashboard layout acceptance covers automatic/right/bottom docking, percentage
bounds and minimum readable panes at 40×12, 80×24 and 159×45 in both row densities.
Assert exact rendered dimensions, visible back/quit/menu controls, pane-specific
mouse selection/scrolling and focus after resizing. Prove scalar choices stay out
of boolean toggles, invalid percentages retain their editor, cancel restores the
layout, and temporary zoom retains selection and saved preferences. Native
acceptance exercises the real binary at all three sizes, saves/reloads/reopens
both layout settings and restores its borrowed terminal. Short stacked previews
must show description content while full details retain separate native fields.

Dashboard hierarchy acceptance exercises native parent IDs, custom type names,
child queries, back/selection preservation, and root-type Settings save/reload/
restart. Core guards distinguish omitted and null parents, bound ancestor reads,
retain partial denied observations and refuse cycles or changed identities.
Late hierarchy reads cannot replace another list or a closed Settings editor.
