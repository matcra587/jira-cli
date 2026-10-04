package adf

import (
	"bytes"
	"cmp"
	_ "embed"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Validation uses a real draft-04 validator rather than a hand-written
// subset: the schema's anyOf/allOf/recursion would turn a stdlib-only
// checker into a bespoke JSON Schema engine, or leave it silently weaker
// than the schema it claims to enforce. Validate authored/edited content
// only — server-originated documents may carry nodes newer than the pin
// and must pass through unvalidated.

//go:embed schema/full.json
var schemaJSON []byte

// compileSchema compiles the embedded schema once; every Validate call
// shares the result. Compilation failure is a build defect (the vendored
// file is broken), surfaced as an error so a bad bump fails loudly at first
// use instead of panicking at import time.
var compileSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return nil, fmt.Errorf("adf: parse vendored schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	// An absolute virtual URI keeps the embedded resource independent of cwd
	// path escaping. All schema references are resolved from this resource.
	const schemaURI = "https://jira-cli.invalid/schema/adf-full.json"
	if err := c.AddResource(schemaURI, doc); err != nil {
		return nil, fmt.Errorf("adf: load vendored schema: %w", err)
	}
	s, err := c.Compile(schemaURI)
	if err != nil {
		return nil, fmt.Errorf("adf: compile vendored schema: %w", err)
	}
	return s, nil
})

// Validate checks a complete ADF document against the vendored schema.
// The input is raw JSON, not a *Document: validation speaks for the exact
// bytes that would be sent to Jira, with no marshaling step in between to
// trust.
func Validate(data []byte) error {
	_, err := ValidateDiagnostics(data)
	return err
}

// ValidationDiagnostic identifies a schema failure using JSON pointers. Empty
// instance_location names the root. Malformed JSON has no schema keyword.
type ValidationDiagnostic struct {
	InstanceLocation string `json:"instance_location"`
	KeywordLocation  string `json:"keyword_location"`
	Message          string `json:"message"`
}

// ValidateDiagnostics uses the same pinned validator as Validate. Diagnostics
// retain input failures; a schema compilation defect returns no input report.
// It never marshals, normalizes or alters the submitted bytes.
func ValidateDiagnostics(data []byte) ([]ValidationDiagnostic, error) {
	s, err := compileSchema()
	if err != nil {
		return nil, err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return []ValidationDiagnostic{{Message: err.Error()}}, fmt.Errorf("adf: validate: %w", err)
	}
	if err := s.Validate(inst); err != nil {
		failure, ok := errors.AsType[*jsonschema.ValidationError](err)
		if !ok {
			return nil, err
		}
		report := failure.DetailedOutput()
		diagnostics := []ValidationDiagnostic{}
		var walk func(jsonschema.OutputUnit)
		walk = func(unit jsonschema.OutputUnit) {
			if unit.Error != nil {
				diagnostics = append(diagnostics, ValidationDiagnostic{InstanceLocation: unit.InstanceLocation, KeywordLocation: unit.KeywordLocation, Message: unit.Error.String()})
			}
			for _, child := range unit.Errors {
				walk(child)
			}
		}
		walk(*report)
		slices.SortFunc(diagnostics, func(a, b ValidationDiagnostic) int {
			return cmp.Or(cmp.Compare(a.InstanceLocation, b.InstanceLocation), cmp.Compare(a.KeywordLocation, b.KeywordLocation), cmp.Compare(a.Message, b.Message))
		})
		return slices.Compact(diagnostics), fmt.Errorf("adf: document does not conform to the ADF schema: %w", err)
	}
	return []ValidationDiagnostic{}, nil
}

// Validate marshals the document and checks it against the vendored
// schema — the convenience form of the package-level Validate for authored
// documents.
func (d *Document) Validate() error {
	data, err := d.MarshalJSON()
	if err != nil {
		return fmt.Errorf("adf: validate: %w", err)
	}
	return Validate(data)
}
