//go:build live

package live

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestParityActualBinaryWorklogAuthoringFlags(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	t.Cleanup(func() { saveParityLocal(t, "worklog-flags-fixture", evidence) })
	for _, lane := range []parityLane{{Name: "before", Binary: os.Getenv("JIRA_PARITY_BEFORE_BINARY"), Digest: os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {Name: "after", Binary: os.Getenv("JIRA_LIVE_BINARY"), Digest: os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		if lane.Binary == "" {
			t.Skip("requires both selected actual binaries")
		}
		if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"json", "compact"} {
			for _, recipe := range []struct {
				name, effort, start, wire string
				seconds, workday          int
			}{
				{"minutes-utc", "2m", "2026-01-02T12:30:00Z", "2026-01-02T12:30:00.000+0000", 120, 28800},
				{"mixed-offset", "1d1h2m", "2026-01-02T12:30:00+02:30", "2026-01-02T12:30:00.000+0230", 32520, 28800},
				{"configured-workday", "1d1h2m", "2026-01-02T12:30:00+02:30", "2026-01-02T12:30:00.000+0230", 25320, 21600},
			} {
				for _, source := range []string{"inline", "file"} {
					for _, preview := range []bool{true, false} {
						t.Run(fmt.Sprintf("%s/%s/%s/%s/preview=%v", lane.Name, mode, recipe.name, source, preview), func(t *testing.T) {
							payload := fmt.Sprintf(`{"timeSpentSeconds":%d,"started":%q,"comment":%s}`, recipe.seconds, recipe.wire, parityAuthoringDocument)
							response := fmt.Sprintf(`{"id":"501","timeSpentSeconds":%d,"started":%q,"comment":%s,"author":{"accountId":"native-author","emailAddress":null},"future":900719925474099312345}`, recipe.seconds, recipe.wire, parityAuthoringDocument)
							var calls atomic.Int32
							server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								calls.Add(1)
								body, err := io.ReadAll(io.LimitReader(r.Body, 8193))
								query := ""
								if lane.Name == "after" {
									query = "notifyUsers=false"
								}
								user, token, authenticated := r.BasicAuth()
								if preview || err != nil || len(body) > 8192 || !parityAuthoringJSON(body, []byte(payload)) || r.Method != "POST" || r.URL.Path != "/rest/api/3/issue/PAIR-1/worklog" || r.URL.RawQuery != query || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Cookie") != "" || !authenticated || user != "fixture@example.invalid" || token != "synthetic-fixture-token" {
									t.Error("worklog flags changed exact native body, offset, effort or authenticated request")
									http.Error(w, "invalid worklog flag request", http.StatusBadRequest)
									return
								}
								w.Header().Set("Content-Type", "application/json")
								w.WriteHeader(http.StatusCreated)
								_, _ = io.WriteString(w, response)
							}))
							defer server.Close()
							home := t.TempDir()
							config := parityCachePublicConfig(t, home, lane.Name, server.URL)
							public, err := os.ReadFile(config)
							if err != nil {
								t.Fatal(err)
							}
							public = append(public, fmt.Appendf(nil, "workday_seconds=%d\n", recipe.workday)...)
							if err := os.WriteFile(config, public, 0o600); err != nil {
								t.Fatal(err)
							}
							args := []string{"worklog", "add", "PAIR-1", "--time-spent", recipe.effort, "--started", recipe.start, "--parallelism=1"}
							if source == "inline" {
								args = append(args, "--markdown=**Owned café**")
							} else {
								path := filepath.Join(home, "owned worklog 古い '$;&.md")
								if err := os.WriteFile(path, []byte("**Owned café**"), 0o600); err != nil {
									t.Fatal(err)
								}
								args = append(args, "--markdown-file", path)
							}
							if lane.Name == "after" {
								args = append(args, "--notify=false")
							}
							extra := []string{"JIRA_READ_ONLY=false"}
							if preview {
								args = append(args, "--dry-run")
								extra = append(extra, "JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=")
							}
							before, began := parityLocalTree(t, home), time.Now()
							result := parityFixtureProcessAt(t, lane.Name, lane.Binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, extra, args...)
							evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.Name, Digest: lane.Digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
							wantCalls := int32(1)
							if preview {
								wantCalls = 0
							}
							if result.Exit != 0 || len(result.Stderr) != 0 || calls.Load() != wantCalls {
								t.Fatalf("worklog authoring flags failed: exit=%d HTTP=%d stdout=%s stderr=%s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
							}
							data := result.Stdout
							if mode == "json" {
								parityAuthoringCleanData(t, data, lane.Name, "worklog.add")
								data = parityOriginalFrameData(data)
							}
							var expected string
							if lane.Name == "before" {
								worklog := payload
								if !preview {
									worklog = fmt.Sprintf(`{"id":"501","timeSpentSeconds":%d,"started":%q,"comment":%s}`, recipe.seconds, recipe.wire, parityAuthoringDocument)
								}
								expected = fmt.Sprintf(`{"issue":{"key":"PAIR-1"},"worklog":%s,"dry_run":%v}`, worklog, preview)
							} else {
								entry := `{"applied":false}`
								if !preview {
									entry = `{"applied":true,"worklog":{"id":"501","fields":` + response + `}}`
								}
								expected = fmt.Sprintf(`{"dry_run":%v,"payload":%s,"notify":false,"body_route":"markdown","workday_seconds":%d,"results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":%s}]}`, preview, payload, recipe.workday, entry)
							}
							if !parityAuthoringJSON(data, []byte(expected)) {
								t.Fatalf("worklog flags lost complete independent per-lane output: %s", data)
							}
							if !preview && lane.Name == "after" {
								history := parityOpenHistory(t, home, lane.Name, server.URL, "PAIR-1", began)
								parityOpenOnlyHistory(t, home, history, before)
							} else if !reflect.DeepEqual(before, parityLocalTree(t, home)) {
								t.Fatal("worklog flags changed unrelated owned input or storage")
							}
							id := fmt.Sprintf("C072.flags.%s.%s.%s.preview=%v", recipe.name, source, mode, preview)
							evidence.Checks[id] = append(evidence.Checks[id], lane.Name)
						})
					}
				}
			}
		}
	}
}
