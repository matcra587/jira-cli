package jira

import (
	"bytes"
	"encoding/json"
	"testing"
)

// rawADFSample carries the runes stdlib's default encoder rewrites (<, >,
// &) plus the line separator it escapes even with HTML escaping off — the
// exact bytes a preserved ADF description brings through the wire tier.
const rawADFSample = "{\"text\":\"<tag>&amp;end next\"}"

// The wire tier is the innermost marshal boundary a preserved value
// crosses, so byte fidelity has to hold HERE: escaping only goes forward,
// and no outer encoder setting can undo escapes a nested Marshaler has
// already baked in. These two tests pin that, and they exist because it
// was once false — Nullable.MarshalJSON used package-level json.Marshal
// and silently mangled every ADF description on the way out.
func TestNullableRawMessagePreservesBytes(t *testing.T) {
	t.Parallel()
	n := NullableOf(json.RawMessage(rawADFSample))

	out, err := n.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != rawADFSample {
		t.Errorf("Nullable[json.RawMessage].MarshalJSON() = %s, want the input bytes %s", out, rawADFSample)
	}
}

func TestIssueFieldsPreservesBytesThroughExtraMerge(t *testing.T) {
	t.Parallel()
	fields := IssueFields{
		Description: NullableOf(json.RawMessage(rawADFSample)),
		// A populated Extra forces the merge path, which re-encodes the
		// whole object — the second place escaping used to creep in.
		Extra: map[string]json.RawMessage{"customfield_1": json.RawMessage(rawADFSample)},
	}

	// Exercise the actual request encoder. Go 1.27's v1 JSON encoder
	// escapes line separators even in RawMessage with HTML escaping off;
	// that encoder is not a preservation boundary.
	out, err := MarshalNoEscape(fields)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.Contains(out, []byte(rawADFSample)) {
		t.Errorf("declared field mangled:\n got: %s\nwant it to contain: %s", out, rawADFSample)
	}
	if bytes.Contains(out, []byte(`\u003c`)) || bytes.Contains(out, []byte(`\u2028`)) {
		t.Errorf("escape sequences present in output: %s", out)
	}
}
