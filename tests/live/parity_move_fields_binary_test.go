//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

func parityFieldMoveReadOnly(raw []byte, lane string, keys []string, payload []byte) bool {
	if !jsontext.Value(raw).IsValid() || len(keys) == 0 || (lane != "before" && lane != "after") {
		return false
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return false
	}
	errors := []any{parityEpicReadOnlyError(lane, keys[0], true)}
	if lane == "before" {
		errors = make([]any, 0, len(keys))
		for _, key := range keys {
			errors = append(errors, parityEpicReadOnlyError(lane, key, true))
		}
	}
	wantErrors, _ := json.Marshal(errors)
	if !parityAuthoringJSON(root["errors"], wantErrors) {
		return false
	}
	if lane == "before" && len(keys) == 1 {
		return bytes.Equal(root["data"], []byte("null"))
	}
	rows := make([]any, 0, len(keys))
	for _, key := range keys {
		row := map[string]any{"issue": map[string]any{"key": key}, "ok": false, "error": parityEpicReadOnlyError(lane, key, false)}
		if lane == "before" {
			delete(row, "issue")
			row["key"] = key
		}
		rows = append(rows, row)
	}
	want := map[string]any{"results": rows, "succeeded": 0, "failed": len(keys)}
	if lane == "after" {
		want = map[string]any{"dry_run": false, "notify": true, "payload": json.RawMessage(payload), "description_route": "adf", "results": rows}
	}
	expected, _ := json.Marshal(want)
	return parityAuthoringJSON(root["data"], expected)
}

func TestParityActualBinaryMoveFields(t *testing.T) {
	lanes := []parityLane{{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}}
	for _, lane := range lanes {
		if lane.Binary == "" {
			t.Skip("requires both selected actual binaries")
		}
		if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
			t.Fatal(err)
		}
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	const payload = `{"fields":{"summary":"Owned field move café <&>","issuetype":{"id":"2"}}}`
	for _, lane := range lanes {
		for _, count := range []int{1, 2} {
			for _, step := range []string{"preview", "confirmation", "read_only", "native"} {
				for _, mode := range []string{"json", "compact"} {
					t.Run(fmt.Sprintf("%s/%d/%s/%s", lane.Name, count, step, mode), func(t *testing.T) {
						keys := []string{"PAIR-1", "PAIR-2"}[:count]
						var mu sync.Mutex
						var transcript []string
						invalid := false
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							mu.Lock()
							defer mu.Unlock()
							transcript = append(transcript, r.Method+" "+r.URL.Path)
							w.Header().Set("Content-Type", "application/json")
							user, password, auth := r.BasicAuth()
							if !auth || user != "fixture@example.invalid" || password != "synthetic-fixture-token" || r.URL.RawQuery != "" {
								invalid = true
								http.Error(w, "unexpected binding or query", http.StatusBadRequest)
								return
							}
							for _, key := range keys {
								if lane.Name == "before" && r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/"+key+"/editmeta" {
									body, err := io.ReadAll(io.LimitReader(r.Body, 1))
									if err != nil || len(body) != 0 {
										invalid = true
										http.Error(w, "metadata request carried a body", http.StatusBadRequest)
										return
									}
									_, _ = io.WriteString(w, `{"fields":{"summary":{"name":"Summary","key":"summary","fieldId":"summary","required":true,"schema":{"type":"string"}}}}`)
									return
								}
								if r.Method == http.MethodPut && r.URL.Path == "/rest/api/3/issue/"+key {
									body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
									if err != nil || len(body) > 4096 || !parityAuthoringJSON(body, []byte(payload)) {
										invalid = true
										http.Error(w, "changed native payload", http.StatusBadRequest)
										return
									}
									w.WriteHeader(http.StatusNoContent)
									return
								}
							}
							invalid = true
							http.Error(w, "unexpected field move route", http.StatusBadRequest)
						}))
						defer server.Close()
						home := t.TempDir()
						config := parityCachePublicConfig(t, home, lane.Name, server.URL)
						input := filepath.Join(home, "native field input.json")
						if err := os.WriteFile(input, []byte(payload), 0o600); err != nil {
							t.Fatal(err)
						}
						args := []string{"issue", "move"}
						operation := "issue.move"
						if lane.Name == "after" {
							args = append(args, "fields")
							operation = "issue.move.fields"
						}
						args = append(args, keys...)
						args = append(args, "--json-input", input, "--parallelism=1")
						if step != "confirmation" {
							args = append(args, "--force")
						}
						var extra []string
						if step == "preview" {
							args = append(args, "--dry-run")
							extra = []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false"}
						}
						if step == "read_only" {
							extra = []string{"JIRA_READ_ONLY=true"}
						}
						result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, extra, args...)
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						wantExit := 0
						if step == "confirmation" || step == "read_only" {
							wantExit = 3
						}
						if result.Exit != wantExit || !jsontext.Value(result.Stdout).IsValid() {
							t.Fatalf("field move failed invocation: exit%d stdout%s stderr%s", result.Exit, result.Stdout, result.Stderr)
						}
						data := result.Stdout
						if mode == "json" || result.Exit != 0 {
							if err := parityValidateFrame(result.Stdout, lane.Name, operation, result.Exit); err != nil {
								t.Fatal(err)
							}
							data = parityOriginalFrameData(result.Stdout)
						}
						switch step {
						case "confirmation":
							if !parityFieldMoveConfirmation(result.Stdout, lane.Name, keys) {
								t.Fatal("missing confirmation fabricated a field-write receipt")
							}
						case "read_only":
							if !parityFieldMoveReadOnly(result.Stdout, lane.Name, keys, []byte(payload)) {
								t.Fatalf("field move read-only receipt changed: %s", result.Stdout)
							}
						default:
							if len(result.Stderr) != 0 || !parityFieldMoveData(data, lane.Name, keys, []byte(payload), step == "preview") {
								t.Fatalf("field move receipt changed: %s", result.Stdout)
							}
						}
						var expected []string
						if step == "native" {
							for _, key := range keys {
								if lane.Name == "before" {
									expected = append(expected, "GET /rest/api/3/issue/"+key+"/editmeta")
								}
								expected = append(expected, "PUT /rest/api/3/issue/"+key)
							}
						}
						if lane.Name == "before" && count == 1 && step == "confirmation" {
							expected = []string{"GET /rest/api/3/issue/PAIR-1/editmeta"}
						}
						mu.Lock()
						observed := slices.Clone(transcript)
						bad := invalid
						mu.Unlock()
						if bad || !slices.Equal(observed, expected) {
							t.Fatalf("field move native transcript changed: got%v want%v", observed, expected)
						}
						retained, err := os.ReadFile(input)
						if err != nil || !bytes.Equal(retained, []byte(payload)) {
							t.Fatal("field move changed its authored input bytes")
						}
						if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
							t.Fatal(err)
						}
						check := fmt.Sprintf("I020.move.fields.wire.%d.%s.%s", count, step, mode)
						evidence.Checks[check] = append(evidence.Checks[check], lane.Name)
					})
				}
			}
		}
	}
	saveParityLocal(t, "move-fields-fixture", evidence)
}
