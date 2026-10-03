//go:build flagmatrix

package matrix

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMoveFieldsFlagEffects(t *testing.T) {
	t.Parallel()
	const input = `{"fields":{"issuetype":{"id":"10001"},"summary":"Confirmed field change"}}`
	for _, flag := range []string{"json-input", "from-json", "body-adf", "dry-run", "force", "yes", "parallelism"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()
			var writes atomic.Int32
			var active, peak atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || r.Method != "PUT" || !strings.HasPrefix(r.URL.Path, "/rest/api/3/issue/KEY-") || string(body) != input {
					t.Errorf("field route changed exact request: %s %s %s", r.Method, r.URL, body)
					w.WriteHeader(400)
					return
				}
				writes.Add(1)
				if flag == "parallelism" {
					current := active.Add(1)
					defer active.Add(-1)
					for {
						previous := peak.Load()
						if previous >= current || peak.CompareAndSwap(previous, current) {
							break
						}
					}
					// Keep each request open so an incorrect wider executor
					// exposes overlapping PUTs at the HTTP boundary.
					time.Sleep(40 * time.Millisecond)
				}
				w.WriteHeader(204)
			}))
			defer server.Close()
			inputFlag := "json-input"
			if flag == "from-json" || flag == "body-adf" {
				inputFlag = flag
			}
			args := []string{"issue", "move", "fields", "KEY-1..KEY-2", "--" + inputFlag + "=fields.json", "--no-input"}
			if flag == "force" || flag == "yes" {
				res := fwExec(t, server.URL, map[string]string{"fields.json": input}, "", args...)
				fwRefusal(t, res, "flag_required", "force")
				if writes.Load() != 0 {
					t.Fatal("missing force reached PUT")
				}
			}
			forceFlag := "force"
			if flag == "yes" {
				forceFlag = "yes"
			}
			args = append(args, "--"+forceFlag)
			if flag == "parallelism" {
				res := fwExec(t, server.URL, map[string]string{"fields.json": input}, "", append(args, "--parallelism=0")...)
				fwRefusal(t, res, "flag_value_invalid", "parallelism")
				if writes.Load() != 0 {
					t.Fatal("invalid parallelism reached PUT")
				}
				args = append(args, "--parallelism=1")
			}
			wantWrites := int32(2)
			if flag == "dry-run" {
				args = append(args, "--dry-run")
				wantWrites = 0
			}
			res := fwExec(t, server.URL, map[string]string{"fields.json": input}, "", args...)
			out := fwParse(t, res)
			if res.Exit != 0 || !out.OK || writes.Load() != wantWrites || fwAt(t, out.Data, "dry_run") != (flag == "dry-run") || fwAt(t, out.Data, "payload", "fields", "summary") != "Confirmed field change" || fwAt(t, out.Data, "results", 0, "issue", "key") != "KEY-1" || fwAt(t, out.Data, "results", 1, "issue", "key") != "KEY-2" {
				t.Fatalf("field route flag had no verified effect: %s exit=%d writes=%d stdout=%s", flag, res.Exit, writes.Load(), res.Stdout)
			}
			if flag == "parallelism" && peak.Load() != 1 {
				t.Fatalf("--parallelism=1 exceeded one in-flight PUT: %d", peak.Load())
			}
			Prove(t, "jira issue move fields", flag, "exact native PUTs, ordered expanded keys, preview laziness, force refusal and execution-bound validation asserted through the binary")
		})
	}
}
