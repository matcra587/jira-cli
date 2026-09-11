package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIssueListKeyRangesUseParallelism(t *testing.T) {
	keyPattern := regexp.MustCompile(`PROJ-\d+`)
	var current atomic.Int32
	var peak atomic.Int32
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		requests.Add(1)
		now := current.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
		current.Add(-1)

		var payload struct {
			JQL string `json:"jql"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode search payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		keys := keyPattern.FindAllString(payload.JQL, -1)
		var body strings.Builder
		body.WriteString(`{"isLast":true,"issues":[`)
		for i, key := range keys {
			if i > 0 {
				body.WriteByte(',')
			}
			_, _ = fmt.Fprintf(&body, `{"id":"%[1]s","key":"%[1]s","fields":{"summary":"%[1]s summary"}}`, key)
		}
		body.WriteString(`]}`)
		_, _ = w.Write([]byte(body.String()))
	}))
	defer srv.Close()

	bin := buildJiraBinary(t)
	cmd := exec.Command(bin, "--config", jiraConfig(t, srv.URL), "--output=json",
		"issue", "list", "--key", "PROJ-1..60", "-p", "2")
	cmd.Env = append(os.Environ(), "JIRA_TOKEN_DEFAULT=test-token")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("issue list multi-key error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	var env struct {
		OK   bool `json:"ok"`
		Meta struct {
			Pagination struct {
				MaxResults int  `json:"maxResults"`
				Total      int  `json:"total"`
				IsLast     bool `json:"isLast"`
			} `json:"pagination"`
		} `json:"meta"`
		Data struct {
			Issues []struct {
				Key string `json:"key"`
			} `json:"issues"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout envelope is not JSON: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if !env.OK {
		t.Fatalf("issue list envelope ok=false:\n%s", stdout.String())
	}
	if got := len(env.Data.Issues); got != 60 {
		t.Fatalf("issue count = %d, want all 60 requested keys", got)
	}
	if env.Data.Issues[0].Key != "PROJ-1" || env.Data.Issues[59].Key != "PROJ-60" {
		t.Fatalf("issue order first=%q last=%q, want requested key order", env.Data.Issues[0].Key, env.Data.Issues[59].Key)
	}
	if requests.Load() < 2 {
		t.Fatalf("search requests = %d, want expanded keys split into multiple chunks", requests.Load())
	}
	if peak.Load() < 2 {
		t.Fatalf("peak concurrency = %d, want local -p 2 to allow two in-flight key chunks", peak.Load())
	}
}

func TestIssueListKeyRangesKeepRequestedOrderWhenJiraReturnsChunkOutOfOrder(t *testing.T) {
	keyPattern := regexp.MustCompile(`PROJ-\d+`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var payload struct {
			JQL string `json:"jql"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode search payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		keys := keyPattern.FindAllString(payload.JQL, -1)
		var body strings.Builder
		body.WriteString(`{"isLast":true,"issues":[`)
		for i, key := range slices.Backward(keys) {
			if i != len(keys)-1 {
				body.WriteByte(',')
			}

			_, _ = fmt.Fprintf(&body, `{"id":"%[1]s","key":"%[1]s","fields":{"summary":"%[1]s summary"}}`, key)
		}
		body.WriteString(`]}`)
		_, _ = w.Write([]byte(body.String()))
	}))
	defer srv.Close()

	bin := buildJiraBinary(t)
	cmd := exec.Command(bin, "--config", jiraConfig(t, srv.URL), "--output=json",
		"issue", "list", "--key", "PROJ-1:5")
	cmd.Env = append(os.Environ(), "JIRA_TOKEN_DEFAULT=test-token")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("issue list key order error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	var env struct {
		Data struct {
			Issues []struct {
				Key string `json:"key"`
			} `json:"issues"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout envelope is not JSON: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	got := make([]string, len(env.Data.Issues))
	for i, issue := range env.Data.Issues {
		got[i] = issue.Key
	}
	if strings.Join(got, ",") != "PROJ-1,PROJ-2,PROJ-3,PROJ-4,PROJ-5" {
		t.Fatalf("issue list key order = %v, want requested key order", got)
	}
}

func TestIssueListKeyRangesPreserveSuccessfulChunksOnChunkFailure(t *testing.T) {
	keyPattern := regexp.MustCompile(`PROJ-\d+`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var payload struct {
			JQL string `json:"jql"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode search payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		keys := keyPattern.FindAllString(payload.JQL, -1)
		if len(keys) > 0 && keys[0] == "PROJ-51" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"errorMessages":["rate limited"],"errors":{}}`))
			return
		}
		var body strings.Builder
		body.WriteString(`{"isLast":true,"issues":[`)
		for i, key := range keys {
			if i > 0 {
				body.WriteByte(',')
			}
			_, _ = fmt.Fprintf(&body, `{"id":"%[1]s","key":"%[1]s","fields":{"summary":"%[1]s summary"}}`, key)
		}
		body.WriteString(`]}`)
		_, _ = w.Write([]byte(body.String()))
	}))
	defer srv.Close()

	bin := buildJiraBinary(t)
	// Retry off: this exercises chunk-failure preservation, not the retry
	// loop (covered in internal/jira), so the 429 must surface on the first
	// attempt without real backoff.
	cmd := exec.Command(bin, "--config", jiraConfig(t, srv.URL), "--output=json",
		"--max-retry-wait", "0",
		"issue", "list", "--key", "PROJ-1:60", "-p", "2")
	cmd.Env = append(os.Environ(), "JIRA_TOKEN_DEFAULT=test-token")
	var env struct {
		OK   bool `json:"ok"`
		Meta struct {
			Pagination struct {
				MaxResults int  `json:"maxResults"`
				Total      int  `json:"total"`
				IsLast     bool `json:"isLast"`
			} `json:"pagination"`
		} `json:"meta"`
		Data struct {
			Issues []struct {
				Key string `json:"key"`
			} `json:"issues"`
			SucceededKeyChunks int `json:"succeeded_key_chunks"`
			FailedKeyChunks    []struct {
				KeyExpr string `json:"key_expr"`
				Error   struct {
					Code       string `json:"code"`
					HTTPStatus int    `json:"http_status"`
				} `json:"error"`
			} `json:"failed_key_chunks"`
		} `json:"data"`
		Errors []struct {
			Code       string `json:"code"`
			HTTPStatus int    `json:"http_status"`
		} `json:"errors"`
	}
	_, _, _ = runCommandExpectErrorEnvelope(t, cmd, &env)

	if env.OK {
		t.Fatal("issue list chunk failure envelope ok=true, want false")
	}
	if got := len(env.Data.Issues); got != 50 {
		t.Fatalf("preserved issue count = %d, want successful chunk's 50 issues", got)
	}
	if env.Meta.Pagination.MaxResults != 60 || env.Meta.Pagination.Total != 50 || !env.Meta.Pagination.IsLast {
		t.Fatalf("partial pagination = %+v, want maxResults=60 total=50 isLast=true", env.Meta.Pagination)
	}
	if env.Data.Issues[0].Key != "PROJ-1" || env.Data.Issues[49].Key != "PROJ-50" {
		t.Fatalf("preserved issues first=%q last=%q, want PROJ-1..PROJ-50",
			env.Data.Issues[0].Key, env.Data.Issues[49].Key)
	}
	if env.Data.SucceededKeyChunks != 1 || len(env.Data.FailedKeyChunks) != 1 {
		t.Fatalf("chunk summary succeeded=%d failed=%d", env.Data.SucceededKeyChunks, len(env.Data.FailedKeyChunks))
	}
	if !strings.HasPrefix(env.Data.FailedKeyChunks[0].KeyExpr, "PROJ-51,") || env.Data.FailedKeyChunks[0].Error.HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("failed chunk detail = %+v", env.Data.FailedKeyChunks[0])
	}
	if len(env.Errors) == 0 || env.Errors[0].HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("top-level errors missing chunk failure: %+v", env.Errors)
	}
}
