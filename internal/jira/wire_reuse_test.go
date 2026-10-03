package jira

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestSharedWireDecoderReplacesFactsAndPreservesFailedReceiver(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, field string
		model       interface {
			MarshalJSON() ([]byte, error)
			UnmarshalJSON([]byte) error
		}
	}{
		{"generated user", "displayName", &UserDetails{}},
		{"curated fields", "summary", &IssueFields{}},
		{"curated comment", "created", &Comment{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, body := range []string{
				fmt.Sprintf(`{%q:null,"old_extra":false}`, tc.field),
				fmt.Sprintf(`{%q:"new","new_extra":9007199254740993}`, tc.field),
			} {
				if err := tc.model.UnmarshalJSON([]byte(body)); err != nil {
					t.Fatal(err)
				}
			}
			before, err := tc.model.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			var facts map[string]json.RawMessage
			if err := json.Unmarshal(before, &facts); err != nil {
				t.Fatal(err)
			}
			if len(facts) != 2 || string(facts[tc.field]) != `"new"` || string(facts["new_extra"]) != "9007199254740993" {
				t.Fatalf("old facts survived: %s", before)
			}
			if err := tc.model.UnmarshalJSON([]byte(fmt.Sprintf(`{%q:17,"new_extra":false}`, tc.field))); err == nil {
				t.Fatal("wrong field type accepted")
			}
			after, err := tc.model.MarshalJSON()
			if err != nil || string(before) != string(after) {
				t.Fatalf("failed decode replaced facts: %s %s %v", before, after, err)
			}
			if err := tc.model.UnmarshalJSON([]byte(`{"only_extra":null}`)); err != nil {
				t.Fatal(err)
			}
			cleared, err := tc.model.MarshalJSON()
			if err != nil || string(cleared) != `{"only_extra":null}` {
				t.Fatalf("absent fields were retained: %s %v", cleared, err)
			}
		})
	}
}
