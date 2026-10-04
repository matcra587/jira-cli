//go:build live

package live

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

func TestLiveADFStatusBoundaries(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "markdown-status-boundaries")
	const ready = `{"type":"status","attrs":{"text":"READY","color":"green"}}`
	var paragraphs, expectedBlocks []string
	for _, tc := range []struct{ source, preceding string }{
		{source: `[open](https://example.test):status[READY]{color=green}`, preceding: `{"type":"text","text":"open","marks":[{"type":"link","attrs":{"href":"https://example.test"}}]}`},
		{source: `**[open](https://example.test)**:status[READY]{color=green}`, preceding: `{"type":"text","text":"open","marks":[{"type":"strong"},{"type":"link","attrs":{"href":"https://example.test"}}]}`},
		{source: `[open](https://example.test)**!**:status[READY]{color=green}`, preceding: `{"type":"text","text":"open","marks":[{"type":"link","attrs":{"href":"https://example.test"}}]},{"type":"text","text":"!","marks":[{"type":"strong"}]}`},
		{source: "`https://example.test`:status[READY]{color=green}", preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"code"}]}`},
		{source: `<https://example.test>:status[READY]{color=green}`, preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"link","attrs":{"href":"https://example.test"}}]}`},
		{source: `:status[https://example.test]{color=blue}:status[READY]{color=green}`, preceding: `{"type":"status","attrs":{"text":"https://example.test","color":"blue"}}`},
		{source: `**https://example.test**:status[READY]{color=green}`, preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"strong"}]}`},
		{source: `~~https://example.test~~:status[READY]{color=green}`, preceding: `{"type":"text","text":"https://example.test","marks":[{"type":"strike"}]}`},
	} {
		paragraphs = append(paragraphs, tc.source)
		expectedBlocks = append(expectedBlocks, `{"type":"paragraph","content":[`+tc.preceding+`,`+ready+`]}`)
	}
	expected := []byte(`{"version":1,"type":"doc","content":[` + strings.Join(expectedBlocks, ",") + `]}`)
	if err := adf.Validate(expected); err != nil {
		t.Fatal("independently authored boundary expectation is invalid", err)
	}
	posted := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--markdown", strings.Join(paragraphs, "\n\n"))
	id := postedADFCommentID(t, posted)
	r.registerComment(t, key, id)
	stored := rawOwnedComment(t, r, key, id)
	equal, err := adf.StructuralEqual(expected, stored.Body)
	if err != nil || !equal {
		t.Fatal("stored inline owners or following statuses differ from independent expectations", err)
	}
	listed := r.success(t, "issue", "comment", "list", key, "--all")
	if err := verifyStoredADFComments(map[string]json.RawMessage{id: expected}, listed); err != nil {
		t.Fatal(err)
	}

	// URI contents remain literal even when the URI carries Markdown marks.
	const literal = `mailto:person@example.test?subject=:status[READY]{color=green}`
	posted = r.mutate(t, []string{key}, "issue", "comment", "add", key, "--markdown", "**"+literal+"**")
	id = postedADFCommentID(t, posted)
	r.registerComment(t, key, id)
	stored = rawOwnedComment(t, r, key, id)
	doc, err := adf.ParseDocument(stored.Body)
	if err != nil || doc.Validate() != nil {
		t.Fatal("stored formatted URI is invalid", err)
	}
	blocks := doc.Content()
	if len(blocks) != 1 || blocks[0].Type() != "paragraph" {
		t.Fatal("stored formatted URI changed its block")
	}
	var text strings.Builder
	for _, child := range blocks[0].Content() {
		if child.Type() != "text" || len(child.Marks()) != 1 || child.Marks()[0].Type() != "strong" {
			t.Fatal("stored URI authored status or changed formatting")
		}
		text.WriteString(child.Text())
	}
	if text.String() != literal {
		t.Fatal("stored formatted URI changed its literal text")
	}
}
