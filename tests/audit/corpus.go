//go:build outputaudit

package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/core"
)

// Capture is one recorded invocation: a scenario run in one output
// mode, canonicalised so captures compare stably across runs.
type Capture struct {
	Scenario Scenario
	Mode     Mode
	Stdout   string
	Stderr   string
	ExitCode int
	// WroteFiles reports whether the run changed any file under its own
	// HOME/XDG tree. A dry-run that writes is the failure this exists to
	// make visible.
	WroteFiles bool
}

var (
	binOnce sync.Once
	binDir  string
	binPath string
	binErr  error
)

// jiraBinary compiles the real binary exactly once per test process —
// a per-test build would dominate suite wall time (the 0.15 scar the
// compile-once guardrail encoded). CGO stays off, matching the release
// posture.
func jiraBinary() (string, error) {
	binOnce.Do(func() {
		var rootDir string
		rootDir, binErr = repoRoot()
		if binErr != nil {
			return
		}
		binDir, binErr = os.MkdirTemp("", "jira-audit-bin-")
		if binErr != nil {
			return
		}
		binPath = filepath.Join(binDir, "jira")
		if runtime.GOOS == "windows" {
			binPath += ".exe"
		}
		buildCtx, cancelBuild := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancelBuild()
		// -buildvcs=false: the harness needs no version stamp, and VCS probing
		// fails in stripped checkouts (scratch worktrees, CI shallow clones).
		cmd := exec.CommandContext(buildCtx, "go", "build", "-tags=outputaudit", "-buildvcs=false", "-o", binPath, "./cmd/jira")
		cmd.Dir = rootDir
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			binErr = fmt.Errorf("build jira binary: %w\n%s", err, out)
		}
	})
	return binPath, binErr
}

// cleanupBinary removes the compiled binary; called from TestMain after
// the run.
func cleanupBinary() {
	if binDir != "" {
		_ = os.RemoveAll(binDir) //nolint:errcheck // best-effort temp cleanup after the run; nothing can act on the failure
	}
}

// repoRoot resolves the module root from the suite's working directory
// (go test runs the package in tests/audit) and refuses to guess: a
// wrong root would silently build a different binary.
func repoRoot() (string, error) {
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return "", fmt.Errorf("resolve repo root: %s has no go.mod: %w", dir, err)
	}
	return dir, nil
}

var (
	corpusOnce sync.Once
	corpusData map[string]Capture
	corpusErr  error
)

// corpus builds every scenario × mode capture once per test process,
// keyed "<scenario id>|<mode>".
func corpus() (map[string]Capture, error) {
	corpusOnce.Do(func() {
		corpusData, corpusErr = buildCorpus()
	})
	return corpusData, corpusErr
}

func buildCorpus() (map[string]Capture, error) {
	bin, err := jiraBinary()
	if err != nil {
		return nil, err
	}
	out := map[string]Capture{}
	for _, s := range scenarios {
		var baseURL string
		var closeStub func()
		if s.Stub != "" {
			handler, ok := stubServers[s.Stub]
			if !ok {
				return nil, fmt.Errorf("scenario %q references unknown stub %q", s.ID, s.Stub)
			}
			srv := httptest.NewServer(handler())
			baseURL, closeStub = srv.URL, srv.Close
		}
		for _, mode := range modesFor(s.Category) {
			cap, err := runScenario(bin, s, mode, baseURL)
			if err != nil {
				if closeStub != nil {
					closeStub()
				}
				return nil, err
			}
			out[s.ID+"|"+string(mode)] = cap
		}
		if closeStub != nil {
			closeStub()
		}
	}
	return out, nil
}

// captureFor finds the capture backing one cell coordinate: the recipe
// with the given op+variant+category, run in the given mode.
func captureFor(captures map[string]Capture, op, variant string, category Category, mode Mode) (Capture, bool) {
	for _, s := range scenarios {
		if s.Op == op && s.Variant == variant && s.Category == category {
			capture, ok := captures[s.ID+"|"+string(mode)]
			return capture, ok
		}
	}
	return Capture{}, false
}

// outputSelectorFor maps an audit Mode to the value the CLI's own
// --output flag accepts. Every mode maps to itself except
// ModeHumanColor, whose selector value is "human" — the CLI has never
// heard of "human_color"; it is where the audit records a SECOND capture
// of the same human render with color forced, not a distinct render
// path the binary itself needs to know about.
func outputSelectorFor(mode Mode) string {
	if mode == ModeHumanColor {
		return string(ModeHuman)
	}
	return string(mode)
}

// runScenario executes one scenario in one mode inside a fresh working
// directory with a scrubbed environment. Isolation is per MODE, not per
// scenario: a shared workdir or cache would let one mode's side effects
// make a later mode diverge, an artifact the cross-mode checks would
// misread as a CLI inconsistency (a live 0.15 scar).
func runScenario(bin string, s Scenario, mode Mode, baseURL string) (Capture, error) {
	workdir, err := os.MkdirTemp("", "jira-audit-work-")
	if err != nil {
		return Capture{}, err
	}
	defer os.RemoveAll(workdir) //nolint:errcheck // best-effort temp cleanup; the capture is already taken

	args := []string{"--output=" + outputSelectorFor(mode)}
	if mode == ModeHumanColor {
		// human_color is an audit concept, not a CLI value — the real
		// output selector stays "human"; color is forced through the
		// documented --color surface instead (ResolveColorMode's
		// clog.ColorAlways branch answers true unconditionally, so this
		// does not depend on the harness's pipes ever looking like a
		// terminal — see color.go).
		args = append(args, "--color=always")
	}
	args = append(args, s.Args...)
	// A hung scenario must fail NAMED, not ride to the package timeout and
	// dump every goroutine.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if s.Executable != "" {
		if !filepath.IsLocal(s.Executable) {
			return Capture{}, fmt.Errorf("scenario %q executable must stay inside its owned directory", s.ID)
		}
		copyPath := filepath.Join(workdir, s.Executable)
		if err := os.MkdirAll(filepath.Dir(copyPath), 0o700); err != nil {
			return Capture{}, err
		}
		if err := xos.CopyFile(bin, copyPath); err != nil {
			return Capture{}, err
		}
		bin = copyPath
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workdir
	cmd.Env = scenarioEnv(workdir, s, baseURL, mode)
	if s.Stdin != "" {
		cmd.Stdin = strings.NewReader(s.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	var finishTerminal func() error
	if s.Terminal {
		if s.Stdin != "" {
			return Capture{}, fmt.Errorf("scenario %q cannot combine terminal and piped input", s.ID)
		}
		finishTerminal, err = attachAuditTerminal(cmd, &stderr)
		if err != nil {
			return Capture{}, fmt.Errorf("scenario %q terminal invocation is unrun: %w", s.ID, err)
		}
		defer func() { _ = finishTerminal() }() //nolint:errcheck // closes admission failures; completed runs check the first close outcome below
	}

	// Seed the recipe's input files BEFORE the fingerprint below, not
	// after. A file-fed input route (--json-input, --markdown-file) needs
	// a real file on disk, and a fixture written after the baseline was
	// taken would look exactly like a write the command made — which
	// would fail every dry-run recipe that reads one, for a file the
	// harness itself created.
	if err := seedFiles(workdir, s.Files); err != nil {
		return Capture{}, fmt.Errorf("scenario %q mode %s: %w", s.ID, mode, err)
	}
	if len(s.SetupArgs) != 0 {
		setup := exec.CommandContext(ctx, bin, append([]string{"--output=json"}, s.SetupArgs...)...)
		setup.Dir, setup.Env = workdir, cmd.Env
		body, err := setup.Output()
		if err != nil {
			return Capture{}, fmt.Errorf("scenario %q setup did not run successfully: %w", s.ID, err)
		}
		var frame struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(body, &frame); err != nil || !frame.OK {
			return Capture{}, fmt.Errorf("scenario %q setup produced no successful envelope", s.ID)
		}
	}

	// Fingerprint the state a run is allowed to touch, so a dry-run that
	// writes cannot pass by talking about not writing. Taken around the
	// command rather than asserted inside it: the point is to observe
	// the binary from outside, the way a caller would.
	before, err := fingerprint(workdir)
	if err != nil {
		return Capture{}, err
	}

	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return Capture{}, fmt.Errorf("scenario %q mode %s: %w", s.ID, mode, err)
		}
		code = exit.ExitCode()
	}
	if finishTerminal != nil {
		if err := finishTerminal(); err != nil {
			return Capture{}, fmt.Errorf("scenario %q terminal capture failed: %w", s.ID, err)
		}
	}
	after, err := fingerprint(workdir)
	if err != nil {
		return Capture{}, err
	}

	return Capture{
		Scenario:   s,
		Mode:       mode,
		Stdout:     canonicalize(stdout.String()),
		Stderr:     canonicalize(stderr.String()),
		ExitCode:   code,
		WroteFiles: !maps.Equal(before, after),
	}, nil
}

// seedFiles writes a recipe's input fixtures into its working directory.
//
// Relative names only, resolved against the sandboxed workdir that is
// also the command's cwd — so a recipe writes `--json-input body.json`
// and gets the file this created, with no absolute path baked into a
// table that has to run on anyone's machine. An absolute or escaping
// name is refused rather than written, because a recipe that reached
// outside the sandbox would be exactly the corpus touching real state.
func seedFiles(workdir string, files map[string]string) error {
	for name, content := range files {
		if filepath.IsAbs(name) || !filepath.IsLocal(name) {
			return fmt.Errorf("fixture %q must be a relative path inside the working directory", name)
		}
		path := filepath.Join(workdir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// fingerprint records every file under root against a hash of its
// contents. Contents, not just names: an in-place rewrite leaves the
// tree looking identical, and that is exactly the write a preview must
// not make.
func fingerprint(root string) (map[string]string, error) {
	seen := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A file that vanished mid-walk is not a fingerprint failure.
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			// The same race, one step later: listed, then gone before the
			// read.
			if errors.Is(readErr, fs.ErrNotExist) {
				return nil
			}
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		seen[rel] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("fingerprint %s: %w", root, err)
	}
	return seen, nil
}

// canonPatterns masks volatile values so captures and the committed
// matrix stay stable across runs: correlation ids, clock reads,
// ephemeral ports, and temp paths.
var canonPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`"request_id":"[0-9a-f]{32}"`), `"request_id":"<CANON>"`},
	// Deliberately strict: only UTC RFC 3339 masks, so a timestamp that
	// broke the M5 contract stays visible — the M5 check reads "the mask
	// matched" as "the clock met the contract".
	{regexp.MustCompile(`"timestamp":"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z"`), `"timestamp":"<CANON>"`},
	// Board snapshots publish a UTC RFC 3339 nanosecond fetch timestamp.
	{regexp.MustCompile(`"fetched_at":"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z"`), `"fetched_at":"<CANON>"`},
	// Metadata primers acknowledge a UTC local cache publication timestamp.
	{regexp.MustCompile(`"written_at":"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z"`), `"written_at":"<CANON>"`},
	{regexp.MustCompile(`127\.0\.0\.1:\d+`), `<CANON>`},
	{regexp.MustCompile(`[^"\s]*jira-audit-(bin|work)-[^"\s]*`), `<CANON>`},
}

var credentialBackupPattern = regexp.MustCompile(`"credential_backup":"([^"\\]*)"`)

func canonicalize(s string) string {
	s = credentialBackupPattern.ReplaceAllStringFunc(s, func(member string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(member, `"credential_backup":"`), `"`)
		if core.ValidCredentialBackupID(name) {
			return `"credential_backup":"<CANON>"`
		}
		return member
	})
	for _, p := range canonPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

// scenarioEnv builds the scrubbed environment a capture runs in.
//
// An allowlist, never os.Environ(): the developer's shell (JIRA_*
// variables, agent markers, color settings) must not reach a capture.
// The unroutable proxy is the hermeticity guard — anything not aimed at
// the loopback stub dies at 127.0.0.1:1 instead of leaving the machine.
//
// Extracted so a guardrail can assert what is in it, because one of
// these entries exists for a reason no reader would guess.
//
// mode gates exactly one entry: NO_COLOR=1 is pinned for every mode
// EXCEPT ModeHumanColor, whose whole reason to exist is to see the SGR
// bytes NO_COLOR exists to suppress. Color itself is forced by
// runScenario's own --color=always argument, not by an env var here —
// ResolveColorMode's ColorAlways branch (color.go) never consults
// NO_COLOR at all, so leaving NO_COLOR unset for this mode is belt to
// that branch's braces, not the actual mechanism; it stays unset anyway
// so a future refactor that routes back through ColorAuto cannot
// silently reintroduce the blindness this mode exists to end.
func scenarioEnv(workdir string, s Scenario, baseURL string, mode Mode) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + workdir,
		"TMPDIR=" + workdir,
		"XDG_CONFIG_HOME=" + filepath.Join(workdir, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(workdir, "cache"),
		"HTTP_PROXY=http://127.0.0.1:1",
		"HTTPS_PROXY=http://127.0.0.1:1",
		"NO_PROXY=127.0.0.1,localhost",
		// Fixed width, so layout stays deterministic in EVERY mode,
		// human_color included.
		"COLUMNS=100",
		"TERM=dumb",
	}
	if mode != ModeHumanColor {
		env = append(env, "NO_COLOR=1")
	}
	env = append(env, []string{
		// Cut the process off from the machine's real credential store.
		// HOME and XDG_* sandbox the filesystem, but the OS keyring is a
		// SESSION service and ignores both — without this the corpus
		// reads (and prints) whatever the developer or the CI runner is
		// actually logged in with. It did exactly that once.
		//
		// Hence the binary's own switch, not just the scrubbed session
		// bus: cutting the bus covers Linux, but macOS shells out to an
		// absolute /usr/bin/security that no environment scrubbing can
		// reach. The switch is the guarantee, the bus scrub is the belt
		// to its braces.
		"JIRA_KEYRING=false",
		"DBUS_SESSION_BUS_ADDRESS=/dev/null",
		"XDG_RUNTIME_DIR=" + workdir,
	}...)
	env = append(env, s.Env...)
	if baseURL != "" {
		env = append(env, "JIRA_BASE_URL="+baseURL)
	}
	return env
}
