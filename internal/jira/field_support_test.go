package jira

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestFieldTypeExamplesCoverFamiliesAndOwnValues(t *testing.T) {
	t.Parallel()
	rows := FieldTypeExamples()
	want := []string{"cascadingselect", "components", "date", "datetime", "fixversions", "group", "labels", "multigroup", "multiselect", "multiuser", "multiversionpicker", "number", "parent", "projectpicker", "select", "string", "textarea", "user", "version", "versionpicker"}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
		if row.NativeShape == "" || row.Notes == "" || !json.Valid(row.Example) {
			t.Fatalf("%s lost its native shape, explanation or valid example", row.Name)
		}
	}
	if !slices.Equal(names, want) {
		t.Fatal("native field discovery lost a field family or deterministic order")
	}
	before, err := MarshalNoEscape(rows)
	if err != nil {
		t.Fatal(err)
	}
	rows[0].Name = "changed"
	rows[0].Example[0] = 'x'
	after, err := MarshalNoEscape(FieldTypeExamples())
	if err != nil || string(before) != string(after) {
		t.Fatal("returned discovery rows expose shared mutable examples")
	}
}
