package contract

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestIssueFieldPreflightRefusesUnavailableFieldsBeforeWrites(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"create", "edit"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := newInputDiscoveryServer(t, &calls)
			payload := `{"fields":{"customfield_99999":{"future":9007199254740993}}}`
			args := []string{"issue", verb}
			if verb == "create" {
				args = append(args, "--project=KEY", "--type=Task", "--summary=Fixture")
			} else {
				args = append(args, "KEY-1")
			}
			args = append(args, "--json-input", apiInput(t, []byte(payload)), "--dry-run", "--validate-remote", "--output=json")
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			var frame apiFrame
			if json.Unmarshal([]byte(stdout), &frame) != nil || exit != 3 || frame.OK || calls.Load() == 0 {
				t.Fatalf("explicit field preflight skipped the live screen: exit=%d reads=%d stdout=%q stderr=%q", exit, calls.Load(), stdout, stderr)
			}
		})
	}
}

func TestIssueFieldPreflightAdmitsNativeValuesWithoutChangingPayload(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"create", "edit"} {
		for _, mode := range []string{"json", "compact"} {
			for _, live := range []bool{false, true} {
				t.Run(verb+"/"+mode+"/live="+strconv.FormatBool(live), func(t *testing.T) {
					t.Parallel()
					var reads, writes atomic.Int32
					metadata := newInputDiscoveryServer(t, &reads)
					var expected atomic.Value
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method == http.MethodGet {
							metadata.Config.Handler.ServeHTTP(w, r)
							return
						}
						writes.Add(1)
						body, err := io.ReadAll(r.Body)
						if err != nil || !bytes.Equal(body, expected.Load().([]byte)) {
							t.Error("field metadata preflight changed native request bytes")
						}
						if verb == "create" && r.Method == http.MethodPost && r.URL.Path == "/rest/api/3/issue" {
							w.WriteHeader(http.StatusCreated)
							_, _ = io.WriteString(w, `{"id":"10001","key":"KEY-1","self":"https://example.invalid/issue/10001"}`)
						} else if verb == "edit" && r.Method == http.MethodPut && r.URL.Path == "/rest/api/3/issue/KEY-1" {
							w.WriteHeader(http.StatusNoContent)
						} else {
							t.Errorf("unexpected preflight write: %s %s", r.Method, r.URL.Path)
							w.WriteHeader(http.StatusBadRequest)
						}
					}))
					defer server.Close()
					payload := `{"fields":{"customfield_14002":{"futureValue":9007199254740993,"empty":[],"clear":null,"false":false}},"properties":[{"key":"fixture","value":{"future":false}}]}`
					args := []string{"issue", verb}
					if verb == "create" {
						args = append(args, "--project=KEY", "--type=Task", "--summary=Fixture")
					} else {
						args = append(args, "KEY-1")
					}
					args = append(args, "--json-input", apiInput(t, []byte(payload)), "--output="+mode)
					env := editReceiptEnv(server.URL)
					stdout, stderr, exit := runBinaryEnv(t, env, append(slices.Clone(args), "--dry-run")...)
					if exit != 0 || stderr != "" || reads.Load() != 0 || writes.Load() != 0 {
						t.Fatal("unflagged preview opened metadata or wrote")
					}
					var plain struct {
						Payload json.RawMessage `json:"payload"`
					}
					if json.Unmarshal(adfData(t, stdout, mode, "issue."+verb, true), &plain) != nil {
						t.Fatal("local preview lost payload")
					}
					// Establish expected bytes before starting the remote invocation.
					expected.Store([]byte(plain.Payload))
					remote := append(slices.Clone(args), "--validate-remote")
					if !live {
						remote = append(remote, "--dry-run")
					}
					stdout, stderr, exit = runBinaryEnv(t, env, remote...)
					if exit != 0 || stderr != "" {
						t.Fatalf("admitted preflight failed: exit=%d stderr=%q stdout=%q", exit, stderr, stdout)
					}
					raw := adfData(t, stdout, mode, "issue."+verb, true)
					var checked *envelope.IssueFieldPreflight
					if verb == "create" {
						var out envelope.IssueCreateOutput
						if json.Unmarshal(raw, &out) != nil || !bytes.Equal(out.Payload, expected.Load().([]byte)) || out.DryRun == live {
							t.Fatal("create preflight changed payload or mutation state")
						}
						checked = out.FieldPreflight
					} else {
						var out envelope.IssueEditOutput
						if json.Unmarshal(raw, &out) != nil || !bytes.Equal(out.Payload, expected.Load().([]byte)) || out.DryRun == live || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Data == nil {
							t.Fatal("edit preflight lost ordered native receipt")
						}
						checked = out.Results[0].Data.FieldPreflight
					}
					want := []string{"customfield_14002"}
					wantReads := int32(1)
					if verb == "create" {
						want = append(want, "summary")
						wantReads = 5
					}
					wantWrites := int32(0)
					if live {
						wantWrites = 1
					}
					if checked == nil || !slices.Equal(checked.Fields, want) || reads.Load() != wantReads || writes.Load() != wantWrites {
						t.Fatalf("incomplete/broadened preflight: checked=%+v reads=%d writes=%d", checked, reads.Load(), writes.Load())
					}
				})
			}
		}
	}
}

func TestIssueFieldPreflightRetainsOrderedPartialContextsWithoutRejectedWrites(t *testing.T) {
	t.Parallel()
	for _, live := range []bool{false, true} {
		t.Run("live="+strconv.FormatBool(live), func(t *testing.T) {
			t.Parallel()
			var reads, writes atomic.Int32
			metadata := newInputDiscoveryServer(t, &reads)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					metadata.Config.Handler.ServeHTTP(w, r)
					return
				}
				writes.Add(1)
				if r.Method != http.MethodPut || r.URL.Path != "/rest/api/3/issue/KEY-1" {
					t.Error("refused context received a write")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			args := []string{"issue", "edit", "KEY-1", "KEY-2", "--json-input", apiInput(t, []byte(`{"fields":{"customfield_14002":null}}`)), "--validate-remote", "--parallelism=2", "--output=json"}
			if !live {
				args = append(args, "--dry-run")
			}
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			var out envelope.IssueEditOutput
			if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, "json", "issue.edit", true), &out) != nil || len(out.Results) != 2 || !out.Results[0].OK || out.Results[1].OK || out.Results[0].Issue.Key != "KEY-1" || out.Results[1].Issue.Key != "KEY-2" || out.Results[0].Data.FieldPreflight == nil || out.Results[1].Err.Code != "issue_field_preflight_failed" {
				t.Fatalf("partial metadata outcomes changed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			wantWrites := int32(0)
			if live {
				wantWrites = 1
			}
			if reads.Load() != 2 || writes.Load() != wantWrites {
				t.Fatalf("partial metadata replayed or wrote refused key: reads=%d writes=%d", reads.Load(), writes.Load())
			}
		})
	}
}

func TestIssueFieldPreflightRejectsRedirectedMetadataBeforeWrites(t *testing.T) {
	t.Parallel()
	for _, resource := range []string{"project", "types", "fields", "edit"} {
		t.Run(resource, func(t *testing.T) {
			t.Parallel()
			var reads, writes, redirects atomic.Int32
			metadata := newInputDiscoveryServer(t, &reads)
			foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirects.Add(1)
				if r.Header.Get("Authorization") != "" {
					t.Error("metadata redirect leaked credentials")
				}
				// Generate the same valid native fixture while checking the actual
				// redirected request above remained credential-free.
				fixture := r.Clone(r.Context())
				fixture.SetBasicAuth("person@example.com", "fixture")
				metadata.Config.Handler.ServeHTTP(w, fixture)
			}))
			defer foreign.Close()
			path := "/rest/api/3/project/KEY"
			if resource == "types" {
				path = "/rest/api/3/issue/createmeta/10402/issuetypes"
			}
			if resource == "fields" {
				path = "/rest/api/3/issue/createmeta/10402/issuetypes/10002"
			}
			if resource == "edit" {
				path = "/rest/api/3/issue/KEY-1/editmeta"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				if r.URL.Path == path {
					http.Redirect(w, r, foreign.URL+r.URL.RequestURI(), http.StatusFound)
					return
				}
				metadata.Config.Handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			args := []string{"issue", "create", "--project=KEY", "--type=Task", "--summary=Fixture"}
			if resource == "edit" {
				args = []string{"issue", "edit", "KEY-1", "--json-input", apiInput(t, []byte(`{"fields":{"customfield_14002":null}}`))}
			}
			args = append(args, "--validate-remote", "--output=json", "--no-input")
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
			if exit != 5 || writes.Load() != 0 || redirects.Load() != 1 || !bytes.Contains([]byte(stdout), []byte("issue_metadata_response_invalid")) || bytes.Contains([]byte(stdout), []byte(`"field_preflight":`)) {
				t.Fatalf("redirected metadata authorized a write: exit=%d writes=%d redirects=%d stdout=%q stderr=%q", exit, writes.Load(), redirects.Load(), stdout, stderr)
			}
		})
	}
}

func TestIssueFieldPreflightRejectsInvalidMetadataAndOperationsBeforeWrites(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"create", "edit"} {
		for _, failure := range []string{"operation", "malformed", "forbidden"} {
			t.Run(verb+"/"+failure, func(t *testing.T) {
				t.Parallel()
				var reads, writes atomic.Int32
				metadata := newInputDiscoveryServer(t, &reads)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						writes.Add(1)
						w.WriteHeader(http.StatusNoContent)
						return
					}
					isField := r.URL.Path == "/rest/api/3/issue/createmeta/10402/issuetypes/10002" || r.URL.Path == "/rest/api/3/issue/KEY-1/editmeta"
					if !isField {
						metadata.Config.Handler.ServeHTTP(w, r)
						return
					}
					if failure == "forbidden" {
						reads.Add(1)
						w.WriteHeader(http.StatusForbidden)
						_, _ = io.WriteString(w, `{"errorMessages":["Fixture refusal"]}`)
						return
					}
					if failure == "malformed" {
						reads.Add(1)
						_, _ = io.WriteString(w, `{"fields":`)
						return
					}
					recorder := httptest.NewRecorder()
					metadata.Config.Handler.ServeHTTP(recorder, r)
					_, _ = w.Write(bytes.ReplaceAll(recorder.Body.Bytes(), []byte(`"operations":["set"]`), []byte(`"operations":[]`)))
				}))
				defer server.Close()
				args := []string{"issue", verb}
				if verb == "create" {
					args = append(args, "--project=KEY", "--type=Task", "--summary=Fixture")
				} else {
					args = append(args, "KEY-1")
				}
				args = append(args, "--json-input", apiInput(t, []byte(`{"fields":{"customfield_14002":null}}`)), "--validate-remote", "--output=json", "--no-input")
				stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				wantCode, wantExit := "issue_metadata_response_invalid", 5
				if failure == "operation" {
					wantCode, wantExit = "issue_field_preflight_failed", 3
				}
				if failure == "forbidden" {
					wantCode, wantExit = "forbidden", 1
				}
				if exit != wantExit || writes.Load() != 0 || reads.Load() == 0 || !bytes.Contains([]byte(stdout), []byte(wantCode)) {
					t.Fatalf("invalid metadata authorized write: exit=%d writes=%d stdout=%q stderr=%q", exit, writes.Load(), stdout, stderr)
				}
			})
		}
	}
}
