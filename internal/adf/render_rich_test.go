package adf_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/gechr/x/ansi"

	"github.com/matcra587/jira-cli/internal/adf"
)

type statusRecordingStyler struct {
	fakeStyler
	label, hue string
}

func (s *statusRecordingStyler) Status(label, hue string) string {
	s.label, s.hue = label, hue
	return "[" + label + "]"
}

const richTableDocument = `{"type":"doc","version":1,"content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Name"}]}]},{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Value"}]}]}]},{"type":"tableRow","content":[{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"東京"}]}]},{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"abcdefghijklmno"}]}]}]}]}]}`

func TestRenderPanelPreservesStructuredBody(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"visible","marks":[{"type":"strong"}]}]},{"type":"codeBlock","content":[{"type":"text","text":"line one\nline two"}]}]}]}`)
	before, err := doc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got := adf.Render(doc, fakeStyler{}, 30)
	for _, want := range []string{"Warning", "**visible**", "`line one`", "`line two`", "│"} {
		if !strings.Contains(got, want) {
			t.Errorf("panel lost %q: %q", want, got)
		}
	}
	after, err := doc.Marshal()
	if err != nil || string(after) != string(before) {
		t.Fatal("render altered native panel bytes")
	}
}

func TestRenderStatusSanitizesAttrsBeforeStyling(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"status","attrs":{"text":"Ready\u001b\u0007","color":"#aa55cc\u001b"}},{"type":"text","text":" next"}]}]}`)
	styler := &statusRecordingStyler{}
	got := adf.Render(doc, styler, 0)
	if styler.label != "Ready" || styler.hue != "#aa55cc" || got != "[Ready] next" {
		t.Fatalf("status attrs not sanitized before styling: label=%q hue=%q output=%q", styler.label, styler.hue, got)
	}
	plain := adf.Render(doc, fakeStyler{}, 0)
	if plain != "[Ready] next" {
		t.Fatalf("status fallback lost inline text: %q", plain)
	}
}

func TestRenderTableWrapsWithoutDroppingCells(t *testing.T) {
	for _, width := range []int{0, 30, 18, 8} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			got := adf.Render(mustParse(t, richTableDocument), fakeStyler{}, width)
			var columns [2]string
			for _, line := range strings.Split(ansi.Strip(got), "\n") {
				fields := strings.Split(line, "│")
				if len(fields) == 4 {
					for c := range columns {
						columns[c] += strings.TrimSpace(fields[c+1])
					}
				}
			}
			joined := strings.ReplaceAll(strings.Join(columns[:], ""), "**", "")
			for _, want := range []string{"Name", "Value", "東京", "abcdefghijklmno"} {
				if !strings.Contains(joined, want) {
					t.Errorf("width %d lost cell %q: %q", width, want, got)
				}
			}
			if strings.Contains(got, "…") {
				t.Fatal("table truncated cell text")
			}
			if width > 0 {
				for _, line := range strings.Split(got, "\n") {
					if ansi.StringWidth(line) > width {
						t.Errorf("width %d overflow: %q", width, line)
					}
				}
			}
		})
	}
}

func TestRenderTableMergedCellsPreservePlacement(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","attrs":{"rowspan":2},"content":[{"type":"paragraph","content":[{"type":"text","text":"left"}]}]},{"type":"tableCell","attrs":{"colspan":2},"content":[{"type":"paragraph","content":[{"type":"text","text":"across"}]}]}]},{"type":"tableRow","content":[{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"middle"}]}]},{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"right"}]}]}]}]}]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	if strings.Count(got, "left") != 1 || strings.Count(got, "across") != 1 || !strings.Contains(got, "middle") || !strings.Contains(got, "right") {
		t.Fatalf("merged table lost or repeated cells: %q", got)
	}
	var first, second string
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "across") {
			first = line
		}
		if strings.Contains(line, "middle") {
			second = line
		}
	}
	if strings.Count(first, "│") != 3 || strings.Count(second, "│") != 4 || strings.Index(first, "left") >= strings.Index(first, "across") || strings.Index(second, "middle") >= strings.Index(second, "right") {
		t.Fatalf("merged table lost geometry: %q", got)
	}
}

func TestRenderTableUnsafeSpanKeepsCellContent(t *testing.T) {
	for _, span := range []string{"1e99", "-1", "1.5"} {
		raw := `{"type":"doc","version":1,"content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","attrs":{"colspan":` + span + `},"content":[{"type":"paragraph","content":[{"type":"text","text":"retained"}]}]}]}]}]}`
		got := adf.Render(mustParse(t, raw), fakeStyler{}, 0)
		if !strings.Contains(got, "retained") || !strings.Contains(got, `"colspan":`+span) {
			t.Fatalf("unsafe span lost content/attrs: %q", got)
		}
	}
}

func TestRenderMalformedStatusRemainsExplicit(t *testing.T) {
	for _, status := range []string{`{"type":"status"}`, `{"type":"status","attrs":null}`, `{"type":"status","attrs":{"color":"blue"}}`} {
		doc := mustParse(t, `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[`+status+`]}]}`)
		if got := adf.Render(doc, fakeStyler{}, 0); !strings.Contains(got, "[status —") {
			t.Fatalf("malformed status disappeared: %q", got)
		}
	}
}

func TestRenderTableMalformedChildrenRetainContentAndIdentity(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"paragraph","content":[{"type":"text","text":"paragraph retained"}]},{"type":"text","text":"direct retained"},{"type":"futureCell","content":[{"type":"paragraph","content":[{"type":"text","text":"future retained"}]}]}]},{"type":"tableRow","content":[]}]}]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	for _, want := range []string{"paragraph retained", "direct retained", "future retained", "[futureCell —", "[tableRow —"} {
		if !strings.Contains(got, want) {
			t.Errorf("malformed table lost %q: %q", want, got)
		}
	}
}

type headerSafetyStyler struct {
	fakeStyler
	nested bool
}

func (s *headerSafetyStyler) Strong(text string) string {
	if strings.Contains(text, "\x1b") {
		s.nested = true
	}
	return "\x1b[1m" + text + "\x1b[0m"
}
func (*headerSafetyStyler) Code(text string) string { return "\x1b[31m" + text + "\x1b[0m" }

func TestRenderTableHeaderDoesNotNestStyledSpans(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"code","marks":[{"type":"code"}]},{"type":"text","text":" trailing"}]}]}]}]}]}`)
	styler := &headerSafetyStyler{}
	got := adf.Render(doc, styler, 0)
	if styler.nested || !strings.Contains(ansi.Strip(got), "code trailing") {
		t.Fatalf("header nests styled spans or loses trailing text: %q", got)
	}
}

func TestRenderNarrowMergedTableRetainsAllAuthoredCells(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","attrs":{"colspan":2},"content":[{"type":"paragraph","content":[{"type":"text","text":"retained"}]}]}]}]}]}`)
	got := adf.Render(doc, fakeStyler{}, 6)
	if !strings.Contains(strings.ReplaceAll(got, "\n", ""), "retained") || strings.Contains(got, "…") {
		t.Fatalf("narrow merged fallback lost content: %q", got)
	}
	if !strings.Contains(strings.ReplaceAll(got, "\n", ""), `"colspan":2`) {
		t.Fatalf("narrow merged fallback lost authored span: %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if ansi.StringWidth(line) > 6 {
			t.Errorf("narrow fallback overflow: %q", line)
		}
	}
}

func TestRenderTableNestedBlocksKeepStructureAndSanitize(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","content":[{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"first item"}]}]},{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"second item"}]}]}]},{"type":"codeBlock","content":[{"type":"text","text":"line one\u001b\nline two\u0007"}]},{"type":"paragraph","content":[{"type":"status","attrs":{"text":"Ready","color":"blue"}}]},{"type":"futureBlock"}]}]}]}]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	for _, want := range []string{"• first item", "• second item", "`line one`", "`line two`", "[Ready]", "[futureBlock —"} {
		if !strings.Contains(got, want) {
			t.Errorf("nested cell lost %q: %q", want, got)
		}
	}
	if strings.ContainsAny(got, "\x1b\a") {
		t.Fatal("nested cell leaked authored controls")
	}
}

func TestRenderPanelCustomColorSanitizesBeforeSemanticSink(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"custom","panelColor":"#aA55Cc\u001b\u0007"},"content":[{"type":"paragraph","content":[{"type":"text","text":"visible\u001b\u0007"}]}]}]}`)
	styler := &statusRecordingStyler{}
	got := adf.Render(doc, styler, 12)
	if styler.label != "Custom" || styler.hue != "#aA55Cc" || strings.ContainsAny(got, "\x1b\a") || !strings.Contains(got, "visible") {
		t.Fatalf("custom panel attrs bypassed semantic sanitation: label=%q hue=%q output=%q", styler.label, styler.hue, got)
	}
}

func TestRenderPanelNarrowWidthsRetainBody(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[{"type":"panel","attrs":{"panelType":"info"},"content":[{"type":"paragraph","content":[{"type":"text","text":"abcdefghijklmnop"}]}]}]}`)
	for _, width := range []int{2, 6, 12} {
		got := adf.Render(doc, fakeStyler{}, width)
		joined := strings.NewReplacer("\n", "", "│", "", " ", "").Replace(got)
		if !strings.Contains(joined, "abcdefghijklmnop") {
			t.Errorf("narrow panel lost body: %q", got)
		}
		for _, line := range strings.Split(got, "\n") {
			if ansi.StringWidth(line) > width {
				t.Errorf("width %d panel overflowed: %q", width, line)
			}
		}
	}
}

func TestRenderTableMalformedCellDescendantsRetainText(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","version":1,"content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","attrs":{"colspan":-1},"content":[{"type":"text","text":"direct cell text"},{"type":"futureCellChild","content":[{"type":"paragraph","content":[{"type":"text","text":"future cell text"}]}]}]}]}]}]}`)
	got := adf.Render(doc, fakeStyler{}, 0)
	for _, want := range []string{"direct cell text", "[futureCellChild —", "future cell text"} {
		if !strings.Contains(got, want) {
			t.Errorf("malformed cell lost descendant %q: %q", want, got)
		}
	}
}
