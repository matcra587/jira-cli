package adf_test

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// FuzzRoundTrip hammers the preservation contract: any input that parses
// must reach a fixed point after one round-trip — marshal(parse(x)) fed
// back through parse→marshal reproduces itself byte-for-byte. (Byte
// identity with the ORIGINAL is only promised for compact input, so the
// assertion pins idempotence, which holds for every input.)
func FuzzRoundTrip(f *testing.F) {
	f.Add([]byte(`{"type":"doc","version":1,"content":[]}`))
	f.Add([]byte(`{"type":"doc","version":1,"content":[{"type":"text","text":"a < b"}]}`))
	f.Add([]byte(`{"version":1,"type":"doc","content":[{"attrs":{"bigId":9007199254740993,"ratio":1.50},"type":"futureWidget","marks":null},{"marks":[{}]},{}]}`))
	f.Add([]byte(`{"type":"doc","version":1,"content":[{"type":"paragraph","marks":[],"content":null}]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := adf.ParseDocument(data)
		if err != nil {
			t.Skip()
		}
		first, err := doc.Marshal()
		if err != nil {
			t.Fatalf("marshal after parse: %v", err)
		}
		doc2, err := adf.ParseDocument(first)
		if err != nil {
			t.Fatalf("own output does not re-parse: %v\noutput: %s", err, first)
		}
		second, err := doc2.Marshal()
		if err != nil {
			t.Fatalf("marshal after re-parse: %v", err)
		}
		if string(first) != string(second) {
			t.Errorf("round-trip is not a fixed point\nfirst:  %s\nsecond: %s", first, second)
		}
	})
}
