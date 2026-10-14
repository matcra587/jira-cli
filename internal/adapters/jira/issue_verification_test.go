package jira

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestIssueVerificationRejectsIncompleteAmbiguousAndOversizedReadbacks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
	}{
		{"missing fields", `{"key":"KEY-1"}`},
		{"null fields", `{"key":"KEY-1","fields":null}`},
		{"duplicate outer member", `{"fields":{},"fields":{}}`},
		{"duplicate nested member", `{"fields":{"customfield_1":{"id":"1","id":"2"}}}`},
		{"case variant", `{"Fields":{}}`},
		{"field case variant", `{"fields":{"Summary":"x"}}`},
		{"trailing JSON", `{"fields":{}} {}`},
		{"invalid typed field", `{"fields":{"summary":false}}`},
		{"oversized document", `{"fields":{"customfield_1":"` + strings.Repeat("x", int(core.DefaultMaxAPIResponseBytes)) + `"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			issue, fields, err := decodeVerificationIssue(strings.NewReader(tc.body), []string{"summary", "customfield_1"})
			if issue != nil || fields != nil || !errors.Is(err, errIssueVerificationInvalid) {
				t.Fatal("invalid native readback supplied comparison evidence")
			}
		})
	}
}

func TestIssueVerificationKeepsNativeEmptyAndUnknownValues(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{"fields":{}}`, `{"fields":{"customfield_1":9007199254740993,"labels":[],"summary":null}}`} {
		issue, fields, err := decodeVerificationIssue(strings.NewReader(body), []string{"summary", "labels", "customfield_1"})
		if err != nil || issue == nil || fields == nil || !issue.Fields.Set || issue.Fields.Null {
			t.Fatalf("valid native readback was not retained: %v", err)
		}
		if value, ok := issue.Fields.Value.Extra["customfield_1"]; ok && string(value) != "9007199254740993" {
			t.Fatal("native readback rounded an unknown numeric value")
		}
	}
	_, _, err := decodeVerificationIssue(io.MultiReader(strings.NewReader(`{"fields":{}}`), verificationFailingReader{}), nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("readback failure lost its read cause")
	}
}

type verificationFailingReader struct{}

func (verificationFailingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
