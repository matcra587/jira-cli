//go:build flagmatrix

package matrix

import (
	"strings"
	"testing"
)

func TestADFInputFlagEffects(t *testing.T) {
	t.Parallel()
	converted := fwParse(t, fwExec(t, "", map[string]string{"notes.md": "file source"}, "stdin source", "adf", "convert", "--input=notes.md"))
	if !strings.Contains(fwJSONOf(t, converted.Data["document"]), "file source") || strings.Contains(fwJSONOf(t, converted.Data["document"]), "stdin source") {
		t.Fatal("convert input flag did not select the file over stdin")
	}
	Prove(t, "jira adf convert", "input", "selected file source rather than competing piped stdin content")
	const native = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"file source"}]}]}`
	rendered := fwParse(t, fwExec(t, "", map[string]string{"native.json": native}, "invalid competing stdin", "adf", "render", "--input=native.json"))
	if rendered.Data["markdown"] != "file source\n" || rendered.Data["lossy"] != false {
		t.Fatal("render input flag did not select the native file")
	}
	Prove(t, "jira adf render", "input", "projected native file source instead of rejecting the competing invalid stdin")
}

func TestADFStrictAndBestEffortFlagEffects(t *testing.T) {
	t.Parallel()
	const input = "<script>lost</script>"
	refusal := fwExec(t, "", nil, input, "adf", "convert")
	fwRefusal(t, refusal, "adf_content_loss", "input")
	for _, option := range []struct{ flag, argument string }{
		{"adf-strict", "--adf-strict=false"}, {"adf-best-effort", "--adf-best-effort"},
	} {
		accepted := fwParse(t, fwExec(t, "", nil, input, "adf", "convert", option.argument))
		if accepted.Data["lossy"] != true || len(accepted.Data["diagnostics"].([]any)) == 0 || len(accepted.Data["document"].(map[string]any)["content"].([]any)) != 0 {
			t.Fatal("accepted content loss lost its diagnostics or empty candidate")
		}
		Prove(t, "jira adf convert", option.flag, "default refused actual HTML loss; explicit flag accepted a validated empty candidate with visible loss diagnostics")
	}
}
