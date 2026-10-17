package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestRateLimitNearWarningReachesOutput(t *testing.T) {
	t.Parallel()
	for _, operation := range []struct {
		name string
		args []string
		path string
		body string
	}{
		{name: "identity", args: []string{"auth", "whoami"}, path: "/rest/api/3/myself", body: `{"accountId":"native-id","active":true}`},
		{name: "issue", args: []string{"issue", "view", "KEY-1"}, path: "/rest/api/3/issue/KEY-1", body: `{"id":"10001","key":"KEY-1","fields":{"summary":"Native issue","comment":{"startAt":0,"maxResults":1,"total":0,"comments":[]}}}`},
	} {
		for _, header := range []string{"true", "false", "", "invalid"} {
			for _, mode := range []string{"json", "compact", "human"} {
				for _, refused := range []bool{false, true} {
					name := operation.name + "/" + header + "/" + mode + "/success"
					if refused {
						name = operation.name + "/" + header + "/" + mode + "/failure"
					}
					t.Run(name, func(t *testing.T) {
						t.Parallel()
						var calls atomic.Int32
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							calls.Add(1)
							email, token, ok := r.BasicAuth()
							if r.Method != http.MethodGet || r.URL.Path != operation.path || !ok || email != "person@example.com" || token != "example-fixture-token" {
								t.Errorf("unexpected request method/path or credential binding")
							}
							if header != "" {
								w.Header().Set("X-RateLimit-NearLimit", header)
							}
							if refused {
								w.WriteHeader(http.StatusForbidden)
								_, _ = io.WriteString(w, `{"errorMessages":["Owned refusal"]}`)
								return
							}
							_, _ = io.WriteString(w, operation.body)
						}))
						t.Cleanup(server.Close)
						args := append(append([]string{}, operation.args...), "--output="+mode, "--color=never", "--no-input")
						stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
						wantExit := 0
						if refused {
							wantExit = cli.ExitAuth
						}
						if exit != wantExit || calls.Load() != 1 {
							t.Fatalf("exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
						}
						if mode == "human" {
							if got := strings.Count(stderr, "Approaching Jira's rate limit;"); got != boolCount(header == "true") {
								t.Fatalf("human warning count=%d stderr=%q", got, stderr)
							}
							return
						}
						if !refused && stderr != "" {
							t.Fatalf("machine success wrote diagnostics: %q", stderr)
						}
						var frame struct {
							Warnings []lossyWarning `json:"warnings"`
						}
						if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
							t.Fatal(err)
						}
						want := boolCount(header == "true" && (refused || mode == "json"))
						if len(frame.Warnings) != want {
							t.Fatalf("warnings=%+v want=%d stdout=%q", frame.Warnings, want, stdout)
						}
						if want == 1 {
							warning := frame.Warnings[0]
							if warning.Type != "rate_limit_near" || warning.Message != "Approaching Jira's rate limit; requests may soon be throttled. Slow down or lower --parallelism." || warning.Lossy == nil || *warning.Lossy {
								t.Fatalf("incorrect warning: %+v", warning)
							}
						}
					})
				}
			}
		}
	}
}

func TestRateLimitNearWarningDeduplicatesConcurrentWrites(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(req.Body)
		if err != nil || string(body) != `{"fields":{"summary":"Owned update"}}` || req.Method != http.MethodPut ||
			(req.URL.Path != "/rest/api/3/issue/KEY-1" && req.URL.Path != "/rest/api/3/issue/KEY-2") {
			t.Error("unexpected native write")
		}
		w.Header().Set("X-RateLimit-NearLimit", "true")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL),
		"issue", "edit", "KEY-1", "KEY-2", "--summary=Owned update", "--parallelism=2", "--output=json", "--no-input")
	var frame struct {
		OK       bool           `json:"ok"`
		Warnings []lossyWarning `json:"warnings"`
		Data     struct {
			Results []struct {
				OK bool `json:"ok"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
		t.Fatal(err)
	}
	if exit != 0 || stderr != "" || calls.Load() != 2 || !frame.OK || len(frame.Data.Results) != 2 ||
		!frame.Data.Results[0].OK || !frame.Data.Results[1].OK || len(frame.Warnings) != 1 || frame.Warnings[0].Type != "rate_limit_near" {
		t.Fatalf("batch lost its acknowledgement or deduplicated warning: exit=%d calls=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
	}
}

func TestRateLimitNearWarningSurvivesFilterFailure(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("X-RateLimit-NearLimit", "true")
		_, _ = io.WriteString(w, `{"accountId":"native-id","active":true}`)
	}))
	t.Cleanup(server.Close)
	stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "auth", "whoami", "--output=json", `--jq=error("owned failure")`)
	var frame struct {
		OK       bool           `json:"ok"`
		Warnings []lossyWarning `json:"warnings"`
		Errors   []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
		t.Fatal(err)
	}
	if exit != cli.ExitValidation || calls.Load() != 1 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "jq_eval_failed" ||
		len(frame.Warnings) != 1 || frame.Warnings[0].Type != "rate_limit_near" {
		t.Fatalf("filter failure lost or duplicated its warning: exit=%d calls=%d stdout=%q", exit, calls.Load(), stdout)
	}
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
