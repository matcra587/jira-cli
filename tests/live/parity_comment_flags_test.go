//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// This complete output oracle is authored separately from the CLI's comment
// models, Markdown converter and visibility helpers. Native receipt visibility
// is retained in the legacy layout; current output reports requested visibility
// beside its narrower stored-comment projection.
func parityCommentFlagsExpected(t *testing.T, lane, verb, visibility, mode string, preview, notify bool) []byte {
	t.Helper()
	issue := map[string]any{"key": "PAIR-1"}
	body := json.RawMessage(parityAuthoringDocument)
	vis := map[string]any{"type": visibility, "value": "Owned " + visibility + " <&> café"}
	author := map[string]any{"account_id": "owned-author", "display_name": "Owned author <&>"}
	comment := map[string]any{"id": "501", "author": author, "body": body, "created": "2026-01-02T12:30:00.000+0000", "updated": "2026-01-03T12:30:00.000+0000"}
	if lane == "before" {
		comment["update_author"], comment["visibility"] = nil, vis
		// The delivered compact renderer omits native null object members.
		// Full JSON retains the explicit null in its legacy projection.
		if mode == "compact" {
			delete(comment, "update_author")
		}
	}
	var data map[string]any
	if lane == "before" {
		data = map[string]any{"issue": issue, "dry_run": preview}
		if verb == "edit" {
			data["comment_id"], data["body_adf_summary"], data["visibility_change"] = "501", body, "replace"
			if !preview {
				data["comment"] = comment
			}
		} else if preview {
			data["comment"] = map[string]any{"body": body}
		} else {
			data["comment"] = comment
		}
	} else {
		data = map[string]any{"dry_run": preview, "body": body, "body_route": "markdown", "visibility": vis}
		if verb == "edit" {
			data["issue"], data["comment_id"], data["notify"], data["visibility_cleared"] = issue, "501", notify, false
			if !preview {
				data["comment"] = comment
			}
		} else {
			entry := map[string]any{}
			if !preview {
				entry["comment"] = comment
			}
			data["results"] = []any{map[string]any{"issue": issue, "ok": true, "data": entry}}
		}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParityActualBinaryCommentMarkdownFlags(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []parityLane{{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		if lane.Binary == "" {
			t.Skip("requires both selected actual binaries")
		}
		if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
			t.Fatal(err)
		}
		for _, verb := range []string{"bare", "add", "edit"} {
			for _, source := range []string{"inline", "file"} {
				for _, visibility := range []string{"group", "role"} {
					for _, mode := range []string{"json", "compact"} {
						for _, preview := range []bool{true, false} {
							notifications := []bool{true}
							if lane.Name == "after" && verb == "edit" {
								notifications = append(notifications, false)
							}
							for _, notify := range notifications {
								t.Run(fmt.Sprintf("%s/%s/%s/%s/%s/preview=%t/notify=%t", lane.Name, verb, source, visibility, mode, preview, notify), func(t *testing.T) {
									var calls atomic.Int32
									server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
										count := calls.Add(1)
										body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
										user, token, authenticated := r.BasicAuth()
										method, path, query := "POST", "/rest/api/3/issue/PAIR-1/comment", ""
										if verb == "edit" {
											method, path = "PUT", path+"/501"
											if !notify {
												query = "notifyUsers=false"
											}
										}
										vis := map[string]any{"type": visibility, "value": "Owned " + visibility + " <&> café"}
										expected, marshalErr := json.Marshal(map[string]any{"body": json.RawMessage(parityAuthoringDocument), "visibility": vis})
										if count != 1 || preview || err != nil || marshalErr != nil || len(body) > 8192 || !parityAuthoringJSON(body, expected) || r.Method != method || r.URL.Path != path || r.URL.RawQuery != query || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Cookie") != "" || !authenticated || user != "fixture@example.invalid" || token != "synthetic-fixture-token" {
											t.Error("comment flags changed the exact authenticated native write or replayed it")
											http.Error(w, "wrong synthetic request", http.StatusBadRequest)
											return
										}
										w.Header().Set("Content-Type", "application/json")
										if verb != "edit" {
											w.WriteHeader(http.StatusCreated)
										}
										if err := json.NewEncoder(w).Encode(map[string]any{"id": "501", "body": json.RawMessage(parityAuthoringDocument), "visibility": vis, "author": map[string]any{"accountId": "owned-author", "displayName": "Owned author <&>"}, "created": "2026-01-02T12:30:00.000+0000", "updated": "2026-01-03T12:30:00.000+0000"}); err != nil {
											t.Error(err)
										}
									}))
									defer server.Close()
									home := t.TempDir()
									config := parityCachePublicConfig(t, home, lane.Name, server.URL)
									path := filepath.Join(home, "comment spaces 古い '$;&.md")
									if err := os.WriteFile(path, []byte("**Owned café**"), 0o600); err != nil {
										t.Fatal(err)
									}
									args := []string{"issue", "comment", "add", "PAIR-1"}
									prefix := "I007"
									switch verb {
									case "bare":
										prefix = "I006"
										if lane.Name == "before" {
											args = []string{"issue", "comment", "PAIR-1"}
										}
									case "edit":
										prefix, args = "I009", []string{"issue", "comment", "edit", "PAIR-1", "501"}
									}
									if source == "file" {
										args = append(args, "--markdown-file", path)
									} else {
										args = append(args, "--markdown=**Owned café**")
									}
									args = append(args, "--visibility-"+visibility, "Owned "+visibility+" <&> café")
									if verb != "edit" {
										args = append(args, "--parallelism=2")
									} else if !notify {
										args = append(args, "--notify=false")
									}
									extra := []string{"JIRA_READ_ONLY=false"}
									if preview {
										args = append(args, "--dry-run")
										extra = append(extra, "JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=")
									}
									preimage, began := parityLocalTree(t, home), time.Now()
									result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, extra, args...)
									evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
									wantCalls := int32(1)
									if preview {
										wantCalls = 0
									}
									if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != wantCalls {
										t.Fatalf("comment flag execution failed: exit %d; HTTP %d want %d; stdout %s; stderr %s", result.Exit, calls.Load(), wantCalls, result.Stdout, result.Stderr)
									}
									data := result.Stdout
									if mode == "json" {
										parityAuthoringCleanData(t, result.Stdout, lane.Name, parityExpectedOperation(lane.Name, args, 0))
										data = parityOriginalFrameData(result.Stdout)
									}
									if !parityAuthoringJSON(data, parityCommentFlagsExpected(t, lane.Name, verb, visibility, mode, preview, notify)) {
										t.Fatalf("comment flags lost complete independent candidate or native receipt: %s", result.Stdout)
									}
									if lane.Name == "after" && !preview {
										history := parityOpenHistory(t, home, lane.Name, server.URL, "PAIR-1", began)
										parityOpenOnlyHistory(t, home, history, preimage)
									} else if !reflect.DeepEqual(preimage, parityLocalTree(t, home)) {
										t.Fatal("comment flags changed authored input or unrelated public storage")
									}
									id := fmt.Sprintf("%s.flags.%s.%s.%s.preview=%t", prefix, source, visibility, mode, preview)
									if !notify {
										id += ".notify=" + strconv.FormatBool(notify)
									}
									evidence.Checks[id] = append(evidence.Checks[id], lane.Name)
								})
							}
						}
					}
				}
			}
		}
	}
	saveParityLocal(t, "comment-flags-fixture", evidence)
}
