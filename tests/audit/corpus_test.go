//go:build outputaudit

package audit

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
)

// axisCheck evaluates one axis over one capture. Checks are the
// INDEPENDENTLY written contract expectations (design §10): they never
// consult SchemaOf or any derivation sharing a declaration with the
// output under test — that would be the circular validation the design
// forbids. Where a check needs per-operation facts (which fields, which
// success shape), those live in hand-written tables in this file.
type axisCheck func(cap Capture, m command.Meta) (ok bool, detail string)

// axisChecks maps category × axis to its check. An expected cell whose
// (category, axis) has no entry here records a red cell naming the gap
// — a new operation extends this table alongside its recipes, it cannot
// silently ride an unwritten check.
var axisChecks = map[Category]map[Axis]axisCheck{
	CategorySuccess: {
		AxisM1:  checkSuccessIdentity,
		AxisM2:  checkSuccessDryRunAbsent,
		AxisM3:  checkSuccessKeyedResults,
		AxisM4:  checkSuccessSnakeCase,
		AxisM5:  checkSuccessTimestamps,
		AxisM6:  checkSuccessModeContract,
		AxisM7:  checkSuccessPreviewAbsent,
		AxisM8:  checkSuccessSchemaTruth,
		AxisM10: checkSuccessPassthroughFidelity,
		AxisM12: checkSuccessDeterminism,
		// H1/H3/H4 (human_test.go) run against the SAME human capture
		// cellAxes selects for CategorySuccess+ModeHuman — the check
		// functions read cap.Mode themselves rather than needing a
		// separate map per mode, exactly like checkSuccessModeContract
		// above already does for JSON vs compact. checkH1ViewPrimitives is
		// the one exception to "reads cap": its evidence is source code
		// (tests/guardrails' own AST scan), not this capture, but it still
		// only ever gets asked at this coordinate — cellAxes keeps H1 out
		// of every other one. H5 is the same idea one mode over: cellAxes
		// only ever pairs AxisH5 with a ModeHumanColor capture, so one
		// entry here covers it exactly as H1/H3/H4 does for ModeHuman.
		AxisH1: checkH1ViewPrimitives,
		AxisH3: checkH3Formats,
		AxisH4: checkH4Sanitized,
		AxisH5: checkH5StylingNests,
	},
	CategoryDryRun: {
		AxisM2: checkDryRunFlagTrue,
		AxisM7: checkDryRunPreview,
		// H2 attaches here, never to CategorySuccess: applicabilityFor
		// keeps it out of every read (a read never dry-runs at all), so
		// this is the only category it can ever be asked against. H3/H4/H5
		// are reused verbatim from the CategorySuccess table above: a
		// mutation's dry-run preview is the only human capture it has (see
		// cellAxes), so they attach here instead for auth.login/logout and
		// config.edit.
		AxisH2: checkH2Tense,
		AxisH3: checkH3Formats,
		AxisH4: checkH4Sanitized,
		AxisH5: checkH5StylingNests,
	},
	CategoryFailure: {AxisM11: checkFailureSurface},
}

// identityRefField names, per op declaring Identity, the top-level data
// key holding the domain object the CLI's identity slot lives at — M1's
// "never a bare scalar" rule proven against the field a caller actually
// reads, not merely against whichever field happens to satisfy the
// shape.
var identityRefField = map[string]string{
	"issue.view":         "issue",
	"issue.comment.list": "issue",
	// issue.list has no SINGLE identity object — it is a page of many
	// issues, not one — so its identity slot is the ARRAY at data.issues,
	// each element a ref object. checkSuccessIdentity below checks both
	// shapes: a single object (issue.view, issue.comment.list, whose
	// identity is the ONE issue the comment thread or the view belongs
	// to) and an array of objects (issue.list), because M1's actual
	// invariant — "an object, never a bare scalar" — applies exactly the
	// same way to every element of a list as it does to a single field.
	"issue.list": "issues",
	"issue.mine": "issues",
	// A KEYED op's identity is per RESULT: data.results[] is one entry
	// per requested key, and each entry carries its own issue ref (see
	// envelope.KeyedResults). The field named here is therefore the
	// results array, and checkSuccessIdentity reaches one level further
	// in for a keyed member — the invariant is the same "an object,
	// never a bare scalar", applied where a keyed command actually puts
	// its identity.
	"issue.link.list":       "results",
	"issue.attachment.list": "results",
	"issue.watchers.list":   "results",
	// The first MUTATION with an identity, and it is keyed like the
	// three reads above: a write answers per key too, so its identity
	// lives at results[].issue exactly as theirs does.
	"issue.comment.add": "results",
	// Keyed like every entry above it, for the same reason: a transition
	// answers per key, so its identity is at results[].issue.
	"issue.transition": "results",
	// Keyed, so per key like the writes above it.
	"issue.edit": "results",
	// THE ONLY SINGULAR IDENTITY ON A WRITE, and the only one anywhere in
	// this table that the REQUEST did not carry. Every other entry names
	// an issue the caller typed; a create learns its key from the 201, so
	// data.issue is the answer rather than an echo. M1's invariant is
	// unchanged — an object, never a bare scalar — and it is checked on
	// the success capture alone, which is what lets the field stay
	// genuinely absent from a preview that has no key to report.
	"issue.create": "issue",
	// The first two mutations whose identity is a SINGLE object again,
	// and the reason is the same one that keeps them out of
	// keyedResultKeys below: each addresses one comment on one issue, so
	// there is no per-key nesting for the identity to live inside. They
	// sit beside issue.view and issue.comment.list here, not beside the
	// keyed writes, which is exactly what M1 should show — the slot moves
	// with the arity, not with whether the op writes.
	"issue.comment.edit":   "issue",
	"issue.comment.delete": "issue",
	// Keyed writes, so their identity is per result, like every other
	// keyed member.
	"issue.watchers.add":    "results",
	"issue.watchers.remove": "results",
	// Keyed too, and link.create is the entry that shows the slot is
	// decided by ARITY rather than by what else the envelope carries: it
	// also publishes a `to` issue at the top, which is the link's other
	// end and NOT the operation's subject. The identity is still per
	// result.
	"issue.link.create":  "results",
	"issue.weblink.add":  "results",
	"issue.weblink.list": "results",
	// The single-target remote-link delete, whose identity is one object
	// at data.issue — beside issue.comment.delete for the same reason.
	//
	// issue.link.delete is deliberately ABSENT from this table and always
	// will be: it declares no Identity, because a link id is unique
	// across the site and the operation names no issue at all (see
	// envelope.IssueLinkDeleteOutput). Its M1 cell is NotApplicable
	// rather than red, which is applicabilityFor's own conditional doing
	// the work.
	"issue.weblink.delete": "issue",
	// The attachment upload, keyed like every other keyed write.
	"issue.attachment.add": "results",
	// The three lifecycle writes, keyed like every other keyed member.
	"issue.delete": "results",
	"issue.clone":  "results",
	"issue.rank":   "results",
	// issue.attachment.download and issue.attachment.delete are
	// deliberately ABSENT and always will be, for issue.link.delete's
	// reason: an attachment id is unique across the site, so neither
	// operation names an issue and neither declares Identity. Their M1
	// cells are NotApplicable rather than red.
}

// checkSuccessIdentity is M1: the identity slot at the declared field is
// a real object (issue.view, issue.comment.list) or an array of real
// objects (issue.list — see identityRefField's own doc comment for why
// a list command's identity is per-element rather than singular), never
// a bare scalar anywhere in it. key is the one member envelope.IssueRef
// promises unconditionally (see its own doc comment); id and self are
// omitted, never emitted empty, when an operation genuinely cannot
// resolve them — issue.comment.list's wire response (GET
// .../issue/{key}/comment) carries no issue-level id/self at all, unlike
// GetIssue's — so their ABSENCE is not itself an M1 violation, but a
// field that IS present must still be a real, non-empty value: M1's
// actual invariant is "an object, never a bare scalar", not "every
// metadata field always resolved".
func checkSuccessIdentity(cap Capture, m command.Meta) (bool, string) {
	field, ok := identityRefField[m.Op]
	if !ok {
		return false, fmt.Sprintf("no identity field expectation written for op %q — add it beside its recipes", m.Op)
	}
	data, err := successData(cap)
	if err != nil {
		return false, err.Error()
	}
	raw, present := data[field]
	if !present {
		return false, fmt.Sprintf("data.%s missing — the declared identity slot is absent", field)
	}
	if m.Keyed {
		return checkKeyedIdentity(field, raw)
	}
	if m.Op == "user.search" {
		return checkUserSearchIdentity(raw)
	}
	if m.Op == "boards.list" {
		return checkBoardsIdentity(raw)
	}
	if m.Op == "issue.move.status" {
		// A bulk task has an opaque ID, never an issue key. Preserve the
		// identity-object invariant without inventing a different resource.
		object, ok := raw.(map[string]any)
		if !ok {
			return false, "data.task must be an identity object"
		}
		id, ok := object["id"].(string)
		if !ok || strings.TrimSpace(id) == "" {
			return false, "data.task.id must be a non-empty string"
		}
		return true, ""
	}
	switch node := raw.(type) {
	case map[string]any:
		return checkIdentityRefObject(fmt.Sprintf("data.%s", field), node)
	case []any:
		// An empty page (a search matching nothing) has no element to
		// check and is not itself an M1 violation — the invariant is
		// about the SHAPE of whatever elements exist, not that at least
		// one must.
		for i, elem := range node {
			obj, ok := elem.(map[string]any)
			if !ok {
				return false, fmt.Sprintf("data.%s[%d] = %v (%T), want an object — a bare scalar identity violates M1", field, i, elem, elem)
			}
			if ok, msg := checkIdentityRefObject(fmt.Sprintf("data.%s[%d]", field, i), obj); !ok {
				return false, msg
			}
		}
		return true, ""
	default:
		return false, fmt.Sprintf("data.%s = %v (%T), want an object or an array of objects — a bare scalar identity violates M1", field, raw, raw)
	}
}

// checkBoardsIdentity requires positive integer board IDs.
func checkBoardsIdentity(raw any) (bool, string) {
	rows, ok := raw.([]any)
	if !ok {
		return false, "data.boards must be an array of identity objects"
	}
	for i, row := range rows {
		object, ok := row.(map[string]any)
		if !ok {
			return false, fmt.Sprintf("data.boards[%d] must be an identity object", i)
		}
		id, ok := object["id"].(float64)
		if !ok || id <= 0 || id != math.Trunc(id) {
			return false, fmt.Sprintf("data.boards[%d].id must be a positive integer", i)
		}
	}
	return true, ""
}

// checkUserSearchIdentity preserves the same object invariant with an account
// identifier. An issue key would invent an identity the user endpoint never has.
func checkUserSearchIdentity(raw any) (bool, string) {
	rows, ok := raw.([]any)
	if !ok {
		return false, "data.users must be an array of identity objects"
	}
	for i, row := range rows {
		obj, ok := row.(map[string]any)
		if !ok {
			return false, fmt.Sprintf("data.users[%d] must be an identity object", i)
		}
		id, ok := obj["account_id"].(string)
		if !ok || id == "" {
			return false, fmt.Sprintf("data.users[%d].account_id must be a non-empty string", i)
		}
	}
	return true, ""
}

// checkIdentityRefObject is checkSuccessIdentity's per-object check,
// shared by the single-object and array-of-objects shapes: key is
// present and non-empty, and id/self, when present at all, are also
// non-empty strings rather than an emitted-empty placeholder.
func checkIdentityRefObject(path string, obj map[string]any) (bool, string) {
	v, present := obj["key"]
	if !present {
		return false, fmt.Sprintf("%s.key missing", path)
	}
	if s, isStr := v.(string); !isStr || s == "" {
		return false, fmt.Sprintf("%s.key = %v, want a non-empty string", path, v)
	}
	for _, key := range []string{"id", "self"} {
		v, present := obj[key]
		if !present {
			continue
		}
		if s, isStr := v.(string); !isStr || s == "" {
			return false, fmt.Sprintf("%s.%s = %v, want a non-empty string when present", path, key, v)
		}
	}
	return true, ""
}

// checkKeyedIdentity is checkSuccessIdentity's keyed shape: every entry
// of the results array carries its own identity object at .issue. A
// keyed command has no single identity to put at data.<noun> — it
// answers for several keys — so the rule applies once per entry, exactly
// as it applies once per element of issue.list's own issues array.
func checkKeyedIdentity(field string, raw any) (bool, string) {
	results, ok := raw.([]any)
	if !ok {
		return false, fmt.Sprintf("data.%s = %T, want an array — a keyed command answers per key", field, raw)
	}
	if len(results) == 0 {
		return false, fmt.Sprintf("data.%s is empty; a keyed command takes at least one key, so every capture has at least one result", field)
	}
	for i, elem := range results {
		entry, ok := elem.(map[string]any)
		if !ok {
			return false, fmt.Sprintf("data.%s[%d] is not an object", field, i)
		}
		issue, ok := entry["issue"].(map[string]any)
		if !ok {
			return false, fmt.Sprintf("data.%s[%d].issue = %v (%T), want an object — a bare-key result violates M1", field, i, entry["issue"], entry["issue"])
		}
		if ok, msg := checkIdentityRefObject(fmt.Sprintf("data.%s[%d].issue", field, i), issue); !ok {
			return false, msg
		}
	}
	return true, ""
}

// keyedResultKeys names, per keyed op, the keys its success recipe
// passes — IN THE ORDER it passes them. Hand-written beside the recipe
// rather than re-derived from Scenario.Args, so the ordering expectation
// is stated independently of the argv the harness happens to send: a
// recipe edited to pass different keys has to say so here too.
var keyedResultKeys = map[string][]string{
	"cache.refresh":         {"fields", "labels"},
	"issue.link.list":       {"TST-2", "TST-3"},
	"issue.attachment.list": {"TST-2", "TST-3"},
	// Three, where its siblings pass two: TST-4 is the
	// permission-restricted response only this command can receive (see
	// the recipe's own comment in scenarios.go).
	"issue.watchers.list": {"TST-2", "TST-3", "TST-4"},
	// Two, and for the reason the reads pass two: with one key the
	// ordered-results contract is invisible. It matters more on a write
	// — results[] is the list a caller retries from, so an entry under
	// the wrong key would send the retry at an issue that already has
	// the comment.
	"issue.comment.add": {"TST-2", "TST-3"},
	// Two, and here the two keys are not interchangeable the way they
	// are above: TST-2's workflow has a transition screen and TST-3's
	// does not, so the pair is what proves the route is chosen PER KEY.
	// An implementation that read the first key's workflow and applied
	// that verdict to the rest would pass a single-key capture and
	// silently lose the second issue's comment.
	"issue.transition": {"TST-2", "TST-3"},
	// Two, for issue.comment.add's reason exactly: results[] is the list
	// a caller retries from, and an entry filed under the wrong key would
	// send the retry at an issue that already carries the edit.
	"issue.edit": {"TST-2", "TST-3"},
	// Two apiece, for comment.add's reason — with one key the ordered-
	// results contract is invisible, and on a write results[] is the list
	// a caller retries from. The two verbs pass the SAME pair rather than
	// one covering a case the other does not: they share an executor, and
	// the point of capturing both is that the shared code produces the
	// same ordering guarantee under each verb.
	"issue.watchers.add":    {"TST-2", "TST-3"},
	"issue.watchers.remove": {"TST-2", "TST-3"},
	// ONE key, and the only single-key entry in this table. The stub
	// accepts exactly one source so that a request with reversed ends
	// lands on a 400 rather than being mistaken for the other key's (see
	// serveLinkIssues) — this recipe exists to prove the DIRECTION, and
	// the ordered-results contract is pinned by every other entry here.
	"issue.link.create":    {"TST-2"},
	"issue.weblink.add":    {"TST-2", "TST-3"},
	"issue.weblink.list":   {"TST-2", "TST-3"},
	"issue.attachment.add": {"TST-2", "TST-3"},
	"issue.delete":         {"TST-2", "TST-3"},
	"issue.clone":          {"TST-2", "TST-3"},
	// Two, and for rank the pair proves something no other entry here
	// does: both arrive in ONE request, and the stub refuses anything
	// else — so this is the only keyed op whose ordering is asserted
	// against the wire rather than against a fan-out.
	"issue.rank": {"TST-2", "TST-3"},
}

// checkSuccessKeyedResults is M3: the keyed contract itself — one result
// per requested key, in the requested order, each carrying an explicit
// per-key ok and exactly one of data or error for issue operations. Metadata
// resource failures retain their data alongside error so partial reads and
// acknowledged storage survive. Resource success frames need a successful row.
//
// Ordering is the tooth that matters, and it is the one a concurrent
// fan-out can silently break: a command that appended results as reads
// COMPLETED rather than filling them by index would pass every other
// check in this file and hand back TST-3's links under TST-2 whenever
// the second read happened to answer first. Nothing about that is
// visible in a single-key capture, which is why the success recipe
// passes two.
func checkSuccessKeyedResults(cap Capture, m command.Meta) (bool, string) {
	want, ok := keyedResultKeys[m.Op]
	if !ok {
		return false, fmt.Sprintf("no keyed-result expectation written for op %q — add it beside its recipes", m.Op)
	}
	data, err := successData(cap)
	if err != nil {
		return false, err.Error()
	}
	results, ok := data["results"].([]any)
	if !ok {
		return false, "data.results is not an array — a keyed command returns one entry per key"
	}
	if len(results) != len(want) {
		return false, fmt.Sprintf("data.results has %d entries, want %d — one per requested key", len(results), len(want))
	}
	anySuccess := false
	for i, elem := range results {
		entry, ok := elem.(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d] is not an object", i)
		}
		if m.Op == "cache.refresh" {
			if name, _ := entry["resource"].(string); name != want[i] {
				return false, fmt.Sprintf("results[%d].resource = %q, want %q in request order", i, name, want[i])
			}
			if _, exists := entry["issue"]; exists {
				return false, "metadata resource fabricated an issue reference"
			}
			child, ok := entry["data"].(map[string]any)
			if !ok {
				return false, fmt.Sprintf("results[%d].data must retain the resource plan and observations", i)
			}
			if name, _ := child["resource"].(string); name != want[i] {
				return false, fmt.Sprintf("results[%d].data.resource = %q, want %q", i, name, want[i])
			}
		} else {
			issue, _ := entry["issue"].(map[string]any)
			if key, _ := issue["key"].(string); key != want[i] {
				return false, fmt.Sprintf("results[%d].issue.key = %q, want %q — results must be ordered by the keys as given, not by which read finished first", i, key, want[i])
			}
		}
		okFlag, present := entry["ok"]
		if !present {
			return false, fmt.Sprintf("results[%d] omits ok — a caller must branch on the value, never on which of data/error is present", i)
		}
		succeeded, isBool := okFlag.(bool)
		if !isBool {
			return false, fmt.Sprintf("results[%d].ok = %v (%T), want a boolean", i, okFlag, okFlag)
		}
		anySuccess = anySuccess || succeeded
		_, hasData := entry["data"]
		failure, hasError := entry["error"].(map[string]any)
		switch {
		case succeeded && !hasData:
			return false, fmt.Sprintf("results[%d] is ok but carries no data", i)
		case succeeded && hasError:
			return false, fmt.Sprintf("results[%d] is ok yet carries an error", i)
		case !succeeded && hasData && m.Op != "cache.refresh":
			return false, fmt.Sprintf("results[%d] failed yet carries data", i)
		case !succeeded && !hasError:
			return false, fmt.Sprintf("results[%d] failed but says nothing about why", i)
		case !succeeded:
			for _, key := range []string{"code", "message", "hint"} {
				if s, _ := failure[key].(string); s == "" {
					return false, fmt.Sprintf("results[%d].error.%s missing or empty — a per-key failure owes the same three things the envelope's own errors do", i, key)
				}
			}
		}
	}
	if m.Op == "cache.refresh" && !anySuccess {
		return false, "metadata success frame has no successful resource"
	}
	return true, ""
}

// passthroughFidelity is the hand-written proof, per op declaring
// Passthrough, that one field this package does NOT model survives the
// wire boundary verbatim: the JSON path into the success data and the
// exact value the stub serves there. Written independently of the
// stub's literal payload — this pins the CONTRACT ("an unmodeled field
// round-trips"), not an incidental byte match against whatever the stub
// happens to contain today.
var passthroughFidelity = map[string]struct {
	path  []string
	value string
}{
	"issue.view": {path: []string{"issue", "fields", "customfield_10011"}, value: "corpus custom value"},
	// A numeric path segment ("0") indexes into an array — comments is a
	// slice, so reaching inside its first element needs that, unlike
	// issue.view's path, which never leaves nested objects. See
	// checkSuccessPassthroughFidelity's walk below.
	"issue.comment.list": {
		path:  []string{"comments", "0", "body", "customUnmodeledField"},
		value: "comment custom value",
	},
	"issue.list": {
		path:  []string{"issues", "0", "fields", "customfield_10011"},
		value: "list custom value",
	},
	"issue.mine": {
		path:  []string{"issues", "0", "fields", "customfield_10011"},
		value: "list custom value",
	},
	// Deepest path in this table, and every segment is load-bearing: the
	// created comment's ADF body is nested inside a KEYED result, so the
	// proof has to reach through results[0].data before it gets to the
	// raw document. See createdCommentJSON for why this field rides the
	// response rather than the posted body.
	"issue.comment.add": {
		path:  []string{"results", "0", "data", "comment", "body", "customUnmodeledField"},
		value: "comment add custom value",
	},
	// The only entries here proving an OUTBOUND passthrough: a custom
	// field the caller put in --json-input reaches the composed request
	// untouched. That is the property the whole "no --field flag" decision
	// rests on — custom fields are reachable, losslessly, through the JSON
	// route — so it is proved by capture rather than asserted in help
	// text.
	"issue.create": {
		path:  []string{"payload", "fields", "customfield_10011"},
		value: "create custom value",
	},
	"issue.clone": {
		path:  []string{"results", "0", "data", "receipt", "future", "opaque"},
		value: "native receipt sentinel",
	},
	"issue.edit": {
		path:  []string{"payload", "fields", "customfield_10011"},
		value: "edit custom value",
	},
	// The SHALLOWEST comment path in this table, and the difference from
	// the entry above it is the whole point: comment.edit is not keyed,
	// so the stored comment sits straight at data.comment with no
	// results[0].data to reach through. Same boundary, same unmodeled
	// field, one nesting level fewer — which is the placement rule
	// (envelope.KeyedResults) demonstrated on the fidelity axis.
	//
	// The value reads "comment add custom value" because the edit stub
	// answers through createdCommentJSON, the same builder the add stub
	// uses. Reusing it is deliberate — one response fixture for one
	// response shape — and the string is left as it is rather than
	// parameterized, since renaming it would be churn in the add op's
	// own expectation for no reader's benefit.
	"issue.comment.edit": {
		path:  []string{"comment", "body", "customUnmodeledField"},
		value: "comment add custom value",
	},
}

// checkSuccessPassthroughFidelity is M10: the registered boundary
// carries the tenant's unmodeled field through unchanged. The walk
// supports both object-key and array-index segments (a numeric segment
// indexes into a []any) because issue.comment.list's fidelity proof
// lives inside comments[0].body — a raw ADF blob nested under an array,
// unlike issue.view's path, which stays entirely inside nested objects.
func checkSuccessPassthroughFidelity(cap Capture, m command.Meta) (bool, string) {
	expect, ok := passthroughFidelity[m.Op]
	if !ok {
		return false, fmt.Sprintf("no passthrough fidelity expectation written for op %q — add it beside its recipes", m.Op)
	}
	data, err := successData(cap)
	if err != nil {
		return false, err.Error()
	}
	var cur any = data
	for _, key := range expect.path {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[key]
			if !ok {
				return false, fmt.Sprintf("path %s: %q missing", strings.Join(expect.path, "."), key)
			}
			cur = v
		case []any:
			idx, err := strconv.Atoi(key)
			if err != nil || idx < 0 || idx >= len(node) {
				return false, fmt.Sprintf("path %s: %q is not a valid index into an array of length %d", strings.Join(expect.path, "."), key, len(node))
			}
			cur = node[idx]
		default:
			return false, fmt.Sprintf("path %s: not an object or array before %q", strings.Join(expect.path, "."), key)
		}
	}
	got, ok := cur.(string)
	if !ok || got != expect.value {
		return false, fmt.Sprintf("passthrough field %s = %v, want %q verbatim", strings.Join(expect.path, "."), cur, expect.value)
	}
	return true, ""
}

// dryRunPreview is the hand-written statement of what each mutation's
// preview must actually SHOW. A dry-run whose payload says only "I would
// have done something" is worse than none: it reads as verified and
// verifies nothing, so the check is against content, not a field name.
var dryRunPreview = map[string]func(data map[string]any) (bool, string){
	"config.set": func(data map[string]any) (bool, string) {
		if data["key"] != "read_only" || data["value"] != "false" || data["previous_value"] != "true" || data["previous_present"] != true || data["previous_secret"] != false || data["would_change"] != true || data["changed"] != false || data["created"] != false {
			return false, "setting preview lost original declaration, candidate or actual publication state"
		}
		if path, ok := data["path"].(string); !ok || path == "" {
			return false, "setting preview names no selected file"
		}
		return true, ""
	},
	// The preview answers both questions a caller has before opening an
	// editor: which file, and which editor. Either one missing makes the
	// preview unactionable.
	// A login preview's whole value is the verification it performed, so
	// the account it resolved to is what must be present — "I would have
	// stored something" proves nothing.
	"auth.login": func(data map[string]any) (bool, string) {
		if id, _ := data["account_id"].(string); id == "" {
			return false, "preview names no account, so it did not verify the token"
		}
		if site, _ := data["base_url"].(string); site == "" {
			return false, "preview names no site"
		}
		return true, ""
	},
	// A logout preview says which account would be affected.
	"auth.logout": func(data map[string]any) (bool, string) {
		if email, _ := data["email"].(string); email == "" {
			return false, "preview names no account to log out of"
		}
		return true, ""
	},
	"config.edit": func(data map[string]any) (bool, string) {
		path, _ := data["path"].(string)
		if path == "" {
			return false, "preview names no file to edit"
		}
		editor, ok := data["editor"].([]any)
		if !ok || len(editor) == 0 {
			return false, "preview names no editor, so it cannot say what would open"
		}
		if name, _ := editor[0].(string); name == "" {
			return false, "preview carries an empty editor command"
		}
		return true, ""
	},
	// The field-taking writes' previews. Both publish the composed
	// payload, which is the bar issue.comment.add set below: a preview has
	// to publish the REQUEST, not announce that one would be made.
	//
	// What each must NOT carry is the sharper half. A create preview has
	// no key — the site assigns it — so `issue` absent is the contract,
	// and a preview inventing one would be the single most misleading
	// thing this envelope could do. `verified` is absent for the same
	// reason one step on: --verify was asked for in the recipe, and a
	// preview that reported a readback would be reporting a request it
	// never made.
	"issue.create": func(data map[string]any) (bool, string) {
		if ok, msg := previewCarriesPayload(data); !ok {
			return false, msg
		}
		if data["attempted"] != false || data["applied"] != false {
			return false, "preview must publish attempted=false and applied=false — it never invokes creation"
		}
		if _, present := data["receipt"]; present {
			return false, "preview carries a native creation receipt — no request was made"
		}
		if _, named := data["issue"]; named {
			return false, "preview carries an issue identity — no issue exists, so a preview cannot name one"
		}
		// The @me ruling, proved end to end rather than only in a unit
		// test: a bare preview never reads the site, so it cannot know the
		// caller's account id and must leave the sentinel where it is. A
		// resolved id here would be a value the preview invented.
		fields, _ := data["payload"].(map[string]any)["fields"].(map[string]any)
		assignee, _ := fields["assignee"].(map[string]any)
		if got := assignee["accountId"]; got != "@me" {
			return false, fmt.Sprintf("preview resolved --assignee to %v; --dry-run never contacts Jira, so it must leave @me in place", got)
		}
		if _, read := data["verified"]; read {
			return false, "preview carries a readback — --verify reads the site, which a preview never contacts"
		}
		if failed, _ := data["verify_failed"].(bool); failed {
			return false, "preview reports a failed readback — it never attempted one"
		}
		return true, ""
	},
	"issue.edit": func(data map[string]any) (bool, string) {
		if ok, msg := previewCarriesPayload(data); !ok {
			return false, msg
		}
		results, ok := data["results"].([]any)
		if !ok || len(results) == 0 {
			return false, "preview carries no per-key results; a caller cannot see which issues would be edited"
		}
		for i, raw := range results {
			entry, ok := raw.(map[string]any)
			if !ok {
				return false, fmt.Sprintf("results[%d] is not an object", i)
			}
			child, ok := entry["data"].(map[string]any)
			if !ok {
				return false, fmt.Sprintf("results[%d].data is missing; a preview key succeeded, so it carries data", i)
			}
			if _, read := child["verified"]; read {
				return false, fmt.Sprintf("results[%d].data.verified is present on a PREVIEW — the readback contacts the site, which a preview never does", i)
			}
		}
		return true, ""
	},
	// The first Jira mutation's preview, and the bar it sets for the rest:
	// a preview has to publish the REQUEST, not merely announce that a
	// request would be made. A caller runs --dry-run to review what would
	// be sent, and "a comment would be added to two issues" does not let
	// them review anything.
	//
	// The created comment must be ABSENT: it does not exist, and a
	// preview inventing one would be the single most misleading thing
	// this envelope could do.
	"issue.comment.add": func(data map[string]any) (bool, string) {
		body, _ := data["body"].(map[string]any)
		if len(body) == 0 {
			return false, "preview carries no composed body, so a caller cannot see what would be posted"
		}
		if kind, _ := body["type"].(string); kind != "doc" {
			return false, fmt.Sprintf("preview body is not an ADF document (type=%v)", body["type"])
		}
		if route, _ := data["body_route"].(string); route == "" {
			return false, "preview does not say which input route produced the body"
		}
		results, ok := data["results"].([]any)
		if !ok || len(results) == 0 {
			return false, "preview carries no per-key results; a caller cannot see which issues would be commented on"
		}
		for i, raw := range results {
			entry, ok := raw.(map[string]any)
			if !ok {
				return false, fmt.Sprintf("results[%d] is not an object", i)
			}
			child, ok := entry["data"].(map[string]any)
			if !ok {
				return false, fmt.Sprintf("results[%d].data is missing; a preview key succeeded, so it carries data", i)
			}
			if _, invented := child["comment"]; invented {
				return false, fmt.Sprintf("results[%d].data.comment is present on a PREVIEW — nothing was created, so nothing may be reported as created", i)
			}
		}
		return true, ""
	},
	// The first preview that has to report what it CANNOT know.
	//
	// This op's request shape depends on a live read (which transition,
	// and whether it has a screen), and a bare --dry-run never makes
	// one. So the preview must publish the composed comment — same bar
	// as comment.add above — while publishing NO route for it and NO
	// resolved transition, because it has neither. The recipe is bare
	// --dry-run precisely so this is the branch under test; the remote
	// preview that DOES resolve them is pinned in the unit tests.
	//
	// comment_posted is the field that would be catastrophic here and it
	// is checked by name: it is past tense, so its presence on a preview
	// would claim a comment reached Jira during a run that opened no
	// connection.
	"issue.transition": func(data map[string]any) (bool, string) {
		comment, _ := data["comment"].(map[string]any)
		if len(comment) == 0 {
			return false, "preview carries no composed comment, so a caller cannot see what would be attached"
		}
		if kind, _ := comment["type"].(string); kind != "doc" {
			return false, fmt.Sprintf("preview comment is not an ADF document (type=%v)", comment["type"])
		}
		if route, _ := data["comment_route"].(string); route == "" {
			return false, "preview does not say which input route produced the comment"
		}
		if target, _ := data["transition"].(string); target == "" {
			return false, "preview does not say what it would transition to"
		}
		results, ok := data["results"].([]any)
		if !ok || len(results) == 0 {
			return false, "preview carries no per-key results; a caller cannot see which issues would move"
		}
		for i, raw := range results {
			entry, ok := raw.(map[string]any)
			if !ok {
				return false, fmt.Sprintf("results[%d] is not an object", i)
			}
			child, ok := entry["data"].(map[string]any)
			if !ok {
				return false, fmt.Sprintf("results[%d].data is missing; a preview key succeeded, so it carries data", i)
			}
			if _, claimed := child["comment_posted"]; claimed {
				return false, fmt.Sprintf("results[%d].data.comment_posted is present on a PREVIEW — nothing was posted, so no past-tense field may report one", i)
			}
			if _, foreseen := child["comment_plan"]; foreseen {
				return false, fmt.Sprintf("results[%d].data.comment_plan is present on a BARE preview — the route needs a live read, so this run cannot know it; only --validate-remote may fill it in", i)
			}
			if _, resolved := child["transition"]; resolved {
				return false, fmt.Sprintf("results[%d].data.transition is present on a BARE preview — resolving it needs the workflow, which this run never read", i)
			}
		}
		return true, ""
	},
	// The first UNKEYED mutation's preview. The bar comment.add set holds
	// unchanged — publish the REQUEST, not an announcement that a request
	// would be made — and the only difference is where the answer sits:
	// straight at data, with no results[] to walk.
	//
	// The stored comment must be ABSENT for comment.add's reason: it does
	// not exist yet, and a preview reporting one would claim a PUT that
	// never happened. comment_id must be PRESENT for the opposite reason:
	// it is request context, and a preview that named the body but not
	// which comment would take it has told the caller almost nothing
	// about the change they are reviewing.
	"issue.comment.edit": func(data map[string]any) (bool, string) {
		if id, _ := data["comment_id"].(string); id == "" {
			return false, "preview names no comment, so a caller cannot see which one would change"
		}
		body, _ := data["body"].(map[string]any)
		if len(body) == 0 {
			return false, "preview carries no composed body, so a caller cannot see what would be stored"
		}
		if kind, _ := body["type"].(string); kind != "doc" {
			return false, fmt.Sprintf("preview body is not an ADF document (type=%v)", body["type"])
		}
		if route, _ := data["body_route"].(string); route == "" {
			return false, "preview does not say which input route produced the body"
		}
		if _, invented := data["comment"]; invented {
			return false, "data.comment is present on a PREVIEW — nothing was stored, so nothing may be reported as stored"
		}
		return true, ""
	},
	// The SMALLEST preview in this table, and the entry that shows the
	// M7 bar is about completeness rather than volume: this op's whole
	// request is "which comment, on which issue", so a preview carrying
	// both is carrying the entire thing a caller could review. There is
	// no body, no options, and nothing else to publish.
	//
	// What it must NOT carry is checked too: `deleted` in any form. The
	// envelope deliberately has no such field (see
	// envelope.IssueCommentDeleteOutput), because dry_run already
	// answers it — and a preview reporting a deletion would be the worst
	// thing this particular op could say.
	"issue.comment.delete": func(data map[string]any) (bool, string) {
		issue, _ := data["issue"].(map[string]any)
		if key, _ := issue["key"].(string); key == "" {
			return false, "preview names no issue, so a caller cannot see what would be touched"
		}
		if id, _ := data["comment_id"].(string); id == "" {
			return false, "preview names no comment, so a caller cannot see which one would go"
		}
		if _, claimed := data["deleted"]; claimed {
			return false, "data.deleted is present on a PREVIEW — nothing was deleted, and dry_run already answers the question this field would"
		}
		return true, ""
	},
	// The watcher previews, and one shared function would have served
	// both — they are written twice on purpose, matching the two Output
	// types they check. An expectation table that collapsed the pair
	// could not notice the day one verb's preview stopped matching the
	// other's.
	//
	// account_id is the field that carries the bar here: the composed
	// request for these ops is one account applied to N keys, so a
	// preview omitting it has told the caller which issues would change
	// and not what would change about them.
	//
	// readback must be ABSENT from every entry. It is the resulting
	// watcher set, which only a live GET can know, and a preview
	// contacted nobody.
	"issue.watchers.add":    watcherWritePreview,
	"issue.watchers.remove": watcherWritePreview,
	// THE PREVIEW THAT HAS TO REPORT WHAT IT CANNOT KNOW, and the second
	// op in this table to do so after issue.transition.
	//
	// A link type lives on the site and a bare --dry-run never reads one,
	// so this preview must publish the type the caller TYPED, must NOT
	// publish a resolved type or relation, and must say which of those
	// two states it is in. type_resolved is checked by name because it is
	// the field that distinguishes them: a preview reporting false while
	// carrying a relation would be claiming a resolution it never made.
	"issue.link.create": func(data map[string]any) (bool, string) {
		to, _ := data["to"].(map[string]any)
		if key, _ := to["key"].(string); key == "" {
			return false, "preview names no target issue, so a caller cannot see what would be linked to"
		}
		if input, _ := data["type_input"].(string); input == "" {
			return false, "preview does not echo the type that was typed, which is all a bare preview knows"
		}
		resolved, present := data["type_resolved"].(bool)
		if !present {
			return false, "preview omits type_resolved, so an absent type could mean either 'not checked' or 'not found'"
		}
		if resolved {
			return false, "preview reports type_resolved=true on a BARE --dry-run — resolving needs a live read, which this run never made"
		}
		if _, invented := data["type"]; invented {
			return false, "data.type is present on a bare preview, which resolved nothing"
		}
		if _, invented := data["relation"]; invented {
			return false, "data.relation is present on a bare preview — the direction comes from the site's type list, which this run never read"
		}
		results, ok := data["results"].([]any)
		if !ok || len(results) == 0 {
			return false, "preview carries no per-key results; a caller cannot see which issues would be linked"
		}
		return true, ""
	},
	// The SMALLEST preview here alongside comment.delete's, and for the
	// same reason: the whole request is one id, so a preview carrying it
	// carries everything there is to review.
	"issue.link.delete": func(data map[string]any) (bool, string) {
		if id, _ := data["link_id"].(string); id == "" {
			return false, "preview names no link, so a caller cannot see what would go"
		}
		if _, named := data["issue"]; named {
			return false, "data.issue is present — a link id is site-wide, so this operation resolves no issue and must not imply one"
		}
		return true, ""
	},
	// The composed link, plus the ABSENCE that matters most on this op:
	// created must not appear. It reports whether Jira added or replaced,
	// which only a live status code can say — and false would be the
	// specific claim that an existing link was overwritten.
	"issue.weblink.add": func(data map[string]any) (bool, string) {
		if u, _ := data["url"].(string); u == "" {
			return false, "preview carries no url, so a caller cannot see what would be attached"
		}
		if title, _ := data["title"].(string); title == "" {
			return false, "preview carries no title"
		}
		results, ok := data["results"].([]any)
		if !ok || len(results) == 0 {
			return false, "preview carries no per-key results"
		}
		for i, raw := range results {
			entry, ok := raw.(map[string]any)
			if !ok {
				return false, fmt.Sprintf("results[%d] is not an object", i)
			}
			child, ok := entry["data"].(map[string]any)
			if !ok {
				return false, fmt.Sprintf("results[%d].data is missing; a preview key succeeded, so it carries data", i)
			}
			if _, claimed := child["created"]; claimed {
				return false, fmt.Sprintf("results[%d].data.created is present on a PREVIEW — whether Jira would add or replace is knowable only from a live status code", i)
			}
			if _, invented := child["weblink"]; invented {
				return false, fmt.Sprintf("results[%d].data.weblink is present on a PREVIEW — Jira issued no identifier", i)
			}
		}
		return true, ""
	},
	"issue.weblink.delete": func(data map[string]any) (bool, string) {
		issue, _ := data["issue"].(map[string]any)
		if key, _ := issue["key"].(string); key == "" {
			return false, "preview names no issue, so a caller cannot see what would be touched"
		}
		if id, _ := data["weblink_id"].(string); id == "" {
			return false, "preview names no web link, so a caller cannot see which one would go"
		}
		return true, ""
	},
	// Upload previews inspect metadata but never invent created resources.
	// checkDryRunPreview separately rejects any local writes.
	"issue.attachment.add": func(data map[string]any) (bool, string) {
		files, ok := data["files"].([]any)
		if !ok || len(files) == 0 {
			return false, "preview names no files, so a caller cannot see what would be uploaded"
		}
		details, ok := data["file_details"].([]any)
		if !ok || len(details) != len(files) {
			return false, "preview loses inspected metadata for an input"
		}
		for i, raw := range details {
			file, ok := raw.(map[string]any)
			if !ok || file["path"] != files[i] || file["size"] != float64(len(corpusAttachmentBody)) || file["mime_inferred"] != "text/plain" {
				return false, "preview metadata differs from its authored fixture"
			}
		}
		results, ok := data["results"].([]any)
		if !ok || len(results) == 0 {
			return false, "preview carries no per-key results"
		}
		for i, raw := range results {
			entry, _ := raw.(map[string]any)
			child, ok := entry["data"].(map[string]any)
			if !ok {
				return false, fmt.Sprintf("results[%d].data is missing; a preview key succeeded, so it carries data", i)
			}
			if _, invented := child["attachments"]; invented {
				return false, fmt.Sprintf("results[%d].data.attachments is present on a PREVIEW — nothing was uploaded, so nothing may be reported as created", i)
			}
		}
		return true, ""
	},
	// THE ONLY PREVIEW IN THIS TABLE FOR A COMMAND THAT WRITES A FILE, so
	// it carries the strictest set of absences here. The directory is
	// local and must be present; the filename comes from the site and
	// must NOT be, on a bare preview; and bytes_written must be absent
	// because a preview that reported bytes would be claiming a write.
	"issue.attachment.download": func(data map[string]any) (bool, string) {
		if id, _ := data["attachment_id"].(string); id == "" {
			return false, "preview names no attachment"
		}
		// Presence only. That the directory is ABSOLUTE is a real part of
		// the contract and is pinned in the unit tests
		// (TestResolveDirIsAbsolute) rather than here, because the
		// harness canonicalizes machine-varying paths to <CANON> for
		// determinism (M12) — so this capture cannot see the property,
		// and a check for it here would be asserting against the
		// harness's redaction rather than against the CLI.
		if dir, _ := data["directory"].(string); dir == "" {
			return false, "preview names no directory, so a caller cannot see where the file would land"
		}
		resolved, present := data["filename_resolved"].(bool)
		if !present {
			return false, "preview omits filename_resolved, so an absent filename could mean either 'not looked up' or 'unnamed'"
		}
		if resolved {
			return false, "preview reports filename_resolved=true on a BARE --dry-run — the name comes from the site, which this run never read"
		}
		for _, invented := range []string{"filename", "path", "bytes_written", "overwrote"} {
			if _, present := data[invented]; present {
				return false, fmt.Sprintf("data.%s is present on a bare preview, which resolved nothing and wrote nothing", invented)
			}
		}
		return true, ""
	},
	"issue.attachment.delete": func(data map[string]any) (bool, string) {
		if id, _ := data["attachment_id"].(string); id == "" {
			return false, "preview names no attachment, so a caller cannot see what would go"
		}
		if _, named := data["issue"]; named {
			return false, "data.issue is present — an attachment id is site-wide, so this operation resolves no issue and must not imply one"
		}
		return true, ""
	},
	// The preview for the most destructive command in the tree, and what
	// it must carry is exactly what a caller needs to check BEFORE
	// agreeing: which issues, and whether subtasks are coming too.
	"issue.delete": func(data map[string]any) (bool, string) {
		if _, present := data["delete_subtasks"].(bool); !present {
			return false, "preview omits delete_subtasks — whether subtasks are taken changes how much disappears, and a preview that hides it cannot be reviewed"
		}
		results, ok := data["results"].([]any)
		if !ok || len(results) == 0 {
			return false, "preview carries no per-key results; a caller cannot see what would be deleted"
		}
		for i, raw := range results {
			entry, _ := raw.(map[string]any)
			issue, _ := entry["issue"].(map[string]any)
			if key, _ := issue["key"].(string); key == "" {
				return false, fmt.Sprintf("results[%d] names no issue", i)
			}
		}
		return true, ""
	},
	// The thinnest preview in this table, and honestly so: the fields a
	// clone would carry live on the SOURCE, and reading them is what a
	// live run does first. A preview that described the copy would have
	// to make N reads, which is the network a --dry-run must not touch.
	//
	// What it must NOT carry is the clone itself: no issue was created,
	// so no key may be reported.
	"issue.clone": func(data map[string]any) (bool, string) {
		results, ok := data["results"].([]any)
		if !ok || len(results) == 0 {
			return false, "preview carries no per-key results; a caller cannot see which issues would be cloned"
		}
		for i, raw := range results {
			entry, _ := raw.(map[string]any)
			child, ok := entry["data"].(map[string]any)
			if !ok {
				return false, fmt.Sprintf("results[%d].data is missing; a preview key succeeded, so it carries data", i)
			}
			if _, invented := child["clone"]; invented {
				return false, fmt.Sprintf("results[%d].data.clone is present on a PREVIEW — nothing was created, so no new key exists to report", i)
			}
			if child["applied"] != false {
				return false, fmt.Sprintf("results[%d].data must publish applied=false on a preview", i)
			}
			if _, present := child["receipt"]; present {
				return false, fmt.Sprintf("results[%d].data carries a native creation receipt on a preview", i)
			}
		}
		return true, ""
	},
	// The whole request is a direction and an anchor, so a preview
	// carrying both carries everything there is to review.
	"issue.rank": func(data map[string]any) (bool, string) {
		anchor, _ := data["anchor"].(string)
		if anchor == "" {
			return false, "preview names no anchor, so a caller cannot see what the issues would move relative to"
		}
		switch position, _ := data["position"].(string); position {
		case "before", "after":
		default:
			return false, fmt.Sprintf("position = %q, want before or after — the direction is half the request", position)
		}
		if results, ok := data["results"].([]any); !ok || len(results) == 0 {
			return false, "preview carries no per-key results; the order they appear in is the order they would land in"
		}
		return true, ""
	},
}

// watcherWritePreview is the M7 expectation both watcher verbs use. One
// function, two table entries: the two ops publish the same preview
// shape, and duplicating the CHECK would only create a second place for
// it to rot. The table above still names each op, so a verb that
// diverged would be re-pointed there deliberately rather than silently
// covered.
func watcherWritePreview(data map[string]any) (bool, string) {
	if id, _ := data["account_id"].(string); id == "" {
		return false, "preview names no account, so a caller cannot see whose watch would change"
	}
	results, ok := data["results"].([]any)
	if !ok || len(results) == 0 {
		return false, "preview carries no per-key results; a caller cannot see which issues would change"
	}
	for i, raw := range results {
		entry, ok := raw.(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d] is not an object", i)
		}
		child, ok := entry["data"].(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d].data is missing; a preview key succeeded, so it carries data", i)
		}
		if _, invented := child["readback"]; invented {
			return false, fmt.Sprintf("results[%d].data.readback is present on a PREVIEW — reading the watcher set needs a live GET, which this run never made", i)
		}
	}
	return true, ""
}

// previewCarriesPayload is the half issue.create and issue.edit share:
// the composed request is published and it is a real `fields`/`update`
// document, not an empty object a caller could not review.
//
// Written once because the two commands genuinely compose the SAME
// payload through the same code — unlike the halves above it, which
// differ per command and are spelled out per entry.
func previewCarriesPayload(data map[string]any) (bool, string) {
	payload, ok := data["payload"].(map[string]any)
	if !ok || len(payload) == 0 {
		return false, "preview carries no composed payload, so a caller cannot see what would be sent"
	}
	_, hasFields := payload["fields"]
	_, hasUpdate := payload["update"]
	if !hasFields && !hasUpdate {
		return false, "preview payload has neither fields nor update — it would change nothing"
	}
	if _, said := data["description_route"]; !said {
		return false, "preview does not say which input route produced the description"
	}
	return true, ""
}

// checkDryRunFlagTrue is M2 on the preview path: dry_run is present AND
// true. Present-but-false would be the worst outcome the field exists to
// prevent — a caller reading it would conclude the write happened.
func checkDryRunFlagTrue(cap Capture, _ command.Meta) (bool, string) {
	data, err := successData(cap)
	if err != nil {
		return false, err.Error()
	}
	value, present := data["dry_run"]
	if !present {
		return false, `preview omits "dry_run" — a caller cannot tell it from a completed write`
	}
	if flag, ok := value.(bool); !ok || !flag {
		return false, fmt.Sprintf(`preview carries dry_run=%v, want true`, value)
	}
	return true, ""
}

// checkDryRunPreview is M7: the preview carries enough to act on, per the
// hand-written expectation above.
func checkDryRunPreview(cap Capture, m command.Meta) (bool, string) {
	// A preview that changed something on disk has already broken the
	// only promise it makes, whatever its payload says.
	if cap.WroteFiles {
		return false, "the dry-run changed a file under HOME — a preview must not write"
	}
	expect, ok := dryRunPreview[m.Op]
	if !ok {
		return false, fmt.Sprintf("no dry-run preview expectation written for op %q — add it beside its recipes", m.Op)
	}
	data, err := successData(cap)
	if err != nil {
		return false, err.Error()
	}
	return expect(data)
}

// envelopeFreeSuccess is the hand-written expectation for each declared
// envelope-free op: what its machine-mode success is allowed to put on
// stdout. An op missing here fails loud — declaring "no envelope" does
// not excuse a surface from having a written contract.
var envelopeFreeSuccess = map[string]func(cap Capture) (bool, string){
	// auth token emits the SECRET and nothing else: no frame, no
	// formatting, byte-identical whichever machine mode was asked for.
	// A JSON wrapper here would carry the token into logs and --jq
	// output, which is the whole reason the op is declared envelope-free.
	"auth.token": func(cap Capture) (bool, string) {
		body := strings.TrimRight(cap.Stdout, "\n")
		switch {
		case body == "":
			return false, "no token on stdout"
		case strings.HasPrefix(strings.TrimSpace(cap.Stdout), "{"):
			return false, "the token surface emitted a JSON frame — a secret must not be wrapped"
		case strings.ContainsAny(body, "\n"):
			return false, "more than one line on stdout; the output must be the token alone"
		}
		if sibling, ok := siblingModeCapture(cap); ok && sibling.Stdout != cap.Stdout {
			return false, "token bytes differ between machine modes — the output selector must not alter a secret"
		}
		return true, ""
	},
	// completion emits a shell script: non-empty, not a JSON frame, and
	// byte-identical whichever machine mode was requested — the output
	// selector must not touch an artifact surface.
	"completion": func(cap Capture) (bool, string) {
		switch {
		case cap.Stdout == "":
			return false, "completion script is empty"
		case strings.HasPrefix(strings.TrimSpace(cap.Stdout), "{"):
			return false, "completion script looks like a JSON frame — the artifact surface leaked the envelope"
		}
		if sibling, ok := siblingModeCapture(cap); ok && sibling.Stdout != cap.Stdout {
			return false, "script bytes differ between machine modes — the output selector must not alter the artifact"
		}
		return true, ""
	},
}

// successDataShape is the independently written field contract per
// envelope-bearing op: required fields always present, optional fields
// allowed, anything else a violation. This is the M8 "schema truth"
// check's source — hand-authored, never derived from SchemaOf.
var successDataShape = map[string]struct {
	required []string
	optional []string
}{
	"release.notes": {required: []string{"releases"}},
	"version": {
		required: []string{"version", "go_version", "platform"},
		optional: []string{"commit", "dirty", "build_date"},
	},
	// Written here by hand, independently of the Output struct, so this
	// states what the CONTRACT owes a caller rather than echoing whatever
	// the declaration happens to say. Every field is required: a settings
	// view that omits the path, or the file's existence, or the rows,
	// cannot answer the question it exists for.
	// Every field required: a status that omits the account it resolved
	// to, or the layer the token came from, cannot answer either of the
	// questions it exists for.
	"auth.status": {
		required: []string{"base_url", "email", "account_id", "display_name", "token_source"},
		optional: []string{"cloud_id"},
	},
	"auth.login": {
		required: []string{"dry_run", "verified", "base_url", "email", "account_id", "display_name", "credential", "settings"},
		optional: []string{"cloud_id", "replaced"},
	},
	"auth.whoami": {required: []string{"base_url", "user"}},
	"config.list": {
		required: []string{"path", "file_exists", "settings"},
		optional: []string{"profile", "default_profile"},
	},
	"config.get": {
		required: []string{"path", "file_exists", "key", "value", "present", "secret"},
		optional: []string{"profile"},
	},
	"config.set": {
		required: []string{"dry_run", "path", "key", "value", "previous_value", "previous_present", "previous_secret", "would_change", "changed", "created"},
		optional: []string{"profile"},
	},
	// The identity object carries everything: the curated members typed
	// under it, the tenant's own riding in its Extra passthrough — so
	// the flat contract is just the one slot, and checkIssueViewFields
	// reaches inside it.
	"issue.view": {
		required: []string{"issue"},
	},
	// Every field required: a comment page that omitted the pagination
	// block, or the comments themselves, could not answer either of the
	// questions this command exists for.
	"issue.comment.list": {
		// truncated is unconditional, IssueListOutput.Truncated's own
		// reasoning: a consumer that never checks the key must not be able
		// to mistake a capped --all drain for a complete thread just
		// because the key happened to be absent. truncated_reason is
		// optional because it is empty — and so omitted — whenever
		// truncated is false, which is every read that is not a capped
		// drain.
		required: []string{"issue", "comments", "pagination", "truncated"},
		optional: []string{"truncated_reason"},
	},
	// Every field required: a search result that omitted the issues
	// themselves, or the pagination cursor a caller needs to see the
	// next page, could not answer either of the questions this command
	// exists for. truncated is unconditional too — CursorPagination's
	// own is_last already sets that precedent (serialized even when
	// false) — so a caller can branch on it without first checking
	// whether the key exists; truncated_reason is optional, present
	// only when truncated is true.
	"issue.list": {
		required: []string{"issues", "pagination", "truncated"},
		// jql is --as-jql's own answer and appears on no other path; it
		// is omitempty for exactly that reason, so its PRESENCE is the
		// signal (see IssueListOutput.JQL).
		//
		// board appears only under --board/--board-id, and is declared
		// here ahead of any recipe that exercises them: an optional field
		// nobody has declared fails as "not in the published contract" the
		// moment a capture first carries it, which would land on whoever
		// adds that recipe rather than on whoever added the field.
		optional: []string{"truncated_reason", "jql", "board", "search_pages", "saved_query"},
	},
	"issue.mine": {
		required: []string{"issues", "pagination", "truncated"},
		optional: []string{"truncated_reason", "jql", "board", "search_pages", "saved_query"},
	},
	// A keyed command's whole flat contract is the one array: everything
	// else — the identity, the ok flag, the links, a failure — lives
	// inside an entry, and checkIssueLinkListFields reaches in for it.
	"issue.link.list": {
		required: []string{"results"},
	},
	// The same one-array contract, and notably NO pagination key: this
	// command publishes none, because Jira has no attachment-list
	// endpoint to paginate against (see
	// envelope.IssueAttachmentListOutput for the whole argument). The
	// exhaustive check below turns that into a real guard — a pagination
	// block added here later would fail as "not in the published
	// contract" rather than appearing unnoticed.
	"issue.attachment.list": {
		required: []string{"results"},
	},
	// The same one-array contract, and no pagination key here either —
	// this command's endpoint is real (unlike its two siblings') but
	// takes no paging parameters and returns the whole set, so there is
	// still no page to publish. See envelope.IssueWatchersListOutput.
	"issue.watchers.list": {
		required: []string{"results"},
	},
	// The first mutation's shape, and the one the writes after it copy.
	// dry_run, body and body_route are REQUIRED on both paths: the
	// composed request is a fact about the run, so a live write has to
	// answer "what did you send?" just as a preview answers "what would
	// you send?". visibility is optional because an unrestricted comment
	// genuinely has none — an empty object would be a restriction naming
	// no group, which is a different thing.
	//
	// The created comment is NOT here: it lives inside results[].data,
	// which this flat table cannot reach — checkIssueCommentAddFields
	// below is what proves it appears on the live path and is absent on
	// the preview.
	"issue.comment.add": {
		required: []string{"dry_run", "body", "body_route", "results"},
		optional: []string{"visibility"},
	},
	// The field-taking writes. dry_run, payload and description_route are
	// REQUIRED on both paths, for the reason the comment's composed body
	// is: the request is a fact about the run, so
	// a live write has to answer "what did you send?" just as a preview
	// answers "what would you send?". description_route is required even
	// when it is empty — the empty string is a real answer ("no
	// description was given") and a missing key would be indistinguishable
	// from a producer that forgot.
	//
	// issue is required HERE and nowhere else in this table, because M1
	// only ever asks the success capture and a create's success always has
	// a key. verified and verify_failed are optional: they depend on
	// --verify, and verify_failed is further suppressed when false.
	// The site's link types, with the provenance of the answer. types and
	// cached are REQUIRED — a caller branching on freshness must not have
	// to tell false from absent — while age_seconds is optional because a
	// fresh read has no age to report and a zero would read as "just
	// written" rather than "not cached".
	"issue.link.types": {
		required: []string{"types", "cached"},
		optional: []string{"age_seconds"},
	},
	"issue.create": {
		required: []string{"dry_run", "attempted", "applied", "payload", "description_route", "issue"},
		optional: []string{"receipt", "verified", "verify_failed"},
	},
	// The keyed sibling: the same request context, with the per-key half
	// inside results[] where this flat table cannot reach it. Nothing
	// optional at the top level — an edit's readback is per key, so
	// `verified` never appears here.
	// notify is REQUIRED rather than optional, for delete_subtasks'
	// reason: it is a boolean whose false is the interesting value, and a
	// caller auditing what a script did has to be able to see which
	// request was made. It also cannot be inferred from payload — it
	// rides the query string, so the published body never mentions it.
	"issue.edit": {
		required: []string{"dry_run", "notify", "payload", "description_route", "results"},
	},
	// atomic_only is REQUIRED while comment/comment_route are optional,
	// which looks inconsistent and is not. atomic_only is a boolean whose
	// false is meaningful — it is the difference between "the fallback
	// was available" and "the caller forbade it" — and a caller reading
	// `comment_posted: "separate"` needs it present to know the fallback
	// was allowed rather than merely untriggered. The comment fields are
	// genuinely absent when no comment was asked for, and an empty ADF
	// document would be a comment saying nothing.
	//
	// The per-key route fields are not here: this flat table cannot
	// reach inside results[], and checkIssueTransitionFields below is
	// what proves each key reports how its comment traveled.
	"issue.transition": {
		required: []string{"dry_run", "transition", "payload", "atomic_only", "results"},
		optional: []string{"comment", "comment_route", "resolution", "assignee"},
	},
	// The first UNKEYED mutation's shape, and the first in this table
	// with BOTH an identity slot and mutation fields at the same level —
	// every mutation before it is keyed, so its identity lived one level
	// down inside results[].
	//
	// visibility_cleared is REQUIRED where visibility is optional, and
	// the asymmetry is the point rather than an oversight: an absent
	// visibility means "leave the comment's restriction alone", so the
	// only thing separating that from "remove it" is this boolean. Make
	// it optional and the two requests publish identically.
	//
	// comment is optional because it exists only after Jira stores it —
	// the server-created-objects-stay-conditional rule — and
	// checkIssueCommentEditFields below is what proves it appears on the
	// live path and is absent on the preview.
	"issue.comment.edit": {
		required: []string{"issue", "dry_run", "notify", "comment_id", "body", "body_route", "visibility_cleared"},
		optional: []string{"visibility", "comment"},
	},
	// The smallest contract in this table, and every absence is
	// deliberate: no `deleted` (dry_run answers it), no deleted comment
	// (the 204 carries none). See envelope.IssueCommentDeleteOutput for
	// both arguments. The exhaustive check turns those absences into a
	// real guard — either field added later fails as "not in the
	// published contract" rather than appearing unnoticed.
	"issue.comment.delete": {
		required: []string{"issue", "dry_run", "comment_id"},
	},
	// The watcher writes, identical to each other and notably WITHOUT an
	// issue key at this level: they are keyed, so the identity is per
	// result. account_id is required on both paths for the reason
	// comment.add's body is — it is the composed request, and a live run
	// has to answer "what did you send?" just as a preview answers "what
	// would you send?".
	//
	// The readback is NOT here: it lives inside results[].data, which
	// this flat table cannot reach.
	"issue.watchers.add": {
		required: []string{"dry_run", "account_id", "results"},
	},
	"issue.watchers.remove": {
		required: []string{"dry_run", "account_id", "results"},
	},
	// type_resolved is REQUIRED where type and relation are optional, and
	// the asymmetry is the point rather than an oversight: it is what
	// makes an absent type unambiguous. Without it a caller cannot tell
	// "the site was never asked" (a bare preview) from "the site has no
	// such type" — and the second would be a failure, not a success.
	"issue.link.create": {
		required: []string{"dry_run", "to", "type_input", "type_resolved", "results"},
		optional: []string{"type", "relation", "sentences"},
	},
	// The only success payload in this table with no issue and no
	// results — a link id is site-wide, so there is nothing else to
	// carry. The exhaustive check turns that into a real guard: an
	// `issue` key added here later fails as "not in the published
	// contract" rather than appearing unnoticed.
	"issue.link.delete": {
		required: []string{"dry_run", "link_id"},
	},
	// The same one-array contract the keyed reads have, and no
	// pagination: this endpoint takes no paging pair and answers with the
	// whole set.
	"issue.weblink.list": {
		required: []string{"results"},
	},
	// url and title are required on both paths for comment.add's reason —
	// the composed request is a fact about the run, so a live write has
	// to answer "what did you send?" too. summary, relationship and
	// global_id are optional because a link genuinely need not have them.
	"issue.weblink.add": {
		required: []string{"dry_run", "url", "title", "payload", "results"},
		optional: []string{"summary", "relationship", "global_id"},
	},
	"issue.weblink.delete": {
		required: []string{"issue", "dry_run", "weblink_id"},
	},
	// files is required on both paths for comment.add's reason: the
	// composed request is a fact about the run, so a live upload has to
	// answer "what did you send?" too.
	"issue.attachment.add": {
		required: []string{"dry_run", "files", "file_details", "results"},
	},
	// The richest optional set in this table, and every entry is
	// conditional for a different reason. filename and path appear only
	// once the name is resolved — which a bare preview never does.
	// bytes_written and overwrote appear only on a live run, because a
	// preview writes nothing and so replaced nothing.
	//
	// filename_resolved is REQUIRED while filename is optional, and that
	// asymmetry is the point: it is what makes an absent filename mean
	// "not looked up" rather than "unnamed".
	"issue.attachment.download": {
		required: []string{"dry_run", "attachment_id", "directory", "filename_resolved"},
		optional: []string{"filename", "path", "bytes_written", "overwrote"},
	},
	// The smallest contract here alongside link.delete's, and with no
	// issue for the same reason.
	"issue.attachment.delete": {
		required: []string{"dry_run", "attachment_id"},
	},
	// delete_subtasks is REQUIRED, and it is the field in this contract
	// that most needs to be: Jira refuses an issue that has subtasks
	// without it, so false and true are not "one issue or several" but
	// "this may have been refused" against "this took everything below
	// it". A caller auditing a script has to see which request was made.
	"issue.delete": {
		required: []string{"dry_run", "delete_subtasks", "results"},
	},
	// Each source's creation acknowledgement, full native receipt and new
	// identity live inside its result rather than at this level.
	"issue.clone": {
		required: []string{"dry_run", "results"},
	},
	"issue.rank": {
		required: []string{"dry_run", "anchor", "position", "results"},
	},
}

// successTimestampFields names the data fields per op that, when
// present, must be RFC 3339 UTC (the M5 CLI-timestamp rule; wire
// passthrough timestamps are exempt by design and none exist yet).
var successTimestampFields = map[string][]string{
	"version": {"build_date"},
}

// frame is the audit's own minimal decode of the envelope — written
// here, independent of internal/envelope's types, so the check cannot
// inherit a defect from the declaration it verifies.
type frame struct {
	OK   *bool `json:"ok"`
	Meta struct {
		Command         string `json:"command"`
		ContractVersion string `json:"contract_version"`
		Timestamp       string `json:"timestamp"`
		RequestID       string `json:"request_id"`
		ExitCode        *int   `json:"exit_code"`
	} `json:"meta"`
	Data     json.RawMessage   `json:"data"`
	Errors   []json.RawMessage `json:"errors"`
	Warnings []json.RawMessage `json:"warnings"`
}

func decodeFrame(stdout string) (frame, error) {
	var f frame
	if err := json.Unmarshal([]byte(stdout), &f); err != nil {
		return frame{}, fmt.Errorf("stdout is not a JSON frame: %w", err)
	}
	return f, nil
}

// successData returns the op's data object from a machine-mode success
// capture: the frame's .data in json mode, the bare object in compact.
func successData(cap Capture) (map[string]any, error) {
	raw := []byte(cap.Stdout)
	if cap.Mode == ModeJSON {
		f, err := decodeFrame(cap.Stdout)
		if err != nil {
			return nil, err
		}
		raw = f.Data
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("data does not decode as an object: %w", err)
	}
	return data, nil
}

// siblingModeCapture finds the same scenario captured in the other
// machine mode — the cross-mode half of the determinism and artifact
// checks.
func siblingModeCapture(cap Capture) (Capture, bool) {
	captures, err := corpus()
	if err != nil {
		return Capture{}, false
	}
	return siblingModeCaptureFrom(captures, cap)
}

func siblingModeCaptureFrom(captures map[string]Capture, cap Capture) (Capture, bool) {
	other := ModeCompact
	if cap.Mode == ModeCompact {
		other = ModeJSON
	}
	sibling, ok := captures[cap.Scenario.ID+"|"+string(other)]
	return sibling, ok
}

// successWarningTypes is the CLOSED set of warning types each op may
// emit on a SUCCESSFUL run. An op absent from this map may emit none.
//
// This table replaced a flat "warnings is an empty array on success"
// assertion, and the replacement is worth arguing because it looks like
// a loosening. It is not — it is the same rule with a named exception
// list, which is strictly stronger than the alternative that was
// actually available.
//
// The flat assertion pinned an accident. The envelope has ALWAYS carried
// a warnings array (envelope.Warning, and the whole
// WriteEnvelopeWithWarnings path), and `--web`'s browser_open_failed
// warning has always been able to ride a successful envelope; no capture
// happened to produce one, so "always empty" held by coincidence and
// read as contract. `issue.transition` is the first op where a warning
// on success is REQUIRED rather than incidental: its whole reason for
// existing is that a comment may not have traveled the way the caller
// asked, and design §3.1 mandates saying so in warnings[] as well as in
// data.
//
// So the rule becomes: a warning on success must be DECLARED. An op
// cannot start emitting one without coming here, which is the same
// deliberate-extension gate the inventory pin applies to a new command.
// A warning of an undeclared type still fails the axis.
var successWarningTypes = map[string][]string{
	"issue.create": {"field_value_differs", "field_verification_incomplete", "verification_failed"},
	"issue.edit":   {"field_value_differs", "field_verification_incomplete", "verification_failed"},
	// The comment could not ride the transition (the workflow has no
	// screen), or could not be posted at all after one that had already
	// landed. Both are facts about a run that SUCCEEDED — the status
	// changed either way — which is exactly why they are warnings and
	// not errors.
	"issue.transition": {"comment_not_atomic", "comment_failed"},
}

// successWarningsDeclared enforces successWarningTypes against one
// capture's warnings.
func successWarningsDeclared(warnings []json.RawMessage, m command.Meta) (bool, string) {
	allowed := successWarningTypes[m.Op]
	for i, raw := range warnings {
		var w struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			return false, fmt.Sprintf("warnings[%d] does not decode: %v", i, err)
		}
		if !slices.Contains(allowed, w.Type) {
			return false, fmt.Sprintf("op %q emitted warning type %q on success, which it does not declare (declared: %v) — a warning on a successful run must be written into successWarningTypes deliberately", m.Op, w.Type, allowed)
		}
		if w.Message == "" {
			return false, fmt.Sprintf("warnings[%d] (type %q) carries no message — a warning nobody can read is worse than none", i, w.Type)
		}
	}
	return true, ""
}

// checkSuccessModeContract is M6 for machine-mode success captures:
// json is the full frame, compact is bare data, and the declared
// envelope-free surfaces hold to their own written expectations.
func checkSuccessModeContract(cap Capture, m command.Meta) (bool, string) {
	if cap.ExitCode != 0 {
		return false, fmt.Sprintf("exit = %d, want 0", cap.ExitCode)
	}
	if cap.Stderr != "" {
		return false, fmt.Sprintf("success wrote diagnostics to stderr: %q", cap.Stderr)
	}
	if m.NoSuccessEnvelope != "" {
		check, ok := envelopeFreeSuccess[m.Op]
		if !ok {
			return false, fmt.Sprintf("no envelope-free success expectation written for op %q — add it beside its recipes", m.Op)
		}
		return check(cap)
	}

	switch cap.Mode {
	case ModeJSON:
		f, err := decodeFrame(cap.Stdout)
		if err != nil {
			return false, err.Error()
		}
		switch {
		case f.OK == nil || !*f.OK:
			return false, "frame ok is not true"
		case f.Meta.Command != m.Op:
			return false, fmt.Sprintf("meta.command = %q, want %q", f.Meta.Command, m.Op)
		case f.Meta.ContractVersion != "1":
			return false, fmt.Sprintf("meta.contract_version = %q, want %q", f.Meta.ContractVersion, "1")
		case f.Meta.RequestID != "<CANON>":
			return false, "meta.request_id did not match the canonical correlation-id shape"
		case f.Meta.ExitCode != nil:
			return false, "meta.exit_code present on success"
		case f.Errors == nil || len(f.Errors) != 0:
			return false, "errors is not a present empty array"
		case f.Warnings == nil:
			return false, "warnings is not a present array"
		case len(f.Data) == 0 || string(f.Data) == "null":
			return false, "data is missing or null on success"
		}
		return successWarningsDeclared(f.Warnings, m)
	case ModeCompact:
		data, err := successData(cap)
		if err != nil {
			return false, err.Error()
		}
		for _, key := range []string{"ok", "meta", "errors", "warnings"} {
			if _, leaked := data[key]; leaked {
				return false, fmt.Sprintf("compact success carries frame key %q — compact is bare data", key)
			}
		}
		return true, ""
	}
	return false, fmt.Sprintf("no success expectation for mode %q", cap.Mode)
}

var snakeCaseRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// passthroughBoundary names, per op declaring Passthrough, the exact
// dotted path rooted at data (e.g. "issue.fields") where M4's
// snake_case rule stops applying — wire idioms (camelCase, a tenant's
// own custom-field names) live there by design. The boundary is the
// one the metadata actually claims, not "the whole payload," so a
// naming violation OUTSIDE it still fails loud.
var passthroughBoundary = map[string][]string{
	"issue.view": {"issue.fields", "issue.details"},
	// comments is an array, so every element shares this one boundary —
	// firstNonSnakeKey does not index array elements individually, it
	// recurses each one under the SAME dotted path (see its own doc
	// comment), which is what lets one declaration cover every comment
	// in the page rather than one per index.
	"issue.comment.list": {"comments.body"},
	// issues is an array, exactly like comments.body above — every
	// element shares this one boundary, which is what lets one
	// declaration cover every issue in the page rather than one per
	// index (firstNonSnakeKey's own doc comment on array recursion).
	"issue.list": {"issues.fields", "issues.details", "search_pages"},
	"issue.mine": {"issues.fields", "issues.details", "search_pages"},
	// Both raw-ADF slots ride one declaration: results is an array, so
	// this covers every key's created comment. data.body — the composed
	// document at the top level — needs no exemption, because ADF's own
	// keys (type, version, content, text, attrs, marks) are all
	// single lowercase words and pass the snake_case rule unaided.
	"issue.comment.add": {"results.data.comment.body"},
	// The field-taking writes' boundary is the COMPOSED REQUEST, which is
	// a different kind of passthrough from every entry above it: those
	// carry a wire document Jira SENT, and this one carries a document the
	// CLI is about to send. The exemption is needed for the same reason
	// either way — the keys are Jira's own (fixVersions, issuetype,
	// duedate, a tenant's customfield_NNNNN), not names this CLI minted.
	//
	// Worth recording how this surfaced: the first edit recipe used only
	// `summary` and `labels`, whose lowercase single-word keys pass the
	// snake_case rule unaided, so the missing declaration stayed invisible
	// until a --remove-fix-version was added to the recipe. The boundary
	// was always required; the capture simply had not exercised a
	// camelCase field yet.
	"issue.create": {"payload", "receipt", "verified.fields.observed"},
	"issue.edit":   {"payload", "results.data.verified.fields.observed"},
	// Native creation receipts retain Jira's own nested vocabulary. Only the
	// exact receipt subtree is exempt; the CLI's per-key fields stay checked.
	"issue.clone": {"results.data.receipt"},
	// The unkeyed sibling's boundary, one level shallower for the reason
	// its passthroughFidelity entry gives. data.body — the composed
	// document — needs no exemption here either: ADF's own keys are all
	// single lowercase words and pass the snake_case rule unaided.
	"issue.comment.edit": {"comment.body"},
}

// Tenant status names and local alias names are data keys, not names minted by
// the CLI. Only these exact map boundaries permit arbitrary names; fixed keys stay
// subject to the naming guard independently of the issue-fields passthrough.
var literalMapBoundaries = map[string][]string{
	"epic.board": {"epics.counts", "totals"},
}

// checkSuccessSnakeCase is M4: every key the CLI mints is snake_case,
// except inside a declared passthrough boundary, where the wire's own
// idioms are expected and exempt — a naming rule that stops at the
// registered boundary, not one that vanishes for the whole payload.
func checkSuccessSnakeCase(cap Capture, m command.Meta) (bool, string) {
	var doc any
	if err := json.Unmarshal([]byte(cap.Stdout), &doc); err != nil {
		return false, "stdout does not decode: " + err.Error()
	}
	var exemptions []string
	if m.Passthrough {
		boundary, ok := passthroughBoundary[m.Op]
		if !ok {
			return false, fmt.Sprintf("no passthrough boundary declared for op %q — add it beside its recipes", m.Op)
		}
		exemptions = append(exemptions, boundary...)
	}
	exemptions = append(exemptions, literalMapBoundaries[m.Op]...)
	if cap.Mode == ModeJSON {
		for i := range exemptions {
			exemptions[i] = "data." + exemptions[i]
		}
	}
	if bad := firstNonSnakeKey("", doc, exemptions...); bad != "" {
		return false, "key " + bad + " is not snake_case"
	}
	return true, ""
}

// firstNonSnakeKey walks the decoded document, reporting the first
// non-snake_case key found outside the exempt boundary. dotPath is the
// root-relative key path (no leading "$"), which is what exempt is
// compared against; the "$." form firstNonSnakeKey's callers expect
// back is built only at the point a violation is actually found, so the
// walk itself carries just the one path representation.
//
// An array element recurses under the SAME dotPath as its parent key —
// an index carries no name worth distinguishing, and a boundary
// declared against the array's key (e.g. "comments.body") must cover
// every element the same way, not one index at a time.
func firstNonSnakeKey(dotPath string, v any, exemptions ...string) string {
	switch node := v.(type) {
	case map[string]any:
		for key, val := range node {
			childDot := key
			if dotPath != "" {
				childDot = dotPath + "." + key
			}
			inBoundary := slices.ContainsFunc(exemptions, func(exempt string) bool {
				return exempt != "" && (childDot == exempt || strings.HasPrefix(childDot, exempt+"."))
			})
			if !inBoundary && !snakeCaseRe.MatchString(key) {
				return "$." + childDot
			}
			if inBoundary {
				continue
			}
			if bad := firstNonSnakeKey(childDot, val, exemptions...); bad != "" {
				return bad
			}
		}
	case []any:
		for _, elem := range node {
			if bad := firstNonSnakeKey(dotPath, elem, exemptions...); bad != "" {
				return bad
			}
		}
	}
	return ""
}

// utcTimestampRe is the strict shape the canonical mask matches — kept
// in lockstep with canonPatterns: a timestamp that is NOT UTC RFC 3339
// stays unmasked, so the M5 check below sees and rejects it.
var utcTimestampRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)

// checkSuccessTimestamps is M5 for success captures: the frame clock is
// UTC RFC 3339 (proven by the strict canonical mask having matched),
// and every declared CLI timestamp field in the data, when present,
// carries the same shape.
func checkSuccessTimestamps(cap Capture, m command.Meta) (bool, string) {
	if cap.Mode == ModeJSON {
		f, err := decodeFrame(cap.Stdout)
		if err != nil {
			return false, err.Error()
		}
		if f.Meta.Timestamp != "<CANON>" {
			return false, fmt.Sprintf("meta.timestamp = %q — the strict UTC RFC 3339 mask did not match it", f.Meta.Timestamp)
		}
	}
	data, err := successData(cap)
	if err != nil {
		return false, err.Error()
	}
	for _, field := range successTimestampFields[m.Op] {
		v, present := data[field]
		if !present {
			continue // absence is the field's documented unstamped state
		}
		s, isString := v.(string)
		if !isString || !utcTimestampRe.MatchString(s) {
			return false, fmt.Sprintf("data.%s = %v, want RFC 3339 UTC", field, v)
		}
	}
	return true, ""
}

// checkSuccessDryRunAbsent (M2, assert-absent on reads) and
// checkSuccessPreviewAbsent (M7, same) prove a read never claims
// mutation semantics.
func checkSuccessDryRunAbsent(cap Capture, _ command.Meta) (bool, string) {
	return checkKeyAbsent(cap, "dry_run")
}

func checkSuccessPreviewAbsent(cap Capture, _ command.Meta) (bool, string) {
	return checkKeyAbsent(cap, "preview")
}

func checkKeyAbsent(cap Capture, key string) (bool, string) {
	data, err := successData(cap)
	if err != nil {
		return false, err.Error()
	}
	if _, present := data[key]; present {
		return false, fmt.Sprintf("read success carries %q — mutation semantics on a read", key)
	}
	return true, ""
}

// checkSuccessSchemaTruth is M8 against the HAND-WRITTEN field contract
// in successDataShape — required fields present, nothing undeclared —
// plus per-op depth where the flat table cannot reach.
func checkSuccessSchemaTruth(cap Capture, m command.Meta) (bool, string) {
	shape, ok := successDataShape[m.Op]
	if !ok {
		return false, fmt.Sprintf("no data-shape expectation written for op %q — add it beside its recipes", m.Op)
	}
	data, err := successData(cap)
	if err != nil {
		return false, err.Error()
	}
	allowed := map[string]bool{}
	for _, key := range shape.required {
		if _, present := data[key]; !present {
			return false, fmt.Sprintf("required field %q missing", key)
		}
		allowed[key] = true
	}
	for _, key := range shape.optional {
		allowed[key] = true
	}
	for key := range data {
		if !allowed[key] {
			return false, fmt.Sprintf("field %q is not in the published contract", key)
		}
	}
	// Per-op depth. The flat table above can say a key is present; only
	// these can say what is inside it.
	switch m.Op {
	case "issue.input.prepare":
		return checkPreparedIssueInput(data)
	case "issue.create.input":
		return checkIssueInputFields(data, true)
	case "issue.edit.input":
		return checkIssueInputFields(data, false)
	case "issue.create":
		if data["attempted"] != true || data["applied"] != true {
			return false, "native creation success must publish attempted=true and applied=true"
		}
		return checkIssueCreationReceipt(data["issue"], data["receipt"])
	case "issue.clone":
		results, ok := data["results"].([]any)
		if !ok || len(results) == 0 {
			return false, "clone success carries no per-source results"
		}
		succeeded := false
		for i, raw := range results {
			entry, ok := raw.(map[string]any)
			if !ok {
				return false, fmt.Sprintf("results[%d] is not an object", i)
			}
			if entry["ok"] != true {
				continue
			}
			succeeded = true
			child, ok := entry["data"].(map[string]any)
			if !ok || child["applied"] != true {
				return false, fmt.Sprintf("results[%d].data must publish applied=true for native creation success", i)
			}
			if ok, msg := checkIssueCreationReceipt(child["clone"], child["receipt"]); !ok {
				return false, fmt.Sprintf("results[%d].data: %s", i, msg)
			}
		}
		if !succeeded {
			return false, "clone success carries no successful source outcome"
		}
		return true, ""
	case "config.profile.list":
		return checkProfileList(data)
	case "config.profile.add", "config.profile.default", "config.profile.remove", "config.profile.rename":
		return checkProfileChange(data, strings.TrimPrefix(m.Op, "config.profile."), false)
	case "config.set":
		if data["key"] != "read_only" || data["value"] != "false" || data["previous_value"] != "true" || data["previous_present"] != true || data["previous_secret"] != false || data["would_change"] != true || data["changed"] != true || data["created"] != false || data["dry_run"] != false {
			return false, "stored setting publication lost the original declaration, typed value or actual effect"
		}
		if path, ok := data["path"].(string); !ok || path == "" {
			return false, "setting publication names no selected file"
		}
		return true, ""
	case "config.theme":
		return checkThemeSelection(data, false)
	case "config.init":
		return checkConfigInitialization(data, false)
	case "config.get":
		if data["key"] != "read_only" || data["value"] != "false" || data["present"] != true || data["secret"] != false || data["file_exists"] != true {
			return false, "stored read_only=false was replaced, omitted or misclassified"
		}
		if path, ok := data["path"].(string); !ok || path == "" {
			return false, "stored setting has no selected file path"
		}
		return true, ""
	case "config.list":
		return checkConfigListRows(data)
	case "version":
		return checkVersionFields(data)
	case "release.notes":
		releases, ok := data["releases"].([]any)
		if !ok || len(releases) != 1 {
			return false, "latest release notes must contain one structured release"
		}
		release, ok := releases[0].(map[string]any)
		if !ok || release["version"] != "0.16.0-beta.1" || release["tag"] != "v0.16.0-beta.1" || release["date"] != "2026-10-26" || release["url"] != "https://github.com/matcra587/jira-cli/releases/tag/v0.16.0-beta.1" {
			return false, "latest release notes lost embedded version or heading metadata"
		}
		wantSections := []struct {
			kind  string
			count int
		}{
			{kind: "Breaking Changes", count: 2},
			{kind: "Changed", count: 1},
			{kind: "Fixed", count: 4},
			{kind: "Dependencies", count: 3},
		}
		sections, ok := release["sections"].([]any)
		if !ok || len(sections) != len(wantSections) {
			return false, "latest release notes lost its change sections"
		}
		for i, want := range wantSections {
			section, ok := sections[i].(map[string]any)
			if !ok || section["kind"] != want.kind {
				return false, "latest release notes lost its ordered change kinds"
			}
			changes, ok := section["changes"].([]any)
			if !ok || len(changes) != want.count {
				return false, "latest release notes lost change entries"
			}
			for _, change := range changes {
				if text, ok := change.(string); !ok || strings.TrimSpace(text) == "" {
					return false, "release notes carry an empty or invalid change entry"
				}
			}
		}
		return true, ""
	case "issue.view":
		return checkIssueViewFields(data)
	case "issue.comment.list":
		return checkIssueCommentListFields(data)
	case "issue.list", "issue.mine":
		return checkIssueListFields(data)
	case "issue.link.list":
		return checkIssueLinkListFields(data)
	case "issue.attachment.list":
		return checkIssueAttachmentListFields(data)
	case "issue.watchers.list":
		return checkIssueWatchersListFields(data)
	case "issue.comment.add":
		return checkIssueCommentAddFields(data)
	case "issue.transition":
		return checkIssueTransitionFields(data)
	case "issue.transition.list":
		return checkTransitionDiscoveryFields(data)
	}
	return true, ""
}

// Both creation recipes use the native HTTP adapter. Their complete receipt
// must survive alongside the projected identity; unknown receipt members are
// native data and deliberately remain outside the CLI field whitelist.
func checkIssueCreationReceipt(rawIdentity, rawReceipt any) (bool, string) {
	identity, ok := rawIdentity.(map[string]any)
	if !ok {
		return false, "creation success carries no issue identity"
	}
	receipt, ok := rawReceipt.(map[string]any)
	if !ok {
		return false, "native creation success carries no complete object receipt"
	}
	for _, field := range []string{"id", "key"} {
		value, ok := identity[field].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return false, fmt.Sprintf("creation identity carries no usable %s", field)
		}
		if receipt[field] != value {
			return false, fmt.Sprintf("native receipt %s disagrees with the published identity", field)
		}
	}
	// The recipes deliberately author these optional members. Matching only
	// id/key would let an identity-only reconstruction discard the full receipt.
	expected := map[string]any{
		"self": "https://example.atlassian.net/rest/api/3/issue/" + identity["id"].(string),
		"transition": map[string]any{
			"status":            float64(200),
			"warningCollection": map[string]any{"warnings": []any{"transition receipt"}},
		},
		"watchers": map[string]any{"status": float64(204)},
		"future": map[string]any{
			"opaque": "native receipt sentinel",
			"values": []any{nil, false, float64(13)},
		},
	}
	for member, want := range expected {
		if !reflect.DeepEqual(receipt[member], want) {
			return false, fmt.Sprintf("native receipt discarded or changed authored %s", member)
		}
	}
	return true, ""
}

func TestCreationReceiptAuditRejectsLostNativeMembers(t *testing.T) {
	identity := map[string]any{"id": "10101", "key": "KEY-77"}
	var receipt map[string]any
	if err := json.Unmarshal([]byte(`{"id":"10101","key":"KEY-77","self":"https://example.atlassian.net/rest/api/3/issue/10101","transition":{"status":200,"warningCollection":{"warnings":["transition receipt"]}},"watchers":{"status":204},"future":{"opaque":"native receipt sentinel","values":[null,false,13]}}`), &receipt); err != nil {
		t.Fatal(err)
	}
	if ok, msg := checkIssueCreationReceipt(identity, receipt); !ok {
		t.Fatalf("complete native receipt rejected: %s", msg)
	}
	for _, member := range []string{"self", "transition", "watchers", "future"} {
		t.Run(member, func(t *testing.T) {
			incomplete := maps.Clone(receipt)
			delete(incomplete, member)
			if ok, _ := checkIssueCreationReceipt(identity, incomplete); ok {
				t.Fatalf("receipt with discarded %s still accepted", member)
			}
		})
	}
}

func TestCloneSuccessAuditRejectsAllFailedRows(t *testing.T) {
	cap := Capture{Mode: ModeCompact, Stdout: `{"dry_run":false,"results":[{"issue":{"key":"KEY-1"},"ok":false,"error":{"code":"jira_not_found","message":"source absent","hint":"inspect source"}}]}`}
	if ok, _ := checkSuccessSchemaTruth(cap, command.Meta{Op: "issue.clone"}); ok {
		t.Fatal("clone success accepted without any successful row")
	}
}

// checkIssueTransitionFields reaches inside the keyed results on the
// LIVE path and proves the thing this whole command exists for: that
// each key reports HOW its comment reached Jira, and that the two keys
// in the capture reported DIFFERENT answers.
//
// The last part is the tooth. Every other assertion here would stay
// green against an implementation that hard-coded one route: a CLI that
// always composed the comment into the transition would report
// "atomic" for both keys and satisfy any per-key shape check, while
// silently losing TST-3's comment on the real site. Requiring both
// routes to appear in one capture is what makes that impossible —
// backed by the stub, which 400s each wrong request rather than serving
// it (see serveTransitionPost and serveSeparateComment).
//
// comment_id is checked only on the separate path, and its ABSENCE is
// required on the atomic one. That asymmetry is the contract: the
// transition POST answers 204 with no body, so a comment created inside
// it has no id the CLI ever learns, and publishing an empty or invented
// one would be worse than publishing none.
func checkIssueTransitionFields(data map[string]any) (bool, string) {
	if ok, detail := checkTransitionNativePayload(data["payload"], "name", "Done", true); !ok {
		return false, detail
	}
	results, ok := data["results"].([]any)
	if !ok || len(results) == 0 {
		return false, "results is not a non-empty array"
	}
	seen := map[string]bool{}
	for i, raw := range results {
		entry, ok := raw.(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d] is not an object", i)
		}
		child, ok := entry["data"].(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d].data is missing on a successful key", i)
		}
		transition, ok := child["transition"].(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d].data.transition is missing; a live run resolved one to send the request, so it can say which", i)
		}
		if id, _ := transition["id"].(string); id == "" {
			return false, fmt.Sprintf("results[%d].data.transition.id is empty; it is the only thing the endpoint accepts, so a successful key had one", i)
		}
		if _, present := transition["has_screen"]; !present {
			return false, fmt.Sprintf("results[%d].data.transition.has_screen is absent; it is the fact that explains the route this key took", i)
		}
		posted, _ := child["comment_posted"].(string)
		if ok, detail := checkTransitionNativePayload(child["payload"], "id", transition["id"].(string), posted == "atomic"); !ok {
			return false, fmt.Sprintf("results[%d]: %s", i, detail)
		}
		switch posted {
		case "atomic":
			if _, invented := child["comment_id"]; invented {
				return false, fmt.Sprintf("results[%d].data.comment_id is present on the ATOMIC path — the transition POST answers 204 with no body, so no id was ever returned", i)
			}
		case "separate":
			if id, _ := child["comment_id"].(string); id == "" {
				return false, fmt.Sprintf("results[%d].data.comment_id is empty on the SEPARATE path — that comment was created by its own POST, whose 201 carries the id", i)
			}
		default:
			return false, fmt.Sprintf("results[%d].data.comment_posted = %q, want atomic or separate; the recipe attached a comment to every key", i, posted)
		}
		if _, foreseen := child["comment_plan"]; foreseen {
			return false, fmt.Sprintf("results[%d].data.comment_plan is present on a LIVE run — it is the preview's field, and carrying both tenses would leave a caller guessing which one happened", i)
		}
		seen[posted] = true
	}
	if !seen["atomic"] || !seen["separate"] {
		return false, fmt.Sprintf("the capture's keys reported routes %v, want BOTH atomic and separate — one key's workflow has a transition screen and the other's does not, so an implementation that picks one route for the whole run must fail here", slices.Sorted(maps.Keys(seen)))
	}
	return true, ""
}

// checkIssueCommentAddFields reaches inside the keyed results on the
// LIVE path, where the flat table above cannot: it can say "results" is
// present, only this can say each entry carries the comment Jira
// created.
//
// The created comment is the part worth guarding. It is published with
// no --verify flag because the POST's own 201 already carries it, and
// the id it brings is what `comment edit` and `comment delete` take — so
// a regression that dropped it would leave a caller unable to act on the
// comment they just made, with nothing else in the envelope saying so.
func checkIssueCommentAddFields(data map[string]any) (bool, string) {
	if route, _ := data["body_route"].(string); route != "adf" && route != "markdown" {
		return false, fmt.Sprintf("body_route = %v, want adf or markdown", data["body_route"])
	}
	results, ok := data["results"].([]any)
	if !ok || len(results) == 0 {
		return false, "results is not a non-empty array"
	}
	for i, raw := range results {
		entry, ok := raw.(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d] is not an object", i)
		}
		child, ok := entry["data"].(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d].data is missing on a successful key", i)
		}
		comment, ok := child["comment"].(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d].data.comment is missing; the POST response carries the created comment, so it costs nothing to publish and the id is unobtainable otherwise", i)
		}
		if id, _ := comment["id"].(string); id == "" {
			return false, fmt.Sprintf("results[%d].data.comment.id is empty; it is the value comment edit and comment delete both take", i)
		}
		if _, ok := comment["body"].(map[string]any); !ok {
			return false, fmt.Sprintf("results[%d].data.comment.body is not an ADF object", i)
		}
	}
	return true, ""
}

// checkIssueListFields reaches inside the issues array and the
// pagination block: M8 must prove the fields the table's own columns
// and a machine caller both depend on actually render, not merely that
// "issues" and "pagination" are present as keys.
func checkIssueListFields(data map[string]any) (bool, string) {
	issues, ok := data["issues"].([]any)
	if !ok {
		return false, "issues is not an array — a null or object here would still satisfy the flat field list"
	}
	if len(issues) == 0 {
		return false, "issues is empty; the success fixture must carry at least one issue"
	}
	for i, raw := range issues {
		issue, ok := raw.(map[string]any)
		if !ok {
			return false, fmt.Sprintf("issues[%d] is not an object", i)
		}
		for _, key := range []string{"key", "fields"} {
			if _, present := issue[key]; !present {
				return false, fmt.Sprintf("issues[%d].%s missing", i, key)
			}
		}
		if key, _ := issue["key"].(string); key == "" {
			return false, fmt.Sprintf("issues[%d].key missing or empty", i)
		}
		fields, ok := issue["fields"].(map[string]any)
		if !ok {
			return false, fmt.Sprintf("issues[%d].fields is not an object", i)
		}
		if s, _ := fields["summary"].(string); s == "" {
			return false, fmt.Sprintf("issues[%d].fields.summary missing or empty", i)
		}
		status, ok := fields["status"].(map[string]any)
		if !ok {
			return false, fmt.Sprintf("issues[%d].fields.status is not an object", i)
		}
		if name, _ := status["name"].(string); name == "" {
			return false, fmt.Sprintf("issues[%d].fields.status.name missing or empty", i)
		}
	}
	// The bar above proves every issue; the M10 fidelity check
	// (checkSuccessPassthroughFidelity) separately reaches into
	// issues[0].fields.customfield_10011 specifically, so no extra
	// "first entry" carve-out is needed here.

	pagination, ok := data["pagination"].(map[string]any)
	if !ok {
		return false, "pagination is not an object"
	}
	isLast, present := pagination["is_last"]
	if !present {
		return false, "pagination.is_last missing"
	}
	if _, isBool := isLast.(bool); !isBool {
		return false, fmt.Sprintf("pagination.is_last = %v (%T), want a boolean", isLast, isLast)
	}
	returned, present := pagination["returned"]
	if !present {
		return false, "pagination.returned missing"
	}
	if _, isNumber := returned.(float64); !isNumber {
		return false, fmt.Sprintf("pagination.returned = %v (%T), want a number", returned, returned)
	}
	// next_page_token is omitempty (genuinely absent on the last page,
	// per CursorPagination's own doc comment) — present here because the
	// fixture's own is_last=false pagination-hint proof needs it, but its
	// presence is not itself part of the required contract, only its
	// TYPE when present.
	if v, present := pagination["next_page_token"]; present {
		if _, isString := v.(string); !isString {
			return false, fmt.Sprintf("pagination.next_page_token = %v (%T), want a string when present", v, v)
		}
	}

	truncated, present := data["truncated"]
	if !present {
		return false, "truncated missing"
	}
	truncatedBool, isBool := truncated.(bool)
	if !isBool {
		return false, fmt.Sprintf("truncated = %v (%T), want a boolean", truncated, truncated)
	}
	// truncated_reason is omitempty — present only when truncated is
	// true (envelope.IssueListOutput's own doc comment) — so its
	// presence is checked against truncated's OWN value, not merely its
	// type when present.
	reason, reasonPresent := data["truncated_reason"]
	switch {
	case truncatedBool && !reasonPresent:
		return false, "truncated is true but truncated_reason is missing"
	case !truncatedBool && reasonPresent:
		return false, "truncated is false but truncated_reason is present"
	case reasonPresent:
		if _, isString := reason.(string); !isString {
			return false, fmt.Sprintf("truncated_reason = %v (%T), want a string", reason, reason)
		}
	}
	return true, ""
}

// checkIssueLinkListFields reaches inside the keyed results: M8 must
// prove the fields the LINK table's columns and a machine caller both
// depend on actually render, not merely that "results" is present as a
// key.
//
// This fixture explicitly returns link arrays, including an empty array.
// The output must retain them, and at least one result must carry a
// link, or the per-link field checks below would be vacuous against a
// fixture that had quietly stopped serving links at all.
func checkIssueLinkListFields(data map[string]any) (bool, string) {
	results, ok := data["results"].([]any)
	if !ok {
		return false, "results is not an array"
	}
	linked := 0
	for i, raw := range results {
		entry, ok := raw.(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d] is not an object", i)
		}
		payload, ok := entry["data"].(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d].data is not an object — the keyed operation object belongs there", i)
		}
		links, ok := payload["links"].([]any)
		if !ok {
			return false, fmt.Sprintf("results[%d].data.links lost the fixture's explicit array", i)
		}
		linked += len(links)
		for j, rawLink := range links {
			if ok, msg := checkIssueLinkFields(i, j, rawLink); !ok {
				return false, msg
			}
		}
	}
	if linked == 0 {
		return false, "no result carries a link; the success fixture must serve at least one"
	}
	return true, ""
}

// checkIssueLinkFields is the per-link half: the link's own identity and
// direction, the relationship phrase this command derives for the key it
// was asked about, and enough of the far issue to draw a row.
func checkIssueLinkFields(result, index int, raw any) (bool, string) {
	where := fmt.Sprintf("results[%d].data.links[%d]", result, index)
	link, ok := raw.(map[string]any)
	if !ok {
		return false, where + " is not an object"
	}
	for _, key := range []string{"id", "direction", "relation", "type", "issue"} {
		if _, present := link[key]; !present {
			return false, where + "." + key + " missing"
		}
	}
	// Direction is a closed set, and the one field a caller branches on
	// to know which end of the link they are reading from.
	switch direction, _ := link["direction"].(string); direction {
	case "inward", "outward":
	default:
		return false, fmt.Sprintf("%s.direction = %q, want \"inward\" or \"outward\"", where, direction)
	}
	if relation, _ := link["relation"].(string); relation == "" {
		return false, where + ".relation is empty — the link would read as nothing at all"
	}
	linkType, ok := link["type"].(map[string]any)
	if !ok {
		return false, where + ".type is not an object"
	}
	for _, key := range []string{"name", "inward", "outward"} {
		if s, _ := linkType[key].(string); s == "" {
			return false, fmt.Sprintf("%s.type.%s missing or empty", where, key)
		}
	}
	far, ok := link["issue"].(map[string]any)
	if !ok {
		return false, where + ".issue is not an object"
	}
	if key, _ := far["key"].(string); key == "" {
		return false, where + ".issue.key missing or empty"
	}
	fields, ok := far["fields"].(map[string]any)
	if !ok {
		return false, where + ".issue.fields is not an object"
	}
	if summary, _ := fields["summary"].(string); summary == "" {
		return false, where + ".issue.fields.summary missing or empty"
	}
	status, ok := fields["status"].(map[string]any)
	if !ok {
		return false, where + ".issue.fields.status is not an object"
	}
	if name, _ := status["name"].(string); name == "" {
		return false, where + ".issue.fields.status.name missing or empty"
	}
	// The category is what a renderer colors by, and the only stable
	// signal about a status a caller can branch on — a per-workflow name
	// is not one.
	if category, _ := status["category"].(string); category == "" {
		return false, where + ".issue.fields.status.category missing or empty"
	}
	return true, ""
}

// checkIssueAttachmentListFields reaches inside the results array: M8
// must prove the fields the table's columns and a machine caller both
// depend on actually render, not merely that "results" is present.
func checkIssueAttachmentListFields(data map[string]any) (bool, string) {
	results, ok := data["results"].([]any)
	if !ok {
		return false, "results is not an array"
	}
	attached := 0
	for i, raw := range results {
		entry, ok := raw.(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d] is not an object", i)
		}
		payload, ok := entry["data"].(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d].data is not an object — the keyed operation object belongs there", i)
		}
		// The per-key object is the whole operation payload, and this
		// command's is exactly one key. Checked exhaustively rather than
		// just for presence: the flat dataShapes table above cannot see
		// inside a keyed entry, so without this a pagination block (or
		// anything else) added at results[].data would go unnoticed by
		// the very check written to catch it.
		for key := range payload {
			if key != "attachments" {
				return false, fmt.Sprintf("results[%d].data.%s is not in the published contract — this command publishes attachments and nothing else, pagination included (there is no attachment-list endpoint to paginate)", i, key)
			}
		}
		attachments, ok := payload["attachments"].([]any)
		if !ok {
			return false, fmt.Sprintf("results[%d].data.attachments is not an array — a key with no attachments must publish an empty array, never null", i)
		}
		attached += len(attachments)
		for j, rawAttachment := range attachments {
			if ok, msg := checkAttachmentFields(i, j, rawAttachment); !ok {
				return false, msg
			}
		}
	}
	if attached == 0 {
		return false, "no result carries an attachment; the success fixture must serve at least one"
	}
	return true, ""
}

// checkAttachmentFields is the per-attachment half: the id the download
// and delete verbs take, enough to draw a row, and the size as a real
// JSON NUMBER.
//
// The size's type is checked, not just its presence. Publishing it as a
// formatted display string ("1.4 MiB") is the plausible mistake — the
// human table renders exactly that — and it would leave a caller
// summing or thresholding attachment sizes parsing a string back into a
// number, which is the whole reason the contract carries the integer.
func checkAttachmentFields(result, index int, raw any) (bool, string) {
	where := fmt.Sprintf("results[%d].data.attachments[%d]", result, index)
	attachment, ok := raw.(map[string]any)
	if !ok {
		return false, where + " is not an object"
	}
	for _, key := range []string{"id", "filename", "mime_type", "size", "created", "author", "content"} {
		if _, present := attachment[key]; !present {
			return false, where + "." + key + " missing"
		}
	}
	for _, key := range []string{"id", "filename", "content"} {
		if s, _ := attachment[key].(string); s == "" {
			return false, fmt.Sprintf("%s.%s is empty — %s", where, key,
				"an attachment with no id could not be downloaded, and one with no filename or content URL could not be identified or fetched")
		}
	}
	// encoding/json decodes every JSON number into a float64 here, so
	// this asserts "arrived as a number" — a string would fail the type
	// assertion outright, which is the mistake being guarded.
	size, ok := attachment["size"].(float64)
	if !ok {
		return false, fmt.Sprintf("%s.size = %v (%T), want a JSON number — a formatted size string would force every machine caller to parse it back", where, attachment["size"], attachment["size"])
	}
	if size <= 0 {
		return false, fmt.Sprintf("%s.size = %v, want the fixture's real byte count", where, size)
	}
	author, ok := attachment["author"].(map[string]any)
	if !ok {
		return false, where + ".author is not an object"
	}
	if name, _ := author["display_name"].(string); name == "" {
		return false, where + ".author.display_name missing or empty"
	}
	return true, ""
}

// checkIssueWatchersListFields reaches inside the results array: M8
// must prove the fields the table's columns and a machine caller both
// depend on actually render, not merely that "results" is present.
//
// The fixture returns explicit counts and membership for all three keys.
// Only TST-2 returns a watcher array; TST-3 reports zero and TST-4 reports
// seven without a list. Missing lists must remain absent in the output.
func checkIssueWatchersListFields(data map[string]any) (bool, string) {
	results, ok := data["results"].([]any)
	if !ok || len(results) != 3 {
		return false, "results must retain all three watcher fixtures"
	}
	watching, restricted := 0, 0
	for i, raw := range results {
		entry, ok := raw.(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d] is not an object", i)
		}
		identity, _ := entry["issue"].(map[string]any)
		if identity["key"] != []string{"TST-2", "TST-3", "TST-4"}[i] {
			return false, fmt.Sprintf("results[%d] lost its requested watcher identity", i)
		}
		payload, ok := entry["data"].(map[string]any)
		if !ok {
			return false, fmt.Sprintf("results[%d].data is not an object — the keyed operation object belongs there", i)
		}
		// Exhaustive, for the reason checkIssueAttachmentListFields gives:
		// the flat dataShapes table cannot see inside a keyed entry, so
		// without this a pagination block added at results[].data would go
		// unnoticed by the very check written to catch it.
		for key := range payload {
			switch key {
			case "watchers", "watch_count", "is_watching":
			default:
				return false, fmt.Sprintf("results[%d].data.%s is not in the published contract — this command publishes watchers, watch_count and is_watching, pagination included (the watcher endpoint takes no paging parameters)", i, key)
			}
		}
		var watchers []any
		if i == 0 {
			watchers, ok = payload["watchers"].([]any)
			if !ok || len(watchers) != 3 {
				return false, "populated watcher fixture must retain all three accounts"
			}
		} else if _, present := payload["watchers"]; present {
			return false, fmt.Sprintf("results[%d].data.watchers fabricated a list absent from the fixture", i)
		}
		// encoding/json decodes every JSON number into a float64 here, so
		// this asserts "arrived as a number" as well as "present".
		count, ok := payload["watch_count"].(float64)
		if !ok || count != []float64{3, 0, 7}[i] {
			return false, fmt.Sprintf("results[%d].data.watch_count = %v (%T), want a JSON number — it is the only thing that distinguishes a hidden watcher list from an empty one", i, payload["watch_count"], payload["watch_count"])
		}
		if member, ok := payload["is_watching"].(bool); !ok || member != (i == 0) {
			return false, fmt.Sprintf("results[%d].data.is_watching = %v (%T), want a JSON boolean", i, payload["is_watching"], payload["is_watching"])
		}
		if len(watchers) == 0 && count > 0 {
			restricted++
		}
		watching += len(watchers)
		for j, rawWatcher := range watchers {
			if ok, msg := checkWatcherFields(i, j, rawWatcher); !ok {
				return false, msg
			}
		}
	}
	if watching == 0 {
		return false, "no result carries a watcher; the success fixture must serve at least one"
	}
	// The permission-restricted shape is the one this command exists to
	// get right, and it is invisible to every check above if the fixture
	// stops serving it — a corpus that only ever saw populated and
	// genuinely-empty results would pass while the distinction rotted.
	if restricted == 0 {
		return false, "no result retains the positive watcher count with an absent list"
	}
	return true, ""
}

// checkWatcherFields is the per-watcher half: the account id the add and
// remove verbs take, the name a row is read by, and the two
// classification fields the table draws.
//
// active is checked for TYPE rather than value: both values are
// legitimate (a deactivated account keeps watching), so what matters is
// that it arrives as a real boolean rather than the string "false",
// which would be truthy in every language a caller might branch in.
func checkWatcherFields(result, index int, raw any) (bool, string) {
	where := fmt.Sprintf("results[%d].data.watchers[%d]", result, index)
	watcher, ok := raw.(map[string]any)
	if !ok {
		return false, where + " is not an object"
	}
	for _, key := range []string{"account_id", "display_name", "active", "account_type"} {
		if _, present := watcher[key]; !present {
			return false, where + "." + key + " missing"
		}
	}
	for _, key := range []string{"account_id", "display_name"} {
		if s, _ := watcher[key].(string); s == "" {
			return false, fmt.Sprintf("%s.%s is empty — %s", where, key,
				"a watcher with no account id could not be removed, and one with no display name could not be identified")
		}
	}
	if _, ok := watcher["active"].(bool); !ok {
		return false, fmt.Sprintf("%s.active = %v (%T), want a JSON boolean — the string \"false\" is truthy almost everywhere", where, watcher["active"], watcher["active"])
	}
	return true, ""
}

// checkIssueCommentListFields reaches inside the comments array and the
// pagination block: M8 must prove the fields the human render path and
// a machine caller both depend on actually render, not merely that
// "comments" and "pagination" are present as keys.
func checkIssueCommentListFields(data map[string]any) (bool, string) {
	issue, ok := data["issue"].(map[string]any)
	if !ok {
		return false, "issue is not an object"
	}
	if key, _ := issue["key"].(string); key == "" {
		return false, "issue.key missing or empty"
	}

	comments, ok := data["comments"].([]any)
	if !ok {
		return false, "comments is not an array — a null or object here would still satisfy the flat field list"
	}
	if len(comments) == 0 {
		return false, "comments is empty; the success fixture must carry at least one comment"
	}
	for i, raw := range comments {
		c, ok := raw.(map[string]any)
		if !ok {
			return false, fmt.Sprintf("comments[%d] is not an object", i)
		}
		for _, key := range []string{"id", "author", "created", "updated", "body"} {
			if _, present := c[key]; !present {
				return false, fmt.Sprintf("comments[%d].%s missing", i, key)
			}
		}
		if id, _ := c["id"].(string); id == "" {
			return false, fmt.Sprintf("comments[%d].id missing or empty", i)
		}
		author, ok := c["author"].(map[string]any)
		if !ok {
			return false, fmt.Sprintf("comments[%d].author is not an object", i)
		}
		if name, _ := author["display_name"].(string); name == "" {
			return false, fmt.Sprintf("comments[%d].author.display_name missing or empty", i)
		}
		// A null body would still satisfy the presence check above (the
		// key exists, decoded as JSON null) — this is the shape/type
		// tooth, not a golden byte comparison against the fixture's ADF.
		if c["body"] == nil {
			return false, fmt.Sprintf("comments[%d].body is null", i)
		}
	}
	// The bar above proves every comment; the M10 fidelity check
	// (checkSuccessPassthroughFidelity) separately reaches into
	// comments[0].body specifically, so no extra "first entry" carve-out
	// is needed here — every element already meets the same shape.

	pagination, ok := data["pagination"].(map[string]any)
	if !ok {
		return false, "pagination is not an object"
	}
	for _, key := range []string{"start_at", "max_results", "total", "returned"} {
		v, present := pagination[key]
		if !present {
			return false, fmt.Sprintf("pagination.%s missing", key)
		}
		// json.Unmarshal into map[string]any decodes every JSON number as
		// float64 — asserting that type is how this check proves
		// "numeric", not merely "present" (a string like "3" would pass a
		// bare presence check but break a caller doing arithmetic on it).
		if _, isNumber := v.(float64); !isNumber {
			return false, fmt.Sprintf("pagination.%s = %v (%T), want a number", key, v, v)
		}
	}
	return true, ""
}

// checkIssueViewFields reaches inside the identity object: M8 must
// prove the curated fields the schema and human render paths depend on
// actually render, not merely that "issue" is present as a key.
func checkIssueViewFields(data map[string]any) (bool, string) {
	issue, ok := data["issue"].(map[string]any)
	if !ok {
		return false, "issue is not an object"
	}
	for _, key := range []string{"key", "id", "self", "fields"} {
		if _, present := issue[key]; !present {
			return false, fmt.Sprintf("issue.%s missing", key)
		}
	}
	fields, ok := issue["fields"].(map[string]any)
	if !ok {
		return false, "issue.fields is not an object"
	}
	if s, _ := fields["summary"].(string); s == "" {
		return false, "issue.fields.summary missing or empty"
	}
	status, ok := fields["status"].(map[string]any)
	if !ok {
		return false, "issue.fields.status is not an object"
	}
	if name, _ := status["name"].(string); name == "" {
		return false, "issue.fields.status.name missing or empty"
	}
	if _, ok := status["statusCategory"].(map[string]any); !ok {
		return false, "issue.fields.status.statusCategory is not an object"
	}
	return true, ""
}

// checkConfigListRows reaches inside the settings array. A list whose
// rows are unchecked is a contract that says "there is a settings key"
// and nothing about what is in it — settings: null, or rows missing
// value/source/secret, would satisfy the flat table.
func checkConfigListRows(data map[string]any) (bool, string) {
	rows, ok := data["settings"].([]any)
	if !ok {
		return false, "settings is not an array — a null or object here would still satisfy the flat field list"
	}
	if len(rows) == 0 {
		return false, "settings is empty; every declared key must render a row"
	}
	for i, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			return false, fmt.Sprintf("settings[%d] is not an object", i)
		}
		for _, key := range []string{"key", "value", "source", "secret"} {
			if _, present := row[key]; !present {
				return false, fmt.Sprintf("settings[%d] is missing %q", i, key)
			}
		}
		if name, _ := row["key"].(string); name == "" {
			return false, fmt.Sprintf("settings[%d] has an empty key", i)
		}
		// Source is the column the command exists for; blank makes the row
		// unable to answer "why is it this?".
		if src, _ := row["source"].(string); src == "" {
			return false, fmt.Sprintf("settings[%d] has an empty source", i)
		}
	}
	return true, ""
}

// checkVersionFields reaches inside the version payload's nested object,
// which the flat field list cannot see into.
func checkVersionFields(data map[string]any) (bool, string) {
	platform, _ := data["platform"].(map[string]any)
	for _, key := range []string{"os", "arch"} {
		if s, _ := platform[key].(string); s == "" {
			return false, fmt.Sprintf("platform.%s missing or empty", key)
		}
	}
	if s, _ := data["version"].(string); s == "" {
		return false, "version is empty"
	}
	return true, ""
}

// checkSuccessDeterminism is M12 across modes: two separate invocations
// (json and compact run in isolated workdirs) must serialize the same
// data object — same fields present, same values, no flapping between
// null, absent, and zero.
func checkSuccessDeterminism(cap Capture, _ command.Meta) (bool, string) {
	sibling, ok := siblingModeCapture(cap)
	if !ok {
		return false, "no sibling machine-mode capture to compare against"
	}
	return checkSuccessDeterminismPair(cap, sibling)
}

func checkSuccessDeterminismPair(cap, sibling Capture) (bool, string) {
	mine, err := successData(cap)
	if err != nil {
		return false, err.Error()
	}
	theirs, err := successData(sibling)
	if err != nil {
		return false, "sibling capture: " + err.Error()
	}
	if cap.Scenario.Op == "config.migrate" || cap.Scenario.Op == "config.rollback" {
		if err := normalizeMigrationComparison(mine); err != nil {
			return false, err.Error()
		}
		if err := normalizeMigrationComparison(theirs); err != nil {
			return false, err.Error()
		}
	}
	if !reflect.DeepEqual(mine, theirs) {
		return false, fmt.Sprintf("data differs across modes: %v vs %v", mine, theirs)
	}
	return true, ""
}

// checkFailureSurface: M11. Full typed frame on stdout; silent stderr.
// Exit mirrored in meta. Error code/message/hint required, even before parse.
// NoSuccessEnvelope: success only; failures keep uniform frames.
func checkFailureSurface(cap Capture, _ command.Meta) (bool, string) {
	if cap.ExitCode != cap.Scenario.ExpectExit {
		return false, fmt.Sprintf("exit = %d, want %d", cap.ExitCode, cap.Scenario.ExpectExit)
	}
	if cap.Stderr != "" {
		return false, "machine failure emitted diagnostics outside its typed frame"
	}
	if cap.Stdout == "" {
		return false, "failure left stdout empty — every failure the binary can produce must reach the machine envelope in machine modes"
	}
	f, err := decodeFrame(cap.Stdout)
	if err != nil {
		return false, err.Error()
	}
	switch {
	case f.OK == nil || *f.OK:
		return false, "failure frame ok is not false"
	case f.Meta.ContractVersion != "1":
		return false, fmt.Sprintf("failure frame meta.contract_version = %q, want %q", f.Meta.ContractVersion, "1")
	case f.Meta.ExitCode == nil || *f.Meta.ExitCode != cap.ExitCode:
		return false, "meta.exit_code does not mirror the process exit"
	case len(f.Errors) == 0:
		return false, "failure frame carries no errors"
	}
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
	}
	if err := json.Unmarshal(f.Errors[0], &e); err != nil {
		return false, "errors[0] does not decode: " + err.Error()
	}
	if e.Code == "" || e.Message == "" || e.Hint == "" {
		return false, fmt.Sprintf("errors[0] = %+v, want code, message, and hint all present", e)
	}
	// The code, not just the exit: a recipe that reaches a different
	// failure than the one it is named for is the failure mode this
	// pins, and an exit code alone cannot see it.
	switch {
	case cap.Scenario.ExpectCode == "":
		return false, "failure recipe declares no ExpectCode — an exit code alone cannot tell this failure from any other in its class"
	case e.Code != cap.Scenario.ExpectCode:
		return false, fmt.Sprintf("errors[0].code = %q, want %q — the recipe reached a different failure than it names", e.Code, cap.Scenario.ExpectCode)
	}
	return true, ""
}

// TestCorpusEvaluatesEveryDerivedCell runs the real binary's captures
// through the axis checks, recording one cell per expected coordinate
// that has a capture. Cells with no capture stay unrecorded on purpose:
// TestMain's finalization fails them by name (R10), which separates
// "the recipe is missing" from "the check is red".
func TestCorpusEvaluatesEveryDerivedCell(t *testing.T) {
	members, problems := liveMembers()
	if len(problems) > 0 {
		t.Fatalf("inventory not clean; fix the inventory failures first:\n  %s", strings.Join(problems, "\n  "))
	}
	captures, err := corpus()
	if err != nil {
		t.Fatalf("build corpus: %v", err)
	}

	for _, cell := range expectedCells(members) {
		member, ok := memberFor(members, cell.Op, cell.Variant)
		if !ok {
			continue // impossible by derivation; finalization would name it
		}
		capture, ok := captureFor(captures, cell.Op, cell.Variant, cell.Category, cell.Mode)
		if !ok {
			continue // unrecorded → named by finalization (R10)
		}
		check := axisChecks[cell.Category][cell.Axis]
		if check == nil {
			record(cell, false, "no axis check implemented for "+string(cell.Category)+"×"+string(cell.Axis))
			t.Errorf("cell %s: no axis check implemented", cell.Key())
			continue
		}
		ok, detail := check(capture, member.Meta)
		record(cell, ok, detail)
		if !ok && !isExempt(cell) {
			t.Errorf("cell %s red: %s", cell.Key(), detail)
		}
	}
}

// isExempt consults the reviewed exemption table for one cell.
func isExempt(cell ExpectedCell) bool {
	for _, e := range exemptions {
		if e.Cell == cell.Key() {
			return true
		}
	}
	return false
}

// TestCanonicalizeMasksVolatileValues pins the canonicaliser the whole
// matrix's stability rests on: correlation ids, clock reads, ephemeral
// ports, and temp paths must all mask; stable content must survive —
// and a NON-UTC timestamp must NOT mask, because the M5 check reads
// "the mask matched" as "the clock met the contract".
func TestCanonicalizeMasksVolatileValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "request id",
			in:   `{"request_id":"0a1b2c3d4e5f67890a1b2c3d4e5f6789"}`,
			want: `{"request_id":"<CANON>"}`,
		},
		{
			name: "timestamp",
			in:   `{"timestamp":"2026-08-02T10:11:12Z"}`,
			want: `{"timestamp":"<CANON>"}`,
		},
		{
			name: "non-utc timestamp stays visible for the M5 check",
			in:   `{"timestamp":"2026-08-02T10:11:12+02:00"}`,
			want: `{"timestamp":"2026-08-02T10:11:12+02:00"}`,
		},
		{name: "cache publication UTC timestamp", in: `{"written_at":"2026-10-03T10:11:12.123456789Z"}`, want: `{"written_at":"<CANON>"}`},
		{name: "non-UTC cache timestamp remains visible", in: `{"written_at":"2026-10-03T10:11:12+02:00"}`, want: `{"written_at":"2026-10-03T10:11:12+02:00"}`},
		{name: "cache metadata text is stable content", in: `{"written_at":"not a timestamp"}`, want: `{"written_at":"not a timestamp"}`},
		{name: "dedicated random credential backup", in: `{"credential_backup":"jira-cli-recovery-01234567-89ab-cdef-0123-456789abcdef"}`, want: `{"credential_backup":"<CANON>"}`},
		{name: "invalid credential backup remains visible", in: `{"credential_backup":"jira-cli-recovery-invalid"}`, want: `{"credential_backup":"jira-cli-recovery-invalid"}`},
		{name: "nil credential backup remains visible", in: `{"credential_backup":"jira-cli-recovery-00000000-0000-0000-0000-000000000000"}`, want: `{"credential_backup":"jira-cli-recovery-00000000-0000-0000-0000-000000000000"}`},
		{name: "backup spelling in unrelated data remains visible", in: `{"item":"jira-cli-recovery-01234567-89ab-cdef-0123-456789abcdef"}`, want: `{"item":"jira-cli-recovery-01234567-89ab-cdef-0123-456789abcdef"}`},
		{
			name: "loopback port",
			in:   "dial tcp 127.0.0.1:43831: refused",
			want: "dial tcp <CANON>: refused",
		},
		{
			name: "temp path",
			in:   "/tmp/jira-audit-work-12345/x failed",
			want: "<CANON> failed",
		},
		{
			name: "stable content survives",
			in:   `unknown flag: --definitely-not-a-flag`,
			want: `unknown flag: --definitely-not-a-flag`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := canonicalize(tt.in); got != tt.want {
				t.Errorf("canonicalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestScenarioEnvironmentIsHermetic pins the guard rails themselves:
// the scenario environment must carry the unroutable proxy and must not
// leak the developer's JIRA_* environment. The check runs against the
// environment CONSTRUCTION by executing the help scenario and asserting
// it succeeded offline (nothing live needs a server yet; when a
// stub-backed scenario lands, forgetting its stub dies at the proxy,
// loudly).
func TestScenarioEnvironmentIsHermetic(t *testing.T) {
	captures, err := corpus()
	if err != nil {
		t.Fatalf("build corpus: %v", err)
	}
	capture, ok := captures["root-help|human"]
	if !ok {
		t.Fatal("help capture missing")
	}
	if capture.ExitCode != 0 || capture.Stdout == "" {
		t.Errorf("help surface did not render offline: exit %d, stdout %d bytes", capture.ExitCode, len(capture.Stdout))
	}
}

// TestPreParseFailureFramesAcrossModes drives the two pre-parse
// failures an agent most commonly produces — a typo'd flag and an
// unknown subcommand — through the real binary in BOTH machine modes,
// and requires the full typed frame with the class-specific code and
// its recovery data. The unknown-subcommand recipe cannot live in the
// scenario table (recipe args must resolve against the live tree), so
// this test runs it adhoc; the typo'd flag rides the table too, and is
// re-checked here for its CODE, which M11 does not pin.
func TestPreParseFailureFramesAcrossModes(t *testing.T) {
	bin, err := jiraBinary()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		args        []string
		wantCode    string
		wantField   string
		wantSuggest string
	}{
		{
			name:      "typoed flag",
			args:      []string{"--definitely-not-a-flag"},
			wantCode:  "flag_unknown",
			wantField: "definitely-not-a-flag",
		},
		{
			name:        "unknown subcommand",
			args:        []string{"verson"},
			wantCode:    "command_unknown",
			wantField:   "verson",
			wantSuggest: "version",
		},
	}
	for _, tt := range tests {
		for _, mode := range []Mode{ModeJSON, ModeCompact} {
			t.Run(tt.name+"/"+string(mode), func(t *testing.T) {
				capture, err := runScenario(bin, Scenario{
					ID: "adhoc-" + tt.name, Op: "root", Category: CategoryFailure, Args: tt.args,
				}, mode, "")
				if err != nil {
					t.Fatal(err)
				}
				if capture.ExitCode != 3 {
					t.Fatalf("exit = %d, want 3 (stderr %q)", capture.ExitCode, capture.Stderr)
				}
				f, err := decodeFrame(capture.Stdout)
				if err != nil {
					t.Fatalf("%v (stdout %q)", err, capture.Stdout)
				}
				if f.OK == nil || *f.OK || len(f.Errors) != 1 {
					t.Fatalf("frame = %q, want ok:false with one error", capture.Stdout)
				}
				var e struct {
					Code        string   `json:"code"`
					Hint        string   `json:"hint"`
					Field       string   `json:"field"`
					Suggestions []string `json:"suggestions"`
				}
				if err := json.Unmarshal(f.Errors[0], &e); err != nil {
					t.Fatalf("errors[0] does not decode: %v", err)
				}
				if e.Code != tt.wantCode {
					t.Errorf("code = %q, want %q", e.Code, tt.wantCode)
				}
				if e.Field != tt.wantField {
					t.Errorf("field = %q, want the offender %q", e.Field, tt.wantField)
				}
				if e.Hint == "" {
					t.Error("hint is empty; every code carries one")
				}
				if tt.wantSuggest != "" && !slices.Contains(e.Suggestions, tt.wantSuggest) {
					t.Errorf("suggestions = %v, want them to include %q", e.Suggestions, tt.wantSuggest)
				}
			})
		}
	}
}

// TestVersionHumanCaptureMatchesVersionFlag executes the human surface
// through the real binary: `version --output=human` and `-V` must emit
// identical bytes, because both read the root's resolved version. The
// golden-based H axes join the matrix with the human-surface work;
// until then this pins the human render by EXECUTION rather than by
// reading the renderer.
func TestVersionHumanCaptureMatchesVersionFlag(t *testing.T) {
	bin, err := jiraBinary()
	if err != nil {
		t.Fatal(err)
	}
	human, err := runScenario(bin, Scenario{ID: "adhoc-version-human", Op: "version", Category: CategorySuccess, Args: []string{"version"}}, ModeHuman, "")
	if err != nil {
		t.Fatal(err)
	}
	if human.ExitCode != 0 {
		t.Fatalf("human exit = %d, want 0 (stderr %q)", human.ExitCode, human.Stderr)
	}
	flag, err := runScenario(bin, Scenario{ID: "adhoc-version-flag", Op: "version", Category: CategorySuccess, Args: []string{"-V"}}, ModeHuman, "")
	if err != nil {
		t.Fatal(err)
	}
	if flag.ExitCode != 0 {
		t.Fatalf("-V exit = %d, want 0 (stderr %q)", flag.ExitCode, flag.Stderr)
	}
	if human.Stdout == "" || human.Stdout != flag.Stdout {
		t.Errorf("`version` human output %q != `-V` output %q — the two must share one source", human.Stdout, flag.Stdout)
	}
	if strings.Count(strings.TrimRight(human.Stdout, "\n"), "\n") != 0 {
		t.Errorf("human output is not a one-liner: %q", human.Stdout)
	}
}

// TestCorpusIsolatesTheCredentialStore pins the sandbox's least obvious
// boundary.
//
// HOME and XDG_* scope the filesystem, but the operating system's
// credential store is a SESSION service and ignores both. Without the
// neutralization below, the corpus runs the real binary against the
// developer's — or a CI runner's — actual login: `auth status` reaches a
// live Jira, and `auth token` prints a real secret into the test log.
// That happened once, which is why this is pinned rather than trusted.
func TestCorpusIsolatesTheCredentialStore(t *testing.T) {
	t.Parallel()
	env := scenarioEnv(t.TempDir(), Scenario{}, "", ModeHuman)

	var dbus string
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok && name == "DBUS_SESSION_BUS_ADDRESS" {
			dbus = value
		}
	}
	if dbus == "" {
		t.Fatal("the corpus environment does not neutralize DBUS_SESSION_BUS_ADDRESS; scenarios will read the machine's real credential store")
	}
	if dbus == os.Getenv("DBUS_SESSION_BUS_ADDRESS") && dbus != "" {
		t.Error("the corpus inherited the developer's session bus, so it can reach their real keyring")
	}
}
