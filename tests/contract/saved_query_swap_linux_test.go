//go:build queryswap && linux

package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestSavedQueryFileSwapKeepsOpenedRegularSnapshot(t *testing.T) {
	t.Parallel()
	tracer := os.Getenv("JIRA_TEST_STRACE")
	if tracer == "" {
		tracer = "strace"
	}
	tracer, err := exec.LookPath(tracer)
	if err != nil {
		t.Fatalf("native saved-query swap acceptance requires strace: %v", err)
	}
	bin := jiraBinary(t)
	for _, tc := range []struct {
		name      string
		link      bool
		interrupt bool
	}{
		{name: "direct"},
		{name: "symlink", link: true},
		{name: "direct_canceled", interrupt: true},
		{name: "symlink_canceled", link: true, interrupt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			dir := filepath.Join(root, "queries")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "inbox.jql")
			target := path
			if tc.link {
				target = filepath.Join(root, "saved-input")
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			const original = "---\nname: Personal inbox\ndescription: Saved work\nproject: OTHER\n---\nproject = ABC ORDER BY key ASC\n"
			if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			trace := filepath.Join(root, "trace.log")
			held := filepath.Join(root, "held-input")
			var timer *time.Timer
			onStart := func(process *os.Process) {
				deadline := time.Now().Add(10 * time.Second)
				for {
					data, readErr := os.ReadFile(trace)
					if readErr != nil && !os.IsNotExist(readErr) {
						t.Fatal(readErr)
					}
					if strings.Contains(string(data), "S_IFREG") && strings.Contains(string(data), "DELAYED") {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("tracer never paused a successful regular query stat: %s", data)
					}
					time.Sleep(5 * time.Millisecond)
				}
				children, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", process.Pid, process.Pid))
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(strings.TrimSpace(string(children)))
				if err != nil || pid <= 0 {
					t.Fatalf("tracer did not own exactly one CLI child: %q, %v", children, err)
				}
				child, err := os.FindProcess(pid)
				if err != nil {
					t.Fatal(err)
				}
				// Kill only the blocked CLI; its tracer remains alive to reap it.
				timer = time.AfterFunc(6*time.Second, func() { _ = child.Kill() })
				t.Cleanup(func() {
					timer.Stop()
					_ = child.Release()
				})
				if err := os.Rename(target, held); err != nil {
					t.Fatal(err)
				}
				if err := unix.Mkfifo(target, 0o600); err != nil {
					t.Fatal(err)
				}
				if tc.interrupt {
					if err := child.Signal(os.Interrupt); err != nil {
						t.Fatal(err)
					}
				}
			}
			// The delay is after the real kernel stat succeeds. It leaves time
			// to replace the pathname before Go receives that result. A regular
			// opened descriptor must still yield its coherent original contents.
			stdout, stderr, exit := runExecutableIOInDirStarted(t, tracer, root,
				[]string{"JIRA_QUERIES_PATH=" + dir, "JIRA_KEYRING=false", "AGENT=1"}, nil, nil, onStart,
				"--follow-forks", "--kill-on-exit", "--quiet=all", "--signal=none",
				"--trace=openat,newfstatat,fstat,statx", "--trace-path="+target, "--trace-path="+path,
				"--inject=newfstatat,fstat,statx:delay_exit=2s", "-o", trace, "--", bin,
				"issue", "list", "--saved=inbox", "--as-jql", "--output=json", "--no-input")
			if timer != nil {
				timer.Stop()
			}
			data, err := os.ReadFile(trace)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("native query stat and open trace: %s", data)
			data, err = os.ReadFile(held)
			if err != nil || string(data) != original {
				t.Fatalf("saved-query read changed authored bytes: %q, %v", data, err)
			}
			if tc.interrupt {
				var refusal struct {
					OK     bool            `json:"ok"`
					Data   json.RawMessage `json:"data"`
					Errors []struct {
						Code string `json:"code"`
					} `json:"errors"`
				}
				if exit != cli.ExitCanceled || !strings.Contains(stderr, "context canceled") || json.Unmarshal([]byte(stdout), &refusal) != nil || refusal.OK || len(refusal.Errors) != 1 || refusal.Errors[0].Code != cli.CodeCanceled || (string(refusal.Data) != "null" && len(refusal.Data) != 0) {
					t.Fatalf("saved-query pathname swap lost cancellation or fabricated a result: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
				}
				return
			}
			var out struct {
				OK   bool `json:"ok"`
				Data struct {
					JQL        string                   `json:"jql"`
					SavedQuery *envelope.SavedQueryView `json:"saved_query"`
				} `json:"data"`
			}
			if exit != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &out) != nil || !out.OK || out.Data.SavedQuery == nil {
				t.Fatalf("saved-query pathname swap blocked or lost its result: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
			}
			q := out.Data.SavedQuery
			if out.Data.JQL != "project = ABC ORDER BY key ASC" || q.JQL != out.Data.JQL || q.Key != "inbox" || q.Name != "Personal inbox" || q.Description != "Saved work" || q.Project != "OTHER" {
				t.Fatalf("opened saved-query snapshot lost authored input: %+v", out)
			}
		})
	}
}
