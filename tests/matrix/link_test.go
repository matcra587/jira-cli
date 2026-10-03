//go:build flagmatrix

package matrix

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The `jira issue link` group (create, delete, list, types) and the
// `jira issue weblink` group (add, list, delete). The harness lives in
// list_test.go.

const (
	mx3LinkTypePath   = "/rest/api/3/issueLinkType"
	mx3IssueLinkPath  = "/rest/api/3/issueLink"
	mx3LinkTypesJSON  = `{"issueLinkTypes":[{"id":"10","name":"Blocks","inward":"is blocked by","outward":"blocks"}]}`
	mx3RemoteLinkPath = "/rest/api/3/issue/KEY-123/remotelink"
)

// mx3LinkBody is the link endpoint's request shape as the stub reads it.
type mx3LinkBody struct {
	Type struct {
		Name string `json:"name"`
	} `json:"type"`
	InwardIssue struct {
		Key string `json:"key"`
	} `json:"inwardIssue"`
	OutwardIssue struct {
		Key string `json:"key"`
	} `json:"outwardIssue"`
	Comment json.RawMessage `json:"comment"`
}

// mx3LinkHandler answers the link-type read and the link write,
// recording everything. broken (guarded by mu) fails the TYPE endpoint
// with a non-retryable 400 while the write stays healthy — the shape the
// cache tests need.
func mx3LinkHandler(rec *mx3Recorder, broken *bool, mu *sync.Mutex) http.Handler {
	typesDown := func() bool {
		if broken == nil {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		return *broken
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.Path == mx3LinkTypePath:
			if typesDown() {
				http.Error(w, `{"errorMessages":["link types are down"]}`, http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprint(w, mx3LinkTypesJSON)
		case req.Method == http.MethodPost && req.Path == mx3IssueLinkPath:
			w.WriteHeader(http.StatusCreated)
		default:
			http.Error(w, `{"errorMessages":["unexpected request"]}`, http.StatusBadRequest)
		}
	})
}

// mx3LinkPosts returns the recorded link-write bodies, decoded.
func mx3LinkPosts(t *testing.T, rec *mx3Recorder) []mx3LinkBody {
	t.Helper()
	reqs := rec.byPath(mx3IssueLinkPath)
	out := make([]mx3LinkBody, len(reqs))
	for i, req := range reqs {
		if err := json.Unmarshal(req.Body, &out[i]); err != nil {
			t.Fatalf("decode link body %d: %v\nbody: %s", i, err, req.Body)
		}
	}
	return out
}

// --to names the link's other end, and --type picks both the site type
// and the DIRECTION: the outward phrase puts the source inward, and the
// inward phrase swaps the two ends of the same request.
func TestLinkCreateToAndTypeComposeRequest(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3LinkHandler(rec, nil, nil))
	home := t.TempDir()

	forward := mx3Run(t, home, baseURL, "issue", "link", "create", "KEY-123",
		"--to", "KEY-456", "--type", "blocks", "--output=json")
	if forward.Exit != 0 {
		t.Fatalf("link create --type blocks: exit %d\nstderr: %s", forward.Exit, forward.Stderr)
	}
	reversed := mx3Run(t, home, baseURL, "issue", "link", "create", "KEY-123",
		"--to", "KEY-456", "--type", "is blocked by", "--output=json")
	if reversed.Exit != 0 {
		t.Fatalf("link create --type 'is blocked by': exit %d\nstderr: %s", reversed.Exit, reversed.Stderr)
	}

	posts := mx3LinkPosts(t, rec)
	if len(posts) != 2 {
		t.Fatalf("link writes = %d, want 2", len(posts))
	}
	fwd, rev := posts[0], posts[1]
	if fwd.Type.Name != "Blocks" || fwd.InwardIssue.Key != "KEY-123" || fwd.OutwardIssue.Key != "KEY-456" {
		t.Fatalf("forward body = %+v, want Blocks with KEY-123 inward and KEY-456 outward", fwd)
	}
	if rev.Type.Name != "Blocks" || rev.InwardIssue.Key != "KEY-456" || rev.OutwardIssue.Key != "KEY-123" {
		t.Fatalf("reversed body = %+v, want the same type with the two ends swapped", rev)
	}

	Prove(t, "jira issue link create", "to",
		"stub link body carried --to's KEY-456 as the other end of the link (outward for the forward phrase)")
	Prove(t, "jira issue link create", "type",
		`--type blocks resolved to type Blocks with KEY-123 inward; --type "is blocked by" swapped the same request's two ends`)
}

// mx3LinkCommentDoc is the --json-input fixture: the one member the link
// flags cannot express.
const mx3LinkCommentDoc = `{"comment":{"body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"linked"}]}]}}}`

// mx3LinkCreateWithPayload runs link create routing the comment document
// through the named flag and returns the recorded write body.
func mx3LinkCreateWithPayload(t *testing.T, flag string) mx3LinkBody {
	t.Helper()
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3LinkHandler(rec, nil, nil))
	home := t.TempDir()
	path := filepath.Join(home, "link.json")
	if err := os.WriteFile(path, []byte(mx3LinkCommentDoc), 0o600); err != nil {
		t.Fatalf("seed payload: %v", err)
	}
	res := mx3Run(t, home, baseURL, "issue", "link", "create", "KEY-123",
		"--to", "KEY-456", "--type", "blocks", "--"+flag, path, "--output=json")
	if res.Exit != 0 {
		t.Fatalf("link create --%s: exit %d\nstderr: %s", flag, res.Exit, res.Stderr)
	}
	posts := mx3LinkPosts(t, rec)
	if len(posts) != 1 {
		t.Fatalf("link writes = %d, want 1", len(posts))
	}
	return posts[0]
}

// requireLinkComment asserts the write body carried the document's
// comment text — absent entirely when the payload route is not wired.
func requireLinkComment(t *testing.T, body mx3LinkBody) {
	t.Helper()
	if len(body.Comment) == 0 {
		t.Fatalf("link body carries no comment — the payload route was dropped")
	}
	var comment struct {
		Body json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(body.Comment, &comment); err != nil || len(comment.Body) == 0 {
		t.Fatalf("link body comment = %s, want the document's ADF body", body.Comment)
	}
}

// --json-input carries the link's comment member into the same request.
func TestLinkCreateJSONInputCarriesComment(t *testing.T) {
	requireLinkComment(t, mx3LinkCreateWithPayload(t, "json-input"))
	Prove(t, "jira issue link create", "json-input",
		"stub link body carried the document's comment member in the same POST as the link")
}

// --from-json and --body-adf are --json-input's hidden alias spellings,
// each proved by the same wire effect the canonical has.
func TestLinkCreateHiddenPayloadAliases(t *testing.T) {
	requireLinkComment(t, mx3LinkCreateWithPayload(t, "from-json"))
	Prove(t, "jira issue link create", "from-json",
		"hidden --from-json routed the document into the link POST's comment member")
	requireLinkComment(t, mx3LinkCreateWithPayload(t, "body-adf"))
	Prove(t, "jira issue link create", "body-adf",
		"hidden --body-adf routed the document into the link POST's comment member")
}

// A bare --dry-run contacts nothing at all and says what it could not
// know: type_resolved stays false and the unresolved-type warning rides
// warnings[].
func TestLinkCreateDryRunContactsNothing(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "link", "create", "KEY-123",
		"--to", "KEY-456", "--type", "blocks", "--dry-run", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("link create --dry-run: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	mx3RequireNoRequests(t, rec)
	data := mx3Data(t, res)
	if data["dry_run"] != true {
		t.Fatalf("data.dry_run = %v, want true", data["dry_run"])
	}
	if data["type_resolved"] != false {
		t.Fatalf("data.type_resolved = %v, want false on a bare preview", data["type_resolved"])
	}
	Prove(t, "jira issue link create", "dry-run",
		"stub saw zero requests; envelope published dry_run=true and type_resolved=false")
}

// --validate-remote lets the preview read the type list — one GET, still
// no write — and fills in the resolved relationship.
func TestLinkCreateValidateRemoteResolvesType(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3LinkHandler(rec, nil, nil))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "link", "create", "KEY-123",
		"--to", "KEY-456", "--type", "blocks", "--dry-run", "--validate-remote", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("link create --dry-run --validate-remote: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	if got := rec.byPath(mx3LinkTypePath); len(got) != 1 {
		t.Fatalf("type-list reads = %d, want 1", len(got))
	}
	if got := rec.byPath(mx3IssueLinkPath); len(got) != 0 {
		t.Fatalf("link writes = %d, want 0 — the remote preview must still write nothing", len(got))
	}
	data := mx3Data(t, res)
	if data["type_resolved"] != true || data["relation"] != "blocks" {
		t.Fatalf("data.type_resolved = %v, data.relation = %v; want true and %q", data["type_resolved"], data["relation"], "blocks")
	}
	Prove(t, "jira issue link create", "validate-remote",
		"the preview made exactly one type-list GET and zero link POSTs, and published type_resolved=true with relation blocks (a bare preview leaves it false)")
}

// --parallelism is range-checked before any I/O.
func TestLinkCreateParallelismRefusedOutOfRange(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "link", "create", "KEY-123",
		"--to", "KEY-456", "--type", "blocks", "--parallelism", "0", "--output=json")
	mx3RequireRefusal(t, res, "flag_value_invalid", "parallelism")
	mx3RequireNoRequests(t, rec)
	Prove(t, "jira issue link create", "parallelism",
		"--parallelism 0 refused by code flag_value_invalid on field parallelism, before any request")
}

// --refresh on link create forgets the cached type list rather than
// bypassing it: after a failed --refresh, a plain run fails too.
func TestLinkCreateRefreshForgetsTypeCache(t *testing.T) {
	rec := &mx3Recorder{}
	var mu sync.Mutex
	broken := false
	baseURL := mx3Stub(t, mx3LinkHandler(rec, &broken, &mu))
	setBroken := func(v bool) { mu.Lock(); broken = v; mu.Unlock() }
	home := t.TempDir()
	argv := []string{"issue", "link", "create", "KEY-123", "--to", "KEY-456", "--type", "blocks", "--output=json"}

	if res := mx3Run(t, home, baseURL, argv...); res.Exit != 0 {
		t.Fatalf("warm-up create: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	setBroken(true)
	if res := mx3Run(t, home, baseURL, argv...); res.Exit != 0 {
		t.Fatalf("cached create against a broken type endpoint: exit %d — the cache is not serving, so nothing below discriminates\nstderr: %s", res.Exit, res.Stderr)
	}
	refreshed := mx3Run(t, home, baseURL, append(argv, "--refresh")...)
	if refreshed.Exit == 0 {
		t.Fatalf("--refresh against a broken type endpoint succeeded — it served the cache it promised to ignore\nstdout: %s", refreshed.Stdout)
	}
	after := mx3Run(t, home, baseURL, argv...)
	if after.Exit == 0 {
		t.Fatalf("plain create AFTER the failed --refresh succeeded — the stale entry was bypassed, not forgotten\nstdout: %s", after.Stdout)
	}
	Prove(t, "jira issue link create", "refresh",
		"a failed --refresh left the next plain create failing too: the type-list cache entry was forgotten, not bypassed (a cached create against the same broken endpoint succeeded just before)")
}

// --ttl-minutes on link create widens how old a cached type list may be.
func TestLinkCreateTTLMinutesWidensAcceptance(t *testing.T) {
	rec := &mx3Recorder{}
	var mu sync.Mutex
	broken := false
	baseURL := mx3Stub(t, mx3LinkHandler(rec, &broken, &mu))
	home := t.TempDir()
	argv := []string{"issue", "link", "create", "KEY-123", "--to", "KEY-456", "--type", "blocks", "--output=json"}

	if res := mx3Run(t, home, baseURL, argv...); res.Exit != 0 {
		t.Fatalf("warm-up create: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	mx3AgeCacheEntries(t, home, 25*time.Hour)
	mu.Lock()
	broken = true
	mu.Unlock()

	expired := mx3Run(t, home, baseURL, argv...)
	if expired.Exit == 0 {
		t.Fatalf("default TTL accepted a 25h-old type list — the aged entry should have been a miss\nstdout: %s", expired.Stdout)
	}
	widened := mx3Run(t, home, baseURL, append(argv, "--ttl-minutes", "2880")...)
	if widened.Exit != 0 {
		t.Fatalf("--ttl-minutes 2880 rejected a 25h-old type list it should accept: exit %d\nstderr: %s", widened.Exit, widened.Stderr)
	}
	Prove(t, "jira issue link create", "ttl-minutes",
		"a 25h-old type-list cache entry missed under the 24h default (create failed on the broken endpoint) and hit under --ttl-minutes 2880 (create succeeded)")
}

// link delete --dry-run composes and stops: zero requests, dry_run true.
func TestLinkDeleteDryRunContactsNothing(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "link", "delete", "10042", "--dry-run", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("link delete --dry-run: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	mx3RequireNoRequests(t, rec)
	data := mx3Data(t, res)
	if data["dry_run"] != true || data["link_id"] != "10042" {
		t.Fatalf("data = %v, want dry_run=true and link_id=10042", data)
	}
	Prove(t, "jira issue link delete", "dry-run",
		"stub saw zero requests (an ignored flag would have sent the DELETE) and the envelope published dry_run=true")
}

func TestLinkListParallelismRefusedOutOfRange(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "link", "list", "KEY-123",
		"--parallelism", "99", "--output=json")
	mx3RequireRefusal(t, res, "flag_value_invalid", "parallelism")
	mx3RequireNoRequests(t, rec)
	Prove(t, "jira issue link list", "parallelism",
		"--parallelism 99 refused by code flag_value_invalid on field parallelism, before any request")
}

// link types --refresh: same forget-not-bypass contract as the board and
// create caches, observed through the command that SHOWS the list.
func TestLinkTypesRefreshForgetsCacheEntry(t *testing.T) {
	rec := &mx3Recorder{}
	var mu sync.Mutex
	broken := false
	baseURL := mx3Stub(t, mx3LinkHandler(rec, &broken, &mu))
	setBroken := func(v bool) { mu.Lock(); broken = v; mu.Unlock() }
	home := t.TempDir()
	argv := []string{"issue", "link", "types", "--output=json"}

	if res := mx3Run(t, home, baseURL, argv...); res.Exit != 0 {
		t.Fatalf("warm-up types read: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	setBroken(true)
	cached := mx3Run(t, home, baseURL, argv...)
	if cached.Exit != 0 {
		t.Fatalf("cached types read against a broken endpoint: exit %d — the cache is not serving\nstderr: %s", cached.Exit, cached.Stderr)
	}
	if data := mx3Data(t, cached); data["cached"] != true {
		t.Fatalf("data.cached = %v, want true on the served-from-disk read", data["cached"])
	}
	refreshed := mx3Run(t, home, baseURL, append(argv, "--refresh")...)
	if refreshed.Exit == 0 {
		t.Fatalf("--refresh against a broken endpoint succeeded — it served the cache it promised to ignore\nstdout: %s", refreshed.Stdout)
	}
	after := mx3Run(t, home, baseURL, argv...)
	if after.Exit == 0 {
		t.Fatalf("plain read AFTER the failed --refresh succeeded — the stale entry was bypassed, not forgotten\nstdout: %s", after.Stdout)
	}
	Prove(t, "jira issue link types", "refresh",
		"a failed --refresh left the next plain read failing too: the entry was forgotten, not bypassed (the run before it served from cache with data.cached=true)")
}

func TestLinkTypesTTLMinutesWidensAcceptance(t *testing.T) {
	rec := &mx3Recorder{}
	var mu sync.Mutex
	broken := false
	baseURL := mx3Stub(t, mx3LinkHandler(rec, &broken, &mu))
	home := t.TempDir()
	argv := []string{"issue", "link", "types", "--output=json"}

	if res := mx3Run(t, home, baseURL, argv...); res.Exit != 0 {
		t.Fatalf("warm-up types read: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	mx3AgeCacheEntries(t, home, 25*time.Hour)
	mu.Lock()
	broken = true
	mu.Unlock()

	expired := mx3Run(t, home, baseURL, argv...)
	if expired.Exit == 0 {
		t.Fatalf("default TTL accepted a 25h-old list — the aged entry should have been a miss\nstdout: %s", expired.Stdout)
	}
	widened := mx3Run(t, home, baseURL, append(argv, "--ttl-minutes", "2880")...)
	if widened.Exit != 0 {
		t.Fatalf("--ttl-minutes 2880 rejected a 25h-old list it should accept: exit %d\nstderr: %s", widened.Exit, widened.Stderr)
	}
	if data := mx3Data(t, widened); data["cached"] != true {
		t.Fatalf("data.cached = %v, want true when the widened TTL served the aged entry", data["cached"])
	}
	Prove(t, "jira issue link types", "ttl-minutes",
		"a 25h-old entry missed under the 24h default (read failed on the broken endpoint) and hit under --ttl-minutes 2880 (read succeeded, data.cached=true)")
}

// mx3RemoteLinkBody is the weblink endpoint's request shape.
type mx3RemoteLinkBody struct {
	Object struct {
		URL     string `json:"url"`
		Title   string `json:"title"`
		Summary string `json:"summary"`
	} `json:"object"`
	GlobalID     string `json:"globalId"`
	Relationship string `json:"relationship"`
}

// weblink add: every field flag lands in its own member of the one
// request body, so one capture proves each flag by its own member.
func TestWeblinkAddFieldFlagsComposeRequest(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		if req.Method != http.MethodPost || req.Path != mx3RemoteLinkPath {
			http.Error(w, `{"errorMessages":["unexpected request"]}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"id":10001,"self":"https://example.invalid/remotelink/10001"}`)
	}))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "weblink", "add", "KEY-123",
		"--url", "https://example.com/runbook", "--title", "Deploy runbook",
		"--summary", "Says how.", "--relationship", "is documented by",
		"--global-id", "ci-build-7", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("weblink add: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	posts := rec.byPath(mx3RemoteLinkPath)
	if len(posts) != 1 {
		t.Fatalf("remotelink writes = %d, want 1", len(posts))
	}
	var body mx3RemoteLinkBody
	if err := json.Unmarshal(posts[0].Body, &body); err != nil {
		t.Fatalf("decode remotelink body: %v\nbody: %s", err, posts[0].Body)
	}
	if body.Object.URL != "https://example.com/runbook" {
		t.Fatalf("object.url = %q, want the --url value", body.Object.URL)
	}
	if body.Object.Title != "Deploy runbook" {
		t.Fatalf("object.title = %q, want the --title value", body.Object.Title)
	}
	if body.Object.Summary != "Says how." {
		t.Fatalf("object.summary = %q, want the --summary value", body.Object.Summary)
	}
	if body.Relationship != "is documented by" {
		t.Fatalf("relationship = %q, want the --relationship value", body.Relationship)
	}
	if body.GlobalID != "ci-build-7" {
		t.Fatalf("globalId = %q, want the --global-id value", body.GlobalID)
	}

	Prove(t, "jira issue weblink add", "url", "stub remotelink body carried object.url=https://example.com/runbook")
	Prove(t, "jira issue weblink add", "title", "stub remotelink body carried object.title=Deploy runbook")
	Prove(t, "jira issue weblink add", "summary", "stub remotelink body carried object.summary=Says how.")
	Prove(t, "jira issue weblink add", "relationship", "stub remotelink body carried relationship=is documented by")
	Prove(t, "jira issue weblink add", "global-id", "stub remotelink body carried globalId=ci-build-7, the create-or-replace key")
}

func TestWeblinkAddDryRunContactsNothing(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "weblink", "add", "KEY-123",
		"--url", "https://example.com", "--title", "Example", "--dry-run", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("weblink add --dry-run: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	mx3RequireNoRequests(t, rec)
	if data := mx3Data(t, res); data["dry_run"] != true {
		t.Fatalf("data.dry_run = %v, want true", data["dry_run"])
	}
	Prove(t, "jira issue weblink add", "dry-run",
		"stub saw zero requests (an ignored flag would have POSTed the link) and the envelope published dry_run=true")
}

func TestWeblinkAddParallelismRefusedOutOfRange(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "weblink", "add", "KEY-123",
		"--url", "https://example.com", "--title", "Example", "--parallelism", "0", "--output=json")
	mx3RequireRefusal(t, res, "flag_value_invalid", "parallelism")
	mx3RequireNoRequests(t, rec)
	Prove(t, "jira issue weblink add", "parallelism",
		"--parallelism 0 refused by code flag_value_invalid on field parallelism, before any request")
}

func TestWeblinkListParallelismRefusedOutOfRange(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "weblink", "list", "KEY-123",
		"--parallelism", "99", "--output=json")
	mx3RequireRefusal(t, res, "flag_value_invalid", "parallelism")
	mx3RequireNoRequests(t, rec)
	Prove(t, "jira issue weblink list", "parallelism",
		"--parallelism 99 refused by code flag_value_invalid on field parallelism, before any request")
}

func TestWeblinkDeleteDryRunContactsNothing(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "weblink", "delete", "KEY-123", "10042", "--dry-run", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("weblink delete --dry-run: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	mx3RequireNoRequests(t, rec)
	data := mx3Data(t, res)
	if data["dry_run"] != true || data["weblink_id"] != "10042" {
		t.Fatalf("data = %v, want dry_run=true and weblink_id=10042", data)
	}
	Prove(t, "jira issue weblink delete", "dry-run",
		"stub saw zero requests (an ignored flag would have sent the DELETE) and the envelope published dry_run=true")
}
