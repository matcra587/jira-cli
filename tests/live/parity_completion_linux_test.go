//go:build live && linux

package live

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type parityCompletionShell struct{ name, binary, digest string }

func parityCompletionShells(t *testing.T) []parityCompletionShell {
	t.Helper()
	out := []parityCompletionShell{}
	for _, name := range []string{"bash", "elvish", "fish", "pwsh", "zsh"} {
		requested := os.Getenv("JIRA_PARITY_" + strings.ToUpper(name) + "_BINARY")
		if requested == "" {
			requested = name
		}
		binary, err := exec.LookPath(requested)
		if err != nil {
			t.Fatalf("required selected native shell %s unavailable: %v", name, err)
		}
		body, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, parityCompletionShell{name, binary, fmt.Sprintf("%x", sha256.Sum256(body))})
	}
	return out
}

func parityCompletionEnvironment(home, site string, extra ...string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + home, "XDG_CACHE_HOME=" + home, "XDG_DATA_HOME=" + filepath.Join(home, "data"), "XDG_STATE_HOME=" + filepath.Join(home, "state"), "ZDOTDIR=" + home, "JIRA_BASE_URL=" + site, "JIRA_EMAIL=fixture@example.invalid", "JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false", "JIRA_SECRET_BACKEND=env", "JIRA_READ_ONLY=true", "JIRA_NO_UPDATE_CHECK=1", "NO_COLOR=1", "TERM=dumb", "DOTNET_CLI_TELEMETRY_OPTOUT=1", "POWERSHELL_TELEMETRY_OPTOUT=1"}
	return append(env, extra...)
}

func parityCompletionProcess(t *testing.T, sh parityCompletionShell, home, site string, extra []string, args ...string) parityFixtureResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := parityFingerprint(sh.binary, sh.digest); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, sh.binary, args...)
	cmd.WaitDelay = time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return unix.Kill(-cmd.Process.Pid, unix.SIGKILL) }
	cmd.Dir = home
	cmd.Env = parityCompletionEnvironment(home, site, extra...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	exit := 0
	if err := cmd.Run(); err != nil {
		var status *exec.ExitError
		if !errors.As(err, &status) {
			t.Fatal("selected native shell did not start: ", err)
		}
		exit = status.ExitCode()
	}
	if ctx.Err() != nil {
		t.Fatalf("selected native shell exceeded owned deadline: %v; stderr %s", ctx.Err(), stderr.Bytes())
	}
	return parityFixtureResult{Argv: append([]string{}, args...), Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Exit: exit}
}

func parityCompletionVersionArgs(name string) []string {
	switch name {
	case "elvish":
		return []string{"-version"}
	case "pwsh":
		return []string{"-NoProfile", "-NonInteractive", "-Command", "$PSVersionTable.PSVersion.ToString()"}
	default:
		return []string{"--version"}
	}
}

func parityCompletionSyntaxArgs(name, path string) []string {
	switch name {
	case "bash":
		return []string{"--noprofile", "--norc", "-n", path}
	case "fish":
		return []string{"--no-config", "--no-execute", path}
	case "zsh":
		return []string{"-f", "-n", path}
	case "elvish":
		return []string{"-norc", "-compileonly", path}
	default:
		return []string{"-NoProfile", "-NonInteractive", "-Command", `$errors=$null; $tokens=$null; [System.Management.Automation.Language.Parser]::ParseFile($env:JIRA_PARITY_SCRIPT,[ref]$tokens,[ref]$errors) | Out-Null; if($errors.Count){$errors | ForEach-Object {$_.Message}; exit 3}`}
	}
}

func parityCompletionRecord(evidence *parityLocalEvidence, lane string, sh parityCompletionShell, action string, script []byte, result parityFixtureResult) {
	run := parityNativeShellRun{Lane: lane, Shell: sh.name, Binary: sh.binary, Digest: sh.digest, Action: action, Argv: result.Argv, Exit: result.Exit, Stdout: result.Stdout, Stderr: result.Stderr}
	if script != nil {
		run.ScriptDigest = fmt.Sprintf("%x", sha256.Sum256(script))
	}
	evidence.NativeRuns = append(evidence.NativeRuns, run)
}

func TestParityActualBinaryCompletionScripts(t *testing.T) {
	if os.Getenv("JIRA_PARITY_BEFORE_BINARY") == "" || os.Getenv("JIRA_LIVE_BINARY") == "" {
		t.Skip("requires selected actual binaries")
	}
	shells := parityCompletionShells(t)
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	ids := map[string]string{"bash": "C042", "elvish": "C043", "fish": "C044", "pwsh": "C045", "zsh": "C046"}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		for _, sh := range shells {
			t.Run(lane.name+"/"+sh.name, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					http.Error(w, "completion entered HTTP", 500)
				}))
				defer server.Close()
				home := t.TempDir()
				config := parityCachePublicConfig(t, home, lane.name, server.URL)
				before := parityLocalTree(t, home)
				version := parityCompletionProcess(t, sh, home, server.URL, nil, parityCompletionVersionArgs(sh.name)...)
				if version.Exit != 0 || len(version.Stdout) == 0 || len(version.Stderr) != 0 {
					t.Fatalf("selected %s version did not execute: exit %d stdout %s stderr %s", sh.name, version.Exit, version.Stdout, version.Stderr)
				}
				parityCompletionRecord(&evidence, lane.name, sh, "version", nil, version)
				initialized := parityLocalTree(t, home)
				for path := range initialized {
					if _, present := before[path]; !present {
						t.Logf("native %s version initialized owned path %s", sh.name, path)
					}
				}
				// Native runtime initialization is separate from CLI effects.
				before = initialized
				var script []byte
				for _, mode := range []string{"json", "compact", "human"} {
					if !t.Run(mode, func(t *testing.T) {
						if err := parityFingerprint(lane.binary, lane.digest); err != nil {
							t.Fatal(err)
						}
						result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false", "JIRA_READ_ONLY=true"}, "completion", sh.name)
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						if result.Exit != 0 || len(result.Stderr) != 0 || !bytes.Contains(result.Stdout, []byte("jira")) || len(result.Stdout) < 100 || calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
							t.Fatalf("native script generation failed or entered effects: exit %d HTTP %d stderr %s", result.Exit, calls.Load(), result.Stderr)
						}
						if script == nil {
							script = append([]byte{}, result.Stdout...)
						} else if !bytes.Equal(script, result.Stdout) {
							t.Fatal("completion output mode changed emitted script bytes")
						}
						check := ids[sh.name] + ".scripts.generated." + mode
						evidence.Checks[check] = append(evidence.Checks[check], lane.name)
					}) {
						return
					}
				}
				extension := sh.name
				if sh.name == "pwsh" {
					extension = "ps1"
				}
				path := filepath.Join(home, "completion."+extension)
				if err := os.WriteFile(path, script, 0o600); err != nil {
					t.Fatal(err)
				}
				// Elvish's edit namespace exists only in a native interactive runtime.
				// Its complete generated script is loaded by that callback gate below.
				if sh.name != "elvish" {
					syntax := parityCompletionProcess(t, sh, home, server.URL, []string{"JIRA_PARITY_SCRIPT=" + path}, parityCompletionSyntaxArgs(sh.name, path)...)
					parityCompletionRecord(&evidence, lane.name, sh, "syntax", script, syntax)
					if syntax.Exit != 0 || len(syntax.Stdout) != 0 || len(syntax.Stderr) != 0 {
						t.Fatalf("actual generated %s script did not parse: exit %d stdout %s stderr %s", sh.name, syntax.Exit, syntax.Stdout, syntax.Stderr)
					}
				}
				broken := "if then\n"
				switch sh.name {
				case "fish":
					broken = "function\n"
				case "elvish":
					broken = "var x = (\n"
				case "pwsh":
					broken = "function {\n"
				}
				invalid := filepath.Join(home, "invalid."+sh.name)
				if err := os.WriteFile(invalid, []byte(broken), 0o600); err != nil {
					t.Fatal(err)
				}
				control := parityCompletionProcess(t, sh, home, server.URL, []string{"JIRA_PARITY_SCRIPT=" + invalid}, parityCompletionSyntaxArgs(sh.name, invalid)...)
				parityCompletionRecord(&evidence, lane.name, sh, "invalid-syntax-control", []byte(broken), control)
				if control.Exit == 0 || len(control.Stdout)+len(control.Stderr) == 0 {
					t.Fatal("native syntax control did not reject deliberately broken source")
				}
				if err := parityFingerprint(lane.binary, lane.digest); err != nil {
					t.Fatal(err)
				}
				prime := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: "json"}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false", "JIRA_READ_ONLY=true"}, "open", "KEY-7")
				evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: prime.Argv, Exit: prime.Exit, OutputBytes: prime.Stdout, StderrBytes: prime.Stderr})
				if prime.Exit != 0 || len(prime.Stderr) != 0 {
					t.Fatal("owned native history prime failed")
				}
				parityAuthoringCleanData(t, prime.Stdout, lane.name, "open")
				history := parityOpenHistory(t, home, lane.name, server.URL, "KEY-7", time.Time{})
				cacheRoot := filepath.Join(home, "jira")
				if lane.name == "before" {
					cacheRoot = filepath.Join(home, "jira-cli")
				}
				cacheBefore := parityLocalTree(t, cacheRoot)
				callback, result := parityCompletionCachedCallback(t, sh, home, server.URL, path, lane.name, lane.binary, config)
				parityCompletionRecord(&evidence, lane.name, sh, "cached-key-callback", script, result)
				evidence.NativeRuns[len(evidence.NativeRuns)-1].CallbackArgs = callback
				if result.Exit != 0 {
					t.Fatalf("native %s callback runtime failed: exit %d stdout %s stderr %s", sh.name, result.Exit, result.Stdout, result.Stderr)
				}
				if calls.Load() != 0 || !reflect.DeepEqual(cacheBefore, parityLocalTree(t, cacheRoot)) || history == "" {
					t.Fatal("native callback entered HTTP or changed its owned scoped cache")
				}
				if err := parityFingerprint(lane.binary, lane.digest); err != nil {
					t.Fatal(err)
				}
				check := ids[sh.name] + ".scripts.cached-key-callback"
				evidence.Checks[check] = append(evidence.Checks[check], lane.name)
			})
		}
	}
	saveParityLocal(t, "completion", evidence)
}

func parityCompletionPTY(t *testing.T, sh parityCompletionShell, home, site string, extra, args []string, ready, marker string) parityFixtureResult {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(fd), "paired-native-terminal")
	defer func() { _ = master.Close() }()
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slave.Close() }()
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 30, Col: 160}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := parityFingerprint(sh.binary, sh.digest); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, sh.binary, args...)
	cmd.Dir = home
	cmd.Env = parityCompletionEnvironment(home, site, append(extra, "TERM=xterm-256color")...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	cmd.Cancel = func() error { return unix.Kill(-cmd.Process.Pid, unix.SIGKILL) }
	if err := cmd.Start(); err != nil {
		t.Fatal("selected native editor did not start: ", err)
	}
	_ = slave.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	joined := false
	defer func() {
		// Descendants retaining the terminal belong to this process group even
		// when the selected shell has already exited.
		_ = cmd.Cancel()
		if !joined {
			<-done
		}
	}()
	if err := unix.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	var screen bytes.Buffer
	initialized := sh.name != "zsh"
	sent, interrupted, finished := false, false, false
	promptOffset := 0
	buffer := make([]byte, 64*1024)
	exit, closed := 0, false
	var queries parityCompletionTerminalQueries
	for {
		if !joined {
			select {
			case waitErr := <-done:
				joined = true
				if waitErr != nil {
					var status *exec.ExitError
					if !errors.As(waitErr, &status) {
						t.Fatal(waitErr)
					}
					exit = status.ExitCode()
				}
			default:
			}
		}
		if ctx.Err() != nil {
			t.Fatalf("native editor exceeded its owned deadline: %v; screen %s", ctx.Err(), screen.Bytes())
		}
		if _, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 20); err != nil && !errors.Is(err, unix.EINTR) {
			t.Fatal(err)
		}
		count, readErr := unix.Read(fd, buffer)
		if readErr == nil && count > 0 {
			chunk := buffer[:count]
			if screen.Len()+len(chunk) > 1024*1024 {
				t.Fatal("native editor diagnostics exceeded one MiB")
			}
			screen.Write(chunk)
			if response := queries.feed(chunk); len(response) != 0 && !joined {
				if _, err := master.Write(response); err != nil {
					t.Fatal(err)
				}
			}
		} else if errors.Is(readErr, unix.EIO) || readErr == nil && count == 0 {
			closed = true
		} else if readErr != nil && !errors.Is(readErr, unix.EAGAIN) && !errors.Is(readErr, unix.EINTR) {
			t.Fatal(readErr)
		}
		if joined {
			if closed {
				if exit == 0 && sh.name == "zsh" && (!initialized || !sent || !interrupted || !finished) {
					t.Fatal("native Zsh exited before the controlled completion and exit sequence finished")
				}
				return parityFixtureResult{Argv: append([]string{}, args...), Stdout: screen.Bytes(), Exit: exit}
			}
			continue
		}
		if !initialized && strings.Contains(screen.String(), "PAIRED_BOOT_READY>") {
			if _, err := master.Write([]byte("source \"$JIRA_PARITY_INIT\"\r")); err != nil {
				t.Fatal(err)
			}
			initialized = true
		}
		if initialized && ready != "" && !sent && strings.Contains(screen.String(), ready) {
			if _, err := master.Write([]byte("jira open KEY-\t")); err != nil {
				t.Fatal(err)
			}
			sent = true
		}
		if sent && !interrupted {
			if _, err := os.Stat(marker); err == nil {
				promptOffset = screen.Len()
				if _, err := master.Write([]byte("\x03")); err != nil {
					t.Fatal(err)
				}
				interrupted = true
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		}
		if interrupted && !finished && bytes.Contains(screen.Bytes()[promptOffset:], []byte(ready)) {
			if _, err := master.Write([]byte("exit 0\r")); err != nil {
				t.Fatal(err)
			}
			finished = true
		}
	}
}

func parityCompletionCachedCallback(t *testing.T, sh parityCompletionShell, home, site, script, lane, binary, config string) ([]byte, parityFixtureResult) {
	t.Helper()
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(home, "callback-argv")
	reply := filepath.Join(home, "candidate-replies")
	prefix := "--config \"$JIRA_PARITY_COMPLETION_CONFIG\""
	if lane == "before" {
		prefix += " --profile fixture"
	}
	shim := "#!/bin/sh\nprintf 'CALL\\000' >> \"$JIRA_PARITY_CALLBACK_LOG\"\nprintf '%s\\000' \"$@\" >> \"$JIRA_PARITY_CALLBACK_LOG\"\n\"$JIRA_PARITY_COMPLETION_BINARY\" " + prefix + " \"$@\"\nstatus=$?\nprintf 'EXIT\\000%s\\000' \"$status\" >> \"$JIRA_PARITY_CALLBACK_LOG\"\nexit \"$status\"\n"
	if err := os.WriteFile(filepath.Join(bin, "jira"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	extra := []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "JIRA_PARITY_COMPLETION_BINARY=" + binary, "JIRA_PARITY_COMPLETION_CONFIG=" + config, "JIRA_PARITY_CALLBACK_LOG=" + log, "JIRA_PARITY_SCRIPT=" + script, "JIRA_PARITY_REPLIES=" + reply, "JIRA_PARITY_LANE=" + lane}
	var result parityFixtureResult
	switch sh.name {
	case "bash":
		code := `source "$JIRA_PARITY_SCRIPT" || exit 31
registration=$(complete -p jira) || exit 33
read -r -a specification <<< "$registration"
callback=''
for ((i=0; i<${#specification[@]}; i++)); do
    if [[ ${specification[i]} == -F ]]; then callback=${specification[i+1]}; fi
done
[[ $callback == _jira && ${specification[-1]} == jira && $(type -t "$callback") == function ]] || exit 34
printf '%s\n' "$registration"
compopt() { return 0; }
COMP_WORDS=(jira open KEY-)
COMP_CWORD=2
COMP_LINE='jira open KEY-'
COMP_POINT=${#COMP_LINE}
"$callback" jira KEY- open || exit 32
printf '%s\n' "${COMPREPLY[@]}" > "$JIRA_PARITY_REPLIES"`
		result = parityCompletionProcess(t, sh, home, site, extra, "--noprofile", "--norc", "-c", code)
	case "fish":
		code := `source "$JIRA_PARITY_SCRIPT"; or exit 31
complete -C 'jira open KEY-' > "$JIRA_PARITY_REPLIES"`
		result = parityCompletionProcess(t, sh, home, site, extra, "--no-config", "-c", code)
	case "pwsh":
		code := `$ErrorActionPreference='Stop'
. $env:JIRA_PARITY_SCRIPT
$line='jira open KEY-'
$matches=[System.Management.Automation.CommandCompletion]::CompleteInput($line,$line.Length,$null).CompletionMatches
$receipts=@($matches | ForEach-Object {
    $errors=$null; $tokens=$null
    $ast=[System.Management.Automation.Language.Parser]::ParseInput(('observer ' + $_.CompletionText),[ref]$tokens,[ref]$errors)
    $commands=@($ast.FindAll({param($node) $node -is [System.Management.Automation.Language.CommandAst]},$true))
    if ($errors.Count -or $commands.Count -ne 1 -or $commands[0].CommandElements.Count -ne 2 -or $commands[0].CommandElements[1] -isnot [System.Management.Automation.Language.StringConstantExpressionAst]) { throw 'Completion is not one literal argument' }
    [pscustomobject]@{token=$_.CompletionText; value=$commands[0].CommandElements[1].Value}
})
[System.IO.File]::WriteAllText($env:JIRA_PARITY_REPLIES,(ConvertTo-Json -InputObject @($receipts | ForEach-Object {$_.value}) -Compress),[System.Text.UTF8Encoding]::new($false))
$control='jira open --out'
$registration=@([System.Management.Automation.CommandCompletion]::CompleteInput($control,$control.Length,$null).CompletionMatches | ForEach-Object {$_.ListItemText})
$receipt=[ordered]@{candidates=$receipts; registration_control=$registration}
if ($env:JIRA_PARITY_LANE -eq 'before') {
    $errors=$null; $tokens=$null
    $ast=[System.Management.Automation.Language.Parser]::ParseInput($line,[ref]$tokens,[ref]$errors)
    if ($errors.Count) { throw 'Owned completion line did not parse' }
    $command=$ast.EndBlock.Statements[0].PipelineElements[0]
    $prior=@(__jira_Tokens $command 'KEY-')
    $receipt.legacy_positionals=@(__jira_Positionals $prior 1 @())
}
ConvertTo-Json -InputObject $receipt -Depth 8 -Compress`
		result = parityCompletionProcess(t, sh, home, site, extra, "-NoProfile", "-NonInteractive", "-Command", code)
	case "elvish":
		// A real terminal supplies edit's namespace. This calls its registered
		// completer and matcher; it does not establish Tab insertion.
		generated, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		code := string(generated) + "\n" + `var replies = [($edit:completion:arg-completer[jira] jira open KEY-)]
var stems = []
for candidate $replies { if (eq (kind-of $candidate) string) { set stems = [$@stems $candidate] } else { set stems = [$@stems $candidate[stem]] } }
var matcher = $edit:match-prefix~
if (has-key $edit:completion:matcher '') { set matcher = $edit:completion:matcher[''] }
if (has-key $edit:completion:matcher argument) { set matcher = $edit:completion:matcher[argument] }
var matches = [(put $@stems | $matcher KEY-)]
var matched = []
for i [(range (count $stems))] { if $matches[$i] { set matched = [$@matched $stems[$i]] } }
put $matched | to-json > $E:JIRA_PARITY_REPLIES
exit 0
`
		rc := filepath.Join(home, "callback.rc.elv")
		if err := os.WriteFile(rc, []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
		result = parityCompletionPTY(t, sh, home, site, extra, []string{"-rc", rc, "-db", "", "-sock", filepath.Join(home, "owned.sock")}, "", "")
	case "zsh":
		marker := filepath.Join(home, "callback-finished")
		code := `autoload -Uz compinit
compinit -D || exit 30
source "$JIRA_PARITY_SCRIPT" || exit 31
function compadd() {
    local -a observed
    builtin compadd -O observed "$@"
    if (( ${#observed} )); then
        printf '%s\n' "${observed[@]}" >> "$JIRA_PARITY_REPLIES"
    fi
    builtin compadd "$@"
}
function _jira_paired_capture() {
    _main_complete
    print -r -- ready > "$JIRA_PARITY_DONE"
}
zle -C jira-paired-complete complete-word _jira_paired_capture
bindkey -e
bindkey '^I' jira-paired-complete
PS1='PAIRED_COMPLETION_READY> '
`
		if err := os.WriteFile(filepath.Join(home, "zsh-init.zsh"), []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
		extra = append(extra, "PS1=PAIRED_BOOT_READY> ", "JIRA_PARITY_DONE="+marker, "JIRA_PARITY_INIT="+filepath.Join(home, "zsh-init.zsh"))
		result = parityCompletionPTY(t, sh, home, site, extra, []string{"-d", "-f", "-i"}, "PAIRED_COMPLETION_READY>", marker)
	}
	if result.Exit != 0 {
		return nil, result
	}
	callback, err := os.ReadFile(log)
	if sh.name == "pwsh" && lane == "before" {
		// The delivered generator wraps an empty positional list in another
		// array, so its arity check suppresses this callback. Native flag
		// completion proves that the actual registration did execute.
		if !errors.Is(err, os.ErrNotExist) || !parityCompletionLegacyPowerShell(result.Stdout) {
			t.Fatalf("legacy native PowerShell dispatch differs: callback %q read error %v stdout %s", callback, err, result.Stdout)
		}
		values, readErr := os.ReadFile(reply)
		if readErr != nil || !parityAuthoringJSON(values, []byte(`[]`)) {
			t.Fatal("legacy PowerShell unexpectedly returned cached candidates")
		}
		return nil, result
	}
	if err != nil {
		t.Fatalf("native %s did not execute the owned callback: %v; stdout %s", sh.name, err, result.Stdout)
	}
	if !parityCompletionCallbackReceipt(callback) {
		t.Fatalf("native %s lacks a complete successful exact issue-key callback receipt: %q", sh.name, callback)
	}
	raw, err := os.ReadFile(reply)
	if err != nil {
		t.Fatalf("native %s callback produced no candidate receipt: %v; stdout %s stderr %s", sh.name, err, result.Stdout, result.Stderr)
	}
	values := []string{}
	if sh.name == "elvish" || sh.name == "pwsh" {
		if json.Unmarshal(raw, &values) != nil {
			t.Fatal("native callback receipt is not candidate JSON")
		}
	} else {
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			value, _, _ := strings.Cut(line, "\t")
			if value != "" && !slices.Contains(values, value) {
				values = append(values, value)
			}
		}
	}
	if !slices.Equal(values, []string{"KEY-7"}) {
		t.Fatalf("native %s cached completion changed candidate identity: %q; callback %q stdout %s stderr %s", sh.name, values, callback, result.Stdout, result.Stderr)
	}
	return callback, result
}

func parityCompletionLegacyPowerShell(stdout []byte) bool {
	return parityAuthoringJSON(stdout, []byte(`{"candidates":[],"registration_control":["--output"],"legacy_positionals":[[]]}`))
}

func parityCompletionCallbackReceipt(callback []byte) bool {
	const invocation = "CALL\x00--@complete=issuekey\x00--\x00EXIT\x000\x00"
	return len(callback) != 0 && len(callback)%len(invocation) == 0 && bytes.Equal(callback, bytes.Repeat([]byte(invocation), len(callback)/len(invocation)))
}

func TestParityCompletionNativeReceiptGuards(t *testing.T) {
	const valid = "CALL\x00--@complete=issuekey\x00--\x00EXIT\x000\x00"
	if !parityCompletionCallbackReceipt([]byte(valid)) || !parityCompletionCallbackReceipt([]byte(valid+valid)) {
		t.Fatal("complete native callback receipts refused")
	}
	for name, bad := range map[string]string{
		"missing": "", "truncated": strings.TrimSuffix(valid, "\x00"),
		"failed":                 strings.Replace(valid, "EXIT\x000", "EXIT\x003", 1),
		"wrong-predictor":        strings.Replace(valid, "issuekey", "profile", 1),
		"foreign-argument":       strings.Replace(valid, "--\x00EXIT", "--\x00foreign\x00EXIT", 1),
		"missing-terminator":     strings.Replace(valid, "--\x00EXIT", "EXIT", 1),
		"incomplete-second-call": valid + "CALL\x00",
	} {
		t.Run(name, func(t *testing.T) {
			if parityCompletionCallbackReceipt([]byte(bad)) {
				t.Fatal("incomplete or different native callback admitted")
			}
		})
	}
	const legacy = `{"candidates":[],"registration_control":["--output"],"legacy_positionals":[[]]}`
	if !parityCompletionLegacyPowerShell([]byte(legacy)) {
		t.Fatal("independent delivered PowerShell observation refused")
	}
	for name, bad := range map[string]string{
		"registration-missing":  strings.Replace(legacy, `["--output"]`, `[]`, 1),
		"flattened-positionals": strings.Replace(legacy, `[[]]`, `[]`, 1),
		"unexpected-candidate":  strings.Replace(legacy, `"candidates":[]`, `"candidates":["KEY-7"]`, 1),
		"missing-observation":   `{}`, "null": `null`,
	} {
		t.Run(name, func(t *testing.T) {
			if parityCompletionLegacyPowerShell([]byte(bad)) {
				t.Fatal("unproven legacy PowerShell result admitted")
			}
		})
	}
}

// Only the longest incomplete recognized query is retained between PTY reads.
// Unrecognized terminal output remains in the independent diagnostic receipt.
type parityCompletionTerminalQueries struct{ pending []byte }

func (q *parityCompletionTerminalQueries) feed(chunk []byte) []byte {
	q.pending = append(q.pending, chunk...)
	var replies []byte
	for len(q.pending) != 0 {
		partial, consumed := false, false
		for _, query := range []struct{ request, response string }{
			{"\x1b[6n", "\x1b[1;1R"},
			{"\x1b[0c", "\x1b[?1;2c"},
			{"\x1b[?u", "\x1b[?0u"},
			{"\x1b[>0q", "\x1bP>|xterm(1)\x1b\\"},
			{"\x1b]11;?", "\x1b]11;rgb:0000/0000/0000\x1b\\"},
		} {
			if bytes.HasPrefix(q.pending, []byte(query.request)) {
				replies = append(replies, query.response...)
				q.pending = q.pending[len(query.request):]
				consumed = true
				break
			}
			partial = partial || bytes.HasPrefix([]byte(query.request), q.pending)
		}
		if partial && !consumed {
			break
		}
		if !consumed {
			q.pending = q.pending[1:]
		}
	}
	q.pending = bytes.Clone(q.pending)
	return replies
}

func TestParityCompletionTerminalSplitQueries(t *testing.T) {
	for _, tc := range []struct{ query, reply string }{
		{"\x1b[6n", "\x1b[1;1R"},
		{"\x1b[0c", "\x1b[?1;2c"},
		{"\x1b[?u", "\x1b[?0u"},
		{"\x1b[>0q", "\x1bP>|xterm(1)\x1b\\"},
		{"\x1b]11;?", "\x1b]11;rgb:0000/0000/0000\x1b\\"},
	} {
		for split := 0; split <= len(tc.query); split++ {
			var state parityCompletionTerminalQueries
			reply := append(state.feed([]byte("noise"+tc.query[:split])), state.feed([]byte(tc.query[split:]+"tail"))...)
			if string(reply) != tc.reply || len(state.pending) != 0 || len(state.feed(nil)) != 0 {
				t.Fatalf("split query %q at byte %d lost or repeated its answer: %q", tc.query, split, reply)
			}
		}
		var state parityCompletionTerminalQueries
		var reply []byte
		for _, value := range []byte(tc.query + tc.query) {
			reply = append(reply, state.feed([]byte{value})...)
		}
		if string(reply) != tc.reply+tc.reply {
			t.Fatal("bytewise adjacent native queries lost an answer")
		}
	}
}
