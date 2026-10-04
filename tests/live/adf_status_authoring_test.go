//go:build live

package live

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestLiveADFStatusAuthoring(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "markdown-status-authoring")
	markdown := ":status[Ready \\*literal\\* &amp; 雪]{color=green}\n\n" +
		":status[BLOCKED]{color='#12abEF'}\n\n:status[WAITING]"
	expected := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"status","attrs":{"text":"Ready *literal* & 雪","color":"green"}}]},{"type":"paragraph","content":[{"type":"status","attrs":{"text":"BLOCKED","color":"#12abEF"}}]},{"type":"paragraph","content":[{"type":"status","attrs":{"text":"WAITING","color":"neutral"}}]}]}`)
	if err := adf.Validate(expected); err != nil {
		t.Fatal("independently authored status expectation is invalid", err)
	}
	posted := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--markdown", markdown)
	id := postedADFCommentID(t, posted)
	r.registerComment(t, key, id)
	stored := rawOwnedComment(t, r, key, id)
	equal, err := adf.StructuralEqual(expected, stored.Body)
	if err != nil || !equal {
		t.Fatal("stored status differs from independent label/color/default expectations", err)
	}
	listed := r.success(t, "issue", "comment", "list", key, "--all")
	if err := verifyStoredADFComments(map[string]json.RawMessage{id: expected}, listed); err != nil {
		t.Fatal(err)
	}
	// Known native statuses now have a proof-backed directive inverse.
	doc, err := adf.ParseDocument(stored.Body)
	if err != nil {
		t.Fatal(err)
	}
	projection := adf.ToMarkdownLossy(doc)
	if len(projection.LossyConstructs) != 0 {
		t.Fatal("known native statuses lost their proved directive inverse")
	}
	inverse, warnings, err := adf.FromMarkdownLossy(projection.Markdown)
	if err != nil || len(warnings) != 0 {
		t.Fatal("stored status projection cannot be converted", err, warnings)
	}
	inverseBytes, err := inverse.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	equal, err = adf.StructuralEqual(expected, inverseBytes)
	if err != nil || !equal {
		t.Fatal("stored status inverse lost independent native label/color expectations", err)
	}

	// Literal native text must stay literal after enabling directive authoring.
	const literalPanel = `{"version":1,"type":"doc","content":[{"type":"panel","attrs":{"panelType":"info"},"content":[{"type":"paragraph","content":[{"type":"text","text":":status and :status[READY]{color=green}"}]}]}]}`
	endpoint, path := r.rawDescriptionInput(t, key, []byte(literalPanel))
	updated, frame, exit := rawMutation(t, r, key, nil, "api", "put", endpoint, "--input", path, "--force")
	requireRawSuccess(t, updated, frame, exit, 204)
	native := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=description")
	var issue struct {
		Fields struct{ Description json.RawMessage }
	}
	if err := json.Unmarshal(native.Response.JSON, &issue); err != nil {
		t.Fatal(err)
	}
	equal, err = adf.StructuralEqual([]byte(literalPanel), issue.Fields.Description)
	if err != nil || !equal {
		t.Fatal("native literal panel storage changed", err)
	}
	assertStoredPreconditionBody(t, r, key, "", "description", []byte(literalPanel))
	_, rendered, exit := r.invoke(t, t.Context(), nil, "adf", "render", "--input", r.nativeBody(t, issue.Fields.Description))
	var renderedPanel struct{ Data envelope.ADFRenderOutput }
	if exit != 0 || json.Unmarshal(rendered, &renderedPanel) != nil || renderedPanel.Data.Lossy {
		t.Fatal("stored literal text lost panel representability")
	}
	markdownPath := filepath.Join(r.dir, "literal-panel.md")
	if err := os.WriteFile(markdownPath, []byte(renderedPanel.Data.Markdown), 0o600); err != nil {
		t.Fatal(err)
	}
	_, converted, exit := r.invoke(t, t.Context(), nil, "adf", "convert", "--input", markdownPath)
	var candidate struct{ Data envelope.ADFConvertOutput }
	if exit != 0 || json.Unmarshal(converted, &candidate) != nil || candidate.Data.Lossy || len(candidate.Data.Diagnostics) != 0 {
		t.Fatal("strict stored literal panel conversion failed")
	}
	equal, err = adf.StructuralEqual([]byte(literalPanel), candidate.Data.Document)
	if err != nil || !equal {
		t.Fatal("binary changed stored directive-shaped literal text", err)
	}
	doc, err = adf.ParseDocument(issue.Fields.Description)
	if err != nil {
		t.Fatal(err)
	}
	session, err := adf.PrepareEdit(doc)
	if err != nil || len(session.Regions()) != 1 || session.Regions()[0].Protected {
		t.Fatalf("stored literal native panel became protected: %v", err)
	}
}
