//go:build live

package live

import (
	"encoding/json"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// Check independently authored literal expectations through both stored read
// routes. Code is deliberately kept beside decoded text to detect over-decoding.
func TestLiveADFMarkdownText(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "markdown-text-fidelity")
	markdown := "\\*literal\\* \\[label\\] &amp; &amp;lt; &#00065; 雪\n\n> [!NOTE]\n> \\*native\\* &amp;\n\n`\\* &amp; &#65;`"
	expected := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"*literal* [label] & &lt; A 雪"}]},{"type":"panel","attrs":{"panelType":"info"},"content":[{"type":"paragraph","content":[{"type":"text","text":"*native* &"}]}]},{"type":"paragraph","content":[{"type":"text","text":"\\* &amp; &#65;","marks":[{"type":"code"}]}]}]}`)
	if err := adf.Validate(expected); err != nil {
		t.Fatal("independently authored literal expectation is invalid", err)
	}
	posted := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--markdown", markdown)
	id := postedADFCommentID(t, posted)
	r.registerComment(t, key, id)
	stored := rawOwnedComment(t, r, key, id)
	equal, err := adf.StructuralEqual(expected, stored.Body)
	if err != nil || !equal {
		t.Fatal("stored native text differs from decoded literal and unchanged code expectations", err)
	}
	listed := r.success(t, "issue", "comment", "list", key, "--all")
	if err := verifyStoredADFComments(map[string]json.RawMessage{id: expected}, listed); err != nil {
		t.Fatal(err)
	}
	doc, err := adf.ParseDocument(stored.Body)
	if err != nil {
		t.Fatal(err)
	}
	projection := adf.ToMarkdownLossy(doc)
	if len(projection.LossyConstructs) != 0 {
		t.Fatal("stored literal content lacks an exact Markdown representation", projection.LossyConstructs)
	}
	converted, warnings, err := adf.FromMarkdownLossy(projection.Markdown)
	if err != nil || len(warnings) != 0 {
		t.Fatal("stored literal projection did not convert cleanly", err)
	}
	candidate, err := converted.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	equal, err = adf.StructuralEqual(expected, candidate)
	if err != nil || !equal {
		t.Fatal("stored literal projection changed native text on conversion", err)
	}
}
