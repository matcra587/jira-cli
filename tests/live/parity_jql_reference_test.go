//go:build live

package live

import (
	"encoding/json"
	"encoding/json/jsontext"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

const parityReferenceFixture = `{"visibleFieldNames":[{"value":"cf[10001]","displayName":"Owned field","cfid":"cf[10001]","auto":"true","orderable":"false","searchable":"true","operators":["=","in"],"types":["java.lang.String"],"future":{"number":900719925474099312345}}],"visibleFunctionNames":[{"value":"currentUser()","displayName":"Current user","isList":"false","types":["User"]}],"jqlReservedWords":["and","or"],"future":{"number":900719925474099312345}}`

const parityReferenceNullableFixture = `{"visibleFieldNames":[{"value":"summary","displayName":null,"cfid":null}],"visibleFunctionNames":[{"value":"currentUser()"}],"jqlReservedWords":["and","or"]}`

const parityReferenceSummary = `"fields":[{"value":"cf[10001]","display_name":"Owned field","custom_field_id":"cf[10001]"}],"functions":[{"value":"currentUser()","display_name":"Current user"}],"reserved_words":["and","or"]`

// Summaries have intentionally different presence contracts. Legacy strings
// flatten absent/null to empty and omit an empty cfid; current preserves each
// native optional member and the complete reference object separately.
func parityReferenceProjection(data, native []byte, lane string) bool {
	if (lane != "before" && lane != "after") || !jsontext.Value(data).IsValid() || !jsontext.Value(native).IsValid() {
		return false
	}
	var source, actual map[string]json.RawMessage
	if json.Unmarshal(native, &source) != nil || source == nil || json.Unmarshal(data, &actual) != nil || actual == nil {
		return false
	}
	var words, actualWords []string
	if json.Unmarshal(source["jqlReservedWords"], &words) != nil || len(words) == 0 || json.Unmarshal(actual["reserved_words"], &actualWords) != nil {
		return false
	}
	// Jira can reorder reserved words between independent reads. Compare their
	// multiset, retaining duplicates; field/function order remains significant.
	slices.Sort(words)
	slices.Sort(actualWords)
	if !slices.Equal(words, actualWords) {
		return false
	}
	want := map[string]any{"reserved_words": actual["reserved_words"]}
	for _, family := range []struct{ native, summary string }{{"visibleFieldNames", "fields"}, {"visibleFunctionNames", "functions"}} {
		var entries []map[string]json.RawMessage
		if json.Unmarshal(source[family.native], &entries) != nil || len(entries) == 0 {
			return false
		}
		projected := make([]map[string]json.RawMessage, len(entries))
		for i, entry := range entries {
			if entry == nil {
				return false
			}
			projected[i] = make(map[string]json.RawMessage)
			names := []struct{ native, summary string }{{"value", "value"}, {"displayName", "display_name"}}
			if family.summary == "fields" {
				names = append(names, struct{ native, summary string }{"cfid", "custom_field_id"})
			}
			for _, name := range names {
				raw, present := entry[name.native]
				var text string
				if present && string(raw) != "null" && json.Unmarshal(raw, &text) != nil {
					return false
				}
				if lane == "after" {
					if present {
						projected[i][name.summary] = raw
					}
				} else if name.summary != "custom_field_id" || text != "" {
					projected[i][name.summary], _ = json.Marshal(text)
				}
			}
		}
		want[family.summary] = projected
	}
	if lane == "after" {
		var ownReference map[string]json.RawMessage
		if json.Unmarshal(actual["reference"], &ownReference) != nil || !parityAuthoringJSON(actual["reserved_words"], ownReference["jqlReservedWords"]) || !jqlReferenceReadsEquivalent(native, actual["reference"]) {
			return false
		}
		want["reference"] = actual["reference"]
	}
	expected, err := json.Marshal(want)
	return err == nil && parityAuthoringJSON(data, expected)
}

// Fixed responses cannot reorder between observations. Keep the independent
// live-read tolerance out of synthetic transport and preservation acceptance.
func parityReferenceFixtureProjection(data, native []byte, lane string) bool {
	if !parityReferenceProjection(data, native, lane) {
		return false
	}
	var source, actual map[string]json.RawMessage
	if json.Unmarshal(native, &source) != nil || json.Unmarshal(data, &actual) != nil || !parityAuthoringJSON(actual["reserved_words"], source["jqlReservedWords"]) {
		return false
	}
	return lane == "before" || parityAuthoringJSON(actual["reference"], native)
}

func TestParityReferenceProjectionRequiresNativeData(t *testing.T) {
	for _, lane := range []string{"before", "after"} {
		t.Run(lane, func(t *testing.T) {
			data := `{` + parityReferenceSummary + `}`
			if lane == "after" {
				data = `{` + parityReferenceSummary + `,"reference":` + parityReferenceFixture + `}`
			}
			if !parityReferenceFixtureProjection([]byte(data), []byte(parityReferenceFixture), lane) {
				t.Fatal("complete independently authored reference refused")
			}
			for _, changed := range []string{`{}`, strings.Replace(data, `"Owned field"`, `"other"`, 1), strings.Replace(data, `["and","or"]`, `["and","and"]`, 1), strings.Replace(data, `"functions":[{`, `"functions":[{}, {`, 1), strings.Replace(data, `"value":"cf[10001]"`, `"value":"wrong","value":"cf[10001]"`, 1)} {
				if parityReferenceProjection([]byte(changed), []byte(parityReferenceFixture), lane) {
					t.Fatal("reference oracle admitted missing, changed or duplicate metadata")
				}
			}
			if lane == "after" {
				for _, changed := range []string{strings.Replace(data, `900719925474099312345`, `900719925474099300000`, 1), strings.Replace(data, `"orderable":"false"`, `"orderable":false`, 1), strings.Replace(data, `,"future":{"number":900719925474099312345}`, ``, 1)} {
					if parityReferenceProjection([]byte(changed), []byte(parityReferenceFixture), lane) {
						t.Fatal("current discovery lost complete native metadata")
					}
				}
			}
		})
	}
}

func TestParityReferenceProjectionBindsSummaryOrder(t *testing.T) {
	data := `{` + strings.Replace(parityReferenceSummary, `["and","or"]`, `["or","and"]`, 1) + `,"reference":` + parityReferenceFixture + `}`
	if parityReferenceProjection([]byte(data), []byte(parityReferenceFixture), "after") {
		t.Fatal("summary reordered the selected response's native reserved words")
	}
}

func TestParityReferenceFixtureRejectsReorderedNativeArrays(t *testing.T) {
	data := `{` + parityReferenceSummary + `,"reference":` + parityReferenceFixture + `}`
	for _, tc := range []struct{ name, changed string }{
		{"operators", strings.Replace(data, `["=","in"]`, `["in","="]`, 1)},
		{"words", strings.ReplaceAll(data, `["and","or"]`, `["or","and"]`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !parityReferenceProjection([]byte(tc.changed), []byte(parityReferenceFixture), "after") {
				t.Fatal("independent live-read comparison lost its bounded order tolerance")
			}
			if parityReferenceFixtureProjection([]byte(tc.changed), []byte(parityReferenceFixture), "after") {
				t.Fatal("fixed-response acceptance permitted changed native array order")
			}
		})
	}
}

func TestParityActualBinaryJQLReference(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			for _, fixture := range []struct{ name, native string }{{"populated", parityReferenceFixture}, {"nullable", parityReferenceNullableFixture}} {
				t.Run(fixture.name, func(t *testing.T) {
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						user, password, authenticated := r.BasicAuth()
						body, bodyErr := io.ReadAll(io.LimitReader(r.Body, 1))
						if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/jql/autocompletedata" || r.URL.RawQuery != "" || !authenticated || user != "fixture@example.invalid" || password != "synthetic-fixture-token" || bodyErr != nil || len(body) != 0 {
							http.Error(w, "unexpected synthetic reference request", http.StatusBadRequest)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(fixture.native))
					}))
					defer server.Close()
					if err := parityFingerprint(lane.binary, lane.digest); err != nil {
						t.Fatal(err)
					}
					result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, t.TempDir(), parityFixtureInput{}, nil, "jql", "reference")
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if result.Exit != 0 || calls.Load() != 1 {
						t.Fatalf("reference failed to enter exactly one authenticated GET: exit %d; calls %d; stdout %s; stderr %s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
					}
					data := parityAuthoringCleanData(t, result.Stdout, lane.name, "jql.reference")
					encoded, err := json.Marshal(data)
					if err != nil || !parityReferenceFixtureProjection(encoded, []byte(fixture.native), lane.name) {
						t.Fatalf("reference changed independently authored native metadata: %s", result.Stdout)
					}
					name := "C058.fixture." + fixture.name
					evidence.Checks[name] = append(evidence.Checks[name], lane.name)
				})
			}
		})
	}
	// These synthetic authenticated reads are neither zero-HTTP local acceptance
	// nor live Jira execution. Keep their evidence in its own cohort.
	saveParityLocal(t, "jql-reference-fixture", evidence)
}

func TestLiveParityJQLReference(t *testing.T) {
	p := newParityRun(t)
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			native := rawRead(t, p.owner, "api", "get", "/rest/api/3/jql/autocompletedata")
			frame, stdout, exit := p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, "jql", "reference")
			if exit != 0 || !frame.OK {
				t.Fatal("live reference failed; inspect retained receipts")
			}
			data := parityAuthoringCleanData(t, stdout, lane.Name, "jql.reference")
			encoded, err := json.Marshal(data)
			if err != nil || !parityReferenceProjection(encoded, native.Response.JSON, lane.Name) {
				t.Fatal("domain discovery differs from the complete independent native reference")
			}
			p.check(t, lane, "C058.reference.native")
		})
	}
}
