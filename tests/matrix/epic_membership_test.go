//go:build flagmatrix

package matrix

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEpicMembershipFlagEffects(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"add", "remove"} {
		for _, flag := range []string{"dry-run", "validate-remote", "verify", "notify", "skip-notify", "parallelism"} {
			t.Run(verb+" "+flag, func(t *testing.T) {
				t.Parallel()
				var reads, writes, active, peak atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" {
						reads.Add(1)
						switch {
						case strings.HasSuffix(r.URL.Path, "KEY-100"):
							_, _ = fmt.Fprint(w, `{"fields":{"issuetype":{"name":"Epic","subtask":false,"hierarchyLevel":1}}}`)
						case verb == "remove" && r.URL.Query().Get("fields") == "parent,summary":
							_, _ = fmt.Fprint(w, `{"fields":{"parent":null}}`)
						default:
							_, _ = fmt.Fprint(w, `{"fields":{"parent":{"key":"KEY-100"},"issuetype":{"subtask":false}}}`)
						}
						return
					}
					if r.Method != "PUT" {
						t.Error("unexpected membership method")
						w.WriteHeader(400)
						return
					}
					writes.Add(1)
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("bad parent payload")
					}
					wantNotify := ""
					if flag == "notify" || flag == "skip-notify" {
						wantNotify = "false"
					}
					if r.URL.Query().Get("notifyUsers") != wantNotify {
						t.Error("notification flag ignored")
					}
					n := active.Add(1)
					for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
					}
					if flag == "parallelism" {
						time.Sleep(100 * time.Millisecond)
					}
					active.Add(-1)
					w.WriteHeader(204)
				}))
				defer server.Close()
				args := []string{"epic", verb, "KEY-1"}
				if flag == "parallelism" {
					args = append(args, "KEY-2")
				}
				if verb == "add" {
					args = append(args, "KEY-100")
				}
				args = append(args, "--no-input")
				switch flag {
				case "validate-remote":
					args = append(args, "--dry-run", "--validate-remote")
				case "notify":
					args = append(args, "--notify=false")
				case "parallelism":
					args = append(args, "--parallelism=1")
				default:
					args = append(args, "--"+flag)
				}
				r := fwExec(t, server.URL, nil, "", args...)
				out := fwParse(t, r)
				wantWrites, wantReads := int32(1), int32(1)
				if verb == "remove" {
					wantReads = 2
				}
				if flag == "dry-run" {
					wantWrites, wantReads = 0, 0
				}
				if flag == "validate-remote" {
					wantWrites = 0
				}
				if flag == "verify" {
					wantReads++
				}
				if flag == "parallelism" {
					wantWrites = 2
					if verb == "remove" {
						wantReads = 4
					}
				}
				if r.Exit != 0 || writes.Load() != wantWrites || reads.Load() != wantReads || (flag == "parallelism" && peak.Load() != 1) {
					t.Fatalf("flag effect missing: %s exit=%d reads=%d writes=%d peak=%d output=%s", flag, r.Exit, reads.Load(), writes.Load(), peak.Load(), r.Stdout)
				}
				if flag == "verify" && (fwAt(t, out.Data, "results", 0, "data", "parent", "state") != map[string]string{"add": "value", "remove": "null"}[verb] || fwAt(t, out.Data, "results", 0, "data", "verify_failed") != false) {
					t.Fatal("verify made a request without publishing its observation")
				}
				if flag == "validate-remote" && (fwAt(t, out.Data, "dry_run") != true || fwAt(t, out.Data, "results", 0, "data", "remote_validated") != true || fwAt(t, out.Data, "results", 0, "data", "applied") != false) {
					t.Fatal("remote preview did not publish the validated non-write")
				}
				Prove(t, "jira epic "+verb, flag, "exact read/write counts, notification query, readback observation and serial request bound verified through the binary")
			})
		}
	}
}
