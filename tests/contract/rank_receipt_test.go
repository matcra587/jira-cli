package contract

import (
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

type rankReceiptFrame struct {
	OK   bool `json:"ok"`
	Meta struct {
		ExitCode *int `json:"exit_code"`
	} `json:"meta"`
	Data   *envelope.IssueRankOutput `json:"data"`
	Errors []envelope.Error          `json:"errors"`
}

func rankReceiptArgs(mode string) []string {
	args := []string{"issue", "rank", "KEY-1", "KEY-2", "--before=KEY-9", "--no-input"}
	if mode != "agent" {
		args = append(args, "--output="+mode)
	}
	return args
}

func rankReceiptServer(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	return rankReceiptServerFor(t, status, body, []string{"KEY-1", "KEY-2"})
}

func rankReceiptServerFor(t *testing.T, status int, body string, issues []string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	calls := &atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		var payload struct {
			Issues          []string `json:"issues"`
			RankBeforeIssue string   `json:"rankBeforeIssue"`
			RankAfterIssue  string   `json:"rankAfterIssue"`
		}
		if req.Method != http.MethodPut || req.URL.Path != "/rest/agile/1.0/issue/rank" || len(req.URL.Query()) != 0 || json.NewDecoder(req.Body).Decode(&payload) != nil || !slices.Equal(payload.Issues, issues) || payload.RankBeforeIssue != "KEY-9" || payload.RankAfterIssue != "" {
			t.Error("ranking changed its one authored request or input order")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, calls
}

func TestRankMaximumBatchUsesOneOrderedRequest(t *testing.T) {
	t.Parallel()
	issues := make([]string, 50)
	for i := range issues {
		issues[i] = fmt.Sprintf("KEY-%d", i+100)
	}
	for _, preview := range []bool{false, true} {
		t.Run(fmt.Sprintf("preview=%t", preview), func(t *testing.T) {
			t.Parallel()
			server, calls := rankReceiptServerFor(t, http.StatusNoContent, "", issues)
			args := append([]string{"issue", "rank"}, issues...)
			args = append(args, "--before=KEY-9", "--output=json", "--no-input")
			if preview {
				args = append(args, "--dry-run")
			}
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			wantCalls := int64(1)
			if preview {
				wantCalls = 0
			}
			var frame rankReceiptFrame
			if exit != 0 || calls.Load() != wantCalls || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || frame.Data == nil || frame.Data.DryRun != preview || frame.Data.Anchor != "KEY-9" || frame.Data.Position != "before" || len(frame.Data.Results) != 50 {
				t.Fatalf("maximum rank batch split, replayed or lost receipt: exit=%d calls=%d stderr=%q stdout=%q", exit, calls.Load(), stderr, stdout)
			}
			for i, row := range frame.Data.Results {
				if row.Issue.Key != issues[i] || !row.OK || row.Data == nil || row.Err != nil {
					t.Fatal("maximum rank batch lost its authored order or acknowledgement")
				}
			}
		})
	}
}

func TestRankUnexpectedSuccessRequiresInspection(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusResetContent} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			server, calls := rankReceiptServer(t, status, `{}`)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), rankReceiptArgs("json")...)
			var frame rankReceiptFrame
			if exit != cli.ExitServer || calls.Load() != 1 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "rank_response_invalid" || !strings.Contains(frame.Errors[0].Hint, "Inspect") || frame.Data == nil || len(frame.Data.Results) != 2 {
				t.Fatalf("unexpected rank acknowledgement claimed success or replayed: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
			}
			for _, row := range frame.Data.Results {
				if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != "rank_response_invalid" {
					t.Fatal("unexpected native status fabricated an issue move")
				}
			}
		})
	}
}

func TestRankInterruptRetainsAuthoredBatchWithoutReplay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported for subprocesses on Windows")
	}
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			started := make(chan struct{})
			var once sync.Once
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				var body struct {
					Issues []string `json:"issues"`
					Before string   `json:"rankBeforeIssue"`
				}
				if req.Method != http.MethodPut || req.URL.Path != "/rest/agile/1.0/issue/rank" || json.NewDecoder(req.Body).Decode(&body) != nil || !slices.Equal(body.Issues, []string{"KEY-1", "KEY-2"}) || body.Before != "KEY-9" {
					t.Error("interrupted ranking changed its single authored batch")
				}
				once.Do(func() { close(started) })
				<-req.Context().Done()
			}))
			t.Cleanup(server.Close)
			env := editReceiptEnv(server.URL)
			if mode == "agent" {
				env = append(env, "AGENT=1")
			}
			stdout, stderr, exit := runBinaryEnvStarted(t, env, func(process *os.Process) {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("ranking did not reach the independent cancellation stimulus")
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}, rankReceiptArgs(mode)...)
			if exit != cli.ExitCanceled || calls.Load() != 1 {
				t.Fatalf("rank interrupt changed severity or replayed: exit=%d calls=%d stderr=%q", exit, calls.Load(), stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1:") || !strings.Contains(stdout, "KEY-2:") || !strings.Contains(stdout, "The command was interrupted.") || !strings.Contains(stderr, "context canceled") {
					t.Fatalf("interrupted ranking discarded human target outcomes: stdout=%q stderr=%q", stdout, stderr)
				}
				return
			}
			var frame rankReceiptFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitCanceled || len(frame.Errors) != 1 || frame.Errors[0].Code != cli.CodeCanceled || frame.Data == nil || frame.Data.Anchor != "KEY-9" || frame.Data.Position != "before" || len(frame.Data.Results) != 2 {
				t.Fatal("interrupted ranking discarded its authored move and outcomes")
			}
			for i, row := range frame.Data.Results {
				if row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] || row.OK || row.Data != nil || row.Err == nil || row.Err.Code != cli.CodeCanceled {
					t.Fatal("interrupted ranking reordered targets or invented acknowledged moves")
				}
			}
		})
	}
}

func TestRankDocumentedMultiStatusPublishesExactOutcomes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			// The vendored Agile response example wraps complete native entries.
			// Return them out of order so request correlation must be explicit.
			server, calls := rankReceiptServer(t, http.StatusMultiStatus, `{"entries":[{"issueId":10002,"issueKey":"KEY-2","status":403,"errors":["fixture denied\u001b[31m\u0007"]},{"issueId":10001,"issueKey":"KEY-1","status":200}]}`)
			env := editReceiptEnv(server.URL)
			if mode == "agent" {
				env = append(env, "AGENT=1")
			}
			stdout, stderr, exit := runBinaryEnv(t, env, rankReceiptArgs(mode)...)
			if exit != 0 || calls.Load() != 1 || strings.ContainsAny(stderr, "\x1b\a") {
				t.Fatalf("documented partial ranking lost success or replayed: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1: moved before KEY-9") || !strings.Contains(stdout, "KEY-2: fixture denied") || strings.ContainsAny(stdout, "\x1b\a") {
					t.Fatal("partial ranking lost ordered or sanitized human outcomes")
				}
				return
			}
			var out envelope.IssueRankOutput
			if mode == "json" {
				var frame rankReceiptFrame
				if json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || frame.Data == nil {
					t.Fatal("partial ranking did not publish its full success frame")
				}
				out = *frame.Data
			} else if json.Unmarshal([]byte(stdout), &out) != nil {
				t.Fatal("partial ranking did not publish bare compact data")
			}
			if out.Anchor != "KEY-9" || out.Position != "before" || len(out.Results) != 2 {
				t.Fatal("documented ranking discarded the composed request or ordered outcomes")
			}
			first, second := out.Results[0], out.Results[1]
			if first.Issue.Key != "KEY-1" || !first.OK || first.Data == nil || first.Err != nil || second.Issue.Key != "KEY-2" || second.OK || second.Data != nil || second.Err == nil || second.Err.Code != "jira_forbidden" || second.Err.Message != "fixture denied\x1b[31m\a" {
				t.Fatal("ranking changed native attribution, severity or literal machine diagnostics")
			}
		})
	}
}

func TestRankTotalFailureRetainsOrderedOutcomes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact", "human", "agent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			server, calls := rankReceiptServer(t, http.StatusMultiStatus, `{"entries":[{"issueId":10002,"issueKey":"KEY-2","status":403,"errors":["fixture denied"]},{"issueId":10001,"issueKey":"KEY-1","status":404,"errors":["fixture absent"]}]}`)
			env := editReceiptEnv(server.URL)
			if mode == "agent" {
				env = append(env, "AGENT=1")
			}
			args := rankReceiptArgs(mode)
			if mode == "json" || mode == "compact" {
				args = append(args, "--jq", `error("failed ranking bypasses jq")`)
			}
			stdout, stderr, exit := runBinaryEnv(t, env, args...)
			if exit != cli.ExitAuth || calls.Load() != 1 {
				t.Fatalf("rank total failure lost severity or replayed: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
			}
			if mode == "human" {
				if !strings.Contains(stdout, "KEY-1: fixture absent") || !strings.Contains(stdout, "KEY-2: fixture denied") {
					t.Fatal("rank total failure discarded human outcomes")
				}
				return
			}
			var frame rankReceiptFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitAuth || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_forbidden" || frame.Data == nil || frame.Data.Anchor != "KEY-9" || len(frame.Data.Results) != 2 {
				t.Fatal("rank total failure discarded its request or diagnostics")
			}
			for i, code := range []string{"jira_not_found", "jira_forbidden"} {
				row := frame.Data.Results[i]
				if row.Issue.Key != []string{"KEY-1", "KEY-2"}[i] || row.OK || row.Data != nil || row.Err == nil || row.Err.Code != code {
					t.Fatal("rank total failure lost input ordering or invented a move")
				}
			}
		})
	}
}

func TestRankRejectsUnusableMultiStatusWithoutInventingMoves(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"blank": "", "null": "null", "missing-entries": `{}`, "null-entries": `{"entries":null}`, "empty-entries": `{"entries":[]}`,
		"legacy-foreign-array":  `[{"key":"KEY-3","status":403,"errors":["fixture denied"]}]`,
		"foreign-entry":         `{"entries":[{"issueKey":"KEY-3","status":403},{"issueKey":"KEY-2","status":200}]}`,
		"incomplete":            `{"entries":[{"issueKey":"KEY-1","status":200}]}`,
		"repeated-key":          `{"entries":[{"issueKey":"KEY-1","status":200},{"issueKey":"KEY-1","status":403}]}`,
		"repeated-id":           `{"entries":[{"issueId":10001,"issueKey":"KEY-1","status":200},{"issueId":10001,"issueKey":"KEY-2","status":403}]}`,
		"missing-status":        `{"entries":[{"issueKey":"KEY-1"},{"issueKey":"KEY-2","status":403}]}`,
		"missing-identity":      `{"entries":[{"status":200},{"issueKey":"KEY-2","status":403}]}`,
		"zero-id":               `{"entries":[{"issueId":0,"issueKey":"KEY-1","status":200},{"issueKey":"KEY-2","status":403}]}`,
		"contradictory-success": `{"entries":[{"issueKey":"KEY-1","status":200,"errors":["fixture denied"]},{"issueKey":"KEY-2","status":403}]}`,
		"trailing":              `{"entries":[{"issueKey":"KEY-1","status":200},{"issueKey":"KEY-2","status":403}]}{}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server, calls := rankReceiptServer(t, http.StatusMultiStatus, body)
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), rankReceiptArgs("json")...)
			var frame rankReceiptFrame
			if exit != cli.ExitServer || calls.Load() != 1 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "rank_response_invalid" || !strings.Contains(frame.Errors[0].Hint, "Inspect") || frame.Data == nil || frame.Data.Anchor != "KEY-9" || len(frame.Data.Results) != 2 {
				t.Fatalf("unusable ranking invented moves, discarded targets or replayed: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
			}
			for _, row := range frame.Data.Results {
				if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != "rank_response_invalid" {
					t.Fatal("unattributed native ranking fabricated an issue move")
				}
			}
		})
	}
}

func TestRankRefusesInvalidTargetsBeforeRequests(t *testing.T) {
	t.Parallel()
	lateDuplicate := make([]string, 101)
	for i := range lateDuplicate {
		lateDuplicate[i] = fmt.Sprintf("KEY-%d", i+100)
	}
	lateDuplicate[100] = lateDuplicate[0]
	lateAnchor := slices.Clone(lateDuplicate)
	lateAnchor[100] = "KEY-9"
	for name, issues := range map[string][]string{
		"cross-batch-duplicate": lateDuplicate, "late-anchor": lateAnchor, "repeated": {"KEY-1", "KEY-1"}, "self-anchor": {"KEY-9"}, "empty": {""},
	} {
		for _, preview := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/write", true: "/preview"}[preview], func(t *testing.T) {
				t.Parallel()
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					w.WriteHeader(http.StatusNoContent)
				}))
				t.Cleanup(server.Close)
				args := append([]string{"issue", "rank"}, issues...)
				args = append(args, "--before=KEY-9", "--output=json", "--no-input")
				if preview {
					args = append(args, "--dry-run")
				}
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				var frame rankReceiptFrame
				if exit != cli.ExitValidation || calls.Load() != 0 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || len(frame.Errors) != 1 {
					t.Fatalf("invalid rank target escaped local preflight: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
				}
			})
		}
	}
}

// Independently exercise the assembled binary beyond the native request cap.
// Expectations use authored literal boundaries, never the production planner.
func TestRankMultipleBatchesUseOrderedNativeRequests(t *testing.T) {
	t.Parallel()
	for _, count := range []int{51, 101} {
		for _, position := range []string{"before", "after"} {
			for _, preview := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/preview=%t", position, count, preview), func(t *testing.T) {
					t.Parallel()
					issues := make([]string, count)
					for i := range issues {
						issues[i] = fmt.Sprintf("KEY-%d", i+100)
					}
					var calls atomic.Int64
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
						batch := int(calls.Add(1)) - 1
						start := batch * 50
						if start >= len(issues) {
							t.Error("rank replayed an authored batch")
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						before, after := "KEY-9", ""
						if position == "after" {
							before, after = "", "KEY-9"
						}
						if batch > 0 {
							before, after = "", issues[start-1]
						}
						if req.Method != http.MethodPut || req.URL.RequestURI() != "/rest/agile/1.0/issue/rank" || !rankRequestMatches(req.Body, issues[start:min(start+50, len(issues))], before, after) {
							t.Error("rank changed its native boundary, source order or chained anchor")
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						w.WriteHeader(http.StatusNoContent)
					}))
					t.Cleanup(server.Close)
					args := append([]string{"issue", "rank"}, issues...)
					args = append(args, "--"+position+"=KEY-9", "--output=json", "--no-input")
					if preview {
						args = append(args, "--dry-run")
					}
					stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
					wantCalls := int64((count + 49) / 50)
					if preview {
						wantCalls = 0
					}
					var frame rankReceiptFrame
					if exit != 0 || stderr != "" || calls.Load() != wantCalls || json.Unmarshal([]byte(stdout), &frame) != nil || !frame.OK || frame.Data == nil || frame.Data.Anchor != "KEY-9" || frame.Data.Position != position || frame.Data.DryRun != preview || len(frame.Data.Results) != count {
						t.Fatalf("multi-batch ranking lost its complete ordered receipt: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
					}
					for i, row := range frame.Data.Results {
						if row.Issue.Key != issues[i] || !row.OK || row.Data == nil || row.Err != nil {
							t.Fatal("multi-batch ranking lost an authored move or acknowledgement")
						}
					}
				})
			}
		}
	}
}

func rankRequestMatches(reader io.Reader, issues []string, before, after string) bool {
	raw, err := io.ReadAll(io.LimitReader(reader, 8193))
	if err != nil || len(raw) > 8192 || !jsontext.Value(raw).IsValid() {
		return false
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil || len(members) != 2 {
		return false
	}
	var observed []string
	if json.Unmarshal(members["issues"], &observed) != nil || !slices.Equal(observed, issues) {
		return false
	}
	name, expected := "rankBeforeIssue", before
	if after != "" {
		name, expected = "rankAfterIssue", after
	}
	var anchor string
	return expected != "" && json.Unmarshal(members[name], &anchor) == nil && anchor == expected
}

func TestRankBatchWireOracleRejectsAmbiguousMembers(t *testing.T) {
	t.Parallel()
	valid := `{"issues":["KEY-1"],"rankBeforeIssue":"KEY-9"}`
	if !rankRequestMatches(strings.NewReader(valid), []string{"KEY-1"}, "KEY-9", "") {
		t.Fatal("valid independent rank fixture refused")
	}
	for name, raw := range map[string]string{
		"duplicate":          `{"issues":[],"issues":["KEY-1"],"rankBeforeIssue":"KEY-9"}`,
		"inactive-null":      `{"issues":["KEY-1"],"rankBeforeIssue":"KEY-9","rankAfterIssue":null}`,
		"extra-member":       `{"issues":["KEY-1"],"rankBeforeIssue":"KEY-9","extra":true}`,
		"trailing":           valid + `{}`,
		"null-anchor":        `{"issues":["KEY-1"],"rankBeforeIssue":null}`,
		"hidden-after-bound": valid + strings.Repeat(" ", 8192-len(valid)) + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			if rankRequestMatches(strings.NewReader(raw), []string{"KEY-1"}, "KEY-9", "") {
				t.Fatal("rank oracle accepted ambiguous native members")
			}
		})
	}
}

func TestRankLaterBatchRefusalRetainsPublicOutcomes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			issues := make([]string, 101)
			for i := range issues {
				issues[i] = fmt.Sprintf("KEY-%d", i+100)
			}
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				batch := int(calls.Add(1)) - 1
				if batch > 1 {
					t.Error("rank entered a batch after refusal")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				before, after := "KEY-9", ""
				if batch == 1 {
					before, after = "", issues[49]
				}
				if req.Method != http.MethodPut || req.URL.RequestURI() != "/rest/agile/1.0/issue/rank" || !rankRequestMatches(req.Body, issues[batch*50:(batch+1)*50], before, after) {
					t.Error("rank refusal fixture received a changed request")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if batch == 0 {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"errorMessages":["fixture denied"]}`))
			}))
			t.Cleanup(server.Close)
			args := append([]string{"issue", "rank"}, issues...)
			args = append(args, "--before=KEY-9", "--output="+mode, "--no-input")
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			var frame rankReceiptFrame
			if exit != cli.ExitAuth || calls.Load() != 2 || json.Unmarshal([]byte(stdout), &frame) != nil || frame.OK || frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != cli.ExitAuth || len(frame.Errors) != 1 || frame.Errors[0].Code != "jira_forbidden" || frame.Data == nil || frame.Data.Anchor != "KEY-9" || frame.Data.Position != "before" || len(frame.Data.Results) != 101 {
				t.Fatalf("later refusal lost original severity or retained data: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
			}
			for i, row := range frame.Data.Results {
				if row.Issue.Key != issues[i] {
					t.Fatal("later refusal reordered public outcomes")
				}
				if i < 50 {
					if !row.OK || row.Data == nil || row.Err != nil {
						t.Fatal("later refusal erased an acknowledged move")
					}
					continue
				}
				code := "jira_forbidden"
				if i == 100 {
					code = "rank_not_attempted"
				}
				if row.OK || row.Data != nil || row.Err == nil || row.Err.Code != code {
					t.Fatal("later refusal confused entered and withheld moves")
				}
			}
		})
	}
}
