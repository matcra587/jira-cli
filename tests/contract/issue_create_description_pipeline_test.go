package contract

// Regression coverage: an issue-create description — whether supplied as
// `description_markdown` or as a raw ADF `description` — MUST be routed
// through pipeline.RunMutation before submission. The validated,
// compatibility-applied document is what reaches the wire, never a
// post-pipeline conversion that skipped stage 2.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// createmetaDescriptionFields is the field-metadata array for a create
// screen carrying the wire system fields plus a description field.
const createmetaDescriptionFields = `[` +
	`{"fieldId":"project","name":"Project","required":true,"schema":{"type":"project"}},` +
	`{"fieldId":"issuetype","name":"Issue Type","required":true,"schema":{"type":"issuetype"}},` +
	`{"fieldId":"assignee","name":"Assignee","required":false,"schema":{"type":"user"}},` +
	`{"fieldId":"summary","name":"Summary","required":true,"schema":{"type":"string"}},` +
	`{"fieldId":"description","name":"Description","required":false,"schema":{"type":"doc"}}` +
	`]`

// TestIssueCreateMarkdownDescriptionSubmittedAsValidatedADF proves a
// `description_markdown` payload key is converted to ADF and submitted
// as a structured ADF document — not as the raw markdown string and not
// as a doc that bypassed validation.
func TestIssueCreateMarkdownDescriptionSubmittedAsValidatedADF(t *testing.T) {
	cap := &captureServer{}
	mux := http.NewServeMux()
	registerCreatemeta(mux, "PROJ", "Task", "10002", createmetaDescriptionFields)
	mux.HandleFunc("POST /rest/api/3/issue", func(w http.ResponseWriter, r *http.Request) {
		cap.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","key":"PROJ-1","self":"x"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	payload := `{"summary":"Hi","project_key":"PROJ","issue_type":"Task","description_markdown":"# Heading\n\nBody paragraph"}`
	path := filepath.Join(t.TempDir(), "create.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg,
		"issue", "create", "--no-input", "--json-input", path, "--output=json")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}

	body := cap.capturedBody()
	if body == nil {
		t.Fatalf("no POST body captured")
	}
	fields, _ := body["fields"].(map[string]any)
	if fields == nil {
		fields = body
	}
	// The raw markdown convenience key must never reach the wire.
	if _, leaked := fields["description_markdown"]; leaked {
		t.Fatalf("description_markdown reached the wire — Jira rejects unknown keys: %#v", fields)
	}
	desc, ok := fields["description"].(map[string]any)
	if !ok {
		t.Fatalf("description not submitted as an ADF document object: %#v", fields["description"])
	}
	if desc["type"] != "doc" {
		t.Fatalf("submitted description is not an ADF doc: %#v", desc)
	}
}

// TestIssueCreateRawADFDescriptionStrictRejectsBeforeWire proves a raw
// ADF `description` with an unknown node aborts in strict mode BEFORE
// any POST — confirming the description is validated by the pipeline,
// not converted past it. If the description bypassed stage 2, the POST
// would reach the server.
func TestIssueCreateRawADFDescriptionStrictRejectsBeforeWire(t *testing.T) {
	var posts atomic.Int32
	mux := http.NewServeMux()
	registerCreatemeta(mux, "PROJ", "Task", "10002", createmetaDescriptionFields)
	mux.HandleFunc("POST /rest/api/3/issue", func(w http.ResponseWriter, _ *http.Request) {
		posts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","key":"PROJ-1","self":"x"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	payload := `{"summary":"Hi","project_key":"PROJ","issue_type":"Task","description":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"x"},{"type":"unknown_magic_node"}]}]}}`
	path := filepath.Join(t.TempDir(), "create.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg,
		"issue", "create", "--no-input", "--json-input", path, "--output=json")
	if code == 0 {
		t.Fatalf("strict mode accepted an unknown ADF node in description; want abort\nstdout=%s", stdout)
	}
	if n := posts.Load(); n != 0 {
		t.Fatalf("description bypassed the pipeline — %d POST(s) reached the wire despite an invalid node", n)
	}
	var env struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	decodeErrorEnvelopeFromStdout(t, stdout, stderr, []string{"jira", "--config", cfg, "issue", "create", "--no-input", "--json-input", path, "--output=json"}, &env)
	if len(env.Errors) == 0 {
		t.Fatalf("expected a validation error naming the unknown node: %s", stderr)
	}
}

// TestIssueCreateRawADFDescriptionBestEffortWarnsAndSubmits proves a raw
// ADF `description` with an unknown node, in best-effort mode, surfaces
// a pipeline warning AND still submits — proving stage 2 ran on the
// description (a post-pipeline conversion would warn about nothing).
func TestIssueCreateRawADFDescriptionBestEffortWarnsAndSubmits(t *testing.T) {
	cap := &captureServer{}
	mux := http.NewServeMux()
	registerCreatemeta(mux, "PROJ", "Task", "10002", createmetaDescriptionFields)
	mux.HandleFunc("POST /rest/api/3/issue", func(w http.ResponseWriter, r *http.Request) {
		cap.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","key":"PROJ-1","self":"x"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	payload := `{"summary":"Hi","project_key":"PROJ","issue_type":"Task","description":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"x"},{"type":"unknown_magic_node"}]}]}}`
	path := filepath.Join(t.TempDir(), "create.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg, "--adf-best-effort",
		"issue", "create", "--no-input", "--json-input", path, "--output=json")
	if code != 0 {
		t.Fatalf("best-effort exit = %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	var env struct {
		Warnings []map[string]any `json:"warnings"`
	}
	if err := json.Unmarshal(stdout, &env); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, stdout)
	}
	if len(env.Warnings) == 0 {
		t.Fatalf("best-effort: unknown node in description must produce a pipeline warning: %s", stdout)
	}
	body := cap.capturedBody()
	if body == nil {
		t.Fatalf("no POST body captured")
	}
	fields, _ := body["fields"].(map[string]any)
	if fields == nil {
		fields = body
	}
	if _, ok := fields["description"].(map[string]any); !ok {
		t.Fatalf("description not submitted as an ADF document object: %#v", fields["description"])
	}
}
