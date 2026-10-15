//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

const parityCacheStamp = "2026-01-01T00:00:00Z"

func parityCachePublicConfig(t *testing.T, home, lane, site string) string {
	t.Helper()
	name, public := "public.toml", "theme='dark'\n"
	if lane == "before" {
		name = "legacy.toml"
		public = fmt.Sprintf("default_profile='fixture'\n[[profiles]]\nname='fixture'\nbase_url=%q\nemail='fixture@example.invalid'\nsecret_backend='env'\n", site)
	}
	path := filepath.Join(home, name)
	if err := os.WriteFile(path, []byte(public), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Fixtures are literal persisted consumer inputs, never production cache writes.
func parityCacheSeed(t *testing.T, home, lane, site, resource string, payload json.RawMessage) string {
	t.Helper()
	var path string
	var wrapper map[string]any
	if lane == "before" {
		profile, selected := parityMetadataLegacyLocation(home, site, resource)
		path = selected
		wrapper = map[string]any{"profile": profile, "resource": resource, "schema": 1, "fetched_at": parityCacheStamp, "data": payload}
	} else {
		identity := fmt.Sprintf("%d:%s%d:%s", len(site), site, len("fixture@example.invalid"), "fixture@example.invalid")
		namespace := fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))
		logical := "v1:metadata:" + resource
		if resource == "issuekeys" {
			logical = "v1:issuekeys"
		}
		key := "scope-v2:" + namespace + ":" + logical
		path = filepath.Join(home, "jira", "scope-v2-"+namespace, fmt.Sprintf("%x.json", sha256.Sum256([]byte(key))))
		if resource != "issuekeys" {
			payload = []byte(fmt.Sprintf(`{"version":1,"items":%s,"count":1,"pages":1,"complete":true}`, payload))
		}
		wrapper = map[string]any{"key": key, "written": parityCacheStamp, "value": []byte(payload)}
	}
	raw, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func parityCacheLocalData(t *testing.T, result parityFixtureResult, lane, mode, operation string, want map[string]any) {
	t.Helper()
	if result.Exit != 0 || len(result.Stderr) != 0 {
		t.Fatalf("local cache command failed: exit %d; stdout %s; stderr %s", result.Exit, result.Stdout, result.Stderr)
	}
	data := result.Stdout
	if mode == "json" {
		var err error
		data, err = json.Marshal(parityAuthoringCleanData(t, data, lane, operation))
		if err != nil {
			t.Fatal(err)
		}
	}
	expected, err := json.Marshal(want)
	if err != nil || !jsontext.Value(data).IsValid() || !parityAuthoringJSON(data, expected) {
		t.Fatalf("local cache receipt differs from independent storage expectation: %s", result.Stdout)
	}
}

func parityCacheClearRefusal(stdout []byte, lane string, exit int, force bool) bool {
	code, message := "arg_value_invalid", "unknown cache resource"
	if force {
		code, message = "flag_required", "cache clear requires --force in headless, agent or --no-input mode"
	}
	if lane == "before" {
		message = `unknown cache resource "unknown"; valid resources: labels, projects, epics, fields, issuetypes, linktypes, boards, statuses, priorities, resolutions, issuekeys`
		if force {
			code, message = "validation_failed", "cache clear requires --force in headless / agent / --no-input mode"
		}
	}
	if !parityLocalRefusal(stdout, lane, "cache.clear", exit, code) {
		return false
	}
	var frame struct{ Errors []struct{ Message string } }
	return json.Unmarshal(stdout, &frame) == nil && len(frame.Errors) == 1 && frame.Errors[0].Message == message
}

func TestParityActualBinaryLocalCacheHistory(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				t.Error("local history entered HTTP")
				http.Error(w, "unexpected request", http.StatusTeapot)
			}))
			defer server.Close()
			for _, sample := range []string{"missing", "present", "duplicates"} {
				for _, mode := range []string{"json", "compact"} {
					t.Run(sample+"/"+mode, func(t *testing.T) {
						home := t.TempDir()
						config := parityCachePublicConfig(t, home, lane.name, server.URL)
						keys := []string{}
						switch sample {
						case "present":
							keys = []string{"PAIR-2", "PAIR-10"}
						case "duplicates":
							keys = []string{"PAIR-2", "PAIR-2"}
						}
						if sample != "missing" {
							raw, err := json.Marshal(keys)
							if err != nil {
								t.Fatal(err)
							}
							parityCacheSeed(t, home, lane.name, server.URL, "issuekeys", raw)
						}
						before := parityLocalTree(t, home)
						if err := parityFingerprint(lane.binary, lane.digest); err != nil {
							t.Fatal(err)
						}
						result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_READ_ONLY=true"}, "cache", "issuekeys")
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						state := sample
						if sample == "duplicates" && lane.name == "after" {
							keys = []string{}
							state = "malformed"
						}
						want := map[string]any{"issue_keys": keys, "count": len(keys)}
						if lane.name == "before" {
							want["profile"] = "fixture"
						} else {
							want["cache_state"] = state
							if sample == "present" {
								want["written_at"] = parityCacheStamp
							}
						}
						parityCacheLocalData(t, result, lane.name, mode, "cache.issuekeys", want)
						if calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
							t.Fatal("local cache history read changed owned files or entered HTTP")
						}
						id := "C032.history." + sample + "." + mode
						evidence.Checks[id] = append(evidence.Checks[id], lane.name)
					})
				}
			}
		})
	}
	saveParityLocal(t, "cache-history", evidence)
}

func TestParityActualBinaryLocalCacheClear(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				t.Error("local cache clear entered HTTP")
				http.Error(w, "unexpected request", http.StatusTeapot)
			}))
			defer server.Close()
			for _, populated := range []bool{false, true} {
				for _, tc := range []struct {
					name, resource string
					dry, force     bool
					code           string
				}{
					{"preview-all", "", true, false, ""},
					{"preview-fields", "fields", true, false, ""},
					{"clear-all", "", false, true, ""},
					{"clear-fields", "fields", false, true, ""},
					{"missing-force", "fields", false, false, "force"},
					{"unknown-resource", "unknown", true, false, "arg_value_invalid"},
				} {
					for _, mode := range []string{"json", "compact"} {
						t.Run(fmt.Sprintf("populated=%t/%s/%s", populated, tc.name, mode), func(t *testing.T) {
							home := t.TempDir()
							config := parityCachePublicConfig(t, home, lane.name, server.URL)
							parityCacheSeed(t, home, lane.name, "https://other.invalid", "fields", json.RawMessage(`[{"id":"summary","name":"Foreign"}]`))
							selected := map[string]string{}
							marker := ""
							if populated {
								selected["fields"] = parityCacheSeed(t, home, lane.name, server.URL, "fields", json.RawMessage(`[{"id":"summary","name":"Summary"}]`))
								selected["issuekeys"] = parityCacheSeed(t, home, lane.name, server.URL, "issuekeys", json.RawMessage(`["PAIR-2","PAIR-10"]`))
								marker = filepath.Join(filepath.Dir(selected["fields"]), "owned-unrelated.txt")
								if err := os.WriteFile(marker, []byte("owned unrelated preimage"), 0o600); err != nil {
									t.Fatal(err)
								}
							}
							before := parityLocalTree(t, home)
							args := []string{"cache", "clear"}
							if tc.resource != "" {
								args = append(args, tc.resource)
							}
							if tc.dry {
								args = append(args, "--dry-run")
							}
							if tc.force {
								args = append(args, "--force")
							}
							if err := parityFingerprint(lane.binary, lane.digest); err != nil {
								t.Fatal(err)
							}
							result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_READ_ONLY=true"}, args...)
							evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
							if tc.code != "" {
								if !parityCacheClearRefusal(result.Stdout, lane.name, result.Exit, tc.code == "force") {
									t.Fatalf("local clear refusal changed: exit %d; stdout %s; stderr %s", result.Exit, result.Stdout, result.Stderr)
								}
							} else {
								targets := map[string]string{}
								for resource, path := range selected {
									if tc.resource == "" || tc.resource == resource {
										targets[resource] = path
									}
								}
								want := map[string]any{"dry_run": tc.dry}
								if lane.name == "before" {
									want["profile"] = "fixture"
									if tc.resource != "" {
										want["resource"], want["removed"] = tc.resource, len(targets) > 0
									} else {
										n := len(targets)
										if populated {
											n++
										}
										want["removed"] = n
									}
								} else {
									resource := tc.resource
									if resource == "" {
										resource = "all"
									}
									entries := []map[string]any{}
									for name, path := range targets {
										entries = append(entries, map[string]any{"id": filepath.Base(path), "resource": name, "attempted": !tc.dry, "removed": !tc.dry})
									}
									sort.Slice(entries, func(i, j int) bool { return entries[i]["id"].(string) < entries[j]["id"].(string) })
									removed := len(entries)
									if tc.dry {
										removed = 0
									}
									want["resource"], want["would_remove"], want["removed"], want["unclassified"], want["entries"] = resource, len(entries), removed, 0, entries
								}
								parityCacheLocalData(t, result, lane.name, mode, "cache.clear", want)
								if !tc.dry {
									for _, path := range targets {
										relative, err := filepath.Rel(home, path)
										if err != nil {
											t.Fatal(err)
										}
										delete(before, relative)
									}
									if lane.name == "before" && tc.resource == "" && marker != "" {
										relative, err := filepath.Rel(home, marker)
										if err != nil {
											t.Fatal(err)
										}
										delete(before, relative)
									}
								}
							}
							if calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
								t.Fatal("cache clear changed a foreign scope/unrelated preimage or missed its acknowledged effect")
							}
							id := fmt.Sprintf("C029.clear.populated=%t.%s.%s", populated, tc.name, mode)
							evidence.Checks[id] = append(evidence.Checks[id], lane.name)
						})
					}
				}
			}
		})
	}
	saveParityLocal(t, "cache-clear", evidence)
}

func TestParityActualBinaryEmptyCacheInspection(t *testing.T) {
	binary, digest := os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")
	if binary == "" {
		t.Skip("requires selected actual binary")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Error("local inspection entered HTTP")
		http.Error(w, "unexpected request", http.StatusTeapot)
	}))
	defer server.Close()
	for _, resource := range []string{"fields", "issuekeys"} {
		for _, mode := range []string{"json", "compact", "human"} {
			t.Run(resource+"/"+mode, func(t *testing.T) {
				home := t.TempDir()
				config := parityCachePublicConfig(t, home, "after", server.URL)
				before := parityLocalTree(t, home)
				if err := parityFingerprint(binary, digest); err != nil {
					t.Fatal(err)
				}
				result := parityFixtureProcessAt(t, "after", binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_READ_ONLY=true"}, "cache", "inspect", resource)
				evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: "after", Digest: digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
				if mode == "human" {
					want := fmt.Sprintf("cache scope  %s  fixture@example.invalid\n0  cached entries for  %s  with  0  known payload bytes\n%s  missing  0  cached items\n", server.URL, resource, resource)
					if result.Exit != 0 || len(result.Stderr) != 0 || string(result.Stdout) != want {
						t.Fatalf("empty inspection lost meaningful human facts: exit %d; stdout %s; stderr %s", result.Exit, result.Stdout, result.Stderr)
					}
				} else {
					var ttl any
					if resource == "fields" {
						ttl = 20160
					}
					want := map[string]any{"base_url": server.URL, "email": "fixture@example.invalid", "resource": resource, "complete": true, "count": 0, "known_payload_bytes": 0, "unclassified": 0, "entries": []any{}, "resources": []map[string]any{{"resource": resource, "cache_state": "missing", "ttl_minutes": ttl, "count": 0, "pages": 0, "complete": false, "age_seconds": 0}}}
					parityCacheLocalData(t, result, "after", mode, "cache.inspect", want)
				}
				if calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
					t.Fatal("empty inspection entered HTTP or created/changed owned storage")
				}
				evidence.Checks["C089.inspect.empty."+resource+"."+mode] = []string{"after"}
			})
		}
	}
	saveParityLocal(t, "cache-inspect", evidence)
}

func TestParityCacheClearRefusalRejectsWrongReasonOrEffects(t *testing.T) {
	for _, lane := range []string{"before", "after"} {
		t.Run(lane, func(t *testing.T) {
			code, message := "flag_required", "cache clear requires --force in headless, agent or --no-input mode"
			if lane == "before" {
				code, message = "validation_failed", "cache clear requires --force in headless / agent / --no-input mode"
			}
			valid := fmt.Sprintf(`{"ok":false,"meta":{"command":"cache.clear","exit_code":3,"contract_version":"1","timestamp":"2026-01-01T00:00:00Z","request_id":"owned-fixture"},"data":null,"errors":[{"code":%q,"message":%q}],"warnings":[]}`, code, message)
			if lane == "before" {
				valid = strings.Replace(valid, `"contract_version":"1",`, "", 1)
			}
			if !parityCacheClearRefusal([]byte(valid), lane, 3, true) {
				t.Fatal("independent local refusal fixture refused")
			}
			for _, tc := range []struct{ name, original, replacement string }{
				{"wrong-reason", message, "unsupported configuration"},
				{"partial-clear", `"data":null`, `"data":{"removed":1}`},
				{"wrong-code", code, "auth_missing"},
				{"null-warnings", `"warnings":[]`, `"warnings":null`},
				{"duplicate", `"data":null`, `"data":{},"data":null`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if parityCacheClearRefusal([]byte(strings.Replace(valid, tc.original, tc.replacement, 1)), lane, 3, true) {
						t.Fatal("wrong refusal reason or partial deletion earned local refusal credit")
					}
				})
			}
		})
	}
}
