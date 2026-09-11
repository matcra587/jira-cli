package unit

// Local-validation tests for `jira issue attachment …`.
//
// These exercise the CLI's local pre-flight checks: file-existence /
// readability before `attachment add` issues any HTTP, and
// clobber-protect for `attachment download --output PATH`.
//
// Local validation: the CLI MUST locally validate attachment file
// paths exist and are readable before any HTTP call.
//
// Clobber-protect: the CLI must refuse to overwrite an existing file
// unless --force is passed.
//
// Both checks MUST exit 3 (validation).

import (
	"bytes"
	stdlibErrors "errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAttachmentAddRejectsMissingFileBeforeHTTP(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	cfg := attachmentTestConfig(t, srv.URL)
	t.Setenv("JIRA_TOKEN_DEFAULT", "test-token")

	bin := buildAttachmentTestBinary(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist.bin")
	// `attachment add` has no interactive prompts so --no-input is not
	// needed here; the local pre-flight check fires before any TUI
	// branch is even considered.
	cmd := exec.Command(bin, "--config", cfg, "--output=json",
		"issue", "attachment", "add", "PROJ-1", "--file", missing)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("attachment add for missing file succeeded; want exit 3")
	}
	var exitErr *exec.ExitError
	if !stdlibErrors.As(err, &exitErr) {
		t.Fatalf("err is not *exec.ExitError: %v", err)
	}
	if exitErr.ExitCode() != 3 {
		t.Fatalf("exit code = %d, want 3 (validation)", exitErr.ExitCode())
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("HTTP server received %d requests; want 0 (local validation BEFORE any HTTP call)", got)
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, missing) {
		t.Fatalf("error output does not echo the missing path %q:\nstdout=%s\nstderr=%s",
			missing, stdout.String(), stderr.String())
	}
}

func TestAttachmentDownloadClobberProtectExitsBeforeHTTP(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	cfg := attachmentTestConfig(t, srv.URL)
	t.Setenv("JIRA_TOKEN_DEFAULT", "test-token")

	existing := filepath.Join(t.TempDir(), "keep.bin")
	if err := os.WriteFile(existing, []byte("LOCAL"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bin := buildAttachmentTestBinary(t)
	// `attachment download` clobber-protect is a local pre-flight check
	// — fires before any HTTP call and before any TUI branch — so
	// --no-input is not needed.
	cmd := exec.Command(bin, "--config", cfg, "--output=json",
		"issue", "attachment", "download", "PROJ-1", "10042", "--output", existing)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("download to existing path without --force succeeded; want exit 3")
	}
	var exitErr *exec.ExitError
	if !stdlibErrors.As(err, &exitErr) {
		t.Fatalf("err is not *exec.ExitError: %v", err)
	}
	if exitErr.ExitCode() != 3 {
		t.Fatalf("exit code = %d, want 3 (clobber-protect)", exitErr.ExitCode())
	}
	// Local pre-flight check: the existence check happens BEFORE the
	// download HTTP call, so the server should not be hit.
	if got := hits.Load(); got != 0 {
		t.Fatalf("HTTP server received %d requests; want 0 (clobber-protect must short-circuit before HTTP)", got)
	}
	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "LOCAL" {
		t.Fatalf("existing file was clobbered: contents = %q", string(got))
	}
}

// attachmentTestConfig writes a minimal config.toml suitable for
// driving the CLI binary against the supplied test server. Mirrors the
// config helper used by the contract suite.
func attachmentTestConfig(t *testing.T, baseURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	content := `default_profile = "default"
queries_path = "` + filepath.ToSlash(t.TempDir()) + `/queries"

[[profiles]]
name = "default"
base_url = "` + baseURL + `"
auth_type = "token"
secret_backend = "keyring"
refresh_interval = 30
timeout = 30
workday_seconds = 28800
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// buildAttachmentTestBinary builds the jira binary into a temp dir and
// returns its path. We rebuild per-test rather than share across the
// unit suite because tests/unit doesn't have a TestMain that can
// amortize the cost; the Go test cache absorbs the body of the build.
// Suffix `Attachment` keeps the helper from colliding with sibling
// teams' build helpers in the same package.
func buildAttachmentTestBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "jira")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/jira")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build jira error = %v\n%s", err, out)
	}
	return bin
}
