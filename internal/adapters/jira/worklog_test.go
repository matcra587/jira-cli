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

	wire "github.com/matcra587/jira-cli/internal/jira"
)

func TestWorklogReadPreservesPageAndRetries(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	queries := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries <- r.Method + " " + r.URL.RequestURI()
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `{"startAt":2,"maxResults":2,"total":4,"worklogs":[{"id":"100","timeSpentSeconds":0,"comment":null,"author":{"accountId":"u","displayName":null},"future":9007199254740993}],"future":false}`)
	}))
	t.Cleanup(server.Close)
	client, slept := newRetryTestClient(t, server.URL, retryPolicy())
	client.readOnly = true
	read, err := client.GetWorklogs(t.Context(), "KEY-1", 2, 2)
	if err != nil || read.Page == nil || len(read.Page.Worklogs.Value) != 1 || read.Retry.Attempts != 2 || len(*slept) != 1 {
		t.Fatalf("read/retry facts: %+v %v", read, err)
	}
	for range 2 {
		if got := <-queries; got != "GET /rest/api/3/issue/KEY-1/worklog?maxResults=2&startAt=2" {
			t.Fatalf("request changed: %s", got)
		}
	}
	worklog := read.Page.Worklogs.Value[0]
	if worklog.ID.Value != "100" || !worklog.Comment.Null || !worklog.TimeSpentSeconds.Set || worklog.TimeSpentSeconds.Value != 0 || !worklog.Author.Value.DisplayName.Null || string(worklog.Extra["future"]) != "9007199254740993" || string(read.Page.Extra["future"]) != "false" {
		t.Fatalf("response data lost: %+v", read.Page)
	}
}

func TestWorklogReadRejectsMalformedPages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body string }{
		{"null", `null`},
		{"missing facts", `{}`},
		{"missing array", `{"startAt":0,"maxResults":2,"total":0}`},
		{"null array", `{"startAt":0,"maxResults":2,"total":0,"worklogs":null}`},
		{"null start", `{"startAt":null,"maxResults":2,"total":0,"worklogs":[]}`},
		{"wrong start", `{"startAt":1,"maxResults":2,"total":1,"worklogs":[]}`},
		{"null size", `{"startAt":0,"maxResults":null,"total":0,"worklogs":[]}`},
		{"missing size", `{"startAt":0,"total":0,"worklogs":[]}`},
		{"negative size", `{"startAt":0,"maxResults":-1,"total":0,"worklogs":[]}`},
		{"null total", `{"startAt":0,"maxResults":2,"total":null,"worklogs":[]}`},
		{"negative total", `{"startAt":0,"maxResults":2,"total":-1,"worklogs":[]}`},
		{"missing total", `{"startAt":0,"maxResults":2,"worklogs":[]}`},
		{"null row", `{"startAt":0,"maxResults":2,"total":1,"worklogs":[null]}`},
		{"missing identity", `{"startAt":0,"maxResults":2,"total":1,"worklogs":[{}]}`},
		{"null identity", `{"startAt":0,"maxResults":2,"total":1,"worklogs":[{"id":null}]}`},
		{"blank identity", `{"startAt":0,"maxResults":2,"total":1,"worklogs":[{"id":" "}]}`},
		{"duplicate", `{"startAt":0,"maxResults":2,"total":2,"worklogs":[{"id":"1"},{"id":"1"}]}`},
		{"oversized", `{"startAt":0,"maxResults":3,"total":3,"worklogs":[{"id":"1"},{"id":"2"},{"id":"3"}]}`},
		{"declared size exceeded", `{"startAt":0,"maxResults":0,"total":1,"worklogs":[{"id":"1"}]}`},
		{"total exceeded", `{"startAt":0,"maxResults":2,"total":0,"worklogs":[{"id":"1"}]}`},
		{"trailing document", `{"startAt":0,"maxResults":2,"total":0,"worklogs":[]} {}`},
		{"trailing garbage", `{"startAt":0,"maxResults":2,"total":0,"worklogs":[]} garbage`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, tc.body) }))
			t.Cleanup(server.Close)
			client, _ := newRetryTestClient(t, server.URL, retryPolicy())
			read, err := client.GetWorklogs(t.Context(), "KEY-1", 0, 2)
			if !errors.Is(err, ErrWorklogResponseInvalid) || read.Page != nil {
				t.Fatalf("malformed page accepted: %+v %v", read, err)
			}
		})
	}
}

func TestWorklogReadRetainsEmptyAndSparsePages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		body  string
		start int
	}{
		{`{"startAt":0,"maxResults":2,"total":0,"worklogs":[]}`, 0},
		{`{"startAt":0,"maxResults":2,"total":3,"worklogs":[]}`, 0},
		{`{"startAt":4,"maxResults":2,"total":3,"worklogs":[]}`, 4},
		{`{"startAt":0,"maxResults":2,"total":3,"worklogs":[{"id":"1"}]}`, 0},
	} {
		t.Run(tc.body, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, tc.body) }))
			t.Cleanup(server.Close)
			client, _ := newRetryTestClient(t, server.URL, retryPolicy())
			read, err := client.GetWorklogs(t.Context(), "KEY-1", tc.start, 2)
			if err != nil || read.Page == nil || !read.Page.Worklogs.Set || read.Page.Worklogs.Null {
				t.Fatalf("valid page lost: %+v %v", read, err)
			}
		})
	}
}

func TestWorklogAddSendsExactBodyAndNotify(t *testing.T) {
	t.Parallel()
	const comment = "{\"type\":\"doc\",\"version\":1,\"content\":[],\"future\":\"<>&\u2028\u2029\"}"
	for _, notify := range []bool{true, false} {
		t.Run(fmt.Sprint(notify), func(t *testing.T) {
			t.Parallel()
			requests := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				requests <- fmt.Sprintf("%s %s %s %s %v", r.Method, r.URL.RequestURI(), r.Header.Get("Content-Type"), body, err)
				w.WriteHeader(http.StatusCreated)
				_, _ = fmt.Fprint(w, `{"id":"100","started":"2026-01-02T12:30:00.000+0000","timeSpentSeconds":60,"author":{"accountId":"u"},"future":false}`)
			}))
			t.Cleanup(server.Close)
			client, _ := newRetryTestClient(t, server.URL, retryPolicy())
			write, err := client.AddWorklog(t.Context(), "KEY-1", wire.AddWorklogRequest{TimeSpentSeconds: 60, Started: "2026-01-02T12:30:00.000+0000", Comment: []byte(comment)}, notify)
			if err != nil || !write.Acknowledged || write.Worklog == nil || write.Worklog.ID.Value != "100" || write.Retry.Attempts != 1 || string(write.Worklog.Extra["future"]) != "false" {
				t.Fatalf("receipt changed: %+v %v", write, err)
			}
			query := ""
			if !notify {
				query = "?notifyUsers=false"
			}
			want := "POST /rest/api/3/issue/KEY-1/worklog" + query + " application/json {\"timeSpentSeconds\":60,\"started\":\"2026-01-02T12:30:00.000+0000\",\"comment\":" + comment + "} <nil>"
			if got := <-requests; got != want {
				t.Fatalf("request changed: %s; want %s", got, want)
			}
		})
	}
}

func TestWorklogAddRetainsAcknowledgedMalformedReceipt(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`null`, `{}`, `{"id":null}`, `{"id":" "}`, `{"id":100}`, `{"id":"100"} {}`, `{"id":"100"} garbage`, `{"id":`} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusCreated)
				_, _ = fmt.Fprint(w, body)
			}))
			t.Cleanup(server.Close)
			client, slept := newRetryTestClient(t, server.URL, retryPolicy())
			write, err := client.AddWorklog(t.Context(), "KEY-1", wire.AddWorklogRequest{TimeSpentSeconds: 60}, true)
			if !errors.Is(err, ErrWorklogResponseInvalid) || !write.Acknowledged || write.Worklog != nil || write.Retry.Attempts != 1 || hits.Load() != 1 || len(*slept) != 0 || !strings.Contains(err.Error(), "worklog was created") {
				t.Fatalf("acknowledged result lost/replayed: %+v %v hits=%d", write, err, hits.Load())
			}
		})
	}
}

func TestWorklogWritesNeverReplay(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusOK} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, `{"id":"100"}`)
			}))
			t.Cleanup(server.Close)
			client, slept := newRetryTestClient(t, server.URL, retryPolicy())
			write, err := client.AddWorklog(t.Context(), "KEY-1", wire.AddWorklogRequest{TimeSpentSeconds: 60}, true)
			if err == nil || write.Acknowledged || hits.Load() != 1 || len(*slept) != 0 {
				t.Fatalf("unexpected status accepted/replayed: %+v %v hits=%d", write, err, hits.Load())
			}
		})
	}
}

func TestWorklogLocalRefusalsAndCancellationMakeNoRequest(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"id":"100"}`)
	}))
	t.Cleanup(server.Close)
	client, _ := newRetryTestClient(t, server.URL, retryPolicy())
	for _, seconds := range []int{0, -1} {
		if _, err := client.AddWorklog(t.Context(), "KEY-1", wire.AddWorklogRequest{TimeSpentSeconds: seconds}, true); err == nil {
			t.Fatal("invalid duration accepted")
		}
	}
	for _, pair := range [][2]int{{-1, 1}, {0, 0}, {0, -1}} {
		if _, err := client.GetWorklogs(t.Context(), "KEY-1", pair[0], pair[1]); err == nil {
			t.Fatal("invalid page accepted")
		}
	}
	client.readOnly = true
	if write, err := client.AddWorklog(t.Context(), "KEY-1", wire.AddWorklogRequest{TimeSpentSeconds: 60}, true); !errors.Is(err, ErrReadOnly) || write.Acknowledged {
		t.Fatalf("read-only write accepted: %+v %v", write, err)
	}
	client.readOnly = false
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.AddWorklog(ctx, "KEY-1", wire.AddWorklogRequest{TimeSpentSeconds: 60}, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("write cancellation lost: %v", err)
	}
	if _, err := client.GetWorklogs(ctx, "KEY-1", 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("read cancellation lost: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("local refusal made %d requests", hits.Load())
	}
}
