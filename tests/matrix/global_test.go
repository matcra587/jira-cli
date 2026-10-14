//go:build flagmatrix

package matrix

// The global machinery's proving cases: the root's output selector and
// its hidden aliases, the jq surface, the standard trio, --config,
// --timeout, --no-input, -V/--version, --help, the completion flags, and
// the auth/config leaf flags.
//
// The inherited-pair rule does the accounting: a global flag's proof is
// exercised through whichever descendant makes its effect observable
// (`version` for shapes, `issue delete` for refusal-path stderr,
// `auth status` for the transport), and the registry credits the
// DECLARING command — root — via Pair.OwnerKey.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gechr/x/shell"
)

const rootPath = "jira"

// ---------------------------------------------------------------------
// The output selector and its hidden aliases.
// ---------------------------------------------------------------------

func TestGlobalOutputSelectsDocumentShape(t *testing.T) {
	jsonRun := mx4Run(t, mx4Opts{}, "version", "--output=json")
	env := mx4OK(t, jsonRun)
	if env.Meta.Command != "version" {
		t.Fatalf("json mode should carry the full frame, meta.command = %q", env.Meta.Command)
	}

	compactRun := mx4Run(t, mx4Opts{}, "version", "--output=compact")
	var bare map[string]any
	if err := json.Unmarshal([]byte(compactRun.Stdout), &bare); err != nil {
		t.Fatalf("compact stdout is not JSON: %v\n%s", err, compactRun.Stdout)
	}
	if _, framed := bare["meta"]; framed {
		t.Fatalf("compact success must be BARE data, found a meta frame:\n%s", compactRun.Stdout)
	}
	if _, ok := bare["go_version"]; !ok {
		t.Fatalf("compact bare data should be the version payload:\n%s", compactRun.Stdout)
	}

	humanRun := mx4Run(t, mx4Opts{}, "version", "--output=human")
	if json.Valid([]byte(humanRun.Stdout)) {
		t.Fatalf("human mode must not emit JSON:\n%s", humanRun.Stdout)
	}
	if strings.TrimSpace(humanRun.Stdout) == "" {
		t.Fatalf("human version printed nothing")
	}
	Prove(t, rootPath, "output",
		"the three values produce three document shapes from one command: json a full frame (ok/meta/data), compact bare data with no frame, human non-JSON prose — an ignored selector could not produce all three")
}

// mx4AliasStdout runs `version` under one hidden alias and returns raw
// stdout for shape asserts.
func mx4AliasStdout(t *testing.T, alias string) string {
	t.Helper()
	res := mx4Run(t, mx4Opts{}, "version", "--"+alias)
	if res.Exit != 0 {
		t.Fatalf("--%s: exit %d\n%s", alias, res.Exit, res.Stderr)
	}
	return res.Stdout
}

func TestGlobalJSONAlias(t *testing.T) {
	env := mx4Frame(t, mx4AliasStdout(t, "json"))
	if !env.OK || env.Meta.Command != "version" {
		t.Fatalf("--json should select the full frame")
	}
	// Two disagreeing selectors are refused, and the refusal names only
	// canonical vocabulary. Hidden aliases resolve BEFORE dispatch, so
	// the code is the selector conflict's own.
	conflict := mx4Run(t, mx4Opts{}, "version", "--json", "--human")
	conflictEnv := mx4ErrCode(t, conflict, "validation_failed")
	if conflictEnv.Meta.ExitCode == nil || *conflictEnv.Meta.ExitCode != 3 {
		t.Fatalf("selector conflict should exit 3, got %v", conflict.Exit)
	}
	Prove(t, rootPath, "json",
		"hidden --json selected the full envelope frame (default non-TTY would too, but --json --human proved the alias is READ: the pair is refused validation_failed exit 3 as disagreeing selectors)")
}

func TestGlobalCompactAlias(t *testing.T) {
	stdout := mx4AliasStdout(t, "compact")
	var bare map[string]any
	if err := json.Unmarshal([]byte(stdout), &bare); err != nil {
		t.Fatalf("compact stdout is not JSON: %v", err)
	}
	if _, framed := bare["meta"]; framed {
		t.Fatalf("--compact must produce bare data, found a frame:\n%s", stdout)
	}
	if _, ok := bare["go_version"]; !ok {
		t.Fatalf("--compact bare data should be the version payload:\n%s", stdout)
	}
	Prove(t, rootPath, "compact",
		"hidden --compact switched the same command from the non-TTY default (full frame) to bare data with no meta frame")
}

func TestGlobalHumanAlias(t *testing.T) {
	stdout := mx4AliasStdout(t, "human")
	if json.Valid([]byte(stdout)) || strings.TrimSpace(stdout) == "" {
		t.Fatalf("--human should force non-JSON prose, got:\n%s", stdout)
	}
	Prove(t, rootPath, "human",
		"hidden --human forced the human render (non-JSON prose on stdout) where the non-TTY default emits the JSON frame")
}

func TestGlobalPlainAlias(t *testing.T) {
	stdout := mx4AliasStdout(t, "plain")
	if json.Valid([]byte(stdout)) || strings.TrimSpace(stdout) == "" {
		t.Fatalf("--plain should force the human surface, got:\n%s", stdout)
	}
	Prove(t, rootPath, "plain",
		"hidden --plain forced the human surface (non-JSON prose) where the non-TTY default emits the JSON frame")
}

// ---------------------------------------------------------------------
// The jq surface.
// ---------------------------------------------------------------------

func TestGlobalJQFilters(t *testing.T) {
	res := mx4Run(t, mx4Opts{}, "version", "--output=json", "--jq", ".data.go_version")
	if res.Exit != 0 {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	filtered := strings.TrimSpace(res.Stdout)
	if !strings.HasPrefix(filtered, "go1") || strings.ContainsAny(filtered, "{}") {
		t.Fatalf("--jq should reduce the document to the selected go version string, got:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "\"meta\"") {
		t.Fatalf("--jq output still carries the whole frame:\n%s", res.Stdout)
	}
	Prove(t, rootPath, "jq",
		"the expression reduced the envelope to the single .data.go_version string on stdout; ignored, the whole frame prints")
}

func TestGlobalJQArgBinds(t *testing.T) {
	res := mx4Run(t, mx4Opts{}, "version", "--output=json", "--jq", "$who", "--jq-arg", "who=matrix-string")
	if res.Exit != 0 {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "matrix-string" {
		t.Fatalf("$who should evaluate to the bound string, got %q", res.Stdout)
	}
	// Bindings without an expression are a typed refusal, not a no-op.
	dangling := mx4Run(t, mx4Opts{}, "version", "--output=json", "--jq-arg", "x=1")
	mx4ErrCode(t, dangling, "flag_not_applicable")
	Prove(t, rootPath, "jq-arg",
		"the binding surfaced as $who's value in the expression's output, and --jq-arg with no --jq was refused flag_not_applicable rather than ignored")
}

func TestGlobalJQArgJSONBindsTyped(t *testing.T) {
	res := mx4Run(t, mx4Opts{}, "version", "--output=json", "--jq", "$n + 1", "--jq-argjson", "n=41")
	if res.Exit != 0 {
		t.Fatalf("exit %d: %s", res.Exit, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "42" {
		t.Fatalf("$n should be the NUMBER 41 (41+1=42), got %q", res.Stdout)
	}
	Prove(t, rootPath, "jq-argjson",
		"the value bound as JSON stayed a number — $n + 1 evaluated to 42, which the string-binding --jq-arg route cannot do")
}

// ---------------------------------------------------------------------
// The standard trio and the version flag.
// ---------------------------------------------------------------------

func TestGlobalColorMode(t *testing.T) {
	// NO_COLOR is removed so the flag alone decides; both runs are piped,
	// so `always` forcing SGR is precisely the flag overriding detection.
	always := mx4Run(t, mx4Opts{Unset: []string{"NO_COLOR"}},
		"issue", "delete", "KEY-1", "--output=human", "--color=always")
	never := mx4Run(t, mx4Opts{Unset: []string{"NO_COLOR"}},
		"issue", "delete", "KEY-1", "--output=human", "--color=never")
	if !strings.Contains(always.Stderr, "\x1b[") {
		t.Fatalf("--color=always should emit SGR on piped stderr, saw none:\n%q", always.Stderr)
	}
	if strings.Contains(never.Stderr, "\x1b[") {
		t.Fatalf("--color=never must strip SGR, found some:\n%q", never.Stderr)
	}
	Prove(t, rootPath, "color",
		"with NO_COLOR unset and stderr piped, --color=always emitted SGR escapes and --color=never emitted none on the identical refusal output")
}

func TestGlobalQuietSuppressesHint(t *testing.T) {
	loud := mx4Run(t, mx4Opts{}, "issue", "delete", "KEY-1", "--output=human")
	quiet := mx4Run(t, mx4Opts{}, "issue", "delete", "KEY-1", "--output=human", "--quiet")
	loudLines := len(strings.Split(strings.TrimSpace(loud.Stderr), "\n"))
	quietLines := len(strings.Split(strings.TrimSpace(quiet.Stderr), "\n"))
	if strings.TrimSpace(quiet.Stderr) == "" {
		t.Fatalf("--quiet must not swallow the error itself")
	}
	if quietLines != loudLines-1 {
		t.Fatalf("--quiet should drop exactly the hint line: default %d lines, quiet %d\ndefault: %q\nquiet: %q",
			loudLines, quietLines, loud.Stderr, quiet.Stderr)
	}
	Prove(t, rootPath, "quiet",
		"on the same human-mode refusal, --quiet dropped exactly one stderr line (the hint) while the error line survived — a level change, not silence. Only the hint level is observable here; deeper level plumbing is conductor's")
}

// mx4TimePrefix matches the wall-clock prefix clog adds at raised
// verbosity.
var mx4TimePrefix = regexp.MustCompile(`^\d{2}:\d{2}:\d{2}`)

func TestGlobalVerboseAddsTimestamps(t *testing.T) {
	plain := mx4Run(t, mx4Opts{}, "issue", "delete", "KEY-1", "--output=human")
	verbose := mx4Run(t, mx4Opts{}, "issue", "delete", "KEY-1", "--output=human", "--verbose=2")
	if mx4TimePrefix.MatchString(strings.TrimSpace(plain.Stderr)) {
		t.Fatalf("default stderr already carries timestamps:\n%q", plain.Stderr)
	}
	if !mx4TimePrefix.MatchString(strings.TrimSpace(verbose.Stderr)) {
		t.Fatalf("--verbose=2 should add wall-clock prefixes to stderr:\n%q", verbose.Stderr)
	}
	Prove(t, rootPath, "verbose",
		"--verbose=2 changed the stderr line format on the same refusal (wall-clock prefix appears; absent at the default level). Timestamping is the observable proxy for the level change")
}

func TestGlobalVersionFlag(t *testing.T) {
	bare := mx4Run(t, mx4Opts{}, "--output=json")
	var discovery struct {
		OK   bool `json:"ok"`
		Data struct {
			Schema struct {
				Path string `json:"path"`
			} `json:"schema"`
		} `json:"data"`
	}
	if bare.Exit != 0 || json.Unmarshal([]byte(bare.Stdout), &discovery) != nil || !discovery.OK || discovery.Data.Schema.Path != "jira" {
		t.Fatal("bare root did not emit its canonical discovery control")
	}
	versioned := mx4Run(t, mx4Opts{}, "--version")
	line := strings.TrimSpace(versioned.Stdout)
	declared := mx4Run(t, mx4Opts{}, "version", "--output=json")
	var version struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if declared.Exit != 0 || json.Unmarshal([]byte(declared.Stdout), &version) != nil || version.Data.Version == "" {
		t.Fatal("version command did not provide the comparison build version")
	}
	if versioned.Exit != 0 || line != version.Data.Version || strings.ContainsAny(line, "\n{") || versioned.Stdout == bare.Stdout {
		t.Fatalf("--version should print the version line, exit=%d stdout=%q", versioned.Exit, versioned.Stdout)
	}
	Prove(t, rootPath, "version",
		"bare root emitted canonical discovery; -V/--version replaced it with one non-JSON version line and exited 0")
}

func TestGlobalHelpFlag(t *testing.T) {
	res := mx4Run(t, mx4Opts{}, "--help")
	if res.Exit != 0 {
		t.Fatalf("--help exited %d", res.Exit)
	}
	rendered := res.Stdout + res.Stderr
	for _, cmd := range []string{"issue", "auth", "config"} {
		if !strings.Contains(rendered, cmd) {
			t.Fatalf("--help should list the %q command group:\n%s", cmd, rendered)
		}
	}
	Prove(t, rootPath, "help",
		"cobra's --help rendered the command tree (issue/auth/config all listed) and exited 0, where a bare machine-mode root emits nothing at all")
}

// ---------------------------------------------------------------------
// --config, --timeout, --no-input.
// ---------------------------------------------------------------------

func TestGlobalConfigNamesTheFile(t *testing.T) {
	work := t.TempDir()
	path := filepath.Join(work, "mine.toml")
	if err := os.WriteFile(path, []byte("read_only = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := mx4Run(t, mx4Opts{}, "config", "list", "--output=json", "--config", path)
	env := mx4OK(t, res)
	var out struct {
		Path     string `json:"path"`
		Settings []struct {
			Key    string `json:"key"`
			Value  string `json:"value"`
			Source string `json:"source"`
		} `json:"settings"`
	}
	mx4Data(t, env, &out)
	if out.Path != path {
		t.Fatalf("data.path = %q, want the named file %q", out.Path, path)
	}
	found := false
	for _, s := range out.Settings {
		if s.Key == "read_only" && s.Value == "true" && s.Source == "file" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the named file's read_only=true should resolve with source=file: %+v", out.Settings)
	}

	// A file the caller named must exist — silently falling back to the
	// environment is the failure mode the loud refusal exists to prevent.
	missing := mx4Run(t, mx4Opts{}, "config", "list", "--output=json", "--config", filepath.Join(work, "absent.toml"))
	mx4ErrCode(t, missing, "validation_failed")
	Prove(t, rootPath, "config",
		"config list reported data.path == the named file and its read_only=true with source=file; naming an absent file failed loudly (validation_failed exit 3) instead of falling back")
}

func TestGlobalTimeoutReachesTheClient(t *testing.T) {
	stub := newMx4Stub(t)
	stub.sleep = 700 * time.Millisecond
	slow := mx4Run(t, mx4Opts{Stub: stub}, "auth", "status", "--output=json", "--timeout=100ms")
	env := mx4ErrCode(t, slow, "timeout")
	if env.Meta.ExitCode == nil || *env.Meta.ExitCode != 7 {
		t.Fatalf("timeout should exit 7, got %d", slow.Exit)
	}
	// The control run survives the same 700ms stub: the default timeout
	// is no deadline, so only the flag can have produced the failure.
	ok := mx4Run(t, mx4Opts{Stub: stub}, "auth", "status", "--output=json")
	mx4OK(t, ok)
	Prove(t, rootPath, "timeout",
		"--timeout=100ms failed the 700ms-slow stub read with taxonomy code timeout exit 7, while the flagless control run against the same stub succeeded — the value reached the transport's deadline")
}

func TestGlobalNoInput(t *testing.T) {
	with := mx4Run(t, mx4Opts{}, "issue", "delete", "KEY-1", "--output=json", "--no-input")
	withEnv := mx4ErrCode(t, with, "flag_required")
	without := mx4Run(t, mx4Opts{}, "issue", "delete", "KEY-1", "--output=json")
	withoutEnv := mx4ErrCode(t, without, "flag_required")
	if withEnv.Errors[0].Message == withoutEnv.Errors[0].Message {
		t.Fatalf("--no-input should change the refusal's stated reason, both say:\n%s", withEnv.Errors[0].Message)
	}
	if !strings.Contains(withEnv.Errors[0].Message, "--no-input") {
		t.Fatalf("the --no-input refusal should attribute itself to the flag:\n%s", withEnv.Errors[0].Message)
	}
	Prove(t, rootPath, "no-input",
		"both refusals share code flag_required, but --no-input changed the stated reason to name itself. HONEST LIMIT: with no PTY in this harness the prompt-suppression itself is unobservable, so this pins only the attribution difference — a plant that stops reading the flag makes the messages identical and fails this case")
}

// ---------------------------------------------------------------------
// The completion flags.
// ---------------------------------------------------------------------

func TestGlobalCompleteInterceptsDispatch(t *testing.T) {
	// A recognized action flag is handled BEFORE cobra parses; the
	// command named in the argv must not run.
	intercepted := mx4Run(t, mx4Opts{}, "--@complete=nosuchkind", "version")
	if intercepted.Exit != 0 || strings.TrimSpace(intercepted.Stdout) != "" {
		t.Fatalf("an unknown predictor kind must answer nothing and exit 0, got exit=%d stdout=%q",
			intercepted.Exit, intercepted.Stdout)
	}
	control := mx4Run(t, mx4Opts{}, "version")
	if strings.TrimSpace(control.Stdout) == "" {
		t.Fatalf("the control run should have printed the version envelope")
	}
	Prove(t, rootPath, "@complete",
		"--@complete intercepted the invocation pre-parse: `version` beside it printed NOTHING and exited 0, while the flagless control printed the version envelope — and an unknown kind answered zero candidates per the predictor laws")
}

func TestGlobalShellSelectsScript(t *testing.T) {
	fish := mx4Run(t, mx4Opts{}, "--print-completion", "--@shell=fish")
	zsh := mx4Run(t, mx4Opts{}, "--print-completion", "--@shell=zsh")
	if !strings.HasPrefix(fish.Stdout, "complete -c jira") {
		t.Fatalf("--@shell=fish should render the fish script, got:\n%.80s", fish.Stdout)
	}
	if !strings.HasPrefix(zsh.Stdout, "#compdef jira") {
		t.Fatalf("--@shell=zsh should render the zsh script, got:\n%.80s", zsh.Stdout)
	}
	Prove(t, rootPath, "@shell",
		"the same --print-completion rendered a fish script under --@shell=fish and a zsh script under --@shell=zsh — the flag alone selected the output grammar")
}

func TestGlobalPrintCompletion(t *testing.T) {
	res := mx4Run(t, mx4Opts{}, "--print-completion", "--@shell=zsh")
	if res.Exit != 0 || !strings.HasPrefix(res.Stdout, "#compdef jira") {
		t.Fatalf("--print-completion should print the script, exit=%d stdout=%.60q", res.Exit, res.Stdout)
	}
	Prove(t, rootPath, "print-completion",
		"the action flag printed the zsh completion script to stdout and exited 0, instead of the bare root runtime schema")
}

func TestGlobalInstallAndUninstallCompletion(t *testing.T) {
	// One sandboxed workdir for BOTH runs, so the uninstall removes the
	// exact file the install created.
	work := t.TempDir()
	env := map[string]string{
		"HOME":            work,
		"XDG_CONFIG_HOME": filepath.Join(work, "config"),
	}
	installed := filepath.Join(work, "config", "fish", "completions", "jira.fish")

	install := mx4Run(t, mx4Opts{Env: env}, "--install-completion", "--@shell=fish")
	if install.Exit != 0 {
		t.Fatalf("install exited %d: %s", install.Exit, install.Stderr)
	}
	data, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("--install-completion should have written %s: %v", installed, err)
	}
	if !strings.Contains(string(data), "complete -c jira") {
		t.Fatalf("installed script is not the fish completion:\n%.80s", data)
	}
	Prove(t, rootPath, "install-completion",
		"the action flag wrote the fish completion script to XDG_CONFIG_HOME/fish/completions/jira.fish; without it no file appears")

	uninstall := mx4Run(t, mx4Opts{Env: env}, "--uninstall-completion", "--@shell=fish")
	if uninstall.Exit != 0 {
		t.Fatalf("uninstall exited %d: %s", uninstall.Exit, uninstall.Stderr)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Fatalf("--uninstall-completion should have removed %s (stat err: %v)", installed, err)
	}
	Prove(t, rootPath, "uninstall-completion",
		"the action flag removed the exact file the install wrote (stat now reports not-exist); ignored, the file survives")
}

// ---------------------------------------------------------------------
// jira issue --web (the GROUP's one persistent flag — declared on
// `jira issue` rather than any leaf, so it belongs to the group-and-
// global cluster).
// ---------------------------------------------------------------------

func TestIssueGroupWeb(t *testing.T) {
	// The base URL is the UNROUTABLE port on purpose: --web documents
	// that it builds the browse URL locally and contacts Jira not at
	// all, and succeeding against a dead endpoint is that claim proved
	// from outside — any request would have failed the run.
	env := map[string]string{
		"JIRA_BASE_URL":  "http://127.0.0.1:1",
		"JIRA_EMAIL":     mx4Email,
		"JIRA_API_TOKEN": mx4Token,
	}
	res := mx4Run(t, mx4Opts{Env: env}, "issue", "view", "KEY-123", "--web", "--output=json")
	frame := mx4OK(t, res)
	var out struct {
		WebURL string `json:"web_url"`
	}
	mx4Data(t, frame, &out)
	if out.WebURL != "http://127.0.0.1:1/browse/KEY-123" {
		t.Fatalf("data.web_url = %q, want the locally composed browse URL", out.WebURL)
	}

	// And the flag REFUSES on a multi-key write rather than guessing
	// which key it meant — by code, before anything is sent.
	refused := mx4Run(t, mx4Opts{Env: env}, "issue", "delete", "KEY-1", "--force", "--web", "--output=json")
	refusedEnv := mx4ErrCode(t, refused, "flag_not_applicable")
	if refusedEnv.Errors[0].Field != "web" {
		t.Fatalf("refusal field = %q, want web", refusedEnv.Errors[0].Field)
	}
	Prove(t, "jira issue", "web",
		"data.web_url carried the locally composed browse URL in machine mode with base_url pointed at an UNROUTABLE port (so zero contact, proved by the success itself), and the same flag on a keyed write was refused flag_not_applicable field=web")
}

// ---------------------------------------------------------------------
// jira auth login / logout / status, jira config edit.
// ---------------------------------------------------------------------

const (
	loginPath      = "jira auth login"
	logoutPath     = "jira auth logout"
	authStatusPath = "jira auth status"
	configEditPath = "jira config edit"
)

func TestAuthLoginSite(t *testing.T) {
	stored := newMx4Stub(t) // what JIRA_BASE_URL names
	flagged := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stored, Stdin: "piped-token\n"},
		"auth", "login", "--with-token", "--dry-run", "--output=json",
		"--site", flagged.URL(), "--email", mx4Email)
	env := mx4OK(t, res)
	if got := len(flagged.matching(http.MethodGet, "/myself")); got != 1 {
		t.Fatalf("the --site host should have received the verification, saw %d", got)
	}
	if got := len(stored.requests()); got != 0 {
		t.Fatalf("the configured host must receive nothing when --site overrides it, saw %d", got)
	}
	var out struct {
		BaseURL string `json:"base_url"`
	}
	mx4Data(t, env, &out)
	if out.BaseURL != flagged.URL() {
		t.Fatalf("data.base_url = %q, want the --site value %q", out.BaseURL, flagged.URL())
	}
	Prove(t, loginPath, "site",
		"the verification GET /myself landed on the --site host while the JIRA_BASE_URL host received zero requests, and data.base_url reported the flag's value")
}

func TestAuthLoginEmail(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub, Stdin: "piped-token\n"},
		"auth", "login", "--with-token", "--dry-run", "--output=json",
		"--site", stub.URL(), "--email", "flagged@example.com")
	mx4OK(t, res)
	gets := stub.matching(http.MethodGet, "/myself")
	if len(gets) != 1 {
		t.Fatalf("expected one verification, got %d", len(gets))
	}
	if gets[0].AuthUser != "flagged@example.com" {
		t.Fatalf("basic-auth user = %q, want the --email value (JIRA_EMAIL is %q)", gets[0].AuthUser, mx4Email)
	}
	Prove(t, loginPath, "email",
		"the verification request authenticated as the --email value in basic auth, beating the different JIRA_EMAIL in the environment")
}

func TestAuthLoginWithToken(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub, Stdin: "piped-token\n"},
		"auth", "login", "--with-token", "--dry-run", "--output=json",
		"--site", stub.URL(), "--email", mx4Email)
	mx4OK(t, res)
	gets := stub.matching(http.MethodGet, "/myself")
	if len(gets) != 1 || gets[0].AuthPass != "piped-token" {
		t.Fatalf("verification should carry the PIPED token in basic auth, got %+v", gets)
	}
	// Without the flag a non-interactive login refuses instead of
	// waiting on input nobody will supply.
	refused := mx4Run(t, mx4Opts{Stub: stub},
		"auth", "login", "--dry-run", "--output=json", "--site", stub.URL(), "--email", mx4Email)
	env := mx4ErrCode(t, refused, "flag_required")
	if env.Errors[0].Field != "with-token" {
		t.Fatalf("refusal field = %q, want with-token", env.Errors[0].Field)
	}
	Prove(t, loginPath, "with-token",
		"the flag routed stdin to the credential: basic auth carried the piped token (not JIRA_API_TOKEN), and the flagless non-interactive control was refused flag_required field=with-token")
}

func TestAuthLoginDryRun(t *testing.T) {
	stub := newMx4Stub(t)
	res := mx4Run(t, mx4Opts{Stub: stub, Stdin: "piped-token\n"},
		"auth", "login", "--with-token", "--dry-run", "--output=json",
		"--site", stub.URL(), "--email", mx4Email)
	env := mx4OK(t, res)
	var out struct {
		DryRun bool `json:"dry_run"`
	}
	mx4Data(t, env, &out)
	if !out.DryRun {
		t.Fatalf("data.dry_run = false on a preview")
	}
	if got := len(stub.matching(http.MethodGet, "/myself")); got != 1 {
		t.Fatalf("a login preview still VERIFIES, saw %d /myself reads", got)
	}
	Prove(t, loginPath, "dry-run",
		"the preview verified (one /myself read) and exited 0 with dry_run=true; ignored, the live path stores into the disabled keyring (JIRA_KEYRING=false) and fails — exit 0 here is only reachable through the flag")
}

func TestAuthLogoutDryRun(t *testing.T) {
	res := mx4Run(t, mx4Opts{Env: map[string]string{"JIRA_EMAIL": mx4Email}},
		"auth", "logout", "--dry-run", "--output=json")
	env := mx4OK(t, res)
	var out struct {
		DryRun bool   `json:"dry_run"`
		Email  string `json:"email"`
	}
	mx4Data(t, env, &out)
	if !out.DryRun || out.Email != mx4Email {
		t.Fatalf("preview should report dry_run=true for the account, got %+v", out)
	}
	Prove(t, logoutPath, "dry-run",
		"the non-interactive preview succeeded with dry_run=true and no confirmation demanded; ignored, the same argv is the flag_required (--yes) refusal the control below gets")
}

func TestAuthLogoutYes(t *testing.T) {
	refused := mx4Run(t, mx4Opts{Env: map[string]string{"JIRA_EMAIL": mx4Email}},
		"auth", "logout", "--output=json")
	env := mx4ErrCode(t, refused, "flag_required")
	if env.Errors[0].Field != "yes" {
		t.Fatalf("refusal field = %q, want yes", env.Errors[0].Field)
	}
	confirmed := mx4Run(t, mx4Opts{Env: map[string]string{"JIRA_EMAIL": mx4Email}},
		"auth", "logout", "--yes", "--output=json")
	out := mx4OK(t, confirmed)
	var data struct {
		DryRun bool `json:"dry_run"`
	}
	mx4Data(t, out, &data)
	if data.DryRun {
		t.Fatalf("--yes is the live path, not a preview")
	}
	Prove(t, logoutPath, "yes",
		"without it the non-interactive logout was refused flag_required field=yes; with it the removal ran (ok envelope, dry_run=false)")
}

func TestAuthStatusShowToken(t *testing.T) {
	stub := newMx4Stub(t)
	masked := mx4Run(t, mx4Opts{Stub: stub}, "auth", "status", "--output=json")
	maskedEnv := mx4OK(t, masked)
	var maskedOut map[string]any
	mx4Data(t, maskedEnv, &maskedOut)
	if _, leaked := maskedOut["token"]; leaked {
		t.Fatalf("without --show-token the token member must be ABSENT, got %v", maskedOut["token"])
	}

	shown := mx4Run(t, mx4Opts{Stub: stub}, "auth", "status", "--show-token", "--output=json")
	shownEnv := mx4OK(t, shown)
	var shownOut struct {
		Token string `json:"token"`
	}
	mx4Data(t, shownEnv, &shownOut)
	if shownOut.Token != mx4Token {
		t.Fatalf("--show-token should publish the live token, got %q", shownOut.Token)
	}
	Prove(t, authStatusPath, "show-token",
		"data.token is absent from the envelope without the flag and equals the configured token with it — presence of the secret itself is the flag's effect")
}

func TestConfigEditDryRun(t *testing.T) {
	editor := mx4Binary(t)
	res := mx4Run(t, mx4Opts{Env: map[string]string{"JIRA_EDITOR": shell.Quote(editor) + " --version"}},
		"config", "edit", "--dry-run", "--output=json")
	env := mx4OK(t, res)
	var out struct {
		DryRun bool     `json:"dry_run"`
		Path   string   `json:"path"`
		Editor []string `json:"editor"`
	}
	mx4Data(t, env, &out)
	if !out.DryRun || len(out.Editor) != 2 || out.Editor[0] != editor || out.Editor[1] != "--version" {
		t.Fatalf("preview should resolve the editor and report dry_run=true, got %+v", out)
	}
	if _, err := os.Stat(out.Path); !os.IsNotExist(err) {
		t.Fatalf("the preview must not create the config file (%s): %v", out.Path, err)
	}
	Prove(t, configEditPath, "dry-run",
		"the terminal-less preview exited 0 with dry_run=true, the resolved editor named, and no config file created; ignored, the same argv is a terminal_required failure")
}

// Explicit empty is a real choice: it prevents public tenant discovery after
// a classic authentication refusal, while omission retains that fallback.
func TestAuthLoginCloudID(t *testing.T) {
	var recorded []string
	var mutex sync.Mutex
	snapshot := func() []string { mutex.Lock(); defer mutex.Unlock(); return append([]string(nil), recorded...) }
	clear := func() { mutex.Lock(); defer mutex.Unlock(); recorded = nil }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		recorded = append(recorded, r.URL.Path)
		mutex.Unlock()
		if r.URL.Path == "/rest/api/3/myself" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/_edge/tenant_info" {
			if r.Header.Get("Authorization") != "" {
				t.Error("public discovery carried authorization")
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	options := mx4Opts{Stdin: "piped-token\n", Env: map[string]string{"JIRA_BASE_URL": server.URL, "JIRA_EMAIL": mx4Email}}
	args := []string{"auth", "login", "--with-token", "--dry-run", "--output=json"}
	mx4ErrCode(t, mx4Run(t, options, append(args, "--cloud-id=")...), "jira_unauthorized")
	paths := snapshot()
	if len(paths) != 1 || paths[0] != "/rest/api/3/myself" {
		t.Fatal("explicit empty scope did not force classic routing")
	}
	clear()
	mx4ErrCode(t, mx4Run(t, options, args...), "jira_unauthorized")
	paths = snapshot()
	if len(paths) != 2 || paths[1] != "/_edge/tenant_info" {
		t.Fatal("omitted scope did not discover after a real refusal")
	}
	clear()
	mx4ErrCode(t, mx4Run(t, options, append(args, "--cloud-id=..")...), "flag_value_invalid")
	paths = snapshot()
	if len(paths) != 0 {
		t.Fatal("invalid scope contacted Jira")
	}
	Prove(t, loginPath, "cloud-id", "the real binary's explicit empty scope stopped tenant discovery after 401; the omitted control discovered, and a path-unsafe ID failed before network I/O")
}
