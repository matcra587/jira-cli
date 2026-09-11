package contract

// Coverage for `issue edit` Markdown descriptions. A description supplied
// via the --markdown flag, or via a `description_markdown` key
// inside a --json-input `{fields:{...}}` payload, MUST be converted to ADF
// through the same lossy converter the create path uses and routed through
// pipeline.RunMutation. The validated ADF document is what reaches the wire;
// the raw Markdown convenience key never does. Strict mode aborts on lossy
// conversions before any network call; best-effort proceeds and warns.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// adfDocText concatenates every text node in an ADF document so a test can
// prove the Markdown actually converted to content, rather than an empty
// {type:"doc","content":[]} that would still satisfy a type=="doc" check.
func adfDocText(node map[string]any) string {
	var b strings.Builder
	var walk func(v any)
	walk = func(v any) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		if m["type"] == "text" {
			if s, ok := m["text"].(string); ok {
				b.WriteString(s)
			}
		}
		if content, ok := m["content"].([]any); ok {
			for _, child := range content {
				walk(child)
			}
		}
	}
	walk(node)
	return b.String()
}

// adfDocHasNode reports whether the ADF document contains a node of the given
// type at any depth.
func adfDocHasNode(node map[string]any, typ string) bool {
	if node["type"] == typ {
		return true
	}
	content, ok := node["content"].([]any)
	if !ok {
		return false
	}
	for _, child := range content {
		if m, ok := child.(map[string]any); ok && adfDocHasNode(m, typ) {
			return true
		}
	}
	return false
}

// assertConvertedADF fails unless desc is an ADF doc that contains a heading
// node and the wanted body text — proving conversion fidelity, not just shape.
func assertConvertedADF(t *testing.T, desc map[string]any, wantBody string) {
	t.Helper()
	if desc["type"] != "doc" {
		t.Fatalf("description is not an ADF doc: %#v", desc)
	}
	if !adfDocHasNode(desc, "heading") {
		t.Fatalf("converted description lost its heading node: %#v", desc)
	}
	if got := adfDocText(desc); !strings.Contains(got, wantBody) {
		t.Fatalf("converted description missing body text %q, got %q", wantBody, got)
	}
}

// TestIssueEditDescriptionMarkdownFlagSubmitsValidatedADF proves the
// --markdown flag is converted to ADF and submitted as a
// structured document on the live PUT, with the raw convenience key absent.
func TestIssueEditDescriptionMarkdownFlagSubmitsValidatedADF(t *testing.T) {
	cap := &captureServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rest/api/3/issue/PROJ-1/editmeta", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(editmetaFloatField))
	})
	mux.HandleFunc("PUT /rest/api/3/issue/PROJ-1", func(w http.ResponseWriter, r *http.Request) {
		cap.record(r)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg,
		"issue", "edit", "PROJ-1", "--no-input",
		"--markdown", "# Heading\n\nBody paragraph", "--output=json")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}

	body := cap.capturedBody()
	if body == nil {
		t.Fatalf("no PUT body captured")
	}
	fields, _ := body["fields"].(map[string]any)
	if fields == nil {
		t.Fatalf("PUT body missing fields object: %#v", body)
	}
	if _, leaked := fields["description_markdown"]; leaked {
		t.Fatalf("description_markdown reached the wire — Jira rejects unknown keys: %#v", fields)
	}
	desc, ok := fields["description"].(map[string]any)
	if !ok {
		t.Fatalf("description not submitted as an ADF document object: %#v", fields["description"])
	}
	assertConvertedADF(t, desc, "Body paragraph")
}

// TestIssueEditDescriptionMarkdownJSONKeySubmitsValidatedADF proves the
// `description_markdown` create alias also works inside an edit
// --json-input `fields` object.
func TestIssueEditDescriptionMarkdownJSONKeySubmitsValidatedADF(t *testing.T) {
	cap := &captureServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rest/api/3/issue/PROJ-1/editmeta", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(editmetaFloatField))
	})
	mux.HandleFunc("PUT /rest/api/3/issue/PROJ-1", func(w http.ResponseWriter, r *http.Request) {
		cap.record(r)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	payload := `{"fields":{"description_markdown":"## Notes\n\nA paragraph."}}`
	path := filepath.Join(t.TempDir(), "edit.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg,
		"issue", "edit", "PROJ-1", "--no-input", "--json-input", path, "--output=json")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}

	body := cap.capturedBody()
	if body == nil {
		t.Fatalf("no PUT body captured")
	}
	fields, _ := body["fields"].(map[string]any)
	if fields == nil {
		t.Fatalf("PUT body missing fields object: %#v", body)
	}
	if _, leaked := fields["description_markdown"]; leaked {
		t.Fatalf("description_markdown reached the wire — Jira rejects unknown keys: %#v", fields)
	}
	desc, ok := fields["description"].(map[string]any)
	if !ok {
		t.Fatalf("description not submitted as an ADF document object: %#v", fields["description"])
	}
	assertConvertedADF(t, desc, "A paragraph.")
}

// TestIssueEditDescriptionMarkdownDryRunPreview proves --dry-run previews the
// encoded ADF under data.fields.description and never contacts Jira.
func TestIssueEditDescriptionMarkdownDryRunPreview(t *testing.T) {
	cmd := exec.Command(buildJiraBinary(t),
		"issue", "edit", "PROJ-1", "--dry-run", "--no-input",
		"--markdown", "# Title\n\nText body", "--output=json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("issue edit dry-run error = %v\n%s", err, out)
	}
	var env struct {
		Data struct {
			DryRun bool           `json:"dry_run"`
			Fields map[string]any `json:"fields"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out)
	}
	if !env.Data.DryRun {
		t.Fatalf("dry-run preview did not set dry_run=true: %s", out)
	}
	if _, leaked := env.Data.Fields["description_markdown"]; leaked {
		t.Fatalf("preview leaks the raw Markdown convenience key: %#v", env.Data.Fields)
	}
	desc, ok := env.Data.Fields["description"].(map[string]any)
	if !ok {
		t.Fatalf("preview description is not an encoded ADF doc: %#v", env.Data.Fields["description"])
	}
	assertConvertedADF(t, desc, "Text body")
}

// TestIssueEditDescriptionMarkdownMultiKeyDryRun proves a multi-key edit
// routes the same converted ADF into every per-key result, with no aliasing
// across the fan-out.
func TestIssueEditDescriptionMarkdownMultiKeyDryRun(t *testing.T) {
	cmd := exec.Command(buildJiraBinary(t),
		"issue", "edit", "PROJ-1", "PROJ-2", "--dry-run", "--no-input",
		"--markdown", "# Shared\n\nBody para", "--output=json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("multi-key dry-run error = %v\n%s", err, out)
	}
	var env struct {
		Data struct {
			Results []struct {
				Key  string `json:"key"`
				Data struct {
					Fields map[string]any `json:"fields"`
				} `json:"data"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out)
	}
	if len(env.Data.Results) != 2 {
		t.Fatalf("expected 2 results, got %d: %s", len(env.Data.Results), out)
	}
	for _, r := range env.Data.Results {
		desc, ok := r.Data.Fields["description"].(map[string]any)
		if !ok {
			t.Fatalf("key %s missing encoded ADF description: %#v", r.Key, r.Data.Fields)
		}
		assertConvertedADF(t, desc, "Body para")
	}
}

// TestIssueEditMarkdownExcludesJSONInput proves --markdown and --json-input
// reject the combination outright: they are two ways to set the same field,
// so the earlier silent flag-over-payload precedence is now a clear error
// declared as Cobra flag metadata.
func TestIssueEditMarkdownExcludesJSONInput(t *testing.T) {
	payload := `{"fields":{"description_markdown":"# From payload\n\npayload body"}}`
	path := filepath.Join(t.TempDir(), "edit.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cmd := exec.Command(buildJiraBinary(t),
		"issue", "edit", "PROJ-1", "--dry-run", "--no-input",
		"--json-input", path,
		"--markdown", "# From flag\n\nflag body", "--output=json")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("--markdown with --json-input must be rejected, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "markdown") || !strings.Contains(string(out), "json-input") {
		t.Fatalf("mutual-exclusion error must name both flags:\n%s", out)
	}
}

// TestIssueEditDescriptionMarkdownStrictAbortsBeforeWire proves a lossy
// Markdown conversion (raw HTML has no ADF authoring path) aborts in the
// default strict mode before any PUT reaches the server.
func TestIssueEditDescriptionMarkdownStrictAbortsBeforeWire(t *testing.T) {
	var puts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rest/api/3/issue/PROJ-1/editmeta", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(editmetaFloatField))
	})
	mux.HandleFunc("PUT /rest/api/3/issue/PROJ-1", func(w http.ResponseWriter, _ *http.Request) {
		puts.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := jiraConfig(t, srv.URL)
	stdout, stderr, code := runJira(t, "--config", cfg,
		"issue", "edit", "PROJ-1", "--no-input",
		"--markdown", "intro\n\n<div>\nraw\n</div>\n", "--output=json")
	if code == 0 {
		t.Fatalf("strict mode accepted lossy raw HTML; want abort\nstdout=%s", stdout)
	}
	if n := puts.Load(); n != 0 {
		t.Fatalf("description bypassed strict abort — %d PUT(s) reached the wire", n)
	}
	var env struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	decodeErrorEnvelopeFromStdout(t, stdout, stderr,
		[]string{"jira", "--config", cfg, "issue", "edit", "PROJ-1"}, &env)
	if len(env.Errors) == 0 {
		t.Fatalf("expected a validation error on lossy Markdown: %s", stderr)
	}
}

// TestIssueEditDescriptionMarkdownBestEffortWarns proves --adf-best-effort
// proceeds through a lossy conversion, encoding what it can and surfacing a
// markdown_lossy_conversion warning instead of aborting.
func TestIssueEditDescriptionMarkdownBestEffortWarns(t *testing.T) {
	cmd := exec.Command(buildJiraBinary(t),
		"--adf-best-effort", "issue", "edit", "PROJ-1", "--dry-run", "--no-input",
		"--markdown", "intro\n\n<div>\nraw\n</div>\n", "--output=json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("best-effort dry-run should proceed, got error = %v\n%s", err, out)
	}
	var env struct {
		Data struct {
			DryRun bool           `json:"dry_run"`
			Fields map[string]any `json:"fields"`
		} `json:"data"`
		Warnings []struct {
			Type     string `json:"type"`
			NodeType string `json:"node_type"`
		} `json:"warnings"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out)
	}
	if !env.Data.DryRun {
		t.Fatalf("expected dry_run=true: %s", out)
	}
	var sawHTMLLoss bool
	for _, w := range env.Warnings {
		if w.Type == "markdown_lossy_conversion" && w.NodeType == "raw HTML block" {
			sawHTMLLoss = true
		}
	}
	if !sawHTMLLoss {
		t.Fatalf("best-effort must surface a markdown_lossy_conversion warning for the dropped HTML block: %s", out)
	}
	desc, ok := env.Data.Fields["description"].(map[string]any)
	if !ok || desc["type"] != "doc" {
		t.Fatalf("best-effort must still encode the convertible content: %#v", env.Data.Fields["description"])
	}
	if txt := adfDocText(desc); !strings.Contains(txt, "intro") {
		t.Fatalf("best-effort dropped the convertible intro text, got %q", txt)
	}
}
