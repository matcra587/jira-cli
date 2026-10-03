package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xos "github.com/gechr/x/os"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type attachmentReceiptFrame struct {
	OK   bool `json:"ok"`
	Meta struct {
		Command  string `json:"command"`
		ExitCode *int   `json:"exit_code"`
	} `json:"meta"`
	Data   envelope.IssueAttachmentAddOutput `json:"data"`
	Errors []envelope.Error                  `json:"errors"`
}

func TestAttachmentFailureReceiptBrokenPipeUsesOutputExitContract(t *testing.T) {
	t.Parallel()
	if !xos.IsUnix() {
		t.Skip("Unix SIGPIPE contract")
	}
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "authored.txt")
			if err := os.WriteFile(path, []byte("authored"), 0o600); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if _, err := io.Copy(io.Discard, req.Body); err != nil {
					t.Error(err)
				}
				calls.Add(1)
				w.WriteHeader(http.StatusNotFound)
			}))
			t.Cleanup(server.Close)
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Close() }()
			dir := t.TempDir()
			cmd := exec.CommandContext(t.Context(), jiraBinary(t), "issue", "attachment", "add", "KEY-1", path, "--output="+mode, "--no-input")
			cmd.Dir = dir
			cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "NO_COLOR=1", "TERM=dumb"}, editReceiptEnv(server.URL)...)
			var stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = writer, &stderr
			err = cmd.Run()
			var failure *exec.ExitError
			if !errors.As(err, &failure) || failure.ExitCode() != cli.ExitOutput || calls.Load() != 1 {
				t.Fatalf("failed receipt output bypassed exit8 or replayed its upload: err=%v calls=%d stderr=%q", err, calls.Load(), stderr.String())
			}
		})
	}
}

func TestAttachmentTotalFailureRetainsOrderedOutcomes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "authored.txt")
			if err := os.WriteFile(path, []byte("authored"), 0o600); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodPost || req.Header.Get("X-Atlassian-Token") != "no-check" {
					t.Error("upload changed its write method or XSRF header")
				}
				if _, err := io.Copy(io.Discard, req.Body); err != nil {
					t.Error(err)
				}
				mu.Lock()
				calls = append(calls, req.URL.Path)
				mu.Unlock()
				switch req.URL.Path {
				case "/rest/api/3/issue/KEY-1/attachments":
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"errorMessages":["fixture absent \u001b[2Jdisplay\u0007"]}`))
				case "/rest/api/3/issue/KEY-2/attachments":
					_, _ = w.Write([]byte("{"))
				default:
					t.Error("upload changed its issue targets")
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			args := []string{"issue", "attachment", "add", "KEY-1", "KEY-2", "--file", path, "--parallelism=1", "--no-input"}
			extra := editReceiptEnv(server.URL)
			if mode == "agent" {
				extra = append(extra, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			if mode == "json" || mode == "compact" {
				args = append(args, "--jq", `error("failed uploads must bypass filters")`)
			}
			stdout, stderr, exit := runBinaryEnv(t, extra, args...)
			mu.Lock()
			got := append([]string(nil), calls...)
			mu.Unlock()
			if exit != cli.ExitServer || len(got) != 2 || got[0] != "/rest/api/3/issue/KEY-1/attachments" || got[1] != "/rest/api/3/issue/KEY-2/attachments" {
				t.Fatalf("failed uploads lost severity, order or replay policy: exit=%d calls=%v stderr=%q", exit, got, stderr)
			}
			if strings.ContainsAny(stderr, "\x1b\a") {
				t.Fatal("upload diagnostics forwarded terminal controls")
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1:") || !strings.Contains(stdout, "fixture absent display") || !strings.Contains(stdout, "KEY-2:") || strings.ContainsAny(stdout, "\x1b\a") {
					t.Fatalf("human upload failure lost sanitized per-key outcomes: %q", stdout)
				}
				return
			}
			var frame attachmentReceiptFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.Command != "issue.attachment.add" || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitServer || len(frame.Errors) != 1 || frame.Errors[0].Code != "attachment_response_invalid" || !strings.Contains(frame.Errors[0].Hint, "before retrying") {
				t.Fatal("retained upload failure changed classification or lost its full frame")
			}
			out := frame.Data
			if out.DryRun || len(out.Files) != 1 || out.Files[0] != path || len(out.FileDetails) != 1 || out.FileDetails[0].Path != path || out.FileDetails[0].Size != 8 || out.FileDetails[0].MIMEInferred != "text/plain" || len(out.Results) != 2 {
				t.Fatal("failed upload discarded authored metadata or per-key outcomes")
			}
			for i, code := range []string{"jira_not_found", "attachment_response_invalid"} {
				r := out.Results[i]
				if r.Issue.Key != []string{"KEY-1", "KEY-2"}[i] || r.OK || r.Data != nil || r.Err == nil || r.Err.Code != code {
					t.Fatal("failed upload fabricated success or changed ordered verdicts")
				}
			}
		})
	}
}

func TestAttachmentInterruptRetainsCompletedAndQueuedOutcomes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported for subprocesses on Windows")
	}
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "authored.txt")
			if err := os.WriteFile(path, []byte("authored"), 0o600); err != nil {
				t.Fatal(err)
			}
			secondStarted := make(chan struct{})
			var mu sync.Mutex
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if _, err := io.Copy(io.Discard, req.Body); err != nil {
					t.Error(err)
				}
				mu.Lock()
				calls = append(calls, req.URL.Path)
				mu.Unlock()
				switch req.URL.Path {
				case "/rest/api/3/issue/KEY-1/attachments":
					_, _ = w.Write([]byte(`[{"id":"10042","filename":"authored.txt","size":8}]`))
				case "/rest/api/3/issue/KEY-2/attachments":
					close(secondStarted)
					<-req.Context().Done()
				default:
					t.Error("canceled batch started a queued upload or replayed a write")
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnvStarted(t, editReceiptEnv(server.URL), func(process *os.Process) {
				select {
				case <-secondStarted:
				case <-time.After(5 * time.Second):
					t.Fatal("upload did not reach the independent cancellation stimulus")
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}, "issue", "attachment", "add", "KEY-1", "KEY-2", "KEY-3", "--file", path, "--parallelism=1", "--output="+mode, "--no-input")
			mu.Lock()
			got := append([]string(nil), calls...)
			mu.Unlock()
			if exit != cli.ExitCanceled || len(got) != 2 || got[0] != "/rest/api/3/issue/KEY-1/attachments" || got[1] != "/rest/api/3/issue/KEY-2/attachments" {
				t.Fatalf("interrupted batch lost cancellation or sent a queued/replayed upload: exit=%d calls=%v stderr=%q", exit, got, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1: 1 file attached.") || !strings.Contains(stdout, "10042") || !strings.Contains(stdout, "KEY-2:") || !strings.Contains(stdout, "KEY-3:") {
					t.Fatalf("human cancellation discarded its completed attachment identity or failed targets: %q", stdout)
				}
				return
			}
			var frame attachmentReceiptFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitCanceled || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeCanceled || len(frame.Data.Results) != 3 {
				t.Fatal("interrupted upload discarded its joined receipt")
			}
			for i, key := range []string{"KEY-1", "KEY-2", "KEY-3"} {
				r := frame.Data.Results[i]
				if r.Issue.Key != key {
					t.Fatal("cancellation reordered upload outcomes")
				}
				if i == 0 {
					if !r.OK || r.Data == nil || len(r.Data.Attachments) != 1 || r.Data.Attachments[0].ID != "10042" || r.Data.Attachments[0].Filename != "authored.txt" || r.Data.Attachments[0].Size != 8 {
						t.Fatal("cancellation discarded a completed resource identity")
					}
				} else if r.OK || r.Data != nil || r.Err == nil || r.Err.Code != cli.CodeCanceled {
					t.Fatal("canceled or queued upload invented completion")
				}
			}
		})
	}
}
