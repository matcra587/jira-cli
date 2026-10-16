//go:build live

package live

import (
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Target acceptance selects exact native member names. Unknown data remains
// outside this narrow oracle, but aliases of the observed members cannot win.
func parityTargetMember(object map[string]json.RawMessage, name string) ([]byte, bool) {
	for key := range object {
		if key != name && strings.EqualFold(key, name) {
			return nil, false
		}
	}
	raw, found := object[name]
	return raw, found
}

func parityTargetRows(raw []byte, want []string, preview bool) bool {
	if !jsontext.Value(raw).IsValid() {
		return false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return false
	}
	if preview {
		value, found := parityTargetMember(object, "dry_run")
		if !found || string(value) != "true" {
			return false
		}
	}
	value, found := parityTargetMember(object, "results")
	var rows []map[string]json.RawMessage
	if !found || json.Unmarshal(value, &rows) != nil || rows == nil || len(rows) != len(want) {
		return false
	}
	for i, row := range rows {
		value, found = parityTargetMember(row, "ok")
		if !found || string(value) != "true" {
			return false
		}
		value, found = parityTargetMember(row, "issue")
		var issue map[string]json.RawMessage
		if !found || json.Unmarshal(value, &issue) != nil || issue == nil {
			return false
		}
		value, found = parityTargetMember(issue, "key")
		var key string
		if !found || json.Unmarshal(value, &key) != nil || key != want[i] {
			return false
		}
	}
	return true
}

func TestParityTargetRowsRequiresExactOrderedIdentity(t *testing.T) {
	const valid = `{"dry_run":true,"results":[{"issue":{"key":"PAIR-2"},"ok":true},{"issue":{"key":"PAIR-1"},"ok":true}]}`
	want := []string{"PAIR-2", "PAIR-1"}
	if !parityTargetRows([]byte(valid), want, true) || !parityTargetRows([]byte(valid), want, false) {
		t.Fatal("independent target control was refused")
	}
	for _, tc := range []struct{ name, raw string }{
		{"duplicate-root", strings.Replace(valid, `"dry_run":true`, `"dry_run":false,"dry_run":true`, 1)},
		{"escaped-duplicate", strings.Replace(valid, `"key":"PAIR-2"`, `"key":"wrong","\u006bey":"PAIR-2"`, 1)},
		{"duplicate-row", strings.Replace(valid, `"ok":true`, `"ok":false,"ok":true`, 1)},
		{"wrong-case-results", strings.Replace(valid, `"results"`, `"Results"`, 1)},
		{"wrong-case-issue", strings.Replace(valid, `"issue"`, `"Issue"`, 1)},
		{"wrong-case-key", strings.Replace(valid, `"key"`, `"Key"`, 1)},
		{"case-alias", strings.Replace(valid, `"ok":true`, `"ok":true,"OK":true`, 1)},
		{"null-rows", `{"dry_run":true,"results":null}`},
		{"null-issue", strings.Replace(valid, `{"key":"PAIR-2"}`, `null`, 1)},
		{"false-acknowledgement", strings.Replace(valid, `"ok":true`, `"ok":false`, 1)},
		{"wrong-order", strings.ReplaceAll(valid, `PAIR-2`, `PAIR-1`)},
		{"trailing-object", valid + `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if parityTargetRows([]byte(tc.raw), want, true) || parityTargetRows([]byte(tc.raw), want, false) {
				t.Fatal("malformed identity earned target acceptance")
			}
		})
	}
}

// A repeated target must create one comment. Distinct native IDs on repeated
// POSTs ensure a duplicate cannot masquerade as a harmless repeated receipt.
func TestParityActualBinaryCommentDuplicateTargets(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []parityLane{{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		if lane.Binary == "" {
			t.Skip("requires both selected actual binaries")
		}
		if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"json", "compact"} {
			for _, tc := range []struct {
				name string
				keys []string
			}{{"repeated", []string{"PAIR-1", "PAIR-1"}}, {"csv", []string{"PAIR-1,PAIR-1"}}} {
				t.Run(lane.Name+"/"+mode+"/"+tc.name, func(t *testing.T) {
					var calls, posts atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
						user, token, authenticated := r.BasicAuth()
						w.Header().Set("Content-Type", "application/json")
						if r.Method != "POST" || r.URL.Path != "/rest/api/3/issue/PAIR-1/comment" {
							w.WriteHeader(http.StatusNotFound)
							_, _ = io.WriteString(w, `{"errorMessages":["No issue matches the literal target"]}`)
							return
						}
						if err != nil || len(body) > 8192 || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" || !authenticated || user != "fixture@example.invalid" || token != "synthetic-fixture-token" || !parityAuthoringJSON(body, []byte(`{"body":`+parityAuthoringDocument+`,"visibility":{"type":"role","value":"Owned role <&> café"}}`)) {
							t.Error("comment target control changed its exact authenticated native write")
							http.Error(w, "wrong synthetic request", http.StatusBadRequest)
							return
						}
						id := strconv.Itoa(500 + int(posts.Add(1)))
						w.WriteHeader(http.StatusCreated)
						if err := json.NewEncoder(w).Encode(map[string]any{"id": id, "body": json.RawMessage(parityAuthoringDocument), "visibility": map[string]any{"type": "role", "value": "Owned role <&> café"}, "author": map[string]any{"accountId": "owned-author", "displayName": "Owned author <&>"}, "created": "2026-01-02T12:30:00.000+0000", "updated": "2026-01-03T12:30:00.000+0000"}); err != nil {
							t.Error(err)
						}
					}))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.Name, server.URL)
					before, began := parityLocalTree(t, home), time.Now()
					args := append([]string{"issue", "comment", "add"}, tc.keys...)
					args = append(args, "--markdown=**Owned café**", "--visibility-role=Owned role <&> café", "--parallelism=1")
					result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_READ_ONLY=false"}, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != 1 || posts.Load() != 1 {
						t.Fatalf("duplicate target failed exactly-once native creation: exit %d; HTTP %d; created %d; stdout %s; stderr %s", result.Exit, calls.Load(), posts.Load(), result.Stdout, result.Stderr)
					}
					data := result.Stdout
					if mode == "json" {
						parityAuthoringCleanData(t, data, lane.Name, "issue.comment.add")
						data = parityOriginalFrameData(data)
					}
					if !parityAuthoringJSON(data, parityCommentFlagsExpected(t, lane.Name, "add", "role", mode, false, true)) {
						t.Fatal("duplicate target lost the independent single-comment acknowledgement")
					}
					if lane.Name == "after" {
						history := parityOpenHistory(t, home, lane.Name, server.URL, "PAIR-1", began)
						parityOpenOnlyHistory(t, home, history, before)
					} else if !reflect.DeepEqual(before, parityLocalTree(t, home)) {
						t.Fatal("legacy target control changed unrelated owned files")
					}
					id := fmt.Sprintf("I007.duplicate-targets.%s.%s.fixture", tc.name, mode)
					evidence.Checks[id] = append(evidence.Checks[id], lane.Name)
				})
			}
		}
	}
	saveParityLocal(t, "comment-targets-fixture", evidence)
}

func TestParityActualBinarySharedTargetPreviews(t *testing.T) {
	lane := parityLane{Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}
	if lane.Binary == "" {
		t.Skip("requires the selected actual current binary")
	}
	if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
		t.Fatal(err)
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	t.Cleanup(func() { saveParityLocal(t, "shared-target-previews-fixture", evidence) })
	for _, writer := range []struct {
		id, operation  string
		command, flags []string
	}{
		{"I007", "issue.comment.add", []string{"issue", "comment", "add"}, []string{"--markdown=Owned target preview"}},
		{"I005", "issue.clone", []string{"issue", "clone"}, nil},
		{"I012", "issue.delete", []string{"issue", "delete"}, nil},
		{"I013", "issue.edit", []string{"issue", "edit"}, []string{"--summary=Owned target preview"}},
		{"I022", "issue.transition", []string{"issue", "transition"}, []string{"Done"}},
		{"I013", "issue.edit.input", []string{"issue", "edit"}, []string{"--generate-json"}},
	} {
		for _, mode := range []string{"json", "compact"} {
			for _, expression := range []struct {
				name       string
				args, want []string
			}{
				{"repeated", []string{"PAIR-2", "PAIR-1", "PAIR-2"}, []string{"PAIR-2", "PAIR-1"}},
				{"csv", []string{"PAIR-2,PAIR-1,PAIR-2"}, []string{"PAIR-2", "PAIR-1"}},
				{"overlapping-ranges", []string{"PAIR-2:3", "PAIR-1..PAIR-2", "PAIR-3"}, []string{"PAIR-2", "PAIR-3", "PAIR-1"}},
				{"canonical-number", []string{"PAIR-0002", "PAIR-2"}, []string{"PAIR-2"}},
				{"invalid-late", []string{"PAIR-1", "PAIR-2,"}, nil},
			} {
				if writer.operation == "issue.edit.input" && expression.want != nil {
					continue // This live read has no local preview variant.
				}
				t.Run(writer.operation+"/"+mode+"/"+expression.name, func(t *testing.T) {
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						calls.Add(1)
						http.Error(w, "local target admission must not send HTTP", http.StatusInternalServerError)
					}))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.Name, server.URL)
					before := parityLocalTree(t, home)
					args := append(slices.Clone(writer.command), expression.args...)
					args = append(args, writer.flags...)
					// Also refuse malformed live targets before credential resolution,
					// rather than proving only a preview's normal no-request branch.
					if expression.want != nil {
						args = append(args, "--dry-run")
					}
					result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_READ_ONLY=false"}, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
						t.Fatal("local target admission changed storage or sent HTTP")
					}
					if expression.want == nil {
						const failure = `[{"code":"arg_value_invalid","message":"invalid issue key \"\"; use PROJECT-123","hint":"That isn't one of the accepted values — run the command with ` + "`--help`" + ` to see the choices."}]`
						if !parityPagingRefusal(result.Stdout, writer.operation, result.Exit, "arg_value_invalid", []byte(failure)) {
							t.Fatalf("malformed late target did not refuse before credentials: exit=%d stdout=%s stderr=%s", result.Exit, result.Stdout, result.Stderr)
						}
					} else {
						if result.Exit != 0 || len(result.Stderr) != 0 {
							t.Fatalf("target preview failed: exit=%d stdout=%s stderr=%s", result.Exit, result.Stdout, result.Stderr)
						}
						data := result.Stdout
						if mode == "json" {
							parityAuthoringCleanData(t, data, lane.Name, writer.operation)
							data = parityOriginalFrameData(data)
						}
						if !parityTargetRows(data, expression.want, true) {
							t.Fatalf("target preview lost its ordered unique rows: %s", data)
						}
					}
					id := writer.id + ".target-previews." + writer.operation + "." + expression.name + "." + mode
					evidence.Checks[id] = []string{lane.Name}
				})
			}
		}
	}
}
