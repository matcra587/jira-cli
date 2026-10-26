package contract

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestCacheRefreshBinaryOfflinePreviewPreservesResourceOrder(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "cold-cache")
			env := []string{"XDG_CACHE_HOME=" + dir, "JIRA_BASE_URL=https://example.com", "JIRA_EMAIL=person@example.com", "JIRA_API_TOKEN=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "JIRA_READ_ONLY=true", "JIRA_NO_UPDATE_CHECK=1"}
			args := []string{"cache", "refresh", "fields", "labels", "fields", "--dry-run", "--parallelism=2", "--ttl-minutes=7", "--force", "--unbounded"}
			if mode == "agent" {
				env = append(env, "AGENT=1")
			} else {
				args = append(args, "--output="+mode)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != 0 {
				t.Fatalf("cache refresh preview(%s) exit=%d stdout=%q stderr=%q, want 0 without credentials", mode, exit, stdout, stderr)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("cache refresh preview created storage: %v", err)
			}
			if mode == "human" {
				text := strings.Join(strings.Fields(stdout), " ")
				if !strings.Contains(text, "would fetch fields") || !strings.Contains(text, "would fetch labels") || strings.Index(text, "fields") > strings.Index(text, "labels") {
					t.Fatalf("cache refresh preview human output=%q, want ordered prospective rows", text)
				}
				return
			}
			var data map[string]any
			if mode == "json" {
				var result frame
				if err := json.Unmarshal([]byte(stdout), &result); err != nil || !result.OK || result.Meta.Command != "cache.refresh" {
					t.Fatalf("cache refresh preview frame=%+v err=%v", result, err)
				}
				if err := json.Unmarshal(result.Data, &data); err != nil {
					t.Fatal(err)
				}
			} else if err := json.Unmarshal([]byte(stdout), &data); err != nil {
				t.Fatal(err)
			}
			rows, ok := data["results"].([]any)
			if !ok || len(rows) != 2 || data["dry_run"] != true || data["parallelism"] != float64(2) {
				t.Fatalf("cache refresh preview data=%v, want two deduplicated rows", data)
			}
			var names []string
			for _, value := range rows {
				row := value.(map[string]any)
				name, _ := row["resource"].(string)
				names = append(names, name)
				item, _ := row["data"].(map[string]any)
				for _, field := range []string{"ttl_minutes", "force", "unbounded", "age_seconds"} {
					if _, exists := item[field]; exists {
						t.Fatalf("cache receipt repeats request or derived field %q: %v", field, item)
					}
				}
				if _, exists := row["issue"]; exists || row["ok"] != true || item["resource"] != name || item["dry_run"] != true || item["would_fetch"] != true || item["attempted"] != false || item["saved"] != false || item["from_cache"] != false || item["cache_state"] != "missing" || item["complete"] != false || item["pages"] != float64(0) || item["count"] != float64(0) {
					t.Fatalf("cache refresh preview row=%v, want resource identity and truthful offline plan", row)
				}
			}
			if !reflect.DeepEqual(names, []string{"fields", "labels"}) {
				t.Fatalf("cache refresh preview names=%v, want [fields labels]", names)
			}
		})
	}
}

func TestCacheRefreshBinaryFailuresRetainEveryResourceWithoutInventingFetches(t *testing.T) {
	t.Parallel()
	for _, total := range []bool{false, true} {
		name := "mixed"
		if total {
			name = "total"
		}
		for _, mode := range []string{"json", "compact", "human", "agent"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/rest/api/3/field" {
						_, _ = w.Write([]byte(`{}`))
						return
					}
					if total {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					_, _ = w.Write([]byte(`[{"id":"1","name":"Open","nativeUnknown":null}]`))
				}))
				defer server.Close()
				env := append(editReceiptEnv(server.URL), "XDG_CACHE_HOME="+t.TempDir(), "JIRA_READ_ONLY=true")
				args := []string{"cache", "refresh", "fields", "statuses", "--parallelism=2"}
				if mode == "agent" {
					env = append(env, "AGENT=1")
				} else {
					args = append(args, "--output="+mode)
				}
				stdout, stderr, exit := runBinaryEnv(t, env, args...)
				wantExit := 0
				if total {
					wantExit = 1
				}
				if exit != wantExit {
					t.Fatalf("refresh %s(%s) exit=%d stdout=%q stderr=%q, want %d", name, mode, exit, stdout, stderr, wantExit)
				}
				if mode == "human" {
					text := strings.Join(strings.Fields(stdout), " ")
					if !strings.Contains(text, "failed fields") || strings.Contains(text, "fetched 0 fields") || strings.Contains(text, "local storage is available") {
						t.Fatalf("refresh %s human=%q, want actual failed read without invented fetch or storage cause", name, text)
					}
					return
				}
				var out envelope.CacheRefreshOutput
				if mode == "json" || total {
					var frame apiFrame
					if err := json.Unmarshal([]byte(stdout), &frame); err != nil || frame.OK == total {
						t.Fatalf("refresh %s frame=%+v err=%v", name, frame, err)
					}
					if total && (len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_unauthorized") {
						t.Fatalf("refresh total error=%+v, want severe credential cause", frame.Errors)
					}
					if err := json.Unmarshal(frame.Data, &out); err != nil {
						t.Fatal(err)
					}
				} else if err := json.Unmarshal([]byte(stdout), &out); err != nil {
					t.Fatal(err)
				}
				if len(out.Results) != 2 || out.Results[0].Resource != "fields" || out.Results[0].OK || out.Results[0].Err == nil || out.Results[0].Err.Code != "cache_response_invalid" || out.Results[0].Data == nil || !out.Results[0].Data.Attempted || out.Results[0].Data.Saved {
					t.Fatalf("refresh %s rows=%+v, want retained failed fields", name, out.Results)
				}
				second := out.Results[1]
				if second.Resource != "statuses" || second.OK == total || second.Data == nil || second.Data.Saved == total {
					t.Fatalf("refresh %s sibling=%+v", name, second)
				}
			})
		}
	}
}
