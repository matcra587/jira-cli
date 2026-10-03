package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestAttachmentUploadInvalidFilesRefuseBeforeRequests(t *testing.T) {
	t.Parallel()
	for _, preview := range []bool{false, true} {
		for _, kind := range []string{"missing", "directory", "oversized", "aggregate", "good-then-missing"} {
			t.Run(strconv.FormatBool(preview)+"/"+kind, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				paths := []string{filepath.Join(dir, "input.bin")}
				if kind == "directory" {
					paths[0] = dir
				} else if kind != "missing" {
					size := int64(1)
					switch kind {
					case "oversized":
						size = (100 << 20) + 1
					case "aggregate":
						size = (50 << 20) + 1
					}
					file, err := os.Create(paths[0])
					if err != nil {
						t.Fatal(err)
					}
					if err := file.Truncate(size); err != nil {
						t.Fatal(err)
					}
					if err := file.Close(); err != nil {
						t.Fatal(err)
					}
					switch kind {
					case "aggregate":
						paths = append(paths, paths[0])
					case "good-then-missing":
						paths = append(paths, filepath.Join(dir, "missing.bin"))
					}
				}
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"errorMessages":["invalid local inputs must not reach Jira"]}`))
				}))
				t.Cleanup(server.Close)
				args := append([]string{"issue", "attachment", "add", "KEY-1"}, paths...)
				args = append(args, "--output=json", "--no-input")
				if preview {
					args = append(args, "--dry-run")
				}
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				if calls.Load() != 0 {
					t.Fatal("local file validation allowed an HTTP request")
				}
				var frame apiFrame
				if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || exit != 3 || len(frame.Errors) != 1 || frame.Errors[0].Code != "attachment_file_invalid" {
					t.Fatalf("invalid local input was not refused accurately: exit=%d stderr=%q", exit, stderr)
				}
				if string(frame.Data) != "null" {
					t.Fatal("local preflight refusal invented an upload receipt")
				}
			})
		}
	}
}

func TestAttachmentUploadPreviewPublishesMetadataWithoutContentsOrRequests(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "empty.txt")
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			// Metadata inspection must succeed even when contents cannot be
			// opened. The independent open verifies this stimulus is effective.
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
			if file, err := os.Open(path); err == nil {
				_ = file.Close()
				t.Skip("host permits reading mode-000 files; no unreadability claim")
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusBadRequest)
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "attachment", "add", "KEY-1", path, "--dry-run", "--output="+mode, "--no-input", "--color=never")
			if exit != 0 || calls.Load() != 0 {
				t.Fatalf("metadata preview opened contents or contacted Jira: exit=%d requests=%d stderr=%q", exit, calls.Load(), stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, path) || !strings.Contains(stdout, "0 B") || !strings.Contains(stdout, "inferred MIME text/plain") || !strings.Contains(stdout+stderr, "no file contents read") {
					t.Fatalf("human preview hid metadata or misstated its work: stdout=%q stderr=%q", stdout, stderr)
				}
				return
			}
			data := []byte(stdout)
			if mode == "json" {
				var frame apiFrame
				if json.Unmarshal(data, &frame) != nil || !frame.OK {
					t.Fatal("preview lost its success envelope")
				}
				data = frame.Data
			}
			var out envelope.IssueAttachmentAddOutput
			if json.Unmarshal(data, &out) != nil || !out.DryRun || len(out.Files) != 1 || out.Files[0] != path || len(out.FileDetails) != 1 || out.FileDetails[0].Path != path || out.FileDetails[0].Size != 0 || out.FileDetails[0].MIMEInferred != "text/plain" || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Data == nil || len(out.Results[0].Data.Attachments) != 0 {
				t.Fatal("metadata preview lost local facts or invented created resources")
			}
			var fields struct {
				FileDetails []map[string]json.RawMessage `json:"file_details"`
			}
			if json.Unmarshal(data, &fields) != nil || len(fields.FileDetails) != 1 || string(fields.FileDetails[0]["size"]) != "0" {
				t.Fatal("machine preview omitted its explicit zero-byte size")
			}
		})
	}
}

func TestAttachmentAcknowledgementDoesNotInventCompleteUpload(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{"malformed", "empty", "early", "growth", "missing-identity", "duplicate-identity"} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "authored.bin")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			size := int64(17)
			switch outcome {
			case "early":
				size = 8 << 20
			case "growth":
				size = 100 << 20
			}
			if err := file.Truncate(size); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if req.Method != http.MethodPost || req.URL.Path != "/rest/api/3/issue/KEY-1/attachments" {
					t.Error("upload changed its write route")
				}
				if outcome != "early" {
					if outcome == "growth" {
						if _, err := io.CopyN(io.Discard, req.Body, 128); err != nil {
							t.Error(err)
						}
						if err := os.Truncate(path, (100<<20)+1); err != nil {
							t.Error(err)
						}
					}
					if _, err := io.Copy(io.Discard, req.Body); err != nil {
						if outcome != "growth" {
							t.Error(err)
						}
					}
				}
				switch outcome {
				case "malformed":
					_, _ = w.Write([]byte("{"))
				case "empty":
					_, _ = w.Write([]byte("[]"))
				case "missing-identity":
					_, _ = w.Write([]byte(`[{"filename":"authored.bin","size":17}]`))
				case "duplicate-identity":
					_, _ = w.Write([]byte(`[{"id":"10042","filename":"authored.bin","size":17},{"id":"10042","filename":"authored.bin","size":17}]`))
				default:
					_, _ = w.Write([]byte(`[{"id":"10042","filename":"authored.bin","size":8388608}]`))
				}
				w.(http.Flusher).Flush()
			}))
			t.Cleanup(server.Close)
			paths := []string{path}
			if outcome == "duplicate-identity" {
				paths = append(paths, path)
			}
			args := append([]string{"issue", "attachment", "add", "KEY-1"}, paths...)
			args = append(args, "--output=json", "--no-input")
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			var frame apiFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || exit != 5 || len(frame.Errors) != 1 || frame.Errors[0].Code != "attachment_response_invalid" {
				t.Fatalf("incomplete acknowledged upload was misreported: exit=%d stderr=%q", exit, stderr)
			}
			if calls.Load() != 1 {
				t.Fatalf("upload was missing or replayed: requests=%d", calls.Load())
			}
			if !strings.Contains(frame.Errors[0].Hint, "before retrying") {
				t.Fatal("uncertain upload omitted inspection guidance")
			}
			var receipt envelope.IssueAttachmentAddOutput
			if json.Unmarshal(frame.Data, &receipt) != nil || len(receipt.Files) != len(paths) || len(receipt.FileDetails) != len(receipt.Files) || len(receipt.Results) != 1 || receipt.Results[0].Issue.Key != "KEY-1" || receipt.Results[0].OK || receipt.Results[0].Data != nil || receipt.Results[0].Err == nil || receipt.Results[0].Err.Code != "attachment_response_invalid" {
				t.Fatal("incomplete upload lost its failure receipt or fabricated created resources")
			}
		})
	}
}
