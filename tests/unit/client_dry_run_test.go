package unit

// Service-level dry-run guard: a client built WithDryRun must refuse
// every state-changing HTTP method (POST/PUT/PATCH/DELETE) before it
// reaches the wire. This is the defense-in-depth net behind the
// command-layer dry-run branches — even a command that forgot to gate a
// submission cannot mutate Jira under dry-run.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

func TestClientDryRunRefusesMutatingMethods(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		t.Errorf("dry-run client reached the server: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := jira.NewClient(jira.WithBaseURL(srv.URL), jira.WithDryRun(true))

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, err := client.NewRequest(context.Background(), method, "/rest/api/3/issue", map[string]any{"x": 1})
		if err != nil {
			t.Fatalf("NewRequest(%s) error = %v", method, err)
		}
		_, err = client.Do(req, nil)
		if err == nil {
			t.Fatalf("Do(%s) succeeded under dry-run; mutation must be refused", method)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "dry-run") {
			t.Fatalf("Do(%s) error = %v; want a dry-run refusal", method, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("dry-run client made %d live request(s); want 0", n)
	}
}

func TestClientDryRunAllowsSafeReads(t *testing.T) {
	var gets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		gets.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := jira.NewClient(jira.WithBaseURL(srv.URL), jira.WithDryRun(true))
	req, err := client.NewRequest(context.Background(), http.MethodGet, "/rest/api/3/myself", nil)
	if err != nil {
		t.Fatalf("NewRequest(GET) error = %v", err)
	}
	if _, err := client.Do(req, nil); err != nil {
		t.Fatalf("Do(GET) under dry-run error = %v; reads must still be allowed", err)
	}
	if n := gets.Load(); n != 1 {
		t.Fatalf("expected the GET to reach the server, got %d hits", n)
	}
}
