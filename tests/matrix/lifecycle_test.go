//go:build flagmatrix

package matrix

// The lifecycle cluster's proving cases: `issue transition`, `issue
// delete`, `issue clone` and `issue rank`, plus the harness every case in
// this file and global_test.go runs through.
//
// The bar each case holds itself to (doc.go's, restated because it is
// the entire point): a case must FAIL if its flag is IGNORED. Exit 0 and
// non-empty output prove nothing; what is asserted is the flag's
// observable effect — the request the stub recorded (method, path,
// query, body), a field present or absent in the envelope because of the
// flag, or a refusal identified by taxonomy CODE.
//
// Every invocation runs the REAL binary against a loopback stub with a
// scrubbed environment. The base URL comes from the keyring in
// production, ignoring HOME and XDG entirely, so JIRA_BASE_URL is set
// explicitly on every run and the proxy pins anything else to
// 127.0.0.1:1 — this cluster is the destructive one, and a stray delete
// against a real tenant is unrecoverable.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	xos "github.com/gechr/x/os"
)

// ---------------------------------------------------------------------
// Harness: binary, stub, run, envelope.
// ---------------------------------------------------------------------

const (
	mx4Email = "person@example.com"
	mx4Token = "matrix-token"
)

var (
	mx4BinOnce sync.Once
	mx4BinPath string
	mx4BinErr  error
)

// mx4Binary compiles the real binary once per test process — the audit
// corpus's compile-once posture, for its reason (a per-case build would
// dominate wall time). The directory is a plain MkdirTemp left to the OS
// temp reaper: TestMain belongs to the harness and cannot host a cleanup
// hook from here.
func mx4Binary(t *testing.T) string {
	t.Helper()
	mx4BinOnce.Do(func() {
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			mx4BinErr = err
			return
		}
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
			mx4BinErr = fmt.Errorf("resolve repo root: %w", err)
			return
		}
		dir, err := os.MkdirTemp("", "jira-matrix-bin-")
		if err != nil {
			mx4BinErr = err
			return
		}
		mx4BinPath = filepath.Join(dir, "jira")
		if runtime.GOOS == "windows" {
			mx4BinPath += ".exe"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", mx4BinPath, "./cmd/jira")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			mx4BinErr = fmt.Errorf("build jira binary: %w\n%s", err, out)
		}
	})
	if mx4BinErr != nil {
		t.Fatalf("flagmatrix harness: %v", mx4BinErr)
	}
	return mx4BinPath
}

// mx4Request is one request the stub recorded, complete enough that a
// case can assert the wire effect of a flag: method, path, query, body,
// and the basic-auth pair (which is how --email and --with-token become
// observable).
type mx4Request struct {
	Method   string
	Path     string
	Query    string
	Body     string
	AuthUser string
	AuthPass string
}

// mx4Stub is a recording Jira lookalike serving exactly the endpoints
// this cluster's commands touch. Any path it does not model is a 404,
// so a case that strays fails loudly rather than being quietly served.
type mx4Stub struct {
	srv *httptest.Server

	mu   sync.Mutex
	reqs []mx4Request

	// screens says whether a key's "Done" transition has a screen.
	// Missing keys resolve to screened=true.
	screens map[string]bool
	// searchKeys is what POST /search/jql answers, in order.
	searchKeys []string
	// filterJQL is what GET /filter/{id} carries.
	filterJQL string
	// sleep delays every response — the --timeout case's whole lever.
	sleep time.Duration
	// commentStatus lets a case fail the separate-comment POST.
	commentStatus int
}

func newMx4Stub(t *testing.T) *mx4Stub {
	t.Helper()
	s := &mx4Stub{
		screens:       map[string]bool{},
		searchKeys:    []string{"KEY-101", "KEY-102"},
		filterJQL:     "project = KEY AND labels = matrix",
		commentStatus: http.StatusCreated,
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *mx4Stub) URL() string { return s.srv.URL }

func (s *mx4Stub) record(r *http.Request, body []byte) {
	user, pass, _ := r.BasicAuth()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, mx4Request{
		Method:   r.Method,
		Path:     r.URL.Path,
		Query:    r.URL.RawQuery,
		Body:     string(body),
		AuthUser: user,
		AuthPass: pass,
	})
}

// requests snapshots everything recorded so far.
func (s *mx4Stub) requests() []mx4Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]mx4Request, len(s.reqs))
	copy(out, s.reqs)
	return out
}

// matching returns the recorded requests with the given method whose
// path contains fragment.
func (s *mx4Stub) matching(method, fragment string) []mx4Request {
	var out []mx4Request
	for _, r := range s.requests() {
		if r.Method == method && strings.Contains(r.Path, fragment) {
			out = append(out, r)
		}
	}
	return out
}

func (s *mx4Stub) handle(w http.ResponseWriter, r *http.Request) {
	body := make([]byte, 0)
	if r.Body != nil {
		b := new(strings.Builder)
		buf := make([]byte, 32<<10)
		for {
			n, err := r.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		body = []byte(b.String())
	}
	s.record(r, body)
	if s.sleep > 0 {
		time.Sleep(s.sleep)
	}

	path := r.URL.Path
	switch {
	case path == "/rest/api/3/myself" && r.Method == http.MethodGet:
		mx4WriteJSON(w, http.StatusOK, map[string]any{
			"accountId":    "5b10a2844c20165700ede21g",
			"displayName":  "Matrix Account",
			"emailAddress": mx4Email,
			"active":       true,
		})
	case path == "/rest/api/3/search/jql" && r.Method == http.MethodPost:
		issues := make([]any, 0, len(s.searchKeys))
		for i, key := range s.searchKeys {
			issues = append(issues, map[string]any{"id": fmt.Sprintf("1000%d", i), "key": key})
		}
		mx4WriteJSON(w, http.StatusOK, map[string]any{"isLast": true, "issues": issues})
	case strings.HasPrefix(path, "/rest/api/3/filter/") && r.Method == http.MethodGet:
		mx4WriteJSON(w, http.StatusOK, map[string]any{
			"id":   strings.TrimPrefix(path, "/rest/api/3/filter/"),
			"name": "matrix filter",
			"jql":  s.filterJQL,
		})
	case path == "/rest/agile/1.0/issue/rank" && r.Method == http.MethodPut:
		w.WriteHeader(http.StatusNoContent)
	case path == "/rest/api/3/issue" && r.Method == http.MethodPost:
		mx4WriteJSON(w, http.StatusCreated, map[string]any{
			"id": "10900", "key": "KEY-900", "self": s.srv.URL + "/rest/api/3/issue/10900",
		})
	case strings.HasPrefix(path, "/rest/api/3/issue/"):
		s.handleIssue(w, r, strings.TrimPrefix(path, "/rest/api/3/issue/"))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *mx4Stub) handleIssue(w http.ResponseWriter, r *http.Request, rest string) {
	key, tail, _ := strings.Cut(rest, "/")
	screened, known := s.screens[key]
	if !known {
		screened = true
	}
	switch {
	case tail == "transitions" && r.Method == http.MethodGet:
		mx4WriteJSON(w, http.StatusOK, mx4TransitionsBody(screened))
	case tail == "transitions" && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusNoContent)
	case tail == "comment" && r.Method == http.MethodPost:
		if s.commentStatus != http.StatusCreated {
			w.WriteHeader(s.commentStatus)
			return
		}
		mx4WriteJSON(w, http.StatusCreated, map[string]any{"id": "9001"})
	case tail == "" && r.Method == http.MethodGet:
		mx4WriteJSON(w, http.StatusOK, map[string]any{
			"id": "10100", "key": key, "self": s.srv.URL + "/rest/api/3/issue/10100",
			"fields": map[string]any{
				"summary":   "Release v1.4",
				"issuetype": map[string]any{"name": "Task"},
				"project":   map[string]any{"key": "KEY"},
				"labels":    []string{"source-label"},
			},
		})
	case tail == "" && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// mx4TransitionsBody is the transition list every key answers: a decoy
// and the "Done" target the cases name, with the screen bit under test.
func mx4TransitionsBody(doneHasScreen bool) map[string]any {
	return map[string]any{
		"transitions": []any{
			map[string]any{
				"id": "11", "name": "Start Progress", "hasScreen": !doneHasScreen,
				"isAvailable": true,
				"to":          map[string]any{"id": "3", "name": "In Progress"},
			},
			map[string]any{
				"id": "31", "name": "Done", "hasScreen": doneHasScreen,
				"isAvailable": true,
				"to":          map[string]any{"id": "5", "name": "Done"},
			},
		},
	}
}

func mx4WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// A stub write failure surfaces as the case's own decode failure.
	_ = json.NewEncoder(w).Encode(v)
}

// mx4Opts configures one invocation of the real binary.
type mx4Opts struct {
	// Executable copies the cached binary into this owned installation path.
	Executable string
	// Stub wires JIRA_BASE_URL (plus the stub credentials) when non-nil.
	Stub *mx4Stub
	// Env overrides or adds variables; Unset removes harness defaults
	// (the --color case needs NO_COLOR gone, not empty).
	Env   map[string]string
	Unset []string
	// Stdin is piped in when non-empty.
	Stdin string
	// Files are seeded into the working directory before the run.
	Files map[string]string
}

type mx4Result struct {
	Stdout string
	Stderr string
	Exit   int
	// Workdir is where the run happened, for cases asserting what a run
	// did or did not leave on disk.
	Workdir string
}

// mx4Run executes one scrubbed invocation. The environment is an
// allowlist, never os.Environ(), and every route off the loopback stub
// dies at the unroutable proxy — the corpus's hermeticity posture,
// copied because this cluster deletes things.
func mx4Run(t *testing.T, o mx4Opts, args ...string) mx4Result {
	t.Helper()
	bin := mx4Binary(t)
	work := t.TempDir()
	if o.Executable != "" {
		if !filepath.IsLocal(o.Executable) {
			t.Fatal("executable fixture must stay inside its owned directory")
		}
		copyPath := filepath.Join(work, o.Executable)
		if err := os.MkdirAll(filepath.Dir(copyPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := xos.CopyFile(bin, copyPath); err != nil {
			t.Fatal(err)
		}
		bin = copyPath
	}
	for name, content := range o.Files {
		if err := os.WriteFile(filepath.Join(work, name), []byte(content), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	env := map[string]string{
		"PATH":                     os.Getenv("PATH"),
		"HOME":                     work,
		"TMPDIR":                   work,
		"XDG_CONFIG_HOME":          filepath.Join(work, "config"),
		"XDG_CACHE_HOME":           filepath.Join(work, "cache"),
		"XDG_RUNTIME_DIR":          work,
		"HTTP_PROXY":               "http://127.0.0.1:1",
		"HTTPS_PROXY":              "http://127.0.0.1:1",
		"NO_PROXY":                 "127.0.0.1,localhost",
		"COLUMNS":                  "100",
		"TERM":                     "dumb",
		"NO_COLOR":                 "1",
		"JIRA_KEYRING":             "false",
		"DBUS_SESSION_BUS_ADDRESS": "/dev/null",
	}
	if o.Stub != nil {
		env["JIRA_BASE_URL"] = o.Stub.URL()
		env["JIRA_EMAIL"] = mx4Email
		env["JIRA_API_TOKEN"] = mx4Token
	}
	for k, v := range o.Env {
		env[k] = v
	}
	for _, k := range o.Unset {
		delete(env, k)
	}
	flat := make([]string, 0, len(env))
	for k, v := range env {
		flat = append(flat, k+"="+v)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var stdout, stderr strings.Builder
	var cmd *exec.Cmd
	var err error
	startDeadline := time.Now().Add(time.Second)
	for {
		cmd = exec.CommandContext(ctx, bin, args...)
		cmd.Dir, cmd.Env = work, flat
		if o.Stdin != "" {
			cmd.Stdin = strings.NewReader(o.Stdin)
		}
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err = cmd.Start()
		if o.Executable == "" || !errors.Is(err, syscall.ETXTBSY) || !time.Now().Before(startDeadline) {
			break
		}
		// Another fork can briefly retain the copied executable's writable
		// descriptor before CLOEXEC closes it (Go issue 22315). Only Start
		// failed: no command ran and no mutation can be replayed.
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(10 * time.Millisecond):
			continue
		}
		break
	}
	if err == nil {
		err = cmd.Wait()
	}
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("run %v: %v\nstderr: %s", args, err, stderr.String())
		}
		code = exit.ExitCode()
	}
	return mx4Result{Stdout: stdout.String(), Stderr: stderr.String(), Exit: code, Workdir: work}
}

// mx4Envelope is the machine frame as these cases read it.
type mx4Envelope struct {
	OK   bool `json:"ok"`
	Meta struct {
		Command  string `json:"command"`
		ExitCode *int   `json:"exit_code"`
	} `json:"meta"`
	Data     json.RawMessage `json:"data"`
	Errors   []mx4Error      `json:"errors"`
	Warnings []mx4Warning    `json:"warnings"`
}

type mx4Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field"`
}

type mx4Warning struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Lossy   bool   `json:"lossy"`
}

func mx4Frame(t *testing.T, stdout string) mx4Envelope {
	t.Helper()
	var env mx4Envelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not an envelope: %v\n%s", err, stdout)
	}
	return env
}

// mx4Data unmarshals the envelope's data into out.
func mx4Data(t *testing.T, env mx4Envelope, out any) {
	t.Helper()
	if err := json.Unmarshal(env.Data, out); err != nil {
		t.Fatalf("envelope data: %v\n%s", err, env.Data)
	}
}

// mx4ErrCode asserts a failed run's leading taxonomy code — refusals are
// identified by CODE, never by message text.
func mx4ErrCode(t *testing.T, res mx4Result, code string) mx4Envelope {
	t.Helper()
	env := mx4Frame(t, res.Stdout)
	if env.OK || res.Exit == 0 {
		t.Fatalf("expected a refusal, got ok=%t exit=%d\n%s", env.OK, res.Exit, res.Stdout)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != code {
		t.Fatalf("expected errors[0].code == %q\n%s", code, res.Stdout)
	}
	return env
}

// mx4OK asserts a run succeeded and returns its envelope.
func mx4OK(t *testing.T, res mx4Result) mx4Envelope {
	t.Helper()
	if res.Exit != 0 {
		t.Fatalf("expected success, exit=%d\nstdout: %s\nstderr: %s", res.Exit, res.Stdout, res.Stderr)
	}
	env := mx4Frame(t, res.Stdout)
	if !env.OK {
		t.Fatalf("expected ok envelope\n%s", res.Stdout)
	}
	return env
}

// mx4Body decodes a recorded request body into a map for shape asserts.
func mx4Body(t *testing.T, req mx4Request) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(req.Body), &m); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, req.Body)
	}
	return m
}

// mx4Dig walks nested maps by key, failing loudly at the first miss.
func mx4Dig(t *testing.T, m map[string]any, path ...string) any {
	t.Helper()
	var cur any = m
	for _, k := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("dig %v: %T is not an object", path, cur)
		}
		cur, ok = obj[k]
		if !ok {
			t.Fatalf("dig %v: key %q absent", path, k)
		}
	}
	return cur
}

// mx4KeyedResults is the shared results[] shape the lifecycle envelopes
// carry.
type mx4KeyedResult struct {
	Issue struct {
		Key string `json:"key"`
	} `json:"issue"`
	OK  bool            `json:"ok"`
	Err *mx4Error       `json:"error"`
	Dat json.RawMessage `json:"data"`
}

func mx4ResultKeys(results []mx4KeyedResult) []string {
	keys := make([]string, len(results))
	for i, r := range results {
		keys[i] = r.Issue.Key
	}
	return keys
}

// ---------------------------------------------------------------------
// jira issue transition
// ---------------------------------------------------------------------

const transitionPath = "jira issue transition"

// transitionData is the per-key operation object.
type transitionData struct {
	Transition *struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		HasScreen bool   `json:"has_screen"`
	} `json:"transition"`
	CommentPosted string `json:"comment_posted"`
	CommentPlan   string `json:"comment_plan"`
}

type transitionOut struct {
	DryRun     bool             `json:"dry_run"`
	Transition string           `json:"transition"`
	Results    []mx4KeyedResult `json:"results"`
	Selector   *struct {
		Kind     string `json:"kind"`
		Resolved bool   `json:"resolved"`
	} `json:"selector"`
}

func TestTransitionTransitionFlag(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub}, "issue", "transition", "KEY-1", "--transition", "Done", "--output=json")
	mx4OK(t, res)
	posts := stub.matching(http.MethodPost, "/issue/KEY-1/transitions")
	if len(posts) != 1 {
		t.Fatalf("expected one transition POST, got %d", len(posts))
	}
	if id := mx4Dig(t, mx4Body(t, posts[0]), "transition", "id"); id != "31" {
		t.Fatalf("POST carries transition.id %v, want 31 (the resolved 'Done')", id)
	}
	Prove(t, transitionPath, "transition",
		"with --transition the lone positional stayed a key: stub saw POST /issue/KEY-1/transitions with transition.id=31; without the flag the same argv is an arg_count_invalid refusal")
}

func TestTransitionStatusHiddenAlias(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub}, "issue", "transition", "KEY-1", "--status", "Done", "--output=json")
	mx4OK(t, res)
	posts := stub.matching(http.MethodPost, "/issue/KEY-1/transitions")
	if len(posts) != 1 {
		t.Fatalf("expected one transition POST, got %d", len(posts))
	}
	if id := mx4Dig(t, mx4Body(t, posts[0]), "transition", "id"); id != "31" {
		t.Fatalf("POST carries transition.id %v, want 31", id)
	}
	Prove(t, transitionPath, "status",
		"hidden --status folded into --transition: stub saw POST with transition.id=31 for an argv naming no positional status; unfolded it would be an arg_count_invalid refusal")
}

// transitionCommentADF asserts the transition POST for key carried an
// atomic comment whose ADF contains text, and returns the run.
func transitionCommentADF(t *testing.T, stub *mx4Stub, text string) {
	t.Helper()
	posts := stub.matching(http.MethodPost, "/issue/KEY-1/transitions")
	if len(posts) != 1 {
		t.Fatalf("expected one transition POST, got %d", len(posts))
	}
	comments, ok := mx4Dig(t, mx4Body(t, posts[0]), "update", "comment").([]any)
	if !ok || len(comments) != 1 {
		t.Fatalf("POST update.comment missing or not a one-element array: %s", posts[0].Body)
	}
	if !strings.Contains(posts[0].Body, text) {
		t.Fatalf("comment ADF does not contain %q: %s", text, posts[0].Body)
	}
}

func TestTransitionComment(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub},
		"issue", "transition", "KEY-1", "Done", "--comment", "Fixed in matrix.", "--output=json")
	env := mx4OK(t, res)
	transitionCommentADF(t, stub, "Fixed in matrix.")
	var out transitionOut
	mx4Data(t, env, &out)
	var data transitionData
	if err := json.Unmarshal(out.Results[0].Dat, &data); err != nil {
		t.Fatal(err)
	}
	if data.CommentPosted != "atomic" {
		t.Fatalf("comment_posted = %q, want atomic", data.CommentPosted)
	}
	Prove(t, transitionPath, "comment",
		"the screened key's one transition POST composed update.comment[0].add with an ADF body carrying the text, and the envelope reported comment_posted=atomic")
}

func TestTransitionCommentFile(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub, Files: map[string]string{"note.md": "From a file."}},
		"issue", "transition", "KEY-1", "Done", "--comment-file", "note.md", "--output=json")
	mx4OK(t, res)
	transitionCommentADF(t, stub, "From a file.")
	Prove(t, transitionPath, "comment-file",
		"the transition POST's update.comment ADF carries the seeded file's text, so the file route fed the atomic comment")
}

// transitionBodyAlias proves one hidden spelling of --comment: the alias
// value must surface in the transition POST's ADF, which an unfolded
// alias (a parsed flag nobody reads) cannot produce.
func transitionBodyAlias(t *testing.T, flag string) {
	t.Helper()
	stub := newMx4Stub(t)
	text := "Via --" + flag + "."
	res := mx4Run(t, mx4Opts{Stub: stub},
		"issue", "transition", "KEY-1", "Done", "--"+flag, text, "--output=json")
	mx4OK(t, res)
	transitionCommentADF(t, stub, text)
	Prove(t, transitionPath, flag,
		"hidden --"+flag+" folded into --comment: its value reached the transition POST's update.comment ADF; ignored, the POST would carry no comment at all")
}

func TestTransitionBodyAlias(t *testing.T)        { transitionBodyAlias(t, "body") }
func TestTransitionDescriptionAlias(t *testing.T) { transitionBodyAlias(t, "description") }
func TestTransitionMarkdownAlias(t *testing.T)    { transitionBodyAlias(t, "markdown") }

// transitionFileAlias proves one hidden spelling of --comment-file.
func transitionFileAlias(t *testing.T, flag string) {
	t.Helper()
	stub := newMx4Stub(t)
	text := "File via --" + flag + "."
	res := mx4Run(t, mx4Opts{Stub: stub, Files: map[string]string{"aliased.md": text}},
		"issue", "transition", "KEY-1", "Done", "--"+flag, "aliased.md", "--output=json")
	mx4OK(t, res)
	transitionCommentADF(t, stub, text)
	Prove(t, transitionPath, flag,
		"hidden --"+flag+" folded into --comment-file: the seeded file's text reached the transition POST's ADF; ignored, the POST would carry no comment")
}

func TestTransitionBodyFileAlias(t *testing.T)        { transitionFileAlias(t, "body-file") }
func TestTransitionDescriptionFileAlias(t *testing.T) { transitionFileAlias(t, "description-file") }
func TestTransitionTemplateAlias(t *testing.T)        { transitionFileAlias(t, "template") }
func TestTransitionMarkdownFileAlias(t *testing.T)    { transitionFileAlias(t, "markdown-file") }

// mx4ADFDoc is a native ADF document with a marker string no Markdown
// conversion would produce byte-identically.
const mx4ADFDoc = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"raw-adf-marker"}]}]}`

func transitionADFRoute(t *testing.T, flag string) {
	t.Helper()
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub, Files: map[string]string{"doc.json": mx4ADFDoc}},
		"issue", "transition", "KEY-1", "Done", "--"+flag, "doc.json", "--output=json")
	mx4OK(t, res)
	transitionCommentADF(t, stub, "raw-adf-marker")
	Prove(t, transitionPath, flag,
		"--"+flag+" fed the native ADF document straight into the transition POST's update.comment body (its marker text arrived verbatim)")
}

func TestTransitionJSONInput(t *testing.T)     { transitionADFRoute(t, "json-input") }
func TestTransitionFromJSONAlias(t *testing.T) { transitionADFRoute(t, "from-json") }
func TestTransitionBodyADFAlias(t *testing.T)  { transitionADFRoute(t, "body-adf") }

func transitionNativePayloadRoute(t *testing.T, flag string) {
	t.Helper()
	stub := newMx4Stub(t)
	const input = `{"transition":{"name":"Done","looped":false},"fields":{"customfield_10001":9007199254740993},"update":{"labels":[{"add":"<>&\u2028"}]},"historyMetadata":{"type":"matrix"},"properties":[{"key":"fixture","value":null}],"futureCamelField":false}`
	res := mx4Run(t, mx4Opts{Stub: stub, Files: map[string]string{"transition.json": input}},
		"issue", "transition", "KEY-1", "--"+flag, "transition.json", "--output=json")
	env := mx4OK(t, res)
	requests := stub.matching(http.MethodPost, "/issue/KEY-1/transitions")
	if len(requests) != 1 {
		t.Fatal("native transition input skipped or replayed its write")
	}
	var wire map[string]json.RawMessage
	if json.Unmarshal([]byte(requests[0].Body), &wire) != nil || string(wire["transition"]) != `{"id":"31","looped":false}` || string(wire["fields"]) != `{"customfield_10001":9007199254740993}` || string(wire["update"]) != `{"labels":[{"add":"<>&\u2028"}]}` || string(wire["historyMetadata"]) != `{"type":"matrix"}` || string(wire["properties"]) != `[{"key":"fixture","value":null}]` || string(wire["futureCamelField"]) != "false" {
		t.Fatal("native transition flag lost target, raw values or request extensions")
	}
	var data struct {
		Results []struct {
			OK   bool `json:"ok"`
			Data struct {
				Payload   json.RawMessage `json:"payload"`
				Attempted bool            `json:"attempted"`
				Applied   bool            `json:"applied"`
			} `json:"data"`
		} `json:"results"`
	}
	mx4Data(t, env, &data)
	if len(data.Results) != 1 || !data.Results[0].OK || !data.Results[0].Data.Attempted || !data.Results[0].Data.Applied || string(data.Results[0].Data.Payload) != requests[0].Body {
		t.Fatal("native transition flag lost its exact prepared write receipt")
	}
	Prove(t, transitionPath, flag, "native request target, custom fields, operations and extensions reached one transition write and its exact receipt")
}

func TestTransitionNativeJSONInput(t *testing.T) { transitionNativePayloadRoute(t, "json-input") }

func TestTransitionNativeFromJSONAlias(t *testing.T) { transitionNativePayloadRoute(t, "from-json") }
func TestTransitionNativeBodyADFAlias(t *testing.T)  { transitionNativePayloadRoute(t, "body-adf") }

func TestTransitionResolution(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub},
		"issue", "transition", "KEY-1", "Done", "--resolution", "Won't Do", "--output=json")
	mx4OK(t, res)
	posts := stub.matching(http.MethodPost, "/issue/KEY-1/transitions")
	if len(posts) != 1 {
		t.Fatalf("expected one transition POST, got %d", len(posts))
	}
	if name := mx4Dig(t, mx4Body(t, posts[0]), "fields", "resolution", "name"); name != "Won't Do" {
		t.Fatalf("fields.resolution.name = %v", name)
	}
	Prove(t, transitionPath, "resolution",
		"the transition POST carried fields.resolution.name == the flag's value; without the flag the request has no fields object")
}

func TestTransitionAssignee(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub},
		"issue", "transition", "KEY-1", "Done", "--assignee", "5b10a2844c20165700ede21g", "--output=json")
	mx4OK(t, res)
	posts := stub.matching(http.MethodPost, "/issue/KEY-1/transitions")
	if len(posts) != 1 {
		t.Fatalf("expected one transition POST, got %d", len(posts))
	}
	if id := mx4Dig(t, mx4Body(t, posts[0]), "fields", "assignee", "accountId"); id != "5b10a2844c20165700ede21g" {
		t.Fatalf("fields.assignee.accountId = %v", id)
	}
	Prove(t, transitionPath, "assignee",
		"the transition POST carried fields.assignee.accountId == the flag's account id; without the flag the request has no fields object")
}

func TestTransitionAtomicOnlyRefusesScreenless(t *testing.T) {
	stub := newMx4Stub(t)
	stub.screens["KEY-2"] = false
	res := mx4Run(t, mx4Opts{Stub: stub},
		"issue", "transition", "KEY-2", "Done", "--comment", "Guaranteed.", "--atomic-only", "--output=json")
	if res.Exit == 0 {
		t.Fatalf("expected the screenless key to be refused\n%s", res.Stdout)
	}
	env := mx4Frame(t, res.Stdout)
	if len(env.Errors) == 0 || env.Errors[0].Code != "flag_not_applicable" {
		t.Fatalf("expected flag_not_applicable, got %v", env.Errors)
	}
	if got := len(stub.matching(http.MethodPost, "")); got != 0 {
		t.Fatalf("REFUSAL MUST WRITE NOTHING: stub saw %d POSTs", got)
	}
	// Without the flag the identical argv splits into two writes.
	stub2 := newMx4Stub(t)
	stub2.screens["KEY-2"] = false
	mx4OK(t, mx4Run(t, mx4Opts{Stub: stub2},
		"issue", "transition", "KEY-2", "Done", "--comment", "Guaranteed.", "--output=json"))
	if got := len(stub2.matching(http.MethodPost, "")); got != 2 {
		t.Fatalf("control run should split into transition+comment POSTs, saw %d", got)
	}
	Prove(t, transitionPath, "atomic-only",
		"on a screenless transition the flag turned the split into a flag_not_applicable refusal with ZERO POSTs recorded, where the control run without it performed both the transition and the separate comment POST")
}

func TestTransitionValidateRemote(t *testing.T) {
	stub := newMx4Stub(t)
	stub.screens["KEY-2"] = false
	res := mx4Run(t, mx4Opts{Stub: stub},
		"issue", "transition", "KEY-1", "KEY-2", "Done", "--comment", "Plan?", "--dry-run", "--validate-remote", "--output=json")
	env := mx4OK(t, res)
	if got := len(stub.matching(http.MethodGet, "/transitions")); got != 2 {
		t.Fatalf("remote preview should read both workflows, saw %d GETs", got)
	}
	if got := len(stub.matching(http.MethodPost, "")); got != 0 {
		t.Fatalf("remote preview must write nothing, saw %d POSTs", got)
	}
	var out transitionOut
	mx4Data(t, env, &out)
	plans := map[string]string{}
	for _, r := range out.Results {
		var data transitionData
		if err := json.Unmarshal(r.Dat, &data); err != nil {
			t.Fatal(err)
		}
		plans[r.Issue.Key] = data.CommentPlan
	}
	if plans["KEY-1"] != "atomic" || plans["KEY-2"] != "separate" {
		t.Fatalf("comment_plan = %v, want KEY-1 atomic / KEY-2 separate", plans)
	}
	Prove(t, transitionPath, "validate-remote",
		"with --dry-run it read both workflows (2 GETs, 0 POSTs) and filled comment_plan atomic/separate per key — fields a bare --dry-run leaves absent")
}

func TestTransitionDryRun(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub},
		"issue", "transition", "KEY-1", "Done", "--comment", "Preview.", "--dry-run", "--output=json")
	env := mx4OK(t, res)
	if got := len(stub.requests()); got != 0 {
		t.Fatalf("--dry-run must contact nobody, stub saw %d requests", got)
	}
	var out transitionOut
	mx4Data(t, env, &out)
	if !out.DryRun {
		t.Fatalf("data.dry_run = false on a preview")
	}
	Prove(t, transitionPath, "dry-run",
		"the preview recorded dry_run=true and the stub received ZERO requests, where the live run performs a workflow GET and a transition POST")
}

func TestTransitionForceOnSelector(t *testing.T) {
	stub := newMx4Stub(t)
	keys := "KEY-101\nKEY-102\n"
	refused := mx4Run(t, mx4Opts{Stub: stub, Files: map[string]string{"keys.txt": keys}},
		"issue", "transition", "--from-file", "keys.txt", "Done", "--output=json")
	mx4ErrCode(t, refused, "flag_required")
	if got := len(stub.matching(http.MethodPost, "/transitions")); got != 0 {
		t.Fatalf("refused run must write nothing, saw %d transition POSTs", got)
	}
	forced := mx4Run(t, mx4Opts{Stub: stub, Files: map[string]string{"keys.txt": keys}},
		"issue", "transition", "--from-file", "keys.txt", "Done", "--force", "--output=json")
	env := mx4OK(t, forced)
	if got := len(stub.matching(http.MethodPost, "/transitions")); got != 2 {
		t.Fatalf("forced run should transition both keys, saw %d POSTs", got)
	}
	var out transitionOut
	mx4Data(t, env, &out)
	if got := mx4ResultKeys(out.Results); len(got) != 2 || got[0] != "KEY-101" {
		t.Fatalf("enumeration must survive --force, results = %v", got)
	}
	Prove(t, transitionPath, "force",
		"the selector-driven run was refused flag_required with zero POSTs; --force turned the same argv into two transition POSTs while results[] still enumerated both keys")
}

func TestTransitionYesHiddenAlias(t *testing.T) {
	stub := newMx4Stub(t)
	forced := mx4Run(t, mx4Opts{Stub: stub, Files: map[string]string{"keys.txt": "KEY-101\n"}},
		"issue", "transition", "--from-file", "keys.txt", "Done", "--yes", "--output=json")
	mx4OK(t, forced)
	if got := len(stub.matching(http.MethodPost, "/transitions")); got != 1 {
		t.Fatalf("--yes should have skipped the prompt and transitioned, saw %d POSTs", got)
	}
	Prove(t, transitionPath, "yes",
		"hidden --yes set --force's bool: the selector-driven run wrote its transition POST instead of the flag_required refusal the unforced control gets")
}

func TestTransitionJQLSelector(t *testing.T) {
	stub := newMx4Stub(t)
	query := "project = KEY AND status = 'In Review'"
	res := mx4Run(t, mx4Opts{Stub: stub},
		"issue", "transition", "--jql", query, "Done", "--force", "--output=json")
	env := mx4OK(t, res)
	searches := stub.matching(http.MethodPost, "/search/jql")
	if len(searches) != 1 {
		t.Fatalf("expected one search, got %d", len(searches))
	}
	if got := mx4Dig(t, mx4Body(t, searches[0]), "jql"); got != query {
		t.Fatalf("search body jql = %v", got)
	}
	var out transitionOut
	mx4Data(t, env, &out)
	if got := mx4ResultKeys(out.Results); len(got) != 2 || got[0] != "KEY-101" || got[1] != "KEY-102" {
		t.Fatalf("results should be the search's keys, got %v", got)
	}
	Prove(t, transitionPath, "jql",
		"the flag's query arrived verbatim in the POST /search/jql body and the run transitioned exactly the keys the search answered; ignored, the lone positional is an arg_count_invalid refusal")
}

func TestTransitionFilterSelector(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub},
		"issue", "transition", "--filter", "42", "Done", "--force", "--output=json")
	mx4OK(t, res)
	if got := len(stub.matching(http.MethodGet, "/filter/42")); got != 1 {
		t.Fatalf("expected the saved filter to be read, got %d GETs", got)
	}
	searches := stub.matching(http.MethodPost, "/search/jql")
	if len(searches) != 1 {
		t.Fatalf("expected one search, got %d", len(searches))
	}
	if got := mx4Dig(t, mx4Body(t, searches[0]), "jql"); got != stub.filterJQL {
		t.Fatalf("search ran %v, want the filter's own jql", got)
	}
	Prove(t, transitionPath, "filter",
		"--filter 42 caused GET /rest/api/3/filter/42 and a search whose jql equals the filter's stored query; ignored, the argv is an arg_count_invalid refusal")
}

func TestTransitionFromFile(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub, Files: map[string]string{"keys.txt": "KEY-201\nKEY-202\n"}},
		"issue", "transition", "--from-file", "keys.txt", "Done", "--dry-run", "--output=json")
	env := mx4OK(t, res)
	if got := len(stub.requests()); got != 0 {
		t.Fatalf("--from-file resolves locally; a bare preview must make no requests, saw %d", got)
	}
	var out transitionOut
	mx4Data(t, env, &out)
	if got := mx4ResultKeys(out.Results); len(got) != 2 || got[0] != "KEY-201" || got[1] != "KEY-202" {
		t.Fatalf("preview should enumerate the file's keys, got %v", got)
	}
	Prove(t, transitionPath, "from-file",
		"under a bare --dry-run the preview enumerated exactly the file's two keys with ZERO stub requests — local resolution; ignored, the argv is an arg_count_invalid refusal")
}

func TestTransitionParallelismRange(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub},
		"issue", "transition", "KEY-1", "Done", "--parallelism", "99", "--output=json")
	env := mx4ErrCode(t, res, "flag_value_invalid")
	if env.Errors[0].Field != "parallelism" {
		t.Fatalf("refusal field = %q, want parallelism", env.Errors[0].Field)
	}
	if got := len(stub.requests()); got != 0 {
		t.Fatalf("range refusal happens before I/O, stub saw %d requests", got)
	}
	Prove(t, transitionPath, "parallelism",
		"--parallelism 99 was refused flag_value_invalid (field=parallelism) before any request; an ignored flag would leave the default 4 and the run would succeed. Range proof only — actual fan-out width is not asserted")
}

// ---------------------------------------------------------------------
// jira issue delete
// ---------------------------------------------------------------------

const deletePath = "jira issue delete"

type deleteOut struct {
	DryRun         bool             `json:"dry_run"`
	DeleteSubtasks bool             `json:"delete_subtasks"`
	Results        []mx4KeyedResult `json:"results"`
	Selector       *struct {
		Kind     string `json:"kind"`
		Resolved bool   `json:"resolved"`
	} `json:"selector"`
}

func TestDeleteForce(t *testing.T) {
	stub := newMx4Stub(t)
	refused := mx4Run(t, mx4Opts{Stub: stub}, "issue", "delete", "KEY-1", "--output=json")
	env := mx4ErrCode(t, refused, "flag_required")
	if env.Errors[0].Field != "force" {
		t.Fatalf("refusal field = %q, want force", env.Errors[0].Field)
	}
	if got := len(stub.matching(http.MethodDelete, "")); got != 0 {
		t.Fatalf("refused run deleted %d issues", got)
	}
	forced := mx4Run(t, mx4Opts{Stub: stub}, "issue", "delete", "KEY-1", "--force", "--output=json")
	mx4OK(t, forced)
	dels := stub.matching(http.MethodDelete, "/issue/KEY-1")
	if len(dels) != 1 {
		t.Fatalf("forced run should DELETE once, saw %d", len(dels))
	}
	Prove(t, deletePath, "force",
		"without it the non-interactive run is a flag_required refusal (field=force) with zero DELETEs; with it the stub recorded DELETE /issue/KEY-1")
}

func TestDeleteYesHiddenAlias(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub}, "issue", "delete", "KEY-1", "--yes", "--output=json")
	mx4OK(t, res)
	if got := len(stub.matching(http.MethodDelete, "/issue/KEY-1")); got != 1 {
		t.Fatalf("--yes should have deleted, saw %d DELETEs", got)
	}
	Prove(t, deletePath, "yes",
		"hidden --yes set --force's bool: the DELETE was sent where the unforced control run is a flag_required refusal")
}

func TestDeleteSubtasks(t *testing.T) {
	stub := newMx4Stub(t)
	mx4OK(t, mx4Run(t, mx4Opts{Stub: stub}, "issue", "delete", "KEY-1", "--force", "--delete-subtasks", "--output=json"))
	mx4OK(t, mx4Run(t, mx4Opts{Stub: stub}, "issue", "delete", "KEY-2", "--force", "--output=json"))
	dels := stub.matching(http.MethodDelete, "")
	if len(dels) != 2 {
		t.Fatalf("expected two DELETEs, got %d", len(dels))
	}
	if dels[0].Query != "deleteSubtasks=true" || dels[1].Query != "deleteSubtasks=false" {
		t.Fatalf("queries = %q / %q, want true then false", dels[0].Query, dels[1].Query)
	}
	Prove(t, deletePath, "delete-subtasks",
		"the flag flipped the DELETE's query to deleteSubtasks=true where the control run sent deleteSubtasks=false")
}

func TestDeleteCascadeHiddenAlias(t *testing.T) {
	stub := newMx4Stub(t)
	mx4OK(t, mx4Run(t, mx4Opts{Stub: stub}, "issue", "delete", "KEY-1", "--force", "--cascade", "--output=json"))
	dels := stub.matching(http.MethodDelete, "")
	if len(dels) != 1 || dels[0].Query != "deleteSubtasks=true" {
		t.Fatalf("hidden --cascade should send deleteSubtasks=true, got %v", dels)
	}
	Prove(t, deletePath, "cascade",
		"hidden --cascade set the same bool as --delete-subtasks: the DELETE query carried deleteSubtasks=true")
}

func TestDeleteDryRun(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub}, "issue", "delete", "KEY-1", "KEY-2", "--dry-run", "--output=json")
	env := mx4OK(t, res)
	if got := len(stub.requests()); got != 0 {
		t.Fatalf("--dry-run must contact nobody, stub saw %d requests", got)
	}
	var out deleteOut
	mx4Data(t, env, &out)
	if !out.DryRun || len(out.Results) != 2 {
		t.Fatalf("preview should list both keys with dry_run=true, got %+v", out)
	}
	Prove(t, deletePath, "dry-run",
		"the preview exited 0 with dry_run=true, enumerated both keys, and the stub saw ZERO requests; ignored, the same non-interactive argv is a flag_required refusal")
}

func TestDeleteJQLResolvesBeforeConfirmation(t *testing.T) {
	stub := newMx4Stub(t)
	query := "project = KEY AND status = Done"
	// No --force, no terminal: the refusal must arrive AFTER the
	// selector resolved — the ordering is the safety property.
	refused := mx4Run(t, mx4Opts{Stub: stub}, "issue", "delete", "--jql", query, "--output=json")
	mx4ErrCode(t, refused, "flag_required")
	searches := stub.matching(http.MethodPost, "/search/jql")
	if len(searches) != 1 {
		t.Fatalf("the selector must resolve before the confirmation refuses, saw %d searches", len(searches))
	}
	if got := mx4Dig(t, mx4Body(t, searches[0]), "jql"); got != query {
		t.Fatalf("search jql = %v", got)
	}
	if got := len(stub.matching(http.MethodDelete, "")); got != 0 {
		t.Fatalf("nothing may be deleted before confirmation, saw %d DELETEs", got)
	}
	Prove(t, deletePath, "jql",
		"the flag's query hit POST /search/jql BEFORE the flag_required confirmation refusal, and zero DELETEs followed — resolution precedes the gate, and the gate precedes the write")
}

func TestDeleteFilterSelector(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub}, "issue", "delete", "--filter", "42", "--force", "--output=json")
	env := mx4OK(t, res)
	if got := len(stub.matching(http.MethodGet, "/filter/42")); got != 1 {
		t.Fatalf("expected the filter read, got %d", got)
	}
	dels := stub.matching(http.MethodDelete, "")
	if len(dels) != 2 {
		t.Fatalf("expected the search's two keys deleted, got %d", len(dels))
	}
	var out deleteOut
	mx4Data(t, env, &out)
	if out.Selector == nil || out.Selector.Kind != "filter" {
		t.Fatalf("envelope selector = %+v, want kind=filter", out.Selector)
	}
	Prove(t, deletePath, "filter",
		"--filter 42 read GET /filter/42, searched with its stored jql, deleted exactly the two matched keys, and published selector.kind=filter")
}

func TestDeleteFromFile(t *testing.T) {
	stub := newMx4Stub(t)
	files := map[string]string{"keys.txt": "KEY-301\nKEY-302\n"}
	live := mx4Run(t, mx4Opts{Stub: stub, Files: files},
		"issue", "delete", "--from-file", "keys.txt", "--force", "--output=json")
	mx4OK(t, live)
	dels := stub.matching(http.MethodDelete, "")
	deleted := map[string]bool{}
	for _, d := range dels {
		deleted[filepath.Base(d.Path)] = true
	}
	if len(dels) != 2 || !deleted["KEY-301"] || !deleted["KEY-302"] {
		t.Fatalf("expected DELETEs for exactly the file's keys, got %v", dels)
	}
	Prove(t, deletePath, "from-file",
		"the live run deleted exactly the file's two keys (DELETE per key recorded); ignored, the argv names nothing and is an arg_count_invalid refusal")
}

func TestDeleteValidateRemote(t *testing.T) {
	stub := newMx4Stub(t)
	bare := mx4Run(t, mx4Opts{Stub: stub}, "issue", "delete", "--jql", "project = KEY", "--dry-run", "--output=json")
	bareEnv := mx4OK(t, bare)
	if got := len(stub.requests()); got != 0 {
		t.Fatalf("bare preview must not resolve, stub saw %d requests", got)
	}
	var bareOut deleteOut
	mx4Data(t, bareEnv, &bareOut)
	if len(bareOut.Results) != 0 || bareOut.Selector == nil || bareOut.Selector.Resolved {
		t.Fatalf("bare preview should report unresolved with no results, got %+v", bareOut)
	}
	warned := false
	for _, w := range bareEnv.Warnings {
		if w.Type == "selector_unresolved" && w.Lossy {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("bare preview should warn selector_unresolved(lossy), got %+v", bareEnv.Warnings)
	}

	remote := mx4Run(t, mx4Opts{Stub: stub}, "issue", "delete", "--jql", "project = KEY", "--dry-run", "--validate-remote", "--output=json")
	remoteEnv := mx4OK(t, remote)
	if got := len(stub.matching(http.MethodPost, "/search/jql")); got != 1 {
		t.Fatalf("remote preview should search once, saw %d", got)
	}
	if got := len(stub.matching(http.MethodDelete, "")); got != 0 {
		t.Fatalf("remote preview must still delete nothing, saw %d", got)
	}
	var remoteOut deleteOut
	mx4Data(t, remoteEnv, &remoteOut)
	if got := mx4ResultKeys(remoteOut.Results); len(got) != 2 {
		t.Fatalf("remote preview should enumerate the matches, got %v", got)
	}
	Prove(t, deletePath, "validate-remote",
		"bare --dry-run left the selector unresolved (zero requests, empty results, selector_unresolved lossy warning); adding the flag resolved it (one search, two enumerated keys) and still deleted nothing")
}

func TestDeleteParallelismRange(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub}, "issue", "delete", "KEY-1", "--force", "--parallelism", "0", "--output=json")
	env := mx4ErrCode(t, res, "flag_value_invalid")
	if env.Errors[0].Field != "parallelism" {
		t.Fatalf("refusal field = %q", env.Errors[0].Field)
	}
	if got := len(stub.requests()); got != 0 {
		t.Fatalf("range refusal happens before I/O, saw %d requests", got)
	}
	Prove(t, deletePath, "parallelism",
		"--parallelism 0 was refused flag_value_invalid (field=parallelism) with zero requests; ignored, the default 4 would let the forced delete proceed. Range proof only")
}

// ---------------------------------------------------------------------
// jira issue clone
// ---------------------------------------------------------------------

const clonePath = "jira issue clone"

type cloneOut struct {
	DryRun   bool             `json:"dry_run"`
	Results  []mx4KeyedResult `json:"results"`
	Selector *struct {
		Kind     string `json:"kind"`
		Resolved bool   `json:"resolved"`
	} `json:"selector"`
}

// cloneCreateBody runs a clone of KEY-1 with extra args and returns the
// POST /rest/api/3/issue body the stub recorded.
func cloneCreateBody(t *testing.T, extra ...string) map[string]any {
	t.Helper()
	stub := newMx4Stub(t)
	args := append([]string{"issue", "clone", "KEY-1", "--output=json"}, extra...)
	res := mx4Run(t, mx4Opts{Stub: stub}, args...)
	mx4OK(t, res)
	posts := stub.matching(http.MethodPost, "/rest/api/3/issue")
	if len(posts) != 1 {
		t.Fatalf("expected one create POST, got %d", len(posts))
	}
	return mx4Body(t, posts[0])
}

// cloneFieldOverride proves one field-override flag: the value must land
// in the create POST under the given Jira field path.
func cloneFieldOverride(t *testing.T, flag, value string, fieldPath ...string) {
	t.Helper()
	body := cloneCreateBody(t, "--"+flag, value)
	got := mx4Dig(t, body, fieldPath...)
	// Repeatable flags land as arrays; scalars land bare.
	switch v := got.(type) {
	case string:
		if v != value {
			t.Fatalf("--%s: %v = %q, want %q", flag, fieldPath, v, value)
		}
	case []any:
		found := false
		for _, item := range v {
			if s, ok := item.(string); ok && s == value {
				found = true
			}
			if m, ok := item.(map[string]any); ok && (m["name"] == value || m["key"] == value || m["accountId"] == value) {
				found = true
			}
		}
		if !found {
			t.Fatalf("--%s: %v = %v does not carry %q", flag, fieldPath, v, value)
		}
	default:
		t.Fatalf("--%s: %v is %T", flag, fieldPath, got)
	}
	Prove(t, clonePath, flag,
		fmt.Sprintf("the create POST's %s carried the flag's value %q, overriding the copied source", strings.Join(fieldPath, "."), value))
}

func TestCloneSummaryOverride(t *testing.T) {
	body := cloneCreateBody(t, "--summary", "Retry of the migration")
	if got := mx4Dig(t, body, "fields", "summary"); got != "Retry of the migration" {
		t.Fatalf("fields.summary = %v, want the override (source says 'Release v1.4')", got)
	}
	Prove(t, clonePath, "summary",
		"the create POST's fields.summary carried the override where the copied source summary is 'Release v1.4'")
}

func TestCloneTypeOverride(t *testing.T) {
	cloneFieldOverride(t, "type", "Bug", "fields", "issuetype", "name")
}

func TestCloneProjectOverride(t *testing.T) {
	cloneFieldOverride(t, "project", "OTHER", "fields", "project", "key")
}

func TestCloneToProjectAlias(t *testing.T) {
	body := cloneCreateBody(t, "--to-project", "OTHER")
	if got := mx4Dig(t, body, "fields", "project", "key"); got != "OTHER" {
		t.Fatalf("hidden --to-project should set fields.project.key, got %v (source project is KEY)", got)
	}
	Prove(t, clonePath, "to-project",
		"hidden --to-project folded into --project: the create POST's fields.project.key became OTHER where the copied source project is KEY")
}

func TestClonePriorityOverride(t *testing.T) {
	cloneFieldOverride(t, "priority", "High", "fields", "priority", "name")
}

func TestCloneLabelOverride(t *testing.T) {
	cloneFieldOverride(t, "label", "matrix-label", "fields", "labels")
}

// TestCloneLabelsAlias closes the pair the matrix found BROKEN.
//
// The probe that first ran here failed, and the failure was real: clone
// shares AddIssueFieldFlags with create and edit, so it REGISTERED the
// hidden --labels alias, but its Validate never folded the field
// aliases the way write.go does. The flag parsed, was accepted, and was
// silently discarded — the create POST still carried the source issue's
// own labels. That is the declared-but-unbacked defect this matrix
// exists to find, and the case was left unwritten rather than asserting
// the broken behavior and calling the pair covered.
//
// The fold is wired now (clone.go's Validate), so the assertion is the
// one that was impossible before: the alias alone lands the value on the
// canonical flag's field.
func TestCloneLabelsAlias(t *testing.T) {
	body := cloneCreateBody(t, "--labels", "matrix-alias-label")
	got := mx4Dig(t, body, "fields", "labels")
	labels, ok := got.([]any)
	if !ok || len(labels) != 1 || labels[0] != "matrix-alias-label" {
		t.Fatalf("hidden --labels should set fields.labels, got %v — before clone folded its field aliases this silently kept the source's labels", got)
	}
	Prove(t, clonePath, "labels",
		"hidden --labels folded into --label: the create POST's fields.labels became [matrix-alias-label], where before the fold was wired the flag parsed and was discarded")
}

func TestCloneComponentOverride(t *testing.T) {
	cloneFieldOverride(t, "component", "backend", "fields", "components")
}

func TestCloneFixVersionOverride(t *testing.T) {
	cloneFieldOverride(t, "fix-version", "1.5.0", "fields", "fixVersions")
}

func TestCloneAffectsVersionOverride(t *testing.T) {
	cloneFieldOverride(t, "affects-version", "1.4.0", "fields", "versions")
}

func TestCloneDueOverride(t *testing.T) {
	cloneFieldOverride(t, "due", "2026-09-01", "fields", "duedate")
}

func TestCloneParentOverride(t *testing.T) {
	cloneFieldOverride(t, "parent", "KEY-500", "fields", "parent", "key")
}

func TestCloneAssigneeOverride(t *testing.T) {
	cloneFieldOverride(t, "assignee", "5b10a2844c20165700ede21g", "fields", "assignee", "accountId")
}

func TestCloneReporterOverride(t *testing.T) {
	cloneFieldOverride(t, "reporter", "5b10a2844c20165700ede21g", "fields", "reporter", "accountId")
}

func TestCloneOriginalEstimate(t *testing.T) {
	cloneFieldOverride(t, "original-estimate", "3d", "fields", "timetracking", "originalEstimate")
}

// cloneOverridesDoc names a custom field no flag can reach.
const cloneOverridesDoc = `{"fields":{"customfield_10016":8}}`

func cloneJSONInputRoute(t *testing.T, flag string) {
	t.Helper()
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub, Files: map[string]string{"overrides.json": cloneOverridesDoc}},
		"issue", "clone", "KEY-1", "--"+flag, "overrides.json", "--output=json")
	mx4OK(t, res)
	posts := stub.matching(http.MethodPost, "/rest/api/3/issue")
	if len(posts) != 1 {
		t.Fatalf("expected one create POST, got %d", len(posts))
	}
	if got := mx4Dig(t, mx4Body(t, posts[0]), "fields", "customfield_10016"); got != float64(8) {
		t.Fatalf("--%s: fields.customfield_10016 = %v, want 8", flag, got)
	}
	Prove(t, clonePath, flag,
		"--"+flag+"'s document layered fields.customfield_10016=8 into the create POST on top of the copied source fields")
}

func TestCloneJSONInput(t *testing.T)     { cloneJSONInputRoute(t, "json-input") }
func TestCloneFromJSONAlias(t *testing.T) { cloneJSONInputRoute(t, "from-json") }
func TestCloneBodyADFAlias(t *testing.T)  { cloneJSONInputRoute(t, "body-adf") }

func TestCloneReplace(t *testing.T) {
	body := cloneCreateBody(t, "--replace", "v1.4=v1.5")
	if got := mx4Dig(t, body, "fields", "summary"); got != "Release v1.5" {
		t.Fatalf("fields.summary = %v, want the rewritten 'Release v1.5' (source says v1.4)", got)
	}
	Prove(t, clonePath, "replace",
		"the copied summary 'Release v1.4' arrived in the create POST rewritten to 'Release v1.5'; ignored, the source text goes through untouched")
}

func TestCloneDryRun(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub}, "issue", "clone", "KEY-1", "--dry-run", "--output=json")
	env := mx4OK(t, res)
	if got := len(stub.requests()); got != 0 {
		t.Fatalf("--dry-run must contact nobody, stub saw %d requests", got)
	}
	var out cloneOut
	mx4Data(t, env, &out)
	if !out.DryRun || len(out.Results) != 1 {
		t.Fatalf("preview should list the source with dry_run=true, got %+v", out)
	}
	Prove(t, clonePath, "dry-run",
		"the preview recorded dry_run=true with ZERO stub requests, where a live clone performs the source GET and the create POST")
}

func TestCloneJQLSelector(t *testing.T) {
	stub := newMx4Stub(t)
	query := "project = KEY AND labels = template"
	res := mx4Run(t, mx4Opts{Stub: stub},
		"issue", "clone", "--jql", query, "--dry-run", "--validate-remote", "--output=json")
	env := mx4OK(t, res)
	searches := stub.matching(http.MethodPost, "/search/jql")
	if len(searches) != 1 {
		t.Fatalf("expected one search, got %d", len(searches))
	}
	if got := mx4Dig(t, mx4Body(t, searches[0]), "jql"); got != query {
		t.Fatalf("search jql = %v", got)
	}
	var out cloneOut
	mx4Data(t, env, &out)
	if got := mx4ResultKeys(out.Results); len(got) != 2 {
		t.Fatalf("preview should enumerate the matches, got %v", got)
	}
	Prove(t, clonePath, "jql",
		"the flag's query arrived verbatim in POST /search/jql and the resolved preview enumerated the two matched keys; ignored, the empty argv is an arg_count_invalid refusal")
}

func TestCloneFilterSelector(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub},
		"issue", "clone", "--filter", "42", "--dry-run", "--validate-remote", "--output=json")
	mx4OK(t, res)
	if got := len(stub.matching(http.MethodGet, "/filter/42")); got != 1 {
		t.Fatalf("expected the filter read, got %d", got)
	}
	searches := stub.matching(http.MethodPost, "/search/jql")
	if len(searches) != 1 || mx4Dig(t, mx4Body(t, searches[0]), "jql") != stub.filterJQL {
		t.Fatalf("search should run the filter's stored jql, got %v", searches)
	}
	Prove(t, clonePath, "filter",
		"--filter 42 read GET /filter/42 and searched with the filter's stored jql in the resolved preview; ignored, the empty argv is a refusal")
}

func TestCloneFromFile(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub, Files: map[string]string{"keys.txt": "KEY-401\n# comment\nKEY-402\n"}},
		"issue", "clone", "--from-file", "keys.txt", "--dry-run", "--output=json")
	env := mx4OK(t, res)
	if got := len(stub.requests()); got != 0 {
		t.Fatalf("--from-file resolves locally, stub saw %d requests", got)
	}
	var out cloneOut
	mx4Data(t, env, &out)
	if got := mx4ResultKeys(out.Results); len(got) != 2 || got[0] != "KEY-401" || got[1] != "KEY-402" {
		t.Fatalf("preview should enumerate the file's keys (comments stripped), got %v", got)
	}
	Prove(t, clonePath, "from-file",
		"a bare preview enumerated exactly the file's two keys (comment line stripped) with zero requests; ignored, the empty argv is a refusal")
}

func TestCloneForceOnSelector(t *testing.T) {
	stub := newMx4Stub(t)
	refused := mx4Run(t, mx4Opts{Stub: stub}, "issue", "clone", "--jql", "project = KEY", "--output=json")
	mx4ErrCode(t, refused, "flag_required")
	if got := len(stub.matching(http.MethodPost, "/rest/api/3/issue")); got != 0 {
		t.Fatalf("refused run created %d issues", got)
	}
	forced := mx4Run(t, mx4Opts{Stub: stub}, "issue", "clone", "--jql", "project = KEY", "--force", "--output=json")
	mx4OK(t, forced)
	if got := len(stub.matching(http.MethodPost, "/rest/api/3/issue")); got != 2 {
		t.Fatalf("forced run should create both clones, saw %d POSTs", got)
	}
	Prove(t, clonePath, "force",
		"the selector-driven clone was refused flag_required with zero creates; --force turned the identical argv into two POST /issue creates")
}

func TestCloneYesHiddenAlias(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub, Files: map[string]string{"keys.txt": "KEY-1\n"}},
		"issue", "clone", "--from-file", "keys.txt", "--yes", "--output=json")
	mx4OK(t, res)
	if got := len(stub.matching(http.MethodPost, "/rest/api/3/issue")); got != 1 {
		t.Fatalf("--yes should have cloned, saw %d creates", got)
	}
	Prove(t, clonePath, "yes",
		"hidden --yes set --force's bool: the selector-driven clone created its issue where the unforced control is a flag_required refusal")
}

func TestCloneValidateRemote(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub},
		"issue", "clone", "--jql", "project = KEY", "--dry-run", "--validate-remote", "--output=json")
	env := mx4OK(t, res)
	if got := len(stub.matching(http.MethodPost, "/search/jql")); got != 1 {
		t.Fatalf("remote preview should resolve the selector, saw %d searches", got)
	}
	if got := len(stub.matching(http.MethodPost, "/rest/api/3/issue")); got != 0 {
		t.Fatalf("remote preview must create nothing, saw %d", got)
	}
	var out cloneOut
	mx4Data(t, env, &out)
	if out.Selector == nil || !out.Selector.Resolved {
		t.Fatalf("selector should be resolved, got %+v", out.Selector)
	}
	Prove(t, clonePath, "validate-remote",
		"with --dry-run it resolved the jql selector (one search, selector.resolved=true, keys enumerated) and created nothing; the bare preview makes zero requests and stays unresolved")
}

func TestCloneParallelismRange(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub}, "issue", "clone", "KEY-1", "--parallelism", "17", "--output=json")
	env := mx4ErrCode(t, res, "flag_value_invalid")
	if env.Errors[0].Field != "parallelism" {
		t.Fatalf("refusal field = %q", env.Errors[0].Field)
	}
	if got := len(stub.requests()); got != 0 {
		t.Fatalf("range refusal happens before I/O, saw %d requests", got)
	}
	Prove(t, clonePath, "parallelism",
		"--parallelism 17 was refused flag_value_invalid (field=parallelism) before any request; the default 4 would have cloned. Range proof only")
}

// ---------------------------------------------------------------------
// jira issue rank
// ---------------------------------------------------------------------

const rankPath = "jira issue rank"

type rankOut struct {
	DryRun   bool             `json:"dry_run"`
	Anchor   string           `json:"anchor"`
	Position string           `json:"position"`
	Results  []mx4KeyedResult `json:"results"`
}

func TestRankBefore(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub}, "issue", "rank", "KEY-1", "KEY-2", "--before", "KEY-9", "--output=json")
	mx4OK(t, res)
	puts := stub.matching(http.MethodPut, "/rest/agile/1.0/issue/rank")
	if len(puts) != 1 {
		t.Fatalf("expected one rank PUT, got %d", len(puts))
	}
	body := mx4Body(t, puts[0])
	if got := mx4Dig(t, body, "rankBeforeIssue"); got != "KEY-9" {
		t.Fatalf("rankBeforeIssue = %v", got)
	}
	if _, has := body["rankAfterIssue"]; has {
		t.Fatalf("rankAfterIssue must be absent on a --before request: %s", puts[0].Body)
	}
	Prove(t, rankPath, "before",
		"the one Agile PUT carried rankBeforeIssue=KEY-9 and no rankAfterIssue, with both keys in the issues array; without an anchor the argv is a flag_required refusal")
}

func TestRankAfter(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub}, "issue", "rank", "KEY-1", "--after", "KEY-9", "--output=json")
	mx4OK(t, res)
	puts := stub.matching(http.MethodPut, "/rest/agile/1.0/issue/rank")
	if len(puts) != 1 {
		t.Fatalf("expected one rank PUT, got %d", len(puts))
	}
	body := mx4Body(t, puts[0])
	if got := mx4Dig(t, body, "rankAfterIssue"); got != "KEY-9" {
		t.Fatalf("rankAfterIssue = %v", got)
	}
	if _, has := body["rankBeforeIssue"]; has {
		t.Fatalf("rankBeforeIssue must be absent on an --after request: %s", puts[0].Body)
	}
	Prove(t, rankPath, "after",
		"the one Agile PUT carried rankAfterIssue=KEY-9 and no rankBeforeIssue; without an anchor the argv is a flag_required refusal")
}

func TestRankDryRun(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub}, "issue", "rank", "KEY-1", "--before", "KEY-9", "--dry-run", "--output=json")
	env := mx4OK(t, res)
	if got := len(stub.requests()); got != 0 {
		t.Fatalf("--dry-run must contact nobody, stub saw %d requests", got)
	}
	var out rankOut
	mx4Data(t, env, &out)
	if !out.DryRun || out.Anchor != "KEY-9" {
		t.Fatalf("preview should report dry_run=true anchor=KEY-9, got %+v", out)
	}
	Prove(t, rankPath, "dry-run",
		"the preview recorded dry_run=true with ZERO stub requests, where the live run sends the Agile rank PUT")
}
