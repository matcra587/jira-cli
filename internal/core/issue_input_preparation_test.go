package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

func inputPreparationMetadata(field, schema string) json.RawMessage {
	return json.RawMessage(`{"key":"` + field + `","name":"Native field","required":false,"operations":["set","add","remove"],"schema":` + schema + `}`)
}

func TestIssueInputPreparationAuthorsBaselineShapes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, schema, input, want string
	}{
		{"string", `{"type":"string","custom":"textfield"}`, `"text"`, `"text"`},
		{"number", `{"type":"number","custom":"float"}`, `9007199254740993`, `9007199254740993`},
		{"numeric string", `{"type":"number","custom":"float"}`, `"1.25"`, `1.25`},
		{"large numeric string", `{"type":"number","custom":"float"}`, `"9007199254740993"`, `9007199254740993`},
		{"numeric string spelling", `{"type":"number","custom":"float"}`, `"1.00e+9"`, `1.00e+9`},
		{"date", `{"type":"string","custom":"datepicker"}`, `"2028-02-29"`, `"2028-02-29"`},
		{"datetime", `{"type":"string","custom":"datetime"}`, `"2026-10-01T12:00:00.000+0000"`, `"2026-10-01T12:00:00.000+0000"`},
		{"labels", `{"type":"array","custom":"labels"}`, `["one","two"]`, `["one","two"]`},
		{"select label", `{"type":"option","custom":"com.atlassian.jira.plugin.system.customfieldtypes:select"}`, `"High"`, `{"value":"High"}`},
		{"select numeric label", `{"type":"option","custom":"select"}`, `"123"`, `{"value":"123"}`},
		{"multiselect", `{"type":"array","custom":"multiselect"}`, `["High", { "id":"2", "future":9007199254740993 }]`, `[{"value":"High"},{ "id":"2", "future":9007199254740993 }]`},
		{"user", `{"type":"user","custom":"userpicker"}`, `"account-1"`, `{"accountId":"account-1"}`},
		{"multiuser", `{"type":"array","custom":"multiuserpicker"}`, `["account-1"]`, `[{"accountId":"account-1"}]`},
		{"group", `{"type":"group","custom":"grouppicker"}`, `"team"`, `{"name":"team"}`},
		{"multigroup", `{"type":"array","custom":"multigrouppicker"}`, `["team"]`, `[{"name":"team"}]`},
		{"versionpicker", `{"type":"version","custom":"version"}`, `"123"`, `{"name":"123"}`},
		{"multiversionpicker", `{"type":"array","custom":"multiversion"}`, `["v1"]`, `[{"name":"v1"}]`},
		{"projectpicker", `{"type":"project","custom":"project"}`, `"123"`, `{"key":"123"}`},
		{"cascadingselect", `{"type":"option","custom":"cascadingselect"}`, `{ "value":"parent","child":{"value":"child","future":false} }`, `{ "value":"parent","child":{"value":"child","future":false} }`},
		{"textarea Markdown", `{"type":"string","custom":"textarea"}`, `"**Bold**"`, `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Bold","marks":[{"type":"strong"}]}]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := json.RawMessage(tc.input)
			request := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"customfield_1": raw}}
			before := bytes.Clone(raw)
			prepared, err := PrepareIssueInput(t.Context(), request, map[string]json.RawMessage{"customfield_1": inputPreparationMetadata("customfield_1", tc.schema)}, false)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(prepared.Payload.Fields["customfield_1"]); got != tc.want {
				t.Fatalf("prepared value changed identity or native bytes: got %s want %s", got, tc.want)
			}
			if !bytes.Equal(request.Fields["customfield_1"], before) {
				t.Fatal("preparation mutated the caller's input")
			}
			if len(prepared.Fields) != 1 || prepared.Fields[0].Field != "customfield_1" || prepared.Fields[0].Handling == "unverified" {
				t.Fatalf("known preparation lacks an explicit handling receipt: %+v", prepared.Fields)
			}
		})
	}
}

func TestIssueInputPreparationPreservesNativeAndUnknownBytes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, schema, raw string }{
		{"native select", `{"type":"option","custom":"select"}`, `{ "id":"1", "future":"<>&\u2028", "number":9007199254740993 }`},
		{"native list", `{"type":"array","custom":"multiselect"}`, `[ { "id":"1","future":null } ]`},
		{"empty list", `{"type":"array","custom":"multiselect"}`, `[]`},
		{"explicit null", `{"type":"option","custom":"select"}`, `null`},
		{"unknown plugin suffix", `{"type":"option","custom":"example.vendor:select"}`, `42`},
		{"unknown plugin scalar", `{"type":"string","custom":"example.vendor:future"}`, `"original"`},
		{"unknown type", `{"type":"future"}`, `{ "empty":[],"false":false,"null":null,"zero":0 }`},
		{"null plugin schema", `{"type":"future","custom":null}`, `{ "future":0 }`},
		{"native future ADF", `{"type":"string","custom":"textarea"}`, `{ "type":"doc", "version":1, "content":[{"type":"futureNode","attrs":{"extension":"<>&"}}], "future":1 }`},
		{"unsupported native ADF version", `{"type":"string","custom":"textarea"}`, `{"type":"doc","version":2,"content":[]}`},
		{"malformed known native ADF", `{"type":"string","custom":"textarea"}`, `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text"}]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"customfield_1": json.RawMessage(tc.raw)}, Extra: map[string]json.RawMessage{"future": json.RawMessage(`{ "numeric":1.00e+9,"zero":0 }`)}}
			prepared, err := PrepareIssueInput(t.Context(), request, map[string]json.RawMessage{"customfield_1": inputPreparationMetadata("customfield_1", tc.schema)}, false)
			if err != nil {
				t.Fatal(err)
			}
			if string(prepared.Payload.Fields["customfield_1"]) != tc.raw || string(prepared.Payload.Extra["future"]) != string(request.Extra["future"]) {
				t.Fatal("explicit preparation normalized preserved native bytes")
			}
			if len(prepared.Fields) != 1 {
				t.Fatal("missing field handling receipt")
			}
			if tc.name == "explicit null" || tc.name == "unknown plugin suffix" || tc.name == "unknown plugin scalar" || tc.name == "unknown type" || tc.name == "null plugin schema" || tc.schema == `{"type":"string","custom":"textarea"}` {
				if prepared.Fields[0].Handling != "unverified" {
					t.Fatal("an unchecked value claimed local validation")
				}
			}
		})
	}
}

func TestIssueInputPreparationRefusesKnownMalformedValuesAtomically(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, kind, value string }{
		{"select number", "select", `42`},
		{"select empty", "select", `""`},
		{"select identity type", "select", `{"id":42}`},
		{"mixed invalid list", "multiselect", `["first",42]`},
		{"wrong list container", "multiuserpicker", `"account"`},
		{"group object", "grouppicker", `{"id":"1"}`},
		{"string number", "textfield", `42`},
		{"number boolean", "float", `false`},
		{"nonfinite numeric string", "float", `"NaN"`},
		{"invalid date", "datepicker", `"2026-02-29"`},
		{"datetime missing zone", "datetime", `"2026-10-01T12:00:00"`},
		{"labels nonstring", "labels", `[42]`},
		{"cascading malformed child", "cascadingselect", `{"value":"parent","child":false}`},
		{"textarea scalar", "textarea", `42`},
		{"textarea loss", "textarea", `"<div>lost</div>"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"customfield_1": json.RawMessage(`"first"`), "customfield_2": json.RawMessage(tc.value)}}
			fields := map[string]json.RawMessage{
				"customfield_1": inputPreparationMetadata("customfield_1", `{"type":"option","custom":"select"}`),
				"customfield_2": inputPreparationMetadata("customfield_2", `{"type":"native","custom":"`+tc.kind+`"}`),
			}
			out, err := PrepareIssueInput(t.Context(), request, fields, false)
			var refused *IssueInputPreparationError
			if !errors.As(err, &refused) || refused.Field != "customfield_2" || out.Payload.Fields != nil || len(out.Fields) != 0 {
				t.Fatalf("invalid known value produced an accepted or partial candidate: %+v %v", out, err)
			}
			if string(request.Fields["customfield_1"]) != `"first"` || string(request.Fields["customfield_2"]) != tc.value {
				t.Fatal("failed preparation changed input")
			}
		})
	}
}

func TestIssueInputPreparationFlattensOnlyExplicitLegacyWrapper(t *testing.T) {
	t.Parallel()
	metadata := map[string]json.RawMessage{"customfield_1": inputPreparationMetadata("customfield_1", `{"type":"option","custom":"select"}`)}
	for _, tc := range []struct {
		name, payload string
		refused       bool
	}{
		{"legacy wrapper", `{"fields":{"custom_fields":{"customfield_1":"High"}},"future":{"native":false}}`, false},
		{"collision", `{"fields":{"customfield_1":"Low","custom_fields":{"customfield_1":"High"}}}`, true},
		{"null wrapper", `{"fields":{"custom_fields":null}}`, true},
		{"scalar wrapper", `{"fields":{"custom_fields":42}}`, true},
		{"unknown field", `{"fields":{"custom_fields":{"not_on_screen":"High"}}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var request jira.IssueWriteRequest
			if err := json.Unmarshal([]byte(tc.payload), &request); err != nil {
				t.Fatal(err)
			}
			out, err := PrepareIssueInput(t.Context(), request, metadata, false)
			if tc.refused {
				if err == nil || out.Payload.Fields != nil {
					t.Fatalf("invalid wrapper accepted: %+v %v", out, err)
				}
				return
			}
			if err != nil || string(out.Payload.Fields["customfield_1"]) != `{"value":"High"}` || len(out.Payload.Fields) != 1 || string(out.Payload.Extra["future"]) != `{"native":false}` {
				t.Fatalf("wrapper preparation lost a field or extension: %+v %v", out, err)
			}
			if string(request.Fields["custom_fields"]) != `{"customfield_1":"High"}` {
				t.Fatal("preparation changed the original wrapper")
			}
		})
	}
}

func TestIssueInputPreparationRetainsUncheckedUpdateAndCancellation(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`[{"add":{ "future":9007199254740993 }}]`)
	request := jira.IssueWriteRequest{Update: map[string]json.RawMessage{"customfield_1": raw}}
	fields := map[string]json.RawMessage{"customfield_1": inputPreparationMetadata("customfield_1", `{"type":"array","custom":"multiselect"}`)}
	out, err := PrepareIssueInput(t.Context(), request, fields, false)
	if err != nil || !bytes.Equal(out.Payload.Update["customfield_1"], raw) || len(out.Fields) != 1 || out.Fields[0].Handling != "unverified" {
		t.Fatalf("native update value inferred or claimed validated: %+v %v", out, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := PrepareIssueInput(ctx, request, nil, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost priority: %v", err)
	}
}

func TestIssueInputPreparationSystemIdentitiesNeverInferIDs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ field, input, want string }{
		{"project", `"123"`, `{"key":"123"}`},
		{"parent", `"KEY-123"`, `{"key":"KEY-123"}`},
		{"issuetype", `"123"`, `{"name":"123"}`},
		{"priority", `"123"`, `{"name":"123"}`},
		{"assignee", `"account-123"`, `{"accountId":"account-123"}`},
		{"reporter", `"account-123"`, `{"accountId":"account-123"}`},
		{"components", `["123",{ "id":"456", "future":null }]`, `[{"name":"123"},{ "id":"456", "future":null }]`},
		{"versions", `["123"]`, `[{"name":"123"}]`},
		{"fixVersions", `["123"]`, `[{"name":"123"}]`},
	} {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			request := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{tc.field: json.RawMessage(tc.input)}}
			metadata := map[string]json.RawMessage{tc.field: inputPreparationMetadata(tc.field, `{"type":"native","system":"`+tc.field+`"}`)}
			out, err := PrepareIssueInput(t.Context(), request, metadata, false)
			if err != nil || string(out.Payload.Fields[tc.field]) != tc.want {
				t.Fatalf("system preparation changed the baseline identity meaning: %+v %v", out, err)
			}
			if string(request.Fields[tc.field]) != tc.input {
				t.Fatal("system preparation changed the original request")
			}
		})
	}
}

func TestIssueInputPreparationParentKeyShapeRefusesMalformedKeys(t *testing.T) {
	t.Parallel()
	metadata := map[string]json.RawMessage{"parent": inputPreparationMetadata("parent", `{"type":"issuelink","system":"parent"}`)}
	for _, raw := range []string{`"not-a-key"`, `{"key":"not-a-key"}`, `{"key":"123","id":"456"}`} {
		out, err := PrepareIssueInput(t.Context(), jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"parent": json.RawMessage(raw)}}, metadata, false)
		var refused *IssueInputPreparationError
		if !errors.As(err, &refused) || refused.Field != "parent" || out.Payload.Fields != nil {
			t.Fatalf("malformed parent key claimed preparation: %+v %v", out, err)
		}
	}
	const nativeID = `{ "id":"456", "future":null }`
	out, err := PrepareIssueInput(t.Context(), jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"parent": json.RawMessage(nativeID)}}, metadata, false)
	if err != nil || string(out.Payload.Fields["parent"]) != nativeID {
		t.Fatal("explicit native parent ID was guessed or normalized", err)
	}
}
