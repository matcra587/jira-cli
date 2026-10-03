//go:build outputaudit

package audit

import (
	"encoding/json"
	"fmt"
	"strings"
)

// attackPayload is the H4 sanitization axis's single definition of every
// raw-byte class a Jira response can smuggle toward a terminal — an
// ESC[ CSI SGR sequence (color injection), an OSC 8 hyperlink INCLUDING
// its ST terminator (a clickable-link injection, deliberately carrying
// no visible text of its own between open and close), a lone C0 control
// (BEL), DEL, a C1 control (NEL, U+0085 — correctly UTF-8 encoded, not a
// raw byte: a bare 0x85 byte on its own is not valid UTF-8, and the H4
// check must decode runes, not scan bytes, to see a C1 control that
// arrived this way), and a bare carriage return (the line-overwrite
// attack).
//
// Reused verbatim across every hostile fixture field below — the ONE
// place the payload set is defined, so a new attack class added here
// extends every field's coverage at once, per the design doc's "keep the
// payload list defined once" rule.
//
// This exact composition sanitizes to the EMPTY string: cli.Text/
// sanitize strips every byte it contains, with nothing left over (an
// OSC 8 sequence carrying no visible text between its open and close
// halves has nothing for ansi.Strip to preserve, and every remaining
// byte class — ESC, BEL, DEL, the C1 rune, CR — falls inside sanitize's
// own strip range). That property is load-bearing: every hostile field
// below is built as `hostile(benign)`, i.e. `benign + attackPayload`,
// so after sanitization it is the benign prefix byte for byte —
// verified empirically against cli.Text before this was wired in.
// hostileCommentPage's own `created` field depends on exactly this:
// cli.Text runs before cli.Time (sinks.go's TimeFrom), so a payload that
// left residual bytes behind would break that field's parseability and
// falsely trip H3's "no raw ISO timestamp" check for a reason that has
// nothing to do with H4.
const attackPayload = "\x1b[31m" +
	"\x1b]8;;http://evil.example/\x1b\\" +
	"\x1b]8;;\x1b\\" +
	"\x07" +
	"\x7f" +
	"\u0085" +
	"\r"

// legitimateMultibyte is deliberately NOT an attack. It exists to prove
// the H4 check decodes UTF-8 as RUNES rather than scanning raw bytes: 日
// alone encodes to the bytes 0xE6 0x97 0xA5, and 0x97 falls inside the
// very C1 byte-range (0x80-0x9F) a naive byte scanner would flag as a
// control byte. A correct rune-based check must let this through
// unflagged; folded into the hostile assignee's display name below, so
// the fixture that ships actually exercises the distinction rather than
// merely asserting it in a comment.
const legitimateMultibyte = "日本語"

// hostile appends attackPayload to a benign value, so an attacked field
// still reads as the kind of value it is (a name, a summary, a key)
// rather than pure noise — every field this fixture attacks is built
// this way.
func hostile(clean string) string {
	return clean + attackPayload
}

// mustMarshal panics on a marshal failure: this package's own fixture
// values are static and known-marshalable, so a failure here means the
// fixture-building code itself is broken, not a runtime condition any
// caller needs to recover from.
func mustMarshal(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic("audit: hostile fixture failed to marshal: " + err.Error())
	}
	return raw
}

// hostileCommentPage builds one comment page — the shape GET
// /rest/api/3/issue/{key}/comment returns, and the same shape an issue's
// own embedded fields.comment carries — whose author, created
// timestamp, and ADF body (a plain text run, a strong span, a code
// span, and a link whose text AND href both carry the payload) attack
// every comment-shaped field the coverage list names. total is reported
// independently of len(comments), preserving the original fixture's own
// pagination-hint proof: a total larger than what actually came back.
func hostileCommentPage(startAt, maxResults, total int) map[string]any {
	return map[string]any{
		"startAt":    startAt,
		"maxResults": maxResults,
		"total":      total,
		"comments": []any{
			map[string]any{
				"id": "90050",
				"author": map[string]any{
					"accountId":   "5b10a2844c20165700ede21g",
					"displayName": hostile("Corpus Commenter"),
				},
				"created": hostile("2024-03-01T10:00:00.000+0000"),
				"updated": "2024-03-01T10:00:00.000+0000",
				"body": map[string]any{
					"type":    "doc",
					"version": 1,
					"content": []any{
						map[string]any{
							"type": "paragraph",
							"content": []any{
								map[string]any{"type": "text", "text": hostile("Hostile paragraph body")},
								map[string]any{"type": "text", "text": hostile(" strong span"), "marks": []any{
									map[string]any{"type": "strong"},
								}},
								map[string]any{"type": "text", "text": hostile(" code span"), "marks": []any{
									map[string]any{"type": "code"},
								}},
								map[string]any{"type": "text", "text": hostile(" link text"), "marks": []any{
									map[string]any{"type": "link", "attrs": map[string]any{"href": hostile("https://evil.example/link")}},
								}},
							},
						},
					},
					// The M10 passthrough-fidelity proof
					// (checkSuccessPassthroughFidelity): one field neither
					// this package nor internal/adf models, surviving
					// verbatim. Left untouched by the hostile payload —
					// M10 pins an EXACT value, and this field's job is
					// proving byte-for-byte passthrough, not sanitization.
					"customUnmodeledField": "comment custom value",
				},
			},
			// A SECOND comment, so a page can carry more than one entry.
			// That is what puts issue view's "Showing N of TOTAL" banner
			// and its count hint on screen at the default --comments 1:
			// with a single-comment page everything is shown, neither line
			// renders, and the sanitizing of the issue key they interpolate
			// goes UNPROVEN — a gap found by deleting that sanitizing call
			// and watching H4 stay green. Deliberately light: the first
			// comment already attacks every comment-shaped field, so this
			// one only has to exist.
			map[string]any{
				"id": "90051",
				"author": map[string]any{
					"accountId":   "5b10a2844c20165700ede21h",
					"displayName": hostile("Second Commenter"),
				},
				"created": hostile("2024-03-02T11:00:00.000+0000"),
				"updated": "2024-03-02T11:00:00.000+0000",
				"body": map[string]any{
					"type":    "doc",
					"version": 1,
					"content": []any{
						map[string]any{
							"type": "paragraph",
							"content": []any{
								map[string]any{"type": "text", "text": hostile("Second body")},
							},
						},
					},
				},
			},
		},
	}
}

// hostileIssueJSON builds the wire body the "issue" stub serves for key
// TST-2: the same curated shape and M10 custom-field proof the original
// (pre-audit-axes) fixture carried, with every Jira-authored string
// field the coverage list names now carrying attackPayload — summary,
// status name, issue type name, priority name, assignee and reporter
// display names, each label, and the issue's own "key" field. The key is
// deliberately decoupled from the URL routing key the stub matches
// against (see stubServers["issue"]): a compromised or merely malformed
// Jira response can say anything in its OWN key field regardless of
// what was requested, which is exactly the case H4 exists to defend
// against, and using the same value here ("TST-2" + attackPayload)
// keeps the fixture's identity recognizable under sanitization.
//
// The status name is "Ready for Review" — deliberately NOT one of Jira
// Cloud's three English defaults ("To Do"/"In Progress"/"Done") — paired
// with a real statusCategory (key "indeterminate"). This is the exact
// regression cli.Status's category-driven coloring exists to fix: a
// project's own custom workflow status name, colored correctly only
// because Jira told this CLI which category it belongs to. The OLD
// name-only Status would have rendered this status completely plain;
// using one of the three defaults here would have colored under either
// implementation and proven nothing about the fix.
//
// fields.comment embeds a hostile page too, so issue view's inline
// comment preview (runView's embedded-page fast path, view.go) exercises
// the comment fields without a second network call — the "issue" stub
// serves only GET /rest/api/3/issue/{key}, not the /comment sub-route.
func hostileIssueJSON() []byte {
	return mustMarshal(map[string]any{
		"id":   "10002",
		"key":  hostile("TST-2"),
		"self": "https://example.atlassian.net/rest/api/3/issue/10002",
		"fields": map[string]any{
			"summary": hostile("Audit corpus fixture for issue view"),
			"status": map[string]any{
				"name": hostile("Ready for Review"),
				"statusCategory": map[string]any{
					"key":       "indeterminate",
					"name":      "Ready for Review",
					"colorName": "yellow",
				},
			},
			"issuetype":         map[string]any{"name": hostile("Task")},
			"priority":          map[string]any{"name": hostile("Medium")},
			"assignee":          map[string]any{"displayName": hostile("Corpus Assignee") + " " + legitimateMultibyte},
			"reporter":          map[string]any{"displayName": hostile("Corpus Reporter")},
			"labels":            []any{hostile("audit-corpus"), hostile("second-label")},
			"created":           "2024-01-15T09:30:00Z",
			"updated":           "2024-02-20T14:45:00Z",
			"comment":           hostileCommentPage(0, 50, 2),
			"customfield_10011": "corpus custom value",
		},
	})
}

// hostileCommentsJSON builds the wire body the "comments" stub serves
// for key TST-2: GET /rest/api/3/issue/{key}/comment's own response,
// reusing hostileCommentPage — issue.comment.list is a dedicated,
// separately-paginated read of comment-shaped data, and sharing the
// builder keeps this fixture and the issue's own embedded page from
// drifting apart. total (3) stays larger than the one comment actually
// returned, preserving the original fixture's pagination-hint proof.
func hostileCommentsJSON() []byte {
	return mustMarshal(hostileCommentPage(0, 50, 3))
}

// hostileIssueLinksJSON builds the wire body the "issuelinks" stub
// serves: GET /rest/api/3/issue/{key} answered for a `issue link list`
// read, which narrows its request to the one `issuelinks` field (see
// internal/cli/issue/link.go's linkFields — Jira has no
// list-an-issue's-links endpoint, so an issue read IS the link read).
//
// It is a SEPARATE fixture from hostileIssueJSON rather than links bolted
// onto it, even though both are served for the same path. The two
// scenarios want different things from the same endpoint — issue view
// wants a full curated field set, link list wants one field with links in
// it — and folding links into the shared fixture would change every
// issue.view capture to cover a command issue.view does not run.
//
// Every Jira-authored string a link contributes carries attackPayload:
// the link type's name and BOTH its directional phrasings (the `relation`
// this command derives, and the one it renders in the LINK column), and
// the linked issue's own key, summary, status name, type name and
// priority name — the five values the linked-issue columns draw.
//
// The issue's OWN key is deliberately clean, unlike hostileIssueJSON's.
// Here it is the keyed contract's correlation handle: `data.results[]` is
// ordered by argv and each entry names the key it answers for, and the
// M3 check reads exactly those keys back to prove the ordering survived a
// concurrent fan-out. Attacking it would make that proof unwritable while
// buying no H4 coverage the LINKED issue's key does not already give —
// and the linked key is the one this command's table actually renders.
//
// linkCount picks the shape: 2 for a key with links, 0 for one with none
// (the empty-array contract, and the human render's own "No links."
// branch — an empty result must never be silent).
func hostileIssueLinksJSON(key string, linkCount int) []byte {
	links := make([]any, 0, linkCount)
	if linkCount > 0 {
		// An INWARD link first: the direction whose relation is the type's
		// `inward` phrase, and the one a caller most often misreads
		// (TST-2 "is blocked by" TST-90, not the other way round).
		links = append(links, map[string]any{
			"id": "10100",
			"type": map[string]any{
				"id":      "10001",
				"name":    hostile("Blocks"),
				"inward":  hostile("is blocked by"),
				"outward": hostile("blocks"),
			},
			"inwardIssue": hostileLinkedIssue("TST-90", "Hostile blocker", "Ready for Review", "indeterminate", "Bug", "High"),
		})
	}
	if linkCount > 1 {
		// An OUTWARD link second, so one fixture covers both directions
		// and the renderer's own per-direction phrasing choice.
		links = append(links, map[string]any{
			"id": "10101",
			"type": map[string]any{
				"id":      "10002",
				"name":    hostile("Relates"),
				"inward":  hostile("relates to"),
				"outward": hostile("relates to"),
			},
			"outwardIssue": hostileLinkedIssue("TST-91", "Hostile relation", "Done", "done", "Task", "Low"),
		})
	}
	return mustMarshal(map[string]any{
		"id":     "10500",
		"key":    key,
		"self":   "https://example.atlassian.net/rest/api/3/issue/10500",
		"fields": map[string]any{"issuelinks": links},
	})
}

// hostileIssueAttachmentsJSON builds the wire body the "attachments"
// stub serves: GET /rest/api/3/issue/{key} answered for an `issue
// attachment list` read, which narrows its request to the one
// `attachment` field (see internal/cli/issue/attachment.go's
// attachmentFields — Jira has no list-an-issue's-attachments endpoint,
// so an issue read IS the attachment read).
//
// A SEPARATE fixture from hostileIssueJSON and hostileIssueLinksJSON
// rather than a third field bolted onto either, for the reason
// hostileIssueLinksJSON's own doc comment gives: the scenarios want
// different things from the same path, and folding attachments into a
// shared fixture would change every issue.view capture to cover a
// command issue.view does not run.
//
// Every Jira-authored string an attachment contributes carries
// attackPayload: the FILENAME above all — it crosses into the CLI from a
// filesystem rather than a Jira form, which makes it the likeliest
// carrier of hostile bytes this table renders — plus the MIME type and
// the uploader's display name, the other two strings the columns draw.
// The issue's own key stays clean for the same reason it does in the
// links fixture: it is the keyed contract's correlation handle, and M3
// reads it back to prove argv ordering survived a concurrent fan-out.
//
// The sizes are deliberately spread across unit boundaries — a few
// hundred bytes, a few KiB, a few MiB — so ONE capture exercises every
// branch of cli.Bytes's unit selection rather than just the one a single
// plausible file size would land in.
//
// count picks the shape: 3 for a key with attachments, 0 for one with
// none (the empty-array contract, and the human render's own "No
// attachments." branch — an empty result must never be silent).
func hostileIssueAttachmentsJSON(key string, count int) []byte {
	// Each entry is {filename, mime, size, author}; the sizes straddle
	// the B/KiB/MiB boundaries as the doc comment above explains.
	shapes := []struct {
		filename string
		mime     string
		size     int64
		author   string
	}{
		{"crash-log.txt", "text/plain", 812, "Corpus Uploader"},
		{"screenshot of the bug.png", "image/png", 48_512, "Corpus Uploader"},
		{"heap-dump.hprof", "application/octet-stream", 3_355_443, "Second Uploader"},
	}
	attachments := make([]any, 0, count)
	for i := range min(count, len(shapes)) {
		s := shapes[i]
		attachments = append(attachments, map[string]any{
			"id":       fmt.Sprintf("105%02d", i),
			"filename": hostile(s.filename),
			"mimeType": hostile(s.mime),
			"size":     s.size,
			"created":  "2024-02-20T14:45:00.000-0500",
			"self":     fmt.Sprintf("https://example.atlassian.net/rest/api/3/attachment/105%02d", i),
			"content":  fmt.Sprintf("https://example.atlassian.net/rest/api/3/attachment/content/105%02d", i),
			"author": map[string]any{
				"accountId":   "5b10a2844c20165700ede21g",
				"displayName": hostile(s.author),
			},
		})
	}
	return mustMarshal(map[string]any{
		"id":     "10500",
		"key":    key,
		"self":   "https://example.atlassian.net/rest/api/3/issue/10500",
		"fields": map[string]any{"attachment": attachments},
	})
}

// hostileIssueWatchersJSON builds a GET
// /rest/api/3/issue/{key}/watchers response with count watchers, every
// display name attacked.
//
// Unlike its link and attachment siblings this is NOT an issue body:
// the watcher endpoint answers with the Watchers schema alone, so there
// is no `fields` wrapper and no issue id or key in it at all — which is
// itself part of what the capture pins, since it is why a successful
// watchers result publishes an issue ref carrying only the key.
//
// The shapes cover the three account types a caller has to tell apart
// and both sides of `active`: an ordinary user, a deactivated one (the
// departed-colleague case the ACTIVE column exists for), and an app
// (the integration a human scanning the list would otherwise mistake
// for a person).
//
// watchCount is passed separately from count so a fixture can serve the
// permission-restricted response Jira gives a caller without "View
// voters and watchers": a populated count with no array at all.
func hostileIssueWatchersJSON(count int, watchCount int64, isWatching bool) []byte {
	shapes := []struct {
		name        string
		accountType string
		active      bool
	}{
		{"Corpus Watcher", "atlassian", true},
		{"Departed Colleague", "atlassian", false},
		{"Deployment Bot", "app", true},
	}
	watchers := make([]any, 0, count)
	for i := range min(count, len(shapes)) {
		s := shapes[i]
		watchers = append(watchers, map[string]any{
			"accountId":   fmt.Sprintf("5b10a2844c20165700ede2%02d", i),
			"displayName": hostile(s.name),
			"accountType": hostile(s.accountType),
			"active":      s.active,
			"self":        fmt.Sprintf("https://example.atlassian.net/rest/api/3/user?accountId=5b10a2844c20165700ede2%02d", i),
		})
	}
	body := map[string]any{
		"self":       "https://example.atlassian.net/rest/api/3/issue/10500/watchers",
		"isWatching": isWatching,
		"watchCount": watchCount,
	}
	// Omitted entirely, not sent empty, when the caller may not see the
	// list — which is exactly how Jira answers, and the shape the CLI's
	// count-beside-the-array contract has to survive.
	if count > 0 {
		body["watchers"] = watchers
	}
	return mustMarshal(body)
}

// hostileLinkedIssue builds one issue at the far end of a link — the
// spec's own closed "Fields" set (summary, status, issuetype, priority),
// every string of it attacked.
//
// The status name/category pairing is the same one hostileIssueJSON uses
// and for the same reason: a custom workflow name with a real category is
// what proves cli.Status colors by CATEGORY, which a Jira Cloud default
// name would have colored under either implementation.
func hostileLinkedIssue(key, summary, status, category, issueType, priority string) map[string]any {
	return map[string]any{
		"id":   "20" + strings.TrimPrefix(key, "TST-"),
		"key":  hostile(key),
		"self": "https://example.atlassian.net/rest/api/3/issue/20" + strings.TrimPrefix(key, "TST-"),
		"fields": map[string]any{
			"summary": hostile(summary),
			"status": map[string]any{
				"name": hostile(status),
				"statusCategory": map[string]any{
					"key":       category,
					"name":      status,
					"colorName": "yellow",
				},
			},
			"issuetype": map[string]any{"name": hostile(issueType)},
			"priority":  map[string]any{"name": hostile(priority)},
		},
	}
}

// searchPageTwoToken is the cursor the "search" stub's FIRST page hands
// back, and the value its handler matches on to serve the SECOND — a
// plain, non-hostile string. Earlier this fixture also attacked the
// cursor itself, on the theory that issue list's human render echoed it
// verbatim in a "more results" hint; that hint no longer shows the raw
// token at all (list.go's nextPageHint says "raise --limit" or names
// --all's own cap, never the cursor — the whole point of this slice),
// so attacking the cursor stopped exercising anything H4 checks. Left
// clean here so the stub's own page-routing stays simple and legible.
const searchPageTwoToken = "corpus-page-2"

// hostileSearchPageOneJSON builds the FIRST page the "search" stub
// serves for a recognized JQL with no nextPageToken: POST
// /rest/api/3/search/jql's own response shape (jira.SearchResults).
// issue.list reuses IssueView per row (the same shape hostileIssueJSON's
// own fields section proves for issue view), so this attacks the same
// Jira-authored string fields a table row actually renders — summary,
// status name, issue type name, assignee display name, and the issue's
// own key.
//
// isLast is false and nextPageToken names the second page
// (searchPageTwoToken): the recipe driving this fixture (scenarios.go's
// issue-list-success) passes --limit 2, so issue.list's own
// auto-pagination loop (list.go's autoPaginate) genuinely fetches BOTH
// pages before it has collected enough — a real two-request walk, not a
// single response merely claiming there is more. customfield_10011
// carries the M10 passthrough-fidelity proof, deliberately left clean
// (M10 pins an exact value, and attacking it would break that pin
// rather than test anything H4 does not already cover elsewhere in this
// fixture).
//
// The status name is "Ready for Review" with statusCategory key
// "indeterminate" — the same custom-status-name-with-known-category
// shape hostileIssueJSON uses, and for the same reason: it is the
// STATUS column's own proof of cli.Status's category-driven fix, which
// a Jira Cloud default name ("In Progress") would not have exercised.
func hostileSearchPageOneJSON() []byte {
	return mustMarshal(map[string]any{
		"isLast":        false,
		"nextPageToken": searchPageTwoToken,
		"issues": []any{
			map[string]any{
				"id":   "10002",
				"key":  hostile("TST-2"),
				"self": "https://example.atlassian.net/rest/api/3/issue/10002",
				"fields": map[string]any{
					"summary": hostile("Audit corpus fixture for issue list"),
					"status": map[string]any{
						"name": hostile("Ready for Review"),
						"statusCategory": map[string]any{
							"key":       "indeterminate",
							"name":      "Ready for Review",
							"colorName": "yellow",
						},
					},
					"issuetype":         map[string]any{"name": hostile("Task")},
					"assignee":          map[string]any{"displayName": hostile("Corpus Assignee") + " " + legitimateMultibyte},
					"updated":           "2024-02-20T14:45:00Z",
					"customfield_10011": "list custom value",
				},
			},
		},
	})
}

// hostileSearchPageTwoJSON builds the SECOND page, served when the stub
// sees searchPageTwoToken as the request's own nextPageToken: a clean
// (non-hostile) second issue, proving the auto-pagination loop actually
// APPENDS a later page's issues onto the first rather than merely
// re-requesting the same one — a stub that served the same fixture
// twice could not tell a genuine multi-page walk from a client that
// never advanced its cursor. isLast stays false and nextPageToken names
// a THIRD page no recipe ever requests (issue-list-success's own
// --limit 2 is satisfied by this page, so the walk stops here) — an
// honest "there would be more" the same way the original single-page
// fixture's own isLast:false was, not a page this stub needs to
// actually serve.
func hostileSearchPageTwoJSON() []byte {
	return mustMarshal(map[string]any{
		"isLast":        false,
		"nextPageToken": "corpus-page-3",
		"issues": []any{
			map[string]any{
				"id":   "10003",
				"key":  "TST-3",
				"self": "https://example.atlassian.net/rest/api/3/issue/10003",
				"fields": map[string]any{
					"summary": "Second corpus fixture for issue list",
					"status": map[string]any{
						"name": "To Do",
						"statusCategory": map[string]any{
							"key":       "new",
							"name":      "To Do",
							"colorName": "blue-gray",
						},
					},
					"issuetype": map[string]any{"name": "Task"},
					"updated":   "2024-02-21T09:00:00Z",
				},
			},
		},
	})
}

// searchApproximateCountJSON builds the "search" stub's
// /search/approximate-count response: the estimated-count sibling of
// the two /search/jql pages above, served for the SAME recognized JQL
// (scenarios.go's serveSearchApproximateCount). issue-list-success's
// own two-page walk (--limit 2 against a first page whose own
// isLast:false, hostileSearchPageOneJSON's own doc comment) ends
// without ever seeing is_last:true, so this recipe's human capture is
// the "more results exist" shape issue.list's own fetchCountEstimate
// gate (list.go) actually fires the count call for — 1234 is picked
// deliberately non-round so the rendered hint's thousands separator
// (`Showing 2 of ~1,234 — raise ...`) has a real grouping boundary to
// prove, the same reason TestRenderListShowsEstimateWithThousandsSeparator
// (list_test.go) picks a four-digit value rather than a smaller one a
// separator would never touch.
func searchApproximateCountJSON() []byte {
	return mustMarshal(map[string]any{"count": 1234})
}

// createdCommentJSON builds the 201 body the comment-add stub answers
// with: the comment Jira created, echoing back the ADF document that
// was actually posted.
//
// The echo is the point. It makes the capture prove the composed body
// survived the whole round trip — CLI compose, wire marshal, HTTP, wire
// unmarshal, envelope — rather than merely proving something was
// posted. A body that lost its bytes to HTML escaping on the way out
// would come back visibly different.
//
// The author is hostile like every other Jira-authored string in this
// file, because a created comment's author reaches the human render and
// the H4 sanitization axis has to see it. The posted body already
// carries whatever the recipe sent, so it is not attacked again here —
// the recipe's own fixture owns that choice.
func createdCommentJSON(key, id string, posted []byte) []byte {
	// The request is {"body": <adf>, "visibility"?: {...}}; the response
	// is a comment carrying that same body. A malformed request never
	// reaches here (the stub would have rejected it), but a decode
	// failure still must not panic the harness, so the body falls back
	// to an empty document rather than taking the process down.
	var req struct {
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(posted, &req); err != nil || len(req.Body) == 0 {
		req.Body = json.RawMessage(`{"type":"doc","version":1,"content":[]}`)
	}
	// The RESPONSE body carries one field the CLI does not model, which
	// is M10's fidelity proof for this op — the same role
	// comments[0].body.customUnmodeledField plays for issue.comment.list.
	//
	// It is injected here, into what the server SENDS, and never into
	// what the recipe posts. It could not go in the request: --json-input
	// validates against the ADF schema before sending, and the schema
	// rejects an unknown key, so a fixture carrying one would be refused
	// by the CLI rather than round-tripped. A response is not validated,
	// which is exactly why it is the passthrough boundary — and Jira
	// really does return fields this CLI does not model (renderedBody,
	// jsdPublic, properties).
	//
	// data.body — what was composed and sent — stays untouched, so the
	// capture still proves the request survived byte-for-byte alongside
	// this.
	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err == nil {
		body["customUnmodeledField"] = "comment add custom value"
		req.Body = mustMarshal(body)
	}
	return mustMarshal(map[string]any{
		"id":   id,
		"self": "https://example.atlassian.net/rest/api/3/issue/" + key + "/comment/" + id,
		"author": map[string]any{
			"accountId":   "5b10a2844c20165700ede21g",
			"displayName": hostile("Corpus Account"),
			"active":      true,
		},
		"body":    req.Body,
		"created": hostile("2024-03-03T12:00:00.000+0000"),
		"updated": hostile("2024-03-03T12:00:00.000+0000"),
	})
}

// hostileTransitionsJSON builds the transition list for one issue: the
// "Done" transition the recipes name, with or without a screen, and two
// distractors around it.
//
// The distractors are not filler. The list carries a transition whose
// own NAME differs from the status it leads to ("Start Progress" → "In
// Progress"), which is what proves the resolver matches on both — a
// resolver that looked only at `name` would silently fail every caller
// who typed the status they can see on the board. It also carries a
// transition whose hasScreen differs from Done's, so a resolver that
// read hasScreen off the wrong element of the list would flip the route
// and the capture would show it.
//
// Every Jira-authored string here is attacked (axis H4) EXCEPT one: the
// name of the transition the recipes actually ask for. That exception is
// forced and worth stating, because it looks like an oversight. A recipe
// types `Done` on the command line, and the CLI resolves it by comparing
// against these strings — so attacking the string it must match would
// mean nothing matches and the recipe would exercise the not-found path
// instead of the path it is named for. What the human render actually
// PRINTS is the target status (`moved to <status>`, from `to.name`), and
// that IS attacked, so the sanitization axis still sees a hostile value
// on the surface it reaches. The distractors are attacked throughout.
func hostileTransitionsJSON(doneHasScreen bool) []byte {
	return mustMarshal(map[string]any{
		"expand": "transitions",
		"transitions": []any{
			map[string]any{
				"id":            "11",
				"name":          hostile("Start Progress"),
				"hasScreen":     !doneHasScreen,
				"isGlobal":      false,
				"isInitial":     false,
				"isAvailable":   true,
				"isConditional": false,
				"to": map[string]any{
					"id":   "3",
					"name": hostile("In Progress"),
					"statusCategory": map[string]any{
						"id":   4,
						"key":  "indeterminate",
						"name": hostile("In Progress"),
					},
				},
			},
			map[string]any{
				"id": "31",
				// Clean, and only here — see this function's own doc
				// comment. This is the string the recipe's argv must match.
				"name":          "Done",
				"hasScreen":     doneHasScreen,
				"isGlobal":      false,
				"isInitial":     false,
				"isAvailable":   true,
				"isConditional": false,
				// One property the CLI does not model, which is this op's
				// M10 fidelity proof: a real transitions payload carries
				// `looped` and, under expand, a whole `fields` map. The
				// envelope must survive it without complaint.
				"looped": false,
				"to": map[string]any{
					"id":   "10001",
					"name": hostile("Done"),
					"statusCategory": map[string]any{
						"id":   3,
						"key":  "done",
						"name": hostile("Done"),
					},
				},
			},
			map[string]any{
				"id":            "41",
				"name":          hostile("Reopen"),
				"hasScreen":     false,
				"isGlobal":      true,
				"isInitial":     false,
				"isAvailable":   true,
				"isConditional": false,
				"to": map[string]any{
					"id":   "1",
					"name": hostile("To Do"),
					"statusCategory": map[string]any{
						"id":   2,
						"key":  "new",
						"name": hostile("To Do"),
					},
				},
			},
		},
	})
}
