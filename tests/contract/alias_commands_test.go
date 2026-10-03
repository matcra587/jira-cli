package contract

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func aliasConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func aliasFrame(t *testing.T, path, stdin string, args ...string) frame {
	t.Helper()
	argv := append([]string{"--config", path, "--output=json"}, args...)
	stdout, stderr, exit := runBinaryEnvInputStarted(t, []string{"JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_READ_ONLY=true"}, stdin, nil, argv...)
	var result frame
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || exit != 0 || !result.OK {
		t.Fatalf("alias command failed: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
	}
	return result
}

func TestAliasCommandsSetListPreviewAndDeleteWithoutCredentials(t *testing.T) {
	t.Parallel()
	path := aliasConfig(t, "# formatting survives previews\ntheme='dark'\ntimeout='2m'\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const expansion = " version "
	f := aliasFrame(t, path, "", "alias", "set", "my.inbox", expansion, "--dry-run")
	var set envelope.AliasSetOutput
	if err := json.Unmarshal(f.Data, &set); err != nil || !set.DryRun || set.Changed || set.Expansion != expansion || set.Previous != nil {
		t.Fatal("set preview lost literal input or claimed a save")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("set preview changed the file")
	}
	f = aliasFrame(t, path, "", "alias", "set", "my.inbox", expansion)
	if err := json.Unmarshal(f.Data, &set); err != nil || set.DryRun || !set.Changed || set.Expansion != expansion {
		t.Fatal("live alias set did not acknowledge the local update")
	}
	f = aliasFrame(t, path, "", "alias", "set", "my.inbox", expansion)
	if err := json.Unmarshal(f.Data, &set); err != nil || set.Changed || set.Previous == nil || *set.Previous != expansion {
		t.Fatal("same-value alias set rewrote the file or lost the previous value")
	}
	f = aliasFrame(t, path, "", "alias", "ls")
	var list envelope.AliasListOutput
	if err := json.Unmarshal(f.Data, &list); err != nil || list.Count != 1 || !maps.Equal(list.Aliases, map[string]string{"my.inbox": expansion}) {
		t.Fatal("list lost the literal stored alias")
	}
	f = aliasFrame(t, path, "", "alias", "delete", "my.inbox", "--dry-run")
	var deleted envelope.AliasDeleteOutput
	if err := json.Unmarshal(f.Data, &deleted); err != nil || !deleted.DryRun || !deleted.Deleted || deleted.Changed {
		t.Fatal("delete preview did not report the proposed removal")
	}
	f = aliasFrame(t, path, "", "alias", "rm", "my.inbox", "--yes")
	if err := json.Unmarshal(f.Data, &deleted); err != nil || deleted.DryRun || !deleted.Deleted || !deleted.Changed {
		t.Fatal("consented delete did not remove the alias")
	}
	f = aliasFrame(t, path, "", "alias", "delete", "my.inbox", "--force")
	if err := json.Unmarshal(f.Data, &deleted); err != nil || deleted.Deleted || deleted.Changed {
		t.Fatal("missing-name deletion fabricated a change")
	}
	stored, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(stored), "api_token") || strings.Contains(string(stored), "read_only") || !strings.Contains(string(stored), "2m") {
		t.Fatal("local alias commands persisted overlays or discarded a preference")
	}
}

func TestAliasDeleteHeadlessRefusesBeforeReadingOrWritingConfig(t *testing.T) {
	t.Parallel()
	for _, content := range []string{"[aliases]\nmine='version'\n", "[aliases]\nmine=["} {
		path := aliasConfig(t, content)
		for _, env := range [][]string{nil, {"AGENT=1"}} {
			stdout, _, exit := runBinaryEnv(t, env, "alias", "delete", "mine", "--config", path, "--output=json", "--no-input")
			var result apiFrame
			if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.OK || exit != 3 || len(result.Errors) != 1 || result.Errors[0].Code != "flag_required" || result.Errors[0].Field != "force" {
				t.Fatal("headless alias delete lost its force refusal before config loading")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != content {
				t.Fatal("refused alias deletion changed local state")
			}
		}
	}
}

func TestAliasImportResolvesForwardChainsReportsCyclesAndKeepsCollisions(t *testing.T) {
	t.Parallel()
	path := aliasConfig(t, "[aliases]\nkept='version'\n")
	const document = "first: last\nlast: version\na: b\nb: a\nunknown: not-a-command\nkept: 'issue list'\nversion: 'issue list'\n"
	f := aliasFrame(t, path, document, "alias", "import", "-", "--dry-run")
	var out envelope.AliasImportOutput
	if err := json.Unmarshal(f.Data, &out); err != nil || !out.DryRun || out.Imported != 2 || out.Changed || len(out.Skipped) != 5 || strings.Join(out.Aliases, ",") != "first,last" {
		t.Fatal("import preview lost forward dependencies, exclusions or deterministic order")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "[aliases]\nkept='version'\n" {
		t.Fatal("import preview changed aliases")
	}
	f = aliasFrame(t, path, document, "alias", "import")
	if err := json.Unmarshal(f.Data, &out); err != nil || out.DryRun || !out.Changed || out.Imported != 2 || len(out.Skipped) != 5 {
		t.Fatal("live import did not acknowledge the validated merge")
	}
	f = aliasFrame(t, path, "", "alias", "list")
	var list envelope.AliasListOutput
	if err := json.Unmarshal(f.Data, &list); err != nil || !maps.Equal(list.Aliases, map[string]string{"kept": "version", "first": "last", "last": "version"}) {
		t.Fatal("live import stored invalid entries or replaced a kept name")
	}
	f = aliasFrame(t, path, "kept: ' issue list '\n", "alias", "import", "--clobber")
	if err := json.Unmarshal(f.Data, &out); err != nil || out.Imported != 1 || !out.Changed {
		t.Fatal("explicit clobber did not replace the existing alias")
	}
	f = aliasFrame(t, path, "", "alias", "list")
	if err := json.Unmarshal(f.Data, &list); err != nil || list.Aliases["kept"] != " issue list " {
		t.Fatal("YAML import rewrote literal expansion whitespace")
	}
}

func TestAliasMalformedImportsAndSetsLeaveFilesUntouched(t *testing.T) {
	t.Parallel()
	path := aliasConfig(t, "[aliases]\nmine='version'\n")
	for _, input := range []string{"mine: true", "mine: null", "mine: version\nmine: 'issue list'", "mine: version\n---\nextra: version"} {
		stdout, _, exit := runBinaryEnvInputStarted(t, []string{"JIRA_KEYRING=false"}, input, nil, "alias", "import", "--config", path, "--output=json")
		var result apiFrame
		if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.OK || exit != 3 || len(result.Errors) != 1 || result.Errors[0].Code != "alias_invalid" {
			t.Fatal("malformed YAML was accepted or lost its typed refusal")
		}
	}
	for _, args := range [][]string{{"mine", "mine"}, {"version", "issue list"}, {"mine", "version '"}, {"mine", "issue nonexistent"}} {
		argv := append([]string{"alias", "set"}, args...)
		argv = append(argv, "--config", path, "--output=json")
		stdout, _, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, argv...)
		var result apiFrame
		if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.OK || exit != 3 || len(result.Errors) != 1 || result.Errors[0].Code != "alias_invalid" {
			t.Fatal("invalid alias upsert was accepted")
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "[aliases]\nmine='version'\n" {
		t.Fatal("refused alias input changed local settings")
	}
}

func TestAliasEmptyImportAndHumanListRemainInspectable(t *testing.T) {
	t.Parallel()
	path := aliasConfig(t, "[aliases]\nempty=''\nnormal='version'\n")
	f := aliasFrame(t, path, "{}", "alias", "import")
	var imported envelope.AliasImportOutput
	if err := json.Unmarshal(f.Data, &imported); err != nil || imported.Imported != 0 || imported.Changed || imported.Aliases == nil || imported.Skipped == nil {
		t.Fatal("empty import fabricated a write or emitted null collections")
	}
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_KEYRING=false"}, "alias", "list", "--config", path, "--output=human")
	if exit != 0 || !strings.Contains(stdout, "empty") || !strings.Contains(stdout, "normal") || !strings.Contains(stdout, "version") {
		t.Fatalf("human alias list hides malformed empty definitions: %d %s", exit, stderr)
	}
}

func TestAliasExpansionDispatchesQuotedQueriesThroughSharedJiraTransport(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/search/jql" {
			t.Errorf("unexpected alias request: %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			JQL string `json:"jql"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		query = body.JQL
		calls.Add(1)
		_, _ = w.Write([]byte(`{"isLast":true,"issues":[]}`))
	}))
	t.Cleanup(server.Close)
	path := aliasConfig(t, "[aliases]\nchain='query --limit=1'\n")
	marker := filepath.Join(t.TempDir(), "shell-must-not-run")
	literal := "summary ~ '$(touch " + marker + ") #tag'"
	aliasFrame(t, path, "", "alias", "set", "query", "--", "issue", "list", "--jql", literal)
	if calls.Load() != 0 {
		t.Fatal("storing an alias contacted Jira")
	}
	stdout, stderr, exit := runBinaryEnv(t, []string{"JIRA_BASE_URL=" + server.URL, "JIRA_EMAIL=fixture@example.invalid", "JIRA_API_TOKEN=fixture-token", "JIRA_KEYRING=false", "JIRA_READ_ONLY=true"}, "--config", path, "--output=json", "chain")
	var result frame
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || !result.OK || exit != 0 || result.Meta.Command != "issue.list" || calls.Load() != 1 || query != literal {
		t.Fatalf("alias dispatch lost argv, shared output or transport: exit=%d query=%q stderr=%s", exit, query, stderr)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an alias expansion executed shell source")
	}
}
