package jira

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestNew(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{name: "valid", cfg: Config{BaseURL: "https://example.atlassian.net", Email: "u@example.com", APIToken: "tok"}},
		{name: "missing token", cfg: Config{BaseURL: "https://example.atlassian.net", Email: "u@example.com"}, wantErr: true},
		{name: "missing email", cfg: Config{BaseURL: "https://example.atlassian.net", APIToken: "tok"}, wantErr: true},
		{name: "relative base url", cfg: Config{BaseURL: "example.atlassian.net", Email: "u@example.com", APIToken: "tok"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("New error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestClient_newRequest(t *testing.T) {
	t.Parallel()

	t.Run("body bytes survive without html escaping", func(t *testing.T) {
		t.Parallel()
		c, err := New(Config{BaseURL: "https://example.atlassian.net", Email: "u@example.com", APIToken: "tok"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		// Kills re-escaping raw strings during request encoding, including
		// the line separators Go 1.27 escapes with SetEscapeHTML(false).
		const raw = "{\"text\":\"a<b>&c\u2028next\u2029end \\\\u2028\"}"
		body := json.RawMessage(raw)
		req, err := c.newRequest(t.Context(), http.MethodPost, []string{"rest", "api", "3", "issue"}, nil, body)
		if err != nil {
			t.Fatalf("newRequest: %v", err)
		}
		got, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(got) != raw {
			t.Errorf("body = %s, want the exact unescaped document", got)
		}
		if req.GetBody == nil {
			t.Error("GetBody is nil; a bytes-backed body must stay replayable")
		}
		if ct := req.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content type = %q, want application/json", ct)
		}
	})
}

// TestAPIError_Error pins the exact string callers see through the plain
// error interface (as opposed to the typed StatusCode/Messages/FieldErrors
// fields errors.As unwraps): the status text plus, when Jira sent one, only
// the FIRST error message — a multi-message response silently drops the
// rest of the string form. An unrecognized status code is a real Jira
// possibility too (a proxy or a future API version), and it renders with a
// trailing space, since net/http.StatusText returns "" for codes it does
// not know.
func TestAPIError_Error(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  *APIError
		want string
	}{
		{
			name: "no message body",
			err:  &APIError{StatusCode: http.StatusNotFound},
			want: "jira: 404 Not Found",
		},
		{
			name: "single message appended",
			err:  &APIError{StatusCode: http.StatusBadRequest, Messages: []string{"Field 'summary' is required."}},
			want: "jira: 400 Bad Request: Field 'summary' is required.",
		},
		{
			name: "only the first of multiple messages is used",
			err:  &APIError{StatusCode: http.StatusBadRequest, Messages: []string{"first problem", "second problem"}},
			want: "jira: 400 Bad Request: first problem",
		},
		{
			name: "unrecognized status code renders with a trailing space",
			err:  &APIError{StatusCode: 599},
			want: "jira: 599 ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClient_GetIssue(t *testing.T) {
	t.Parallel()

	t.Run("sends auth and fields, decodes the issue", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user, pass, ok := r.BasicAuth(); !ok || user != "u@example.com" || pass != "tok" {
				t.Errorf("basic auth = %q/%q, ok=%v; want the configured pair", user, pass, ok)
			}
			if got := r.URL.Query().Get("fields"); got != "summary,status" {
				t.Errorf("fields query = %q, want summary,status", got)
			}
			if got := r.URL.Path; got != "/rest/api/3/issue/ROCK-1" {
				t.Errorf("path = %q, want /rest/api/3/issue/ROCK-1", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"key":"ROCK-1","fields":{"summary":"hello"}}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		got, err := c.GetIssue(t.Context(), "ROCK-1", []string{"summary", "status"})
		if err != nil {
			t.Fatalf("GetIssue: %v", err)
		}
		if k := got.Issue.Key.Value; k != "ROCK-1" {
			t.Errorf("key = %q, want ROCK-1", k)
		}
		if got.Retry != (core.RetryInfo{Attempts: 1}) {
			t.Errorf("retry info = %+v, want a single attempt with no wait", got.Retry)
		}
	})

	t.Run("not found becomes a typed api error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errorMessages":["Issue does not exist or you do not have permission to see it."],"errors":{}}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		_, err = c.GetIssue(t.Context(), "ROCK-404", nil)
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error = %v, want *APIError", err)
		}
		if apiErr.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want 404", apiErr.StatusCode)
		}
		if len(apiErr.Messages) != 1 {
			t.Errorf("messages = %v, want the server's single message", apiErr.Messages)
		}
	})
}

func TestClient_GetComments(t *testing.T) {
	t.Parallel()

	// The default's own resolution lives in the adapter (GetComments), so
	// this pair of subtests is the only place that mapping is checked:
	// the one above proves "no preference" becomes newest-first, and this
	// one proves an explicit choice is not quietly overwritten by it.
	t.Run("sends an explicit order rather than the default", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query().Get("orderBy"); got != "+created" {
				t.Errorf("orderBy query = %q, want +created (oldest first)", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"startAt":0,"maxResults":50,"total":0,"comments":[]}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if _, err := c.GetComments(t.Context(), "ROCK-1", 0, 50, core.CommentOrderOldestFirst); err != nil {
			t.Fatalf("GetComments: %v", err)
		}
	})

	t.Run("sends pagination and orderBy, decodes the page", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user, pass, ok := r.BasicAuth(); !ok || user != "u@example.com" || pass != "tok" {
				t.Errorf("basic auth = %q/%q, ok=%v; want the configured pair", user, pass, ok)
			}
			if got := r.URL.Path; got != "/rest/api/3/issue/ROCK-1/comment" {
				t.Errorf("path = %q, want /rest/api/3/issue/ROCK-1/comment", got)
			}
			q := r.URL.Query()
			if got := q.Get("startAt"); got != "5" {
				t.Errorf("startAt query = %q, want 5", got)
			}
			if got := q.Get("maxResults"); got != "10" {
				t.Errorf("maxResults query = %q, want 10", got)
			}
			if got := q.Get("orderBy"); got != "-created" {
				t.Errorf("orderBy query = %q, want -created (newest first)", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"startAt":5,"maxResults":10,"total":12,"comments":[{"id":"100","body":{"type":"doc"}}]}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		got, err := c.GetComments(t.Context(), "ROCK-1", 5, 10, core.CommentOrderDefault)
		if err != nil {
			t.Fatalf("GetComments: %v", err)
		}
		if got.Page == nil {
			t.Fatal("page is nil")
		}
		if got.Page.Total != 12 {
			t.Errorf("total = %d, want 12", got.Page.Total)
		}
		if len(got.Page.Comments) != 1 || got.Page.Comments[0].ID.Value != "100" {
			t.Errorf("comments = %+v, want one comment with id 100", got.Page.Comments)
		}
		if got.Retry != (core.RetryInfo{Attempts: 1}) {
			t.Errorf("retry info = %+v, want a single attempt with no wait", got.Retry)
		}
	})

	t.Run("not found becomes a typed api error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errorMessages":["Issue does not exist or you do not have permission to see it."],"errors":{}}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		_, err = c.GetComments(t.Context(), "ROCK-404", 0, 50, core.CommentOrderDefault)
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error = %v, want *APIError", err)
		}
		if apiErr.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want 404", apiErr.StatusCode)
		}
	})
}

func TestClient_GetWatchers(t *testing.T) {
	t.Parallel()

	t.Run("hits the watcher endpoint with no query, decodes the set", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user, pass, ok := r.BasicAuth(); !ok || user != "u@example.com" || pass != "tok" {
				t.Errorf("basic auth = %q/%q, ok=%v; want the configured pair", user, pass, ok)
			}
			if got := r.URL.Path; got != "/rest/api/3/issue/ROCK-1/watchers" {
				t.Errorf("path = %q, want /rest/api/3/issue/ROCK-1/watchers", got)
			}
			// The endpoint takes no parameters at all (see the seam's own
			// doc comment), so a query string here would mean this method
			// had grown a paging pair the resource cannot honor.
			if got := r.URL.RawQuery; got != "" {
				t.Errorf("query = %q, want none — this endpoint takes no parameters", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"isWatching":true,"watchCount":2,"watchers":[` +
				`{"accountId":"acct-1","displayName":"Ada Watcher","accountType":"atlassian","active":true},` +
				`{"accountId":"acct-2","displayName":"Departed Colleague","accountType":"atlassian","active":false}]}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		got, err := c.GetWatchers(t.Context(), "ROCK-1")
		if err != nil {
			t.Fatalf("GetWatchers: %v", err)
		}
		if got.Watchers == nil {
			t.Fatal("watchers payload is nil")
		}
		if got.Watchers.WatchCount.Value != 2 {
			t.Errorf("watchCount = %d, want 2", got.Watchers.WatchCount.Value)
		}
		if !got.Watchers.IsWatching.Value {
			t.Error("isWatching = false, want true")
		}
		list := got.Watchers.Watchers.Value
		if len(list) != 2 || list[0].AccountID.Value != "acct-1" {
			t.Fatalf("watchers = %+v, want two entries led by acct-1", list)
		}
		if !list[0].Active.Value || list[1].Active.Value {
			t.Errorf("active flags = %v/%v, want true/false", list[0].Active.Value, list[1].Active.Value)
		}
		if got.Retry != (core.RetryInfo{Attempts: 1}) {
			t.Errorf("retry info = %+v, want a single attempt with no wait", got.Retry)
		}
	})

	t.Run("a permission-restricted response is not an error", func(t *testing.T) {
		t.Parallel()
		// Jira answers a caller without "View voters and watchers" with a
		// 200 carrying the count and no array. That is a real answer about
		// a real issue, and treating it as a failure would lose the count
		// — the one thing that distinguishes it from an unwatched issue.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"isWatching":false,"watchCount":7}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		got, err := c.GetWatchers(t.Context(), "ROCK-1")
		if err != nil {
			t.Fatalf("GetWatchers: %v", err)
		}
		if got.Watchers == nil {
			t.Fatal("watchers payload is nil")
		}
		if got.Watchers.WatchCount.Value != 7 {
			t.Errorf("watchCount = %d, want 7", got.Watchers.WatchCount.Value)
		}
		if len(got.Watchers.Watchers.Value) != 0 {
			t.Errorf("watchers = %+v, want none visible", got.Watchers.Watchers.Value)
		}
	})

	t.Run("not found becomes a typed api error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errorMessages":["Issue does not exist or you do not have permission to see it."],"errors":{}}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		_, err = c.GetWatchers(t.Context(), "ROCK-404")
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error = %v, want *APIError", err)
		}
		if apiErr.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want 404", apiErr.StatusCode)
		}
	})
}

func TestClient_SearchIssues(t *testing.T) {
	t.Parallel()

	t.Run("sends jql and page size, decodes issues and token", func(t *testing.T) {
		t.Parallel()
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user, pass, ok := r.BasicAuth(); !ok || user != "u@example.com" || pass != "tok" {
				t.Errorf("basic auth = %q/%q, ok=%v; want the configured pair", user, pass, ok)
			}
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			if got := r.URL.Path; got != "/rest/api/3/search/jql" {
				t.Errorf("path = %q, want /rest/api/3/search/jql", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"isLast":false,"nextPageToken":"abc123","issues":[{"key":"ABC-1"}],"warnings":["truncated"]}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		got, err := c.SearchIssues(t.Context(), "project = ABC", []string{"summary"}, "", 25)
		if err != nil {
			t.Fatalf("SearchIssues: %v", err)
		}
		if gotBody["jql"] != "project = ABC" {
			t.Errorf("body jql = %v, want %q", gotBody["jql"], "project = ABC")
		}
		if maxResults, _ := gotBody["maxResults"].(float64); int(maxResults) != 25 {
			t.Errorf("body maxResults = %v, want 25", gotBody["maxResults"])
		}
		if got.Results == nil {
			t.Fatal("results is nil")
		}
		if len(got.Results.Issues.Value) != 1 || got.Results.Issues.Value[0].Key.Value != "ABC-1" {
			t.Errorf("issues = %+v, want one issue keyed ABC-1", got.Results.Issues.Value)
		}
		if !got.Results.NextPageToken.Set || got.Results.NextPageToken.Value != "abc123" {
			t.Errorf("next page token = %+v, want abc123", got.Results.NextPageToken)
		}
		if got.Retry != (core.RetryInfo{Attempts: 1}) {
			t.Errorf("retry info = %+v, want a single attempt with no wait", got.Retry)
		}
	})

	t.Run("no next token honestly reports the end of results", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"isLast":true,"issues":[]}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		got, err := c.SearchIssues(t.Context(), "project = ABC", nil, "", 0)
		if err != nil {
			t.Fatalf("SearchIssues: %v", err)
		}
		if !got.Results.IsLast.Value {
			t.Error("IsLast = false, want true")
		}
		if got.Results.NextPageToken.Set {
			t.Errorf("next page token = %+v, want absent (no next page)", got.Results.NextPageToken)
		}
	})

	t.Run("bad request becomes a typed api error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errorMessages":["The JQL you have entered is not valid."],"errors":{}}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		_, err = c.SearchIssues(t.Context(), "not jql at all", nil, "", 0)
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error = %v, want *APIError", err)
		}
		if apiErr.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", apiErr.StatusCode)
		}
	})

	t.Run("read-only mode does not block it", func(t *testing.T) {
		t.Parallel()
		var hits int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"isLast":true,"issues":[]}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok", ReadOnly: true})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if _, err := c.SearchIssues(t.Context(), "project = ABC", nil, "", 0); err != nil {
			t.Fatalf("SearchIssues under read-only: %v, want the read to be allowed", err)
		}
		if hits != 1 {
			t.Errorf("server saw %d request(s), want 1", hits)
		}
	})
}

func TestClient_CountIssues(t *testing.T) {
	t.Parallel()

	t.Run("sends jql, decodes the estimated count", func(t *testing.T) {
		t.Parallel()
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user, pass, ok := r.BasicAuth(); !ok || user != "u@example.com" || pass != "tok" {
				t.Errorf("basic auth = %q/%q, ok=%v; want the configured pair", user, pass, ok)
			}
			if r.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", r.Method)
			}
			if got := r.URL.Path; got != "/rest/api/3/search/approximate-count" {
				t.Errorf("path = %q, want /rest/api/3/search/approximate-count", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"count":1234}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		got, err := c.CountIssues(t.Context(), "project = ABC")
		if err != nil {
			t.Fatalf("CountIssues: %v", err)
		}
		if gotBody["jql"] != "project = ABC" {
			t.Errorf("body jql = %v, want %q", gotBody["jql"], "project = ABC")
		}
		if got.Count != 1234 {
			t.Errorf("count = %d, want 1234", got.Count)
		}
		if got.Retry != (core.RetryInfo{Attempts: 1}) {
			t.Errorf("retry info = %+v, want a single attempt with no wait", got.Retry)
		}
	})

	t.Run("bad request becomes a typed api error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errorMessages":["The JQL query is invalid"],"errors":{}}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		_, err = c.CountIssues(t.Context(), "not jql at all")
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error = %v, want *APIError", err)
		}
		if apiErr.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", apiErr.StatusCode)
		}
	})

	t.Run("read-only mode does not block it", func(t *testing.T) {
		t.Parallel()
		var hits int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"count":0}`))
		}))
		t.Cleanup(srv.Close)
		c, err := New(Config{BaseURL: srv.URL, Email: "u@example.com", APIToken: "tok", ReadOnly: true})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if _, err := c.CountIssues(t.Context(), "project = ABC"); err != nil {
			t.Fatalf("CountIssues under read-only: %v, want the read to be allowed", err)
		}
		if hits != 1 {
			t.Errorf("server saw %d request(s), want 1", hits)
		}
	})
}

// TestClientRefusesCleartextToRemoteHosts pins the rule at the place
// every client is built, whatever route built it.
//
// Config validation runs on ONE construction path, and `auth login`
// and `auth status` take another — so a rule enforced only there let
// both send the API token to any host that asked, in cleartext. A
// validation that the credential-carrying paths skip is not a
// validation, which is why this one lives at the transport.
func TestClientRefusesCleartextToRemoteHosts(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		base string
		ok   bool
	}{
		{name: "https anywhere", base: "https://example.atlassian.net", ok: true},
		{name: "http to localhost", base: "http://localhost:2990", ok: true},
		{name: "http to 127.0.0.1", base: "http://127.0.0.1:2990", ok: true},
		{name: "http to ipv6 loopback", base: "http://[::1]:2990", ok: true},
		{name: "http to a private address", base: "http://192.168.100.60:9999", ok: false},
		{name: "http to a public host", base: "http://example.atlassian.net", ok: false},
		{name: "http to a lookalike host", base: "http://localhost.example.com", ok: false},
		{name: "a scheme that is not http at all", base: "ftp://evil.example", ok: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(Config{BaseURL: tt.base, Email: "person@example.com", APIToken: "SECRET"})
			if tt.ok && err != nil {
				t.Errorf("%s was refused: %v", tt.base, err)
			}
			if !tt.ok && err == nil {
				t.Errorf("%s built a client; the token would cross the network in cleartext", tt.base)
			}
		})
	}
}

// redirectPair stands up a TLS origin that redirects to a cleartext one,
// both answering for NON-loopback hostnames.
//
// The hostnames matter: http to loopback is legitimately allowed (there
// is no network to intercept), so a test using httptest's own
// 127.0.0.1 URLs proves nothing about the rule — an earlier version of
// this test passed for exactly that reason. A custom dialer maps both
// names onto the local listeners so the client believes it is talking
// to a remote host.
func redirectPair(t *testing.T) (base string, client *http.Client, leaked *string) {
	t.Helper()

	var got string
	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(insecure.Close)

	// Same host, one scheme down — which is what makes the stdlib
	// forward the credential rather than strip it.
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://jira.example"+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(secure.Close)

	secureAddr := strings.TrimPrefix(secure.URL, "https://")
	insecureAddr := strings.TrimPrefix(insecure.URL, "http://")

	client = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			target := insecureAddr
			if strings.HasSuffix(addr, ":443") {
				target = secureAddr
			}
			return (&net.Dialer{}).DialContext(ctx, network, target)
		},
		//nolint:gosec // G402: the test's own throwaway certificate, on a dialer that never leaves this process
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	return "https://jira.example", client, &got
}

// TestARedirectCannotDowngradeToCleartext pins that following a
// redirect cannot undo the scheme rule the constructor enforces.
//
// requireEncrypted checks the base URL, which is the URL the caller
// chose — not the one a server can name in a Location header. Go's
// http.Client forwards Authorization across a redirect to the SAME
// host, and a scheme change does not make it a different host, so an
// https base redirected to http on that host puts the API token on the
// wire in the clear. The credential rides every request as Basic auth,
// so there is no later point at which to catch it.
func TestARedirectCannotDowngradeToCleartext(t *testing.T) {
	t.Parallel()
	base, client, leaked := redirectPair(t)

	// GET, so the write-replay guard would allow this redirect: only
	// the scheme rule can refuse it.
	c, err := New(Config{BaseURL: base, Email: "u@example.com", APIToken: "tok", HTTPClient: client})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.GetIssue(context.Background(), "AAA-1", nil); err == nil {
		t.Error("the redirect to http was followed without complaint")
	}
	if *leaked != "" {
		t.Errorf("the API token reached a cleartext endpoint: %q", *leaked)
	}
}

// TestACallerRedirectPolicyCannotDisplaceTheSchemeRule pins that the
// guard is not optional.
//
// The policy used to be installed only when the caller had supplied
// none, so any caller passing an http.Client with its own CheckRedirect
// silently opted out of both the scheme rule and the write-replay guard
// — the two things that must never be caller policy.
func TestACallerRedirectPolicyCannotDisplaceTheSchemeRule(t *testing.T) {
	t.Parallel()
	base, client, leaked := redirectPair(t)

	// A caller who allows everything.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return nil }

	c, err := New(Config{BaseURL: base, Email: "u@example.com", APIToken: "tok", HTTPClient: client})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.GetIssue(context.Background(), "AAA-1", nil); err == nil {
		t.Error("a caller's permissive redirect policy allowed the downgrade")
	}
	if *leaked != "" {
		t.Errorf("the API token reached a cleartext endpoint: %q", *leaked)
	}
}

// TestCleartextRefusalCarriesItsOwnSentinel pins that the refusal is
// distinguishable from ordinary validation.
//
// The command layer maps this sentinel to its own taxonomy code so the
// hint can name the fix — change the scheme — instead of the generic
// "check the values you passed", which says neither which value nor
// what is wrong with it.
func TestCleartextRefusalCarriesItsOwnSentinel(t *testing.T) {
	t.Parallel()

	_, err := New(Config{BaseURL: "http://example.atlassian.net", Email: "u@example.com", APIToken: "tok"})
	if err == nil {
		t.Fatal("plain http to a remote host was accepted")
	}
	if !errors.Is(err, ErrInsecureScheme) {
		t.Errorf("error is %v, want it to wrap ErrInsecureScheme", err)
	}
	// Loopback stays exempt: there is no network to intercept.
	if _, err := New(Config{BaseURL: "http://127.0.0.1:8080", Email: "u@example.com", APIToken: "tok"}); err != nil {
		t.Errorf("http to loopback was refused: %v", err)
	}
}
