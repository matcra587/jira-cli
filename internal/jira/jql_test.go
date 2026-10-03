package jira

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestJQLParseResponseFidelity(t *testing.T) {
	t.Parallel()
	const body = `{"queries":[{"query":"normalized text","errors":null,"warnings":[],"structure":{"future":{"large":9007199254740993,"text":"<&>"}},"future":[null,0,false,[]]},{"query":"bad syntax","errors":["unexpected token"],"warnings":["warning"]}],"future":{"count":9007199254740993}}`
	out, err := ParseJQLResponse([]byte(body), 2)
	if err != nil {
		t.Fatal(err)
	}
	first, second := out.Queries.Value[0], out.Queries.Value[1]
	if !first.Errors.Null || !first.Warnings.Set || first.Warnings.Null || first.Warnings.Value == nil || second.Structure.Set || second.Errors.Value[0] != "unexpected token" || first.Query.Value != "normalized text" {
		t.Fatal("diagnostics, normalized text or nullable containers changed")
	}
	if string(first.Structure.Value) != `{"future":{"large":9007199254740993,"text":"<&>"}}` || string(first.Extra["future"]) != `[null,0,false,[]]` || string(out.Extra["future"]) != `{"count":9007199254740993}` {
		t.Fatal("native syntax tree or extensions changed")
	}
	roundtrip, err := MarshalNoEscape(out)
	if err != nil || !json.Valid(roundtrip) || !bytes.Contains(roundtrip, first.Structure.Value) {
		t.Fatalf("raw syntax tree changed on output: %s %v", roundtrip, err)
	}
	duplicate, err := ParseJQLResponse([]byte(`{"queries":[{"query":"same"},{"query":"same"}]}`), 2)
	if err != nil || len(duplicate.Queries.Value) != 2 {
		t.Fatalf("duplicate authored queries discarded: %v", err)
	}
}

func TestJQLParseRejectsMalformedResponse(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"null root": `null`, "array root": `[]`, "absent queries": `{}`, "null queries": `{"queries":null}`, "empty queries": `{"queries":[]}`, "oversized queries": `{"queries":[{"query":"one"},{"query":"two"}]}`,
		"null row": `{"queries":[null]}`, "array row": `{"queries":[[]]}`, "missing text": `{"queries":[{}]}`, "null text": `{"queries":[{"query":null}]}`, "wrong text": `{"queries":[{"query":false}]}`,
		"duplicate root": `{"queries":[],"queries":[{"query":"one"}]}`, "case root": `{"Queries":[{"query":"one"}]}`, "duplicate row": `{"queries":[{"query":"one","query":"two"}]}`, "escaped duplicate": `{"queries":[{"query":"one","qu\u0065ry":"two"}]}`, "case row": `{"queries":[{"query":"one","Errors":[]}]}`,
		"null diagnostic": `{"queries":[{"query":"one","errors":[null]}]}`, "wrong diagnostic": `{"queries":[{"query":"one","warnings":[4]}]}`, "wrong array": `{"queries":[{"query":"one","errors":{}}]}`, "wrong structure": `{"queries":[{"query":"one","structure":false}]}`, "trailing object": `{"queries":[{"query":"one"}]} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := ParseJQLResponse([]byte(body), 1)
			if err == nil || out.Queries.Set {
				t.Fatalf("malformed response accepted: %+v %v", out, err)
			}
		})
	}
}

func TestJQLReferenceResponseFidelity(t *testing.T) {
	t.Parallel()
	const body = `{"visibleFieldNames":[{"value":"summary","searchable":"false","auto":"true","deprecated":null,"orderable":"future","operators":[],"types":null,"future":{"large":9007199254740993,"text":"<&>"}},{"value":"custom","cfid":"cf[12345]"}],"visibleFunctionNames":[{"value":"currentUser()","isList":"false","supportsListAndSingleValueOperators":"true","types":["user"],"future":false}],"jqlReservedWords":["and","or"],"future":[]}`
	out, err := ParseJQLReferenceResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	field := out.VisibleFieldNames.Value[0]
	function := out.VisibleFunctionNames.Value[0]
	if field.Searchable.Value != "false" || field.Auto.Value != "true" || !field.Deprecated.Null || field.Orderable.Value != "future" || field.Operators.Value == nil || !field.Types.Null || out.VisibleFieldNames.Value[1].Searchable.Set || function.IsList.Value != "false" || function.SupportsListAndSingleValueOperators.Value != "true" {
		t.Fatal("reference strings or nullable fields collapsed")
	}
	if string(field.Extra["future"]) != `{"large":9007199254740993,"text":"<&>"}` || string(function.Extra["future"]) != "false" || string(out.Extra["future"]) != "[]" {
		t.Fatal("reference extensions changed")
	}
	roundtrip, err := MarshalNoEscape(out)
	if err != nil || !bytes.Contains(roundtrip, field.Extra["future"]) {
		t.Fatalf("native metadata changed on output: %s %v", roundtrip, err)
	}
	for _, body := range []string{`{}`, `{"visibleFieldNames":null,"visibleFunctionNames":[],"jqlReservedWords":null}`, `{"visibleFieldNames":[{}],"visibleFunctionNames":[{}]}`} {
		if _, err := ParseJQLReferenceResponse([]byte(body)); err != nil {
			t.Fatalf("optional metadata rejected: %v", err)
		}
	}
}

func TestJQLReferenceKeepsUnmodeledFunctionOperators(t *testing.T) {
	t.Parallel()
	out, err := ParseJQLReferenceResponse([]byte(`{"visibleFunctionNames":[{"value":"future()","operators":[null]}]}`))
	if err != nil || len(out.VisibleFunctionNames.Value) != 1 || string(out.VisibleFunctionNames.Value[0].Extra["operators"]) != `[null]` {
		t.Fatalf("unmodeled function extension was interpreted as a field property: %+v %v", out, err)
	}
}

func TestJQLReferenceRejectsMalformedResponse(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"null root": `null`, "array root": `[]`, "wrong field list": `{"visibleFieldNames":{}}`, "null field row": `{"visibleFieldNames":[null]}`, "null function row": `{"visibleFunctionNames":[null]}`, "duplicate root": `{"jqlReservedWords":[],"jqlReservedWords":[]}`, "case root": `{"VisibleFieldNames":[]}`,
		"duplicate field": `{"visibleFieldNames":[{"value":"one","value":"two"}]}`, "case field": `{"visibleFieldNames":[{"Value":"one"}]}`, "duplicate function": `{"visibleFunctionNames":[{"isList":"false","isList":"true"}]}`, "case function": `{"visibleFunctionNames":[{"IsList":"false"}]}`, "boolean field": `{"visibleFieldNames":[{"searchable":false}]}`, "boolean function": `{"visibleFunctionNames":[{"isList":true}]}`,
		"null word": `{"jqlReservedWords":[null]}`, "null operator": `{"visibleFieldNames":[{"operators":[null]}]}`, "null type": `{"visibleFunctionNames":[{"types":[null]}]}`, "wrong type": `{"visibleFunctionNames":[{"types":[false]}]}`, "trailing data": `{} []`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := ParseJQLReferenceResponse([]byte(body))
			if err == nil || out.VisibleFieldNames.Set {
				t.Fatalf("malformed reference accepted: %+v %v", out, err)
			}
		})
	}
}
