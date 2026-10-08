//go:build flagmatrix

package matrix

// The field-taking writes' shared harness: `issue create` and `issue
// edit` cases (create_test.go, edit_test.go) run the REAL binary against
// a recording loopback stub, then assert the flag's observable effect —
// the composed payload a --dry-run publishes at data.payload, the
// request the stub recorded (method, path, query, body), or a refusal by
// taxonomy code. Exit 0 and non-empty output prove nothing here; every
// assertion is written to FAIL if its flag were ignored.
//
// The env allowlist and the compile-once binary copy tests/audit/
// corpus.go's shape: base_url comes from the keyring, so HOME/XDG alone
// do not isolate this CLI — every invocation gets an explicit
// JIRA_BASE_URL aimed at the test's own stub, and the proxy pins point
// at an unroutable port so anything else dies on the loopback.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The two command paths this file's cases prove pairs on.
const (
	fwCreatePath = "jira issue create"
	fwEditPath   = "jira issue edit"
)

// Credentials and fixtures the stub serves. Neutral placeholders only —
// KEY is the example project, per the repo's rule.
const (
	fwEmail         = "person@example.com"
	fwToken         = "matrix-token"
	fwAccountID     = "5b10a2844c20165700ede21g"
	fwCreatedKey    = "KEY-77"
	fwStoredSummary = "Stored summary"
	fwFilterJQL     = "project = KEY AND labels = stale"
)

var (
	fwBinOnce sync.Once
	fwBinPath string
	fwBinErr  error
)

// fwBinary compiles the real binary exactly once per test process —
// corpus.go's compile-once guardrail, for its wall-time reason. The temp
// directory is not removed (TestMain belongs to the harness, not to this
// file), which the OS temp sweep covers.
func fwBinary(t *testing.T) string {
	t.Helper()
	fwBinOnce.Do(func() {
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			fwBinErr = err
			return
		}
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
			fwBinErr = fmt.Errorf("resolve repo root: %s has no go.mod: %w", root, err)
			return
		}
		dir, err := os.MkdirTemp("", "jira-matrix-bin-")
		if err != nil {
			fwBinErr = err
			return
		}
		fwBinPath = filepath.Join(dir, "jira")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", fwBinPath, "./cmd/jira")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			fwBinErr = fmt.Errorf("build jira binary: %w\n%s", err, out)
		}
	})
	if fwBinErr != nil {
		t.Fatalf("flagmatrix writes: %v", fwBinErr)
	}
	return fwBinPath
}

// fwRequest is one request the stub recorded, which is the wire-side
// half of the evidence: a flag that rides the query or the body has to
// show up HERE to be proved.
type fwRequest struct {
	Method   string
	Path     string
	RawQuery string
	Body     string
}

// fwStub is the recording Jira double every case in this file talks to.
// It serves the write endpoints create/edit reach — POST /issue, PUT
// /issue/{key}, the /myself lookup, the --verify readback, the saved
// filter, and /search/jql — and records every request so a case can
// assert what was (or was NOT) sent.
type fwStub struct {
	mu   sync.Mutex
	reqs []fwRequest
	srv  *httptest.Server
}

func fwNewStub(t *testing.T) *fwStub {
	t.Helper()
	s := &fwStub{}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *fwStub) url() string { return s.srv.URL }

func (s *fwStub) requests() []fwRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]fwRequest(nil), s.reqs...)
}

// find returns the recorded requests matching method and path prefix.
func (s *fwStub) find(method, pathPrefix string) []fwRequest {
	var out []fwRequest
	for _, r := range s.requests() {
		if r.Method == method && strings.HasPrefix(r.Path, pathPrefix) {
			out = append(out, r)
		}
	}
	return out
}

func (s *fwStub) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	s.mu.Lock()
	s.reqs = append(s.reqs, fwRequest{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Body: string(body)})
	s.mu.Unlock()

	user, pass, ok := r.BasicAuth()
	if !ok || user != fwEmail || pass != fwToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/myself":
		fwWriteJSON(w, http.StatusOK, `{"accountId":"`+fwAccountID+`","displayName":"Matrix User"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/filter/42":
		fwWriteJSON(w, http.StatusOK, `{"id":"42","jql":`+fwQuote(fwFilterJQL)+`}`)
	case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/search/jql":
		fwWriteJSON(w, http.StatusOK, `{"issues":[{"id":"1","key":"KEY-101"},{"id":"2","key":"KEY-102"}],"isLast":true}`)
	case r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue":
		// Jira rejects a create whose required trio went missing, so the
		// stub does too — a CLI that dropped one must not be served.
		var posted struct {
			Fields struct {
				Project   struct{ Key string }  `json:"project"`
				IssueType struct{ Name string } `json:"issuetype"`
				Summary   string                `json:"summary"`
			} `json:"fields"`
		}
		if err := json.Unmarshal(body, &posted); err != nil ||
			posted.Fields.Project.Key == "" || posted.Fields.IssueType.Name == "" || posted.Fields.Summary == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fwWriteJSON(w, http.StatusCreated, `{"id":"10101","key":"`+fwCreatedKey+`","self":"`+s.url()+`/rest/api/3/issue/10101"}`)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/"):
		// Refuse a request whose two halves are both empty, so an edit
		// that composed nothing cannot pass by sending a no-op.
		var posted struct {
			Fields map[string]json.RawMessage `json:"fields"`
			Update map[string]json.RawMessage `json:"update"`
		}
		if err := json.Unmarshal(body, &posted); err != nil || (len(posted.Fields) == 0 && len(posted.Update) == 0) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/"):
		// The --verify readback.
		key := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
		fwWriteJSON(w, http.StatusOK, `{"id":"10101","key":`+fwQuote(key)+
			`,"fields":{"summary":"`+fwStoredSummary+`","status":{"name":"To Do"},"updated":"2026-08-13T10:00:00.000+0000"}}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func fwWriteJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func fwQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// fwResult is one recorded invocation.
type fwResult struct {
	Stdout string
	Stderr string
	Exit   int
}

// fwExec runs the real binary once, in a fresh working directory with
// the scrubbed allowlist environment, --output=json prepended so every
// case reads the envelope rather than prose.
func fwExec(t *testing.T, baseURL string, files map[string]string, stdin string, args ...string) fwResult {
	t.Helper()
	return fwExecEnv(t, baseURL, files, stdin, nil, args...)
}

// fwExecEnv keeps the scrubbed execution boundary while allowing explicit
// environment fixtures, such as a home directory distinct from the workdir.
func fwExecEnv(t *testing.T, baseURL string, files map[string]string, stdin string, extraEnv []string, args ...string) fwResult {
	t.Helper()
	bin := fwBinary(t)
	workdir := t.TempDir()
	for name, content := range files {
		if filepath.IsAbs(name) || !filepath.IsLocal(name) {
			t.Fatalf("fixture %q must be a relative path inside the working directory", name)
		}
		if err := os.WriteFile(filepath.Join(workdir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"--output=json"}, args...)...)
	cmd.Dir = workdir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + workdir,
		"TMPDIR=" + workdir,
		"XDG_CONFIG_HOME=" + filepath.Join(workdir, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(workdir, "cache"),
		"HTTP_PROXY=http://127.0.0.1:1",
		"HTTPS_PROXY=http://127.0.0.1:1",
		"NO_PROXY=127.0.0.1,localhost",
		"NO_COLOR=1",
		"COLUMNS=100",
		"TERM=dumb",
		"JIRA_KEYRING=false",
		"DBUS_SESSION_BUS_ADDRESS=/dev/null",
		"XDG_RUNTIME_DIR=" + workdir,
		"JIRA_EMAIL=" + fwEmail,
		"JIRA_API_TOKEN=" + fwToken,
		"JIRA_BASE_URL=" + baseURL,
	}
	cmd.Env = append(cmd.Env, extraEnv...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	exit := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run %v: %v\nstderr: %s", args, err, stderr.String())
		}
		exit = ee.ExitCode()
	}
	return fwResult{Stdout: stdout.String(), Stderr: stderr.String(), Exit: exit}
}

// fwEnvelope is the JSON envelope as these cases read it.
type fwEnvelope struct {
	OK   bool           `json:"ok"`
	Data map[string]any `json:"data"`
	Errs []struct {
		Code  string `json:"code"`
		Msg   string `json:"message"`
		Field string `json:"field"`
	} `json:"errors"`
	Warnings []struct {
		Type string `json:"type"`
	} `json:"warnings"`
}

func fwParse(t *testing.T, r fwResult) fwEnvelope {
	t.Helper()
	var env fwEnvelope
	if err := json.Unmarshal([]byte(r.Stdout), &env); err != nil {
		t.Fatalf("stdout is not an envelope: %v\nstdout: %s\nstderr: %s", err, r.Stdout, r.Stderr)
	}
	return env
}

// fwSuccess validates execution before callers interpret or credit result data.
// fwParse also serves refusals, so decoding alone cannot establish success.
func fwSuccess(t *testing.T, r fwResult) fwEnvelope {
	t.Helper()
	env := fwParse(t, r)
	if r.Exit != 0 || !env.OK || len(env.Errs) != 0 {
		t.Fatalf("expected successful invocation, got exit=%d ok=%t errors=%d\nstdout: %s\nstderr: %s", r.Exit, env.OK, len(env.Errs), r.Stdout, r.Stderr)
	}
	return env
}

// fwHasWarning reports whether the envelope carries a warning of the
// given type.
func fwHasWarning(env fwEnvelope, typ string) bool {
	for _, w := range env.Warnings {
		if w.Type == typ {
			return true
		}
	}
	return false
}

// fwRefusal asserts the run refused with the given taxonomy code and
// field, and returns the message — the refusal-side proofs branch on
// CODE, never on wording, except where the assertion IS about which flag
// a message names (the alias rule).
func fwRefusal(t *testing.T, r fwResult, code, field string) string {
	t.Helper()
	env := fwParse(t, r)
	if env.OK || len(env.Errs) == 0 {
		t.Fatalf("expected a refusal, got ok=%t errors=%d\nstdout: %s", env.OK, len(env.Errs), r.Stdout)
	}
	if env.Errs[0].Code != code || env.Errs[0].Field != field {
		t.Fatalf("refusal is code=%q field=%q, want code=%q field=%q\nmessage: %s",
			env.Errs[0].Code, env.Errs[0].Field, code, field, env.Errs[0].Msg)
	}
	return env.Errs[0].Msg
}

// fwAt walks a decoded JSON value: strings index maps, ints index
// slices. A missing step is a fatal with the whole path named, because
// "key absent" is exactly what an ignored flag produces and the failure
// must say which key.
func fwAt(t *testing.T, v any, path ...any) any {
	t.Helper()
	cur := v
	for i, step := range path {
		switch s := step.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("path %v: step %d (%q) indexes a %T, not an object", path, i, s, cur)
			}
			next, ok := m[s]
			if !ok {
				t.Fatalf("path %v: key %q is ABSENT at step %d — the flag that should have composed it left no trace", path, s, i)
			}
			cur = next
		case int:
			list, ok := cur.([]any)
			if !ok {
				t.Fatalf("path %v: step %d (%d) indexes a %T, not an array", path, i, s, cur)
			}
			if s >= len(list) {
				t.Fatalf("path %v: index %d out of range (len %d) at step %d", path, s, len(list), i)
			}
			cur = list[s]
		default:
			t.Fatalf("path %v: unsupported step type %T", path, step)
		}
	}
	return cur
}

// fwPayload returns data.payload as a decoded object.
func fwPayload(t *testing.T, env fwEnvelope) map[string]any {
	t.Helper()
	payload, ok := fwAt(t, env.Data, "payload").(map[string]any)
	if !ok {
		t.Fatalf("data.payload is not an object")
	}
	return payload
}

// fwJSONOf re-encodes a decoded fragment so a case can substring-match
// inside it (the ADF description assertions).
func fwJSONOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("re-encode fragment: %v", err)
	}
	return string(b)
}
