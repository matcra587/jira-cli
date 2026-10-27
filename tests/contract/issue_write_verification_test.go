package contract

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestIssueWriteVerifyChecksRequestedNativeFields(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "compact"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var writes, reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodPut:
					writes.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
					w.WriteHeader(http.StatusNoContent)
				case http.MethodGet:
					reads.Add(1)
					fields := strings.Split(r.URL.Query().Get("fields"), ",")
					if len(fields) != 6 {
						t.Error("verification broadened or duplicated field selectors")
					}
					for _, field := range []string{"summary", "status", "updated", "priority", "labels", "customfield_10042"} {
						found := false
						for _, candidate := range fields {
							found = found || candidate == field
						}
						if !found {
							t.Errorf("verification did not request authored field %q", field)
						}
					}
					_, _ = io.WriteString(w, `{"key":"KEY-1","fields":{"summary":"stored","status":{"name":"Open"},"updated":"native-time","priority":{"id":"2","name":"Low"},"labels":["old"],"customfield_10042":9007199254740992}}`)
				default:
					t.Error("unexpected verification request")
				}
			}))
			defer server.Close()
			input := apiInput(t, []byte(`{"fields":{"summary":"authored","priority":{"name":"High"},"customfield_10042":9007199254740993},"update":{"labels":[{"add":"new"}]}}`))
			stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "edit", "KEY-1", "--json-input", input, "--verify", "--no-input", "--output="+mode)
			var out struct {
				Results []struct {
					OK   bool `json:"ok"`
					Data struct {
						VerifyFailed bool `json:"verify_failed"`
						Verified     struct {
							Summary string                        `json:"summary"`
							Fields  []core.IssueFieldVerification `json:"fields"`
						} `json:"verified"`
					} `json:"data"`
				} `json:"results"`
			}
			if exit != 0 || stderr != "" || json.Unmarshal(adfData(t, stdout, mode, "issue.edit", true), &out) != nil || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Data.VerifyFailed {
				t.Fatalf("readback differences failed or lost the acknowledged write: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
			}
			got := out.Results[0].Data.Verified
			var names []string
			for _, row := range got.Fields {
				names = append(names, row.Field)
				if row.Outcome != "differs" || !row.Observed.Set {
					t.Fatalf("requested field mismatch was hidden: %+v", row)
				}
			}
			if !reflect.DeepEqual(names, []string{"customfield_10042", "labels", "priority", "summary"}) || got.Summary != "stored" || writes.Load() != 1 || reads.Load() != 1 {
				t.Fatalf("verification omitted fields, rounded a number or replayed the write: fields=%v writes=%d reads=%d", names, writes.Load(), reads.Load())
			}
			if mode == "json" {
				var frame struct {
					Warnings []envelope.Warning `json:"warnings"`
				}
				if json.Unmarshal([]byte(stdout), &frame) != nil || len(frame.Warnings) != 4 {
					t.Fatal("field differences lost their machine warnings")
				}
				for _, warning := range frame.Warnings {
					if warning.Type != "field_value_differs" || warning.Lossy {
						t.Fatal("observed differences claimed content loss or changed warning type")
					}
				}
			}
		})
	}
}

func TestIssueWriteVerificationUsesExactNativeFieldsBeforeTypedProjection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, wanted, observed, outcome string
	}{
		{"nested case alias", `{"name":"Low"}`, `{"name":"High","Name":"Low"}`, "differs"},
		{"modeled numeric spelling", `{"name":"High","avatarId":0}`, `{"name":"High","avatarId":-0}`, "differs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					w.WriteHeader(http.StatusNoContent)
				} else {
					_, _ = io.WriteString(w, `{"key":"KEY-1","fields":{"status":{"name":"Open","Name":"Wrong"},"priority":`+tc.observed+`}}`)
				}
			}))
			defer server.Close()
			input := apiInput(t, []byte(`{"fields":{"priority":`+tc.wanted+`}}`))
			stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "edit", "KEY-1", "--json-input", input, "--verify", "--output=json", "--no-input")
			var out envelope.IssueEditOutput
			if exit != 0 || json.Unmarshal(adfData(t, stdout, "json", "issue.edit", true), &out) != nil || len(out.Results) != 1 || out.Results[0].Data == nil || out.Results[0].Data.Verified == nil {
				t.Fatal("native field readback was not published")
			}
			verified := out.Results[0].Data.Verified
			if len(verified.Fields) != 1 || verified.Fields[0].Outcome != tc.outcome || string(verified.Fields[0].Observed.Value) != tc.observed || verified.Status != jira.NullableOf("Open") {
				t.Fatalf("typed decoding replaced native evidence: %+v", verified)
			}
		})
	}
}

func TestIssueWriteVerificationDoesNotReadWithoutSuccessfulFlaggedMutation(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"create", "edit"} {
		for _, tc := range []struct {
			name   string
			verify bool
			status int
			exit   int
		}{
			{"no verify", false, http.StatusOK, 0},
			{"failed write", true, http.StatusForbidden, cli.ExitAuth},
		} {
			t.Run(verb+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				var writes, reads atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						reads.Add(1)
						t.Error("unrequested or failed-write verification read occurred")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					writes.Add(1)
					if tc.status == http.StatusForbidden {
						w.WriteHeader(tc.status)
						_, _ = io.WriteString(w, `{"errorMessages":["denied"]}`)
					} else if verb == "create" {
						w.WriteHeader(http.StatusCreated)
						_, _ = io.WriteString(w, `{"id":"1","key":"KEY-1"}`)
					} else {
						w.WriteHeader(http.StatusNoContent)
					}
				}))
				defer server.Close()
				args := []string{"issue", verb}
				if verb == "create" {
					args = append(args, "--project=KEY", "--type=Task")
				} else {
					args = append(args, "KEY-1")
				}
				args = append(args, "--summary=authored", "--output=json", "--no-input")
				if tc.verify {
					args = append(args, "--verify")
				}
				stdout, _, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
				if exit != tc.exit || writes.Load() != 1 || reads.Load() != 0 || strings.Contains(stdout, `"verified":`) {
					t.Fatalf("verification changed request admission or outcome: exit=%d writes=%d reads=%d stdout=%s", exit, writes.Load(), reads.Load(), stdout)
				}
			})
		}
	}
}

func TestIssueWriteVerificationRetainsAcknowledgementOnIncompleteReadback(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"create", "edit"} {
		for _, tc := range []struct {
			name, body string
			status     int
			failed     bool
		}{
			{"missing authored field", `{"key":"KEY-1","fields":{"status":{"name":"Open"}}}`, http.StatusOK, false},
			{"absent fields", `{"key":"KEY-1"}`, http.StatusOK, true},
			{"null fields", `{"key":"KEY-1","fields":null}`, http.StatusOK, true},
			{"duplicate nested members", `{"key":"KEY-1","fields":{"summary":"authored","customfield_1":{"id":"1","id":"2"}}}`, http.StatusOK, true},
			{"trailing document", `{"key":"KEY-1","fields":{"summary":"authored"}} {}`, http.StatusOK, true},
			{"case ambiguous fields", `{"key":"KEY-1","fields":{"summary":"authored","Summary":"other"}}`, http.StatusOK, true},
			{"HTTP refusal", `{"errorMessages":["not available"]}`, http.StatusForbidden, true},
		} {
			for _, mode := range []string{"json", "compact"} {
				t.Run(verb+"/"+tc.name+"/"+mode, func(t *testing.T) {
					t.Parallel()
					var writes, reads atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method == http.MethodGet {
							reads.Add(1)
							w.WriteHeader(tc.status)
							_, _ = io.WriteString(w, tc.body)
						} else {
							writes.Add(1)
							if verb == "create" {
								w.WriteHeader(http.StatusCreated)
								_, _ = io.WriteString(w, `{"id":"1","key":"KEY-1"}`)
							} else {
								w.WriteHeader(http.StatusNoContent)
							}
						}
					}))
					defer server.Close()
					args := []string{"issue", verb}
					if verb == "create" {
						args = append(args, "--project=KEY", "--type=Task")
					} else {
						args = append(args, "KEY-1")
					}
					args = append(args, "--summary=authored", "--verify", "--no-input", "--output="+mode)
					stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
					data := adfData(t, stdout, mode, "issue."+verb, true)
					var verified *envelope.VerifiedIssue
					var failed bool
					if verb == "create" {
						var out envelope.IssueCreateOutput
						if json.Unmarshal(data, &out) != nil || !out.Applied || out.Issue == nil || out.Issue.Key != "KEY-1" {
							t.Fatal("incomplete readback lost acknowledged creation")
						}
						verified, failed = out.Verified, out.VerifyFailed
					} else {
						var out envelope.IssueEditOutput
						if json.Unmarshal(data, &out) != nil || len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Data == nil {
							t.Fatal("incomplete readback lost acknowledged edit")
						}
						verified, failed = out.Results[0].Data.Verified, out.Results[0].Data.VerifyFailed
					}
					if exit != 0 || stderr != "" || failed != tc.failed || (verified == nil) != tc.failed || writes.Load() != 1 || reads.Load() != 1 {
						t.Fatalf("readback failure lost acknowledgement or replayed: exit=%d failure=%t writes=%d reads=%d stderr=%s", exit, failed, writes.Load(), reads.Load(), stderr)
					}
					if !tc.failed {
						found := false
						for _, row := range verified.Fields {
							if row.Field == "summary" {
								found = row.Outcome == "unverified" && row.Reason == "field-not-returned" && !row.Observed.Set
							}
						}
						if !found {
							t.Fatal("missing field falsely became a match or difference")
						}
					}
					if mode == "json" {
						var frame struct {
							Warnings []envelope.Warning `json:"warnings"`
						}
						if json.Unmarshal([]byte(stdout), &frame) != nil || len(frame.Warnings) == 0 {
							t.Fatal("incomplete verification lost its machine warning")
						}
					}
				})
			}
		}
	}
}

func TestIssueWriteVerificationMetadataPresence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, fields, wantSummary, wantStatus, human string }{
		{name: "absent", fields: `{}`, human: "Readback metadata unavailable."},
		{name: "summary only", fields: `{"summary":"authored"}`, wantSummary: `"authored"`, human: "Summary:  authored"},
		{name: "status only", fields: `{"status":{"name":"Open"}}`, wantStatus: `"Open"`, human: "Status:  Open"},
		{name: "null", fields: `{"summary":null,"status":null,"updated":null}`, wantSummary: `null`, wantStatus: `null`, human: "Summary:  -"},
		{name: "empty", fields: `{"summary":"","status":{"name":""},"updated":""}`, wantSummary: `""`, wantStatus: `""`, human: "Summary:  -"},
		{name: "missing status name", fields: `{"status":{"id":"1"}}`, human: "Readback metadata unavailable."},
		{name: "null status name", fields: `{"status":{"name":null}}`, wantStatus: `null`, human: "Status:  -"},
	} {
		for _, verb := range []string{"create", "edit"} {
			for _, mode := range []string{"human", "json", "compact"} {
				t.Run(tc.name+"/"+verb+"/"+mode, func(t *testing.T) {
					t.Parallel()
					var writes, reads atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method == http.MethodGet {
							reads.Add(1)
							if r.URL.Path != "/rest/api/3/issue/KEY-1" {
								t.Errorf("readback path = %s", r.URL.Path)
							}
							_, _ = io.WriteString(w, `{"id":"1","key":"KEY-1","fields":`+tc.fields+`}`)
							return
						}
						writes.Add(1)
						if verb == "create" {
							if r.Method != http.MethodPost || r.URL.Path != "/rest/api/3/issue" {
								t.Errorf("create = %s %s", r.Method, r.URL)
							}
							w.WriteHeader(http.StatusCreated)
							_, _ = io.WriteString(w, `{"id":"1","key":"KEY-1"}`)
						} else {
							if r.Method != http.MethodPut || r.URL.Path != "/rest/api/3/issue/KEY-1" {
								t.Errorf("edit = %s %s", r.Method, r.URL)
							}
							w.WriteHeader(http.StatusNoContent)
						}
					}))
					t.Cleanup(server.Close)
					args := []string{"issue", verb}
					if verb == "create" {
						args = append(args, "--project=KEY", "--type=Task")
					} else {
						args = append(args, "KEY-1")
					}
					args = append(args, "--summary=authored", "--verify", "--no-input", "--color=never", "--output="+mode)
					stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), args...)
					if exit != 0 || writes.Load() != 1 || reads.Load() != 1 {
						t.Fatalf("verify: exit=%d writes=%d reads=%d stdout=%q stderr=%q", exit, writes.Load(), reads.Load(), stdout, stderr)
					}
					if mode == "human" {
						if !strings.Contains(stdout, tc.human) || strings.Contains(stdout, `reads back as ""`) || strings.Contains(stdout, "status .") {
							t.Fatalf("readback = %q; want %q", stdout, tc.human)
						}
						if tc.wantSummary == "" && strings.Contains(stdout, "Summary:") {
							t.Fatalf("invented summary: %q", stdout)
						}
						if tc.wantStatus == "" && strings.Contains(stdout, "Status:") {
							t.Fatalf("invented status: %q", stdout)
						}
						if tc.wantSummary == "" && (!strings.Contains(stderr, `field "summary" remains unverified`) || !strings.Contains(stderr, "field-not-returned")) {
							t.Fatalf("missing summary warning = %q", stderr)
						}
						if tc.wantSummary == `"authored"` && verb == "edit" && stderr != "" {
							t.Fatalf("successful readback stderr = %q", stderr)
						}
						return
					}
					if stderr != "" {
						t.Fatalf("machine stderr = %q", stderr)
					}
					var data map[string]json.RawMessage
					if err := json.Unmarshal(adfData(t, stdout, mode, "issue."+verb, true), &data); err != nil {
						t.Fatal(err)
					}
					if verb == "edit" {
						var rows []struct {
							OK   bool
							Data map[string]json.RawMessage
						}
						if err := json.Unmarshal(data["results"], &rows); err != nil {
							t.Fatal(err)
						}
						if len(rows) != 1 || !rows[0].OK {
							t.Fatalf("edit results = %s", data["results"])
						}
						data = rows[0].Data
					}
					var metadata map[string]json.RawMessage
					if err := json.Unmarshal(data["verified"], &metadata); err != nil {
						t.Fatal(err)
					}
					for field, want := range map[string]string{"summary": tc.wantSummary, "status": tc.wantStatus} {
						if got := string(metadata[field]); got != want {
							t.Errorf("verified %s = %q; want %q", field, got, want)
						}
					}
					var native map[string]json.RawMessage
					if err := json.Unmarshal([]byte(tc.fields), &native); err != nil {
						t.Fatal(err)
					}
					if string(metadata["updated"]) != string(native["updated"]) {
						t.Errorf("updated = %s; want %s", metadata["updated"], native["updated"])
					}
				})
			}
		}
	}
}

func TestIssueWriteVerificationReportsHumanDifferencesWithoutRetryAdvice(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusNoContent)
		} else {
			_, _ = io.WriteString(w, `{"key":"KEY-1","fields":{"summary":"stored","status":{"name":"Open"}}}`)
		}
	}))
	defer server.Close()
	stdout, stderr, exit := runBinaryEnv(t, editReceiptEnv(server.URL), "issue", "edit", "KEY-1", "--summary=authored", "--verify", "--output=human", "--no-input", "--color=never")
	if exit != 0 || !strings.Contains(stdout, "updated") || !strings.Contains(stdout, "Field readback: 0 match, 1 differ, 0 unverified.") || !strings.Contains(stderr, "field") || strings.Contains(strings.ToLower(stdout+stderr), "retry the write") {
		t.Fatalf("human verification hid a difference or encouraged replay: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
	}
}
