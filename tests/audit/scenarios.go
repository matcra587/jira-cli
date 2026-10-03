//go:build outputaudit

package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Scenario is one runnable recipe: the exact argv for one operation ×
// category, executed by the harness once per applicable mode. Recipes
// are data — the harness is the only mechanism — and the expected
// matrix is derived from metadata, NEVER from this table, so a missing
// or mislabeled recipe is a named failure, not a silent skip.
type Scenario struct {
	// ID keys the capture ("<id>|<mode>").
	ID string
	// Op and Variant join the recipe to its inventory member.
	Op      string
	Variant string
	// Category must be one the member's metadata demands.
	Category Category
	// Args is the invocation WITHOUT the output selector; the harness
	// appends --output per captured mode so per-mode isolation is
	// structural.
	Args []string
	// Env adds variables to the harness's scrubbed allowlist for this
	// recipe only. The allowlist stays the base — a scenario states the
	// extra state it needs rather than inheriting a developer's shell.
	Env []string
	// Stdin is fed to the process on standard input; empty means none.
	Stdin string
	// Stub names the httptest handler serving the scenario's Jira
	// traffic; empty for invocations that must touch no server. The
	// stub's base URL reaches the binary via JIRA_BASE_URL (the settled
	// pre-P5 env seam).
	Stub string
	// ExpectExit is the contract exit code for the capture.
	ExpectExit int
	// Files are input fixtures written into the recipe's working
	// directory before it runs, keyed by relative path. A command whose
	// input arrives through a FILE (--json-input, --markdown-file) cannot
	// be exercised from Args alone, and a path baked into this table
	// would have to exist on every machine that runs the corpus.
	//
	// Seeded before the state fingerprint is taken (see seedFiles), so a
	// fixture is part of the baseline rather than looking like a write
	// the command made.
	Files map[string]string
	// Executable copies the cached CLI to this relative owned fixture path.
	// Installation detection depends on the running executable, not environment
	// hints; managed-channel recipes therefore need a real isolated copy.
	Executable string
	// ExpectCode is the taxonomy code a failure recipe must produce.
	// Required on every failure: exit codes are coarse (a whole class
	// shares 3), so a recipe pinned only by exit can exercise a
	// completely different failure than its name claims and still pass —
	// which is exactly what happened to config-edit-no-terminal.
	ExpectCode string
}

// Credentials the myself stub knows about. Recipes that reach it must
// present exactly the accepted pair.
const (
	stubEmail = "person@example.com"
	// stubToken is what the stub ACCEPTS — the value a login must read
	// from stdin and present.
	stubToken = "corpus-token"
	// stubStaleToken is what the environment holds instead. Different
	// from stubToken on purpose: with the same value in both places, a
	// login that ignored stdin and verified whatever was already
	// resolved would pass, which is precisely the regression the
	// verify-the-candidate rule exists to catch. A review proved the
	// old recipe could not tell them apart.
	stubStaleToken = "stale-environment-token"
)

// The wire bodies the "issue" and "comments" stubs serve for key TST-2
// are built in hostile.go (hostileIssueJSON, hostileCommentsJSON), not
// as literal JSON here: every Jira-authored string field they carry
// (summary, status/issuetype/priority names, assignee/reporter display
// names, labels, the issue's own key, comment author, comment created,
// and the comment's ADF body text and marks) is attacked with
// attackPayload — the H4 sanitization axis's single hostile fixture,
// reused by both success recipes below (issue-view-success and
// issue-comment-list-success) so the SAME capture that already proves
// the M-axis contract also proves sanitization, per the design's "one
// fixture reused by every read scenario" rule. Both fixtures also keep
// the ORIGINAL fixture's two structural proofs intact and untouched by
// the payload: the M10 unmodeled-field passthrough
// (customfield_10011/customUnmodeledField) and the pagination-hint
// mismatch (a reported total larger than the comments actually
// returned).

// stubServers maps a recipe's stub name to its handler constructor.
// Handlers cannot ride data, so they live here by name; a recipe naming
// a missing stub fails the inventory reconciliation.
var stubServers = map[string]func() http.Handler{
	// The first stub-backed recipe. `auth status` is the first surface
	// that talks to Jira at all, so it is where the corpus stops being
	// client-free — the handler answers /myself and nothing else, so a
	// recipe that strays off that path fails loudly rather than being
	// quietly served.
	"myself": func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/rest/api/3/myself" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			// The exact pair, not merely "some credential". A stub that
			// accepts anything cannot tell a login that verified the
			// candidate token from one that verified whatever was
			// already stored — which is the regression the
			// verify-before-store rule exists to prevent.
			user, pass, ok := r.BasicAuth()
			if !ok || user != stubEmail || pass != stubToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"accountId":"5b10a2844c20165700ede21g","displayName":"Corpus Account","emailAddress":"person@example.com","active":true}`)); err != nil {
				return
			}
		})
	},
	// The second stub-backed surface: GET /rest/api/3/issue/{key}.
	// Answers exactly one key (TST-2, the success fixture above); every
	// other key or path is a 404, so a recipe that strays off the
	// covered key fails loudly rather than being quietly served. The
	// same credential pair as "myself" is required — a strayed recipe
	// carrying the wrong creds must not read as a not-found instead of
	// an auth failure.
	"issue": func() http.Handler {
		const prefix = "/rest/api/3/issue/"
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// This surface is read-only; a recipe that reaches it with
			// any other method has strayed off the operation it claims to
			// cover and must fail loudly rather than get served a GET
			// fixture that proves nothing about the method it actually
			// used.
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			key, ok := strings.CutPrefix(r.URL.Path, prefix)
			if !ok || key == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			user, pass, ok := r.BasicAuth()
			if !ok || user != stubEmail || pass != stubToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if key != "TST-2" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write(hostileIssueJSON()); err != nil {
				return
			}
		})
	},
	// The third stub-backed surface: GET
	// /rest/api/3/issue/{key}/comment. Answers exactly one key
	// (TST-2); every other key or path is a 404, matching the "issue"
	// stub's own posture, and the same credential pair is required.
	"comments": func() http.Handler {
		const prefix = "/rest/api/3/issue/"
		const suffix = "/comment"
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Same read-only posture as the "issue" stub above: a wrong
			// method must fail loudly, not get served a fixture that
			// proves nothing about the method actually used.
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			key, ok := strings.CutPrefix(r.URL.Path, prefix)
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			key, ok = strings.CutSuffix(key, suffix)
			if !ok || key == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			user, pass, ok := r.BasicAuth()
			if !ok || user != stubEmail || pass != stubToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if key != "TST-2" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write(hostileCommentsJSON()); err != nil {
				return
			}
		})
	},
	// The issue-link surface: GET /rest/api/3/issue/{key} again, but
	// answering the ONE field `issue link list` asks for (issuelinks) for
	// the TWO keys its success recipe passes. A separate stub from
	// "issue" rather than an extension of it — see hostileIssueLinksJSON
	// for why the two fixtures stay apart even though the path is shared.
	//
	// TST-2 carries two links (one each direction); TST-3 carries none,
	// so ONE capture covers both the populated table and the empty-array
	// contract a caller iterating data.results[].data.links depends on.
	// Every other key is a 404 — which is also the failure recipe's own
	// path, and the same loud posture every sibling stub takes.
	"issuelinks": func() http.Handler {
		const prefix = "/rest/api/3/issue/"
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			key, ok := strings.CutPrefix(r.URL.Path, prefix)
			if !ok || key == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			user, pass, ok := r.BasicAuth()
			if !ok || user != stubEmail || pass != stubToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var body []byte
			switch key {
			case "TST-2":
				body = hostileIssueLinksJSON(key, 2)
			case "TST-3":
				body = hostileIssueLinksJSON(key, 0)
			default:
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write(body); err != nil {
				return
			}
		})
	},
	// The issue-attachment surface: GET /rest/api/3/issue/{key} once
	// more, answering the ONE field `issue attachment list` asks for
	// (attachment) for the TWO keys its success recipe passes. A separate
	// stub from "issue" and "issuelinks" for the reason
	// hostileIssueAttachmentsJSON records: same path, different question.
	//
	// TST-2 carries three attachments; TST-3 carries none, so ONE capture
	// covers both the populated table and the empty-array contract a
	// caller iterating data.results[].data.attachments depends on. Every
	// other key is a 404 — which is also the failure recipe's own path,
	// and the same loud posture every sibling stub takes.
	"attachments": func() http.Handler {
		const prefix = "/rest/api/3/issue/"
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			key, ok := strings.CutPrefix(r.URL.Path, prefix)
			if !ok || key == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			user, pass, ok := r.BasicAuth()
			if !ok || user != stubEmail || pass != stubToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var body []byte
			switch key {
			case "TST-2":
				body = hostileIssueAttachmentsJSON(key, 3)
			case "TST-3":
				body = hostileIssueAttachmentsJSON(key, 0)
			default:
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write(body); err != nil {
				return
			}
		})
	},
	// THE FIRST WRITE STUB: POST /rest/api/3/issue/{key}/comment.
	//
	// It is strict about the method in the OTHER direction from every
	// stub above. Those reject non-GET because a write reaching a read
	// path is the bug they guard; this one rejects non-POST, so a
	// command that reached this path with a GET — or a dry run that
	// contacted the site at all — fails loudly instead of being served.
	//
	// It answers 201 with the created comment, which is Jira's real
	// behavior and the reason `comment add` publishes the comment with
	// no --verify flag: the object is already in the response.
	//
	// It also ECHOES the posted body back, so the capture proves the
	// request survived the round trip byte-for-byte rather than merely
	// proving something was posted.
	"comment-add": func() http.Handler {
		const prefix = "/rest/api/3/issue/"
		const suffix = "/comment"
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			path, ok := strings.CutPrefix(r.URL.Path, prefix)
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			key, ok := strings.CutSuffix(path, suffix)
			if !ok || key == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			user, pass, ok := r.BasicAuth()
			if !ok || user != stubEmail || pass != stubToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			// Only the keys the success recipe names. Every other key is
			// a 404, which is also the failure recipe's own path.
			if key != "TST-2" && key != "TST-3" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			posted, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			if _, err := w.Write(createdCommentJSON(key, "10"+key, posted)); err != nil {
				return
			}
		})
	},
	// THE FIRST STUB SERVING A COMMAND WHOSE REQUEST SHAPE IS DECIDED BY
	// AN EARLIER RESPONSE. It answers three paths, and the relationship
	// between them is the whole contract under test:
	//
	//   GET  /issue/{key}/transitions — the workflow, carrying hasScreen
	//   POST /issue/{key}/transitions — the move itself, 204
	//   POST /issue/{key}/comment     — the separate comment, 201
	//
	// The keys are shaped to exercise BOTH routes in one capture: TST-2's
	// transition HAS a screen, so its comment must ride the transition
	// request; TST-3's has none, so its comment must arrive on the
	// comment path instead.
	//
	// It ENFORCES that rather than merely permitting it. A transition
	// POST for TST-3 carrying an `update` is answered 400, so a CLI that
	// composed the comment into a screenless transition — the exact bug
	// design §3.1 exists to prevent, and the one every naive
	// implementation ships — fails the capture loudly instead of being
	// served a cheerful 204 and reporting success. A stub that accepted
	// both shapes could not tell the correct implementation from the
	// broken one.
	"transitions": func() http.Handler {
		const prefix = "/rest/api/3/issue/"
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path, ok := strings.CutPrefix(r.URL.Path, prefix)
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			user, pass, ok := r.BasicAuth()
			if !ok || user != stubEmail || pass != stubToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			key, tail, ok := strings.Cut(path, "/")
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			// Only the keys the recipes name. Every other key is a 404,
			// which is the failure recipe's own path — and it 404s on the
			// LIST, before any write, which is what makes that recipe a
			// total failure with nothing performed.
			screened, known := corpusTransitionScreens[key]
			if !known {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			switch {
			case tail == "transitions" && r.Method == http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write(hostileTransitionsJSON(screened)); err != nil {
					return
				}
			case tail == "transitions" && r.Method == http.MethodPost:
				serveTransitionPost(w, r, screened)
			case tail == "comment" && r.Method == http.MethodPost:
				serveSeparateComment(w, r, screened)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		})
	},
	// The issue-watcher READ surface, and the first stub here NOT serving
	// GET /rest/api/3/issue/{key}: watchers have a list endpoint of their
	// own (GET .../issue/{key}/watchers), which links and attachments do
	// not, so this handler answers a different path entirely.
	//
	// (This doc comment sat orphaned above the comment-add stub until the
	// watcher WRITE stub below was added and the mismatch became
	// obvious — it describes this handler, and now sits on it.)
	//
	// TST-2 carries three watchers; TST-3 carries none AND reports a
	// watchCount of zero, so one capture covers both the populated table
	// and the genuinely-unwatched contract. TST-4 is the case only this
	// command has: watchCount 7 with no watchers array at all, which is
	// how Jira answers a caller lacking the "View voters and watchers"
	// permission — the capture pins that the CLI reports the count
	// rather than claiming the issue is unwatched. Every other key is a
	// 404, which is also the failure recipe's own path.
	"watchers": func() http.Handler {
		const prefix = "/rest/api/3/issue/"
		const suffix = "/watchers"
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			path, ok := strings.CutPrefix(r.URL.Path, prefix)
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			key, ok := strings.CutSuffix(path, suffix)
			if !ok || key == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			user, pass, ok := r.BasicAuth()
			if !ok || user != stubEmail || pass != stubToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			var body []byte
			switch key {
			case "TST-2":
				body = hostileIssueWatchersJSON(3, 3, true)
			case "TST-3":
				body = hostileIssueWatchersJSON(0, 0, false)
			case "TST-4":
				body = hostileIssueWatchersJSON(0, 7, false)
			default:
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write(body); err != nil {
				return
			}
		})
	},
	// The comment-EDIT surface: PUT /rest/api/3/issue/{key}/comment/{id}.
	//
	// It rejects every method but PUT, which is stricter than it looks and
	// deliberately so: a CLI that reached for POST here would be adding a
	// comment rather than editing one, and a stub tolerant of both could
	// not tell those apart. The 404-on-any-other-id posture matches every
	// sibling — the failure recipe's own path runs through it.
	//
	// It answers 200 with the stored comment (never 201 — nothing was
	// created), ECHOING the posted body back so the capture proves the
	// document survived the round trip byte-for-byte rather than merely
	// proving something was sent.
	"comment-edit": func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			key, commentID, ok := cutCommentPath(r.URL.Path)
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if !stubAuthorized(w, r) {
				return
			}
			if key != corpusCommentKey || commentID != corpusCommentID {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			posted, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write(createdCommentJSON(key, commentID, posted)); err != nil {
				return
			}
		})
	},
	// The comment-DELETE surface: DELETE
	// /rest/api/3/issue/{key}/comment/{id}, answering 204 with no body.
	//
	// The one stub here whose whole contract is that it is REACHED at all.
	// Its command's success capture proves two things a 204 cannot say by
	// itself: that --force skipped the confirmation (a run that prompted
	// instead would hang the harness, which drives the binary through
	// pipes with no terminal to answer at), and that the dry-run capture
	// never arrives here even though a stub is named and listening.
	"comment-delete": func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			key, commentID, ok := cutCommentPath(r.URL.Path)
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if !stubAuthorized(w, r) {
				return
			}
			if key != corpusCommentKey || commentID != corpusCommentID {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	},
	// The watcher WRITE surface, serving both verbs and the readback each
	// one makes afterwards — three request shapes on ONE path:
	//
	//   POST   /issue/{key}/watchers          — add, body is a bare string
	//   DELETE /issue/{key}/watchers?accountId — remove
	//   GET    /issue/{key}/watchers          — the readback
	//
	// The GET is what makes this a separate stub from "watchers" above
	// rather than an extension of it: that one rejects every non-GET, and
	// correctly, since its command only reads. Here all three are legal
	// and the relationship between them is the contract — a verb that
	// wrote and then never read back, or read back without writing, shows
	// up in the capture.
	//
	// It ENFORCES the request shapes rather than permitting anything that
	// arrives. A POST whose body is not a bare JSON string is answered
	// 400, so a CLI that sent {"accountId": …} — the shape every
	// neighboring endpoint takes, and the obvious wrong guess — fails
	// loudly instead of being served a cheerful 204. A DELETE with no
	// accountId query is answered 400 for the mirror reason.
	"watchers-write": func() http.Handler {
		const prefix = "/rest/api/3/issue/"
		const suffix = "/watchers"
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path, ok := strings.CutPrefix(r.URL.Path, prefix)
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			key, ok := strings.CutSuffix(path, suffix)
			if !ok || key == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if !stubAuthorized(w, r) {
				return
			}
			// Only the keys the success recipe names. Every other key is a
			// 404, which is also the failure recipe's own path.
			if key != "TST-2" && key != "TST-3" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write(hostileIssueWatchersJSON(3, 3, true)); err != nil {
					return
				}
			case http.MethodPost:
				serveAddWatcher(w, r)
			case http.MethodDelete:
				serveRemoveWatcher(w, r)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		})
	},
	// The issue-LINK write surface, serving the type list every create
	// resolves against and the link endpoint itself:
	//
	//   GET  /issueLinkType     — the site's configured types
	//   POST /issueLink         — the link itself, 201 with NO body
	//   DELETE /issueLink/{id}  — removing one
	//
	// The type list is what makes this stub interesting rather than
	// mechanical. It carries an asymmetric type (Blocks, whose two
	// phrasings differ) so the capture proves the CLI resolved a PHRASE
	// into the right end of the link, and the POST ENFORCES that: a
	// request whose two ends do not match the direction the recipe asked
	// for is answered 400, so an implementation that reversed them fails
	// the capture loudly instead of being served a cheerful 201.
	"issue-links": func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !stubAuthorized(w, r) {
				return
			}
			switch {
			case r.URL.Path == "/rest/api/3/issueLinkType" && r.Method == http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write(corpusLinkTypesJSON()); err != nil {
					return
				}
			case r.URL.Path == "/rest/api/3/issueLink" && r.Method == http.MethodPost:
				serveLinkIssues(w, r)
			case strings.HasPrefix(r.URL.Path, "/rest/api/3/issueLink/") && r.Method == http.MethodDelete:
				serveDeleteIssueLink(w, r)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		})
	},
	// The REMOTE-link surface, serving all three weblink verbs on the two
	// paths Jira gives them:
	//
	//   GET    /issue/{key}/remotelink        — list
	//   POST   /issue/{key}/remotelink        — add, 201 new / 200 replaced
	//   DELETE /issue/{key}/remotelink/{id}   — delete
	//
	// The POST's status is the assertion: it answers 201 for a request
	// with no globalId and 200 for one carrying corpusWeblinkGlobalID, so
	// the capture proves the CLI reports ADDED and REPLACED as the
	// different outcomes they are rather than collapsing both into
	// success. Nothing else in this CLI reads a status code as data.
	"weblinks": func() http.Handler {
		const prefix = "/rest/api/3/issue/"
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rest, ok := strings.CutPrefix(r.URL.Path, prefix)
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if !stubAuthorized(w, r) {
				return
			}
			key, tail, ok := strings.Cut(rest, "/")
			if !ok || key == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			// Only the keys the recipes name. Every other key is a 404,
			// which is also the failure recipes' own path.
			if key != "TST-2" && key != "TST-3" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			switch {
			case tail == "remotelink" && r.Method == http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				if _, err := w.Write(corpusWeblinksJSON(key)); err != nil {
					return
				}
			case tail == "remotelink" && r.Method == http.MethodPost:
				serveAddRemoteLink(w, r)
			case strings.HasPrefix(tail, "remotelink/") && r.Method == http.MethodDelete:
				id := strings.TrimPrefix(tail, "remotelink/")
				if id != corpusWeblinkID {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		})
	},
	// The ATTACHMENT surface, serving the upload, the two reads a
	// download makes, and the delete:
	//
	//   POST   /issue/{key}/attachments   — upload, 200 with the array
	//   GET    /attachment/{id}           — metadata, where the filename is
	//   GET    /attachment/content/{id}   — the bytes
	//   DELETE /attachment/{id}           — removal, 204
	//
	// The upload path ENFORCES two things the CLI could get wrong
	// silently. It requires the X-Atlassian-Token header, which Jira
	// itself requires and without which a real upload fails with an XSRF
	// complaint that looks nothing like a missing header. And it requires
	// a multipart body with at least one part, so a CLI that composed
	// JSON — the shape every other write on this client sends — fails the
	// capture loudly rather than being served a cheerful 200.
	//
	// The metadata path is what makes the download capture a filename
	// test: it answers with a HOSTILE name (corpusAttachmentFilename), so
	// the capture proves the CLI reduced it rather than writing where it
	// was told. That is the one assertion in this corpus about where a
	// file lands on the machine running the tests.
	"attachments-write": func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !stubAuthorized(w, r) {
				return
			}
			path := r.URL.Path
			switch {
			case strings.HasSuffix(path, "/attachments") && r.Method == http.MethodPost:
				serveAddAttachment(w, r)
			case strings.HasPrefix(path, "/rest/api/3/attachment/content/") && r.Method == http.MethodGet:
				serveAttachmentContent(w, r)
			case strings.HasPrefix(path, "/rest/api/3/attachment/") && r.Method == http.MethodGet:
				serveAttachmentMeta(w, r)
			case strings.HasPrefix(path, "/rest/api/3/attachment/") && r.Method == http.MethodDelete:
				serveDeleteAttachment(w, r)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		})
	},
	// The LIFECYCLE surface: deleting an issue, reading one and creating
	// its clone, and the Agile rank endpoint.
	//
	//   DELETE /issue/{key}?deleteSubtasks=  — delete, 204
	//   GET    /issue/{key}                  — the clone's source read
	//   POST   /issue                        — the clone itself, 201
	//   PUT    /rest/agile/1.0/issue/rank    — rank, 204
	//
	// The rank path is the reason this stub is worth its length. It is the
	// ONLY request this CLI makes outside /rest/api/3, and the capture is
	// what proves the transport reaches it at all — the client's base URL
	// is the site root, so nothing about the Agile prefix was guaranteed
	// until something ran against it. A 404 here would mean the path
	// guard, not the command, and that is exactly the failure worth
	// catching in CI rather than on a tenant.
	//
	// It ENFORCES the two request shapes that could go wrong silently:
	// the delete's deleteSubtasks parameter must be present (the CLI sends
	// it always, explicitly, rather than relying on the server default),
	// and the rank body must carry every issue in ONE array with exactly
	// one anchor.
	"lifecycle": func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !stubAuthorized(w, r) {
				return
			}
			switch {
			case r.URL.Path == "/rest/agile/1.0/issue/rank" && r.Method == http.MethodPut:
				serveRank(w, r)
			case r.URL.Path == "/rest/api/3/issue" && r.Method == http.MethodPost:
				serveCloneCreate(w, r)
			case strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/") && r.Method == http.MethodDelete:
				serveIssueDelete(w, r)
			case strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/") && r.Method == http.MethodGet:
				serveCloneSource(w, r)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		})
	},
	// The fourth stub-backed surface: POST /rest/api/3/search/jql AND its
	// sibling POST /rest/api/3/search/approximate-count — one handler,
	// routed by path, because a Scenario names exactly one Stub and
	// issue-list-success needs both endpoints behind it (list.go's
	// fetchCountEstimate fires the count call from the SAME human
	// invocation that already walked /search/jql). /search/jql answers
	// exactly one JQL body — the one issue-list-success's own recipe
	// composes — with a genuine TWO-page sequence, routed by the
	// request's own nextPageToken (empty for the first page,
	// searchPageTwoToken for the second): issue-list-success passes
	// --limit 2, so issue.list's own auto-pagination loop (list.go's
	// autoPaginate) actually walks both pages before it has collected
	// enough, exercising the paging loop for real rather than a single
	// response merely claiming there is more. Every other JQL (including
	// the deliberately malformed one issue-list-failure sends), and any
	// nextPageToken this sequence never hands out, gets a Jira-shaped
	// 400 — matching the "issue"/"comments" stubs' own posture of
	// failing loudly rather than serving a fixture that proves nothing
	// about the request actually made. Both paths are POST (client.go's
	// own SearchIssues and CountIssues doc comments explain why: query
	// strings too large for a GET), so the method check goes the other
	// way from every sibling stub above.
	//
	// The two-page sequence above ends with is_last still false (page
	// two's own doc comment, hostile.go) — --limit 2 is satisfied before
	// the walk would ever see a true is_last — so this recipe's human
	// capture is exactly the "more results exist" shape
	// fetchCountEstimate's own gate fires on: it exercises the count
	// call for real, not just the count-free fallback wording.
	"search": func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/rest/api/3/search/jql":
				serveSearchJQL(w, r)
			case "/rest/api/3/search/approximate-count":
				serveSearchApproximateCount(w, r)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})
	},
	// THE FIRST STUB FOR A WRITE WITH NO KEY IN ITS PATH: POST
	// /rest/api/3/issue. Every write stub above is addressed to an issue
	// that already exists; this one is how an issue starts existing, so
	// the key travels the other way — out, in the response.
	//
	// It VALIDATES the payload rather than merely accepting it, which is
	// what makes the capture worth taking. A create whose composed
	// payload lost its project, its type or its summary would be answered
	// 400 here instead of being served a cheerful 201, so the recipe
	// proves the three routes composed a request Jira would actually
	// take. The unknown-project branch is the failure recipe's own path.
	//
	// It also serves GET /rest/api/3/issue/{key}, because --verify's
	// readback is a second request to a different path and a stub that
	// answered only the POST could not exercise it.
	"link-types": corpusLinkTypesStub,
	"issue-create": func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok || user != stubEmail || pass != stubToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Method == http.MethodGet {
				serveIssueWriteRead(w, r)
				return
			}
			if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/issue" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			posted, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var body struct {
				Fields struct {
					Project   struct{ Key string }  `json:"project"`
					IssueType struct{ Name string } `json:"issuetype"`
					Summary   string                `json:"summary"`
				} `json:"fields"`
			}
			if err := json.Unmarshal(posted, &body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			// Jira rejects a create missing any of these, so the stub does
			// too — a CLI that dropped one on the way must not pass here.
			if body.Fields.IssueType.Name == "" || body.Fields.Summary == "" || body.Fields.Project.Key != corpusCreateProject {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			if _, err := w.Write([]byte(`{"id":"10101","key":"` + corpusCreatedKey +
				`","self":"https://example.atlassian.net/rest/api/3/issue/10101","transition":{"status":200,"warningCollection":{"warnings":["transition receipt"]}},"watchers":{"status":204},"future":{"opaque":"native receipt sentinel","values":[null,false,13]}}`)); err != nil {
				return
			}
		})
	},
	// PUT /rest/api/3/issue/{key}, answering 204 with no body — which is
	// the endpoint's real shape and the reason issue.edit's per-key data
	// is empty without --verify.
	//
	// It is strict about the method for the comment-add stub's reason,
	// one verb over: a dry run that contacted the site, or an edit that
	// somehow issued a POST, fails loudly instead of being served. It
	// also refuses an empty payload, so an edit that composed nothing
	// cannot pass by sending Jira a request that changes nothing.
	"issue-edit": func() http.Handler {
		const prefix = "/rest/api/3/issue/"
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok || user != stubEmail || pass != stubToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Method == http.MethodGet && r.URL.Path == myselfPath {
				serveIssueWriteRead(w, r)
				return
			}
			key, ok := strings.CutPrefix(r.URL.Path, prefix)
			if !ok || key == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.Method == http.MethodGet {
				serveIssueWriteRead(w, r)
				return
			}
			if r.Method != http.MethodPut {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			// Only the keys the success recipe names; every other key is a
			// 404, which is the failure recipe's own path.
			if key != "TST-2" && key != "TST-3" && key != "KEY-2" && key != "KEY-3" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			posted, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var body struct {
				Fields map[string]json.RawMessage `json:"fields"`
				Update map[string]json.RawMessage `json:"update"`
			}
			if err := json.Unmarshal(posted, &body); err != nil || (len(body.Fields) == 0 && len(body.Update) == 0) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	},
}

// link-types is the only READ stub whose command may not call it at all:
// `issue link types` answers from a disk cache when one is fresh. The
// corpus runs each capture with an isolated HOME (the harness's own
// fixture directory), so the cache is always cold and the read always
// happens — which is what makes the capture deterministic. A recipe that
// shared a warm cache between captures would flip data.cached run to run.
var corpusLinkTypesStub = func() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/issueLinkType" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != stubEmail || pass != stubToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"issueLinkTypes":[` +
			`{"id":"10000","name":"Blocks","inward":"is blocked by","outward":"blocks"},` +
			`{"id":"10001","name":"Relates","inward":"relates to","outward":"relates to"}` +
			`]}`)); err != nil {
			return
		}
	})
}

// corpusCreateProject and corpusCreatedKey are the one project the create
// stub accepts and the key it hands back. Named rather than inlined so
// the stub's validation and the recipes cannot disagree about them — a
// success recipe naming a project the stub rejects would fail as a 400
// that looks like a CLI bug.
const (
	corpusCreateProject = "TST"
	corpusCreatedKey    = "TST-77"
)

// myselfPath is the endpoint the @me resolver reads, named once so the
// stub's route and the assertion about it cannot disagree.
const myselfPath = "/rest/api/3/myself"

// corpusAccountID is the account id the stub resolves @me to. Distinct
// from any literal in a recipe, so a capture showing it proves resolution
// happened rather than echoing something the argv already contained.
const corpusAccountID = "5b10a2844c20165700ede21g"

// serveIssueWriteRead answers the two GETs a write can make: the @me
// lookup, and the --verify readback.
//
// One handler because both stubs need both, and the routing between them
// is a single path comparison — splitting it would put the same two-line
// decision in two places.
func serveIssueWriteRead(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == myselfPath {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"accountId":"` + corpusAccountID + `","displayName":"Corpus User"}`)); err != nil {
			return
		}
		return
	}
	serveCreatedIssueReadback(w, r)
}

// serveCreatedIssueReadback answers the GET that --verify makes after a
// write. Shared by the create and edit stubs because the readback is the
// same request in both cases: verifyIssue asks for summary, status and
// updated whichever write it is confirming.
func serveCreatedIssueReadback(w http.ResponseWriter, r *http.Request) {
	key, ok := strings.CutPrefix(r.URL.Path, "/rest/api/3/issue/")
	if !ok || key == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(`{"id":"10101","key":"` + key +
		`","fields":{"summary":"Rotate the signing key","status":{"name":"To Do"},"updated":"2026-08-13T10:00:00.000+0000"}}`)); err != nil {
		return
	}
}

// serveSearchJQL is the "search" stub's /search/jql branch — pulled out
// of the map literal above only so serveSearchApproximateCount below
// can sit next to it as its own named sibling rather than a second
// anonymous closure nested inside the first.
func serveSearchJQL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	user, pass, ok := r.BasicAuth()
	if !ok || user != stubEmail || pass != stubToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var body struct {
		JQL           string `json:"jql"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var page []byte
	switch {
	case body.JQL != `project in ("TST")` && body.JQL != "assignee = currentUser() ORDER BY updated ASC":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte(`{"errorMessages":["The JQL query is invalid"],"errors":{}}`)); err != nil {
			return
		}
		return
	case body.NextPageToken == "":
		page = hostileSearchPageOneJSON()
	case body.NextPageToken == searchPageTwoToken:
		page = hostileSearchPageTwoJSON()
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte(`{"errorMessages":["Unknown nextPageToken"],"errors":{}}`)); err != nil {
			return
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(page); err != nil {
		return
	}
}

// serveSearchApproximateCount is the "search" stub's /search/
// approximate-count branch: issue.list's own optional count fetch
// (list.go's fetchCountEstimate), reached only for a HUMAN capture of
// issue-list-success (the machine captures never call it at all — see
// fetchCountEstimate's own machine-output gate, which this stub does
// not need to enforce since the client itself never dials out for it).
// Same recognized-JQL/credential/method posture as serveSearchJQL
// above, failing loudly on anything else rather than serving a fixture
// that proves nothing about the request actually made. The response
// carries no Jira-authored STRING content to attack — JQLCountResultsBean
// (internal/jira/count.go) declares one bare integer — so, unlike
// hostileSearchPageOneJSON's own fields, there is nothing here for H4 to
// strip; this fixture stays clean rather than manufacturing a hostile
// shape the wire contract cannot actually carry.
func serveSearchApproximateCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	user, pass, ok := r.BasicAuth()
	if !ok || user != stubEmail || pass != stubToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var body struct {
		JQL string `json:"jql"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if body.JQL != `project in ("TST")` && body.JQL != "assignee = currentUser() ORDER BY updated ASC" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte(`{"errorMessages":["The JQL query is invalid"],"errors":{}}`)); err != nil {
			return
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(searchApproximateCountJSON()); err != nil {
		return
	}
}

// scenarios is the committed recipe table for the live tree. Root emits
// typed discovery, completion emits a shell artifact, and version reports
// build facts. Pre-parse failures retain full machine frames; unknown command
// recipes are exercised separately because they cannot resolve in inventory.
var scenarios = []Scenario{
	{ID: "release-notes-latest", Op: "release.notes", Category: CategorySuccess, Args: []string{"release-notes", "--latest"}},
	{ID: "release-notes-unknown-version", Op: "release.notes", Category: CategoryFailure, Args: []string{"release-notes", "missing"}, ExpectExit: 3, ExpectCode: "arg_value_invalid"},
	{ID: "release-notes-help", Op: "release.notes", Category: CategoryHelp, Args: []string{"release-notes", "--help"}},
	{
		ID:         "root-success",
		Op:         "root",
		Category:   CategorySuccess,
		Args:       nil,
		ExpectExit: 0,
	},
	{
		ID:       "root-unknown-flag",
		Op:       "root",
		Category: CategoryFailure,
		// An unknown flag fails at parse time — and still lands the full
		// typed frame on stdout in machine modes: a typo is the most
		// common agent failure, so it is exactly the failure that must
		// stay parseable. Exit 3, human line on stderr.
		Args:       []string{"--definitely-not-a-flag"},
		ExpectExit: 3,
		ExpectCode: "flag_unknown",
	},
	{
		ID:         "root-help",
		Op:         "root",
		Category:   CategoryHelp,
		Args:       []string{"--help"},
		ExpectExit: 0,
	},
	{
		ID:         "version-success",
		Op:         "version",
		Category:   CategorySuccess,
		Args:       []string{"version"},
		ExpectExit: 0,
	},
	{
		ID:         "version-unknown-flag",
		Op:         "version",
		Category:   CategoryFailure,
		Args:       []string{"version", "--definitely-not-a-flag"},
		ExpectExit: 3,
		ExpectCode: "flag_unknown",
	},
	{
		ID:         "version-help",
		Op:         "version",
		Category:   CategoryHelp,
		Args:       []string{"version", "--help"},
		ExpectExit: 0,
	},
	{
		ID:         "config-get-stored-false",
		Op:         "config.get",
		Category:   CategorySuccess,
		Args:       []string{"config", "get", "read_only", "--config=settings.toml"},
		Files:      map[string]string{"settings.toml": "read_only=false\n"},
		Env:        []string{"JIRA_READ_ONLY=true", "JIRA_KEYRING=false"},
		ExpectExit: 0,
	},
	{
		ID:         "config-get-unknown-key",
		Op:         "config.get",
		Category:   CategoryFailure,
		Args:       []string{"config", "get", "read_ony"},
		ExpectExit: 3,
		ExpectCode: "arg_value_invalid",
	},
	{
		ID:         "config-get-help",
		Op:         "config.get",
		Category:   CategoryHelp,
		Args:       []string{"config", "get", "--help"},
		ExpectExit: 0,
	},
	{
		ID:       "config-list-success",
		Op:       "config.list",
		Category: CategorySuccess,
		// No config file exists under the corpus HOME, which is the
		// state that matters most: the command has to answer for a
		// machine that never configured anything, rather than assuming a
		// file it can read.
		Args: []string{"config", "list"},
		// Keep the value column independent of randomized HOME length.
		// Default directory selection is covered by config's owner tests.
		Env:        []string{"JIRA_QUERIES_PATH=queries"},
		ExpectExit: 0,
	},
	{
		ID:         "config-list-unknown-flag",
		Op:         "config.list",
		Category:   CategoryFailure,
		Args:       []string{"config", "list", "--definitely-not-a-flag"},
		ExpectExit: 3,
		ExpectCode: "flag_unknown",
	},
	{
		ID:         "config-list-help",
		Op:         "config.list",
		Category:   CategoryHelp,
		Args:       []string{"config", "list", "--help"},
		ExpectExit: 0,
	},
	{
		ID:         "config-set-stored-false",
		Op:         "config.set",
		Category:   CategorySuccess,
		Args:       []string{"config", "set", "read_only", "false", "--config=settings.toml", "--no-input"},
		Files:      map[string]string{"settings.toml": "read_only=true\n"},
		Env:        []string{"JIRA_READ_ONLY=true", "JIRA_KEYRING=false"},
		ExpectExit: 0,
	},
	{
		ID:         "config-set-preview-false",
		Op:         "config.set",
		Category:   CategoryDryRun,
		Args:       []string{"config", "set", "read_only", "false", "--config=settings.toml", "--dry-run", "--no-input"},
		Files:      map[string]string{"settings.toml": "read_only=true\n"},
		Env:        []string{"JIRA_READ_ONLY=true", "JIRA_KEYRING=false"},
		ExpectExit: 0,
	},
	{
		ID:         "config-set-invalid-bool",
		Op:         "config.set",
		Category:   CategoryFailure,
		Args:       []string{"config", "set", "read_only", "not-bool", "--no-input"},
		ExpectExit: 3,
		ExpectCode: "arg_value_invalid",
	},
	{
		ID:         "config-set-help",
		Op:         "config.set",
		Category:   CategoryHelp,
		Args:       []string{"config", "set", "--help"},
		ExpectExit: 0,
	},
	{
		ID:       "config-edit-no-terminal",
		Op:       "config.edit",
		Category: CategoryFailure,
		// The corpus runs with pipes, which is exactly the state an agent
		// invokes this in — so this is the SUCCESS path for a machine
		// caller, and the one that must never hang. An editor launched
		// here would block forever on input that cannot arrive, and a
		// command that hangs is worse than one that fails.
		//
		// EDITOR is set deliberately: the editor is resolved BEFORE the
		// terminal check, so without it this recipe reaches
		// editor_missing and silently tests the wrong refusal.
		Args:       []string{"config", "edit"},
		Env:        []string{"EDITOR=nano"},
		ExpectExit: 3,
		ExpectCode: "terminal_required",
	},
	{
		ID:       "config-edit-dry-run",
		Op:       "config.edit",
		Category: CategoryDryRun,
		// The form a machine can actually use: it resolves the path and
		// the editor for real, then opens nothing — so it needs no
		// terminal and is safe to run anywhere.
		Args: []string{"config", "edit", "--dry-run"},
		// A configured editor is the normal state, and the preview has to
		// report which one it would open; with none set the command
		// correctly refuses instead, which is a different scenario.
		Env:        []string{"EDITOR=nano"},
		ExpectExit: 0,
	},
	{
		ID:         "config-edit-help",
		Op:         "config.edit",
		Category:   CategoryHelp,
		Args:       []string{"config", "edit", "--help"},
		ExpectExit: 0,
	},
	{
		ID:       "auth-status-success",
		Op:       "auth.status",
		Category: CategorySuccess,
		// Credentials from the environment, not the keyring: the corpus
		// runs the real binary, and a recipe that wrote to a developer's
		// or a CI box's credential store would be reaching outside its
		// own sandbox.
		Args:       []string{"auth", "status"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "myself",
		ExpectExit: 0,
	},
	{
		ID:       "auth-status-unauthenticated",
		Op:       "auth.status",
		Category: CategoryFailure,
		// The state every fresh machine is in, and the one an agent hits
		// first. Exit 1 in EVERY mode — the whole reason this command
		// does not follow the convention of returning success in machine
		// output regardless.
		Args:       []string{"auth", "status"},
		ExpectExit: 1,
		ExpectCode: "auth_missing",
	},
	{
		ID:         "auth-status-help",
		Op:         "auth.status",
		Category:   CategoryHelp,
		Args:       []string{"auth", "status", "--help"},
		ExpectExit: 0,
	},
	{
		ID: "auth-whoami-success", Op: "auth.whoami", Category: CategorySuccess,
		Args: []string{"auth", "whoami"},
		Env:  []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub: "auth-identity", ExpectExit: 0,
	},
	{
		ID: "auth-whoami-unauthenticated", Op: "auth.whoami", Category: CategoryFailure,
		Args: []string{"auth", "whoami"}, ExpectExit: 1, ExpectCode: "auth_missing",
	},
	{
		ID: "auth-whoami-help", Op: "auth.whoami", Category: CategoryHelp,
		Args: []string{"auth", "whoami", "--help"}, ExpectExit: 0,
	},
	{
		ID: "me-success", Op: "me", Category: CategorySuccess,
		Args: []string{"me"},
		Env:  []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub: "auth-identity", ExpectExit: 0,
	},
	{
		ID: "me-unauthenticated", Op: "me", Category: CategoryFailure,
		Args: []string{"me"}, ExpectExit: 1, ExpectCode: "auth_missing",
	},
	{
		ID: "me-help", Op: "me", Category: CategoryHelp,
		Args: []string{"me", "--help"}, ExpectExit: 0,
	},
	{
		ID:       "auth-token-success",
		Op:       "auth.token",
		Category: CategorySuccess,
		// Declared envelope-free: the output IS the secret.
		Args:       []string{"auth", "token"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		ExpectExit: 0,
	},
	{
		ID:         "auth-token-unauthenticated",
		Op:         "auth.token",
		Category:   CategoryFailure,
		Args:       []string{"auth", "token"},
		ExpectExit: 1,
		ExpectCode: "auth_missing",
	},
	{
		ID:         "auth-token-help",
		Op:         "auth.token",
		Category:   CategoryHelp,
		Args:       []string{"auth", "token", "--help"},
		ExpectExit: 0,
	},
	{
		ID:       "auth-login-dry-run",
		Op:       "auth.login",
		Category: CategoryDryRun,
		// A preview verifies the token against the stub and stores
		// nothing, which is the only login the corpus can safely run:
		// the live path writes to the machine's real credential store.
		Args:       []string{"auth", "login", "--with-token", "--dry-run"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubStaleToken},
		Stub:       "myself",
		Stdin:      stubToken + "\n",
		ExpectExit: 0,
	},
	{
		ID:         "auth-login-no-token-flag",
		Op:         "auth.login",
		Category:   CategoryFailure,
		Args:       []string{"auth", "login"},
		ExpectExit: 3,
		ExpectCode: "flag_required",
	},
	{
		ID:         "auth-login-help",
		Op:         "auth.login",
		Category:   CategoryHelp,
		Args:       []string{"auth", "login", "--help"},
		ExpectExit: 0,
	},
	{
		ID:       "auth-logout-dry-run",
		Op:       "auth.logout",
		Category: CategoryDryRun,
		// Same reasoning as login: the live path touches the real store.
		Args:       []string{"auth", "logout", "--dry-run"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		ExpectExit: 0,
	},
	{
		ID:         "auth-logout-no-account",
		Op:         "auth.logout",
		Category:   CategoryFailure,
		Args:       []string{"auth", "logout"},
		ExpectExit: 3,
		ExpectCode: "config_invalid",
	},
	{
		ID:         "auth-logout-help",
		Op:         "auth.logout",
		Category:   CategoryHelp,
		Args:       []string{"auth", "logout", "--help"},
		ExpectExit: 0,
	},
	{
		ID:         "completion-success",
		Op:         "completion",
		Category:   CategorySuccess,
		Args:       []string{"completion", "bash"},
		ExpectExit: 0,
	},
	{
		ID:       "completion-unknown-shell",
		Op:       "completion",
		Category: CategoryFailure,
		// The Args validator rejects the shell before hooks run; the
		// typed frame (arg_value_invalid, the offending name in field)
		// still reaches stdout — NoSuccessEnvelope only exempts SUCCESS
		// from the envelope, never a failure.
		Args:       []string{"completion", "definitely-not-a-shell"},
		ExpectExit: 3,
		ExpectCode: "arg_value_invalid",
	},
	{
		ID:         "completion-help",
		Op:         "completion",
		Category:   CategoryHelp,
		Args:       []string{"completion", "--help"},
		ExpectExit: 0,
	},
	{
		ID:       "issue-view-success",
		Op:       "issue.view",
		Category: CategorySuccess,
		// The one key the "issue" stub answers with a full body — the
		// curated fields plus the custom field M10 proves.
		Args:       []string{"issue", "view", "TST-2"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issue",
		ExpectExit: 0,
	},
	{
		ID:       "issue-view-not-found",
		Op:       "issue.view",
		Category: CategoryFailure,
		// A well-formed key the stub does not carry — the site answers,
		// nothing lives there, exit 2, jira_not_found.
		Args:       []string{"issue", "view", "TST-999"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issue",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-view-help",
		Op:         "issue.view",
		Category:   CategoryHelp,
		Args:       []string{"issue", "view", "--help"},
		ExpectExit: 0,
	},
	{
		ID:       "issue-comment-list-success",
		Op:       "issue.comment.list",
		Category: CategorySuccess,
		// The one key the "comments" stub answers: one comment, whose
		// body carries the M10 unmodeled field, on a page reporting a
		// larger total — the pagination-hint proof.
		Args:       []string{"issue", "comment", "list", "TST-2"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "comments",
		ExpectExit: 0,
	},
	{
		ID:       "issue-comment-list-not-found",
		Op:       "issue.comment.list",
		Category: CategoryFailure,
		// A well-formed key the stub does not carry — the site answers,
		// nothing lives there, exit 2, jira_not_found.
		Args:       []string{"issue", "comment", "list", "TST-999"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "comments",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-comment-list-help",
		Op:         "issue.comment.list",
		Category:   CategoryHelp,
		Args:       []string{"issue", "comment", "list", "--help"},
		ExpectExit: 0,
	},
	{
		ID:       "issue-list-success",
		Op:       "issue.list",
		Category: CategorySuccess,
		// --project TST composes to `project in ("TST")` — the one JQL the
		// "search" stub answers. --limit 2 makes this a genuine two-page
		// walk: the stub's first page carries one issue and is_last=false,
		// so issue.list's own auto-pagination loop (list.go's
		// autoPaginate) fetches a second page before --limit is
		// satisfied, rather than stopping after one request.
		Args:       []string{"issue", "list", "--project", "TST", "--limit", "2"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "search",
		ExpectExit: 0,
	},
	{
		ID:       "issue-list-failure",
		Op:       "issue.list",
		Category: CategoryFailure,
		// An explicit --jql the stub does not recognize — Jira answers,
		// rejects the query, exit 3, jira_bad_request. Composition (not a
		// malformed local value) is what reaches the wire here; the JQL
		// escaping/composition rule itself is pinned locally in
		// internal/cli/issue/jql_test.go, not by this corpus.
		Args:       []string{"issue", "list", "--jql", "this is not valid jql"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "search",
		ExpectExit: 3,
		ExpectCode: "jira_bad_request",
	},
	{
		ID: "issue-mine-success", Op: "issue.mine", Category: CategorySuccess,
		Args: []string{"issue", "mine", "--limit", "2"},
		Env:  []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub: "search", ExpectExit: 0,
	},
	{
		ID: "issue-mine-failure", Op: "issue.mine", Category: CategoryFailure,
		Args: []string{"issue", "mine", "--jql", "this is not valid jql"},
		Env:  []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub: "search", ExpectExit: 3, ExpectCode: "jira_bad_request",
	},
	{
		ID: "issue-mine-help", Op: "issue.mine", Category: CategoryHelp,
		Args: []string{"issue", "mine", "--help"}, ExpectExit: 0,
	},
	{
		ID:       "issue-link-list-success",
		Op:       "issue.link.list",
		Category: CategorySuccess,
		// TWO keys, because this is the first keyed command and one key
		// would prove nothing this suite exists to prove: results[] has
		// to be ordered by argv, counted against the keys given, and
		// filled by a fan-out that ran them concurrently. TST-2 carries
		// links, TST-3 carries none — so the same capture also pins that
		// an empty result is an empty ARRAY rather than a null or a
		// missing key.
		Args:       []string{"issue", "link", "list", "TST-2", "TST-3"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issuelinks",
		ExpectExit: 0,
	},
	{
		ID:       "issue-link-list-not-found",
		Op:       "issue.link.list",
		Category: CategoryFailure,
		// ONE key, and a well-formed one the stub does not carry. That is
		// the total-failure path this command's contract singles out: a
		// failed key beside a successful one is a partial result and the
		// command SUCCEEDS (the success recipe above would still be exit
		// 0 with a failed second key), but a run where every key failed
		// has no partial result to hand back and fails like any
		// single-key read — exit 2, jira_not_found, the original error's
		// own class rather than a rebuild of it.
		Args:       []string{"issue", "link", "list", "TST-999"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issuelinks",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-link-list-help",
		Op:         "issue.link.list",
		Category:   CategoryHelp,
		Args:       []string{"issue", "link", "list", "--help"},
		ExpectExit: 0,
	},
	{
		ID:         "issue-list-help",
		Op:         "issue.list",
		Category:   CategoryHelp,
		Args:       []string{"issue", "list", "--help"},
		ExpectExit: 0,
	},
	{
		ID:       "issue-attachment-list-success",
		Op:       "issue.attachment.list",
		Category: CategorySuccess,
		// TWO keys, matching the link-list recipe and for the same
		// reasons: results[] has to be ordered by argv, counted against
		// the keys given, and filled by a fan-out that ran them
		// concurrently. TST-2 carries three attachments, TST-3 carries
		// none — so the same capture also pins that an empty result is an
		// empty ARRAY rather than a null or a missing key, and that a
		// key with nothing attached still says so out loud.
		Args:       []string{"issue", "attachment", "list", "TST-2", "TST-3"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "attachments",
		ExpectExit: 0,
	},
	{
		ID:       "issue-attachment-list-not-found",
		Op:       "issue.attachment.list",
		Category: CategoryFailure,
		// ONE key, well-formed, that the stub does not carry — the
		// total-failure path. A failed key beside a successful one is a
		// partial result and the command SUCCEEDS; a run where every key
		// failed has no partial result to hand back and fails like any
		// single-key read: exit 2, jira_not_found, the original error's
		// own class rather than a rebuild of it.
		Args:       []string{"issue", "attachment", "list", "TST-999"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "attachments",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-attachment-list-help",
		Op:         "issue.attachment.list",
		Category:   CategoryHelp,
		Args:       []string{"issue", "attachment", "list", "--help"},
		ExpectExit: 0,
	},
	{
		ID:       "issue-watchers-list-success",
		Op:       "issue.watchers.list",
		Category: CategorySuccess,
		// THREE keys, one more than its keyed siblings' two, because this
		// command has a third distinct success shape they do not. TST-2
		// carries three watchers, TST-3 carries none — between them the
		// ordering, the count, and the empty-ARRAY-never-null contract,
		// exactly as the attachment recipe pins them. TST-4 adds the
		// permission-restricted response: watch_count 7 with an empty
		// watchers array, which is a success and must not be rendered or
		// published as an unwatched issue.
		Args:       []string{"issue", "watchers", "list", "TST-2", "TST-3", "TST-4"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "watchers",
		ExpectExit: 0,
	},
	{
		ID:       "issue-watchers-list-not-found",
		Op:       "issue.watchers.list",
		Category: CategoryFailure,
		// ONE key, well-formed, that the stub does not carry — the
		// total-failure path. A failed key beside a successful one is a
		// partial result and the command SUCCEEDS; a run where every key
		// failed has no partial result to hand back and fails like any
		// single-key read: exit 2, jira_not_found, the original error's
		// own class rather than a rebuild of it.
		Args:       []string{"issue", "watchers", "list", "TST-999"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "watchers",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-watchers-list-help",
		Op:         "issue.watchers.list",
		Category:   CategoryHelp,
		Args:       []string{"issue", "watchers", "list", "--help"},
		ExpectExit: 0,
	},

	// issue.comment.add — THE FIRST MUTATION IN THE CORPUS, so it is the
	// first op whose metadata demands four categories rather than three:
	// categoriesFor (applicability.go) adds CategoryDryRun for anything
	// that is not a read. Both the preview AND the live write are
	// captured, because the contract requires dry_run on both paths and
	// only two captures can prove they agree.
	//
	// The body arrives through --json-input, seeded as a Files fixture.
	// That is the route that works in this build (the Markdown routes
	// refuse until the converter lands, see command.encodeMarkdown), and
	// it is also the stronger fixture: an ADF document written by the
	// recipe can be compared byte-for-byte against what the envelope
	// says was sent.
	{
		ID:       "issue-comment-add-success",
		Op:       "issue.comment.add",
		Category: CategorySuccess,
		// TWO keys, because the keyed contract's whole shape — ordered
		// results[], one per key — is invisible with one.
		Args:       []string{"issue", "comment", "add", "TST-2", "TST-3", "--json-input", "body.json"},
		Files:      map[string]string{"body.json": corpusCommentBody},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "comment-add",
		ExpectExit: 0,
	},
	{
		ID:       "issue-comment-add-dry-run",
		Op:       "issue.comment.add",
		Category: CategoryDryRun,
		// The SAME argv as the success recipe plus --dry-run, on purpose:
		// the two captures differ only by the flag, so a diff between
		// them is a diff the flag caused rather than a diff the recipe
		// wrote in.
		//
		// A stub is still named. Not because the preview should reach it
		// — it must not — but because naming one makes that provable: the
		// stub rejects anything but POST and answers only two keys, so a
		// preview that contacted the site would show up in the capture
		// instead of passing silently. The harness's own file fingerprint
		// covers the local half.
		Args:       []string{"issue", "comment", "add", "TST-2", "TST-3", "--json-input", "body.json", "--dry-run"},
		Files:      map[string]string{"body.json": corpusCommentBody},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "comment-add",
		ExpectExit: 0,
	},
	{
		ID:       "issue-comment-add-not-found",
		Op:       "issue.comment.add",
		Category: CategoryFailure,
		// ONE key the stub does not carry — the total-failure path. A
		// failed key beside a successful one is a partial result and the
		// command SUCCEEDS (which is what makes a half-done write safely
		// retryable); a run where every key failed has no partial result
		// to hand back and fails with the per-key error's own class.
		Args:       []string{"issue", "comment", "add", "TST-999", "--json-input", "body.json"},
		Files:      map[string]string{"body.json": corpusCommentBody},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "comment-add",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-comment-add-help",
		Op:         "issue.comment.add",
		Category:   CategoryHelp,
		Args:       []string{"issue", "comment", "add", "--help"},
		ExpectExit: 0,
	},

	// issue.transition — the first op whose REQUEST SHAPE is decided by a
	// response, so the success recipe is doing something no earlier one
	// had to: proving that two keys in the same run took DIFFERENT routes
	// to Jira and that the envelope said which was which.
	//
	// TST-2's Done transition has a screen and TST-3's does not (see
	// corpusTransitionScreens), so one capture covers the atomic path,
	// the separate path, and the fact that the choice is made per key.
	// The stub enforces both halves rather than merely allowing them: it
	// 400s a screenless transition carrying a comment, and 400s a
	// separate comment for a key that could have had an atomic one.
	{
		ID:       "issue-transition-success",
		Op:       "issue.transition",
		Category: CategorySuccess,
		// The comment arrives through --json-input rather than --comment
		// for issue-comment-add-success's reason: an ADF document written
		// by the recipe can be compared byte-for-byte against what the
		// envelope says was sent. The RENAMED pair (--comment,
		// --comment-file) is what design §3.1 makes canonical, and it is
		// exercised by the help capture and by the unit tests; the
		// lossless route is what makes this capture a round-trip proof.
		Args:       []string{"issue", "transition", "TST-2", "TST-3", "--json-input", "body.json"},
		Files:      map[string]string{"body.json": corpusTransitionPayload},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "transitions",
		ExpectExit: 0,
	},
	{
		ID:       "issue-transition-dry-run",
		Op:       "issue.transition",
		Category: CategoryDryRun,
		// The SAME argv as the success recipe plus --dry-run, so a diff
		// between the two captures is a diff the flag caused.
		//
		// Bare --dry-run, NOT --dry-run --validate-remote, and the choice
		// is deliberate. This is the capture that proves the preview is
		// local-only: the stub is named, so a preview that read the
		// workflow would show up here instead of passing silently, and
		// the envelope must therefore report the composed comment while
		// reporting NO route for it — comment_plan absent on every key,
		// because screen state lives on the site and this path never
		// asked.
		//
		// The remote preview (`--dry-run --validate-remote`, which DOES
		// read the workflow and fills comment_plan in) cannot also live
		// here: the corpus allows one recipe per op × category, and a
		// second dry_run entry is refused as a duplicate. It is pinned in
		// the unit tests instead
		// (internal/cli/issue/transition_test.go).
		Args:       []string{"issue", "transition", "TST-2", "TST-3", "--json-input", "body.json", "--dry-run"},
		Files:      map[string]string{"body.json": corpusTransitionPayload},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "transitions",
		ExpectExit: 0,
	},
	{
		ID:       "issue-transition-not-found",
		Op:       "issue.transition",
		Category: CategoryFailure,
		// ONE key the stub does not carry — the total-failure path, and
		// here it fails on the LIST read, before any write. That ordering
		// is the property worth capturing: a key whose workflow cannot be
		// read is a key nothing happened to, so there is no half-applied
		// transition behind this exit code.
		Args:       []string{"issue", "transition", "TST-999", "Done", "--json-input", "body.json"},
		Files:      map[string]string{"body.json": corpusCommentBody},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "transitions",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-transition-help",
		Op:         "issue.transition",
		Category:   CategoryHelp,
		Args:       []string{"issue", "transition", "--help"},
		ExpectExit: 0,
	},

	// issue.link.types — the only op in the issue tree that is not
	// issue-scoped: link types belong to the SITE, so there is no key in
	// the argv and no identity in the envelope.
	//
	// The capture is always a FRESH read. Each capture runs with its own
	// HOME, so the disk cache is cold every time; data.cached is therefore
	// deterministically false and age_seconds absent, which is what a
	// capture-diffing axis needs. The cached branch is pinned by unit test
	// instead (internal/cli/issue/linktypes_test.go), where a cache can be
	// aged without waiting.
	{
		ID:         "issue-link-types-success",
		Op:         "issue.link.types",
		Category:   CategorySuccess,
		Args:       []string{"issue", "link", "types"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "link-types",
		ExpectExit: 0,
	},
	{
		ID:       "issue-link-types-unauthorized",
		Op:       "issue.link.types",
		Category: CategoryFailure,
		// A bad credential rather than a missing resource: this endpoint
		// has no key to get wrong, so 401 is the failure a caller actually
		// meets, and the stub answers it for any other token.
		Args:       []string{"issue", "link", "types"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=wrong-token"},
		Stub:       "link-types",
		ExpectExit: 1,
		ExpectCode: "jira_unauthorized",
	},
	{
		ID:         "issue-link-types-help",
		Op:         "issue.link.types",
		Category:   CategoryHelp,
		Args:       []string{"issue", "link", "types", "--help"},
		ExpectExit: 0,
	},

	// issue.create — THE FIRST OP IN THE CORPUS WITH NO KEY IN ITS ARGV,
	// and the first whose identity slot is filled by the RESPONSE rather
	// than by the caller. That is what its success capture proves:
	// data.issue arrives from the 201 and exists nowhere in the request.
	//
	// The recipe drives all three input routes at once on purpose — a
	// payload file, a Markdown description, and field flags — because the
	// contract that matters here is that they COMPOSE into one request
	// without colliding. A recipe using only flags would leave the
	// merge, which is the interesting half, unexecuted.
	{
		ID:       "issue-create-success",
		Op:       "issue.create",
		Category: CategorySuccess,
		Args: []string{
			"issue", "create", "--json-input", "payload.json",
			"--summary", "Rotate the signing key", "--label", "security", "--label", "ops",
			"--assignee", "@me", "--verify",
		},
		Files:      map[string]string{"payload.json": corpusCreatePayload},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issue-create",
		ExpectExit: 0,
	},
	{
		ID:       "issue-create-dry-run",
		Op:       "issue.create",
		Category: CategoryDryRun,
		// The SAME argv as the success recipe plus --dry-run, on purpose:
		// the two captures differ only by the flag, so a diff between them
		// is a diff the flag caused rather than one the recipe wrote in.
		//
		// A stub is still named, and it is the strict one. Not because the
		// preview should reach it — it must not — but because naming one
		// makes that provable: the stub 401s without credentials and 400s
		// a malformed payload, so a preview that contacted the site would
		// show up in the capture instead of passing silently.
		Args: []string{
			"issue", "create", "--json-input", "payload.json",
			"--summary", "Rotate the signing key", "--label", "security", "--label", "ops",
			"--assignee", "@me", "--verify", "--dry-run",
		},
		Files:      map[string]string{"payload.json": corpusCreatePayload},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issue-create",
		ExpectExit: 0,
	},
	{
		ID:       "issue-create-rejected",
		Op:       "issue.create",
		Category: CategoryFailure,
		// A project the stub does not carry, which is how Jira answers a
		// create naming a project the caller cannot see: 400, not 404. The
		// create endpoint has no key in its path, so there is nothing for
		// it to report as missing — the whole payload is simply rejected.
		Args: []string{
			"issue", "create", "--project", "NOPE", "--type", "Task", "--summary", "Rotate the signing key",
		},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issue-create",
		ExpectExit: 3,
		ExpectCode: "jira_bad_request",
	},
	{
		ID:         "issue-create-help",
		Op:         "issue.create",
		Category:   CategoryHelp,
		Args:       []string{"issue", "create", "--help"},
		ExpectExit: 0,
	},

	// issue.edit — keyed like comment add, and the capture that proves the
	// design's add-rather-than-replace decision reaches the wire: the
	// payload published here carries `update.labels[{add}]`, not
	// `fields.labels`, and the stub refuses a request whose two halves are
	// both empty.
	{
		ID:       "issue-edit-success",
		Op:       "issue.edit",
		Category: CategorySuccess,
		// TWO keys, because the keyed contract's whole shape — ordered
		// results[], one per key — is invisible with one. --verify is on
		// so the per-key readback (the only thing genuinely per key on an
		// edit) is exercised rather than left to a unit test.
		Args: []string{
			"issue", "edit", "TST-2", "TST-3",
			"--json-input", "payload.json",
			"--summary", "Rotate the signing key", "--label", "security", "--remove-label", "stale",
			"--remove-fix-version", "2026.8",
			"--verify",
		},
		Files:      map[string]string{"payload.json": corpusEditPayload},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issue-edit",
		ExpectExit: 0,
	},
	{
		ID:       "issue-edit-dry-run",
		Op:       "issue.edit",
		Category: CategoryDryRun,
		// The SAME argv as the success recipe plus --dry-run, so a diff
		// between the two captures is a diff the flag caused. The strict
		// stub is named for the create preview's reason: a preview that
		// contacted the site would show up here.
		Args: []string{
			"issue", "edit", "TST-2", "TST-3",
			"--json-input", "payload.json",
			"--summary", "Rotate the signing key", "--label", "security", "--remove-label", "stale",
			"--verify", "--dry-run",
		},
		Files:      map[string]string{"payload.json": corpusEditPayload},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issue-edit",
		ExpectExit: 0,
	},
	{
		ID:       "issue-edit-not-found",
		Op:       "issue.edit",
		Category: CategoryFailure,
		// ONE key the stub does not carry — the total-failure path. A
		// failed key beside a successful one is a partial result and the
		// command SUCCEEDS, which is what makes a half-done write safely
		// retryable; a run where every key failed has no partial result to
		// hand back and fails with the per-key error's own class.
		Args:       []string{"issue", "edit", "TST-999", "--summary", "Rotate the signing key"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issue-edit",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:       "issue-edit-help",
		Op:       "issue.edit",
		Category: CategoryHelp,
		Args:     []string{"issue", "edit", "--help"},
	},
	// issue.comment.edit — THE FIRST MUTATION IN THE CORPUS THAT IS NOT
	// KEYED, which is what these four recipes are here to capture. Every
	// mutation before it publishes data.results[]; this one publishes its
	// operation object straight at data, and the M3 axis is correctly
	// absent from its cells because its metadata declares no Keyed.
	//
	// The body arrives through --json-input for issue-comment-add-
	// success's reason: an ADF document written by the recipe can be
	// compared byte-for-byte against what the envelope says was sent.
	{
		ID:       "issue-comment-edit-success",
		Op:       "issue.comment.edit",
		Category: CategorySuccess,
		// ONE key and ONE id, which is the whole arity this command has —
		// there is no two-key variant to capture, and that absence IS the
		// contract.
		Args:       []string{"issue", "comment", "edit", "TST-2", "10042", "--json-input", "body.json"},
		Files:      map[string]string{"body.json": corpusCommentBody},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "comment-edit",
		ExpectExit: 0,
	},
	{
		ID:       "issue-comment-edit-dry-run",
		Op:       "issue.comment.edit",
		Category: CategoryDryRun,
		// The SAME argv as the success recipe plus --dry-run, so a diff
		// between the two captures is a diff the flag caused rather than
		// one the recipe wrote in. A stub is still named, for the reason
		// issue-comment-add-dry-run gives: it rejects every method but PUT,
		// so a preview that contacted the site shows up in the capture
		// instead of passing silently.
		Args:       []string{"issue", "comment", "edit", "TST-2", "10042", "--json-input", "body.json", "--dry-run"},
		Files:      map[string]string{"body.json": corpusCommentBody},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "comment-edit",
		ExpectExit: 0,
	},
	{
		ID:       "issue-comment-edit-not-found",
		Op:       "issue.comment.edit",
		Category: CategoryFailure,
		// A comment id the stub does not carry, on an issue it does. That
		// is the failure worth capturing rather than an unknown KEY: the id
		// is the argument a caller is likeliest to get wrong, having copied
		// it from a list, and it is the one this command added to the
		// surface.
		Args:       []string{"issue", "comment", "edit", "TST-2", "999999", "--json-input", "body.json"},
		Files:      map[string]string{"body.json": corpusCommentBody},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "comment-edit",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-comment-edit-help",
		Op:         "issue.comment.edit",
		Category:   CategoryHelp,
		Args:       []string{"issue", "comment", "edit", "--help"},
		ExpectExit: 0,
	},

	// issue.comment.delete — THE FIRST DESTRUCTIVE OP IN THE CORPUS.
	//
	// Every live recipe passes --force, and that is not a convenience: the
	// harness drives the real binary through pipes, so there is no
	// terminal for a confirmation dialog to draw on and nothing to answer
	// it. That is exactly the non-interactive case the command refuses
	// rather than silently allowing — which the failure recipe below
	// captures directly, making the corpus prove BOTH halves of the
	// confirmation rule rather than just the one that succeeds.
	{
		ID:       "issue-comment-delete-success",
		Op:       "issue.comment.delete",
		Category: CategorySuccess,
		Args:     []string{"issue", "comment", "delete", "TST-2", "10042", "--force"},
		Env:      []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		// The stub rejects every method but DELETE, so a command that
		// reached it any other way fails the capture loudly.
		Stub:       "comment-delete",
		ExpectExit: 0,
	},
	{
		ID:       "issue-comment-delete-dry-run",
		Op:       "issue.comment.delete",
		Category: CategoryDryRun,
		// NO --force, deliberately, where the success recipe has one. A
		// preview destroys nothing, so it must not ask and must not refuse
		// for want of a terminal — and this capture is what proves the
		// confirmation gate sits on the LIVE path only. It would exit 3
		// (flag_required) if the gate ran here, so the expected exit of 0
		// is a real assertion rather than a formality.
		Args:       []string{"issue", "comment", "delete", "TST-2", "10042", "--dry-run"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "comment-delete",
		ExpectExit: 0,
	},
	{
		ID:       "issue-comment-delete-not-found",
		Op:       "issue.comment.delete",
		Category: CategoryFailure,
		// A comment id the stub does not carry, with --force so the request
		// actually goes out — without it this would fail at the
		// confirmation gate and capture nothing about the delete itself.
		Args:       []string{"issue", "comment", "delete", "TST-2", "999999", "--force"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "comment-delete",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-comment-delete-help",
		Op:         "issue.comment.delete",
		Category:   CategoryHelp,
		Args:       []string{"issue", "comment", "delete", "--help"},
		ExpectExit: 0,
	},

	// issue.watchers.add / issue.watchers.remove — the pair that share an
	// executor in the CLI (internal/cli/issue/watchers_write.go) and do
	// NOT share anything here. Two ops, two full sets of recipes, because
	// the corpus checks the published contract rather than the
	// implementation behind it: if the shared executor ever grew a branch
	// that made one verb behave differently, a corpus that captured only
	// one of them would not see it.
	{
		ID:       "issue-watchers-add-success",
		Op:       "issue.watchers.add",
		Category: CategorySuccess,
		// TWO keys, because the keyed contract's whole shape — ordered
		// results[], one per key — is invisible with one. The readback is
		// left ON (no --no-readback), so the capture also covers the
		// second request each key makes and the readback object it fills.
		Args:       []string{"issue", "watchers", "add", "TST-2", "TST-3", "--user", "5b10a2844c20165700ede21g"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "watchers-write",
		ExpectExit: 0,
	},
	{
		ID:       "issue-watchers-add-dry-run",
		Op:       "issue.watchers.add",
		Category: CategoryDryRun,
		// The SAME argv as the success recipe plus --dry-run. The stub
		// serves GET as well as POST, which makes this capture stronger
		// than its comment-add counterpart: a preview that leaked into
		// EITHER the write or the readback would be served and show up,
		// rather than being caught only by a method rejection.
		Args:       []string{"issue", "watchers", "add", "TST-2", "TST-3", "--user", "5b10a2844c20165700ede21g", "--dry-run"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "watchers-write",
		ExpectExit: 0,
	},
	{
		ID:       "issue-watchers-add-not-found",
		Op:       "issue.watchers.add",
		Category: CategoryFailure,
		// ONE key the stub does not carry — the total-failure path. A
		// failed key beside a successful one is a partial result and the
		// command SUCCEEDS; a run where every key failed has no partial
		// result to hand back and fails with the per-key error's own class.
		Args:       []string{"issue", "watchers", "add", "TST-999", "--user", "5b10a2844c20165700ede21g"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "watchers-write",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-watchers-add-help",
		Op:         "issue.watchers.add",
		Category:   CategoryHelp,
		Args:       []string{"issue", "watchers", "add", "--help"},
		ExpectExit: 0,
	},
	{
		ID:       "issue-watchers-remove-success",
		Op:       "issue.watchers.remove",
		Category: CategorySuccess,
		// --no-readback here where the add recipe leaves it off, so the
		// corpus covers BOTH shapes of the per-key object across the pair:
		// add's entries carry a readback, remove's carry none. Capturing
		// the same choice twice would have left the absent-readback shape
		// unexecuted.
		Args:       []string{"issue", "watchers", "remove", "TST-2", "TST-3", "--user", "5b10a2844c20165700ede21g", "--no-readback"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "watchers-write",
		ExpectExit: 0,
	},
	{
		ID:         "issue-watchers-remove-dry-run",
		Op:         "issue.watchers.remove",
		Category:   CategoryDryRun,
		Args:       []string{"issue", "watchers", "remove", "TST-2", "TST-3", "--user", "5b10a2844c20165700ede21g", "--no-readback", "--dry-run"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "watchers-write",
		ExpectExit: 0,
	},
	{
		ID:         "issue-watchers-remove-not-found",
		Op:         "issue.watchers.remove",
		Category:   CategoryFailure,
		Args:       []string{"issue", "watchers", "remove", "TST-999", "--user", "5b10a2844c20165700ede21g"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "watchers-write",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-watchers-remove-help",
		Op:         "issue.watchers.remove",
		Category:   CategoryHelp,
		Args:       []string{"issue", "watchers", "remove", "--help"},
		ExpectExit: 0,
	},

	// issue.link.create — the first write whose request shape depends on
	// SITE CONFIGURATION rather than on the issues involved, and the
	// recipes are built around that one fact.
	//
	// Every one passes `--type blocks`, the OUTWARD phrase of an
	// asymmetric type, because the stub refuses a request whose ends do
	// not match that direction (serveLinkIssues). So the success capture
	// is a direction proof, not merely a "something was sent" proof.
	{
		ID:       "issue-link-create-success",
		Op:       "issue.link.create",
		Category: CategorySuccess,
		// ONE source key, where most keyed recipes pass two, and the
		// stub is why: it accepts exactly one source (TST-2) so that a
		// reversed request lands on a 400 rather than being mistaken for
		// the other key's. The ordered-results contract is pinned by the
		// keyed reads and by comment add; what this recipe exists to
		// prove is the direction.
		Args:       []string{"issue", "link", "create", "TST-2", "--to", "TST-3", "--type", corpusLinkPhrase},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issue-links",
		ExpectExit: 0,
	},
	{
		ID:       "issue-link-create-dry-run",
		Op:       "issue.link.create",
		Category: CategoryDryRun,
		// The SAME argv as the success recipe plus --dry-run, and
		// deliberately NOT --validate-remote: this is the capture that
		// proves a bare preview is local-only. A stub is named and
		// listening, so a preview that read the type list would show up
		// here — and the envelope must report type_resolved false with no
		// type and no relation, because it never asked.
		Args:       []string{"issue", "link", "create", "TST-2", "--to", "TST-3", "--type", corpusLinkPhrase, "--dry-run"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issue-links",
		ExpectExit: 0,
	},
	{
		ID:       "issue-link-create-not-found",
		Op:       "issue.link.create",
		Category: CategoryFailure,
		// ONE key the stub does not carry — the total-failure path. The
		// type resolves first and the WRITE is what fails, which is the
		// ordering worth capturing: a run that failed at the type list
		// would carry a different code entirely.
		Args:       []string{"issue", "link", "create", "TST-999", "--to", "TST-3", "--type", corpusLinkPhrase},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issue-links",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-link-create-help",
		Op:         "issue.link.create",
		Category:   CategoryHelp,
		Args:       []string{"issue", "link", "create", "--help"},
		ExpectExit: 0,
	},

	// issue.link.delete — the only op in the corpus whose envelope names
	// no issue at all (a link id is site-wide), so its success capture is
	// what proves the M1 identity axis is correctly NOT applied to it.
	{
		ID:         "issue-link-delete-success",
		Op:         "issue.link.delete",
		Category:   CategorySuccess,
		Args:       []string{"issue", "link", "delete", corpusLinkID},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issue-links",
		ExpectExit: 0,
	},
	{
		ID:         "issue-link-delete-dry-run",
		Op:         "issue.link.delete",
		Category:   CategoryDryRun,
		Args:       []string{"issue", "link", "delete", corpusLinkID, "--dry-run"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issue-links",
		ExpectExit: 0,
	},
	{
		ID:         "issue-link-delete-not-found",
		Op:         "issue.link.delete",
		Category:   CategoryFailure,
		Args:       []string{"issue", "link", "delete", "999999"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "issue-links",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-link-delete-help",
		Op:         "issue.link.delete",
		Category:   CategoryHelp,
		Args:       []string{"issue", "link", "delete", "--help"},
		ExpectExit: 0,
	},

	// issue.weblink.list — a keyed READ, so three categories rather than
	// four. TST-2 carries one link and TST-3 none, so one capture covers
	// both the populated table and the empty-array contract.
	{
		ID:         "issue-weblink-list-success",
		Op:         "issue.weblink.list",
		Category:   CategorySuccess,
		Args:       []string{"issue", "weblink", "list", "TST-2", "TST-3"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "weblinks",
		ExpectExit: 0,
	},
	{
		ID:         "issue-weblink-list-not-found",
		Op:         "issue.weblink.list",
		Category:   CategoryFailure,
		Args:       []string{"issue", "weblink", "list", "TST-999"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "weblinks",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-weblink-list-help",
		Op:         "issue.weblink.list",
		Category:   CategoryHelp,
		Args:       []string{"issue", "weblink", "list", "--help"},
		ExpectExit: 0,
	},

	// issue.weblink.add — the only op in this CLI whose outcome is read
	// off a STATUS CODE, and the success recipe is built to capture both
	// halves of it in one run.
	{
		ID:       "issue-weblink-add-success",
		Op:       "issue.weblink.add",
		Category: CategorySuccess,
		// TWO keys AND a --global-id, so the stub answers 200 for both
		// (an existing link replaced). The created=false shape is the one
		// worth capturing here: created=true is what an add without a
		// global id produces, and it is pinned in the unit tests, while
		// the REPLACED path is the one a reader would not predict from
		// the verb's name.
		Args: []string{
			"issue", "weblink", "add", "TST-2", "TST-3",
			"--url", "https://example.com/runbook", "--title", "Runbook",
			"--global-id", corpusWeblinkGlobalID,
		},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "weblinks",
		ExpectExit: 0,
	},
	{
		ID:       "issue-weblink-add-dry-run",
		Op:       "issue.weblink.add",
		Category: CategoryDryRun,
		// The SAME argv plus --dry-run, so a diff between the two
		// captures is a diff the flag caused. created must be ABSENT
		// here, not false: false would claim a link was replaced.
		Args: []string{
			"issue", "weblink", "add", "TST-2", "TST-3",
			"--url", "https://example.com/runbook", "--title", "Runbook",
			"--global-id", corpusWeblinkGlobalID, "--dry-run",
		},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "weblinks",
		ExpectExit: 0,
	},
	{
		ID:       "issue-weblink-add-not-found",
		Op:       "issue.weblink.add",
		Category: CategoryFailure,
		Args: []string{
			"issue", "weblink", "add", "TST-999",
			"--url", "https://example.com/runbook", "--title", "Runbook",
		},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "weblinks",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-weblink-add-help",
		Op:         "issue.weblink.add",
		Category:   CategoryHelp,
		Args:       []string{"issue", "weblink", "add", "--help"},
		ExpectExit: 0,
	},

	// issue.weblink.delete — the counterpart to issue.link.delete, and
	// the pair is why both are in the corpus: this one names an issue
	// because a remote link's id is scoped to one, and that one does not
	// because a link id is site-wide. The two success captures side by
	// side are the evidence for a difference a reader would otherwise
	// take on trust.
	{
		ID:         "issue-weblink-delete-success",
		Op:         "issue.weblink.delete",
		Category:   CategorySuccess,
		Args:       []string{"issue", "weblink", "delete", "TST-2", corpusWeblinkID},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "weblinks",
		ExpectExit: 0,
	},
	{
		ID:         "issue-weblink-delete-dry-run",
		Op:         "issue.weblink.delete",
		Category:   CategoryDryRun,
		Args:       []string{"issue", "weblink", "delete", "TST-2", corpusWeblinkID, "--dry-run"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "weblinks",
		ExpectExit: 0,
	},
	{
		ID:         "issue-weblink-delete-not-found",
		Op:         "issue.weblink.delete",
		Category:   CategoryFailure,
		Args:       []string{"issue", "weblink", "delete", "TST-2", "999999"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "weblinks",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-weblink-delete-help",
		Op:         "issue.weblink.delete",
		Category:   CategoryHelp,
		Args:       []string{"issue", "weblink", "delete", "--help"},
		ExpectExit: 0,
	},

	// issue.attachment.add — the first op in the corpus whose request is
	// not JSON, and the first that reads the caller's filesystem.
	//
	// The file comes from a Files fixture, so the recipe controls its
	// content and the stub can require a real multipart part carrying it.
	{
		ID:       "issue-attachment-add-success",
		Op:       "issue.attachment.add",
		Category: CategorySuccess,
		// --file, which makes BOTH positionals keys: the two-key form is
		// what shows the ordered-results contract, and it is the form
		// that needs the flag to be unambiguous.
		Args:       []string{"issue", "attachment", "add", "TST-2", "TST-3", "--file", "notes.txt"},
		Files:      map[string]string{"notes.txt": corpusAttachmentBody},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "attachments-write",
		ExpectExit: 0,
	},
	{
		ID:       "issue-attachment-add-dry-run",
		Op:       "issue.attachment.add",
		Category: CategoryDryRun,
		// The SAME argv plus --dry-run. A stub is named, so a preview that
		// uploaded would be served and show up in the capture — and the
		// harness's file fingerprint verifies no local writes. Metadata
		// inspection is permitted; content-free inspection has a separate
		// unreadable-input binary guard.
		Args:       []string{"issue", "attachment", "add", "TST-2", "TST-3", "--file", "notes.txt", "--dry-run"},
		Files:      map[string]string{"notes.txt": corpusAttachmentBody},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "attachments-write",
		ExpectExit: 0,
	},
	{
		ID:       "issue-attachment-add-not-found",
		Op:       "issue.attachment.add",
		Category: CategoryFailure,
		// ONE key the stub does not carry — the total-failure path.
		Args:       []string{"issue", "attachment", "add", "TST-999", "--file", "notes.txt"},
		Files:      map[string]string{"notes.txt": corpusAttachmentBody},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "attachments-write",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-attachment-add-help",
		Op:         "issue.attachment.add",
		Category:   CategoryHelp,
		Args:       []string{"issue", "attachment", "add", "--help"},
		ExpectExit: 0,
	},

	// issue.attachment.download — THE ONLY OP IN THE CORPUS THAT WRITES A
	// FILE, and the only one classified as a local-state mutation while
	// reading from Jira.
	//
	// The stub's metadata answers with a TRAVERSAL filename
	// (corpusAttachmentFilename), so this capture is the end-to-end proof
	// of the path sandbox: the envelope must report a reduced name and a
	// path inside the destination, and the harness's own file fingerprint
	// would see anything written where it should not be.
	{
		ID:       "issue-attachment-download-success",
		Op:       "issue.attachment.download",
		Category: CategorySuccess,
		// --to . writes into the harness's own sandboxed working
		// directory, which is what makes a capture that writes a file
		// safe to run in the first place.
		Args:       []string{"issue", "attachment", "download", corpusAttachmentID, "--to", "."},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "attachments-write",
		ExpectExit: 0,
	},
	{
		ID:       "issue-attachment-download-dry-run",
		Op:       "issue.attachment.download",
		Category: CategoryDryRun,
		// BARE --dry-run, deliberately: this is the capture that proves a
		// local preview neither reads the metadata nor writes the file, so
		// the envelope must carry filename_resolved false with no filename
		// and no path. The remote preview that DOES resolve them is pinned
		// in the unit tests — the corpus allows one recipe per op ×
		// category.
		Args:       []string{"issue", "attachment", "download", corpusAttachmentID, "--to", ".", "--dry-run"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "attachments-write",
		ExpectExit: 0,
	},
	{
		ID:         "issue-attachment-download-not-found",
		Op:         "issue.attachment.download",
		Category:   CategoryFailure,
		Args:       []string{"issue", "attachment", "download", "999999", "--to", "."},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "attachments-write",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-attachment-download-help",
		Op:         "issue.attachment.download",
		Category:   CategoryHelp,
		Args:       []string{"issue", "attachment", "download", "--help"},
		ExpectExit: 0,
	},

	// issue.attachment.delete — the second destructive op in the corpus,
	// and every live recipe passes --force for issue-comment-delete's
	// reason: the harness drives the binary through pipes, so there is no
	// terminal to answer a dialog. The dry-run recipe deliberately does
	// NOT, which is what proves the gate sits on the live path only.
	{
		ID:         "issue-attachment-delete-success",
		Op:         "issue.attachment.delete",
		Category:   CategorySuccess,
		Args:       []string{"issue", "attachment", "delete", corpusAttachmentID, "--force"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "attachments-write",
		ExpectExit: 0,
	},
	{
		ID:       "issue-attachment-delete-dry-run",
		Op:       "issue.attachment.delete",
		Category: CategoryDryRun,
		// NO --force. It would exit 3 (flag_required) if the confirmation
		// gate ran on the preview path, so the expected 0 is a real
		// assertion rather than a formality.
		Args:       []string{"issue", "attachment", "delete", corpusAttachmentID, "--dry-run"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "attachments-write",
		ExpectExit: 0,
	},
	{
		ID:         "issue-attachment-delete-not-found",
		Op:         "issue.attachment.delete",
		Category:   CategoryFailure,
		Args:       []string{"issue", "attachment", "delete", "999999", "--force"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "attachments-write",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-attachment-delete-help",
		Op:         "issue.attachment.delete",
		Category:   CategoryHelp,
		Args:       []string{"issue", "attachment", "delete", "--help"},
		ExpectExit: 0,
	},

	// issue.delete — the third destructive op in the corpus, and every
	// live recipe passes --force for issue-comment-delete's reason.
	{
		ID:         "issue-delete-success",
		Op:         "issue.delete",
		Category:   CategorySuccess,
		Args:       []string{"issue", "delete", "TST-2", "TST-3", "--force"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "lifecycle",
		ExpectExit: 0,
	},
	{
		ID:       "issue-delete-dry-run",
		Op:       "issue.delete",
		Category: CategoryDryRun,
		// NO --force, deliberately: it would exit 3 if the confirmation
		// gate ran on the preview path, so the expected 0 is a real
		// assertion. --delete-subtasks is set so the capture also pins
		// that the preview publishes it.
		Args:       []string{"issue", "delete", "TST-2", "TST-3", "--delete-subtasks", "--dry-run"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "lifecycle",
		ExpectExit: 0,
	},
	{
		ID:         "issue-delete-not-found",
		Op:         "issue.delete",
		Category:   CategoryFailure,
		Args:       []string{"issue", "delete", "TST-999", "--force"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "lifecycle",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-delete-help",
		Op:         "issue.delete",
		Category:   CategoryHelp,
		Args:       []string{"issue", "delete", "--help"},
		ExpectExit: 0,
	},

	// issue.clone — the only COMPOSITE op in the corpus: a read of the
	// source followed by a create. The stub refuses a create payload
	// carrying `status` or `assignee`, so this capture is what turns the
	// copy policy into something that can fail.
	{
		ID:         "issue-clone-success",
		Op:         "issue.clone",
		Category:   CategorySuccess,
		Args:       []string{"issue", "clone", "TST-2", "TST-3"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "lifecycle",
		ExpectExit: 0,
	},
	{
		ID:       "issue-clone-dry-run",
		Op:       "issue.clone",
		Category: CategoryDryRun,
		// A stub is named, so a preview that read a source issue would be
		// served and show up here — which is the claim this capture makes:
		// the fields a clone carries live on the source, and the preview
		// deliberately does not go and get them.
		Args:       []string{"issue", "clone", "TST-2", "TST-3", "--dry-run"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "lifecycle",
		ExpectExit: 0,
	},
	{
		ID:       "issue-clone-not-found",
		Op:       "issue.clone",
		Category: CategoryFailure,
		// The SOURCE read is what fails here, before any create — which is
		// the ordering worth capturing: a failed clone leaves nothing
		// behind, because nothing was made.
		Args:       []string{"issue", "clone", "TST-999"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "lifecycle",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-clone-help",
		Op:         "issue.clone",
		Category:   CategoryHelp,
		Args:       []string{"issue", "clone", "--help"},
		ExpectExit: 0,
	},

	// issue.rank — THE ONLY OP IN THE CORPUS THAT LEAVES /rest/api/3.
	//
	// The success capture proves the transport reaches the Agile API at
	// all: the client's base URL is the site root, so nothing about that
	// prefix was guaranteed until something ran against it. The stub also
	// requires all the issues in ONE array, so a CLI that fanned out
	// would fail here rather than silently producing an order nobody
	// asked for.
	{
		ID:         "issue-rank-success",
		Op:         "issue.rank",
		Category:   CategorySuccess,
		Args:       []string{"issue", "rank", "TST-2", "TST-3", "--before", corpusRankAnchor},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "lifecycle",
		ExpectExit: 0,
	},
	{
		ID:         "issue-rank-dry-run",
		Op:         "issue.rank",
		Category:   CategoryDryRun,
		Args:       []string{"issue", "rank", "TST-2", "TST-3", "--before", corpusRankAnchor, "--dry-run"},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "lifecycle",
		ExpectExit: 0,
	},
	{
		ID:         "issue-rank-not-found",
		Op:         "issue.rank",
		Category:   CategoryFailure,
		Args:       []string{"issue", "rank", "TST-999", "--after", corpusRankAnchor},
		Env:        []string{"JIRA_EMAIL=" + stubEmail, "JIRA_API_TOKEN=" + stubToken},
		Stub:       "lifecycle",
		ExpectExit: 2,
		ExpectCode: "jira_not_found",
	},
	{
		ID:         "issue-rank-help",
		Op:         "issue.rank",
		Category:   CategoryHelp,
		Args:       []string{"issue", "rank", "--help"},
		ExpectExit: 0,
	},
}

// corpusCreatePayload is the native payload the create recipes send
// through --json-input.
//
// It carries the project and the type but NOT the summary, which the
// recipe supplies with a flag. That split is the point: the capture then
// proves the two routes merged into one request rather than one winning,
// and it does so without tripping the collision refusal — which is
// exercised by the unit tests, where a refusal is the expected result.
const corpusCreatePayload = `{"fields":{"project":{"key":"` + corpusCreateProject + `"},"issuetype":{"name":"Task"},"priority":{"name":"High"},"customfield_10011":"create custom value"}}`

// corpusEditPayload is the native payload the edit recipes send.
//
// It carries ONLY a custom field, which is the point: it collides with no
// field flag, so the recipe exercises the merge and the M10 passthrough
// proof at once — a tenant field this CLI has no flag for reaching the
// composed request untouched.
const corpusEditPayload = `{"fields":{"customfield_10011":"edit custom value"}}`

// corpusCommentBody is the ADF document the comment-add recipes post.
//
// Deliberately plain, and NOT built with hostile(): the body is echoed
// back by the stub and rendered, so attacking it here would attack the
// same field twice (the created comment's author already carries the
// payload for H4) while making the byte-for-byte round-trip comparison
// unreadable. The `&` is the one adversarial byte kept, because it is
// the character HTML escaping would rewrite — if any layer marshals this
// with escaping on, the capture shows & and the round-trip proof
// fails exactly where it should.
const corpusCommentBody = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Shipped & verified."}]}]}`

// corpusTransitionScreens is the workflow shape each transition recipe's
// keys are on: true where the "Done" transition has a screen, false
// where it does not.
//
// Two keys with DIFFERENT answers, on purpose. One capture then
// exercises both routes at once, and — more importantly — proves the CLI
// decides per key rather than once per run: a single-key corpus could
// not tell a correct implementation from one that read the first key's
// workflow and applied that verdict to every other.
var corpusTransitionScreens = map[string]bool{
	"TST-2": true,
	"TST-3": false,
}

// serveTransitionPost answers the move itself, and refuses the payload
// the CLI must never send.
//
// The 400 on a screenless transition carrying `update` is the corpus's
// single most load-bearing assertion. Real Jira answers 204 there and
// silently drops the comment, so a faithful stub would let the bug
// through — this one is deliberately STRICTER than the site in exactly
// the place the site is silent, which is the only way a capture can see
// the difference.
func serveTransitionPost(w http.ResponseWriter, r *http.Request, screened bool) {
	sent, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var body struct {
		Transition struct {
			ID string `json:"id"`
		} `json:"transition"`
		Fields json.RawMessage `json:"fields"`
		Update json.RawMessage `json:"update"`
	}
	if err := json.Unmarshal(sent, &body); err != nil || body.Transition.ID == "" {
		// A transition with no id is not a request Jira can act on, and
		// the endpoint takes nothing else to identify one — so an empty
		// id means the CLI failed to resolve the caller's status name
		// and sent the request anyway.
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var fields, operations map[string]json.RawMessage
	if (len(body.Fields) > 0 && json.Unmarshal(body.Fields, &fields) != nil) || (len(body.Update) > 0 && json.Unmarshal(body.Update, &operations) != nil) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !screened && (len(operations) > 0 || len(fields) > 0) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// serveSeparateComment answers the fallback comment post, and refuses it
// on a key that never needed one.
//
// The mirror of the assertion above: a transition WITH a screen must
// carry its comment atomically, so a second request arriving for TST-2
// means the CLI split a request it did not have to and gave up the
// atomicity it could have had. Serving it would make the two paths
// indistinguishable in the capture.
func serveSeparateComment(w http.ResponseWriter, r *http.Request, screened bool) {
	if screened {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	posted, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if _, err := w.Write(createdCommentJSON("TST-3", "10TST-3", posted)); err != nil {
		return
	}
}

// The one issue and comment the single-target comment stubs answer for.
// Named constants rather than repeated literals, so the stub, the edit
// recipes and the delete recipes cannot drift into disagreeing about
// which id exists — a drift that would show up as a 404 in a capture
// meant to prove a success.
const (
	corpusCommentKey = "TST-2"
	corpusCommentID  = "10042"
)

// cutCommentPath splits /rest/api/3/issue/{key}/comment/{id} into its two
// identifiers.
//
// Shared by the edit and delete stubs, which serve the same path under
// different methods. It matches the WHOLE shape rather than trimming a
// prefix and hoping: an id containing a slash, or a request to
// .../comment with no id at all, must not resolve to something these
// stubs then serve.
func cutCommentPath(path string) (key, commentID string, ok bool) {
	rest, ok := strings.CutPrefix(path, "/rest/api/3/issue/")
	if !ok {
		return "", "", false
	}
	key, rest, ok = strings.Cut(rest, "/comment/")
	if !ok || key == "" || rest == "" || strings.Contains(rest, "/") {
		return "", "", false
	}
	return key, rest, true
}

// stubAuthorized enforces the Basic-auth pair every stub requires,
// answering 401 itself and reporting whether the caller may continue.
//
// Extracted while adding the three stubs below it, because the check was
// already copied verbatim into six handlers and the seventh would have
// been the point at which one of them quietly diverged.
func stubAuthorized(w http.ResponseWriter, r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok || user != stubEmail || pass != stubToken {
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	return true
}

// serveAddWatcher answers the watcher POST, and refuses the payload the
// CLI must never send.
//
// The 400 on a non-string body is this stub's load-bearing assertion.
// POST .../watchers declares its requestBody as `{"type": "string"}` —
// the account id IS the document — which is unlike every neighboring
// endpoint, so `{"accountId": "..."}` is the obvious wrong guess and the
// one a reviewer would not catch by reading. Real Jira rejects it; this
// stub rejects it in the same place, so a capture can tell the two
// implementations apart.
func serveAddWatcher(w http.ResponseWriter, r *http.Request) {
	sent, err := io.ReadAll(io.LimitReader(r.Body, 1<<10))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var accountID string
	if err := json.Unmarshal(sent, &accountID); err != nil || accountID == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// serveRemoveWatcher answers the watcher DELETE, and refuses one that
// names no account.
//
// The mirror of the assertion above, and it guards the OTHER half of
// Jira's own asymmetry: remove takes its account id as a query parameter
// where add takes it as the body. A DELETE arriving with no accountId
// would, at the real site, be a request to remove nothing in particular;
// refusing it here means a CLI that put the id in the wrong place cannot
// pass this capture by accident.
func serveRemoveWatcher(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("accountId") == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// The fixtures and handlers the issue-link and remote-link stubs answer
// with.

const (
	// corpusLinkPhrase is the phrasing every link recipe passes to
	// --type. It is the OUTWARD description of an asymmetric type, so
	// resolving it correctly means the CLI put the named issue on the
	// inward end — which serveLinkIssues below enforces.
	corpusLinkPhrase = "blocks"
	// corpusLinkID is the only link id the delete stub knows.
	corpusLinkID = "10100"
	// corpusWeblinkID is the only remote-link id the delete stub knows.
	corpusWeblinkID = "10200"
	// corpusWeblinkGlobalID is the de-duplication key that makes the add
	// stub answer 200 (replaced) rather than 201 (created).
	corpusWeblinkGlobalID = "corpus-global-id"
)

// corpusLinkTypesJSON is the site's link-type list.
//
// Two types, and the pair is deliberate: Blocks phrases its two
// directions differently, which is what lets a capture prove the CLI
// resolved a phrase into a direction; Relates phrases both the same,
// which is the case where the resolution has to stay stable anyway.
//
// The names go through hostile() for the reason every other Jira-authored
// string in this corpus does — an admin names link types, so they are
// tenant text and H4 has to see them sanitized on the human surface.
func corpusLinkTypesJSON() []byte {
	body := map[string]any{
		"issueLinkTypes": []any{
			map[string]any{
				"id":      "10000",
				"name":    hostile("Blocks"),
				"inward":  "is blocked by",
				"outward": corpusLinkPhrase,
				"self":    "https://example.atlassian.net/rest/api/3/issueLinkType/10000",
			},
			map[string]any{
				"id":      "10001",
				"name":    "Relates",
				"inward":  "relates to",
				"outward": "relates to",
				"self":    "https://example.atlassian.net/rest/api/3/issueLinkType/10001",
			},
		},
	}
	return mustMarshal(body)
}

// serveLinkIssues answers the link POST, and refuses a request whose
// ends are the wrong way round.
//
// This is the corpus's assertion about DIRECTION, and it is the reason
// this stub is stricter than the site. Real Jira accepts either
// arrangement and silently creates a link pointing whichever way it was
// told, so a faithful stub could not tell a correct implementation from
// one that reversed the sentence — the caller would find out by reading
// the issue in the web UI days later. The recipes ask for "TST-2 blocks
// TST-3" with the type's OUTWARD phrase, so the named issue must arrive
// on the inward end; anything else is a 400.
func serveLinkIssues(w http.ResponseWriter, r *http.Request) {
	sent, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var body struct {
		Type struct {
			Name string `json:"name"`
		} `json:"type"`
		InwardIssue struct {
			Key string `json:"key"`
		} `json:"inwardIssue"`
		OutwardIssue struct {
			Key string `json:"key"`
		} `json:"outwardIssue"`
	}
	if err := json.Unmarshal(sent, &body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	switch {
	case body.Type.Name != hostile("Blocks"):
		// The canonical NAME, not the phrase the caller typed: resolving
		// the phrase to its type is half the job, and sending the phrase
		// through would be the other half left undone.
		w.WriteHeader(http.StatusBadRequest)
	case body.OutwardIssue.Key != "TST-3":
		// The --to target belongs on the outward end for a forward
		// phrasing. Reversed ends land here.
		w.WriteHeader(http.StatusBadRequest)
	case body.InwardIssue.Key != "TST-2":
		// The only source key the success recipe names; anything else is
		// the failure recipe's own 404 path, answered as a 404 rather
		// than a 400 so the taxonomy maps it to jira_not_found.
		w.WriteHeader(http.StatusNotFound)
	default:
		// 201 with NO body, which is this endpoint's real answer and the
		// reason `link create` publishes an empty object per key.
		w.WriteHeader(http.StatusCreated)
	}
}

// serveDeleteIssueLink answers the link DELETE. It 204s the one id the
// recipes name and 404s everything else, which is the failure recipe's
// own path.
func serveDeleteIssueLink(w http.ResponseWriter, r *http.Request) {
	if strings.TrimPrefix(r.URL.Path, "/rest/api/3/issueLink/") != corpusLinkID {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// corpusWeblinksJSON is one issue's remote links.
//
// TST-2 carries one link and TST-3 carries none, so a single capture
// covers both the populated table and the empty-array contract a caller
// iterating data.results[].data.weblinks depends on — the same split the
// watcher stub makes for the same reason.
//
// The response is a BARE ARRAY, which is this endpoint's real shape and
// the only read in this corpus that answers that way.
func corpusWeblinksJSON(key string) []byte {
	if key != "TST-2" {
		return []byte("[]")
	}
	return mustMarshal([]any{
		map[string]any{
			"id":       10200,
			"self":     "https://example.atlassian.net/rest/api/3/issue/10500/remotelink/10200",
			"globalId": corpusWeblinkGlobalID,
			// Author-supplied text on a resource anyone can write to, so
			// it carries the hostile payload H4 has to see sanitized.
			"relationship": hostile("is implemented by"),
			"application":  map[string]any{"name": "GitHub", "type": "com.github"},
			"object": map[string]any{
				"url":     "https://example.com/pr/9",
				"title":   hostile("PR 9"),
				"summary": hostile("Adds the retry loop."),
			},
		},
	})
}

// serveAddRemoteLink answers the remote-link POST, choosing its STATUS
// from the request rather than answering one status for everything.
//
// That choice is the assertion. The endpoint is create-or-update keyed
// on globalId and reports which it did through the status code alone —
// 201 created, 200 replaced — so a stub that always answered 201 could
// not tell a CLI that reads the distinction from one that ignores it.
// A request carrying corpusWeblinkGlobalID is treated as matching an
// existing link and answered 200; anything else is a new link, 201.
func serveAddRemoteLink(w http.ResponseWriter, r *http.Request) {
	sent, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var body struct {
		GlobalID string `json:"globalId"`
		Object   struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"object"`
	}
	if err := json.Unmarshal(sent, &body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if body.Object.URL == "" || body.Object.Title == "" {
		// Both are required by the endpoint, and the CLI refuses a
		// missing one before sending — so arriving here without them
		// means that refusal stopped working.
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	status := http.StatusCreated
	if body.GlobalID == corpusWeblinkGlobalID {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(mustMarshal(map[string]any{
		"id":   10200,
		"self": "https://example.atlassian.net/rest/api/3/issue/10500/remotelink/10200",
	})); err != nil {
		return
	}
}

// The attachment fixtures and handlers.

const (
	// corpusAttachmentID is the only attachment id the stub knows.
	corpusAttachmentID = "10300"
	// corpusAttachmentFilename is what the metadata endpoint reports, and
	// it is HOSTILE on purpose: a traversal that would land in the parent
	// of wherever the download was pointed. The capture proves the CLI
	// reduced it to `escaped.txt` and wrote inside the destination, which
	// is the one thing in this corpus that could otherwise go wrong on
	// the machine running the tests rather than in an envelope.
	corpusAttachmentFilename = "../../escaped.txt"
	// corpusAttachmentBody is what the content endpoint streams. Short,
	// because this capture is about WHERE the bytes go rather than how
	// many.
	corpusAttachmentBody = "corpus attachment body\n"
)

// serveAddAttachment answers the upload, and refuses a request that is
// not the multipart document Jira requires.
//
// Both checks guard a mistake that would otherwise pass unnoticed. The
// XSRF header is Jira's own precondition, and a client that omitted it
// would work against a permissive stub and fail against every real site.
// The multipart parse is the stronger one: every other write on this
// client sends JSON, so composing JSON here is the obvious wrong turn,
// and a stub that accepted anything could not tell the two apart.
func serveAddAttachment(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Atlassian-Token") != "no-check" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/"), "/attachments")
	// Only the keys the success recipe names; every other key is a 404,
	// which is the failure recipe's own path.
	if key != "TST-2" && key != "TST-3" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	reader, err := r.MultipartReader()
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	names := make([]any, 0, 2)
	for i := 0; ; i++ {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		if part.FormName() != "file" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Drained rather than ignored: the CLI streams the body, so a
		// stub that stopped reading would deadlock the writer instead of
		// failing a check.
		if _, err := io.Copy(io.Discard, part); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		names = append(names, map[string]any{
			"id":       fmt.Sprintf("%d", 10400+i),
			"filename": hostile(part.FileName()),
			"size":     len(corpusAttachmentBody),
			"created":  "2026-08-13T10:00:00.000+0000",
			"author": map[string]any{
				"accountId":   "5b10a2844c20165700ede21g",
				"displayName": hostile("Corpus Uploader"),
			},
		})
	}
	if len(names) == 0 {
		// A multipart body with no file parts is not an upload.
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(mustMarshal(names)); err != nil {
		return
	}
}

// serveAttachmentMeta answers the metadata read with a hostile filename;
// see corpusAttachmentFilename.
func serveAttachmentMeta(w http.ResponseWriter, r *http.Request) {
	if strings.TrimPrefix(r.URL.Path, "/rest/api/3/attachment/") != corpusAttachmentID {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(mustMarshal(map[string]any{
		"id":       json.Number(corpusAttachmentID),
		"filename": corpusAttachmentFilename,
		"size":     len(corpusAttachmentBody),
		"mimeType": "text/plain",
		"created":  "2026-08-13T10:00:00.000+0000",
		"author": map[string]any{
			"accountId":   "5b10a2844c20165700ede21g",
			"displayName": hostile("Corpus Uploader"),
		},
	})); err != nil {
		return
	}
}

// serveAttachmentContent streams the bytes. No redirect: the real
// endpoint 303s to a signed media URL and the transport follows it, so
// answering directly here captures the same thing the CLI would end up
// reading without standing up a second host.
func serveAttachmentContent(w http.ResponseWriter, r *http.Request) {
	if strings.TrimPrefix(r.URL.Path, "/rest/api/3/attachment/content/") != corpusAttachmentID {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	if _, err := io.WriteString(w, corpusAttachmentBody); err != nil {
		return
	}
}

// serveDeleteAttachment answers the delete, 204 for the one id the
// recipes name and 404 for everything else.
func serveDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	if strings.TrimPrefix(r.URL.Path, "/rest/api/3/attachment/") != corpusAttachmentID {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// The lifecycle fixtures and handlers.

// corpusRankAnchor is the issue every rank recipe moves relative to.
const corpusRankAnchor = "TST-9"

// serveIssueDelete answers the delete, and requires the parameter that
// decides how much disappears.
//
// deleteSubtasks is checked for PRESENCE rather than for a value: the
// endpoint defaults it to false, so a CLI that omitted it would work
// against a permissive stub and against a real site, right up until
// somebody read the wire and could not tell which request had been made.
// The command sends it always; this is what holds it to that.
func serveIssueDelete(w http.ResponseWriter, r *http.Request) {
	if !r.URL.Query().Has("deleteSubtasks") {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
	if key != "TST-2" && key != "TST-3" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// serveCloneSource answers the read a clone makes first.
//
// The fields it returns are hostile where they are tenant-authored (the
// summary) and structural where they are not, and it deliberately
// includes status, which must not carry, and assignee, which must carry.
// The create handler independently checks both parts of the copy policy.
func serveCloneSource(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
	if key != "TST-2" && key != "TST-3" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(mustMarshal(map[string]any{
		"id":   "10500",
		"key":  key,
		"self": "https://example.atlassian.net/rest/api/3/issue/10500",
		"fields": map[string]any{
			"summary":     hostile("Original summary"),
			"description": map[string]any{"type": "doc", "version": 1, "content": []any{}},
			"issuetype":   map[string]any{"name": "Task"},
			"project":     map[string]any{"key": "TST"},
			"priority":    map[string]any{"name": "High"},
			"labels":      []any{"corpus"},
			// Source workflow status is excluded; assignment must carry.
			"status":   map[string]any{"name": "Done"},
			"assignee": map[string]any{"accountId": "someone-else"},
		},
	})); err != nil {
		return
	}
}

// serveCloneCreate answers the create half, and REFUSES a payload
// carrying anything the copy policy excludes.
//
// The stub refuses source workflow fields and requires the minimal copied
// assignment, so source field loss cannot pass as an ordinary success.
func serveCloneCreate(w http.ResponseWriter, r *http.Request) {
	sent, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var body struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(sent, &body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	for _, forbidden := range []string{"status", "resolution"} {
		if _, present := body.Fields[forbidden]; present {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	if string(body.Fields["assignee"]) != `{"accountId":"someone-else"}` {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if _, present := body.Fields["summary"]; !present {
		// A clone with no summary is not a copy of anything.
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if _, err := w.Write(mustMarshal(map[string]any{
		"id":   "10600",
		"key":  "TST-100",
		"self": "https://example.atlassian.net/rest/api/3/issue/10600",
		"transition": map[string]any{
			"status":            200,
			"warningCollection": map[string]any{"warnings": []any{"transition receipt"}},
		},
		"watchers": map[string]any{"status": 204},
		"future": map[string]any{
			"opaque": "native receipt sentinel",
			"values": []any{nil, false, 13},
		},
	})); err != nil {
		return
	}
}

// serveRank answers the Agile rank endpoint, and requires the one-array
// shape.
//
// Two checks, both guarding a mistake that would pass unnoticed against
// a permissive stub. The issues must arrive as ONE array — a CLI that
// fanned out would send several requests carrying one issue each, and
// each would look fine in isolation while the resulting order was
// whatever the goroutines produced. And exactly one anchor must be set,
// since the endpoint has no meaning for both or neither.
func serveRank(w http.ResponseWriter, r *http.Request) {
	sent, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var body struct {
		Issues          []string `json:"issues"`
		RankBeforeIssue string   `json:"rankBeforeIssue"`
		RankAfterIssue  string   `json:"rankAfterIssue"`
	}
	if err := json.Unmarshal(sent, &body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	anchored := (body.RankBeforeIssue != "") != (body.RankAfterIssue != "")
	if !anchored || len(body.Issues) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	for _, key := range body.Issues {
		if key != "TST-2" && key != "TST-3" {
			// The failure recipe's own path. A 404 rather than a 207,
			// because an unknown issue is not a partial rank — nothing was
			// reordered at all.
			w.WriteHeader(http.StatusNotFound)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
