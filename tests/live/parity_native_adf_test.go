//go:build live

package live

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

const parityNativeADFSchema = "5128562b75278c8a83e7e3619a570205bc80d59696985ec31a7a7883cff66fbe"

func parityNativeADFInspection(data []byte, route string, valid, projection, lossy bool, constructs []string, location string) bool {
	if !jsontext.Value(data).IsValid() {
		return false
	}
	var actual map[string]json.RawMessage
	if json.Unmarshal(data, &actual) != nil {
		return false
	}
	var diagnostics []struct {
		InstanceLocation string `json:"instance_location"`
		KeywordLocation  string `json:"keyword_location"`
		Message          string `json:"message"`
	}
	if json.Unmarshal(actual["diagnostics"], &diagnostics) != nil || diagnostics == nil || valid != (len(diagnostics) == 0) {
		return false
	}
	found := location == ""
	for _, row := range diagnostics {
		if row.Message == "" {
			return false
		}
		found = found || row.InstanceLocation == location && row.KeywordLocation != ""
	}
	if !found {
		return false
	}
	// Diagnostics are native validator reports. Bind their promised failure
	// location separately; all remaining published inspection facts are exact.
	delete(actual, "diagnostics")
	expected := map[string]any{"schema_sha256": parityNativeADFSchema, "valid": valid}
	if route == "lint" {
		expected["projection_available"], expected["lossy"], expected["lossy_constructs"] = projection, lossy, constructs
	}
	left, leftErr := json.Marshal(actual)
	right, rightErr := json.Marshal(expected)
	return leftErr == nil && rightErr == nil && parityAuthoringJSON(left, right)
}

func parityNativeADFEditCandidate(data []byte, document, kind, region, reason string) bool {
	if !jsontext.Value(data).IsValid() {
		return false
	}
	expected := map[string]any{}
	if kind == "" {
		var actual struct{ Document json.RawMessage }
		if json.Unmarshal(data, &actual) != nil || !bytes.Equal(actual.Document, []byte(document)) {
			return false
		}
		expected["document"], expected["diagnostics"] = json.RawMessage(document), []any{}
	} else {
		expected["diagnostics"] = []any{map[string]string{"kind": kind, "region": region, "reason": reason}}
	}
	want, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, want)
}

func TestParityActualBinaryNativeADF(t *testing.T) {
	binary, digest := os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")
	if binary == "" {
		t.Skip("requires selected actual current binary; legacy has no native editing/inspection commands")
	}
	raw, err := os.ReadFile("testdata/parity-native-adf-oracles.json")
	var fixture struct {
		Source   string
		Prepared struct{ Markdown string }
	}
	// Read exact region IDs from the independently authored source fixture,
	// never from production PrepareEdit or a preceding CLI preparation result.
	var sourceFixture map[string]json.RawMessage
	if err != nil || !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &fixture) != nil || json.Unmarshal(raw, &sourceFixture) != nil {
		t.Fatal("independent native ADF fixture unavailable")
	}
	var prepared struct{ Regions []struct{ ID string } }
	if json.Unmarshal(sourceFixture["prepared"], &prepared) != nil || len(prepared.Regions) != 2 || fixture.Source == "" || fixture.Prepared.Markdown == "" {
		t.Fatal("independent native ADF anchor fixture incomplete")
	}
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "local native ADF entered HTTP", 500)
	}))
	defer server.Close()
	invoke := func(t *testing.T, id, scenario, mode string, expectedExit int, stdin []byte, files map[string]string, args ...string) []byte {
		t.Helper()
		home := t.TempDir()
		config := parityCachePublicConfig(t, home, "after", server.URL)
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		before := parityLocalTree(t, home)
		if err := parityFingerprint(binary, digest); err != nil {
			t.Fatal(err)
		}
		result := parityFixtureProcessAt(t, "after", binary, server.URL, home, parityFixtureInput{Config: config, Output: mode, Stdin: stdin}, []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false", "JIRA_READ_ONLY=true"}, args...)
		evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: "after", Digest: digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr, InputBytes: stdin})
		if result.Exit != expectedExit || calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) || result.Exit == 0 && len(result.Stderr) != 0 {
			t.Fatalf("local native ADF entered effects: exit %d HTTP %d stdout %s stderr %s", result.Exit, calls.Load(), result.Stdout, result.Stderr)
		}
		operation := "adf." + args[1]
		if args[1] == "edit" {
			operation = "adf.edit." + args[2]
		}
		var data []byte
		if result.Exit != 0 || mode == "json" {
			parityAuthoringData(t, result.Stdout, "after", operation, result.Exit)
			if result.Exit == 0 {
				parityAuthoringCleanData(t, result.Stdout, "after", operation)
			}
			var frame struct{ Data json.RawMessage }
			if json.Unmarshal(result.Stdout, &frame) != nil {
				t.Fatal("native ADF frame data unavailable")
			}
			// Preserve the original native bytes; re-encoding RawMessage through
			// the default encoder would escape HTML in the test itself.
			data = frame.Data
		} else {
			data = result.Stdout
		}
		if result.Exit != 0 {
			var frame struct {
				Errors   []struct{ Code, Field, Message, Hint string }
				Warnings json.RawMessage
			}
			if result.Exit != 3 || json.Unmarshal(result.Stdout, &frame) != nil || len(frame.Errors) != 1 || !parityAuthoringJSON(frame.Warnings, []byte(`[]`)) {
				t.Fatalf("native ADF refusal lost its full frame: %s", result.Stdout)
			}
			code, field := "adf_invalid", "input"
			if args[1] == "edit" {
				code, field = "flag_value_invalid", "markdown-file"
				if args[2] == "prepare" {
					field = "input"
				}
			}
			if args[1] == "explain-node" {
				code, field = "adf_type_unknown", "TYPE"
			}
			if frame.Errors[0].Code != code || frame.Errors[0].Field != field || frame.Errors[0].Message == "" || frame.Errors[0].Hint == "" {
				t.Fatalf("native ADF refusal changed classification: %s", result.Stdout)
			}
		}
		if err := parityFingerprint(binary, digest); err != nil {
			t.Fatal(err)
		}
		check := id + ".native-adf." + scenario + "." + mode
		evidence.Checks[check] = append(evidence.Checks[check], "after")
		return data
	}
	for _, mode := range []string{"json", "compact"} {
		for _, route := range []string{"validate", "lint"} {
			for _, tc := range []struct {
				name, body               string
				valid, projection, lossy bool
				constructs               []string
				location                 string
			}{
				{"plain", parityAuthoringDocument, true, true, false, []string{}, ""},
				{"rich-boundary-text", parityDescription, true, true, true, []string{"text"}, ""},
				{"opaque-panel", `{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"warning","localId":"keep"},"content":[{"type":"paragraph","content":[{"type":"text","text":"native"}]}]}]}`, true, true, true, []string{"panel"}, ""},
				{"unknown-native", `{"type":"doc","version":1,"content":[{"type":"futureWidget","content":[{"type":"paragraph","content":[{"type":"text","text":"kept"}]}]}]}`, false, true, true, []string{"futureWidget"}, ""},
				{"invalid-heading", `{"type":"doc","version":1,"content":[{"type":"heading","attrs":{"level":7},"content":[]}]}`, false, true, true, []string{"heading"}, "/content/0/attrs/level"},
				{"malformed", `{"type":"doc",`, false, false, false, []string{}, ""},
			} {
				t.Run(route+"/"+mode+"/"+tc.name, func(t *testing.T) {
					id := "C080"
					if route == "lint" {
						id = "C079"
					}
					expectedExit := 0
					if !tc.valid {
						expectedExit = 3
					}
					data := invoke(t, id, tc.name, mode, expectedExit, []byte(tc.body), nil, "adf", route, "--input", "-")
					if !parityNativeADFInspection(data, route, tc.valid, tc.projection, tc.lossy, tc.constructs, tc.location) {
						t.Fatalf("native ADF inspection differs from independent facts: %s", data)
					}
				})
			}
		}
		t.Run("explain/"+mode+"/unknown", func(t *testing.T) {
			data := invoke(t, "C078", "unknown", mode, 3, nil, nil, "adf", "explain-node", "futureOwnedType")
			if string(data) != "null" {
				t.Fatal("unknown ADF identity emitted a partial explanation")
			}
		})
		t.Run("prepare/"+mode+"/invalid-version", func(t *testing.T) {
			data := invoke(t, "C077", "invalid-version", mode, 3, []byte(`{"type":"doc","version":2,"content":[]}`), nil, "adf", "edit", "prepare", "--input", "-")
			if string(data) != "null" {
				t.Fatal("invalid native source emitted a partial edit preparation")
			}
		})
		for _, identity := range []string{"strong", "strong_mark"} {
			t.Run("explain/"+mode+"/"+identity, func(t *testing.T) {
				data := invoke(t, "C078", identity, mode, 0, nil, nil, "adf", "explain-node", identity)
				const row = `{"kind":"mark","name":"strong","definition":"strong_mark","variant":"","capabilities":{"author":true,"render":true,"preserve":true,"validate":true,"submit":true,"edit":false},"terminal_render":true,"markdown_projection":"formatting","mark_bounds":{"name":"strong_mark","type":"strong"}}`
				want := []byte(`{"schema_sha256":"` + parityNativeADFSchema + `","entries":[` + row + `]}`)
				if !parityAuthoringJSON(data, want) {
					t.Fatalf("selected native schema identity/policy differs: %s", data)
				}
			})
		}
		t.Run("prepare/"+mode+"/rich-boundary-text", func(t *testing.T) {
			data := invoke(t, "C077", "rich-boundary-text", mode, 0, []byte(parityDescription), nil, "adf", "edit", "prepare", "--input", "-")
			if !parityAuthoringJSON(data, parityNativeADFRichPrepared([]byte(parityDescription))) {
				t.Fatalf("rich anchored preparation differs from independent literal projection: %s", data)
			}
		})
		for _, input := range []string{"file", "stdin"} {
			t.Run("prepare/"+mode+"/"+input, func(t *testing.T) {
				files := map[string]string{"source.json": fixture.Source}
				path := "source.json"
				var stdin []byte
				if input == "stdin" {
					path = "-"
					stdin = []byte(fixture.Source)
				}
				data := invoke(t, "C077", input, mode, 0, stdin, files, "adf", "edit", "prepare", "--input", path)
				if !parityAuthoringJSON(data, sourceFixture["prepared"]) {
					t.Fatalf("anchored projection differs from independent raw source: %s", data)
				}
			})
		}
		for _, tc := range []struct{ name, command, markdown, current, document, kind, region, reason string }{
			{"unchanged", "apply", fixture.Prepared.Markdown, "", fixture.Source, "", "", ""},
			{"replacement", "apply", strings.Replace(fixture.Prepared.Markdown, "original", "**edited**", 1), "", strings.Replace(fixture.Source, `{"type":"text","text":"original"}`, `{"type":"text","text":"edited","marks":[{"type":"strong"}]}`, 1), "", "", ""},
			{"protected-change", "apply", strings.Replace(fixture.Prepared.Markdown, "protected\n", "tampered\n", 1), "", "", "protected_region", prepared.Regions[1].ID, "protected projection was changed"},
			{"protected-missing", "apply", "", "", "", "protected_region", prepared.Regions[1].ID, "protected region is missing"},
			{"concurrent-edit", "merge", strings.Replace(fixture.Prepared.Markdown, "original", "edited", 1), strings.Replace(fixture.Source, `"text":"original"`, `"text":"competing"`, 1), "", "merge_conflict", prepared.Regions[0].ID, "region was edited or moved locally but removed or replaced remotely"},
			{"protection-changed", "apply", strings.Replace(fixture.Prepared.Markdown, " protected -->", " editable -->", 1), "", "", "invalid_anchor", prepared.Regions[1].ID, "anchor protection metadata changed"},
			{"current-root", "merge", strings.Replace(fixture.Prepared.Markdown, "original", "edited", 1), strings.Replace(fixture.Source, `"future":"<>&\u2028"`, `"future":"remote root"`, 1), strings.Replace(strings.Replace(fixture.Source, `"text":"original"`, `"text":"edited"`, 1), `"future":"<>&\u2028"`, `"future":"remote root"`, 1), "", "", ""},
		} {
			t.Run(tc.command+"/"+mode+"/"+tc.name, func(t *testing.T) {
				id := "C075"
				args := []string{"adf", "edit", tc.command, "--input", "source.json", "--markdown-file", "edited.md"}
				files := map[string]string{"source.json": fixture.Source, "edited.md": tc.markdown}
				if tc.command == "merge" {
					id = "C076"
					args = append(args, "--current", "current.json")
					files["current.json"] = tc.current
				}
				expectedExit := 0
				if tc.kind != "" {
					expectedExit = 3
				}
				data := invoke(t, id, tc.name, mode, expectedExit, nil, files, args...)
				if !parityNativeADFEditCandidate(data, tc.document, tc.kind, tc.region, tc.reason) {
					t.Fatalf("anchored candidate/refusal differs: %s", data)
				}
				if tc.kind == "" {
					var out struct{ Document json.RawMessage }
					if json.Unmarshal(data, &out) != nil {
						t.Fatal("candidate unavailable")
					}
					const opaque = `{"type":"futureBlock","attrs":{"id":900719925474099312345,"extra":"\u003c"},"content":[{"type":"text","text":"protected"}]}`
					if !bytes.Contains(out.Document, []byte(opaque)) || tc.name == "unchanged" && string(out.Document) != fixture.Source {
						t.Fatal("untouched native bytes or numeric tokens changed")
					}
				}
			})
		}
	}
	saveParityLocal(t, "native-adf", evidence)
}

func TestParityNativeADFOracleGuards(t *testing.T) {
	const valid = `{"schema_sha256":"` + parityNativeADFSchema + `","valid":true,"diagnostics":[],"projection_available":true,"lossy":false,"lossy_constructs":[]}`
	if !parityNativeADFInspection([]byte(valid), "lint", true, true, false, []string{}, "") {
		t.Fatal("independent native inspection fixture refused")
	}
	for name, wrong := range map[string]string{
		"wrong-pin":               strings.Replace(valid, parityNativeADFSchema, "wrong", 1),
		"hidden-projection":       strings.Replace(valid, `"projection_available":true`, `"projection_available":false`, 1),
		"invented-loss":           strings.Replace(valid, `"lossy":false`, `"lossy":true`, 1),
		"missing-construct-array": strings.Replace(valid, `"lossy_constructs":[]`, `"lossy_constructs":null`, 1),
		"lost-diagnostics":        strings.Replace(valid, `"diagnostics":[]`, `"diagnostics":null`, 1),
		"duplicate-fact":          strings.Replace(valid, `"valid":true`, `"valid":false,"valid":true`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if parityNativeADFInspection([]byte(wrong), "lint", true, true, false, []string{}, "") {
				t.Fatal("changed native inspection facts admitted")
			}
		})
	}
	const invalid = `{"schema_sha256":"` + parityNativeADFSchema + `","valid":false,"diagnostics":[{"instance_location":"/content/0/attrs/level","keyword_location":"/enum","message":"invalid level"}]}`
	if !parityNativeADFInspection([]byte(invalid), "validate", false, false, false, nil, "/content/0/attrs/level") || parityNativeADFInspection([]byte(strings.Replace(invalid, "/attrs/level", "/attrs/other", 1)), "validate", false, false, false, nil, "/content/0/attrs/level") {
		t.Fatal("native diagnostic failure location was not bound")
	}
	const document = `{"type":"doc","version":1,"content":[],"future":900719925474099312345,"root":"\u2028"}`
	const candidate = `{"document":` + document + `,"diagnostics":[]}`
	if !parityNativeADFEditCandidate([]byte(candidate), document, "", "", "") {
		t.Fatal("independent native candidate refused")
	}
	for name, wrong := range map[string]string{
		"rounded-native-number":  strings.Replace(candidate, "900719925474099312345", "900719925474099300000", 1),
		"normalized-root-escape": strings.Replace(candidate, `\u2028`, "\u2028", 1),
		"missing-candidate":      `{"diagnostics":[]}`,
		"missing-diagnostics":    `{"document":` + document + `}`,
		"partial-refusal":        `{"document":` + document + `,"diagnostics":[{"kind":"protected_region","region":"owned","reason":"protected region is missing"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if parityNativeADFEditCandidate([]byte(wrong), document, "", "", "") {
				t.Fatal("changed or incomplete native candidate admitted")
			}
		})
	}
	const refusal = `{"diagnostics":[{"kind":"protected_region","region":"owned","reason":"protected region is missing"}]}`
	if !parityNativeADFEditCandidate([]byte(refusal), "", "protected_region", "owned", "protected region is missing") {
		t.Fatal("independent native edit refusal refused")
	}
	for name, wrong := range map[string]string{
		"refusal-has-candidate": `{"document":` + document + `,"diagnostics":[{"kind":"protected_region","region":"owned","reason":"protected region is missing"}]}`,
		"wrong-region":          strings.Replace(refusal, `"region":"owned"`, `"region":"foreign"`, 1),
		"lost-reason":           strings.Replace(refusal, "protected region is missing", "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if parityNativeADFEditCandidate([]byte(wrong), "", "protected_region", "owned", "protected region is missing") {
				t.Fatal("changed native edit refusal or partial candidate admitted")
			}
		})
	}
}

// These new local commands have no v0.15 subcommand counterpart. A current
// owned Jira source establishes their input; no candidate is applied remotely.
func TestLiveParityNativeADF(t *testing.T) {
	p := newParityRun(t)
	var lane parityLane
	for _, selected := range p.evidence.Lanes {
		if selected.Name == "after" {
			lane = selected
		}
	}
	if lane.Name != "after" {
		t.Fatal("admitted current lane missing")
	}
	key := p.create(t, lane)
	native := p.native(t, key)
	var fields struct{ Description json.RawMessage }
	if json.Unmarshal(native.Fields, &fields) != nil || !parityAuthoringJSON(fields.Description, []byte(parityDescription)) {
		t.Fatal("owned native ADF source differs from the authored rich body")
	}
	source := filepath.Join(p.owner.dir, "native-adf-source.json")
	edited := filepath.Join(p.owner.dir, "native-adf-edited.md")
	if err := os.WriteFile(source, fields.Description, 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "native ADF entered HTTP", 500)
	}))
	defer trap.Close()
	extra := []string{"JIRA_BASE_URL=" + trap.URL, "JIRA_EMAIL=fixture@example.invalid", "JIRA_API_TOKEN=", "JIRA_KEYRING=false", "JIRA_SECRET_BACKEND=env", "JIRA_READ_ONLY=true"}
	invoke := func(t *testing.T, operation string, args ...string) json.RawMessage {
		t.Helper()
		frame, stdout, exit := p.invoke(t, lane, extra, args...)
		if exit != 0 || !frame.OK || calls.Load() != 0 {
			t.Fatal("stored-source native ADF command failed or entered HTTP; inspect retained receipts")
		}
		parityAuthoringCleanData(t, stdout, "after", operation)
		var result struct{ Data json.RawMessage }
		if json.Unmarshal(stdout, &result) != nil {
			t.Fatal("native ADF result unavailable")
		}
		return result.Data
	}
	for _, route := range []string{"validate", "lint"} {
		t.Run(route, func(t *testing.T) {
			data := invoke(t, "adf."+route, "adf", route, "--input", source)
			if !parityNativeADFInspection(data, route, true, true, true, []string{"text"}, "") {
				t.Fatal("stored native ADF inspection differs from independent rich-source facts")
			}
			id := "C080"
			if route == "lint" {
				id = "C079"
			}
			p.check(t, lane, id+".native-adf.stored-source")
		})
	}
	t.Run("explain", func(t *testing.T) {
		data := invoke(t, "adf.explain-node", "adf", "explain-node", "strong")
		const row = `{"kind":"mark","name":"strong","definition":"strong_mark","variant":"","capabilities":{"author":true,"render":true,"preserve":true,"validate":true,"submit":true,"edit":false},"terminal_render":true,"markdown_projection":"formatting","mark_bounds":{"name":"strong_mark","type":"strong"}}`
		if !parityAuthoringJSON(data, []byte(`{"schema_sha256":"`+parityNativeADFSchema+`","entries":[`+row+`]}`)) {
			t.Fatal("explanation differs from the independently authored native strong mark")
		}
		p.check(t, lane, "C078.native-adf.stored-mark")
	})
	var prepared struct {
		Markdown string
		Regions  []struct {
			ID        string
			NodeType  string `json:"node_type"`
			Protected bool
		}
	}
	if !t.Run("prepare", func(t *testing.T) {
		data := invoke(t, "adf.edit.prepare", "adf", "edit", "prepare", "--input", source)
		if !parityAuthoringJSON(data, parityNativeADFRichPrepared(fields.Description)) || json.Unmarshal(data, &prepared) != nil {
			t.Fatal("stored native ADF preparation lost its independent byte preimage or region")
		}
		if err := os.WriteFile(edited, []byte(prepared.Markdown), 0o600); err != nil {
			t.Fatal(err)
		}
		p.check(t, lane, "C077.native-adf.stored-source")
	}) {
		return
	}
	for _, route := range []string{"apply", "merge"} {
		t.Run(route, func(t *testing.T) {
			args := []string{"adf", "edit", route, "--input", source, "--markdown-file", edited}
			if route == "merge" {
				args = append(args, "--current", source)
			}
			data := invoke(t, "adf.edit."+route, args...)
			if !parityNativeADFEditCandidate(data, string(fields.Description), "", "", "") {
				t.Fatal("unchanged stored native ADF candidate differs")
			}
			var out struct{ Document json.RawMessage }
			if json.Unmarshal(data, &out) != nil || !bytes.Equal(out.Document, fields.Description) {
				t.Fatal("stored source candidate changed exact native bytes")
			}
			id := "C075"
			if route == "merge" {
				id = "C076"
			}
			p.check(t, lane, id+".native-adf.stored-source")
		})
	}
	for path, want := range map[string][]byte{source: fields.Description, edited: []byte(prepared.Markdown)} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("local native ADF command changed its input artifact")
		}
	}
	after := p.native(t, key)
	if after.ID != native.ID || after.Key != native.Key || !parityAuthoringJSON(after.Fields, native.Fields) {
		t.Fatal("local native ADF commands changed the owned Jira source")
	}
}

func TestParityNativeADFOperationAttribution(t *testing.T) {
	for _, lane := range []string{"before", "after"} {
		for _, exit := range []int{0, 3} {
			for _, operation := range []string{"prepare", "apply", "merge"} {
				if parityExpectedOperation(lane, []string{"adf", "edit", operation, "--input", "source.json"}, exit) != "adf.edit."+operation {
					t.Fatal("native ADF operation lost its selected edit subcommand")
				}
			}
		}
	}
	for _, args := range [][]string{{"adf", "edit", "prepare-other"}, {"adf", "editor", "prepare"}, {"adf-other", "edit", "prepare"}} {
		if parityOperation(args) == "adf.edit.prepare" {
			t.Fatal("native ADF operation was inferred from a prefix lookalike")
		}
	}
}

func parityNativeADFRichPrepared(source []byte) []byte {
	id := fmt.Sprintf("%x-0", sha256.Sum256(source))
	// Native text fragments bordering the strong mark contain spaces. The
	// delivered projection conservatively reports text loss and protects this
	// region even though its readable rich Markdown remains available.
	markdown := "<!-- jira-adf:begin " + id + " protected -->\n\nPaired **bold** \\<&amp;\\> Unicode café\n\n<!-- jira-adf:end " + id + " -->\n\n"
	out := map[string]any{"markdown": markdown, "regions": []any{map[string]any{"id": id, "node_type": "paragraph", "protected": true}}}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return encoded
}
