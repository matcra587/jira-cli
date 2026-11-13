package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogGeneratedFileIsCurrent(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "..", "internal", "jira")
	want, err := generate(filepath.Join(dir, "spec"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "api_catalog_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("api_catalog_gen.go is stale; run go generate ./internal/jira")
	}
}

func TestProjectKeepsDocumentedMethodsAndPublicFamilies(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"paths":{
		"/rest/api/3/example":{"parameters":[],"get":{"operationId":"readExample","summary":"Read example"},"post":{"operationId":"createExample","deprecated":true}},
		"/rest/agile/1.0/example":{"get":{"operationId":"readAgile"}},
		"/rest/devinfo/0.10/example":{"get":{"operationId":"outsideRawGrammar"}}
	}}`)
	got, err := project(raw, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("operations = %d, want 3", len(got))
	}
	seen := map[string]entry{}
	for _, value := range got {
		seen[value.OperationID] = value
		if value.family != "fixture" || value.method == "PARAMETERS" {
			t.Fatal("lost provenance or interpreted a path property as a method", value)
		}
	}
	if seen["readExample"].Summary != "Read example" || seen["readExample"].method != "GET" || !seen["createExample"].Deprecated {
		t.Fatal("lost method, summary or deprecation")
	}
	if _, exists := seen["outsideRawGrammar"]; exists {
		t.Fatal("offered an unsupported REST family")
	}
}

func TestGenerateRefusesChangedPins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, file := range []string{"platform.json", "agile.json"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(`{"paths":{"/rest/api/3/example":{"get":{"operationId":"fixture"}}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := generate(dir); err == nil {
		t.Fatal("generated trusted provenance for changed spec bytes")
	}
}
