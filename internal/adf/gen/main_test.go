package main

import (
	"bytes"
	"go/format"
	"os"
	"testing"
)

// TestGeneratedFileIsFresh regenerates from the vendored schema and
// compares against the committed nodes_gen.go, so a schema or generator
// change without a regen fails here rather than shipping stale views.
func TestGeneratedFileIsFresh(t *testing.T) {
	raw, err := os.ReadFile("../" + schemaPath)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	cat, err := buildCatalog(raw)
	if err != nil {
		t.Fatalf("buildCatalog: %v", err)
	}
	want, err := format.Source(render(cat))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	got, err := os.ReadFile("../" + outPath)
	if err != nil {
		t.Fatalf("read committed output: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is stale — run `go generate ./internal/adf`", outPath)
	}
}

func TestExportedName(t *testing.T) {
	tests := map[string]string{
		"localId":       "LocalID",
		"panelIconId":   "PanelIconID",
		"url":           "URL",
		"shortName":     "ShortName",
		"width":         "Width",
		"__fileName":    "FileName",
		"occurrenceKey": "OccurrenceKey",
	}
	for in, want := range tests {
		if got := exportedName(in); got != want {
			t.Errorf("exportedName(%q) = %q, want %q", in, got, want)
		}
	}
}
