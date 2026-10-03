//go:build flagmatrix

package matrix

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const matrixMoveInput = `{"sendBulkNotification":false,"targetToSourcesMapping":{"DEST,10001":{"issueIdsOrKeys":["KEY-1"]}},"futureCamelField":false}`

func TestMoveFlagEffects(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"json-input", "from-json", "body-adf", "dry-run", "validate-remote", "force", "yes", "wait", "max-polls", "poll-interval"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			var reads, posts, polls atomic.Int32
			var firstPoll time.Time
			var pollMu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Path == "/rest/api/3/issue/KEY-1":
					reads.Add(1)
					_, _ = fmt.Fprint(w, `{"id":"101","key":"KEY-1"}`)
				case r.Method == "POST" && r.URL.Path == "/rest/api/3/bulk/issues/move":
					posts.Add(1)
					body, err := io.ReadAll(r.Body)
					var got, want map[string]any
					decodeErr := json.Unmarshal(body, &got)
					wantErr := json.Unmarshal([]byte(matrixMoveInput), &want)
					if err != nil || decodeErr != nil || wantErr != nil || !reflect.DeepEqual(got, want) || r.URL.RawQuery != "" {
						t.Errorf("native move payload or query changed: %s %v", body, err)
						w.WriteHeader(400)
						return
					}
					w.WriteHeader(201)
					_, _ = fmt.Fprint(w, `{"taskId":"100"}`)
				case r.Method == "GET" && r.URL.Path == "/rest/api/3/bulk/queue/100":
					pollMu.Lock()
					defer pollMu.Unlock()
					if polls.Add(1) == 1 {
						firstPoll = time.Now()
						_, _ = fmt.Fprint(w, `{"taskId":"100","status":"RUNNING","progressPercent":100}`)
					} else {
						if flag == "poll-interval" && time.Since(firstPoll) < 20*time.Millisecond {
							t.Error("configured 30ms delay was ignored")
						}
						_, _ = fmt.Fprint(w, `{"taskId":"100","status":"COMPLETE","totalIssueCount":1,"invalidOrInaccessibleIssueCount":0,"processedAccessibleIssues":[101]}`)
					}
				default:
					t.Errorf("unexpected move request: %s %s", r.Method, r.URL)
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			args := []string{"issue", "move", "KEY-1", "--no-input"}
			inputFlag := "json-input"
			if flag == "from-json" || flag == "body-adf" {
				inputFlag = flag
			}
			args = append(args, "--"+inputFlag+"=move.json")
			forceFlag := "force"
			if flag == "yes" {
				forceFlag = "yes"
			}
			if flag == "force" || flag == "yes" {
				refused := fwExec(t, server.URL, map[string]string{"move.json": matrixMoveInput}, "", args...)
				if refused.Exit != 3 || reads.Load() != 0 || posts.Load() != 0 {
					t.Fatal("missing acknowledgement did not refuse before any request")
				}
				fwRefusal(t, refused, "flag_required", "force")
			}
			args = append(args, "--"+forceFlag)
			wantReads, wantPosts, wantPolls := int32(1), int32(1), int32(0)
			switch flag {
			case "dry-run":
				args = append(args, "--dry-run")
				wantReads, wantPosts = 0, 0
			case "validate-remote":
				args = append(args, "--dry-run", "--validate-remote")
				wantPosts = 0
			case "wait", "max-polls", "poll-interval":
				args = append(args, "--wait", "--poll-interval=1ms")
				wantPolls = 2
				if flag == "max-polls" {
					args = append(args, "--max-polls=1")
					wantPolls = 1
				}
				if flag == "poll-interval" {
					args = append(args, "--poll-interval=30ms")
				}
			}
			res := fwExec(t, server.URL, map[string]string{"move.json": matrixMoveInput}, "", args...)
			out := fwParse(t, res)
			if res.Exit != 0 || reads.Load() != wantReads || posts.Load() != wantPosts || polls.Load() != wantPolls || fwAt(t, out.Data, "payload", "futureCamelField") != false || fwAt(t, out.Data, "results", 0, "data", "applied") != (wantPolls == 2) || fwAt(t, out.Data, "poll_limit_reached") != (flag == "max-polls") || fwAt(t, out.Data, "remote_validated") != (wantReads == 1) || fwAt(t, out.Data, "acknowledged") != (wantPosts == 1) {
				t.Fatalf("move flag effect missing: %s exit=%d reads=%d posts=%d polls=%d stdout=%s", flag, res.Exit, reads.Load(), posts.Load(), polls.Load(), res.Stdout)
			}
			Prove(t, "jira issue move", flag, "exact native input, local versus remote preview, explicit acknowledgement, bounded observations and configured delay asserted through the binary")
		})
	}
}

func TestMoveStatusFlagEffects(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"wait", "max-polls", "poll-interval"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			var polls atomic.Int32
			var firstPoll time.Time
			var pollMu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/rest/api/3/bulk/queue/100" {
					t.Errorf("status performed a write or unrelated read: %s %s", r.Method, r.URL)
					w.WriteHeader(400)
					return
				}
				pollMu.Lock()
				defer pollMu.Unlock()
				if polls.Add(1) == 1 {
					firstPoll = time.Now()
					_, _ = fmt.Fprint(w, `{"taskId":"100","status":"RUNNING","progressPercent":100}`)
				} else {
					if flag == "poll-interval" && time.Since(firstPoll) < 20*time.Millisecond {
						t.Error("configured 30ms status delay was ignored")
					}
					_, _ = fmt.Fprint(w, `{"taskId":"100","status":"FAILED"}`)
				}
			}))
			defer server.Close()
			args := []string{"issue", "move", "status", "100", "--wait", "--poll-interval=1ms"}
			wantPolls := int32(2)
			if flag == "max-polls" {
				args = append(args, "--max-polls=1")
				wantPolls = 1
			}
			if flag == "poll-interval" {
				args = append(args, "--poll-interval=30ms")
			}
			res := fwExecEnv(t, server.URL, nil, "", []string{"JIRA_READ_ONLY=true"}, args...)
			out := fwParse(t, res)
			if res.Exit != 0 || polls.Load() != wantPolls || fwAt(t, out.Data, "polls") != float64(wantPolls) || fwAt(t, out.Data, "terminal") != (wantPolls == 2) || fwAt(t, out.Data, "poll_limit_reached") != (wantPolls == 1) {
				t.Fatalf("status flag effect missing: %s exit=%d polls=%d %s", flag, res.Exit, polls.Load(), res.Stdout)
			}
			Prove(t, "jira issue move status", flag, "read-only task observation stops at explicit bound or terminal failure and honors the configured delay without submitting")
		})
	}
}
