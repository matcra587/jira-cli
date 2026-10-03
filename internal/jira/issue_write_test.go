package jira

import (
	"bytes"
	"encoding/json"
	"maps"
	"testing"
)

func TestIssueWriteRequestRefusesAmbiguousMembersAtomically(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{"fields":{},"fields":{"summary":"second"}}`,
		`{"fields":{"summary":"first","summary":"second"}}`,
		`{"update":{"labels":[{"add":"first","add":"second"}]}}`,
		`{"properties":[{"key":"fixture","value":{"x":1,"x":2}}]}`,
		`{"fields":{},"Fields":{"summary":"second"}}`,
		`{"update":{},"UPDATE":{"labels":[]}}`,
	} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			const original = `{"fields":{"summary":"original"},"future":null}`
			var request IssueWriteRequest
			if err := json.Unmarshal([]byte(original), &request); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(input), &request); err == nil {
				t.Fatal("ambiguous native members silently discarded authored values")
			}
			got, err := request.MarshalJSON()
			if err != nil || string(got) != original {
				t.Fatal("rejected native input changed the existing request")
			}
		})
	}
}

func TestIssueWriteRequestKeepsTopLevelMembers(t *testing.T) {
	t.Parallel()
	input := `{"fields":{"summary":"native"},"update":{"labels":[{"add":"ready"}]},"historyMetadata":{"activityDescription":"<>&\u003c\/","futureCount":9007199254740993},"properties":[{"key":"fixture","value":null}],"transition":{"id":"31"},"futureMember":{"zero":0,"empty":[],"null":null,"false":false}}`
	var req IssueWriteRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		t.Fatal(err)
	}
	got, err := req.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	if err := json.Unmarshal([]byte(input), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &after); err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatal("native issue request silently dropped top-level members")
	}
	for name, value := range before {
		if string(after[name]) != string(value) {
			t.Fatalf("native issue request changed %s's numeric or string spelling", name)
		}
	}
	wire, err := MarshalNoEscape(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wire, &after); err != nil {
		t.Fatal(err)
	}
	for name, value := range before {
		if string(after[name]) != string(value) {
			t.Errorf("MarshalNoEscape(IssueWriteRequest) changed %s: got %s, want %s", name, after[name], value)
		}
	}
}

func TestIssueWriteRequestPropertiesOnlyIsNotEmpty(t *testing.T) {
	t.Parallel()
	var req IssueWriteRequest
	if err := json.Unmarshal([]byte(`{"properties":[{"key":"fixture","value":{"count":9007199254740993}}]}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.Empty() {
		t.Fatal("property-only edits were rejected as empty field changes")
	}
	if len(req.Keys()) != 0 {
		t.Fatal("top-level members were mistaken for field-flag conflicts")
	}
}

func TestIssueWriteRequestRetainsEmptyContainers(t *testing.T) {
	t.Parallel()
	for _, input := range []string{`{"fields":{},"properties":[]}`, `{"update":{},"futureMember":null}`} {
		var req IssueWriteRequest
		if err := json.Unmarshal([]byte(input), &req); err != nil {
			t.Fatal(err)
		}
		got, err := req.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		var before, after map[string]json.RawMessage
		if err := json.Unmarshal([]byte(input), &before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(got, &after); err != nil {
			t.Fatal(err)
		}
		if !maps.EqualFunc(before, after, func(a, b json.RawMessage) bool { return bytes.Equal(a, b) }) {
			t.Errorf("IssueWriteRequest.MarshalJSON(%s) = %s, want original member values", input, got)
		}
	}
}

func TestIssueWriteRequestDecodeReplacesAtomically(t *testing.T) {
	t.Parallel()
	var req IssueWriteRequest
	for _, input := range []string{
		`{"fields":{"summary":"old"},"update":{"labels":[{"add":"old"}]},"oldExtension":false}`,
		`{"fields":{"summary":"new"},"properties":[{"key":"new","value":null}]}`,
	} {
		if err := json.Unmarshal([]byte(input), &req); err != nil {
			t.Fatal(err)
		}
	}
	want := `{"fields":{"summary":"new"},"properties":[{"key":"new","value":null}]}`
	got, err := req.MarshalJSON()
	if err != nil || string(got) != want {
		t.Fatalf("IssueWriteRequest reused decode = %s, %v, want %s", got, err, want)
	}
	if err := json.Unmarshal([]byte(`{"fields":{"summary":"partial"},"update":[]}`), &req); err == nil {
		t.Fatal("IssueWriteRequest malformed decode = nil error, want input error")
	}
	got, err = req.MarshalJSON()
	if err != nil || string(got) != want {
		t.Errorf("IssueWriteRequest failed decode = %s, %v, want unchanged %s", got, err, want)
	}
}

func TestIssueWriteRequestExtraCannotShadowContainers(t *testing.T) {
	t.Parallel()
	req := IssueWriteRequest{Extra: map[string]json.RawMessage{
		"FIELDS": json.RawMessage(`{"summary":"shadow"}`),
		"Update": json.RawMessage(`{"labels":[{"add":"shadow"}]}`),
	}}
	if !req.Empty() {
		t.Error("IssueWriteRequest.Empty(reserved extras) = false, want true")
	}
	got, err := req.MarshalJSON()
	if err != nil || string(got) != `{}` {
		t.Errorf("IssueWriteRequest.MarshalJSON(reserved extras) = %s, %v, want {}", got, err)
	}
	if err := req.SetField("summary", "typed"); err != nil {
		t.Fatal(err)
	}
	got, err = req.MarshalJSON()
	if err != nil || string(got) != `{"fields":{"summary":"typed"}}` {
		t.Errorf("IssueWriteRequest.MarshalJSON(typed plus reserved extras) = %s, %v, want typed field only", got, err)
	}
	req.Extra["priority"] = json.RawMessage(`null`)
	if got := req.Keys(); len(got) != 1 || got[0] != "summary" {
		t.Errorf("IssueWriteRequest.Keys(top-level priority) = %v, want [summary]", got)
	}
}

func TestIssueWriteRequestOverridesOwnMaps(t *testing.T) {
	t.Parallel()
	var overrides IssueWriteRequest
	if err := json.Unmarshal([]byte(`{"fields":{"priority":{"name":"High"}},"update":{"labels":[{"add":"ready"}]},"properties":[{"key":"fixture","value":false}]}`), &overrides); err != nil {
		t.Fatal(err)
	}
	first := IssueWriteRequest{Fields: map[string]json.RawMessage{"summary": json.RawMessage(`"source"`)}, Extra: map[string]json.RawMessage{"base": json.RawMessage(`true`)}}
	second := IssueWriteRequest{}
	first.ApplyOverrides(overrides)
	second.ApplyOverrides(overrides)
	if string(first.Fields["summary"]) != `"source"` || string(first.Fields["priority"]) != `{"name":"High"}` || string(first.Extra["base"]) != `true` {
		t.Error("IssueWriteRequest.ApplyOverrides = lost base or missing override, want key-by-key overlay")
	}
	first.Fields["priority"] = json.RawMessage(`null`)
	first.Update["labels"] = json.RawMessage(`[]`)
	first.Extra["properties"] = json.RawMessage(`[]`)
	for name, req := range map[string]IssueWriteRequest{"input": overrides, "second": second} {
		if string(req.Fields["priority"]) != `{"name":"High"}` || string(req.Update["labels"]) != `[{"add":"ready"}]` || string(req.Extra["properties"]) != `[{"key":"fixture","value":false}]` {
			t.Errorf("IssueWriteRequest.ApplyOverrides map ownership: %s changed after another request's assignment", name)
		}
	}
	second.Fields["priority"] = json.RawMessage(`null`)
	second.Update["labels"] = json.RawMessage(`[]`)
	second.Extra["properties"] = json.RawMessage(`[]`)
	if string(overrides.Fields["priority"]) != `{"name":"High"}` || string(overrides.Update["labels"]) != `[{"add":"ready"}]` || string(overrides.Extra["properties"]) != `[{"key":"fixture","value":false}]` {
		t.Error("IssueWriteRequest.ApplyOverrides(empty destination) changed its input after destination assignments")
	}
}

func TestIssueWriteRequestOverridesRetainEmptyContainers(t *testing.T) {
	t.Parallel()
	var overrides IssueWriteRequest
	if err := json.Unmarshal([]byte(`{"fields":{},"update":{}}`), &overrides); err != nil {
		t.Fatal(err)
	}
	var req IssueWriteRequest
	req.ApplyOverrides(overrides)
	got, err := req.MarshalJSON()
	if err != nil || string(got) != `{"fields":{},"update":{}}` {
		t.Errorf("IssueWriteRequest.ApplyOverrides(empty containers) = %s, %v, want both explicit objects", got, err)
	}
	if !req.Empty() {
		t.Error("IssueWriteRequest.Empty(empty native containers) = false, want true")
	}
	req.Fields = nil
	if _, err := req.MarshalJSON(); err == nil {
		t.Error("IssueWriteRequest.MarshalJSON(cleared present container) = nil error, want explicit invalid state error")
	}
}

func TestIssueWriteRequestOverridesReplaceOppositeContainer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, base, override, want string
	}{
		{"operations replace value", `{"fields":{"labels":["source"],"summary":"Source"}}`, `{"update":{"labels":[{"set":["override"]}]}}`, `{"fields":{"summary":"Source"},"update":{"labels":[{"set":["override"]}]}}`},
		{"value replaces operations", `{"update":{"labels":[{"add":"source"}]}}`, `{"fields":{"labels":null}}`, `{"fields":{"labels":null},"update":{}}`},
		{"explicit conflicting input retained", `{"fields":{"labels":["source"]}}`, `{"fields":{"labels":false},"update":{"labels":[]}}`, `{"fields":{"labels":false},"update":{"labels":[]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var request, override IssueWriteRequest
			if json.Unmarshal([]byte(tc.base), &request) != nil || json.Unmarshal([]byte(tc.override), &override) != nil {
				t.Fatal("invalid authored override fixture")
			}
			request.ApplyOverrides(override)
			got, err := request.MarshalJSON()
			if err != nil || string(got) != tc.want {
				t.Fatalf("override changed native route precedence: got=%s err=%v want=%s", got, err, tc.want)
			}
		})
	}
}

func TestIssueWriteRequestRejectsInvalidRawExtension(t *testing.T) {
	t.Parallel()
	req := IssueWriteRequest{Extra: map[string]json.RawMessage{"futureMember": json.RawMessage(`{`)}}
	if _, err := MarshalNoEscape(req); err == nil {
		t.Error("MarshalNoEscape(IssueWriteRequest invalid raw extension) = nil error, want invalid JSON error")
	}
}

func TestIssueWriteRequestRejectsMalformedContainers(t *testing.T) {
	t.Parallel()
	for name, input := range map[string]string{
		"null-request":   `null`,
		"array-request":  `[]`,
		"scalar-request": `3`,
		"null-fields":    `{"fields":null,"properties":[]}`,
		"null-update":    `{"update":null,"futureMember":null}`,
		"array-fields":   `{"fields":[]}`,
		"scalar-update":  `{"update":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var req IssueWriteRequest
			if err := json.Unmarshal([]byte(input), &req); err == nil {
				t.Errorf("IssueWriteRequest.UnmarshalJSON(%s) = nil error, want invalid object error", input)
			}
		})
	}
}
