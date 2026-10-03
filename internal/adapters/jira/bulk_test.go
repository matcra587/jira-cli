package jira

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"

	wire "github.com/matcra587/jira-cli/internal/jira"
)

const nativeMove = `{"sendBulkNotification":false,"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1","KEY-2"],"inferFieldDefaults":false,"future":{"n":9007199254740993,"markup":"<>&","empty":[],"null":null}}},"future":false}`

func moveRequest(t *testing.T) wire.IssueBulkMovePayload {
	t.Helper()
	request, err := wire.ParseIssueMovePayload([]byte(nativeMove))
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func TestBulkMoveSubmissionRetainsNativeRequestAndTask(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != "POST" || r.URL.Path != "/rest/api/3/bulk/issues/move" || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("bulk move used a different route or method: %s %s", r.Method, r.URL)
		}
		received, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		const want = `{"future":false,"sendBulkNotification":false,"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1","KEY-2"],"inferFieldDefaults":false,"future":{"n":9007199254740993,"markup":"<>&","empty":[],"null":null}}}}`
		if string(received) != want {
			t.Errorf("native move request changed: %s", received)
		}
		w.WriteHeader(201)
		_, _ = fmt.Fprint(w, `{"taskId":"100","future":{"n":9007199254740993,"null":null}}`)
	}))
	t.Cleanup(server.Close)
	client, slept := newRetryTestClient(t, server.URL, retryPolicy())
	out, err := client.SubmitIssueMove(t.Context(), moveRequest(t))
	if err != nil || !out.Acknowledged || out.Task == nil || out.Task.TaskID.Value != "100" || out.Retry.Attempts != 1 || hits.Load() != 1 || len(*slept) != 0 || string(out.Task.Extra["future"]) != `{"n":9007199254740993,"null":null}` {
		t.Fatalf("move submission lost its acknowledgement/task or retried: %+v %v hits=%d", out, err, hits.Load())
	}
}

func TestBulkMoveAcknowledgementSurvivesMalformedTaskReceipt(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`null`, `{}`, `{"taskId":null}`, `{"taskId":" "}`, `{"taskId":100}`, `{"taskId":`,
		`{"taskId":"100"} {}`, `{"taskId":"100"} garbage`,
		`{"taskId":"100","taskId":"other"}`, `{"taskId":"100","\u0074askId":"other"}`,
		`{"taskId":"100","TaskId":"other"}`, `{"taskId":"../other"}`, `{"taskId":"%2Fother"}`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(201)
				_, _ = fmt.Fprint(w, body)
			}))
			t.Cleanup(server.Close)
			client, slept := newRetryTestClient(t, server.URL, retryPolicy())
			out, err := client.SubmitIssueMove(t.Context(), moveRequest(t))
			if !errors.Is(err, ErrBulkOperationResponseInvalid) || !out.Acknowledged || out.Task != nil || out.Retry.Attempts != 1 || hits.Load() != 1 || len(*slept) != 0 || !strings.Contains(err.Error(), "move was accepted") {
				t.Fatalf("accepted move was discarded, replayed or given a fabricated task: %+v %v hits=%d", out, err, hits.Load())
			}
		})
	}
}

func TestBulkMoveUnexpectedStatusNeverAcknowledgesOrReplays(t *testing.T) {
	t.Parallel()
	for _, status := range []int{200, 202, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, `{"taskId":"100"}`)
			}))
			t.Cleanup(server.Close)
			client, slept := newRetryTestClient(t, server.URL, retryPolicy())
			out, err := client.SubmitIssueMove(t.Context(), moveRequest(t))
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status || out.Acknowledged || out.Task != nil || hits.Load() != 1 || len(*slept) != 0 {
				t.Fatalf("unexpected move status acknowledged or replayed: %+v %v hits=%d", out, err, hits.Load())
			}
		})
	}
}

func TestBulkProgressSafeRetryReturnsOneObservation(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/rest/api/3/bulk/queue/100" || r.URL.RawQuery != "" {
			t.Errorf("progress did not use its read endpoint: %s %s", r.Method, r.URL)
		}
		if hits.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		_, _ = fmt.Fprint(w, `{"taskId":"100","status":"RUNNING","progressPercent":100,"created":9007199254740993,"started":"2026-01-02T12:30:00Z","updated":null,"future":false}`)
	}))
	t.Cleanup(server.Close)
	client, slept := newRetryTestClient(t, server.URL, retryPolicy())
	client.readOnly = true
	out, err := client.GetBulkOperationProgress(t.Context(), "100")
	if err != nil || out.Progress == nil || out.Progress.Terminal() || out.Progress.Status.Value != "RUNNING" || string(out.Progress.Created.Value) != "9007199254740993" || !out.Progress.Updated.Null || string(out.Progress.Extra["future"]) != "false" || out.Retry.Attempts != 3 || hits.Load() != 3 || len(*slept) != 2 {
		t.Fatalf("progress was mutated, coerced, implicitly polled or inferred complete: %+v %v hits=%d", out, err, hits.Load())
	}
}

func TestBulkProgressRejectsMalformedObservations(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`null`, `{}`, `{"taskId":"other","status":"RUNNING"}`, `{"taskId":"100","status":"unknown"}`,
		`{"taskId":"100","status":"RUNNING"} {}`, `{"taskId":"100","status":"RUNNING"} garbage`,
		`{"taskId":"100","taskId":"other","status":"RUNNING"}`,
		`{"taskId":"100","status":"RUNNING","Status":"COMPLETE"}`,
		`{"taskId":"100","status":"COMPLETE","totalIssueCount":2,"invalidOrInaccessibleIssueCount":0,"processedAccessibleIssues":[1]}`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
			t.Cleanup(server.Close)
			client, _ := newRetryTestClient(t, server.URL, retryPolicy())
			out, err := client.GetBulkOperationProgress(t.Context(), "100")
			if !errors.Is(err, ErrBulkOperationResponseInvalid) || out.Progress != nil {
				t.Fatalf("invalid progress became a usable observation: %+v %v", out, err)
			}
		})
	}
}

func TestBulkMoveLocalRefusalsAndCancellationMakeNoRequest(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(500) }))
	t.Cleanup(server.Close)
	client, _ := newRetryTestClient(t, server.URL, retryPolicy())
	if _, err := client.SubmitIssueMove(t.Context(), wire.IssueBulkMovePayload{}); err == nil {
		t.Fatal("missing target mapping accepted")
	}
	for _, id := range []string{"", " ", " 100", "../other", "%2Fother"} {
		if _, err := client.GetBulkOperationProgress(t.Context(), id); err == nil {
			t.Fatalf("invalid task identity accepted: %q", id)
		}
	}
	client.readOnly = true
	if out, err := client.SubmitIssueMove(t.Context(), moveRequest(t)); !errors.Is(err, ErrReadOnly) || out.Acknowledged {
		t.Fatalf("read-only submission permitted: %+v %v", out, err)
	}
	client.readOnly = false
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := client.SubmitIssueMove(ctx, moveRequest(t)); !errors.Is(err, context.Canceled) || out.Acknowledged {
		t.Fatalf("canceled submission accepted: %+v %v", out, err)
	}
	if _, err := client.GetBulkOperationProgress(ctx, "100"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled progress read lost its cause: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("local refusal contacted Jira %d times", hits.Load())
	}
}

func TestBulkMoveAcknowledgedReadFailureClosesBody(t *testing.T) {
	t.Parallel()
	cause := errors.New("fixture response interrupted")
	body := &apiTrackedBody{Reader: iotest.ErrReader(cause)}
	var hits atomic.Int32
	client := newTestClient(t, "https://example.invalid", &http.Client{Transport: redirectTransport(func(req *http.Request) (*http.Response, error) {
		hits.Add(1)
		return &http.Response{StatusCode: 201, Header: http.Header{}, Body: body, Request: req}, nil
	})})
	out, err := client.SubmitIssueMove(t.Context(), moveRequest(t))
	if !out.Acknowledged || out.Task != nil || !errors.Is(err, ErrBulkOperationResponseInvalid) || !errors.Is(err, cause) || !body.closed || hits.Load() != 1 {
		t.Fatalf("accepted interrupted receipt was lost, replayed or left open: %+v %v closed=%t hits=%d", out, err, body.closed, hits.Load())
	}
}
