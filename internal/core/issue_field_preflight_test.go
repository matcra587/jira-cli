package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

func TestIssueFieldPreflightUsesOnlyNativeScreenAndOperations(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, field, value, update, operations string
		create, refused                        bool
	}{
		{name: "native future value", field: "customfield_future", value: `{"future":9007199254740993,"empty":[],"null":null,"false":false}`, operations: `["set"]`},
		{name: "null remains native", field: "summary", value: `null`, operations: `["set"]`},
		{name: "required value remains Jira validation", field: "summary", value: `false`, operations: `["set"]`},
		{name: "missing field", field: "missing", value: `"x"`, operations: `["set"]`, refused: true},
		{name: "set unavailable", field: "labels", value: `[]`, operations: `["add","remove"]`, refused: true},
		{name: "add remove", field: "labels", update: `[{"add":"a"},{"remove":"b"}]`, operations: `["add","remove"]`},
		{name: "future verb", field: "customfield_future", update: `[{"future":{"raw":9007199254740993}}]`, operations: `["future"]`},
		{name: "verb unavailable", field: "labels", update: `[{"remove":"a"}]`, operations: `["add"]`, refused: true},
		{name: "empty advertised operations", field: "labels", value: `[]`, operations: `[]`, refused: true},
		{name: "empty update", field: "labels", update: `[]`, operations: `["set"]`, refused: true},
		{name: "null update", field: "labels", update: `null`, operations: `["set"]`, refused: true},
		{name: "scalar update", field: "labels", update: `false`, operations: `["set"]`, refused: true},
		{name: "null operation", field: "labels", update: `[null]`, operations: `["set"]`, refused: true},
		{name: "empty operation", field: "labels", update: `[{}]`, operations: `["set"]`, refused: true},
		{name: "multiple verbs", field: "labels", update: `[{"add":"a","remove":"b"}]`, operations: `["add","remove"]`, refused: true},
		{name: "duplicate verb", field: "labels", update: `[{"add":"a","add":"b"}]`, operations: `["add"]`, refused: true},
		{name: "both containers checked", field: "labels", value: `[]`, update: `[{"add":"a"}]`, operations: `["set"]`, refused: true},
		{name: "create project exemption", field: "project", value: `{"id":"1"}`, operations: `[]`, create: true},
		{name: "create type exemption", field: "issuetype", value: `{"id":"2"}`, operations: `[]`, create: true},
		{name: "edit project not exempt", field: "project", value: `{"id":"1"}`, operations: `[]`, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{}, Update: map[string]json.RawMessage{}}
			if tc.value != "" {
				request.Fields[tc.field] = json.RawMessage(tc.value)
			}
			if tc.update != "" {
				request.Update[tc.field] = json.RawMessage(tc.update)
			}
			fields := map[string]json.RawMessage{}
			if tc.field != "missing" {
				fields[tc.field] = json.RawMessage(`{"key":"native","name":"Native","schema":{"type":"future"},"required":true,"operations":` + tc.operations + `}`)
			}
			before := bytes.Clone(request.Fields[tc.field])
			beforeUpdate := bytes.Clone(request.Update[tc.field])
			checked, err := PreflightIssueFields(t.Context(), request, fields, tc.create)
			var refusal *IssueFieldPreflightError
			if tc.refused {
				if !errors.As(err, &refusal) || refusal.Field != tc.field {
					t.Fatalf("preflight admitted an unavailable field/operation: checked=%v error=%v", checked, err)
				}
			} else if err != nil || checked == nil || (!tc.create && (len(checked) != 1 || checked[0] != tc.field)) {
				t.Fatalf("preflight lost an admitted native operation: checked=%v error=%v", checked, err)
			}
			if !bytes.Equal(before, request.Fields[tc.field]) || !bytes.Equal(beforeUpdate, request.Update[tc.field]) {
				t.Fatal("preflight changed native input")
			}
		})
	}
}

func TestIssueFieldPreflightRequiresValidMetadataAndHonorsCancellation(t *testing.T) {
	t.Parallel()
	request := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"summary": json.RawMessage(`"x"`)}}
	for _, raw := range []string{`null`, `{}`, `{"key":"s","name":"S","schema":{"type":"string"},"required":false,"operations":null}`, `{"key":"s","name":"S","schema":{"type":"string"},"required":false,"operations":["set"],"required":true}`} {
		_, err := PreflightIssueFields(t.Context(), request, map[string]json.RawMessage{"summary": json.RawMessage(raw)}, false)
		if !errors.Is(err, ErrIssueMetadataInvalid) {
			t.Fatalf("malformed metadata admitted: %v", err)
		}
	}
	if _, err := PreflightIssueFields(t.Context(), request, nil, false); !errors.Is(err, ErrIssueMetadataInvalid) {
		t.Fatalf("missing complete field map admitted: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := PreflightIssueFields(ctx, request, nil, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation became invalid metadata: %v", err)
	}
}
