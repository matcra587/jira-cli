package adf_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

func TestValidateStatusColors(t *testing.T) {
	tests := []struct {
		name    string
		color   string
		wantErr bool
	}{
		{name: "neutral", color: `"neutral"`},
		{name: "purple", color: `"purple"`},
		{name: "blue", color: `"blue"`},
		{name: "red", color: `"red"`},
		{name: "yellow", color: `"yellow"`},
		{name: "green", color: `"green"`},
		{name: "hex lowercase", color: `"#a1b2c3"`},
		{name: "hex uppercase", color: `"#A1B2C3"`},
		{name: "hex mixed case", color: `"#a1B2c3"`},
		{name: "hex black", color: `"#000000"`},
		{name: "hex white", color: `"#ffffff"`},
		{name: "unknown name", color: `"orange"`, wantErr: true},
		{name: "uppercase name", color: `"RED"`, wantErr: true},
		{name: "hex shorthand", color: `"#abc"`, wantErr: true},
		{name: "hex alpha", color: `"#12345678"`, wantErr: true},
		{name: "hex non-digit", color: `"#12345g"`, wantErr: true},
		{name: "hex missing hash", color: `"123456"`, wantErr: true},
		{name: "hex leading space", color: `" #123456"`, wantErr: true},
		{name: "hex trailing space", color: `"#123456 "`, wantErr: true},
		{name: "hex trailing newline", color: `"#123456\n"`, wantErr: true},
		{name: "empty", color: `""`, wantErr: true},
		{name: "null", color: `null`, wantErr: true},
		{name: "number", color: `123456`, wantErr: true},
		{name: "boolean", color: `true`, wantErr: true},
		{name: "object", color: `{}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(fmt.Sprintf(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"status","attrs":{"text":"READY","color":%s}}]}]}`, tt.color))
			if err := adf.Validate(data); (err != nil) != tt.wantErr {
				t.Fatalf("Validate status color %s: error present %t, want %t", tt.color, err != nil, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			doc, err := adf.ParseDocument(data)
			if err != nil {
				t.Fatal(err)
			}
			status, err := adf.AsStatus(&doc.Content()[0].Content()[0])
			if err != nil || fmt.Sprintf("%q", status.Attrs.Color) != tt.color {
				t.Fatalf("typed status color changed: %v; color %s", err, tt.color)
			}
			encoded, err := doc.Marshal()
			if err != nil || !bytes.Equal(encoded, data) {
				t.Fatalf("validated status document did not preserve its bytes: %v", err)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{
			name: "minimal document",
			in:   `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hi"}]}]}`,
		},
		{
			name: "panel with status and code block",
			in: `{"version":1,"type":"doc","content":[` +
				`{"type":"panel","attrs":{"panelType":"warning"},"content":[` +
				`{"type":"paragraph","content":[{"type":"status","attrs":{"text":"BLOCKED","color":"red"}}]}]},` +
				`{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"x := 1"}]}]}`,
		},
		{
			name:    "unknown node type",
			in:      `{"version":1,"type":"doc","content":[{"type":"futureWidget"}]}`,
			wantErr: true,
		},
		{
			name:    "panel without required panelType",
			in:      `{"version":1,"type":"doc","content":[{"type":"panel","attrs":{},"content":[{"type":"paragraph","content":[]}]}]}`,
			wantErr: true,
		},
		{
			name:    "empty text violates minLength",
			in:      `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":""}]}]}`,
			wantErr: true,
		},
		{
			name:    "wrong attribute type: heading level is a string, schema wants a number",
			in:      `{"version":1,"type":"doc","content":[{"type":"heading","attrs":{"level":"one"},"content":[]}]}`,
			wantErr: true,
		},
		{
			name:    "wrong attribute type: document version is a string, schema wants the number 1",
			in:      `{"version":"1","type":"doc","content":[]}`,
			wantErr: true,
		},
		{
			name:    "missing required top-level content key",
			in:      `{"version":1,"type":"doc"}`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := adf.Validate([]byte(tt.in))
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateRejectsMalformedJSON pins the error class Validate returns
// for input that isn't even parseable JSON, distinct from "parses fine but
// fails the schema": no jsonschema validation error wrapper, just the raw
// decode failure under the "adf: validate:" prefix.
func TestValidateRejectsMalformedJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"not JSON at all", `not json at all`},
		{"empty input", ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := adf.Validate([]byte(tt.in))
			if err == nil {
				t.Fatal("Validate() = nil, want error")
			}
			const wantPrefix = "adf: validate: "
			if got := err.Error(); len(got) < len(wantPrefix) || got[:len(wantPrefix)] != wantPrefix {
				t.Errorf("Validate() error = %q, want prefix %q", got, wantPrefix)
			}
		})
	}
}

func TestDocumentValidate(t *testing.T) {
	doc, err := adf.ParseDocument([]byte(`{"version":1,"type":"doc","content":[{"type":"rule"}]}`))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if err := doc.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}
