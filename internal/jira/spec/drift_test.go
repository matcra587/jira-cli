package spec_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	xmaps "github.com/gechr/x/maps"

	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/matcra587/jira-cli/internal/jira/spec"
)

// specDoc is the sliver of OpenAPI syntax the drift check needs: document
// version, path names, and each schema's property names.
type specDoc struct {
	Info struct {
		Version string `json:"version"`
	} `json:"info"`
	Paths      map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas map[string]*specSchema `json:"schemas"`
	} `json:"components"`
}

type specSchema struct {
	Properties map[string]json.RawMessage `json:"properties"`
}

func loadDoc(t *testing.T, file string) *specDoc {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read vendored spec: %v", err)
	}
	var doc specDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	return &doc
}

// schemaDrift diffs the modeled property set against the vendored document,
// naming every mismatch. Both directions matter: a spec addition means the
// wire types are stale, a spec removal means the model declares a field the
// API no longer documents.
func schemaDrift(doc *specDoc, modeled map[string][]string) []string {
	var drift []string
	for name, props := range xmaps.Sorted(modeled) {
		schema, ok := doc.Components.Schemas[name]
		if !ok {
			drift = append(drift, fmt.Sprintf("%s: modeled schema missing from spec", name))
			continue
		}
		for _, prop := range slices.Sorted(maps.Keys(schema.Properties)) {
			if !slices.Contains(props, prop) {
				drift = append(drift, fmt.Sprintf("%s.%s: in spec, not modeled — regenerate wire types", name, prop))
			}
		}
		for _, prop := range props {
			if _, ok := schema.Properties[prop]; !ok {
				drift = append(drift, fmt.Sprintf("%s.%s: modeled, absent from spec", name, prop))
			}
		}
	}
	return drift
}

// TestVendoredDocumentsMatchPins fails when a committed spec file no longer
// matches its recorded digest or version — an accidental edit, or a bump
// that forgot to update the pin in spec.go.
func TestVendoredDocumentsMatchPins(t *testing.T) {
	for _, pin := range spec.Pins() {
		t.Run(strings.TrimSuffix(pin.File, ".json"), func(t *testing.T) {
			raw, err := os.ReadFile(pin.File)
			if err != nil {
				t.Fatalf("read vendored spec: %v", err)
			}
			sum := sha256.Sum256(raw)
			if got := hex.EncodeToString(sum[:]); got != pin.SHA256 {
				t.Errorf("digest = %s, pin records %s; update the Pin in spec.go with the new provenance", got, pin.SHA256)
			}
			doc := loadDoc(t, pin.File)
			if doc.Info.Version != pin.Version {
				t.Errorf("info.version = %q, pin records %q", doc.Info.Version, pin.Version)
			}
		})
	}
}

// TestWireModelMatchesPlatformSpec is the CI drift detector: every property
// the vendored platform document declares on a generated schema must be
// modeled, and vice versa. It goes red on a spec bump until the wire types
// are regenerated and the diff is reviewed.
func TestWireModelMatchesPlatformSpec(t *testing.T) {
	doc := loadDoc(t, spec.Platform.File)
	for _, line := range schemaDrift(doc, jira.WireSchemaProperties()) {
		t.Error(line)
	}
}

// TestPlantedSpecAdditionFailsByName proves the detector's failure mode is
// actionable: a property planted into a modeled schema must be reported by
// schema and property name, not as a generic mismatch.
func TestPlantedSpecAdditionFailsByName(t *testing.T) {
	doc := loadDoc(t, spec.Platform.File)
	doc.Components.Schemas["StatusDetails"].Properties["driftCanary"] = json.RawMessage(`{"type":"string"}`)

	drift := schemaDrift(doc, jira.WireSchemaProperties())
	for _, line := range drift {
		if strings.Contains(line, "StatusDetails.driftCanary") {
			return
		}
	}
	t.Fatalf("planted StatusDetails.driftCanary not named; drift = %q", drift)
}

// TestAgileDocumentCoversAgileSurface keeps the agile pin honest while no
// agile wire types exist yet: the vendored document must actually be the
// /rest/agile/1.0 surface, so the later sprint/board work starts from the
// right file. The schema drift harness above picks agile schemas up the
// moment a generator models them.
func TestAgileDocumentCoversAgileSurface(t *testing.T) {
	doc := loadDoc(t, spec.Agile.File)
	for path := range doc.Paths {
		if strings.HasPrefix(path, "/rest/agile/1.0/") {
			return
		}
	}
	t.Fatal("no /rest/agile/1.0/ paths in agile.json — wrong document vendored?")
}
