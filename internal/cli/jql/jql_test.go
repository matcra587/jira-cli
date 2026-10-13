package jql

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/jira"
)

type fakeClient struct {
	core.JiraClient
	parsed    *jira.ParsedJqlQueries
	reference *jira.JQLReferenceData
	queries   []string
	mode      core.JQLValidationMode
	err       error
}

func (f *fakeClient) ParseJQL(ctx context.Context, queries []string, mode core.JQLValidationMode) (core.JQLParseRead, error) {
	if err := ctx.Err(); err != nil {
		return core.JQLParseRead{}, err
	}
	f.queries, f.mode = slices.Clone(queries), mode
	return core.JQLParseRead{Parsed: f.parsed}, f.err
}

func (f *fakeClient) GetJQLReference(ctx context.Context) (core.JQLReferenceRead, error) {
	if err := ctx.Err(); err != nil {
		return core.JQLReferenceRead{}, err
	}
	return core.JQLReferenceRead{Reference: f.reference}, f.err
}

func TestValidateResultsKeepDiagnosticsAuthoredTextAndNative(t *testing.T) {
	t.Parallel()
	native, err := jira.ParseJQLResponse([]byte(`{"queries":[{"query":"normalized","errors":null,"warnings":["warning only"],"structure":{"future":"<&>"}},{"query":"invalid","errors":["syntax error"],"warnings":[]},{"query":"same","errors":[]}],"future":{"large":9007199254740993}}`), 3)
	if err != nil {
		t.Fatal(err)
	}
	queries := []string{"authored", "invalid", "authored"}
	fake := &fakeClient{parsed: &native}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}
	for _, mode := range []core.JQLValidationMode{core.JQLValidationStrict, core.JQLValidationWarn, core.JQLValidationNone} {
		out, err := runValidate(t.Context(), deps, queries, mode)
		if err != nil || out.Mode != string(mode) || !reflect.DeepEqual(fake.queries, queries) || fake.mode != mode || !reflect.DeepEqual(out.SubmittedQueries, queries) || len(out.Queries) != 3 || !out.Queries[0].Valid || out.Queries[1].Valid || !out.Queries[2].Valid || out.Queries[0].Query != "normalized" || !out.Queries[0].Errors.Null || out.Queries[1].Errors.Value[0] != "syntax error" || !reflect.DeepEqual(out.Parsed, native) {
			t.Fatalf("validity or complete response changed: %+v %v", out, err)
		}
		for i, query := range out.Queries {
			if !reflect.DeepEqual(query.Errors, native.Queries.Value[i].Errors) || !reflect.DeepEqual(query.Warnings, native.Queries.Value[i].Warnings) {
				t.Fatal("convenient diagnostics differ from the complete native response")
			}
		}
		queries[0] = "changed after return"
		if out.SubmittedQueries[0] == queries[0] {
			t.Fatal("returned authored text aliases mutable caller input")
		}
		queries[0] = "authored"
	}
}

func TestJQLLocalRefusalsAndCancellationNeedNoClient(t *testing.T) {
	t.Parallel()
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		t.Error("local refusal constructed a client")
		return nil, errors.New("unexpected client")
	}}
	for _, test := range []struct {
		queries     []string
		mode        core.JQLValidationMode
		code, field string
	}{
		{[]string{"one"}, "STRICT", cli.CodeFlagValueInvalid, "mode"}, {nil, core.JQLValidationStrict, cli.CodeArgValueInvalid, "QUERY"}, {[]string{""}, core.JQLValidationStrict, cli.CodeArgValueInvalid, "QUERY"},
	} {
		_, err := runValidate(t.Context(), deps, test.queries, test.mode)
		input, ok := errors.AsType[*cli.InputError](err)
		if !ok || input.Code != test.code || input.Field != test.field {
			t.Fatalf("local refusal lost classification: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := runValidate(ctx, deps, []string{"one"}, core.JQLValidationStrict); !errors.Is(err, context.Canceled) {
		t.Fatalf("parse lost cancellation: %v", err)
	}
	if _, err := runReference(ctx, deps); !errors.Is(err, context.Canceled) {
		t.Fatalf("reference lost cancellation: %v", err)
	}
}

func TestJQLCommandsRejectMissingOrIncompleteReadResults(t *testing.T) {
	t.Parallel()
	for _, parsed := range []*jira.ParsedJqlQueries{nil, {}, {Queries: jira.NullableNull[[]jira.ParsedJqlQuery]()}, {Queries: jira.NullableOf([]jira.ParsedJqlQuery{})}, {Queries: jira.NullableOf([]jira.ParsedJqlQuery{{}})}} {
		fake := &fakeClient{parsed: parsed}
		deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}
		out, err := runValidate(t.Context(), deps, []string{"one"}, core.JQLValidationStrict)
		if err == nil || cli.MapError(err).Code != cli.CodeJQLResponseInvalid || len(out.Queries) > 0 {
			t.Fatalf("incomplete parser result accepted: %+v %v", out, err)
		}
	}
	fake := &fakeClient{}
	if _, err := runReference(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}); err == nil || cli.MapError(err).Code != cli.CodeJQLResponseInvalid {
		t.Fatalf("nil reference became success: %v", err)
	}
}

func TestJQLReferenceProjectsDiscoveryAndKeepsNative(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{}`, `{"visibleFieldNames":null,"visibleFunctionNames":[],"jqlReservedWords":null}`, `{"visibleFieldNames":[{"value":"custom","displayName":null,"cfid":"cf[12345]","searchable":"false","future":{"large":9007199254740993}}],"visibleFunctionNames":[{"value":"currentUser()","displayName":"Current user","isList":"false"}],"jqlReservedWords":["and"],"future":false}`} {
		native, err := jira.ParseJQLReferenceResponse([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		fake := &fakeClient{reference: &native}
		out, err := runReference(t.Context(), Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }})
		if err != nil || out.Fields == nil || out.Functions == nil || !reflect.DeepEqual(out.Reference, native) || !reflect.DeepEqual(out.ReservedWords, native.JqlReservedWords) || len(out.Fields) != len(native.VisibleFieldNames.Value) || len(out.Functions) != len(native.VisibleFunctionNames.Value) {
			t.Fatalf("reference projection collapsed native state: %+v %v", out, err)
		}
		if len(out.Fields) > 0 && (!out.Fields[0].DisplayName.Null || out.Fields[0].CustomFieldID.Value != "cf[12345]" || out.Functions[0].Value.Value != "currentUser()") {
			t.Fatal("discovery projection lost custom-field identity or null display name")
		}
		encoded, err := jira.MarshalNoEscape(out)
		if err != nil || !json.Valid(encoded) {
			t.Fatalf("reference output failed native serialization: %v", err)
		}
	}
}

func TestJQLReadFailuresRemainErrors(t *testing.T) {
	t.Parallel()
	want := errors.New("fixture transport failure")
	fake := &fakeClient{err: want}
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}
	if out, err := runValidate(t.Context(), deps, []string{"one"}, core.JQLValidationStrict); !errors.Is(err, want) || len(out.Queries) > 0 {
		t.Fatalf("parser failure fabricated results: %+v %v", out, err)
	}
	if _, err := runReference(t.Context(), deps); !errors.Is(err, want) {
		t.Fatalf("reference swallowed read error: %v", err)
	}
}
