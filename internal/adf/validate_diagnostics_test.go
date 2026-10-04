package adf_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

func TestValidateDiagnosticsPreservesBoundaryAndLocations(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, input, path string
		valid             bool
	}{
		{name: "valid", input: `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"native"}]}]}`, valid: true},
		{name: "wrong context", input: `{"type":"doc","version":1,"content":[{"type":"text","text":"root text"}]}`, path: "/content/0"},
		{name: "attribute enum", input: `{"type":"doc","version":1,"content":[{"type":"heading","attrs":{"level":7},"content":[]}]}`, path: "/content/0/attrs/level"},
		{name: "future node", input: `{"type":"doc","version":1,"content":[{"type":"futureWidget"}]}`, path: "/content/0/type"},
		{name: "trailing JSON", input: `{"type":"doc","version":1,"content":[]} {}`},
		{name: "malformed", input: `{"type":"doc",`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			original := []byte(tc.input)
			before := bytes.Clone(original)
			report, err := adf.ValidateDiagnostics(original)
			if (err == nil) != tc.valid || report == nil || tc.valid && len(report) != 0 || !tc.valid && len(report) == 0 || !bytes.Equal(original, before) {
				t.Fatalf("wrong validity, diagnostics or source preservation: %v", err)
			}
			found := tc.path == ""
			for _, diagnostic := range report {
				if diagnostic.Message == "" {
					t.Fatal("empty diagnostic")
				}
				if strings.HasPrefix(diagnostic.InstanceLocation, tc.path) && diagnostic.KeywordLocation != "" {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing exact instance/schema location %s", tc.path)
			}
			again, _ := adf.ValidateDiagnostics(original)
			if !reflect.DeepEqual(report, again) {
				t.Fatal("diagnostic ordering changed across calls")
			}
			raw, err := json.Marshal(report)
			if err != nil || len(raw) == 0 {
				t.Fatal("diagnostics cannot serialize")
			}
		})
	}
}
