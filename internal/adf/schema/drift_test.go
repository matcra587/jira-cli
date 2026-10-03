// Package schema_test is the drift check for the vendored ADF schema: the
// generated catalog in internal/adf must account for exactly the
// definitions full.json carries, so a schema bump (or hand edit) that adds
// or removes one fails CI naming it.
package schema_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

func vendoredDefinitions(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("full.json")
	if err != nil {
		t.Fatalf("read full.json: %v", err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != adf.SchemaSHA256 {
		t.Errorf("full.json digest %s does not match the generated catalog's %s — run `go generate ./internal/adf`", got, adf.SchemaSHA256)
	}
	var schema struct {
		Definitions map[string]json.RawMessage `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse full.json: %v", err)
	}
	return slices.Sorted(maps.Keys(schema.Definitions))
}

// diff returns the definitions in schema but not the catalog, and vice
// versa. Shared by the real check and its planted-addition self-test.
func diff(schemaDefs, catalogDefs []string) (added, removed []string) {
	for _, name := range schemaDefs {
		if !slices.Contains(catalogDefs, name) {
			added = append(added, name)
		}
	}
	for _, name := range catalogDefs {
		if !slices.Contains(schemaDefs, name) {
			removed = append(removed, name)
		}
	}
	return added, removed
}

func TestSchemaMatchesGeneratedCatalog(t *testing.T) {
	added, removed := diff(vendoredDefinitions(t), adf.SchemaDefinitions)
	for _, name := range added {
		t.Errorf("schema definition %s is not in the generated catalog — run `go generate ./internal/adf`", name)
	}
	for _, name := range removed {
		t.Errorf("generated catalog names %s, which the schema no longer defines — run `go generate ./internal/adf`", name)
	}
}

// TestPlantedAdditionIsNamed proves the drift check actually fires: a
// definition planted into the schema set must come back by name.
func TestPlantedAdditionIsNamed(t *testing.T) {
	planted := append(vendoredDefinitions(t), "shinyNewThing_node")
	slices.Sort(planted)
	added, removed := diff(planted, adf.SchemaDefinitions)
	if len(removed) != 0 {
		t.Errorf("unexpected removals: %v", removed)
	}
	if !slices.Equal(added, []string{"shinyNewThing_node"}) {
		t.Errorf("planted addition not named: added = %v", added)
	}
}

// TestReadmePinMatchesSchema fences the hand-maintained half of the pin:
// the digest recorded in README.md must be the digest of the vendored file
// (the bump procedure updates it by hand — this is what catches a forgotten
// step 3), and the clover-managed Version and Source URL lines must agree
// on the version.
func TestReadmePinMatchesSchema(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}

	shaRe := regexp.MustCompile("SHA-256 of `full.json`: `([0-9a-f]{64})`")
	m := shaRe.FindSubmatch(readme)
	if m == nil {
		t.Fatal("README.md no longer records the full.json SHA-256")
	}
	if got := string(m[1]); got != adf.SchemaSHA256 {
		t.Errorf("README records digest %s, vendored schema is %s — update the README pin", got, adf.SchemaSHA256)
	}

	verRe := regexp.MustCompile(`Version: \*\*([0-9][^*]*)\*\*`)
	v := verRe.FindSubmatch(readme)
	if v == nil {
		t.Fatal("README.md no longer records the package version")
	}
	if !bytes.Contains(readme, []byte("adf-schema-"+string(v[1])+".tgz")) {
		t.Errorf("README source URL does not match recorded version %s", v[1])
	}
}
