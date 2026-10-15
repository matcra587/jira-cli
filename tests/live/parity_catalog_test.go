//go:build live

package live

import (
	_ "embed"
	"encoding/json"
	"encoding/json/jsontext"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// These identities come from the independently pinned raw Atlassian schema,
// not the CLI's generated models or runtime support registries.
//
//go:embed testdata/parity-adf-after.tsv
var parityADFAfterIdentities string

func parityCatalogData(t *testing.T, stdout []byte, lane, mode, operation string) []byte {
	t.Helper()
	if !jsontext.Value(stdout).IsValid() {
		t.Fatal("catalog output contains duplicate names or incomplete JSON")
	}
	if mode == "compact" {
		return stdout
	}
	if lane == "after" {
		data := parityAuthoringCleanData(t, stdout, lane, operation)
		encoded, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	if err := parityValidateLegacyCatalogFrame(stdout, operation); err != nil {
		t.Fatal(err)
	}
	var frame struct {
		Data     json.RawMessage
		Warnings json.RawMessage
	}
	if json.Unmarshal(stdout, &frame) != nil || !parityAuthoringJSON(frame.Warnings, []byte(`[]`)) {
		t.Fatal("legacy local catalog lost clean warnings")
	}
	return frame.Data
}

func parityCatalogRows(t *testing.T, data []byte, lane, family string) ([]map[string]json.RawMessage, map[string]json.RawMessage) {
	t.Helper()
	var header map[string]json.RawMessage
	if lane == "after" {
		if json.Unmarshal(data, &header) != nil || header == nil {
			t.Fatal("current catalog lost typed data")
		}
		member := "types"
		if family == "adf-matrix" {
			member = "entries"
		}
		data = header[member]
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(data, &rows) != nil || rows == nil {
		t.Fatal("catalog lost its row array")
	}
	return rows, header
}

func parityCatalogString(t *testing.T, row map[string]json.RawMessage, name string) string {
	t.Helper()
	var value string
	if json.Unmarshal(row[name], &value) != nil || value == "" {
		t.Fatalf("catalog lacks nonempty %s", name)
	}
	return value
}

func parityCatalogIdentitySet(rows []map[string]json.RawMessage, lane, expected string) bool {
	var identities []string
	seen := make(map[string]bool)
	for _, row := range rows {
		var kind, name, definition string
		if json.Unmarshal(row["kind"], &kind) != nil || (kind != "node" && kind != "mark") || json.Unmarshal(row["name"], &name) != nil || name == "" {
			return false
		}
		identity := kind + "\t" + name
		if lane == "after" {
			if json.Unmarshal(row["definition"], &definition) != nil || definition == "" {
				return false
			}
			identity += "\t" + definition
		}
		if seen[identity] {
			return false
		}
		seen[identity] = true
		identities = append(identities, identity)
	}
	if lane == "before" {
		// The old curated registry interleaves nodes and marks. The native
		// schema establishes membership, independently of that display order.
		slices.Sort(identities)
		wanted := strings.Split(strings.TrimSuffix(expected, "\n"), "\n")
		slices.Sort(wanted)
		return slices.Equal(identities, wanted)
	}
	return strings.Join(identities, "\n")+"\n" == expected
}

func parityFieldCatalog(t *testing.T, rows []map[string]json.RawMessage, header map[string]json.RawMessage, lane string) {
	t.Helper()
	want := strings.Fields("string number date datetime labels select multiselect user multiuser group multigroup components version fixversions versionpicker multiversionpicker projectpicker parent cascadingselect")
	if lane == "after" {
		want = append(want, "textarea")
		slices.Sort(want)
	}
	var names []string
	for _, row := range rows {
		name := parityCatalogString(t, row, "name")
		names = append(names, name)
		parityCatalogString(t, row, "notes")
		if lane == "before" {
			if string(row["kind"]) != `"field-type"` || string(row["status"]) != `"mvp"` || !parityAuthoringJSON(row["capabilities"], []byte(`{"author":true,"render":true,"preserve":true,"validate":true,"submit":true}`)) {
				t.Fatal("legacy registry lost its explicit encoding policy")
			}
			if _, present := row["example"]; present {
				t.Fatal("legacy registry unexpectedly claims native examples")
			}
			parityCatalogString(t, row, "submit_description")
		} else {
			parityCatalogString(t, row, "native_shape")
			if !jsontext.Value(row["example"]).IsValid() {
				t.Fatal("native catalog example is not complete JSON")
			}
			switch name {
			case "number":
				if string(row["example"]) != "9007199254740993" {
					t.Fatal("numeric catalog example lost exact precision")
				}
			case "user":
				if !parityAuthoringJSON(row["example"], []byte(`{"accountId":"example-account-id"}`)) {
					t.Fatal("user catalog no longer uses native account identity")
				}
			case "select":
				if !parityAuthoringJSON(row["example"], []byte(`{"id":"10001"}`)) {
					t.Fatal("select catalog no longer uses native option identity")
				}
			}
		}
	}
	if !slices.Equal(names, want) {
		t.Fatal("field catalog differs from independently declared ordered families")
	}
	if lane == "after" {
		const handling = `{"input_flag":"json-input","encoding":"native-json","preserve_values":true,"type_validation":false,"scalar_conversion":false,"unknown_types":"native-json","null_values":"explicit-null","empty_arrays":"empty-array"}`
		if !parityAuthoringJSON(header["handling"], []byte(handling)) {
			t.Fatal("native handling policy was silently changed")
		}
		var metadata []struct{ Purpose, Path string }
		if json.Unmarshal(header["metadata"], &metadata) != nil || len(metadata) != 3 {
			t.Fatal("native metadata routes missing")
		}
		for i, path := range []string{"/rest/api/3/field", "/rest/api/3/issue/KEY-123/editmeta", "/rest/api/3/issue/createmeta/PROJECT/issuetypes/TYPE_ID"} {
			if metadata[i].Path != path || metadata[i].Purpose == "" {
				t.Fatal("field discovery changed native metadata routing")
			}
		}
	}
}

func parityADFCatalog(t *testing.T, rows []map[string]json.RawMessage, header map[string]json.RawMessage, lane, expected string) {
	t.Helper()
	if !parityCatalogIdentitySet(rows, lane, expected) {
		t.Fatal("ADF catalog differs from independently pinned native identities/context definitions")
	}
	for _, row := range rows {
		var capabilities map[string]json.RawMessage
		if json.Unmarshal(row["capabilities"], &capabilities) != nil {
			t.Fatal("ADF capabilities unavailable")
		}
		for _, name := range []string{"author", "render", "preserve", "validate", "submit"} {
			if string(capabilities[name]) != "true" && string(capabilities[name]) != "false" {
				t.Fatal("ADF catalog has an absent/null/nonboolean capability")
			}
		}
		if !parityCatalogNativePolicy(row) {
			t.Fatal("ADF catalog changed its native preservation/validation/submission declarations")
		}
		if lane == "before" {
			continue
		}
		if string(capabilities["edit"]) != "false" {
			t.Fatal("ADF catalog changed known native policy or claimed completed lossless editing")
		}
		var definition, variant string
		if json.Unmarshal(row["definition"], &definition) != nil || json.Unmarshal(row["variant"], &variant) != nil {
			t.Fatal("ADF context identity missing")
		}
		if !parityCatalogBounds(row) {
			t.Fatal("native context bounds are missing or bound to another identity")
		}
		var bounds struct {
			Marks    json.RawMessage
			Required []string
		}
		if string(row["kind"]) == `"node"` && json.Unmarshal(row["node_bounds"], &bounds) != nil {
			t.Fatal("node context bounds missing")
		}
		switch definition {
		case "paragraph_with_no_marks_node":
			if variant != "with_no_marks" || !parityAuthoringJSON(bounds.Marks, []byte(`{"max":0,"allowed":null}`)) {
				t.Fatal("no-mark paragraph lost its explicit context restriction")
			}
		case "paragraph_with_alignment_node":
			var marks struct {
				Max     *int
				Allowed []string
			}
			if json.Unmarshal(bounds.Marks, &marks) != nil || marks.Max == nil || *marks.Max != -1 {
				t.Fatal("alignment paragraph mark bounds missing")
			}
			slices.Sort(marks.Allowed)
			if variant != "with_alignment" || !slices.Equal(marks.Allowed, []string{"alignment", "fontSize"}) {
				t.Fatal("alignment paragraph lost its independent mark set")
			}
		case "codeBlock_root_only_node":
			if variant != "root_only" || !parityAuthoringJSON(bounds.Marks, []byte(`{"max":-1,"allowed":["breakout"]}`)) {
				t.Fatal("root code block lost its native mark context")
			}
		case "table_cell_node":
			slices.Sort(bounds.Required)
			if !slices.Equal(bounds.Required, []string{"content", "type"}) {
				t.Fatal("table cell lost its required native content")
			}
		}
	}
	if lane == "after" {
		if string(header["schema_sha256"]) != `"5128562b75278c8a83e7e3619a570205bc80d59696985ec31a7a7883cff66fbe"` || !parityAuthoringJSON(header["unknown"], []byte(`{"preserve_native":true,"validate":false,"edit":false,"node_projection":"placeholder","mark_projection":"dropped"}`)) {
			t.Fatal("ADF catalog lost its independent schema pin or unknown-type policy")
		}
	}
}

func parityCatalogNativePolicy(row map[string]json.RawMessage) bool {
	var capabilities map[string]json.RawMessage
	if json.Unmarshal(row["capabilities"], &capabilities) != nil || capabilities == nil {
		return false
	}
	for _, field := range []string{"preserve", "validate", "submit"} {
		if string(capabilities[field]) != "true" {
			return false
		}
	}
	return true
}

func parityCatalogBounds(row map[string]json.RawMessage) bool {
	var kind, name, definition string
	var variant *string
	if json.Unmarshal(row["kind"], &kind) != nil || (kind != "node" && kind != "mark") || json.Unmarshal(row["name"], &name) != nil || name == "" || json.Unmarshal(row["definition"], &definition) != nil || definition == "" || json.Unmarshal(row["variant"], &variant) != nil || variant == nil {
		return false
	}
	opposite := "node_bounds"
	if kind == "node" {
		opposite = "mark_bounds"
	}
	if _, present := row[opposite]; present {
		return false
	}
	var bounds map[string]json.RawMessage
	if json.Unmarshal(row[kind+"_bounds"], &bounds) != nil || bounds == nil {
		return false
	}
	var boundName, boundType string
	var boundVariant *string
	if json.Unmarshal(bounds["name"], &boundName) != nil || boundName != definition || json.Unmarshal(bounds["type"], &boundType) != nil || boundType != name {
		return false
	}
	return kind != "node" || (json.Unmarshal(bounds["variant"], &boundVariant) == nil && boundVariant != nil && *boundVariant == *variant)
}

func TestParityCatalogBoundsAndPolicyAdmission(t *testing.T) {
	for _, kind := range []string{"node", "mark"} {
		t.Run(kind, func(t *testing.T) {
			definition := "paragraph_node"
			name := "paragraph"
			variant := `,"variant":""`
			if kind == "mark" {
				definition, name, variant = "strong_mark", "strong", ""
			}
			const capabilities = `{"author":false,"render":false,"preserve":true,"validate":true,"submit":true}`
			bounds := `{"name":"` + definition + `","type":"` + name + `"` + variant + `}`
			encoded := `{"kind":"` + kind + `","name":"` + name + `","definition":"` + definition + `","variant":"","capabilities":` + capabilities + `,"` + kind + `_bounds":` + bounds + `}`
			var row map[string]json.RawMessage
			if json.Unmarshal([]byte(encoded), &row) != nil || !parityCatalogBounds(row) || !parityCatalogNativePolicy(row) {
				t.Fatal("independent valid bounds/policy refused")
			}
			for _, changed := range []string{"missing", "null", "foreign-name", "foreign-type", "opposite", "variant", "outer-null-variant", "null-bound-variant"} {
				if kind == "mark" && (changed == "variant" || changed == "null-bound-variant") {
					continue
				}
				t.Run(changed, func(t *testing.T) {
					var candidate map[string]json.RawMessage
					if json.Unmarshal([]byte(encoded), &candidate) != nil {
						t.Fatal("invalid independent fixture")
					}
					switch changed {
					case "missing":
						delete(candidate, kind+"_bounds")
					case "null":
						candidate[kind+"_bounds"] = json.RawMessage(`null`)
					case "foreign-name":
						candidate[kind+"_bounds"] = json.RawMessage(strings.Replace(bounds, definition, "other", 1))
					case "foreign-type":
						candidate[kind+"_bounds"] = json.RawMessage(strings.Replace(bounds, `"type":"`+name+`"`, `"type":"other"`, 1))
					case "opposite":
						other := "node_bounds"
						if kind == "node" {
							other = "mark_bounds"
						}
						candidate[other] = json.RawMessage(`null`)
					case "variant":
						candidate[kind+"_bounds"] = json.RawMessage(strings.Replace(bounds, `"variant":""`, `"variant":"other"`, 1))
					case "outer-null-variant":
						candidate["variant"] = json.RawMessage(`null`)
					case "null-bound-variant":
						candidate[kind+"_bounds"] = json.RawMessage(strings.Replace(bounds, `"variant":""`, `"variant":null`, 1))
					}
					if parityCatalogBounds(candidate) {
						t.Fatal("invalid bounds earned catalog credit")
					}
				})
			}
			for _, field := range []string{"preserve", "validate", "submit"} {
				t.Run(field, func(t *testing.T) {
					row["capabilities"] = json.RawMessage(strings.Replace(capabilities, `"`+field+`":true`, `"`+field+`":false`, 1))
					if parityCatalogNativePolicy(row) {
						t.Fatalf("false %s earned catalog credit", field)
					}
				})
			}
		})
	}
}

func TestParityActualBinaryCatalogs(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		t.Run(lane.name, func(t *testing.T) {
			if lane.binary == "" {
				t.Skip("requires selected actual binary")
			}
			expectedADF := parityADFAfterIdentities
			if lane.name == "before" {
				reference, err := os.ReadFile("testdata/parity-adf-before.tsv")
				if err != nil {
					t.Fatalf("read legacy ADF reference: %v", err)
				}
				expectedADF = string(reference)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) }))
			defer server.Close()
			for _, family := range []string{"fieldtypes", "adf-matrix"} {
				for _, mode := range []string{"json", "compact", "human"} {
					t.Run(family+"/"+mode, func(t *testing.T) {
						if err := parityFingerprint(lane.binary, lane.digest); err != nil {
							t.Fatal(err)
						}
						result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, t.TempDir(), parityFixtureInput{Output: mode}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE="}, "agent", family)
						evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
						if result.Exit != 0 || calls.Load() != 0 || len(result.Stderr) != 0 {
							t.Fatalf("local catalog failed: exit %d; HTTP %d; stdout %s; stderr %s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
						}
						if mode == "human" && lane.name == "after" {
							wants := []string{"Native field values", "--json-input", "cascadingselect", "textarea"}
							if family == "adf-matrix" {
								wants = []string{"Local ADF support", "paragraph_node", "strong_mark", "author=", "projection="}
							}
							for _, want := range wants {
								if !strings.Contains(string(result.Stdout), want) {
									t.Fatalf("human catalog omitted %q", want)
								}
							}
						} else {
							data := parityCatalogData(t, result.Stdout, lane.name, mode, "agent."+family)
							rows, header := parityCatalogRows(t, data, lane.name, family)
							if family == "fieldtypes" {
								parityFieldCatalog(t, rows, header, lane.name)
							} else {
								parityADFCatalog(t, rows, header, lane.name, expectedADF)
							}
						}
						id := "C008"
						if family == "adf-matrix" {
							id = "C006"
						}
						name := id + ".catalog." + mode
						evidence.Checks[name] = append(evidence.Checks[name], lane.name)
					})
				}
			}
		})
	}
	saveParityLocal(t, "catalogs", evidence)
}

func TestParityCatalogIdentityOracleRejectsSameCountReplacement(t *testing.T) {
	var rows []map[string]json.RawMessage
	if json.Unmarshal([]byte(`[{"kind":"node","name":"paragraph","definition":"paragraph_node"}]`), &rows) != nil {
		t.Fatal("invalid independent fixture")
	}
	if !parityCatalogIdentitySet(rows, "after", "node\tparagraph\tparagraph_node\n") {
		t.Fatal("independent catalog refused")
	}
	rows[0]["definition"] = json.RawMessage(`"other_node"`)
	if parityCatalogIdentitySet(rows, "after", "node\tparagraph\tparagraph_node\n") {
		t.Fatal("same-count replacement hid a native context")
	}
}

func TestParityLegacyCatalogArrayAdmissionIsExact(t *testing.T) {
	const valid = `{"ok":true,"meta":{"command":"agent.fieldtypes","timestamp":"2026-01-01T00:00:00Z","request_id":"owned-fixture"},"data":[],"errors":[],"warnings":[]}`
	if parityValidateLegacyCatalogFrame([]byte(valid), "agent.fieldtypes") != nil || parityValidateFrame([]byte(valid), "before", "agent.fieldtypes", 0) == nil {
		t.Fatal("catalog adapter weakened ordinary object-envelope admission")
	}
	for _, operation := range []string{"agent.fieldtypes.other", "issue.view"} {
		if parityValidateLegacyCatalogFrame([]byte(strings.Replace(valid, "agent.fieldtypes", operation, 1)), operation) == nil {
			t.Fatal("legacy array adapter admitted another operation")
		}
	}
	if parityValidateFrameShape([]byte(valid), "after", "agent.fieldtypes", 0, true) == nil || parityValidateLegacyCatalogFrame([]byte(strings.Replace(valid, `"data":[]`, `"data":{}`, 1)), "agent.fieldtypes") == nil {
		t.Fatal("catalog adapter admitted a current array or legacy object")
	}
}
