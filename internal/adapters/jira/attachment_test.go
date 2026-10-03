package jira

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/goleak"

	"github.com/matcra587/jira-cli/internal/attachmentfiles"
	"github.com/matcra587/jira-cli/internal/core"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

func TestAttachmentIssuePreservesOriginalReadFailures(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"", `{"id":"10001","key":"KEY-1","fields":{"attachment":[]}}`} {
		t.Run(map[bool]string{false: "initial", true: "trailing"}[prefix != ""], func(t *testing.T) {
			t.Parallel()
			cause := errors.New("fixture attachment observation stream failed")
			issue, err := decodeAttachmentIssue(io.MultiReader(strings.NewReader(prefix), commentFailureReader{cause: cause}))
			if issue != nil || !errors.Is(err, cause) || !errors.Is(err, core.ErrAttachmentResponseInvalid) {
				t.Fatal("attachment observation hid its original reader failure or published an incomplete document")
			}
		})
	}
}

func TestAttachmentDeleteAcknowledgementAndRetryFacts(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusNoContent, http.StatusAccepted, http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if req.Method != http.MethodDelete || req.URL.Path != "/rest/api/3/attachment/10042" {
					t.Error("attachment deletion changed its explicit native resource")
				}
				w.WriteHeader(status)
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.invalid", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			write, err := client.DeleteAttachment(t.Context(), "10042")
			if calls.Load() != 1 {
				t.Fatal("attachment deletion replayed its unsafe request")
			}
			if status == http.StatusNoContent {
				if err != nil || write.StatusCode != http.StatusNoContent || write.Retry.Attempts != 1 {
					t.Fatal("attachment deletion lost its explicit native acknowledgement or transport facts")
				}
				return
			}
			if write.StatusCode != 0 || err == nil {
				t.Fatal("failed attachment deletion invented an acknowledgement")
			}
			if status == http.StatusAccepted {
				if !errors.Is(err, core.ErrAttachmentResponseInvalid) {
					t.Fatal("unexpected successful status lost its protocol failure")
				}
			} else {
				failure, ok := errors.AsType[*APIError](err)
				if !ok || failure.StatusCode != status || failure.Retry.Attempts != 1 {
					t.Fatal("failed deletion lost its native status or original retry facts")
				}
			}
		})
	}
}

func TestGetAttachmentUsesNumericMetadataShapeAndPreservesNativeFields(t *testing.T) {
	t.Parallel()
	const body = `{"id":10042,"filename":"report.bin","size":0,"mimeType":"application/octet-stream","author":{"accountId":"fixture-account","emailAddress":null},"properties":{"future":{"big":9007199254740993}},"future":{"values":[null,[],false,0]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/attachment/10042" {
			t.Error("metadata read changed its route")
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.invalid", APIToken: "fixture", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	read, err := client.GetAttachment(t.Context(), "10042")
	if err != nil || read.Attachment == nil {
		t.Fatalf("documented numeric attachment metadata was refused: %v", err)
	}
	if read.Attachment.Filename.Value != "report.bin" || !read.Attachment.Size.Set || read.Attachment.Size.Null || read.Attachment.Size.Value != 0 {
		t.Fatal("metadata lost filename or explicit zero size")
	}
	encoded, err := wire.MarshalNoEscape(read.Attachment)
	if err != nil || !bytes.Contains(encoded, []byte(`"id":10042`)) || !bytes.Contains(encoded, []byte(`"emailAddress":null`)) || !bytes.Contains(encoded, []byte(`"big":9007199254740993`)) || !bytes.Contains(encoded, []byte(`"values":[null,[],false,0]`)) {
		t.Fatal("metadata decoding lost native numeric, nullable or unknown values")
	}
}

func TestAttachmentInterruptedUploadReleasesMultipartWriter(t *testing.T) {
	for _, outcome := range []string{"early-success", "early-refusal", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			ignore := goleak.IgnoreCurrent()
			path := filepath.Join(t.TempDir(), "authored.bin")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(8 << 20); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				switch outcome {
				case "cancel":
					if _, err := io.CopyN(io.Discard, req.Body, 128); err != nil {
						t.Error(err)
					}
					cancel()
				case "early-refusal":
					w.WriteHeader(http.StatusForbidden)
				default:
					_, _ = w.Write([]byte(`[{"id":"10042"}]`))
				}
				w.(http.Flusher).Flush()
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.invalid", APIToken: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.AddAttachment(ctx, "KEY-1", []string{path})
			if err == nil || calls.Load() != 1 {
				t.Fatalf("interrupted upload reported success or replayed: err=%v calls=%d", err, calls.Load())
			}
			if outcome == "cancel" && !errors.Is(err, context.Canceled) || outcome == "early-success" && !errors.Is(err, core.ErrAttachmentResponseInvalid) {
				t.Fatalf("interrupted upload lost its cause: %v", err)
			}
			client.http.CloseIdleConnections()
			server.CloseClientConnections()
			server.Close()
			if err := goleak.Find(ignore); err != nil {
				t.Fatalf("interrupted upload left a producer alive: %v", err)
			}
		})
	}
}

func TestAttachmentRuntimeContentCeilingDoesNotSendExtraByte(t *testing.T) {
	for _, grows := range []bool{false, true} {
		t.Run(fmt.Sprintf("grows=%t", grows), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bounded.bin")
			source, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := source.Truncate(attachmentfiles.MaxUploadBytes); err != nil {
				t.Fatal(err)
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			empty := filepath.Join(t.TempDir(), "empty.bin")
			if err := os.WriteFile(empty, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			files, err := attachmentfiles.Open(t.Context(), []string{path, empty})
			if err != nil {
				t.Fatal(err)
			}
			defer attachmentfiles.Close(files)
			if grows {
				if err := os.Truncate(path, attachmentfiles.MaxUploadBytes+1); err != nil {
					t.Fatal(err)
				}
			}
			pr, pw := io.Pipe()
			defer pr.Close() //nolint:errcheck // read-only test stream
			form := multipart.NewWriter(pw)
			done := make(chan error, 1)
			go func() {
				err := writeAttachmentParts(form, files)
				_ = pw.CloseWithError(err)
				done <- err
			}()
			joined := false
			defer func() {
				_ = pr.Close()
				if !joined {
					<-done
				}
			}()
			reader := multipart.NewReader(pr, form.Boundary())
			part, err := reader.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			copied, copyErr := io.Copy(io.Discard, part)
			if copied != attachmentfiles.MaxUploadBytes || part.FormName() != "file" || part.FileName() != "bounded.bin" {
				t.Fatalf("stream crossed its exact content ceiling or changed metadata: bytes=%d", copied)
			}
			if grows {
				_ = pr.Close()
				if !errors.Is(copyErr, attachmentfiles.ErrInvalid) {
					t.Fatalf("stream hid growth beyond its ceiling: %v", copyErr)
				}
			} else {
				if copyErr != nil {
					t.Fatal(copyErr)
				}
				part, err = reader.NextPart()
				if err != nil || part.FileName() != "empty.bin" {
					t.Fatalf("exact ceiling refused subsequent empty input: %v", err)
				}
				if n, err := io.Copy(io.Discard, part); err != nil || n != 0 {
					t.Fatalf("empty input changed: bytes=%d err=%v", n, err)
				}
				if _, err := reader.NextPart(); !errors.Is(err, io.EOF) {
					t.Fatalf("multipart stream did not complete: %v", err)
				}
			}
			streamErr := <-done
			joined = true
			if grows != errors.Is(streamErr, attachmentfiles.ErrInvalid) || !grows && streamErr != nil {
				t.Fatalf("producer reported wrong completion: %v", streamErr)
			}
		})
	}
}

func TestAttachmentGrowthAfterRequestStartsRequiresInspection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "growing.bin")
	source, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Truncate(attachmentfiles.MaxUploadBytes); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if _, err := io.CopyN(io.Discard, req.Body, 128); err != nil {
			t.Error(err)
		}
		if err := os.Truncate(path, attachmentfiles.MaxUploadBytes+1); err != nil {
			t.Error(err)
		}
		_, _ = io.Copy(io.Discard, req.Body)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{BaseURL: server.URL, Email: "fixture@example.invalid", APIToken: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.AddAttachment(t.Context(), "KEY-1", []string{path})
	if calls.Load() != 1 || !errors.Is(err, core.ErrAttachmentResponseInvalid) || !errors.Is(err, attachmentfiles.ErrInvalid) {
		t.Fatalf("runtime growth was treated as a safe local refusal instead of an uncertain write: calls=%d err=%v", calls.Load(), err)
	}
}

func TestAttachmentAcknowledgedGrowthClosesResponseBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "growing.bin")
	source, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Truncate(attachmentfiles.MaxUploadBytes); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	body := &apiTrackedBody{Reader: bytes.NewReader([]byte(`[{"id":"10042"}]`))}
	transport := redirectTransport(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close() //nolint:errcheck // read-only test request stream
		if err := os.Truncate(path, attachmentfiles.MaxUploadBytes+1); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, req.Body); !errors.Is(err, attachmentfiles.ErrInvalid) {
			t.Fatalf("growth stimulus did not reach the runtime guard: %v", err)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: req}, nil
	})
	client, err := New(Config{BaseURL: "https://example.invalid", Email: "fixture@example.invalid", APIToken: "fixture", HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.AddAttachment(t.Context(), "KEY-1", []string{path})
	if !errors.Is(err, core.ErrAttachmentResponseInvalid) || !body.closed {
		t.Fatalf("acknowledged growth lost its uncertainty or response closure: closed=%t err=%v", body.closed, err)
	}
}

func TestAttachmentReadOnlyRefusalDoesNotLeakMultipartWriter(t *testing.T) {
	ignore := goleak.IgnoreCurrent()
	path := filepath.Join(t.TempDir(), "authored.txt")
	if err := os.WriteFile(path, []byte("authored bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{BaseURL: "https://example.invalid", Email: "fixture@example.invalid", APIToken: "fixture", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AddAttachment(t.Context(), "KEY-1", []string{path}); err == nil {
		t.Fatal("read-only attachment upload was accepted")
	}
	if err := goleak.Find(ignore); err != nil {
		t.Fatalf("refused upload left a multipart writer alive: %v", err)
	}
}
