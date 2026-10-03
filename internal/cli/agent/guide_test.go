package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gechr/clog"
	"github.com/matcra587/docent"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/output"
)

func TestGuideHumanSinkPreservesLayoutAndRejectsControlSequences(t *testing.T) {
	const hostile = "first<&>\u2028\x1b]52;c;clipboard\a\x1b[31mred\x1b[0m\t\b\x00last"
	const source = "---\nslug: hostile\ntitle: Hostile body\ndescription: Exercise terminal text handling.\nwhen_to_use: When verifying guide boundaries.\ncommands: []\n---\n\n## Decide\n\n" + hostile + "\n\n## Run\n\nsecond line\n\n## Save\n\n## Preconditions\n\n## Recover\n\n## Next\n"
	guides, err := docent.LoadGuides(fstest.MapFS{"hostile.md": &fstest.MapFile{Data: []byte(source)}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := readGuide(guides, "jira guide", []string{"hostile"}, "", false)
	if err != nil || !strings.Contains(out.Document, hostile) {
		t.Fatal("canonical guide document lost source bytes before human rendering")
	}
	for _, mode := range []output.Format{output.FormatJSON, output.FormatCompact} {
		var machine bytes.Buffer
		res := output.Assemble(output.Inputs{Op: "guide", Data: out})
		if err := output.Render(t.Context(), &machine, mode, res); err != nil {
			t.Fatal(err)
		}
		payload := machine.Bytes()
		if mode == output.FormatJSON {
			var frame struct{ Data json.RawMessage }
			if err := json.Unmarshal(payload, &frame); err != nil {
				t.Fatal(err)
			}
			payload = frame.Data
		}
		var decoded envelope.GuideOutput
		if json.Unmarshal(payload, &decoded) != nil || decoded.Document != out.Document {
			t.Fatal("machine rendering changed the canonical guide bytes")
		}
		if !strings.Contains(machine.String(), "first<&>\u2028") {
			t.Fatal("machine rendering escaped ordinary source characters")
		}
	}
	var rendered bytes.Buffer
	if err := renderGuide(output.NewView(&rendered, clog.ColorNever), envelope.Envelope{Data: out}); err != nil {
		t.Fatal(err)
	}
	text := rendered.String()
	if strings.ContainsAny(text, "\x1b\a\r\t\b\x00") || strings.Contains(text, "clipboard") {
		t.Fatal("guide human sink emitted terminal controls")
	}
	if !strings.Contains(text, "first<&>\u2028redlast\n") || !strings.Contains(text, "## Run\n\n\nsecond line\n") || strings.Count(text, "## ") != 6 {
		t.Fatalf("human sanitization changed ordinary text or runbook layout: %q", text)
	}
}
