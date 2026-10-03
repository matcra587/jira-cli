package adf_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// This file pins types.go surfaces the coverage pass found untouched:
// Text(), SetMarks(), NewMark(), and writeCanonical's zero-value path.
// Two of the cases below pin real oddities — flagged inline, not fixed.

func TestNodeTextGetter(t *testing.T) {
	if got := parseNode(t, `{"type":"text","text":"hi"}`).Text(); got != "hi" {
		t.Errorf("Text() = %q, want hi", got)
	}
	if got := parseNode(t, `{"type":"paragraph","content":[]}`).Text(); got != "" {
		t.Errorf("Text() on a node with no text field = %q, want empty", got)
	}
}

// TestSetMarksSurvivesMarshal pins SetMarks's basic contract: assigned
// marks are re-emitted on marshal.
func TestSetMarksSurvivesMarshal(t *testing.T) {
	n := adf.NewNode("text")
	n.SetMarks([]adf.Mark{*adf.NewMark("strong")})
	out, err := n.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	want := `{"type":"text","marks":[{"type":"strong"}]}`
	if string(out) != want {
		t.Errorf("got %s, want %s", out, want)
	}
}

// TestSetMarksNilEmitsEmptyArrayNotNull pins current behavior — possibly a
// bug, see report: the doc comment on SetMarks says "a non-nil empty slice
// marshals as marks: [] — distinct from removing the key", implying a nil
// slice is different again. It is not: SetMarks(nil) marshals identically
// to SetMarks([]Mark{}), both "marks":[]. Only a *parsed* literal null
// preserves as null (see TestNullMarksAndContentRoundTrip in types_test.go);
// SetMarks can never reproduce that state once called, nil argument or not.
func TestSetMarksNilEmitsEmptyArrayNotNull(t *testing.T) {
	nilCase := adf.NewNode("paragraph")
	nilCase.SetMarks(nil)
	nilOut, err := nilCase.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if want := `{"type":"paragraph","marks":[]}`; string(nilOut) != want {
		t.Errorf("SetMarks(nil): got %s, want %s (pins current behavior — possibly a bug, see report)", nilOut, want)
	}

	emptyCase := adf.NewNode("paragraph")
	emptyCase.SetMarks([]adf.Mark{})
	emptyOut, err := emptyCase.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(emptyOut) != string(nilOut) {
		t.Errorf("SetMarks(nil) and SetMarks([]Mark{}) diverge: %s vs %s", nilOut, emptyOut)
	}
}

// TestNewMarkMarshal pins NewMark's canonical output and that a
// programmatically-built mark's SetAttrs is reflected.
func TestNewMarkMarshal(t *testing.T) {
	m := adf.NewMark("link")
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `{"type":"link"}`; string(out) != want {
		t.Errorf("got %s, want %s", out, want)
	}

	if err := m.SetAttrs(map[string]string{"href": "https://example.com"}); err != nil {
		t.Fatalf("SetAttrs: %v", err)
	}
	out, err = json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `{"type":"link","attrs":{"href":"https://example.com"}}`; string(out) != want {
		t.Errorf("got %s, want %s", out, want)
	}
}

// TestZeroValueNodeMarshalsEmptyType pins writeCanonical's zero-value path.
// A Node's fields are unexported, so `var n adf.Node` from outside the
// package is the only way to reach a genuinely nil-fields node without
// NewNode or UnmarshalJSON — MarshalJSON then falls into writeCanonical,
// which unconditionally writes "type": even when Type() is "". Pins current
// behavior — possibly a bug, see report: this is a node with no type at
// all, marshaled as if it had one.
func TestZeroValueNodeMarshalsEmptyType(t *testing.T) {
	var n adf.Node
	out, err := n.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if want := `{"type":""}`; string(out) != want {
		t.Errorf("got %s, want %s", out, want)
	}
}

// TestZeroValueNodeKeepsTypeKey pins the rescue: a Node built outside
// NewNode used to lose its "type" key entirely the moment a setter ran,
// because the first setField switched marshaling to the field-iteration
// path with no identity written.
func TestZeroValueNodeKeepsTypeKey(t *testing.T) {
	t.Parallel()
	var n adf.Node
	n.SetText("hi")
	out, err := n.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"type"`) {
		t.Errorf("zero-value node marshaled without a type key: %s", out)
	}
}
