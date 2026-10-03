package core

import (
	"strings"
	"testing"
)

func TestValidateJQLQueries(t *testing.T) {
	t.Parallel()
	for _, mode := range []JQLValidationMode{JQLValidationStrict, JQLValidationWarn, JQLValidationNone} {
		if err := ValidateJQLQueries([]string{"project = KEY", " ", "project = KEY"}, mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []JQLValidationMode{"", "STRICT", "syntax"} {
		if ValidateJQLQueries([]string{"project = KEY"}, mode) == nil {
			t.Fatalf("accepted mode %q", mode)
		}
	}
	for _, queries := range [][]string{nil, {}, {""}, {"project = KEY", ""}} {
		if ValidateJQLQueries(queries, JQLValidationStrict) == nil {
			t.Fatalf("accepted queries %q", queries)
		}
	}
	queries := strings.Split(strings.Repeat("project = KEY,", 101), ",")[:101]
	if err := ValidateJQLQueries(queries, JQLValidationStrict); err != nil {
		t.Fatalf("invented query-count limit: %v", err)
	}
}
