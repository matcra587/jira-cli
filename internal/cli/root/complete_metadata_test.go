package root

import (
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	cachecmd "github.com/matcra587/jira-cli/internal/cli/cache"
)

type metadataCompletionCase struct {
	resource, kind, items string
	want                  []string
}

func metadataCompletionCases() []metadataCompletionCase {
	return []metadataCompletionCase{
		{resource: "labels", kind: "cachelabel", items: `["alpha","space label"]`, want: []string{"alpha", "space label"}},
		{resource: "projects", kind: "cacheproject", items: `[{"id":"1","key":"DEMO","name":"Example project"}]`, want: []string{"DEMO\tExample project"}},
		{resource: "epics", kind: "cacheepic", items: `[{"id":"1","key":"DEMO-7","fields":{"summary":"Native epic"}}]`, want: []string{"DEMO-7\tNative epic"}},
		{resource: "fields", kind: "cachefield", items: `[{"id":"customfield_10001","name":"Native field"}]`, want: []string{"customfield_10001\tNative field"}},
		{resource: "issuetypes", kind: "cacheissuetype", items: `[{"id":"1","name":"Task"},{"id":"2","name":"Task"}]`, want: []string{"Task"}},
		{resource: "linktypes", kind: "cachelinktype", items: `[{"id":"1","name":"Blocks","inward":"is blocked by","outward":"blocks"}]`, want: []string{"Blocks\t1 (is blocked by / blocks)"}},
		{resource: "statuses", kind: "cachestatus", items: `[{"id":"1","name":"In Progress"},{"id":"2","name":"In Progress"}]`, want: []string{"In Progress"}},
		{resource: "priorities", kind: "cachepriority", items: `[{"id":"1","name":"High"}]`, want: []string{"High"}},
		{resource: "resolutions", kind: "cacheresolution", items: `[{"id":"1","name":"Won't Do"}]`, want: []string{"Won't Do"}},
		{resource: "boards", kind: "cacheboard", items: "", want: []string{"Example board\t42"}},
		{resource: "boards", kind: "cacheboardid", items: "", want: []string{"42\tExample board"}},
	}
}

func prepareMetadataCompletion(t *testing.T, tc metadataCompletionCase) (string, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("JIRA_BASE_URL", "https://example.com")
	t.Setenv("JIRA_EMAIL", "person@example.com")
	t.Setenv("JIRA_API_TOKEN", "")
	t.Setenv("JIRA_KEYRING", "true")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "/dev/null")
	store, err := newScopedDiskCache("https://example.com", "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	switch tc.resource {
	case "boards":
		raw = []byte(`{"boards":[{"id":42,"name":"Example board","type":null,"project_keys":[],"projects_complete":true}],"fetched_at":` + "\"" + time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano) + "\"" + `,"pages_fetched":1,"complete":true,"truncated":false,"project_failures":[]}`)
	case "linktypes":
		raw = []byte(tc.items)
	default:
		var items []json.RawMessage
		if err := json.Unmarshal([]byte(tc.items), &items); err != nil {
			t.Fatal(err)
		}
		raw, err = json.Marshal(struct {
			Version int `json:"version"`
			cachecmd.MetadataValue
		}{Version: 1, MetadataValue: cachecmd.MetadataValue{Items: items, Count: len(items), Pages: 1, Complete: true}})
		if err != nil {
			t.Fatal(err)
		}
	}
	var key string
	for _, resource := range cachecmd.MetadataResources() {
		if resource.Name == tc.resource {
			key = resource.Key("https://example.com")
		}
	}
	if key == "" {
		t.Fatal("fixture has no owning producer")
	}
	if err := store.Set(key, raw); err != nil {
		t.Fatal(err)
	}
	files := completionFileBytes(t, dir)
	if len(files) != 1 {
		t.Fatalf("fixture files=%v", maps.Keys(files))
	}
	for path := range files {
		return dir, path
	}
	panic("unreachable fixture")
}

func completionFileBytes(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestCachedMetadataCompletionReadsAllProducerShapesWithoutChangingStorage(t *testing.T) {
	for _, tc := range metadataCompletionCases() {
		t.Run(tc.kind, func(t *testing.T) {
			dir, _ := prepareMetadataCompletion(t, tc)
			before := completionFileBytes(t, dir)
			fn := completionKinds[tc.kind]
			if fn == nil {
				t.Fatalf("missing metadata predictor %s", tc.kind)
			}
			if got := fn(nil); !slices.Equal(got, tc.want) {
				t.Fatalf("candidates=%q, want %q", got, tc.want)
			}
			if after := completionFileBytes(t, dir); !maps.Equal(before, after) {
				t.Fatal("completion changed cache bytes")
			}
			t.Setenv("JIRA_EMAIL", "other@example.com")
			if got := fn(nil); len(got) != 0 {
				t.Fatalf("completion crossed account scope: %q", got)
			}
			t.Setenv("JIRA_EMAIL", "person@example.com")
			t.Setenv("JIRA_BASE_URL", "https://other.example.com")
			if got := fn(nil); len(got) != 0 {
				t.Fatalf("completion crossed site scope: %q", got)
			}
			t.Setenv("JIRA_BASE_URL", "https://example.com")
			t.Setenv("JIRA_EMAIL", "")
			if got := fn(nil); len(got) != 0 {
				t.Fatalf("completion recovered identity from credentials: %q", got)
			}
		})
	}
}

func TestCachedMetadataCompletionUsesProducerFreshnessAndExplicitFacts(t *testing.T) {
	for _, tc := range metadataCompletionCases() {
		t.Run(tc.kind, func(t *testing.T) {
			_, path := prepareMetadataCompletion(t, tc)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fn := completionKinds[tc.kind]
			if fn == nil {
				t.Fatalf("missing metadata predictor %s", tc.kind)
			}
			for _, age := range []time.Duration{48 * time.Hour, 365 * 24 * time.Hour, -time.Hour} {
				var wrapper map[string]json.RawMessage
				if err := json.Unmarshal(original, &wrapper); err != nil {
					t.Fatal(err)
				}
				wrapper["written"], err = json.Marshal(time.Now().Add(-age))
				if err != nil {
					t.Fatal(err)
				}
				modified, err := json.Marshal(wrapper)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, modified, 0o600); err != nil {
					t.Fatal(err)
				}
				var ttl time.Duration
				for _, resource := range cachecmd.MetadataResources() {
					if resource.Name == tc.resource {
						ttl = resource.DefaultTTL
					}
				}
				got := fn(nil)
				if age > 0 && age < ttl {
					if !slices.Equal(got, tc.want) {
						t.Fatalf("producer TTL rejected %s age=%s: %q", tc.resource, age, got)
					}
				} else if len(got) != 0 {
					t.Fatalf("stale/future %s age=%s completed: %q", tc.resource, age, got)
				}
			}
			if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := fn(nil); len(got) != 0 {
				t.Fatalf("malformed wrapper completed: %q", got)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if got := fn(nil); len(got) != 0 {
				t.Fatalf("missing wrapper completed: %q", got)
			}
		})
	}
}

func TestCachedMetadataCompletionDropsUnsafeIdentitiesAndSanitizesDescriptions(t *testing.T) {
	cases := []metadataCompletionCase{
		{resource: "labels", kind: "cachelabel", items: `["safe","bad\nline","bad\tcolumn","bad\u001b[31mvalue"]`, want: []string{"safe"}},
		{resource: "fields", kind: "cachefield", items: `[{"id":"safe","name":"Native\nfield\tname"},{"id":"bad\tidentity","name":"Bad"}]`, want: []string{"safe\tNativefieldname"}},
		{resource: "projects", kind: "cacheproject", items: `[{"id":"1","key":"DEMO","name":"Safe"},{"id":"2","key":"BAD\rKEY","name":"Bad"}]`, want: []string{"DEMO\tSafe"}},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			prepareMetadataCompletion(t, tc)
			fn := completionKinds[tc.kind]
			if fn == nil {
				t.Fatalf("missing metadata predictor %s", tc.kind)
			}
			got := fn(nil)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("unsafe candidates=%q want=%q", got, tc.want)
			}
			for _, candidate := range got {
				if strings.ContainsAny(candidate, "\n\r\x1b") {
					t.Fatalf("unsafe completion line=%q", candidate)
				}
			}
		})
	}
}

func rewriteCompletionPayload(t *testing.T, path string, edit func(map[string]json.RawMessage)) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := json.Unmarshal(wrapper["value"], &payload); err != nil {
		t.Fatal(err)
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	edit(value)
	payload, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	wrapper["value"], err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCachedMetadataCompletionPreservesKnownPartialValuesAndRejectsInventedFacts(t *testing.T) {
	for _, tc := range metadataCompletionCases() {
		if tc.resource == "linktypes" {
			continue
		}
		t.Run(tc.kind, func(t *testing.T) {
			dir, path := prepareMetadataCompletion(t, tc)
			fn := completionKinds[tc.kind]
			rewriteCompletionPayload(t, path, func(value map[string]json.RawMessage) {
				value["complete"] = json.RawMessage("false")
				switch tc.resource {
				case "epics":
					value["truncated_reason"] = json.RawMessage(`"max_pages"`)
					value["next_page_token"] = json.RawMessage(`"opaque"`)
				case "boards":
					value["truncated"] = json.RawMessage("true")
					value["reason"] = json.RawMessage(`"max_results"`)
					value["next_start"] = json.RawMessage("1")
				default:
					value["truncated_reason"] = json.RawMessage(`"max_results"`)
					value["next_start"] = value["count"]
				}
			})
			before := completionFileBytes(t, dir)
			got := fn(nil)
			if slices.Contains([]string{"fields", "issuetypes", "statuses"}, tc.resource) {
				if len(got) != 0 {
					t.Fatalf("impossible array page completed: %q", got)
				}
			} else if !slices.Equal(got, tc.want) {
				t.Fatalf("bounded known values lost: %q want=%q", got, tc.want)
			}
			if !maps.Equal(before, completionFileBytes(t, dir)) {
				t.Fatal("partial completion changed storage")
			}
			rewriteCompletionPayload(t, path, func(value map[string]json.RawMessage) { delete(value, "complete") })
			if got := fn(nil); len(got) != 0 {
				t.Fatalf("missing completion fact trusted: %q", got)
			}
		})
	}
}

func TestCachedBoardCompletionNeverOffersLocallyAmbiguousNames(t *testing.T) {
	tc := metadataCompletionCase{resource: "boards", kind: "cacheboard"}
	_, path := prepareMetadataCompletion(t, tc)
	rewriteCompletionPayload(t, path, func(value map[string]json.RawMessage) {
		value["boards"] = json.RawMessage(`[{"id":42,"name":"Σ board","project_keys":[],"projects_complete":true},{"id":43,"name":"ς board","project_keys":[],"projects_complete":true},{"id":44,"name":"Unique board","project_keys":[],"projects_complete":true}]`)
	})
	if got := completionKinds["cacheboard"](nil); !slices.Equal(got, []string{"Unique board\t44"}) {
		t.Fatalf("ambiguous names completed: %q", got)
	}
	if got := completionKinds["cacheboardid"](nil); !slices.Equal(got, []string{"42\tΣ board", "43\tς board", "44\tUnique board"}) {
		t.Fatalf("unambiguous numeric choices lost: %q", got)
	}
}

func TestCachedMetadataCompletionNeverCreatesStorageOnMissingOrInvalidScope(t *testing.T) {
	for _, name := range []string{"valid", "missing email", "invalid site", "broken config"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			dir := filepath.Join(t.TempDir(), "absent-cache")
			t.Setenv("XDG_CACHE_HOME", dir)
			t.Setenv("JIRA_BASE_URL", "https://example.com")
			t.Setenv("JIRA_EMAIL", "person@example.com")
			t.Setenv("JIRA_API_TOKEN", "")
			t.Setenv("JIRA_KEYRING", "true")
			t.Setenv("DBUS_SESSION_BUS_ADDRESS", "/dev/null")
			var args []string
			switch name {
			case "missing email":
				t.Setenv("JIRA_EMAIL", "")
			case "invalid site":
				t.Setenv("JIRA_BASE_URL", "invalid")
			case "broken config":
				path := filepath.Join(t.TempDir(), "bad.toml")
				if err := os.WriteFile(path, []byte("["), 0o600); err != nil {
					t.Fatal(err)
				}
				args = []string{"--config", path}
			}
			for _, tc := range metadataCompletionCases() {
				if got := completionKinds[tc.kind](args); len(got) != 0 {
					t.Fatalf("unavailable metadata completed: %s %q", tc.kind, got)
				}
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("completion created storage: %v", err)
			}
		})
	}
}

func TestCachedMetadataCompletionIgnoresUnrelatedNativeExtensions(t *testing.T) {
	for _, tc := range metadataCompletionCases() {
		if tc.resource == "labels" || tc.resource == "epics" || tc.resource == "boards" {
			continue
		}
		t.Run(tc.kind, func(t *testing.T) {
			var items []map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.items), &items); err != nil {
				t.Fatal(err)
			}
			for _, item := range items {
				item["fields"] = json.RawMessage("17")
				item["extension"] = json.RawMessage(`{"unknown":null}`)
			}
			raw, err := json.Marshal(items)
			if err != nil {
				t.Fatal(err)
			}
			tc.items = string(raw)
			prepareMetadataCompletion(t, tc)
			if got := completionKinds[tc.kind](nil); !slices.Equal(got, tc.want) {
				t.Fatalf("unrelated native extension suppressed valid identities: %q want=%q", got, tc.want)
			}
		})
	}
}

func TestCachedMetadataCompletionSelectsForwardedConfigScope(t *testing.T) {
	for _, tc := range metadataCompletionCases() {
		t.Run(tc.kind, func(t *testing.T) {
			dir, _ := prepareMetadataCompletion(t, tc)
			path := filepath.Join(t.TempDir(), "selected.toml")
			if err := os.WriteFile(path, []byte("base_url = 'https://example.com'\nemail = 'person@example.com'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			// Remove environment identity so the selected file must supply it.
			for _, name := range []string{"JIRA_BASE_URL", "JIRA_EMAIL"} {
				if err := os.Unsetenv(name); err != nil {
					t.Fatal(err)
				}
			}
			before := completionFileBytes(t, dir)
			fn := completionKinds[tc.kind]
			if got := fn(nil); len(got) != 0 {
				t.Fatalf("unselected scope completed: %q", got)
			}
			for _, args := range [][]string{{"--config", path}, {"--config=" + path}} {
				if got := fn(args); !slices.Equal(got, tc.want) {
					t.Fatalf("forwarded config %q selected candidates=%q want=%q", args, got, tc.want)
				}
			}
			if !maps.Equal(before, completionFileBytes(t, dir)) {
				t.Fatal("config-selected completion changed storage")
			}
		})
	}
}

func TestCachedBoardCompletionDescribesObservedAndPartialProjects(t *testing.T) {
	_, path := prepareMetadataCompletion(t, metadataCompletionCase{resource: "boards", kind: "cacheboard"})
	rewriteCompletionPayload(t, path, func(value map[string]json.RawMessage) {
		value["boards"] = json.RawMessage(`[{"id":42,"name":"Complete board","type":"scrum","project_keys":["EX","OPS"],"projects_complete":true},{"id":43,"name":"Partial board","type":"kanban","project_keys":["EX"],"projects_complete":false,"projects_next_start":1}]`)
		value["project_failures"] = json.RawMessage(`[{"board_id":43,"error":{"code":"jira_api_error"}}]`)
	})
	for _, tc := range []struct {
		kind string
		want []string
	}{
		{kind: "cacheboard", want: []string{"Complete board\t42 (scrum, EX, OPS)", "Partial board\t43 (kanban, EX) [projects incomplete]"}},
		{kind: "cacheboardid", want: []string{"42\tComplete board (scrum, EX, OPS)", "43\tPartial board (kanban, EX) [projects incomplete]"}},
	} {
		if got := completionKinds[tc.kind](nil); !slices.Equal(got, tc.want) {
			t.Fatalf("%s descriptions=%q want=%q", tc.kind, got, tc.want)
		}
	}
}
