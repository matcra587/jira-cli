//go:build linux

package contract

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gechr/x/shell"
	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/adapters"
	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
)

func TestContextualPathCompletionInsertsLiteralArgvInNativeEditors(t *testing.T) {
	for _, name := range []string{"bash", "fish", "zsh", "elvish"} {
		t.Run(name, func(t *testing.T) {
			binary := name
			if name == "bash" && os.Getenv("JIRA_TEST_BASH_BINARY") != "" {
				binary = os.Getenv("JIRA_TEST_BASH_BINARY")
			}
			if name == "elvish" && os.Getenv("JIRA_TEST_ELVISH_BINARY") != "" {
				binary = os.Getenv("JIRA_TEST_ELVISH_BINARY")
			}
			executable, err := exec.LookPath(binary)
			if err != nil {
				if binary != name {
					t.Fatal("requested native shell unavailable", err)
				}
				t.Skip("native shell unavailable: " + name)
			}
			for _, filename := range []string{"candidate space.txt", "candidate,comma.txt", `candidate"quote.txt`, "candidate$HOME;$(touch injected)'quote.txt"} {
				for _, role := range []string{"attachment", "file", "csv", "inline", "theme"} {
					t.Run(role+"/"+filename, func(t *testing.T) {
						dir := t.TempDir()
						if err := os.WriteFile(filepath.Join(dir, filename), []byte("unread contents"), 0o000); err != nil {
							t.Fatal(err)
						}
						line := "jira issue attachment add KEY-1 candidate"
						want := []string{"issue", "attachment", "add", "KEY-1", filename}
						switch role {
						case "file":
							line = "jira issue attachment add KEY-1 --file candidate"
						case "csv":
							line = "jira issue attachment add KEY-1 --file '\"earlier,selected\",candidate'"
						case "inline":
							line = "jira issue attachment add KEY-1 --file=candidate"
						case "theme":
							line = "jira config set theme_path candidate"
							want = []string{"config", "set", "theme_path", filepath.Join(dir, filename)}
						}
						got := nativeCompletionEditorArgv(t, name, executable, dir, line)
						if role == "file" || role == "csv" || role == "inline" {
							var value string
							if role == "inline" {
								if len(got) != 5 || !strings.HasPrefix(got[4], "--file=") {
									t.Fatal("inline completion changed command argv", got)
								}
								value = strings.TrimPrefix(got[4], "--file=")
							} else if len(got) != 6 || !slices.Equal(got[:5], []string{"issue", "attachment", "add", "KEY-1", "--file"}) {
								t.Fatal("CSV completion changed command argv", got)
							} else {
								value = got[5]
							}
							fields, err := csv.NewReader(strings.NewReader(value)).Read()
							wantFields := []string{filename}
							if role == "csv" {
								wantFields = append([]string{"earlier,selected"}, wantFields...)
							}
							if err != nil || !slices.Equal(fields, wantFields) {
								t.Fatal("native editor changed CSV file identity", got, fields, err)
							}
						} else if !slices.Equal(got, want) {
							t.Fatal("native editor changed literal file identity", got, want)
						}
						if _, err := os.Stat(filepath.Join(dir, "injected")); !errors.Is(err, os.ErrNotExist) {
							t.Fatal("native completion executed filename content", err)
						}
					})
				}
			}
		})
	}
}

func TestBashContextualCompletionPreservesDashPathsAndTerminators(t *testing.T) {
	executable, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("native Bash unavailable")
	}
	for _, tc := range []struct {
		line string
		want []string
	}{
		{line: "jira issue attachment add KEY-1 -- -candidate", want: []string{"issue", "attachment", "add", "KEY-1", "--", "-candidate.txt"}},
		{line: "jira issue attachment add KEY-1 --file -candidate", want: []string{"issue", "attachment", "add", "KEY-1", "--file", "-candidate.txt"}},
		{line: "jira issue attachment add KEY-1 --file=-candidate", want: []string{"issue", "attachment", "add", "KEY-1", "--file=-candidate.txt"}},
	} {
		t.Run(tc.line, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "-candidate.txt"), nil, 0o000); err != nil {
				t.Fatal(err)
			}
			if got := nativeCompletionEditorArgv(t, "bash", executable, dir, tc.line); !slices.Equal(got, tc.want) {
				t.Fatal("dash prefix or terminator changed literal argv", got, tc.want)
			}
		})
	}
}

func TestElvishContextualCompletionCanReloadGeneratedScript(t *testing.T) {
	name := os.Getenv("JIRA_TEST_ELVISH_BINARY")
	explicit := name != ""
	if name == "" {
		name = "elvish"
	}
	executable, err := exec.LookPath(name)
	if err != nil {
		if explicit {
			t.Fatal("requested native Elvish unavailable", err)
		}
		t.Skip("native Elvish unavailable")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "candidate,comma.txt"), nil, 0o000); err != nil {
		t.Fatal(err)
	}
	got := nativeCompletionEditorArgv(t, "elvish", executable, dir, "jira issue attachment add KEY-1 --file candidate", nativeCompletionEditorOptions{reload: true})
	if len(got) != 6 || got[5] != `"candidate,comma.txt"` {
		t.Fatal("reloaded generator changed literal CSV argv", got)
	}
}

type nativeCompletionEditorOptions struct {
	reload       bool
	env          []string
	continuation string
}

func TestContextualNativeCompletionPreservesNestedPathsAndPublicSelection(t *testing.T) {
	env, selected := prepareBinaryMetadataCompletion(t)
	contents, err := os.ReadFile(selected)
	if err != nil {
		t.Fatal(err)
	}
	selected = filepath.Join(t.TempDir(), "selected settings.toml")
	if err := os.WriteFile(selected, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	disk, err := adapters.NewDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	store, err := adapters.NewScopedCache(disk, "https://example.com", "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := cachecmd.RecordIssueKeys(store, []string{"DEMO-1"}); err != nil {
		t.Fatal(err)
	}
	env = append(env, "JIRA_BASE_URL=", "JIRA_EMAIL=")
	for _, name := range []string{"bash", "fish", "zsh", "elvish"} {
		t.Run(name, func(t *testing.T) {
			binary := name
			if name == "bash" && os.Getenv("JIRA_TEST_BASH_BINARY") != "" {
				binary = os.Getenv("JIRA_TEST_BASH_BINARY")
			}
			if name == "elvish" && os.Getenv("JIRA_TEST_ELVISH_BINARY") != "" {
				binary = os.Getenv("JIRA_TEST_ELVISH_BINARY")
			}
			executable, err := exec.LookPath(binary)
			if err != nil {
				if binary != name {
					t.Fatal("requested native shell unavailable", err)
				}
				t.Skip("native shell unavailable: " + name)
			}
			for _, role := range []string{"nested", "directory", "unicode directory", "csv directory", "selected", "files select issues"} {
				t.Run(role, func(t *testing.T) {
					dir := t.TempDir()
					if err := os.Mkdir(filepath.Join(dir, "candidate dir"), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, "candidate dir", "leaf.txt"), nil, 0o000); err != nil {
						t.Fatal(err)
					}
					line := "jira issue attachment add KEY-1 'candidate dir/lea'"
					want := []string{"issue", "attachment", "add", "KEY-1", "candidate dir/leaf.txt"}
					options := nativeCompletionEditorOptions{env: env}
					switch role {
					case "directory":
						line = "jira issue attachment add KEY-1 candidate"
						options.continuation = "leaf.txt"
					case "unicode directory":
						if err := os.Rename(filepath.Join(dir, "candidate dir"), filepath.Join(dir, "candidate café")); err != nil {
							t.Fatal(err)
						}
						line = "jira issue attachment add KEY-1 candidate"
						options.continuation = "lea\t"
						want = []string{"issue", "attachment", "add", "KEY-1", "candidate café/leaf.txt"}
					case "csv directory":
						if err := os.Rename(filepath.Join(dir, "candidate dir"), filepath.Join(dir, "candidate,dir")); err != nil {
							t.Fatal(err)
						}
						line = "jira issue attachment add KEY-1 --file candidate"
						options.continuation = "lea\t"
						want = []string{"issue", "attachment", "add", "KEY-1", "--file", `"candidate,dir/leaf.txt"`}
					case "selected":
						line = "jira --json --config " + shell.Quote(selected) + " config set default_project DE"
						want = []string{"--json", "--config", selected, "config", "set", "default_project", "DEMO"}
					case "files select issues":
						line = "jira --config " + shell.Quote(selected) + " issue attachment add KEY-1 --file '\"earlier,selected\"' DEM"
						want = []string{"--config", selected, "issue", "attachment", "add", "KEY-1", "--file", `"earlier,selected"`, "DEMO-1"}
					}
					if got := nativeCompletionEditorArgv(t, name, executable, dir, line, options); !slices.Equal(got, want) {
						t.Fatal("native context or nested path changed argv", got, want)
					}
				})
			}
		})
	}
}

func nativeCompletionEditorArgv(t *testing.T, name, executable, dir, line string, optional ...nativeCompletionEditorOptions) []string {
	t.Helper()
	var options nativeCompletionEditorOptions
	if len(optional) != 0 {
		options = optional[0]
	}
	env := []string{"JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_BASE_URL=https://unreachable.example.invalid", "XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "XDG_CACHE_HOME=" + filepath.Join(dir, "cache")}
	env = append(env, options.env...)
	script, stderr, exit := runBinaryEnv(t, env, "completion", name)
	if exit != 0 || stderr != "" {
		t.Fatal("native generator failed", exit, stderr)
	}
	if options.reload {
		script += "\n" + script
	}
	fixture := filepath.Join(dir, "bin")
	if err := os.Mkdir(fixture, 0o700); err != nil {
		t.Fatal(err)
	}
	observer, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim := "#!/bin/sh\ncase \"$1\" in --@complete=*|--@shell=*) exec " + shell.Quote(jiraBinary(t)) + " \"$@\";; esac\nexec " + shell.Quote(observer) + " -test.run '^TestNativeCompletionArgumentObserver$' -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(fixture, "jira"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	rc := filepath.Join(dir, "completion.rc")
	var args []string
	switch name {
	case "bash":
		script += "\nPS1='NATIVE_READY> '\nPS2=''\nbind -x '\"\\C-x\\C-v\":printf ready > \"$JIRA_TEST_COMPLETION_READY\"'\n"
		args = []string{"--noprofile", "--rcfile", rc, "-i"}
	case "fish":
		script += "\nfunction fish_prompt; printf 'NATIVE_READY> '; end\nif test (string split . -- $version)[1] -lt 4\n    bind -k f12 'printf ready > $JIRA_TEST_COMPLETION_READY'\nelse\n    bind f12 'printf ready > $JIRA_TEST_COMPLETION_READY'\nend\n"
		args = []string{"--no-config", "--init-command", "source " + shell.Quote(rc), "-i"}
	case "zsh":
		script = "autoload -Uz compinit\ncompinit -D\n" + script + "\nPROMPT='NATIVE_READY> '\nfunction native_completion_ready() { print -r -- ready > \"$JIRA_TEST_COMPLETION_READY\"; }\nzle -N native_completion_ready\nbindkey '\\e[24~' native_completion_ready\n"
		rc = filepath.Join(dir, ".zshrc")
		args = []string{"-i"}
	case "elvish":
		script += "\nset edit:prompt = { put 'NATIVE_READY> ' }\nset edit:rprompt = (constantly '')\nset edit:global-binding[F12] = { echo ready > $E:JIRA_TEST_COMPLETION_READY }\n"
		args = []string{"-rc", rc}
	}
	if err := os.WriteFile(rc, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(dir, "argv.json")
	ready := filepath.Join(dir, "completion.ready")
	coverage := filepath.Join(dir, "coverage")
	if err := os.Mkdir(coverage, 0o700); err != nil {
		t.Fatal(err)
	}
	master, slave := editorPTY(t)
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 30, Col: 160}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), executable, args...)
	cmd.Dir = dir
	cmd.Env = append(env, "PATH="+fixture+string(os.PathListSeparator)+os.Getenv("PATH"), "TERM=xterm-256color", "ZDOTDIR="+dir, "XDG_DATA_HOME="+filepath.Join(dir, "data"), "XDG_STATE_HOME="+filepath.Join(dir, "state"), "JIRA_TEST_COMPLETION_OBSERVER=1", "JIRA_TEST_COMPLETION_RECEIPT="+receipt, "JIRA_TEST_COMPLETION_READY="+ready, "GOCOVERDIR="+coverage)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	if err := unix.SetNonblock(int(master.Fd()), true); err != nil {
		t.Fatal(err)
	}
	var screen bytes.Buffer
	read := func() {
		buffer := make([]byte, 64*1024)
		count, err := unix.Read(int(master.Fd()), buffer)
		if err == nil && count > 0 {
			screen.Write(buffer[:count])
			if bytes.Contains(buffer[:count], []byte("\x1b[6n")) {
				_, _ = master.Write([]byte("\x1b[1;1R"))
			}
			if bytes.Contains(buffer[:count], []byte("\x1b[0c")) {
				_, _ = master.Write([]byte("\x1b[?1;2c"))
			}
			if bytes.Contains(buffer[:count], []byte("\x1b[?u")) {
				_, _ = master.Write([]byte("\x1b[?0u"))
			}
			if bytes.Contains(buffer[:count], []byte("\x1b[>0q")) {
				_, _ = master.Write([]byte("\x1bP>|xterm(1)\x1b\\"))
			}
			if bytes.Contains(buffer[:count], []byte("\x1b]11;?")) {
				_, _ = master.Write([]byte("\x1b]11;rgb:0000/0000/0000\x1b\\"))
			}
		}
	}
	deadline := time.Now().Add(8 * time.Second)
	for !strings.Contains(screen.String(), "NATIVE_READY>") {
		if time.Now().After(deadline) {
			t.Fatal("native editor never reached prompt", screen.String())
		}
		_, _ = unix.Poll([]unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}, 20)
		read()
	}
	boundary := "\x1b[24~"
	if name == "bash" {
		boundary = "\x18\x16"
	}
	if _, err := fmt.Fprint(master, line+"\t"+boundary); err != nil {
		t.Fatal(err)
	}
	// The editor's next bound key runs only after Tab handling. Its marker
	// provides an actual completion boundary rather than a scheduling sleep.
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("native editor did not finish Tab handling", screen.String())
		}
		_, _ = unix.Poll([]unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}, 20)
		read()
	}
	enter := "\r"
	if name == "elvish" {
		// Elvish's first Enter accepts the selected completion menu item.
		enter += "\r"
	}
	if options.continuation != "" {
		if name == "elvish" {
			// Accept its menu before continuing the same argument.
			options.continuation = "\r" + options.continuation
			if !strings.Contains(options.continuation, "\t") {
				enter = "\r"
			}
		}
	}
	if _, err := fmt.Fprint(master, options.continuation+enter); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		encoded, err := os.ReadFile(receipt)
		if err == nil {
			var args []string
			if err := json.Unmarshal(encoded, &args); err == nil {
				t.Logf("native editor screen: %s", screen.String())
				return args
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		_, _ = unix.Poll([]unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}, 20)
		read()
	}
	t.Fatal("native completion did not execute owned argv observer", screen.String())
	return nil
}

func TestFishFileCompletionPreservesSeparateFlagLikeValue(t *testing.T) {
	executable, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("native Fish unavailable")
	}
	for _, tc := range []struct {
		line, filename string
		want           []string
	}{
		{"jira issue attachment add KEY-1 --file --file=candidate", "--file=candidate.txt", []string{"issue", "attachment", "add", "KEY-1", "--file", `--file=candidate`}},
		{"jira issue attachment add KEY-1 --file=--file=candidate", "--file=candidate.txt", []string{"issue", "attachment", "add", "KEY-1", `--file="--file=candidate.txt"`}},
		{"jira issue attachment add KEY-1 --file '--literal=candidate'", "--literal=candidate.txt", []string{"issue", "attachment", "add", "KEY-1", "--file", `--literal=candidate.txt`}},
		{"jira issue attachment add KEY-1 --file '\"--file=candidate\"'", "--file=candidate.txt", []string{"issue", "attachment", "add", "KEY-1", "--file", `"--file=candidate.txt"`}},
	} {
		t.Run(tc.line, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tc.filename), nil, 0o000); err != nil {
				t.Fatal(err)
			}
			got := nativeCompletionEditorArgv(t, "fish", executable, dir, tc.line)
			if !slices.Equal(got, tc.want) {
				t.Fatal("Fish removed literal flag-like value", got, tc.want)
			}
		})
	}
}

func TestBashNativeFileAndDirectoryHintsInsertLiteralArgv(t *testing.T) {
	binary := os.Getenv("JIRA_TEST_BASH_BINARY")
	if binary == "" {
		binary = "bash"
	}
	executable, err := exec.LookPath(binary)
	if err != nil {
		t.Fatal("requested native Bash unavailable", err)
	}
	for _, role := range []string{"file", "directory"} {
		t.Run(role, func(t *testing.T) {
			dir := t.TempDir()
			line := "jira --config candidate"
			want := []string{"--config", "candidate space.toml"}
			if role == "file" {
				if err := os.WriteFile(filepath.Join(dir, want[1]), nil, 0o000); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(filepath.Join(dir, "candidate dir"), 0o700); err != nil {
					t.Fatal(err)
				}
				line = "jira issue edit inspect --directory candidate"
				want = []string{"issue", "edit", "inspect", "--directory", "candidate dir/"}
			}
			got := nativeCompletionEditorArgv(t, "bash", executable, dir, line)
			if !slices.Equal(got, want) {
				t.Fatal("native path hint changed literal argv", got, want)
			}
		})
	}
}
