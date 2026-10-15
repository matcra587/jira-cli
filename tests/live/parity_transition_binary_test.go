//go:build live

package live

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"testing"
)

func TestParityActualBinaryCommentClose(t *testing.T) {
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		for _, scenario := range []struct {
			name                       string
			screen, failMove, failNote bool
		}{{"screened", true, false, false}, {"screenless", false, false, false}, {"comment_failure", false, false, true}, {"transition_failure", true, true, false}} {
			t.Run(lane.name+"/"+scenario.name, func(t *testing.T) {
				if lane.binary == "" {
					t.Skip("requires selected actual binary")
				}
				if err := parityFingerprint(lane.binary, lane.digest); err != nil {
					t.Fatal(err)
				}
				const marker = "Closed with paired evidence café."
				body := []byte(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Closed with paired evidence café."}]}]}`)
				var mu sync.Mutex
				var requests []string
				var moved, commented bool
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					requests = append(requests, r.Method+" "+r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					if user, token, ok := r.BasicAuth(); !ok || user != "fixture@example.invalid" || token != "synthetic-fixture-token" {
						http.Error(w, "wrong synthetic binding", http.StatusUnauthorized)
						return
					}
					if r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/PAIR-1/transitions" {
						_, _ = fmt.Fprintf(w, `{"transitions":[{"id":"31","name":"Close","hasScreen":%t,"isAvailable":true,"to":{"id":"100","name":"Done","statusCategory":{"key":"done"}},"fields":{"comment":{"required":false,"name":"Comment","key":"comment","schema":{"type":"comments","system":"comment"},"operations":["add"]}}}]}`, scenario.screen)
						return
					}
					data, err := io.ReadAll(io.LimitReader(r.Body, 8193))
					if err != nil || len(data) > 8192 {
						t.Error("compound writer body unavailable or too large")
					}
					switch r.Method + " " + r.URL.Path {
					case "POST /rest/api/3/issue/PAIR-1/transitions":
						want := []byte(`{"transition":{"id":"31"}}`)
						if scenario.screen {
							want = []byte(`{"transition":{"id":"31"},"update":{"comment":[{"add":{"body":` + string(body) + `}}]}}`)
						}
						if !nativeMetadataMatches(data, want, nil) || moved {
							t.Errorf("compound transition changed native request or replayed: %s", data)
						}
						if scenario.failMove {
							w.WriteHeader(http.StatusBadRequest)
							_, _ = w.Write([]byte(`{"errorMessages":["fixture transition refused"]}`))
							return
						}
						moved = true
						commented = scenario.screen
						w.WriteHeader(http.StatusNoContent)
					case "POST /rest/api/3/issue/PAIR-1/comment":
						if !moved || commented || scenario.screen || !nativeMetadataMatches(data, []byte(`{"body":`+string(body)+`}`), nil) {
							t.Error("separate comment reordered, replayed or changed its body")
						}
						if scenario.failNote {
							w.WriteHeader(http.StatusForbidden)
							_, _ = w.Write([]byte(`{"errorMessages":["fixture comment refused"]}`))
							return
						}
						commented = true
						w.WriteHeader(http.StatusCreated)
						_, _ = fmt.Fprintf(w, `{"id":"10001","body":%s}`, body)
					default:
						t.Error("unexpected compound route: ", r.Method, " ", r.URL.Path)
						http.Error(w, "unexpected fixture route", http.StatusBadRequest)
					}
				}))
				t.Cleanup(server.Close)
				home := t.TempDir()
				invoke := func(environment, options []string) (liveFrame, []byte, int, []string) {
					t.Helper()
					args, err := parityCloseArgs(lane.name, "PAIR-1", "31", marker, options)
					if err != nil {
						t.Fatal(err)
					}
					mu.Lock()
					start := len(requests)
					mu.Unlock()
					frame, stdout, exit := parityFixtureRunAt(t, lane.name, lane.binary, server.URL, home, environment, args...)
					if err := parityValidateFrame(stdout, lane.name, "issue.transition", exit); err != nil {
						t.Fatalf("compound envelope invalid: %v, %s", err, stdout)
					}
					mu.Lock()
					observed := slices.Clone(requests[start:])
					mu.Unlock()
					return frame, stdout, exit, observed
				}
				target := parityCloseTarget{"31", "100", "Done", scenario.screen}
				frame, stdout, exit, observed := invoke(nil, []string{"--dry-run"})
				if exit != 0 || !frame.OK || len(observed) != 0 || !parityCloseReceipt(stdout, lane.name, "PAIR-1", target, body, true, false, "") {
					t.Fatalf("local compound preview mismatch: exit %d, HTTP %v, %s", exit, observed, stdout)
				}
				frame, stdout, exit, observed = invoke(nil, []string{"--dry-run", "--validate-remote"})
				if !slices.Equal(observed, []string{"GET /rest/api/3/issue/PAIR-1/transitions"}) {
					t.Fatal("validated compound preview did not perform exactly one workflow read")
				}
				if lane.name == "before" && !scenario.screen {
					if exit != 3 || frame.OK || !parityCloseLegacyRefusal(stdout, "31") {
						t.Fatalf("legacy screenless preview has an unrelated refusal: %s", stdout)
					}
				} else {
					route := "separate"
					if scenario.screen {
						route = "atomic"
					}
					if exit != 0 || !frame.OK || !parityCloseReceipt(stdout, lane.name, "PAIR-1", target, body, true, true, route) {
						t.Fatalf("validated compound preview route mismatch: %s", stdout)
					}
				}
				if lane.name == "after" && !scenario.screen {
					frame, _, exit, observed = invoke(nil, []string{"--atomic-only"})
					if exit != 3 || frame.OK || len(frame.Data.Results) != 1 || frame.Data.Results[0].Issue.Key != "PAIR-1" || frame.Data.Results[0].OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "flag_not_applicable" || !slices.Equal(observed, []string{"GET /rest/api/3/issue/PAIR-1/transitions"}) {
						t.Fatal("atomic-only did not refuse before either native write")
					}
				}
				frame, _, exit, observed = invoke([]string{"JIRA_READ_ONLY=true"}, nil)
				code := parityCommentReadOnlyCode(lane.name)
				if lane.name == "before" && !scenario.screen {
					code = "validation_failed"
				}
				if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != code {
					t.Fatalf("compound read-only refusal mismatch: exit %d, HTTP %v", exit, observed)
				}
				for _, request := range observed {
					if request != "GET /rest/api/3/issue/PAIR-1/transitions" {
						t.Fatal("read-only compound request entered a writer: ", observed)
					}
				}
				frame, stdout, exit, observed = invoke(nil, nil)
				want := []string{"GET /rest/api/3/issue/PAIR-1/transitions"}
				refused := lane.name == "before" && !scenario.screen
				if !refused {
					want = append(want, "POST /rest/api/3/issue/PAIR-1/transitions")
					if !scenario.screen && !scenario.failMove {
						want = append(want, "POST /rest/api/3/issue/PAIR-1/comment")
					}
				}
				if !slices.Equal(observed, want) {
					t.Fatalf("compound HTTP sequence differs: got %v, want %v", observed, want)
				}
				mu.Lock()
				gotMoved, gotCommented := moved, commented
				mu.Unlock()
				if gotMoved != (!refused && !scenario.failMove) || gotCommented != (!refused && !scenario.failMove && !scenario.failNote) {
					t.Fatal("compound native effects differ from acknowledgement and comment outcome")
				}
				if refused {
					if exit != 3 || frame.OK || !parityCloseLegacyRefusal(stdout, "31") {
						t.Fatal("legacy compound refusal lost its specific screenless diagnostic")
					}
				} else if scenario.failMove {
					if exit == 0 || frame.OK {
						t.Fatal("refused compound write reported success")
					}
					if lane.name == "after" && !parityCloseReceipt(stdout, lane.name, "PAIR-1", target, body, false, false, "move_failed") {
						t.Fatalf("failed native transition lost its bound attempted/unapplied receipt: %s", stdout)
					}
				} else if scenario.failNote {
					if exit != 0 || !frame.OK || !parityCloseReceipt(stdout, lane.name, "PAIR-1", target, body, false, false, "failed") {
						t.Fatalf("comment failure erased the acknowledged move: %s", stdout)
					}
				} else {
					route := "atomic"
					if !scenario.screen {
						route = "separate"
					}
					if exit != 0 || !frame.OK || !parityCloseReceipt(stdout, lane.name, "PAIR-1", target, body, false, false, route) {
						t.Fatalf("compound success receipt mismatch: exit %d, %s", exit, stdout)
					}
				}
			})
		}
	}
}
