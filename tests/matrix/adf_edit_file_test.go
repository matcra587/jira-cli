//go:build flagmatrix

package matrix

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestADFAnchoredEditFileFlagEffects(t *testing.T) {
	t.Parallel()
	const original = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"original body"}]},{"type":"futureBlock","attrs":{"opaque":"preserved"}}],"extension":"original root"}`
	const current = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"original body"}]},{"type":"futureBlock","attrs":{"opaque":"preserved"}},{"type":"paragraph","content":[{"type":"text","text":"remote body"}]}],"extension":"current root"}`
	prepared := fwSuccess(t, fwExec(t, "", map[string]string{"original.json": original}, "invalid competing stdin", "adf", "edit", "prepare", "--input=original.json"))
	markdown, ok := prepared.Data["markdown"].(string)
	if !ok || !strings.Contains(markdown, "original body") || strings.Contains(markdown, "invalid competing stdin") {
		t.Fatal("prepare input flag did not project selected native file")
	}
	Prove(t, "jira adf edit prepare", "input", "projected selected native file instead of rejecting competing invalid stdin")
	edited := strings.Replace(markdown, "original body", "authored body", 1)
	for _, merge := range []bool{false, true} {
		route := "apply"
		args := []string{"adf", "edit", route, "--input=original.json", "--markdown-file=edited.md"}
		files := map[string]string{"original.json": original, "edited.md": edited}
		if merge {
			route = "merge"
			args[2] = route
			args = append(args, "--current=current.json")
			files["current.json"] = current
		}
		t.Run(route, func(t *testing.T) {
			out := fwSuccess(t, fwExec(t, "", files, "invalid competing stdin", args...))
			document, ok := out.Data["document"].(map[string]any)
			if !ok {
				t.Fatal("selected editing files did not produce a native candidate")
			}
			expected := original
			if merge {
				expected = current
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(strings.Replace(expected, "original body", "authored body", 1)), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(document, want) {
				t.Fatalf("native candidate lost authored or preserved fields: got=%#v want=%#v", document, want)
			}
			path := "jira adf edit " + route
			Prove(t, path, "input", "used immutable native file instead of competing invalid stdin; matching anchors and protected data survive")
			Prove(t, path, "markdown-file", "applied selected authored file text rather than leaving original content unchanged")
			if merge {
				Prove(t, path, "current", "preserved current file root extensions and independent remote block while merging authored edits")
			}
		})
	}
}
