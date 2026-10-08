package contract

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/adapters"
	metacache "github.com/matcra587/jira-cli/internal/cli/cache"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestCacheInspectBinaryEmptyScopeNeedsNoCredentialsOrStorage(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "absent-cache")
			env := []string{"XDG_CACHE_HOME=" + dir, "JIRA_BASE_URL=https://example.com", "JIRA_EMAIL=person@example.com", "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_READ_ONLY=true", "JIRA_NO_UPDATE_CHECK=1"}
			args := []string{"cache", "inspect"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 {
				t.Fatalf("local inspection exit=%d stdout=%q stderr=%q, want credential-free success", exit, stdout, stderr)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("inspection created storage: %v", err)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "person@example.com") || !strings.Contains(stdout, "https://example.com") || !strings.Contains(strings.Join(strings.Fields(stdout), " "), "0 cached entries") {
					t.Fatalf("human inspection omitted selected scope or empty inventory: %q", stdout)
				}
				return
			}
			var data map[string]any
			if mode == "json" {
				var f frame
				if err := json.Unmarshal([]byte(stdout), &f); err != nil || !f.OK || f.Meta.Command != "cache.inspect" {
					t.Fatalf("inspection frame=%+v err=%v", f, err)
				}
				if err := json.Unmarshal(f.Data, &data); err != nil {
					t.Fatal(err)
				}
			} else if err := json.Unmarshal([]byte(stdout), &data); err != nil {
				t.Fatal(err)
			}
			entries, ok := data["entries"].([]any)
			resources, resourcesOK := data["resources"].([]any)
			if !ok || len(entries) != 0 || !resourcesOK || len(resources) != 12 || data["count"] != float64(0) || data["complete"] != true || data["base_url"] != "https://example.com" || data["email"] != "person@example.com" {
				t.Fatalf("inspection=%v, want selected scope, complete empty inventory and twelve local resource states", data)
			}
			for _, row := range resources {
				if row.(map[string]any)["cache_state"] != "missing" {
					t.Fatalf("empty scope fabricated metadata: %v", row)
				}
			}
		})
	}
}

func TestCacheInspectBinaryValidatesProducerDataAndPreservesAllScopes(t *testing.T) {
	first, second, env, dir := cacheClearFixture(t)
	disk, err := adapters.NewDiskCache()
	if err != nil {
		t.Fatal(err)
	}
	otherSite, err := adapters.NewScopedCache(disk, "https://other.example.com", "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	values := map[string]string{
		"v1:metadata:fields":   `{"version":1,"items":[{"id":"name","name":"Native <field>","unknown":null}],"count":1,"pages":1,"complete":true}`,
		"v1:metadata:labels":   `{"version":1,"items":[],"pages":1,"complete":true}`,
		metacache.IssueKeysKey: `["DEMO-2","DEMO-1"]`,
		metacache.BoardListKey: fmt.Sprintf(`{"boards":[],"fetched_at":%q,"pages_fetched":1,"complete":true,"truncated":false,"project_failures":[]}`, stamp),
		metacache.BoardSelectorPrefix + "https://example.com:id:12": `{"id":12,"name":"Example board","jql":"project = DEMO"}`,
		"future:resource":   "owned unknown value",
		"malformed-wrapper": "owned malformed wrapper",
	}
	for key, raw := range values {
		for _, cache := range []*adapters.ScopedCache{first, second, otherSite} {
			if err := cache.Set(key, []byte(raw)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := disk.Set("legacy-flat", []byte("preserve legacy")); err != nil {
		t.Fatal(err)
	}
	entries, err := first.Entries(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var malformedID string
	for _, entry := range entries {
		if entry.Key == "malformed-wrapper" {
			malformedID = entry.ID
		}
	}
	if malformedID == "" {
		t.Fatal("owned malformed fixture missing")
	}
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == malformedID {
			return os.WriteFile(path, []byte("{"), 0o600)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string]string {
		t.Helper()
		result := map[string]string{}
		if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			value := fmt.Sprintf("%s:%d:%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
			if !entry.IsDir() {
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				value += fmt.Sprintf(":%x", sha256.Sum256(raw))
			}
			result[path] = value
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshot()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			local := append([]string(nil), env...)
			args := []string{"cache", "inspect"}
			if mode == "agent" {
				local = append(local, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, local, args...)
			if exit != 0 {
				t.Fatalf("inspection exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			if mode == "human" {
				plain := strings.Join(strings.Fields(stdout), " ")
				for _, want := range []string{"7 cached entries", "fields fresh 1 cached items", "labels malformed", "issuekeys present 2 cached items", "unclassified entry"} {
					if !strings.Contains(plain, want) {
						t.Fatalf("human inspection missing %q: %q", want, stdout)
					}
				}
				return
			}
			var out envelope.CacheInspectOutput
			if mode == "json" {
				var f frame
				if err := json.Unmarshal([]byte(stdout), &f); err != nil || !f.OK || f.Meta.Command != "cache.inspect" {
					t.Fatalf("inspection frame=%+v err=%v", f, err)
				}
				if err := json.Unmarshal(f.Data, &out); err != nil {
					t.Fatal(err)
				}
			} else if err := json.Unmarshal([]byte(stdout), &out); err != nil {
				t.Fatal(err)
			}
			if !out.Complete || out.Count != 7 || len(out.Entries) != 7 || out.Unclassified != 2 || len(out.Resources) != 12 {
				t.Fatalf("scoped inventory=%+v", out)
			}
			for _, row := range out.Resources {
				switch row.Resource {
				case "fields":
					if row.CacheState != "fresh" || row.Count != 1 || !row.Complete || row.TTLMinutes == nil || *row.TTLMinutes != 20160 {
						t.Fatalf("field freshness=%+v", row)
					}
				case "boards":
					if row.CacheState != "fresh" || row.Count != 0 || !row.Complete {
						t.Fatalf("native board decoder not used: %+v", row)
					}
				case "labels":
					if row.CacheState != "malformed" || row.WrittenAt != "" {
						t.Fatalf("incomplete native facts accepted: %+v", row)
					}
				case "issuekeys":
					if row.CacheState != "present" || row.Count != 2 || row.TTLMinutes != nil {
						t.Fatalf("history acquired a TTL or lost keys: %+v", row)
					}
				default:
					if row.CacheState != "missing" {
						t.Fatalf("unknown resource fabricated metadata: %+v", row)
					}
				}
			}
		})
	}
	stdout, stderr, exit := runBinaryEnv(t, env, "cache", "inspect", "boards", "--output=compact")
	var selected envelope.CacheInspectOutput
	if json.Unmarshal([]byte(stdout), &selected) != nil || exit != 0 || selected.Count != 2 || len(selected.Resources) != 1 || selected.Resources[0].Resource != "boards" {
		t.Fatalf("selected inventory exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	stdout, stderr, exit = runBinaryEnv(t, env, "cache", "inspect", "--output=json", "--jq=.data.email")
	if exit != 0 || strings.TrimSpace(stdout) != "person@example.com" {
		t.Fatalf("inspection jq exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	after := snapshot()
	if len(before) != len(after) {
		t.Fatal("inspection created or removed storage")
	}
	for path, value := range before {
		if after[path] != value {
			t.Fatal("inspection changed cache bytes, mode, size or modification time")
		}
	}
}

func TestCacheInspectBinaryHelpSchemaAndResourceRecovery(t *testing.T) {
	local := []string{"JIRA_BASE_URL=https://example.com", "JIRA_EMAIL=person@example.com", "JIRA_KEYRING=true", "JIRA_API_TOKEN=", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_NO_UPDATE_CHECK=1", "XDG_CACHE_HOME=" + filepath.Join(t.TempDir(), "absent")}
	stdout, stderr, exit := runBinaryEnv(t, local, "cache", "inspect", "--help")
	if exit != 0 || !strings.Contains(stdout, "inspect") || !strings.Contains(stdout, "not locked or atomic") {
		t.Fatalf("inspection help exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	stdout, stderr, exit = runBinaryEnv(t, local, "agent", "schema", "--path=cache inspect", "--output=json")
	if exit != 0 {
		t.Fatalf("inspection schema exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	tree, _ := readSchema(t, stdout, "json")
	if tree.Path != "jira cache inspect" || !tree.ReadOnly || tree.OutputSchema == nil {
		t.Fatal("inspection bypassed the shared read/schema contract")
	}
	props := tree.OutputSchema["properties"].(map[string]any)
	for _, field := range []string{"base_url", "email", "resource", "entries", "resources", "complete", "known_payload_bytes"} {
		if props[field] == nil {
			t.Fatalf("inspection schema omitted %s", field)
		}
	}
	stdout, stderr, exit = runBinaryEnv(t, append(append([]string(nil), local...), "JIRA_EMAIL="), "cache", "inspect", "fieldz", "--output=json")
	var failed struct {
		OK     bool             `json:"ok"`
		Errors []envelope.Error `json:"errors"`
	}
	if err := json.Unmarshal([]byte(stdout), &failed); err != nil || exit != 3 || failed.OK || len(failed.Errors) != 1 || failed.Errors[0].Code != "arg_value_invalid" || len(failed.Errors[0].Suggestions) == 0 {
		t.Fatalf("resource preflight/recovery exit=%d frame=%+v stderr=%q err=%v", exit, failed, stderr, err)
	}
	stdout, stderr, exit = runBinaryEnv(t, local, "cache", "inspect", failed.Errors[0].Suggestions[0], "--output=compact")
	var recovered envelope.CacheInspectOutput
	if json.Unmarshal([]byte(stdout), &recovered) != nil || exit != 0 || recovered.Resource != "fields" || len(recovered.Resources) != 1 || recovered.Resources[0].Resource != "fields" {
		t.Fatalf("offered recovery failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

func TestCacheInspectDistantResourceRecoveryListsAndExecutesAllResources(t *testing.T) {
	local := []string{"JIRA_BASE_URL=https://example.com", "JIRA_EMAIL=person@example.com", "JIRA_KEYRING=true", "JIRA_API_TOKEN=", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_NO_UPDATE_CHECK=1", "XDG_CACHE_HOME=" + filepath.Join(t.TempDir(), "absent")}
	stdout, stderr, exit := runBinaryEnv(t, append(append([]string(nil), local...), "JIRA_EMAIL="), "cache", "inspect", "attachments", "--output=json")
	var failed struct {
		OK     bool             `json:"ok"`
		Errors []envelope.Error `json:"errors"`
	}
	if err := json.Unmarshal([]byte(stdout), &failed); err != nil || exit != 3 || failed.OK || len(failed.Errors) != 1 || failed.Errors[0].Code != "arg_value_invalid" || failed.Errors[0].Field != "RESOURCE" || len(failed.Errors[0].Suggestions) != 0 || !strings.Contains(failed.Errors[0].Hint, "--help") {
		t.Fatalf("distant resource recovery exit=%d stdout=%q stderr=%q err=%v", exit, stdout, stderr, err)
	}
	stdout, stderr, exit = runBinaryEnv(t, local, "cache", "inspect", "--help", "--output=human")
	want := "Accepted resources: " + strings.Join(metacache.ClearResourceNames(), ", ")
	if exit != 0 || !strings.Contains(strings.Join(strings.Fields(stdout), " "), want) {
		t.Fatalf("help does not provide complete accepted resource vocabulary: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	for _, name := range metacache.ClearResourceNames() {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, exit := runBinaryEnv(t, local, "cache", "inspect", name, "--output=compact")
			var recovered envelope.CacheInspectOutput
			if err := json.Unmarshal([]byte(stdout), &recovered); err != nil || exit != 0 || !recovered.Complete || recovered.Resource != name || len(recovered.Resources) != 1 || recovered.Resources[0].Resource != name {
				t.Fatalf("documented resource invocation failed: exit=%d stdout=%q stderr=%q err=%v", exit, stdout, stderr, err)
			}
		})
	}
}
