package adf

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/matcra587/jira-cli/internal/errtax"
)

// Warning is a structured non-fatal diagnostic emitted by best-effort ADF
// processing. Strict mode promotes warnings to errors. The shape matches
// cli.Warning byte-for-byte but the type lives here to avoid an import
// cycle; commands convert via cli.WarningFrom.
type Warning struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	Field    string `json:"field,omitempty"`
	Path     string `json:"path,omitempty"`
	NodeType string `json:"node_type,omitempty"`
	MarkType string `json:"mark_type,omitempty"`
	Lossy    bool   `json:"lossy"`
}

// LossyConversionError is the strict-mode abort for a lossy Markdown→ADF
// conversion: it carries the source-mapped warning so the error mapper can
// surface the offending Markdown line and a remediation hint instead of a
// bare message with none.
type LossyConversionError struct {
	Warning Warning
}

func (e LossyConversionError) Error() string { return e.Warning.Message }

// Code classifies the strict-mode abort under markdown_lossy_conversion.
func (e LossyConversionError) Code() errtax.Code { return errtax.CodeMarkdownLossyConversion }

var _ errtax.Coded = LossyConversionError{}

// Implement cli.WarningSource so commands can do cli.WarningFrom(adfW)
// without either package importing the other's concrete type.
func (w Warning) WarningType() string     { return w.Type }
func (w Warning) WarningMessage() string  { return w.Message }
func (w Warning) WarningField() string    { return w.Field }
func (w Warning) WarningPath() string     { return w.Path }
func (w Warning) WarningNodeType() string { return w.NodeType }
func (w Warning) WarningMarkType() string { return w.MarkType }
func (w Warning) WarningIsLossy() bool    { return w.Lossy }

// InvalidDocumentError reports a value that is not an ADF document object —
// most commonly a plain string where {"type":"doc",...} is required. It is
// TYPED so the error mapper can emit a stable adf_invalid code with a clean
// message instead of leaking the raw json unmarshal error to the envelope.
type InvalidDocumentError struct {
	// Got names the JSON shape that was supplied ("string", "number", ...).
	Got string
	// Field optionally names the payload key that carried the bad value;
	// callers that know the key tag it after Parse returns.
	Field string
}

func (e *InvalidDocumentError) Error() string {
	return "value is not an ADF document: got a JSON " + e.Got + ", want an object"
}

// Code classifies the failure under the taxonomy's adf_invalid code.
func (e *InvalidDocumentError) Code() errtax.Code { return errtax.CodeADFInvalid }

var _ errtax.Coded = (*InvalidDocumentError)(nil) //nolint:errcheck // compile-time interface assertion

// Parse decodes ADF JSON into a typed Document and returns any best-effort
// warnings collected during the decode. Unknown nodes/marks are preserved
// opaquely and surface no warnings on parse — they only matter at
// render/submit time. A value of the wrong JSON shape (a string where the
// document object belongs) returns *InvalidDocumentError so the failure
// carries a stable identity instead of the raw json error text.
func Parse(data []byte) (Document, []Warning, error) {
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
			return Document{}, nil, &InvalidDocumentError{Got: typeErr.Value, Field: typeErr.Field}
		}
		return Document{}, nil, fmt.Errorf("adf parse: %w", err)
	}
	return doc, nil, nil
}

// Marshal serializes a Document back to JSON, preserving opaque subtrees.
func Marshal(doc Document) ([]byte, error) {
	return json.Marshal(doc)
}
