//go:build flagmatrix

package matrix

// Phase-2 cases for the `jira issue comment` cluster, plus the shared
// harness the watchers and view files in this cluster reuse.
//
// Every case here runs the REAL binary against a loopback stub and
// asserts the flag's observable effect — the request the CLI made, the
// envelope it published, or its own refusal by taxonomy code — never
// merely that the process exited 0. The helper names are cw-prefixed
// (comment/watchers) so a sibling cluster's file cannot collide with
// them when the case files land together.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------
// Binary
// ---------------------------------------------------------------------

var (
	cwBinOnce sync.Once
	cwBinPath string
	cwBinErr  error
)

// cwBinary compiles the real binary exactly once per test process —
// tests/audit/corpus.go's jiraBinary shape, for its reason (a per-test
// build would dominate suite wall time). The temp dir is left for the
// OS to reap: this package's TestMain is the harness's and cannot host
// a cleanup hook.
func cwBinary(t *testing.T) string {
	t.Helper()
	cwBinOnce.Do(func() {
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			cwBinErr = err
			return
		}
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
			cwBinErr = fmt.Errorf("resolve repo root: %s has no go.mod: %w", root, err)
			return
		}
		dir, err := os.MkdirTemp("", "jira-flagmatrix-cw-")
		if err != nil {
			cwBinErr = err
			return
		}
		cwBinPath = filepath.Join(dir, "jira")
		if runtime.GOOS == "windows" {
			cwBinPath += ".exe"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", cwBinPath, "./cmd/jira")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			cwBinErr = fmt.Errorf("build jira binary: %w\n%s", err, out)
		}
	})
	if cwBinErr != nil {
		t.Fatalf("flagmatrix cw: %v", cwBinErr)
	}
	return cwBinPath
}

// ---------------------------------------------------------------------
// Stub server
// ---------------------------------------------------------------------

// cwAccountID is what the stub's /myself answers — the resolution target
// the @me cases assert arrived in the envelope.
const cwAccountID = "5b10a2844c20165700ede21g"

// cwStoredComment is the comment the stub returns for a successful POST
// or PUT — the adapter demands a decodable comment on both.
const cwStoredComment = `{"id":"10042","created":"2026-01-02T03:04:05.000+0000","updated":"2026-01-02T03:04:06.000+0000","author":{"accountId":"acc-1","displayName":"Stub Author"},"body":{"type":"doc","version":1,"content":[]}}`

// cwWatchersJSON is the stub's GET /watchers answer, used both by the
// list read and the write verbs' readback.
const cwWatchersJSON = `{"isWatching":true,"watchCount":2,"watchers":[{"accountId":"acc-1","displayName":"Watcher One","active":true,"accountType":"atlassian"},{"accountId":"acc-2","displayName":"Watcher Two","active":true,"accountType":"atlassian"}]}`

// cwIssueJSON is the stub's GET /issue/{key} answer: enough fields for
// the human renderer, and deliberately NO embedded comment field, so
// `issue view --comments N` must fall back to its dedicated fetch —
// which is the request that case asserts.
const cwIssueJSON = `{"id":"10000","key":"KEY-123","self":"https://stub.invalid/rest/api/3/issue/10000","fields":{"summary":"A summary","status":{"name":"To Do"},"issuetype":{"name":"Task"},"created":"2026-01-01T00:00:00.000+0000","updated":"2026-01-01T00:00:00.000+0000"}}`

// cwStubConfig parameterizes the one generic Jira stub.
type cwStubConfig struct {
	// userSearch is the first native user-query page; the next page is empty.
	userSearch []string
	// searchKeys is what POST /search/jql answers, whatever the query.
	searchKeys []string
	// comments is the thread GET .../comment pages through, one raw
	// comment JSON object per entry.
	comments []string
	// pageCap bounds how many comments one page returns regardless of
	// the requested maxResults; 0 means no cap. The --all case needs it
	// to force a second page out of a three-comment thread.
	pageCap int
	// filterJQL is what GET /filter/{id} carries.
	filterJQL string
}

type cwReq struct {
	Method string
	Path   string
	Query  url.Values
	Body   []byte
}

type cwStub struct {
	cfg  cwStubConfig
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []cwReq
}

func newCWStub(t *testing.T, cfg cwStubConfig) *cwStub {
	t.Helper()
	s := &cwStub{cfg: cfg}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *cwStub) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.reqs = append(s.reqs, cwReq{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Body: body})
	s.mu.Unlock()

	p := r.URL.Path
	switch {
	case p == "/rest/api/3/myself" && r.Method == http.MethodGet:
		cwWriteJSON(w, http.StatusOK, `{"accountId":"`+cwAccountID+`","displayName":"Stub Account","active":true}`)
	case p == "/rest/api/3/user/search" && r.Method == http.MethodGet:
		users := s.cfg.userSearch
		if r.URL.Query().Get("startAt") != "0" {
			users = nil
		}
		cwWriteJSON(w, http.StatusOK, `[`+strings.Join(users, ",")+`]`)
	case p == "/rest/api/3/search/jql" && r.Method == http.MethodPost:
		issues := make([]string, 0, len(s.cfg.searchKeys))
		for i, key := range s.cfg.searchKeys {
			issues = append(issues, fmt.Sprintf(`{"id":"%d","key":%q}`, i+1, key))
		}
		cwWriteJSON(w, http.StatusOK, `{"issues":[`+strings.Join(issues, ",")+`],"isLast":true}`)
	case strings.HasPrefix(p, "/rest/api/3/filter/") && r.Method == http.MethodGet:
		cwWriteJSON(w, http.StatusOK, fmt.Sprintf(`{"id":"10001","jql":%q}`, s.cfg.filterJQL))
	case strings.Contains(p, "/comment/") && r.Method == http.MethodPut:
		cwWriteJSON(w, http.StatusOK, cwStoredComment)
	case strings.Contains(p, "/comment/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	case strings.HasSuffix(p, "/comment") && r.Method == http.MethodPost:
		cwWriteJSON(w, http.StatusCreated, cwStoredComment)
	case strings.HasSuffix(p, "/comment") && r.Method == http.MethodGet:
		s.serveCommentPage(w, r)
	case strings.HasSuffix(p, "/watchers") && r.Method == http.MethodGet:
		cwWriteJSON(w, http.StatusOK, cwWatchersJSON)
	case strings.HasSuffix(p, "/watchers") && (r.Method == http.MethodPost || r.Method == http.MethodDelete):
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(p, "/rest/api/3/issue/") && r.Method == http.MethodGet:
		cwWriteJSON(w, http.StatusOK, cwIssueJSON)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// serveCommentPage pages cfg.comments by startAt/maxResults, honoring
// pageCap, so both a plain `comment list` page and --all's walk are
// served by one honest offset implementation.
func (s *cwStub) serveCommentPage(w http.ResponseWriter, r *http.Request) {
	start, _ := strconv.Atoi(r.URL.Query().Get("startAt"))
	maxResults, _ := strconv.Atoi(r.URL.Query().Get("maxResults"))
	if s.cfg.pageCap > 0 && maxResults > s.cfg.pageCap {
		maxResults = s.cfg.pageCap
	}
	total := len(s.cfg.comments)
	end := start + maxResults
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	page := s.cfg.comments[start:end]
	cwWriteJSON(w, http.StatusOK, fmt.Sprintf(`{"startAt":%d,"maxResults":%d,"total":%d,"comments":[%s]}`,
		start, maxResults, total, strings.Join(page, ",")))
}

func cwWriteJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// requests snapshots everything the stub has seen.
func (s *cwStub) requests() []cwReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]cwReq, len(s.reqs))
	copy(out, s.reqs)
	return out
}

// matching returns the recorded requests with the given method whose
// path ends in pathSuffix.
func (s *cwStub) matching(method, pathSuffix string) []cwReq {
	var out []cwReq
	for _, r := range s.requests() {
		if r.Method == method && strings.HasSuffix(r.Path, pathSuffix) {
			out = append(out, r)
		}
	}
	return out
}

// cwComment builds one raw comment JSON object for the stub's thread.
func cwComment(id, text string) string {
	return fmt.Sprintf(`{"id":%q,"created":"2026-01-02T03:04:05.000+0000","updated":"2026-01-02T03:04:05.000+0000","author":{"accountId":"acc-1","displayName":"Stub Author"},"body":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":%q}]}]}}`, id, text)
}

// ---------------------------------------------------------------------
// Runner
// ---------------------------------------------------------------------

type cwResult struct {
	Stdout string
	Stderr string
	Exit   int
}

// cwRun executes the real binary in a fresh workdir with the scrubbed
// environment tests/audit/corpus.go's scenarioEnv established — the
// allowlist, the unroutable proxy, JIRA_KEYRING=false — plus the stub's
// own JIRA_BASE_URL. base_url comes from the keyring on a developer
// machine, ignoring HOME/XDG, so the explicit env pin is what keeps
// every invocation off the network.
func cwRun(t *testing.T, stub *cwStub, dir string, args ...string) cwResult {
	t.Helper()
	bin := cwBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"TMPDIR=" + dir,
		"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(dir, "cache"),
		"HTTP_PROXY=http://127.0.0.1:1",
		"HTTPS_PROXY=http://127.0.0.1:1",
		"NO_PROXY=127.0.0.1,localhost",
		"COLUMNS=100",
		"TERM=dumb",
		"NO_COLOR=1",
		"JIRA_KEYRING=false",
		"DBUS_SESSION_BUS_ADDRESS=/dev/null",
		"XDG_RUNTIME_DIR=" + dir,
		"JIRA_EMAIL=person@example.com",
		"JIRA_API_TOKEN=flagmatrix-token",
		"JIRA_BASE_URL=" + stub.srv.URL,
	}
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env,
			"PATHEXT="+os.Getenv("PATHEXT"), "USERPROFILE="+dir,
			"APPDATA="+filepath.Join(dir, "AppData", "Roaming"),
			"LOCALAPPDATA="+filepath.Join(dir, "AppData", "Local"))
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("run %v: %v\nstderr: %s", args, err, stderr.String())
		}
		code = exit.ExitCode()
	}
	return cwResult{Stdout: stdout.String(), Stderr: stderr.String(), Exit: code}
}

// ---------------------------------------------------------------------
// Envelope parsing
// ---------------------------------------------------------------------

type cwEnvelope struct {
	OK     bool            `json:"ok"`
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Code  string `json:"code"`
		Field string `json:"field"`
	} `json:"errors"`
	Warnings []struct {
		Type string `json:"type"`
	} `json:"warnings"`
}

func cwParse(t *testing.T, res cwResult) cwEnvelope {
	t.Helper()
	var env cwEnvelope
	if err := json.Unmarshal([]byte(res.Stdout), &env); err != nil {
		t.Fatalf("stdout is not one JSON envelope: %v\nstdout: %s\nstderr: %s", err, res.Stdout, res.Stderr)
	}
	return env
}

// cwDataMap decodes the envelope's data object for structured asserts.
func cwDataMap(t *testing.T, env cwEnvelope) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(env.Data, &m); err != nil {
		t.Fatalf("envelope data is not an object: %v\ndata: %s", err, env.Data)
	}
	return m
}

// cwRequireCode asserts a refusal by taxonomy code and field — never by
// message text.
func cwRequireCode(t *testing.T, res cwResult, code, field string) {
	t.Helper()
	env := cwParse(t, res)
	if env.OK || res.Exit == 0 {
		t.Fatalf("expected a refusal, got ok=%t exit=%d\nstdout: %s", env.OK, res.Exit, res.Stdout)
	}
	if len(env.Errors) == 0 {
		t.Fatalf("refusal carries no errors[]\nstdout: %s", res.Stdout)
	}
	if env.Errors[0].Code != code {
		t.Fatalf("refusal code = %q, want %q\nstdout: %s", env.Errors[0].Code, code, res.Stdout)
	}
	if field != "" && env.Errors[0].Field != field {
		t.Fatalf("refusal field = %q, want %q\nstdout: %s", env.Errors[0].Field, field, res.Stdout)
	}
}

// cwRequireOK asserts the run succeeded, with the envelope printed on
// failure so a broken stub reads as itself rather than as the flag.
func cwRequireOK(t *testing.T, res cwResult) cwEnvelope {
	t.Helper()
	env := cwParse(t, res)
	if !env.OK || res.Exit != 0 {
		t.Fatalf("expected success, got ok=%t exit=%d\nstdout: %s\nstderr: %s", env.OK, res.Exit, res.Stdout, res.Stderr)
	}
	return env
}

// cwADFDoc is a minimal valid ADF document carrying text, for the
// --json-input route and its aliases.
func cwADFDoc(text string) string {
	return fmt.Sprintf(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":%q}]}]}`, text)
}

// ---------------------------------------------------------------------
// jira issue comment add
// ---------------------------------------------------------------------

func TestCommentAddMarkdownPostsConvertedBody(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "add", "KEY-123",
		"--markdown", "markdown route text", "--output=json")
	cwRequireOK(t, res)
	posts := stub.matching(http.MethodPost, "/issue/KEY-123/comment")
	if len(posts) != 1 {
		t.Fatalf("comment POSTs to KEY-123 = %d, want 1", len(posts))
	}
	if !strings.Contains(string(posts[0].Body), `"markdown route text"`) {
		t.Fatalf("POST body does not carry the --markdown text as ADF: %s", posts[0].Body)
	}
	Prove(t, "jira issue comment add", "markdown",
		"stub saw POST /issue/KEY-123/comment whose ADF body carries the --markdown text")
}

func TestCommentAddMarkdownFileReadsFile(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte("file route text"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := cwRun(t, stub, dir, "issue", "comment", "add", "KEY-123",
		"--markdown-file", "note.md", "--output=json")
	cwRequireOK(t, res)
	posts := stub.matching(http.MethodPost, "/issue/KEY-123/comment")
	if len(posts) != 1 || !strings.Contains(string(posts[0].Body), `"file route text"`) {
		t.Fatalf("POST body does not carry the file's text: %d posts, body %s", len(posts), postsBody(posts))
	}
	Prove(t, "jira issue comment add", "markdown-file",
		"stub saw POST /issue/KEY-123/comment whose ADF body carries the text read from the --markdown-file path")
}

func postsBody(posts []cwReq) string {
	if len(posts) == 0 {
		return "<none>"
	}
	return string(posts[0].Body)
}

func TestCommentAddJSONInputSendsADFVerbatim(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "body.json"), []byte(cwADFDoc("adf route payload")), 0o600); err != nil {
		t.Fatal(err)
	}
	res := cwRun(t, stub, dir, "issue", "comment", "add", "KEY-123",
		"--json-input", "body.json", "--output=json")
	env := cwRequireOK(t, res)
	posts := stub.matching(http.MethodPost, "/issue/KEY-123/comment")
	if len(posts) != 1 || !strings.Contains(string(posts[0].Body), `"adf route payload"`) {
		t.Fatalf("POST body does not carry the ADF file's text: %d posts, body %s", len(posts), postsBody(posts))
	}
	if data := cwDataMap(t, env); data["body_route"] != "adf" {
		t.Fatalf("body_route = %v, want adf", data["body_route"])
	}
	Prove(t, "jira issue comment add", "json-input",
		"stub saw POST /issue/KEY-123/comment carrying the ADF file's own text, and the envelope reports body_route=adf")
}

// TestCommentAddBodyAliases proves the seven hidden body aliases by
// their dry-run envelope: each alias must fold into its canonical route
// and surface the given content at data.body with the right body_route.
// An ignored alias fails as flag_required (no body route chosen) or as
// an unknown flag, either of which breaks the assertions here.
func TestCommentAddBodyAliases(t *testing.T) {
	cases := []struct {
		flag  string
		route string
		file  string // file content; empty means the value is inline text
	}{
		{flag: "body", route: "markdown"},
		{flag: "description", route: "markdown"},
		{flag: "body-file", route: "markdown", file: "markdown text"},
		{flag: "description-file", route: "markdown", file: "markdown text"},
		{flag: "template", route: "markdown", file: "markdown text"},
		{flag: "from-json", route: "adf", file: "adf"},
		{flag: "body-adf", route: "adf", file: "adf"},
	}
	for _, tc := range cases {
		t.Run(tc.flag, func(t *testing.T) {
			stub := newCWStub(t, cwStubConfig{})
			dir := t.TempDir()
			text := "alias " + tc.flag + " text"
			value := text
			if tc.file != "" {
				value = "input-" + tc.flag + ".txt"
				content := text
				if tc.file == "adf" {
					content = cwADFDoc(text)
				}
				if err := os.WriteFile(filepath.Join(dir, value), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			res := cwRun(t, stub, dir, "issue", "comment", "add", "KEY-123",
				"--"+tc.flag, value, "--dry-run", "--output=json")
			env := cwRequireOK(t, res)
			data := cwDataMap(t, env)
			if data["dry_run"] != true {
				t.Fatalf("dry_run = %v, want true", data["dry_run"])
			}
			if data["body_route"] != tc.route {
				t.Fatalf("body_route = %v, want %q", data["body_route"], tc.route)
			}
			if !strings.Contains(string(env.Data), fmt.Sprintf("%q", text)) {
				t.Fatalf("data.body does not carry the alias's content %q: %s", text, env.Data)
			}
			Prove(t, "jira issue comment add", tc.flag, fmt.Sprintf(
				"dry-run envelope carries the --%s content at data.body with body_route=%s, so the hidden alias folded into its canonical route", tc.flag, tc.route))
		})
	}
}

func TestCommentAddDryRunSendsNothing(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "add", "KEY-123",
		"--markdown", "preview only", "--dry-run", "--output=json")
	env := cwRequireOK(t, res)
	if data := cwDataMap(t, env); data["dry_run"] != true {
		t.Fatalf("dry_run = %v, want true", data["dry_run"])
	}
	if reqs := stub.requests(); len(reqs) != 0 {
		t.Fatalf("dry run contacted the stub %d times: %+v", len(reqs), reqs)
	}
	Prove(t, "jira issue comment add", "dry-run",
		"with --dry-run the stub saw ZERO requests and the envelope reports dry_run=true; a live run posts")
}

func TestCommentAddVisibilityGroupRidesRequest(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "add", "KEY-123",
		"--markdown", "restricted", "--visibility-group", "developers", "--output=json")
	cwRequireOK(t, res)
	posts := stub.matching(http.MethodPost, "/issue/KEY-123/comment")
	if len(posts) != 1 || !strings.Contains(string(posts[0].Body), `"visibility":{"type":"group","value":"developers"}`) {
		t.Fatalf("POST body does not carry the group visibility: %s", postsBody(posts))
	}
	Prove(t, "jira issue comment add", "visibility-group",
		`stub saw the POST body carry "visibility":{"type":"group","value":"developers"}`)
}

func TestCommentAddVisibilityRoleRidesRequest(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "add", "KEY-123",
		"--markdown", "restricted", "--visibility-role", "Administrators", "--output=json")
	cwRequireOK(t, res)
	posts := stub.matching(http.MethodPost, "/issue/KEY-123/comment")
	if len(posts) != 1 || !strings.Contains(string(posts[0].Body), `"visibility":{"type":"role","value":"Administrators"}`) {
		t.Fatalf("POST body does not carry the role visibility: %s", postsBody(posts))
	}
	Prove(t, "jira issue comment add", "visibility-role",
		`stub saw the POST body carry "visibility":{"type":"role","value":"Administrators"}`)
}

func TestCommentAddJQLSelectsTargets(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{searchKeys: []string{"KEY-123", "KEY-124"}})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "add",
		"--jql", "project = KEY AND sprint = 12", "--markdown", "bulk text", "--force", "--output=json")
	cwRequireOK(t, res)
	searches := stub.matching(http.MethodPost, "/search/jql")
	if len(searches) != 1 || !strings.Contains(string(searches[0].Body), "project = KEY AND sprint = 12") {
		t.Fatalf("search request does not carry the --jql query: %d searches", len(searches))
	}
	for _, key := range []string{"KEY-123", "KEY-124"} {
		if n := len(stub.matching(http.MethodPost, "/issue/"+key+"/comment")); n != 1 {
			t.Fatalf("comment POSTs to %s = %d, want 1", key, n)
		}
	}
	Prove(t, "jira issue comment add", "jql",
		"stub saw POST /search/jql carrying the --jql query, then a comment POST to each key the search returned")
}

func TestCommentAddFilterResolvesToQuery(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{searchKeys: []string{"KEY-123"}, filterJQL: "project = KEY"})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "add",
		"--filter", "10001", "--markdown", "filtered", "--force", "--output=json")
	cwRequireOK(t, res)
	if n := len(stub.matching(http.MethodGet, "/filter/10001")); n != 1 {
		t.Fatalf("GET /filter/10001 count = %d, want 1", n)
	}
	searches := stub.matching(http.MethodPost, "/search/jql")
	if len(searches) != 1 || !strings.Contains(string(searches[0].Body), "project = KEY") {
		t.Fatalf("search does not carry the filter's own JQL: %d searches", len(searches))
	}
	Prove(t, "jira issue comment add", "filter",
		"stub saw GET /rest/api/3/filter/10001, then a search carrying that filter's stored JQL")
}

func TestCommentAddFromFileNamesTargets(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keys.txt"), []byte("KEY-201\nKEY-202\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := cwRun(t, stub, dir, "issue", "comment", "add",
		"--from-file", "keys.txt", "--markdown", "from a file", "--force", "--output=json")
	cwRequireOK(t, res)
	for _, key := range []string{"KEY-201", "KEY-202"} {
		if n := len(stub.matching(http.MethodPost, "/issue/"+key+"/comment")); n != 1 {
			t.Fatalf("comment POSTs to %s = %d, want 1", key, n)
		}
	}
	Prove(t, "jira issue comment add", "from-file",
		"stub saw comment POSTs to exactly the keys named only inside the --from-file list")
}

func TestCommentAddForceGatesSelectorWrite(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{searchKeys: []string{"KEY-123"}})
	refused := cwRun(t, stub, t.TempDir(), "issue", "comment", "add",
		"--jql", "project = KEY", "--markdown", "gated", "--output=json")
	cwRequireCode(t, refused, "flag_required", "force")
	if n := len(stub.matching(http.MethodPost, "/issue/KEY-123/comment")); n != 0 {
		t.Fatalf("refused run still posted %d comments", n)
	}
	allowed := cwRun(t, stub, t.TempDir(), "issue", "comment", "add",
		"--jql", "project = KEY", "--markdown", "gated", "--force", "--output=json")
	cwRequireOK(t, allowed)
	if n := len(stub.matching(http.MethodPost, "/issue/KEY-123/comment")); n != 1 {
		t.Fatalf("forced run posted %d comments, want 1", n)
	}
	Prove(t, "jira issue comment add", "force",
		"without --force the non-interactive selector write refuses with flag_required (field force) and the stub sees no POST; with it the POST lands")
}

func TestCommentAddYesAliasConfirms(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{searchKeys: []string{"KEY-123"}})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "add",
		"--jql", "project = KEY", "--markdown", "via yes", "--yes", "--output=json")
	cwRequireOK(t, res)
	if n := len(stub.matching(http.MethodPost, "/issue/KEY-123/comment")); n != 1 {
		t.Fatalf("--yes run posted %d comments, want 1", n)
	}
	Prove(t, "jira issue comment add", "yes",
		"the hidden --yes confirmed the non-interactive selector write up front: the stub saw the comment POST that is refused without it")
}

func TestCommentAddValidateRemoteResolvesPreview(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{searchKeys: []string{"KEY-123"}})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "add",
		"--jql", "project = KEY", "--markdown", "preview", "--dry-run", "--validate-remote", "--output=json")
	env := cwRequireOK(t, res)
	if n := len(stub.matching(http.MethodPost, "/search/jql")); n != 1 {
		t.Fatalf("validate-remote preview made %d search requests, want 1", n)
	}
	if n := len(stub.matching(http.MethodPost, "/issue/KEY-123/comment")); n != 0 {
		t.Fatalf("preview still posted %d comments", n)
	}
	data := string(env.Data)
	if !strings.Contains(data, `"resolved":true`) || !strings.Contains(data, "KEY-123") {
		t.Fatalf("preview did not resolve the selector to its keys: %s", data)
	}
	Prove(t, "jira issue comment add", "validate-remote",
		"a --dry-run preview with --validate-remote made the search request (a bare preview makes none), listed KEY-123, and still posted nothing")
}

func TestCommentAddParallelismRange(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "add", "KEY-123",
		"--markdown", "x", "--parallelism", "99", "--output=json")
	cwRequireCode(t, res, "flag_value_invalid", "parallelism")
	if n := len(stub.requests()); n != 0 {
		t.Fatalf("refused run still contacted the stub %d times", n)
	}
	Prove(t, "jira issue comment add", "parallelism",
		"--parallelism 99 is refused with flag_value_invalid (field parallelism) before any request; the range check reads the flag's value")
}

// ---------------------------------------------------------------------
// jira issue comment edit
// ---------------------------------------------------------------------

func TestCommentEditMarkdownPutsBody(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "edit", "KEY-123", "10042",
		"--markdown", "edited text", "--output=json")
	cwRequireOK(t, res)
	puts := stub.matching(http.MethodPut, "/issue/KEY-123/comment/10042")
	if len(puts) != 1 || !strings.Contains(string(puts[0].Body), `"edited text"`) {
		t.Fatalf("PUT body does not carry the --markdown text: %s", postsBody(puts))
	}
	Prove(t, "jira issue comment edit", "markdown",
		"stub saw PUT /issue/KEY-123/comment/10042 whose ADF body carries the --markdown text")
}

// TestCommentEditBodyRoutes proves the file/ADF routes and the seven
// hidden aliases on `comment edit` by their dry-run envelope, exactly
// as TestCommentAddBodyAliases does for `comment add`.
func TestCommentEditBodyRoutes(t *testing.T) {
	cases := []struct {
		flag  string
		route string
		file  string
	}{
		{flag: "markdown-file", route: "markdown", file: "markdown text"},
		{flag: "json-input", route: "adf", file: "adf"},
		{flag: "body", route: "markdown"},
		{flag: "description", route: "markdown"},
		{flag: "body-file", route: "markdown", file: "markdown text"},
		{flag: "description-file", route: "markdown", file: "markdown text"},
		{flag: "template", route: "markdown", file: "markdown text"},
		{flag: "from-json", route: "adf", file: "adf"},
		{flag: "body-adf", route: "adf", file: "adf"},
	}
	for _, tc := range cases {
		t.Run(tc.flag, func(t *testing.T) {
			stub := newCWStub(t, cwStubConfig{})
			dir := t.TempDir()
			text := "edit route " + tc.flag + " text"
			value := text
			if tc.file != "" {
				value = "input-" + tc.flag + ".txt"
				content := text
				if tc.file == "adf" {
					content = cwADFDoc(text)
				}
				if err := os.WriteFile(filepath.Join(dir, value), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			res := cwRun(t, stub, dir, "issue", "comment", "edit", "KEY-123", "10042",
				"--"+tc.flag, value, "--dry-run", "--output=json")
			env := cwRequireOK(t, res)
			data := cwDataMap(t, env)
			if data["dry_run"] != true || data["body_route"] != tc.route {
				t.Fatalf("dry_run=%v body_route=%v, want true/%q", data["dry_run"], data["body_route"], tc.route)
			}
			if !strings.Contains(string(env.Data), fmt.Sprintf("%q", text)) {
				t.Fatalf("data.body does not carry --%s's content %q: %s", tc.flag, text, env.Data)
			}
			Prove(t, "jira issue comment edit", tc.flag, fmt.Sprintf(
				"dry-run envelope carries the --%s content at data.body with body_route=%s", tc.flag, tc.route))
		})
	}
}

func TestCommentEditDryRunSendsNothing(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "edit", "KEY-123", "10042",
		"--markdown", "preview only", "--dry-run", "--output=json")
	env := cwRequireOK(t, res)
	if data := cwDataMap(t, env); data["dry_run"] != true {
		t.Fatalf("dry_run = %v, want true", data["dry_run"])
	}
	if reqs := stub.requests(); len(reqs) != 0 {
		t.Fatalf("dry run contacted the stub %d times: %+v", len(reqs), reqs)
	}
	Prove(t, "jira issue comment edit", "dry-run",
		"with --dry-run the stub saw ZERO requests and the envelope reports dry_run=true; a live run PUTs")
}

func TestCommentEditNotifyFalseRidesQuery(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "edit", "KEY-123", "10042",
		"--markdown", "quiet edit", "--notify=false", "--output=json")
	cwRequireOK(t, res)
	puts := stub.matching(http.MethodPut, "/issue/KEY-123/comment/10042")
	if len(puts) != 1 || puts[0].Query.Get("notifyUsers") != "false" {
		t.Fatalf("PUT does not carry notifyUsers=false: %d puts, query %v", len(puts), putsQuery(puts))
	}
	Prove(t, "jira issue comment edit", "notify",
		"stub saw the PUT carry ?notifyUsers=false for --notify=false; the default sends no such parameter")
}

func putsQuery(puts []cwReq) url.Values {
	if len(puts) == 0 {
		return nil
	}
	return puts[0].Query
}

func TestCommentEditSkipNotifyAlias(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "edit", "KEY-123", "10042",
		"--markdown", "quiet edit", "--skip-notify", "--output=json")
	cwRequireOK(t, res)
	puts := stub.matching(http.MethodPut, "/issue/KEY-123/comment/10042")
	if len(puts) != 1 || puts[0].Query.Get("notifyUsers") != "false" {
		t.Fatalf("PUT does not carry notifyUsers=false for --skip-notify: %d puts, query %v", len(puts), putsQuery(puts))
	}
	Prove(t, "jira issue comment edit", "skip-notify",
		"stub saw the PUT carry ?notifyUsers=false when the hidden --skip-notify was given")
}

func TestCommentEditClearVisibilitySendsNull(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "edit", "KEY-123", "10042",
		"--markdown", "now public", "--clear-visibility", "--output=json")
	cwRequireOK(t, res)
	puts := stub.matching(http.MethodPut, "/issue/KEY-123/comment/10042")
	if len(puts) != 1 || !strings.Contains(string(puts[0].Body), `"visibility":null`) {
		t.Fatalf("PUT body does not carry the explicit visibility null: %s", postsBody(puts))
	}
	Prove(t, "jira issue comment edit", "clear-visibility",
		`stub saw the PUT body carry "visibility":null — the explicit third state only --clear-visibility produces`)
}

func TestCommentEditVisibilityGroupRidesRequest(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "edit", "KEY-123", "10042",
		"--markdown", "internal", "--visibility-group", "developers", "--output=json")
	cwRequireOK(t, res)
	puts := stub.matching(http.MethodPut, "/issue/KEY-123/comment/10042")
	if len(puts) != 1 || !strings.Contains(string(puts[0].Body), `"visibility":{"type":"group","value":"developers"}`) {
		t.Fatalf("PUT body does not carry the group visibility: %s", postsBody(puts))
	}
	Prove(t, "jira issue comment edit", "visibility-group",
		`stub saw the PUT body carry "visibility":{"type":"group","value":"developers"}`)
}

func TestCommentEditVisibilityRoleRidesRequest(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "edit", "KEY-123", "10042",
		"--markdown", "internal", "--visibility-role", "Administrators", "--output=json")
	cwRequireOK(t, res)
	puts := stub.matching(http.MethodPut, "/issue/KEY-123/comment/10042")
	if len(puts) != 1 || !strings.Contains(string(puts[0].Body), `"visibility":{"type":"role","value":"Administrators"}`) {
		t.Fatalf("PUT body does not carry the role visibility: %s", postsBody(puts))
	}
	Prove(t, "jira issue comment edit", "visibility-role",
		`stub saw the PUT body carry "visibility":{"type":"role","value":"Administrators"}`)
}

// ---------------------------------------------------------------------
// jira issue comment delete
// ---------------------------------------------------------------------

func TestCommentDeleteDryRunSendsNothing(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "delete", "KEY-123", "10042",
		"--dry-run", "--output=json")
	env := cwRequireOK(t, res)
	if data := cwDataMap(t, env); data["dry_run"] != true {
		t.Fatalf("dry_run = %v, want true", data["dry_run"])
	}
	if reqs := stub.requests(); len(reqs) != 0 {
		t.Fatalf("dry run contacted the stub %d times: %+v", len(reqs), reqs)
	}
	Prove(t, "jira issue comment delete", "dry-run",
		"with --dry-run the stub saw ZERO requests (no DELETE, no confirmation) and the envelope reports dry_run=true")
}

func TestCommentDeleteForceGatesDestruction(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	refused := cwRun(t, stub, t.TempDir(), "issue", "comment", "delete", "KEY-123", "10042",
		"--output=json")
	cwRequireCode(t, refused, "flag_required", "force")
	if n := len(stub.matching(http.MethodDelete, "/comment/10042")); n != 0 {
		t.Fatalf("refused run still deleted: %d DELETEs", n)
	}
	allowed := cwRun(t, stub, t.TempDir(), "issue", "comment", "delete", "KEY-123", "10042",
		"--force", "--output=json")
	cwRequireOK(t, allowed)
	if n := len(stub.matching(http.MethodDelete, "/comment/10042")); n != 1 {
		t.Fatalf("forced run made %d DELETEs, want 1", n)
	}
	Prove(t, "jira issue comment delete", "force",
		"without --force the non-interactive delete refuses with flag_required (field force) and the stub sees no DELETE; with it the DELETE lands")
}

func TestCommentDeleteYesAliasConfirms(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "delete", "KEY-123", "10042",
		"--yes", "--output=json")
	cwRequireOK(t, res)
	if n := len(stub.matching(http.MethodDelete, "/comment/10042")); n != 1 {
		t.Fatalf("--yes run made %d DELETEs, want 1", n)
	}
	Prove(t, "jira issue comment delete", "yes",
		"the hidden --yes confirmed the non-interactive delete: the stub saw the DELETE that is refused without it")
}

// ---------------------------------------------------------------------
// jira issue comment list
// ---------------------------------------------------------------------

func TestCommentListLimitRidesQuery(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{comments: []string{cwComment("1", "first")}})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "list", "KEY-123",
		"--limit", "7", "--output=json")
	cwRequireOK(t, res)
	gets := stub.matching(http.MethodGet, "/issue/KEY-123/comment")
	if len(gets) != 1 || gets[0].Query.Get("maxResults") != "7" {
		t.Fatalf("GET does not carry maxResults=7: %d gets, query %v", len(gets), putsQuery(gets))
	}
	Prove(t, "jira issue comment list", "limit",
		"stub saw GET .../comment carry ?maxResults=7 for --limit 7 (the default asks for 50)")
}

func TestCommentListStartRidesQuery(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{comments: []string{cwComment("1", "first")}})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "list", "KEY-123",
		"--start", "3", "--output=json")
	cwRequireOK(t, res)
	gets := stub.matching(http.MethodGet, "/issue/KEY-123/comment")
	if len(gets) != 1 || gets[0].Query.Get("startAt") != "3" {
		t.Fatalf("GET does not carry startAt=3: %d gets, query %v", len(gets), putsQuery(gets))
	}
	Prove(t, "jira issue comment list", "start",
		"stub saw GET .../comment carry ?startAt=3 for --start 3 (the default asks for 0)")
}

func TestCommentListOrderRidesQuery(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{comments: []string{cwComment("1", "first")}})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "list", "KEY-123",
		"--order", "oldest", "--output=json")
	cwRequireOK(t, res)
	gets := stub.matching(http.MethodGet, "/issue/KEY-123/comment")
	if len(gets) != 1 || gets[0].Query.Get("orderBy") != "+created" {
		t.Fatalf("GET does not carry orderBy=+created: %d gets, query %v", len(gets), putsQuery(gets))
	}
	Prove(t, "jira issue comment list", "order",
		"stub saw GET .../comment carry ?orderBy=+created for --order oldest (the default asks for -created)")
}

func TestCommentListAllDrainsEveryPage(t *testing.T) {
	stub := newCWStub(t, cwStubConfig{
		comments: []string{cwComment("1", "one"), cwComment("2", "two"), cwComment("3", "three")},
		// The stub caps each page at 2 of the 3 comments, so only a walk
		// that pages makes the second request a single fetch never would.
		pageCap: 2,
	})
	res := cwRun(t, stub, t.TempDir(), "issue", "comment", "list", "KEY-123",
		"--all", "--output=json")
	env := cwRequireOK(t, res)
	gets := stub.matching(http.MethodGet, "/issue/KEY-123/comment")
	if len(gets) != 2 {
		t.Fatalf("--all made %d comment GETs, want 2 (a page walk)", len(gets))
	}
	data := cwDataMap(t, env)
	comments, ok := data["comments"].([]any)
	if !ok || len(comments) != 3 {
		t.Fatalf("data.comments = %v, want all 3 across both pages", data["comments"])
	}
	Prove(t, "jira issue comment list", "all",
		"--all walked two stub pages (2 GETs) and the envelope carries all 3 comments; one bounded fetch returns 2")
}
