package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAliasSetListDeleteAndExpansion(t *testing.T) {
	var seenJQL requestCapture[string]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			JQL string `json:"jql"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode search body: %v", err)
		}
		seenJQL.Add(body.JQL)
		_, _ = w.Write([]byte(`{"isLast":true,"issues":[{"key":"PROJ-1","fields":{"summary":"Alias hit"}}]}`))
	}))
	defer srv.Close()

	cfg := jiraConfig(t, srv.URL)
	cmd := exec.Command(buildJiraBinary(t), "--config", cfg, "alias", "set", "mine", "--", "issue", "list", "--jql", "project = PROJ")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("alias set error = %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"name":"mine"`) {
		t.Fatalf("alias set output = %s", out)
	}

	cmd = exec.Command(buildJiraBinary(t), "--config", cfg, "alias", "list", "--output=json")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("alias list error = %v\n%s", err, out)
	}
	if !envelopeHasKV(t, out, "mine", "issue list --jql 'project = PROJ'") {
		t.Fatalf("alias list output = %s", out)
	}

	cmd = exec.Command(buildJiraBinary(t), "--config", cfg, "mine", "--output=json")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("alias expansion error = %v\n%s", err, out)
	}
	if seenJQL.Last() != "project = PROJ ORDER BY updated DESC" || !envelopeHasKV(t, out, "key", "PROJ-1") {
		t.Fatalf("alias expansion seenJQL=%q output=%s", seenJQL.Last(), out)
	}

	// The test harness is non-TTY, so alias delete is headless and needs --force.
	cmd = exec.Command(buildJiraBinary(t), "--config", cfg, "alias", "delete", "mine", "--force")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("alias delete error = %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"deleted":true`) {
		t.Fatalf("alias delete output = %s", out)
	}
}

func TestAliasSetSingleStringExpansionStoresVerbatimAndDispatches(t *testing.T) {
	var seenJQL requestCapture[string]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			JQL string `json:"jql"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode search body: %v", err)
		}
		seenJQL.Add(body.JQL)
		_, _ = w.Write([]byte(
			`{"isLast":true,"issues":[{"key":"PROJ-1","fields":{"summary":"Alias hit"}}]}`,
		))
	}))
	defer srv.Close()

	cfg := jiraConfig(t, srv.URL)
	expansion := " issue list --assignee me "
	cmd := exec.Command(
		buildJiraBinary(t),
		"--config", cfg,
		"alias", "set", "inbox-test", expansion,
		"--output=json",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("alias set error = %v\n%s", err, out)
	}
	if !envelopeHasKV(t, out, "expansion", expansion) {
		t.Fatalf("alias set wrapped single-string expansion:\n%s", out)
	}

	cmd = exec.Command(buildJiraBinary(t), "--config", cfg, "inbox-test", "--output=json")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("alias dispatch error = %v\n%s", err, out)
	}
	if seenJQL.Last() != "assignee = currentUser() ORDER BY updated DESC" ||
		!envelopeHasKV(t, out, "key", "PROJ-1") {
		t.Fatalf("alias dispatch seenJQL=%q output=%s", seenJQL.Last(), out)
	}
}

func TestAliasImportFromYAML(t *testing.T) {
	cfg := jiraConfig(t, "http://127.0.0.1:1")
	path := filepath.Join(t.TempDir(), "aliases.yml")
	if err := os.WriteFile(path, []byte("mine: issue list --jql \"assignee = currentUser()\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd := exec.Command(buildJiraBinary(t), "--config", cfg, "alias", "import", path, "--output=json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("alias import error = %v\n%s", err, out)
	}
	if !envelopeHasKV(t, out, "imported", 1) || !envelopeHasValue(t, out, "mine") {
		t.Fatalf("alias import output = %s", out)
	}

	cmd = exec.Command(buildJiraBinary(t), "--config", cfg, "alias", "list", "--output=json")
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("alias list error = %v\n%s", err, out)
	}
	if !envelopeHasKV(t, out, "mine", `issue list --jql "assignee = currentUser()"`) {
		t.Fatalf("alias list after import = %s", out)
	}
}

// alias set is a read-modify-write command; like every config-write
// command it must persist only file-backed values and never bake a
// transient JIRA_* env overlay into the saved TOML.
func TestAliasSetDoesNotPersistEnvOverlay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := `default_profile = "work"

[[profiles]]
name = "work"
base_url = "https://work.atlassian.net"
auth_type = "token"
secret_backend = "keyring"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cmd := exec.Command(buildJiraBinary(t), "--config", path, "--output=json", "alias", "set", "mine", "issue", "list")
	cmd.Env = append(
		os.Environ(),
		"JIRA_PROFILE_WORK_DEFAULT_ISSUE_TYPE=OverlayType",
		"JIRA_DEFAULT_PROFILE=phantom",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("alias set error = %v\n%s", err, out)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(content), "OverlayType") {
		t.Fatalf("alias set persisted JIRA_PROFILE_*_DEFAULT_ISSUE_TYPE env overlay into TOML:\n%s", content)
	}
	if strings.Contains(string(content), "phantom") {
		t.Fatalf("alias set persisted JIRA_DEFAULT_PROFILE env overlay into TOML:\n%s", content)
	}
	if !strings.Contains(string(content), `mine =`) {
		t.Fatalf("alias set did not persist the alias:\n%s", content)
	}
}
