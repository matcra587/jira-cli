//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// The expected native document is authored independently of the converter.
// Both raw and domain reads check storage; only this run's fixture is changed.
func TestLiveADFMarkdownAlerts(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "markdown-alert-panels")
	var markdown strings.Builder
	var panels []string
	for _, alert := range []struct {
		name string
		kind string
	}{
		{name: "NOTE", kind: "info"},
		{name: "TIP", kind: "tip"},
		{name: "IMPORTANT", kind: "note"},
		{name: "WARNING", kind: "warning"},
		{name: "CAUTION", kind: "error"},
	} {
		fmt.Fprintf(&markdown, "> [!%s]\n> **Keep** this.\n\n", alert.name)
		panels = append(panels, fmt.Sprintf(`{"type":"panel","attrs":{"panelType":%q},"content":[{"type":"paragraph","content":[{"type":"text","text":"Keep","marks":[{"type":"strong"}]},{"type":"text","text":" this."}]}]}`, alert.kind))
	}
	expected := []byte(`{"version":1,"type":"doc","content":[` + strings.Join(panels, ",") + `]}`)
	if err := adf.Validate(expected); err != nil {
		t.Fatal("independently authored alert expectation is invalid")
	}
	posted := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--markdown", markdown.String())
	id := postedADFCommentID(t, posted)
	r.registerComment(t, key, id)
	stored := rawOwnedComment(t, r, key, id)
	equal, err := adf.StructuralEqual(expected, stored.Body)
	if err != nil || !equal {
		t.Fatal("Jira did not retain all five authored native panels and formatting")
	}
	listed := r.success(t, "issue", "comment", "list", key, "--all")
	if err := verifyStoredADFComments(map[string]json.RawMessage{id: expected}, listed); err != nil {
		t.Fatal(err)
	}
}
