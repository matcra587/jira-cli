package adf

import (
	"bytes"
	_ "embed"
	"fmt"
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
	s, err := compileSchema()
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("adf: validate: %w", err)
	}
	if err := s.Validate(inst); err != nil {
		return fmt.Errorf("adf: document does not conform to the ADF schema: %w", err)
	}
	return nil
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
