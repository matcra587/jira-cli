package adf_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// preservationFixture is deliberately hostile: an unknown node type with an
// unknown sibling key, key order that no Go struct would produce (version
// first, attrs before type), an integer above float64's exact range, a
// trailing-zero decimal, pre-escaped text, and explicit empty marks/content
// arrays. Every one of those is a byte-for-byte trap for a naive
// map-and-remarshal model.
const preservationFixture = `{"version":1,"type":"doc","content":[` +
	`{"type":"paragraph","marks":[],"content":[` +
	`{"type":"text","text":"a < b & c"}]},` +
	`{"attrs":{"bigId":9007199254740993,"ratio":1.50,"localId":"x"},"type":"futureWidget",` +
	`"payload":{"nested":[1,2,{"deep":true}]},"content":[{"type":"text","text":"inner"}]},` +
	`{"type":"panel","attrs":{"panelType":"info"},"content":[` +
	`{"type":"paragraph","content":[]}]}]}`

func mustParse(t *testing.T, data string) *adf.Document {
	t.Helper()
	doc, err := adf.ParseDocument([]byte(data))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	return doc
}

func mustMarshal(t *testing.T, doc *adf.Document) []byte {
	t.Helper()
	out, err := doc.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return out
}

func TestRoundTripPreservesBytes(t *testing.T) {
	doc := mustParse(t, preservationFixture)
	out := mustMarshal(t, doc)
	if string(out) != preservationFixture {
		t.Errorf("round-trip changed bytes\n in: %s\nout: %s", preservationFixture, out)
	}
}

func TestRoundTripPreservesBytesAfterCompaction(t *testing.T) {
	// Whitespaced input round-trips to its compact form: the contract is
	// byte-equivalence for compact input, structural equivalence otherwise.
	spaced := `{ "version": 1, "type": "doc", "content": [ { "type": "rule" } ] }`
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(spaced)); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	out := mustMarshal(t, mustParse(t, spaced))
	if !bytes.Equal(out, compact.Bytes()) {
		t.Errorf("round-trip of spaced input\nwant: %s\n got: %s", compact.Bytes(), out)
	}
}

func TestEditReflectsWhileSiblingsPreserve(t *testing.T) {
	doc := mustParse(t, preservationFixture)
	doc.Content()[0].Content()[0].SetText("edited")
	out := string(mustMarshal(t, doc))

	if !strings.Contains(out, `"text":"edited"`) {
		t.Errorf("edit not reflected in output: %s", out)
	}
	// The untouched unknown sibling keeps its exact bytes — including the
	// values a float64 round-trip would corrupt.
	for _, verbatim := range []string{
		`"bigId":9007199254740993`,
		`"ratio":1.50`,
		`"payload":{"nested":[1,2,{"deep":true}]}`,
		`"attrs":{"bigId":9007199254740993,"ratio":1.50,"localId":"x"},"type":"futureWidget"`,
	} {
		if !strings.Contains(out, verbatim) {
			t.Errorf("sibling bytes not preserved: missing %s in %s", verbatim, out)
		}
	}
}

func TestBuiltNodesMarshalCanonically(t *testing.T) {
	para := adf.NewNode("paragraph")
	text := adf.NewNode("text")
	text.SetText("hi <there>")
	para.SetContent([]adf.Node{*text})

	out, err := para.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	want := `{"type":"paragraph","content":[{"type":"text","text":"hi <there>"}]}`
	if string(out) != want {
		t.Errorf("built node\nwant: %s\n got: %s", want, out)
	}
}

func TestSetAttrsSurvivesRoundTrip(t *testing.T) {
	n := adf.NewNode("panel")
	if err := n.SetAttrs(map[string]string{"panelType": "warning"}); err != nil {
		t.Fatalf("SetAttrs: %v", err)
	}
	out, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"type":"panel","attrs":{"panelType":"warning"}}`
	if string(out) != want {
		t.Errorf("want %s, got %s", want, out)
	}
}

func TestFieldReadsUnknownKeys(t *testing.T) {
	doc := mustParse(t, preservationFixture)
	widget := doc.Content()[1]
	raw, ok := widget.Field("payload")
	if !ok {
		t.Fatalf("Field(payload) not found")
	}
	if string(raw) != `{"nested":[1,2,{"deep":true}]}` {
		t.Errorf("payload bytes changed: %s", raw)
	}
	if _, ok := widget.Field("absent"); ok {
		t.Errorf("Field(absent) unexpectedly present")
	}
	if v, err := doc.Version(); err != nil || v != 1 {
		t.Errorf("Version() = %d, %v; want 1, nil", v, err)
	}
}

func TestParseDocumentRejectsNonDocuments(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"not an object", `[1,2]`},
		{"wrong type", `{"type":"paragraph","version":1}`},
		{"missing version", `{"type":"doc","content":[]}`},
		{"malformed", `{"type":"doc","version":1,`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := adf.ParseDocument([]byte(tt.in)); err == nil {
				t.Errorf("ParseDocument(%s) succeeded, want error", tt.in)
			}
		})
	}
}

func TestAuthoredTextKeepsLineSeparatorsRaw(t *testing.T) {
	// U+2028/U+2029 are valid raw in JSON strings, and Jira emits them raw.
	// stdlib's encoder escapes them unconditionally even with HTML escaping
	// off — this pins writeJSONString's passthrough so nobody "simplifies"
	// it back onto encoding/json and silently breaks byte preservation.
	doc := mustParse(t, preservationFixture)
	doc.Content()[0].Content()[0].SetText("a\u2028b\u2029c")
	out := string(mustMarshal(t, doc))

	if !strings.Contains(out, "\"a\u2028b\u2029c\"") {
		t.Errorf("line separators escaped or lost: %s", out)
	}
}

func TestBareObjectNodesRoundTrip(t *testing.T) {
	// A parsed empty object must not grow an invented canonical "type" key;
	// same for marks. Malformed ADF, but the substrate's contract is
	// verbatim carriage, not validation.
	const in = `{"type":"doc","version":1,"content":[{"marks":[{}]},{}]}`
	doc := mustParse(t, in)
	if got := string(mustMarshal(t, doc)); got != in {
		t.Errorf("bare-object round-trip changed bytes\n in: %s\nout: %s", in, got)
	}
}

func TestNullMarksAndContentRoundTrip(t *testing.T) {
	// A literal null decodes to a nil slice; re-marshal must re-emit the
	// stored null, never promote it to [].
	const in = `{"type":"doc","version":1,"content":[{"type":"paragraph","marks":null,"content":null}]}`
	doc := mustParse(t, in)
	if got := string(mustMarshal(t, doc)); got != in {
		t.Errorf("null marks/content round-trip changed bytes\n in: %s\nout: %s", in, got)
	}
}

func TestFieldAfterSetContentReturnsValidJSON(t *testing.T) {
	doc := mustParse(t, preservationFixture)
	node := &doc.Content()[0]
	node.SetContent(nil)
	raw, ok := node.Field("content")
	if !ok {
		t.Fatal("content field missing after SetContent")
	}
	var decoded []json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Errorf("Field(content) after SetContent is not valid JSON: %v (raw %q)", err, raw)
	}
}

func TestGettersReturnCopies(t *testing.T) {
	// The stored bytes ARE the output; an aliased getter return would let
	// one careless write emit corrupted JSON with no error anywhere.
	doc := mustParse(t, preservationFixture)
	before := string(mustMarshal(t, doc))

	panel := &doc.Content()[2]
	if raw := panel.Attrs(); len(raw) > 0 {
		raw[0] = 'x'
	}
	if raw, ok := panel.Field("attrs"); ok && len(raw) > 0 {
		raw[0] = 'x'
	}

	if after := string(mustMarshal(t, doc)); after != before {
		t.Errorf("mutating getter results corrupted the document\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestParseDocumentSentinels(t *testing.T) {
	if _, err := adf.ParseDocument([]byte(`{"type":"paragraph","version":1}`)); !errors.Is(err, adf.ErrNotDocument) {
		t.Errorf("non-doc root: got %v, want ErrNotDocument", err)
	}
	if _, err := adf.ParseDocument([]byte(`{"type":"doc"}`)); !errors.Is(err, adf.ErrNoVersion) {
		t.Errorf("missing version: got %v, want ErrNoVersion", err)
	}
}
