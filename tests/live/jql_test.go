//go:build live

package live

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/envelope"
	wire "github.com/matcra587/jira-cli/internal/jira"
)

// TestLiveJQLComposition checks shared grammar against Jira's strict parser.
// Validation reads metadata only; it does not search or change matching issues.
func TestLiveJQLComposition(t *testing.T) {
	r := newLiveRun(t)
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	if !regexp.MustCompile("^" + regexp.QuoteMeta(r.manifest.Project) + "-[0-9]+$").MatchString(key) {
		t.Fatal("JQL composition must use the dedicated read key")
	}
	queries := []string{}
	for _, input := range []command.JQLInput{
		{Key: []string{key + ".." + key, key}, Assignee: []string{"me", "none"}, Reporter: []string{"currentUser()"}},
		{Project: []string{r.manifest.Project}, Status: []string{"To Do", ">=In Progress", "!Done"}},
		{Key: []string{key}, Created: "-365d..+2w", Updated: "<=2026-08-01", Resolved: "..-1d"},
		{Epic: []string{key}, OrderBy: "key", Descending: new(true)},
		{Key: []string{key}, OrderBy: "resolutiondate", CustomOrder: true, Descending: new(false)},
		{BoardJQL: "project = " + r.manifest.Project + " OR key = " + key + " ORDER\tBY Rank ASC", Status: []string{"!Done"}},
	} {
		query, err := command.ComposeJQL(input)
		if err != nil {
			t.Fatalf("cannot compose live query: %v", err)
		}
		queries = append(queries, query)
	}
	args := append([]string{"jql", "validate", "--mode=strict", "--"}, queries...)
	frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true", "AGENT=1"}, args...)
	var result struct {
		Data envelope.JQLValidateOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil || len(result.Data.Queries) != len(queries) {
		t.Fatal("live composed-query validation failed; inspect local receipts")
	}
	for i, query := range result.Data.Queries {
		if !query.Valid || len(query.Errors.Value) != 0 {
			t.Fatalf("strict parser rejected composed case %d; inspect local diagnostics", i)
		}
	}
}

// TestLiveJQLTransport exercises the shared raw read transport and the new wire
// boundary. Domain-command acceptance remains separate; no fixture is mutated.
func TestLiveJQLTransport(t *testing.T) {
	r := newLiveRun(t)
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	if !regexp.MustCompile("^" + regexp.QuoteMeta(r.manifest.Project) + "-[0-9]+$").MatchString(key) {
		t.Fatal("JQL read must address an explicit key in the dedicated project")
	}
	queries := []string{"key = " + key, "project = (", "key = " + key}
	body, err := wire.MarshalNoEscape(wire.JqlQueriesToParse{Queries: wire.NullableOf(queries)})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(r.dir, "jql-parse.json")
	if err := os.WriteFile(input, body, 0o600); err != nil {
		t.Fatal("cannot save the local parser input")
	}
	for _, mode := range []string{"strict", "warn", "none"} {
		out := rawRead(t, r, "api", "post", "/rest/api/3/jql/parse", "--query=validation="+mode, "--input", input)
		if out.Request.BodyBase64 == nil || *out.Request.BodyBase64 != base64.StdEncoding.EncodeToString(body) {
			t.Fatal("live parser changed input order, duplicates or native bytes")
		}
		parsed, err := wire.ParseJQLResponse(out.Response.JSON, len(queries))
		if err != nil {
			t.Fatalf("live parser disagrees with the pinned response boundary: %v", err)
		}
		for _, i := range []int{0, 2} {
			if len(parsed.Queries.Value[i].Errors.Value) != 0 || !parsed.Queries.Value[i].Structure.Set || parsed.Queries.Value[i].Structure.Null {
				t.Fatal("valid explicit-key query lacks a parsed syntax tree")
			}
		}
		if len(parsed.Queries.Value[1].Errors.Value) == 0 {
			t.Fatal("malformed query did not produce syntax diagnostics")
		}
		jqlNativeEquivalent(t, out.Response.JSON, parsed)
	}
	out := rawRead(t, r, "api", "get", "/rest/api/3/jql/autocompletedata")
	reference, err := wire.ParseJQLReferenceResponse(out.Response.JSON)
	if err != nil {
		t.Fatalf("live reference disagrees with the pinned response boundary: %v", err)
	}
	if len(reference.VisibleFieldNames.Value) == 0 || len(reference.VisibleFunctionNames.Value) == 0 || len(reference.JqlReservedWords.Value) == 0 {
		t.Fatal("live metadata visibility is insufficient for reference acceptance")
	}
	jqlNativeEquivalent(t, out.Response.JSON, reference)
}

// TestLiveJQLCommands compares domain output with independent raw reads. All
// invocations are read-only; semantic failures remain parser result data.
func TestLiveJQLCommands(t *testing.T) {
	r := newLiveRun(t)
	key := requiredEnv(t, "JIRA_LIVE_READ_KEY")
	if !regexp.MustCompile("^" + regexp.QuoteMeta(r.manifest.Project) + "-[0-9]+$").MatchString(key) {
		t.Fatal("JQL read must address an explicit key in the dedicated project")
	}
	queries := []string{"key = " + key, "project = (", "key = " + key, "jira_cli_missing_field_3e4f0a = 1"}
	body, err := wire.MarshalNoEscape(wire.JqlQueriesToParse{Queries: wire.NullableOf(queries)})
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(r.dir, "jql-domain-parse.json")
	if err := os.WriteFile(input, body, 0o600); err != nil {
		t.Fatal("cannot save the local parser input")
	}
	for _, mode := range []string{"strict", "warn", "none"} {
		raw := rawRead(t, r, "api", "post", "/rest/api/3/jql/parse", "--query=validation="+mode, "--input", input)
		args := append([]string{"jql", "validate", "--mode=" + mode, "--"}, queries...)
		frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true", "AGENT=1"}, args...)
		var result struct {
			Data envelope.JQLValidateOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil || result.Data.Mode != mode || !slices.Equal(result.Data.SubmittedQueries, queries) || len(result.Data.Queries) != len(queries) {
			t.Fatal("domain parser lost successful diagnostics, authored input or selected mode; inspect local receipts")
		}
		jqlNativeEquivalent(t, raw.Response.JSON, result.Data.Parsed)
		for i, query := range result.Data.Queries {
			native := result.Data.Parsed.Queries.Value[i]
			if query.Query != native.Query.Value || query.Valid != (len(native.Errors.Value) == 0) || !reflect.DeepEqual(query.Errors, native.Errors) || !reflect.DeepEqual(query.Warnings, native.Warnings) {
				t.Fatal("domain projection changed parser text, validity or nullable diagnostics")
			}
		}
		if !result.Data.Queries[0].Valid || result.Data.Queries[1].Valid || !result.Data.Queries[2].Valid || result.Data.Queries[3].Valid != (mode == "none") {
			t.Fatal("syntax or semantic diagnostics disagree with the selected mode")
		}
		semanticTree := result.Data.Parsed.Queries.Value[3].Structure
		if (semanticTree.Set && !semanticTree.Null) != (mode != "strict") {
			t.Fatal("syntax tree availability disagrees with strict/warn/none semantics")
		}
	}
	raw := rawRead(t, r, "api", "get", "/rest/api/3/jql/autocompletedata")
	frame, stdout, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true", "AGENT=1"}, "jql", "reference")
	var result struct {
		Data envelope.JQLReferenceOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &result) != nil || len(result.Data.Fields) == 0 || len(result.Data.Functions) == 0 || len(result.Data.ReservedWords.Value) == 0 {
		t.Fatal("domain reference lacks visible live discovery; inspect local receipts")
	}
	native, err := wire.MarshalNoEscape(result.Data.Reference)
	if err != nil || !jqlReferenceReadsEquivalent(raw.Response.JSON, native) {
		t.Fatal("domain reference differs from independent native discovery")
	}
	if len(result.Data.Fields) != len(result.Data.Reference.VisibleFieldNames.Value) || len(result.Data.Functions) != len(result.Data.Reference.VisibleFunctionNames.Value) || !reflect.DeepEqual(result.Data.ReservedWords, result.Data.Reference.JqlReservedWords) {
		t.Fatal("domain metadata counts or reserved words differ from native reference")
	}
	for i, field := range result.Data.Fields {
		native := result.Data.Reference.VisibleFieldNames.Value[i]
		if !reflect.DeepEqual(field.Value, native.Value) || !reflect.DeepEqual(field.DisplayName, native.DisplayName) || !reflect.DeepEqual(field.CustomFieldID, native.Cfid) {
			t.Fatal("domain field discovery changed native identity or null metadata")
		}
	}
	for i, function := range result.Data.Functions {
		native := result.Data.Reference.VisibleFunctionNames.Value[i]
		if !reflect.DeepEqual(function.Value, native.Value) || !reflect.DeepEqual(function.DisplayName, native.DisplayName) {
			t.Fatal("domain function discovery changed native identity or null metadata")
		}
	}
}

// Jira may reorder reserved words and field operators between independent
// reads. Compare their multisets while retaining all other ordering and data.
// Native output order itself is checked against fixed responses hermetically.
func jqlReferenceReadsEquivalent(firstRaw, secondRaw []byte) bool {
	objects := make([]map[string]any, 2)
	for i, raw := range [][]byte{firstRaw, secondRaw} {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&objects[i]) != nil || objects[i] == nil {
			return false
		}
		arrays := []any{objects[i]["jqlReservedWords"]}
		if fields, ok := objects[i]["visibleFieldNames"].([]any); ok {
			for _, row := range fields {
				field, ok := row.(map[string]any)
				if !ok {
					return false
				}
				arrays = append(arrays, field["operators"])
			}
		}
		for _, value := range arrays {
			array, ok := value.([]any)
			if !ok {
				continue
			}
			for _, item := range array {
				if _, ok := item.(string); !ok {
					return false
				}
			}
			slices.SortFunc(array, func(a, b any) int { return strings.Compare(a.(string), b.(string)) })
		}
	}
	return reflect.DeepEqual(objects[0], objects[1])
}

func TestJQLReferenceReadComparisonRetainsAllOtherData(t *testing.T) {
	t.Parallel()
	first := []byte(`{"jqlReservedWords":["and","or"],"visibleFieldNames":[{"value":"first","operators":["=","in"]},{"value":"second","operators":null}],"future":{"large":9007199254740993,"array":[1,2]}}`)
	for _, test := range []struct {
		name string
		body string
		want bool
	}{
		{"reordered collections", `{"jqlReservedWords":["or","and"],"visibleFieldNames":[{"value":"first","operators":["in","="]},{"value":"second","operators":null}],"future":{"large":9007199254740993,"array":[1,2]}}`, true},
		{"missing word", `{"jqlReservedWords":["and"],"visibleFieldNames":[{"value":"first","operators":["=","in"]},{"value":"second","operators":null}],"future":{"large":9007199254740993,"array":[1,2]}}`, false},
		{"duplicate word", `{"jqlReservedWords":["and","and","or"],"visibleFieldNames":[{"value":"first","operators":["=","in"]},{"value":"second","operators":null}],"future":{"large":9007199254740993,"array":[1,2]}}`, false},
		{"missing operator", `{"jqlReservedWords":["and","or"],"visibleFieldNames":[{"value":"first","operators":["="]},{"value":"second","operators":null}],"future":{"large":9007199254740993,"array":[1,2]}}`, false},
		{"reordered fields", `{"jqlReservedWords":["and","or"],"visibleFieldNames":[{"value":"second","operators":null},{"value":"first","operators":["=","in"]}],"future":{"large":9007199254740993,"array":[1,2]}}`, false},
		{"null became absent", `{"jqlReservedWords":["and","or"],"visibleFieldNames":[{"value":"first","operators":["=","in"]},{"value":"second"}],"future":{"large":9007199254740993,"array":[1,2]}}`, false},
		{"extension precision", `{"jqlReservedWords":["and","or"],"visibleFieldNames":[{"value":"first","operators":["=","in"]},{"value":"second","operators":null}],"future":{"large":9007199254740992,"array":[1,2]}}`, false},
		{"extension array order", `{"jqlReservedWords":["and","or"],"visibleFieldNames":[{"value":"first","operators":["=","in"]},{"value":"second","operators":null}],"future":{"large":9007199254740993,"array":[2,1]}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if jqlReferenceReadsEquivalent(first, []byte(test.body)) != test.want {
				t.Fatal("comparison ignores native data or mistakes collection order for a data change")
			}
		})
	}
}

func jqlNativeEquivalent(t *testing.T, raw []byte, typed any) {
	t.Helper()
	encoded, err := wire.MarshalNoEscape(typed)
	if err != nil {
		t.Fatal("cannot serialize typed native JQL response")
	}
	var native, roundtrip any
	first := json.NewDecoder(bytes.NewReader(raw))
	first.UseNumber()
	second := json.NewDecoder(bytes.NewReader(encoded))
	second.UseNumber()
	if first.Decode(&native) != nil || second.Decode(&roundtrip) != nil || !reflect.DeepEqual(native, roundtrip) {
		t.Fatal("typed JQL decoding changed native metadata, diagnostics or syntax tree")
	}
}
