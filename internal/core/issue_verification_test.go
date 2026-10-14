package core

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
)

func TestCompareIssueWriteNativeValuesAndOperations(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, field, want, got, operations, outcome, reason string
	}{
		{"opaque object identity is data", "future_field", `{"name":"x"}`, `{"name":"x","future":true}`, "", "differs", "native-value"},
		{"malformed object identity is data", "customfield_1", `{"id":false}`, `{"id":false,"future":true}`, "", "differs", "native-value"},
		{"remove missing observed component ID", "components", "", `[{"name":"API"}]`, `[{"remove":{"id":"1"}}]`, "unverified", "collection-identity-unavailable"},
		{"same string", "summary", `"same"`, `"same"`, "", "matches", "native-value"},
		{"different string", "summary", `"want"`, `"got"`, "", "differs", "native-value"},
		{"large number differs", "customfield_1", `9007199254740993`, `9007199254740992`, "", "differs", "native-value"},
		{"number spelling", "customfield_1", `1.0`, `1`, "", "differs", "native-value"},
		{"zero", "customfield_1", `0`, `0`, "", "matches", "native-value"},
		{"false", "customfield_1", `false`, `false`, "", "matches", "native-value"},
		{"null", "customfield_1", `null`, `null`, "", "matches", "native-value"},
		{"absent clear", "customfield_1", `null`, "", "", "unverified", "field-not-returned"},
		{"missing value", "customfield_1", `"wanted"`, "", "", "unverified", "field-not-returned"},
		{"empty custom array", "customfield_1", `[]`, `[]`, "", "matches", "native-value"},
		{"custom array order", "customfield_1", `[1,2]`, `[2,1]`, "", "differs", "native-value"},
		{"unknown same object", "customfield_1", `{"future":false,"n":9007199254740993}`, `{"n":9007199254740993,"future":false}`, "", "matches", "native-value"},
		{"unknown object differs", "customfield_1", `{"future":false}`, `{"future":true}`, "", "differs", "native-value"},
		{"expanded priority", "priority", `{"name":"high"}`, `{"id":"1","name":"High"}`, "", "matches", "object-identity"},
		{"account name is not identity", "assignee", `{"name":"bob"}`, `{"name":"bob","accountId":"actual"}`, "", "differs", "native-value"},
		{"different priority", "priority", `{"id":"1"}`, `{"id":"2","name":"High"}`, "", "differs", "native-value"},
		{"option value case", "customfield_1", `{"name":"Value"}`, `{"id":"1","name":"value"}`, "", "differs", "native-value"},
		{"expanded option", "customfield_1", `{"id":"1"}`, `{"id":"1","value":"A","other":false}`, "", "matches", "object-identity"},
		{"expanded multi option", "customfield_1", `[{"id":"1"},{"id":"2"}]`, `[{"id":"2","value":"B"},{"id":"1","value":"A"}]`, "", "matches", "identity-membership"},
		{"missing multi option", "customfield_1", `[{"id":"1"},{"id":"2"}]`, `[{"id":"1","value":"A"}]`, "", "differs", "identity-membership"},
		{"expanded cascading child ID", "customfield_1", `{"id":"1","child":{"id":"2"}}`, `{"id":"1","value":"A","child":{"id":"2","value":"B","self":"x"}}`, "", "matches", "object-identity"},
		{"expanded cascading child value", "customfield_1", `{"value":"A","child":{"value":"B"}}`, `{"id":"1","value":"A","child":{"id":"2","value":"B","self":"x"}}`, "", "matches", "object-identity"},
		{"cascading child value case differs", "customfield_1", `{"id":"1","child":{"value":"B"}}`, `{"id":"1","child":{"id":"2","value":"b"}}`, "", "differs", "native-value"},
		{"cascading child opaque object differs", "customfield_1", `{"id":"1","child":{"future":false}}`, `{"id":"1","child":{"future":false,"extra":true}}`, "", "differs", "native-value"},
		{"cascading child authored extension differs", "customfield_1", `{"id":"1","child":{"id":"2","future":false}}`, `{"id":"1","child":{"id":"2","future":true}}`, "", "differs", "native-value"},
		{"cascading child differs", "customfield_1", `{"id":"1","child":{"id":"2"}}`, `{"id":"1","child":{"id":"3"}}`, "", "differs", "native-value"},
		{"native clear account", "assignee", `{"accountId":null}`, `null`, "", "matches", "account-clear"},
		{"not cleared account", "assignee", `{"accountId":null}`, `{"accountId":"1"}`, "", "differs", "native-value"},
		{"replacement retains old labels", "labels", `["a"]`, `["extra","a"]`, "", "differs", "collection-membership"},
		{"replacement retains old component", "components", `[{"id":"1"}]`, `[{"id":"1","name":"API"},{"id":"2","name":"Old"}]`, "", "differs", "collection-membership"},
		{"replacement retains old option", "customfield_1", `[{"id":"1"}]`, `[{"id":"1","value":"A"},{"id":"2","value":"Old"}]`, "", "differs", "identity-membership"},
		{"set retains old label", "labels", "", `["a","old"]`, `[{"set":["a"]}]`, "differs", "collection-membership"},
		{"set add retains old label", "labels", "", `["a","b","old"]`, `[{"set":["a"]},{"add":"b"}]`, "differs", "update-membership"},
		{"set remove retains old label", "labels", "", `["a","old"]`, `[{"set":["a","b"]},{"remove":"b"}]`, "differs", "update-membership"},
		{"set add expanded components", "components", "", `[{"id":"2","name":"B"},{"id":"1","name":"A"}]`, `[{"set":[{"id":"1"}]},{"add":{"id":"2"}}]`, "matches", "update-membership"},
		{"set add retains old component", "components", "", `[{"id":"1"},{"id":"2"},{"id":"3"}]`, `[{"set":[{"id":"1"}]},{"add":{"id":"2"}}]`, "differs", "update-membership"},
		{"missing label", "labels", `["a","b"]`, `["a"]`, "", "differs", "collection-membership"},
		{"empty labels not cleared", "labels", `[]`, `["old"]`, "", "differs", "collection-clear"},
		{"label case matters", "labels", `["A"]`, `["a"]`, "", "differs", "collection-membership"},
		{"component identity", "components", `[{"name":"Api"}]`, `[{"id":"1","name":"API"}]`, "", "matches", "collection-membership"},
		{"custom ADF normalization", "customfield_1", `{"type":"doc","version":1,"content":[]}`, `{"type":"doc","version":1,"content":[],"attrs":{"normalized":true}}`, "", "unverified", "adf-normalization"},
		{"custom ADF final set normalization", "customfield_1", "", `{"type":"doc","version":1,"content":[],"attrs":{}}`, `[{"set":{"type":"doc","version":1,"content":[]}}]`, "unverified", "adf-normalization"},
		{"custom ADF structural equality", "customfield_1", `{"type":"doc","version":1,"content":[]}`, `{"content":[],"version":1,"type":"doc"}`, "", "matches", "native-value"},
		{"custom object doc without version", "customfield_1", `{"type":"doc","future":false}`, `{"type":"doc","future":true}`, "", "differs", "native-value"},
		{"opaque future document differs", "future_field", `{"type":"doc","version":1,"content":[]}`, `{"type":"doc","version":1,"content":[],"future":true}`, "", "differs", "native-value"},
		{"ADF structural equality", "description", `{"type":"doc","version":1,"content":[]}`, `{"version":1,"content":[],"type":"doc"}`, "", "matches", "native-value"},
		{"ADF normalization", "description", `{"type":"doc","version":1,"content":[]}`, `{"type":"doc","version":1,"content":[{}]}`, "", "unverified", "adf-normalization"},
		{"set value", "summary", "", `"new"`, `[{"set":"new"}]`, "matches", "native-value"},
		{"final set wins", "summary", "", `"new"`, `[{"set":"old"},{"set":"new"}]`, "matches", "native-value"},
		{"unsupported verb", "summary", "", `"new"`, `[{"edit":"new"}]`, "unverified", "unsupported-update-operation"},
		{"unsupported before set", "summary", "", `"new"`, `[{"edit":"old"},{"set":"new"}]`, "unverified", "unsupported-update-operation"},
		{"duplicate operation member", "labels", "", `["a"]`, `[{"add":"b","add":"a"}]`, "unverified", "unsupported-update-operation"},
		{"multi verb operation", "labels", "", `["a"]`, `[{"add":"a","remove":"a"}]`, "unverified", "unsupported-update-operation"},
		{"empty operations", "labels", "", `["a"]`, `[]`, "unverified", "unsupported-update-operation"},
		{"null operation", "labels", "", `["a"]`, `[null]`, "unverified", "unsupported-update-operation"},
		{"add label", "labels", "", `["a","old"]`, `[{"add":"a"}]`, "matches", "update-membership"},
		{"dropped add label", "labels", "", `["old"]`, `[{"add":"a"}]`, "differs", "update-membership"},
		{"remove label", "labels", "", `["old"]`, `[{"remove":"a"}]`, "matches", "update-membership"},
		{"ignored remove label", "labels", "", `["a","old"]`, `[{"remove":"a"}]`, "differs", "update-membership"},
		{"add then remove", "labels", "", `[]`, `[{"add":"a"},{"remove":"a"}]`, "matches", "update-membership"},
		{"remove then add", "labels", "", `["a"]`, `[{"remove":"a"},{"add":"a"}]`, "matches", "update-membership"},
		{"set then add", "labels", "", `["a","b"]`, `[{"set":["a"]},{"add":"b"}]`, "matches", "update-membership"},
		{"set then remove", "labels", "", `[]`, `[{"set":["a"]},{"remove":"a"}]`, "matches", "update-membership"},
		{"set remove clear not empty", "labels", "", `["other"]`, `[{"set":["a"]},{"remove":"a"}]`, "differs", "collection-clear"},
		{"set then remove then add", "labels", "", `["a"]`, `[{"set":["a"]},{"remove":"a"},{"add":"a"}]`, "matches", "update-membership"},
		{"unknown collection update", "customfield_1", "", `["a"]`, `[{"add":"a"}]`, "unverified", "unsupported-update-operation"},
		{"invalid label update", "labels", "", `[]`, `[{"remove":null}]`, "unverified", "unsupported-update-operation"},
		{"ambiguous component aliases", "components", "", `[]`, `[{"add":{"id":"1"}},{"remove":{"name":"API"}}]`, "unverified", "ambiguous-update-identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			request := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{}, Update: map[string]json.RawMessage{}}
			if tc.want != "" {
				request.Fields[tc.field] = json.RawMessage(tc.want)
			}
			if tc.operations != "" {
				request.Update[tc.field] = json.RawMessage(tc.operations)
			}
			var observed jira.IssueFields
			raw := `{}`
			if tc.got != "" {
				raw = fmtVerificationField(tc.field, tc.got)
			}
			if err := json.Unmarshal([]byte(raw), &observed); err != nil {
				t.Fatal(err)
			}
			rows, err := CompareIssueWrite(request, observed)
			if err != nil || len(rows) != 1 || rows[0].Field != tc.field || rows[0].Outcome != tc.outcome || rows[0].Reason != tc.reason || rows[0].Observed.Set != (tc.got != "") || rows[0].Observed.Null != (tc.got == "null") {
				t.Fatalf("verification lost actual native outcome: got=%+v error=%v want=%s/%s", rows, err, tc.outcome, tc.reason)
			}
		})
	}
}

func fmtVerificationField(field, value string) string {
	name, _ := json.Marshal(field)
	return "{" + string(name) + ":" + value + "}"
}

func TestCompareIssueWriteOwnsObservedValuesAndOrdersOnlyRequestedFields(t *testing.T) {
	t.Parallel()
	stored := jira.IssueFields{Extra: map[string]json.RawMessage{
		"customfield_b": json.RawMessage(`{"id":"1","future":"<&>\u2028"}`),
		"customfield_a": json.RawMessage(`false`),
		"unrequested":   json.RawMessage(`"private unrelated value"`),
	}}
	request := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"customfield_b": json.RawMessage(`{"id":"1"}`), "customfield_a": json.RawMessage(`false`)}}
	rows, err := CompareIssueWrite(request, stored)
	if err != nil || len(rows) != 2 || rows[0].Field != "customfield_a" || rows[1].Field != "customfield_b" || rows[1].Outcome != "matches" {
		t.Fatalf("unstable or broadened verification: %+v %v", rows, err)
	}
	before := slices.Clone(stored.Extra["customfield_b"])
	rows[1].Observed.Value[0] = '['
	if !slices.Equal(stored.Extra["customfield_b"], before) {
		t.Fatal("returned readback aliases original native bytes")
	}
	request.Update = map[string]json.RawMessage{"customfield_a": json.RawMessage(`[{"set":false}]`)}
	rows, err = CompareIssueWrite(request, stored)
	if err != nil || rows[0].Outcome != "unverified" || rows[0].Reason != "conflicting-request-containers" {
		t.Fatal("conflicting native containers became a successful proof")
	}
}

func TestCompareNativeIssueWriteOwnsObservedBytesAndRejectsAmbiguity(t *testing.T) {
	t.Parallel()
	observed := map[string]json.RawMessage{"customfield_1": json.RawMessage(`{"id":"1","futureValue":"<&>\u2028"}`)}
	request := jira.IssueWriteRequest{Fields: map[string]json.RawMessage{"customfield_1": json.RawMessage(`{"id":"1"}`)}}
	rows, err := CompareNativeIssueWrite(request, observed)
	if err != nil || len(rows) != 1 || rows[0].Outcome != "matches" {
		t.Fatalf("original native comparison failed: %+v %v", rows, err)
	}
	before := slices.Clone(observed["customfield_1"])
	rows[0].Observed.Value[0] = '['
	if !slices.Equal(observed["customfield_1"], before) {
		t.Fatal("verification outcome aliases original native readback bytes")
	}
	observed["customfield_1"] = json.RawMessage(`{"id":"1","id":"2"}`)
	if rows, err := CompareNativeIssueWrite(request, observed); err == nil || rows != nil {
		t.Fatal("ambiguous original native readback supplied field evidence")
	}
}
