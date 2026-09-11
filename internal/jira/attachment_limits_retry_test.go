package jira

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAttachmentAddRejectsOversizedSourceBeforeHTTP(t *testing.T) {
	var hits atomic.Int32
	client := newHTTPHandlerClient(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))

	service := NewAttachmentService(client)
	_, _, err := service.Add(context.Background(), "PROJ-1", []FileSource{{
		Name:   "large.bin",
		Size:   maxAttachmentUploadBytes + 1,
		Reader: strings.NewReader(""),
	}})
	if err == nil {
		t.Fatal("Add() error = nil, want upload size refusal")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Add() error = %v, want size context", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("server hits = %d, want 0", got)
	}
}

func TestAttachmentDownloadErrorCarriesRetryMetadata(t *testing.T) {
	client := newHTTPHandlerClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.Header().Set("X-RateLimit-Remaining", "2")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"errorMessages":["maintenance"]}`))
	}))

	service := NewAttachmentService(client)
	_, _, err := service.Download(context.Background(), "10042")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Download() error = %T %[1]v, want *APIError", err)
	}
	if apiErr.RetryAfterSeconds != 7 {
		t.Fatalf("RetryAfterSeconds = %d, want 7", apiErr.RetryAfterSeconds)
	}
	if apiErr.RateLimitRemaining != 2 {
		t.Fatalf("RateLimitRemaining = %d, want 2", apiErr.RateLimitRemaining)
	}
}

func TestAttachmentDownloadWrapsTransportErrorWithContext(t *testing.T) {
	sentinel := errors.New("dial failed")
	client := NewClient(
		WithBaseURL("https://jira.example.com/"),
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, sentinel
		})}),
	)
	service := NewAttachmentService(client)

	_, _, err := service.Download(context.Background(), "10042")
	if !errors.Is(err, sentinel) {
		t.Fatalf("Download() error = %v, want wrapping sentinel", err)
	}
	if !strings.Contains(err.Error(), "jira request GET /rest/api/3/attachment/content/10042") {
		t.Fatalf("Download() error lacks operation context: %v", err)
	}
}

func TestAttachmentAddDebugDoesNotLogMultipartFileBytes(t *testing.T) {
	const secret = "SECRET_ATTACHMENT_BYTES"

	ctx, debugLogs := newDebugLogContext(t)

	client := NewClient(
		WithBaseURL("https://jira.example.com/"),
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			_, _ = io.Copy(io.Discard, req.Body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`[]`)),
				Request:    req,
			}, nil
		})}),
		WithDebug(true),
	)
	service := NewAttachmentService(client)
	if _, _, err := service.Add(ctx, "PROJ-1", []FileSource{{
		Name:   "secret.txt",
		Size:   int64(len(secret)),
		Reader: strings.NewReader(secret),
	}}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	logText := debugLogs.String()
	if strings.Contains(logText, secret) {
		t.Fatalf("debug log leaked multipart body:\n%s", logText)
	}
	if !strings.Contains(logText, "redacted non-json body") {
		t.Fatalf("debug log missing multipart redaction marker:\n%s", logText)
	}
}
