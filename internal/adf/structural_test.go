package adf_test

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

func TestStructuralEqualDefinesFidelity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		left, right string
		equal       bool
	}{
		{name: "object order whitespace escapes", left: `{"a":"<\u2028","b":9007199254740993}`, right: "{ \"b\":9007199254740993, \"a\":\"\\u003c\u2028\" }", equal: true},
		{name: "large integer distinction", left: `9007199254740992`, right: `9007199254740993`},
		{name: "number spelling", left: `1`, right: `1.0`},
		{name: "array order", left: `[1,2]`, right: `[2,1]`},
		{name: "absent null", left: `{}`, right: `{"a":null}`},
		{name: "empty array null", left: `[]`, right: `null`},
		{name: "nested objects", left: `[{"b":{},"a":[true,false,null]}]`, right: `[{"a":[true,false,null],"b":{}}]`, equal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			equal, err := adf.StructuralEqual([]byte(tc.left), []byte(tc.right))
			if err != nil || equal != tc.equal {
				t.Fatalf("want equal=%v, got %v, %v", tc.equal, equal, err)
			}
		})
	}
}

func TestStructuralEqualRejectsAmbiguousJSON(t *testing.T) {
	for _, source := range []string{`{"a":1,"a":2}`, `{"nested":{"a":1,"a":1}}`, `{} {}`, `[]junk`, `{"a":`, ``, `[1,]`} {
		for _, reverse := range []bool{false, true} {
			left, right := []byte(source), []byte(`{}`)
			if reverse {
				left, right = right, left
			}
			if equal, err := adf.StructuralEqual(left, right); err == nil || equal {
				t.Fatalf("ambiguous input accepted: %q, reverse=%v", source, reverse)
			}
		}
	}
}

func TestFromMarkdownReferenceOnlyIsMetadata(t *testing.T) {
	doc, warnings, err := adf.FromMarkdownLossy("[docs]: https://example.test/guide\n")
	if err != nil || len(warnings) != 0 || len(doc.Content()) != 0 {
		t.Fatalf("reference-only metadata became visible text: %v, %+v, %s", err, warnings, editBytes(t, doc))
	}
}
