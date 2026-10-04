//go:build live

package live

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// Inspection reads Jira's independently stored native body. These local reports
// do not establish remote write preconditions or Markdown editing fidelity.
func TestLiveADFInspection(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "native-adf-inspection")
	expected := []byte(`{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"warning","localId":"11111111-1111-4111-8111-111111111111"},"content":[{"type":"paragraph","content":[{"type":"text","text":"native inspection","marks":[{"type":"strong"}]}]}]}]}`)
	posted := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--json-input", r.nativeBody(t, expected))
	id := postedADFCommentID(t, posted)
	r.registerComment(t, key, id)
	stored := rawOwnedComment(t, r, key, id)
	equal, err := adf.StructuralEqual(expected, stored.Body)
	if err != nil || !equal {
		t.Fatal("native fixture differs from independently authored expectation")
	}
	path := r.nativeBody(t, stored.Body)
	for _, route := range []string{"validate", "lint"} {
		frame, stdout, exit := r.invoke(t, t.Context(), nil, "adf", route, "--input", path)
		var report struct {
			Data envelope.ADFLintOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(stdout, &report) != nil || !report.Data.Valid || report.Data.Diagnostics == nil || len(report.Data.Diagnostics) != 0 || report.Data.SchemaSHA256 != adf.SchemaSHA256 {
			t.Fatal("stored native ADF did not pass local inspection")
		}
		if route == "lint" && (!report.Data.ProjectionAvailable || !report.Data.Lossy || !slices.Equal(report.Data.LossyConstructs, []string{"panel"})) {
			t.Fatal("stored rich panel projection risk was not reported")
		}
	}
	frame, stdout, exit := r.invoke(t, t.Context(), nil, "adf", "explain-node", "panel_node")
	var explanation struct {
		Data envelope.ADFExplainOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &explanation) != nil || len(explanation.Data.Entries) != 1 || explanation.Data.Entries[0].Definition != "panel_node" || !explanation.Data.Entries[0].Capabilities.Author || explanation.Data.Entries[0].Capabilities.Edit {
		t.Fatal("local panel explanation does not describe observed authoring support honestly")
	}
	bounds := explanation.Data.Entries[0].NodeBounds
	if bounds == nil || bounds.Attrs == nil || !slices.Contains(bounds.Attrs.Required, "panelType") || bounds.Content == nil || bounds.Content.MinItems != 1 || !slices.Contains(bounds.Content.Refs, "paragraph_with_no_marks_node") {
		t.Fatal("panel explanation flattened the required attributes or child context")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(stored.Body) {
		t.Fatal("local inspection changed native input bytes")
	}
	retained := rawOwnedComment(t, r, key, id)
	equal, err = adf.StructuralEqual(expected, retained.Body)
	if err != nil || !equal {
		t.Fatal("local inspection changed Jira's stored native fixture")
	}
}
