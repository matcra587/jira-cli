//go:build live

package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

// The native expectation is authored independently of the Markdown converter.
// Only this run's owned issue/comment are written; no concurrent-write claim.
func TestLiveADFPanelRoundTrip(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "native-panel-roundtrip")
	var panels []string
	for _, kind := range []string{"info", "tip", "note", "warning", "error"} {
		panels = append(panels, `{"type":"panel","attrs":{"panelType":"`+kind+`"},"content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Heading"}]},{"type":"paragraph","content":[{"type":"text","text":"native","marks":[{"type":"strong"}]}]},{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"item"}]}]}]},{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"code"}]},{"type":"rule"}]}`)
	}
	expected := []byte(`{"type":"doc","version":1,"content":[` + strings.Join(panels, ",") + `]}`)
	if err := adf.Validate(expected); err != nil {
		t.Fatal("invalid independent native panels", err)
	}
	endpoint, path := r.rawDescriptionInput(t, key, expected)
	updated, frame, exit := rawMutation(t, r, key, nil, "api", "put", endpoint, "--input", path, "--force")
	requireRawSuccess(t, updated, frame, exit, 204)
	// Read raw storage independently before checking the domain route. The
	// Markdown projection below starts from what Jira stored, not our input.
	readStored := func(want []byte) []byte {
		native := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=description")
		var issue struct {
			Fields struct{ Description json.RawMessage }
		}
		if err := json.Unmarshal(native.Response.JSON, &issue); err != nil {
			t.Fatal(err)
		}
		equal, err := adf.StructuralEqual(want, issue.Fields.Description)
		if err != nil || !equal {
			t.Fatal("raw native panels differ from independent expectation", err)
		}
		assertStoredPreconditionBody(t, r, key, "", "description", want)
		return issue.Fields.Description
	}
	storedDescription := readStored(expected)

	input := r.nativeBody(t, storedDescription)
	_, rendered, exit := r.invoke(t, t.Context(), nil, "adf", "render", "--input", input)
	var projection struct{ Data envelope.ADFRenderOutput }
	if exit != 0 || json.Unmarshal(rendered, &projection) != nil || projection.Data.Lossy || len(projection.Data.LossyConstructs) != 0 {
		t.Fatal("native panels did not project without loss")
	}
	for _, marker := range []string{"NOTE", "TIP", "IMPORTANT", "WARNING", "CAUTION"} {
		if !strings.Contains(projection.Data.Markdown, "> [!"+marker+"]") {
			t.Fatal("native projection lost panel kind", marker)
		}
	}
	markdown := filepath.Join(r.dir, "panel-projection.md")
	if err := os.WriteFile(markdown, []byte(projection.Data.Markdown), 0o600); err != nil {
		t.Fatal(err)
	}
	_, converted, exit := r.invoke(t, t.Context(), nil, "adf", "convert", "--input", markdown)
	var candidate struct{ Data envelope.ADFConvertOutput }
	if exit != 0 || json.Unmarshal(converted, &candidate) != nil || candidate.Data.Lossy || len(candidate.Data.Diagnostics) != 0 {
		t.Fatal("strict panel conversion failed")
	}
	equal, err := adf.StructuralEqual(expected, candidate.Data.Document)
	if err != nil || !equal {
		t.Fatal("actual binary panel round trip changed native structure", err)
	}
	endpoint, path = r.rawDescriptionInput(t, key, candidate.Data.Document)
	updated, frame, exit = rawMutation(t, r, key, nil, "api", "put", endpoint, "--input", path, "--force")
	requireRawSuccess(t, updated, frame, exit, 204)
	readStored(expected)

	doc, err := adf.ParseDocument(candidate.Data.Document)
	if err != nil {
		t.Fatal(err)
	}
	session, err := adf.PrepareEdit(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, region := range session.Regions() {
		if region.Protected {
			t.Fatal("stored representable panel stayed protected")
		}
	}
	changed, err := session.Apply(strings.Replace(session.Markdown(), "native", "changed", 1))
	if err != nil {
		t.Fatal("local panel edit failed", err)
	}
	changedBytes, err := changed.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	expected = []byte(strings.Replace(string(expected), `"native"`, `"changed"`, 1))
	equal, err = adf.StructuralEqual(expected, changedBytes)
	if err != nil || !equal {
		t.Fatal("panel edit changed unaffected native structure", err)
	}
	endpoint, path = r.rawDescriptionInput(t, key, changedBytes)
	updated, frame, exit = rawMutation(t, r, key, nil, "api", "put", endpoint, "--input", path, "--force")
	requireRawSuccess(t, updated, frame, exit, 204)
	readStored(expected)
	posted := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--json-input", r.nativeBody(t, changedBytes))
	id := postedADFCommentID(t, posted)
	r.registerComment(t, key, id)
	stored := rawOwnedComment(t, r, key, id)
	equal, err = adf.StructuralEqual(expected, stored.Body)
	if err != nil || !equal {
		t.Fatal("native panel comment storage changed", err)
	}
	assertStoredPreconditionBody(t, r, key, id, "comment", expected)
}
