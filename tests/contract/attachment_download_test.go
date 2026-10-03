package contract

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestAttachmentOverwriteBinaryPreservesTargetUntilComplete(t *testing.T) {
	t.Parallel()
	content := bytes.Repeat([]byte{0, 0xff, '<', '&', '>', '\n'}, 8192)
	for _, mode := range []string{"json", "compact", "human"} {
		for _, outcome := range []string{"success", "request-failure", "stream-failure"} {
			t.Run(mode+"/"+outcome, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				path := filepath.Join(dir, "report.bin")
				const original = "authored contents"
				if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
					t.Fatal(err)
				}
				before, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					calls.Add(1)
					if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/attachment/content/10042" {
						t.Error("download changed its read-only content request")
					}
					contents, err := os.ReadFile(path)
					if err != nil || string(contents) != original {
						t.Error("overwrite changed the target before receiving complete bytes")
					}
					if outcome == "request-failure" {
						w.WriteHeader(http.StatusNotFound)
						_, _ = w.Write([]byte(`{"errorMessages":["content unavailable"]}`))
						return
					}
					w.Header().Set("Content-Type", "application/octet-stream")
					w.Header().Set("Content-Length", strconv.Itoa(len(content)))
					if outcome == "stream-failure" {
						_, _ = w.Write(content[:100])
						return
					}
					_, _ = w.Write(content)
				}))
				t.Cleanup(server.Close)
				env := append(editReceiptEnv(server.URL), "JIRA_READ_ONLY=true")
				stdout, stderr, exit := runBinaryEnv(t, env, "issue", "attachment", "download", "10042", "--to", dir, "--as=report.bin", "--overwrite", "--output="+mode, "--no-input")
				if calls.Load() != 1 {
					t.Fatalf("content request was missing or replayed: %d", calls.Load())
				}
				contents, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("download removed the target: %v", err)
				}
				if outcome == "success" {
					if exit != 0 || !bytes.Equal(contents, content) {
						t.Fatalf("successful overwrite lost bytes: exit=%d stderr=%q", exit, stderr)
					}
					if mode != "human" {
						data := []byte(stdout)
						if mode == "json" {
							var result apiFrame
							if json.Unmarshal(data, &result) != nil || !result.OK {
								t.Fatal("successful download lacks its envelope")
							}
							data = result.Data
						}
						var out envelope.IssueAttachmentDownloadOutput
						if json.Unmarshal(data, &out) != nil || out.DryRun || !out.FilenameResolved || out.BytesWritten == nil || *out.BytesWritten != int64(len(content)) || out.Path != path || out.Overwrote == nil || !*out.Overwrote {
							t.Fatal("download receipt lost publication facts")
						}
					}
				} else {
					if exit == 0 || string(contents) != original {
						t.Fatalf("failed overwrite destroyed the original: exit=%d stderr=%q", exit, stderr)
					}
					after, err := os.Stat(path)
					if err != nil || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
						t.Fatal("failed overwrite changed original metadata")
					}
					if mode != "human" {
						var result apiFrame
						if json.Unmarshal([]byte(stdout), &result) != nil || result.OK || len(result.Errors) != 1 || len(result.Data) != 0 && string(result.Data) != "null" {
							t.Fatal("failed download fabricated success data")
						}
					}
				}
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 1 || entries[0].Name() != "report.bin" {
					t.Fatal("download left staging files")
				}
			})
		}
	}
}

func TestAttachmentEmptyDownloadPublishesExplicitZeroByteCount(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/attachment/content/10042" {
			t.Error("empty download changed its content request")
		}
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	for _, mode := range []string{"json", "compact"} {
		dir := t.TempDir()
		stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "attachment", "download", "10042", "--to", dir, "--as=empty.bin", "--output="+mode, "--no-input")
		if exit != 0 {
			t.Fatalf("empty download failed: exit=%d stderr=%q", exit, stderr)
		}
		data := []byte(stdout)
		if mode == "json" {
			var frame apiFrame
			if json.Unmarshal(data, &frame) != nil || !frame.OK {
				t.Fatal("empty download lost its success envelope")
			}
			data = frame.Data
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil || string(fields["bytes_written"]) != "0" {
			t.Fatal("empty download omitted its actual zero byte count")
		}
		info, err := os.Stat(filepath.Join(dir, "empty.bin"))
		if err != nil || info.Size() != 0 {
			t.Fatal("empty download did not create a complete empty file")
		}
	}
}

func TestAttachmentDownloadPreviewChecksDestinationWithoutWriting(t *testing.T) {
	t.Parallel()
	for _, remote := range []bool{false, true} {
		for _, overwrite := range []bool{false, true} {
			for _, target := range []string{"absent", "file", "directory", "outside-link", "missing-directory"} {
				t.Run(strconv.FormatBool(remote)+"/"+strconv.FormatBool(overwrite)+"/"+target, func(t *testing.T) {
					t.Parallel()
					dir := t.TempDir()
					outside := filepath.Join(t.TempDir(), "original")
					if err := os.WriteFile(outside, []byte("outside contents"), 0o640); err != nil {
						t.Fatal(err)
					}
					path := filepath.Join(dir, "report.bin")
					switch target {
					case "file":
						if err := os.WriteFile(path, []byte("original contents"), 0o640); err != nil {
							t.Fatal(err)
						}
					case "directory":
						if err := os.Mkdir(path, 0o700); err != nil {
							t.Fatal(err)
						}
					case "outside-link":
						if err := os.Symlink(outside, path); err != nil {
							t.Skipf("symlink unavailable: %v", err)
						}
					case "missing-directory":
						dir = filepath.Join(dir, "missing")
					}
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
						calls.Add(1)
						if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/attachment/10042" {
							t.Error("preview requested content or changed its metadata read")
						}
						_, _ = w.Write([]byte(`{"id":10042,"filename":"report.bin","size":17}`))
					}))
					t.Cleanup(server.Close)
					args := []string{"issue", "attachment", "download", "10042", "--to", dir, "--dry-run", "--output=json", "--no-input"}
					if remote {
						args = append(args, "--validate-remote")
					} else {
						args = append(args, "--as=report.bin")
					}
					if overwrite {
						args = append(args, "--overwrite")
					}
					stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
					wantOK := target == "absent" || target == "file" && overwrite
					var frame apiFrame
					if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK != wantOK || (exit == 0) != wantOK {
						t.Fatalf("preview failed to check its destination: target=%s overwrite=%v exit=%d stderr=%q", target, overwrite, exit, stderr)
					}
					wantCalls := int32(0)
					if remote {
						wantCalls = 1
					}
					if calls.Load() != wantCalls {
						t.Fatalf("preview made %d requests, want %d metadata reads", calls.Load(), wantCalls)
					}
					if !wantOK && (len(frame.Errors) != 1 || frame.Errors[0].Code != "local_io_failed") {
						t.Fatal("destination refusal lost its local I/O classification")
					}
					if target == "missing-directory" {
						if _, err := os.Lstat(dir); !os.IsNotExist(err) {
							t.Fatal("preview created a directory")
						}
					} else {
						entries, err := os.ReadDir(dir)
						wantEntries := 1
						if target == "absent" {
							wantEntries = 0
						}
						if err != nil || len(entries) != wantEntries {
							t.Fatal("preview created or removed files")
						}
						if target == "file" {
							data, err := os.ReadFile(path)
							if err != nil || string(data) != "original contents" {
								t.Fatal("preview changed the target")
							}
						}
					}
					data, err := os.ReadFile(outside)
					if err != nil || string(data) != "outside contents" {
						t.Fatal("preview changed an outside file")
					}
				})
			}
		}
	}
}
