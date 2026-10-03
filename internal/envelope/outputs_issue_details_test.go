package envelope_test

import (
	"encoding/json"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

func TestIssueViewDetailsPreserveNativeStates(t *testing.T) {
	for _, input := range []string{
		`{"key":"ABC-1","fields":{"summary":"sample"}}`,
		`{"key":"ABC-1","fields":{},"schema":null,"operations":null,"transitions":[],"futureCamelMeta":{"large":9007199254740993,"text":"\u2028\u003c\/","empty":[],"false":false}}`,
	} {
		var bean jira.IssueBean
		if err := json.Unmarshal([]byte(input), &bean); err != nil {
			t.Fatal(err)
		}
		before, err := jira.MarshalNoEscape(bean)
		if err != nil {
			t.Fatal(err)
		}
		view := envelope.IssueViewFromBean(bean)
		encoded, err := jira.MarshalNoEscape(view)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &document); err != nil {
			t.Fatal(err)
		}
		var native map[string]json.RawMessage
		if err := json.Unmarshal([]byte(input), &native); err != nil {
			t.Fatal(err)
		}
		delete(native, "key")
		delete(native, "fields")
		if len(native) == 0 {
			if _, found := document["details"]; found {
				t.Fatalf("absent metadata was invented: %s", encoded)
			}
		} else {
			var details map[string]json.RawMessage
			if json.Unmarshal(document["details"], &details) != nil || len(details) != len(native) {
				t.Fatalf("native metadata members changed: %s", encoded)
			}
			for name, expected := range native {
				if string(details[name]) != string(expected) {
					t.Errorf("native %s changed: got=%s want=%s", name, details[name], expected)
				}
			}
		}
		after, err := jira.MarshalNoEscape(bean)
		if err != nil || string(after) != string(before) {
			t.Fatal("projection changed its source observation")
		}
	}
}
