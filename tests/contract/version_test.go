package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gechr/clog"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/root"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/output"
)

// hostileVersion is injected via clive's ldflag so the version string
// itself carries the byte-fidelity gauntlet: HTML-escape bait (<, >, &)
// and U+2028, the JSON-legal-but-JS-hostile separator. If any renderer
// re-escapes, the contract tests see mangled bytes in the one field
// every mode emits.
const hostileVersion = "0.0.0-fidelity<&>\u2028end"

var (
	binOnce      sync.Once
	binPath      string
	binErr       error
	auditBinOnce sync.Once
	auditBinPath string
	auditBinErr  error
)

// jiraBinary compiles the real binary once per test process, with the
// hostile version stamped through the same ldflag the release build
// uses — which also proves the injection path works at all (a mistyped
// -X target falls back silently to build info).
func jiraBinary(t *testing.T) string {
	return contractBinary(t, &binOnce, &binPath, &binErr, "")
}

// jiraOutputAuditBinary uses only a native keyring mock for migration recipes.
// It is compiled once independently; native acceptance keeps the production binary.
func jiraOutputAuditBinary(t *testing.T) string {
	return contractBinary(t, &auditBinOnce, &auditBinPath, &auditBinErr, "outputaudit")
}

func contractBinary(t *testing.T, once *sync.Once, path *string, buildErr *error, tags string) string {
	t.Helper()
	once.Do(func() {
		dir, err := os.MkdirTemp("", "jira-contract-bin-")
		if err != nil {
			*buildErr = err
			return
		}
		*path = filepath.Join(dir, "jira")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build",
			"-tags="+tags,
			"-buildvcs=false",
			"-ldflags", "-X github.com/gechr/clive.version="+hostileVersion,
			"-o", *path, "./cmd/jira")
		cmd.Dir = repoRoot()
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			*buildErr = fmt.Errorf("build jira binary: %w\n%s", err, out)
		}
	})
	if *buildErr != nil {
		t.Fatal(*buildErr)
	}
	return *path
}

func repoRoot() string {
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		panic(err)
	}
	return dir
}

// runBinary executes one invocation hermetically: scrubbed env, temp
// workdir, unroutable proxy so nothing can leave the machine.
func runBinary(t *testing.T, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	return runBinaryEnv(t, nil, args...)
}

// runBinaryEnv is runBinary with extra environment entries appended to the
// scrubbed set. The scrubbing is the point of the harness, so a caller
// names only what it genuinely needs rather than inheriting os.Environ.
func runBinaryEnv(t *testing.T, extraEnv []string, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	return runBinaryEnvStarted(t, extraEnv, nil, args...)
}

// runBinaryEnvStarted keeps the existing compile-once and scrubbed-environment
// boundary while allowing a test to signal the actual process after a request
// has started. Killing a CommandContext would skip graceful CLI cancellation.
func runBinaryEnvStarted(t *testing.T, extraEnv []string, onStart func(*os.Process), args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	return runBinaryEnvInputStarted(t, extraEnv, "", onStart, args...)
}

// runBinaryEnvInputStarted adds piped input without changing the shared binary,
// environment or graceful-process-signaling boundary.
func runBinaryEnvInputStarted(t *testing.T, extraEnv []string, stdin string, onStart func(*os.Process), args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	return runBinaryReaderStarted(t, extraEnv, strings.NewReader(stdin), onStart, args...)
}

// runBinaryReaderStarted also accepts native pipe descriptors for process IO
// tests; the original string-input entry point keeps the same behavior.
func runBinaryReaderStarted(t *testing.T, extraEnv []string, stdin io.Reader, onStart func(*os.Process), args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	return runBinaryIOStarted(t, extraEnv, stdin, nil, onStart, args...)
}

// runBinaryIOStarted gives terminal-dependent commands borrowed stdin/stderr
// descriptors while retaining the compile-once binary and hermetic environment.
func runBinaryIOStarted(t *testing.T, extraEnv []string, stdin io.Reader, terminalStderr *os.File, onStart func(*os.Process), args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	return runBinaryIOInDirStarted(t, t.TempDir(), extraEnv, stdin, terminalStderr, onStart, args...)
}

// runBinaryIOInDirStarted preserves the shared execution boundary while allowing
// filesystem-sensitive tests to select characters TempDir removes from names.
func runBinaryIOInDirStarted(t *testing.T, workdir string, extraEnv []string, stdin io.Reader, terminalStderr *os.File, onStart func(*os.Process), args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	bin := jiraBinary(t)
	return runExecutableIOInDirStarted(t, bin, workdir, extraEnv, stdin, terminalStderr, onStart, args...)
}

// runExecutableIOInDirStarted also runs owned copies of the cached CLI, so
// installation-dependent contracts can select a path without rebuilding it.
func runExecutableIOInDirStarted(t *testing.T, bin, workdir string, extraEnv []string, stdin io.Reader, terminalStderr *os.File, onStart func(*os.Process), args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workdir
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + workdir,
		"HTTP_PROXY=http://127.0.0.1:1",
		"HTTPS_PROXY=http://127.0.0.1:1",
		"NO_COLOR=1",
		"TERM=dumb",
	}
	if runtime.GOOS == "windows" {
		// Bind Windows configuration and cache discovery to the owned fixture,
		// just as HOME does for Unix. Never inherit the user's profile stores.
		cmd.Env = append(cmd.Env,
			// PowerShell needs executable extensions even for a literal .exe path.
			"PATHEXT="+os.Getenv("PATHEXT"),
			"USERPROFILE="+workdir,
			"APPDATA="+filepath.Join(workdir, "AppData", "Roaming"),
			"LOCALAPPDATA="+filepath.Join(workdir, "AppData", "Local"),
		)
	}
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Stdin = stdin
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if terminalStderr != nil {
		cmd.Stderr = terminalStderr
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %v: %v", args, err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	if onStart != nil {
		onStart(cmd.Process)
	}
	err := cmd.Wait()
	waited = true
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run %v: %v", args, err)
		}
		exit = exitErr.ExitCode()
	}
	return outBuf.String(), errBuf.String(), exit
}

// frame mirrors the envelope for decoding; data stays raw so tests can
// hold the exact bytes against the compact rendering.
type frame struct {
	OK   bool `json:"ok"`
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

var requestIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// TestVersionJSONFrame pins the machine contract's success shape end to
// end through the real binary: full frame, contract_version stamped,
// UTC RFC 3339 clock, correlation id, no exit_code on success, and the
// documented data fields — nothing more.
func TestVersionJSONFrame(t *testing.T) {
	t.Parallel()
	stdout, stderr, exit := runBinary(t, "version", "--output=json")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %q)", exit, stderr)
	}
	if stderr != "" {
		t.Errorf("machine-mode success wrote to stderr: %q", stderr)
	}

	var f frame
	if err := json.Unmarshal([]byte(stdout), &f); err != nil {
		t.Fatalf("stdout is not a frame: %v\nstdout: %q", err, stdout)
	}
	if !f.OK {
		t.Error("ok = false on success")
	}
	if f.Meta.Command != "version" {
		t.Errorf("meta.command = %q, want version", f.Meta.Command)
	}
	if f.Meta.ContractVersion != "1" {
		t.Errorf("meta.contract_version = %q, want 1", f.Meta.ContractVersion)
	}
	ts, err := time.Parse(time.RFC3339, f.Meta.Timestamp)
	if err != nil || !strings.HasSuffix(f.Meta.Timestamp, "Z") {
		t.Errorf("meta.timestamp = %q, want RFC 3339 UTC (err: %v)", f.Meta.Timestamp, err)
	}
	if time.Since(ts) > time.Hour {
		t.Errorf("meta.timestamp %q is not a fresh clock reading", f.Meta.Timestamp)
	}
	if !requestIDRe.MatchString(f.Meta.RequestID) {
		t.Errorf("meta.request_id = %q, want 32 hex chars", f.Meta.RequestID)
	}
	if f.Meta.ExitCode != nil {
		t.Errorf("meta.exit_code = %d on success, want absent", *f.Meta.ExitCode)
	}
	if f.Errors == nil || len(f.Errors) != 0 {
		t.Errorf("errors = %v, want present empty array", f.Errors)
	}
	if f.Warnings == nil || len(f.Warnings) != 0 {
		t.Errorf("warnings = %v, want present empty array", f.Warnings)
	}

	var data map[string]any
	if err := json.Unmarshal(f.Data, &data); err != nil {
		t.Fatalf("data does not decode: %v", err)
	}
	assertVersionData(t, data)

	// The whole frame — meta included — obeys snake_case, checked on the
	// real bytes rather than the typed mirror above.
	var raw map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatal(err)
	}
	assertSnakeCaseKeys(t, "$", raw)
}

// assertVersionData holds the payload to the published field set — the
// hand-written half the schema derivation can never provide.
func assertVersionData(t *testing.T, data map[string]any) {
	t.Helper()
	for _, key := range []string{"version", "go_version", "platform"} {
		if _, ok := data[key]; !ok {
			t.Errorf("data.%s missing", key)
		}
	}
	allowed := map[string]bool{
		"version": true, "commit": true, "dirty": true,
		"build_date": true, "go_version": true, "platform": true,
	}
	for key := range data {
		if !allowed[key] {
			t.Errorf("data.%s is not a published field", key)
		}
	}
	if got, _ := data["go_version"].(string); got != runtime.Version() {
		t.Errorf("data.go_version = %q, want %q", got, runtime.Version())
	}
	platform, _ := data["platform"].(map[string]any)
	if platform["os"] != runtime.GOOS || platform["arch"] != runtime.GOARCH {
		t.Errorf("data.platform = %v, want %s/%s", platform, runtime.GOOS, runtime.GOARCH)
	}
	// Built with -buildvcs=false: the VCS trio must be ABSENT — not
	// null, not zeroed — per the null-vs-absent contract.
	for _, key := range []string{"commit", "dirty", "build_date"} {
		if v, ok := data[key]; ok {
			t.Errorf("data.%s = %v, want absent in an unstamped build", key, v)
		}
	}
}

var snakeCaseRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func assertSnakeCaseKeys(t *testing.T, path string, v any) {
	t.Helper()
	obj, ok := v.(map[string]any)
	if !ok {
		return
	}
	for key, val := range obj {
		if !snakeCaseRe.MatchString(key) {
			t.Errorf("key %s.%s is not snake_case", path, key)
		}
		assertSnakeCaseKeys(t, path+"."+key, val)
	}
}

// TestVersionCompactIsBareJSONData pins R2's lean half: compact success
// is the data object alone — no frame — and byte-identical in content
// to what the json frame carries at .data.
func TestVersionCompactIsBareJSONData(t *testing.T) {
	t.Parallel()
	compactOut, _, exit := runBinary(t, "version", "--output=compact")
	if exit != 0 {
		t.Fatalf("compact exit = %d, want 0", exit)
	}
	var compact map[string]any
	if err := json.Unmarshal([]byte(compactOut), &compact); err != nil {
		t.Fatalf("compact stdout does not decode: %v\nstdout: %q", err, compactOut)
	}
	for _, key := range []string{"ok", "meta", "errors", "warnings"} {
		if _, has := compact[key]; has {
			t.Errorf("compact success carries frame key %q — compact must be bare data", key)
		}
	}
	assertVersionData(t, compact)

	jsonOut, _, exit := runBinary(t, "version", "--output=json")
	if exit != 0 {
		t.Fatalf("json exit = %d, want 0", exit)
	}
	var f frame
	if err := json.Unmarshal([]byte(jsonOut), &f); err != nil {
		t.Fatal(err)
	}
	var jsonData map[string]any
	if err := json.Unmarshal(f.Data, &jsonData); err != nil {
		t.Fatal(err)
	}
	// Two separate invocations must serialize the same payload: the
	// determinism axis, checked by execution.
	if !reflect.DeepEqual(compact, jsonData) {
		t.Errorf("compact data %v != json frame data %v", compact, jsonData)
	}
}

// TestVersionHumanMatchesVersionFlag pins the one-source rule: the
// human render and conductor's -V print the identical line, because
// both read the root's resolved version rather than formatting their
// own.
func TestVersionHumanMatchesVersionFlag(t *testing.T) {
	t.Parallel()
	humanOut, _, exit := runBinary(t, "version", "--output=human")
	if exit != 0 {
		t.Fatalf("human exit = %d, want 0", exit)
	}
	flagOut, _, exit := runBinary(t, "-V")
	if exit != 0 {
		t.Fatalf("-V exit = %d, want 0", exit)
	}
	if humanOut != flagOut {
		t.Errorf("`version` human output %q != `-V` output %q — the two must share one source", humanOut, flagOut)
	}
	if !strings.Contains(flagOut, hostileVersion) {
		t.Errorf("-V output %q does not carry the injected version — the ldflag did not apply", flagOut)
	}
}

// TestVersionByteFidelity is the round-3 regression fence run through
// the real binary: <, >, & and U+2028 in a data value must reach the
// rendered bytes verbatim in both machine modes. encoding/json's
// default HTML escaping (<…) is exactly the corruption this pins
// out.
func TestVersionByteFidelity(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		stdout, _, exit := runBinary(t, "version", "--output="+mode)
		if exit != 0 {
			t.Fatalf("%s exit = %d, want 0", mode, exit)
		}
		if !strings.Contains(stdout, hostileVersion) {
			t.Errorf("%s output does not carry the hostile version verbatim:\n%q", mode, stdout)
		}
		for _, escape := range []string{`\u003c`, `\u003e`, `\u0026`, `\u2028`} {
			if strings.Contains(stdout, escape) {
				t.Errorf("%s output re-escaped a byte the contract preserves (%s):\n%q", mode, escape, stdout)
			}
		}
	}
}

// countingDeps returns deps whose Jira constructor counts calls — the
// R6 lazy-root probe — with the clog default restored afterwards
// (conductor reconfigures package globals on every construction).
func countingDeps(t *testing.T) (*root.Deps, *int) {
	t.Helper()
	orig := clog.Default()
	t.Cleanup(func() { clog.SetDefault(orig) })
	calls := 0
	return &root.Deps{
		JiraClient: func(ctx context.Context) (core.JiraClient, error) {
			calls++
			return nil, errors.New("contract: version must not construct a client")
		},
		Detect: func() cli.Detection {
			return cli.Detection{Format: output.FormatJSON}
		},
	}, &calls
}

// TestVersionConstructsNoClient pins the lazy-root contract for the
// version path in every mode: saying what the binary is needs no site,
// no credentials, no client.
func TestVersionConstructsNoClient(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			deps, calls := countingDeps(t)
			var stdout bytes.Buffer
			program := root.NewProgram(deps, &stdout, &bytes.Buffer{})
			if exit := program.Run(t.Context(), []string{"version", "--output=" + mode}); exit != 0 {
				t.Fatalf("exit = %d, want 0", exit)
			}
			if *calls != 0 {
				t.Errorf("jira client constructed %d times by `version`, want 0", *calls)
			}
			if stdout.Len() == 0 {
				t.Error("version wrote nothing to stdout")
			}
		})
	}
}

// failOnceWriter fails the first Write and works afterwards: the
// success envelope's write dies, then the exit boundary's failure frame
// gets through — which is how a command whose only I/O is its output
// can be forced onto the taxonomy path at all.
type failOnceWriter struct {
	failed bool
	buf    bytes.Buffer
}

func (w *failOnceWriter) Write(p []byte) (int, error) {
	if !w.failed {
		w.failed = true
		return 0, errors.New("synthetic destination failure")
	}
	return w.buf.Write(p)
}

// TestVersionForcedFailureEmitsTaxonomyFrame is the round-3 fence for
// the failure surface: a post-resolution failure must exit with the
// taxonomy's code AND land the full error frame — contract_version
// included — on stdout in every machine mode.
func TestVersionForcedFailureEmitsTaxonomyFrame(t *testing.T) {
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			deps, _ := countingDeps(t)
			w := &failOnceWriter{}
			program := root.NewProgram(deps, w, &bytes.Buffer{})
			exit := program.Run(t.Context(), []string{"version", "--output=" + mode})
			if exit != cli.ExitOutput {
				t.Fatalf("exit = %d, want the output_write_failed contract code %d", exit, cli.ExitOutput)
			}

			var f frame
			if err := json.Unmarshal(w.buf.Bytes(), &f); err != nil {
				t.Fatalf("no failure frame on stdout: %v\nstdout: %q", err, w.buf.String())
			}
			if f.OK {
				t.Error("ok = true on a failure frame")
			}
			if f.Meta.ContractVersion != "1" {
				t.Errorf("failure frame meta.contract_version = %q, want 1", f.Meta.ContractVersion)
			}
			if f.Meta.ExitCode == nil || *f.Meta.ExitCode != cli.ExitOutput {
				t.Errorf("meta.exit_code = %v, want %d", f.Meta.ExitCode, cli.ExitOutput)
			}
			if len(f.Errors) != 1 {
				t.Fatalf("errors = %d entries, want 1", len(f.Errors))
			}
			var e struct {
				Code string `json:"code"`
				Hint string `json:"hint"`
			}
			if err := json.Unmarshal(f.Errors[0], &e); err != nil {
				t.Fatal(err)
			}
			if e.Code != "output_write_failed" {
				t.Errorf("errors[0].code = %q, want output_write_failed", e.Code)
			}
			if e.Hint == "" {
				t.Error("errors[0].hint is empty — the taxonomy guarantees one")
			}
		})
	}
}
