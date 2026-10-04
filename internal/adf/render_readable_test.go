package adf_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/gechr/x/ansi"

	"github.com/matcra587/jira-cli/internal/adf"
)

const readableNativeDocument = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"mention","attrs":{"id":"account-test","text":"@Alex"}},{"type":"text","text":" "},{"type":"emoji","attrs":{"shortName":":smile:","text":"🙂"}}]},{"type":"expand","attrs":{"title":"Details"},"content":[{"type":"paragraph","content":[{"type":"text","text":"alpha"}]},{"type":"nestedExpand","attrs":{"title":"More"},"content":[{"type":"paragraph","content":[{"type":"text","text":"omega"}]}]}]},{"type":"layoutSection","content":[{"type":"layoutColumn","attrs":{"width":50},"content":[{"type":"paragraph","content":[{"type":"text","text":"left"}]}]},{"type":"layoutColumn","attrs":{"width":50},"content":[{"type":"paragraph","content":[{"type":"text","text":"right"}]}]}]},{"type":"taskList","attrs":{"localId":"tasks"},"content":[{"type":"taskItem","attrs":{"localId":"todo","state":"TODO"},"content":[{"type":"text","text":"pending"}]},{"type":"taskItem","attrs":{"localId":"done","state":"DONE"},"content":[{"type":"text","text":"completed"}]}]},{"type":"decisionList","attrs":{"localId":"decisions"},"content":[{"type":"decisionItem","attrs":{"localId":"choice","state":"DECIDED"},"content":[{"type":"text","text":"chosen"}]}]}]}`

func TestRenderReadableNativeContent(t *testing.T) {
	t.Parallel()
	if err := adf.Validate([]byte(readableNativeDocument)); err != nil {
		t.Fatal("invalid independently authored fixture", err)
	}
	doc := mustParse(t, readableNativeDocument)
	got := adf.Render(doc, fakeStyler{}, 0)
	for _, want := range []string{"@Alex", "🙂", "Details", "alpha", "More", "omega", "Column 1 (50%)", "left", "Column 2 (50%)", "right", "[ ] pending", "[x] completed", "Decision (DECIDED)", "chosen"} {
		if !strings.Contains(got, want) {
			t.Errorf("native human rendering lost %q: %q", want, got)
		}
	}
	after, err := doc.Marshal()
	if err != nil || string(after) != readableNativeDocument {
		t.Fatal("readable rendering changed native bytes")
	}
}

func TestRenderReadableNativeNarrowWidthsKeepContent(t *testing.T) {
	t.Parallel()
	for _, width := range []int{2, 4, 12, 24} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			got := adf.Render(mustParse(t, readableNativeDocument), fakeStyler{}, width)
			joined := strings.Join(strings.Fields(got), "")
			for _, want := range []string{"@Alex", "🙂", "alpha", "omega", "left", "right", "pending", "completed", "chosen"} {
				if !strings.Contains(joined, want) {
					t.Errorf("width %d lost %q: %q", width, want, got)
				}
			}
			for _, line := range strings.Split(got, "\n") {
				if ansi.StringWidth(line) > width {
					t.Errorf("width %d overflow: %q", width, line)
				}
			}
		})
	}
}

func TestRenderReadableAttrsSanitizedAndMissingRemainsExplicit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, node, want string }{
		{name: "mention label", node: `{"type":"mention","attrs":{"id":"account","text":"@Alex\u001b\u0007"}}`, want: "@Alex"},
		{name: "mention identity", node: `{"type":"mention","attrs":{"id":"account\u001b\u0007"}}`, want: "@account"},
		{name: "missing mention", node: `{"type":"mention"}`, want: "[mention —"},
		{name: "emoji text", node: `{"type":"emoji","attrs":{"shortName":":smile:","text":"🙂\u001b\u0007"}}`, want: "🙂"},
		{name: "emoji short name", node: `{"type":"emoji","attrs":{"shortName":":custom:\u001b\u0007"}}`, want: ":custom:"},
		{name: "missing emoji", node: `{"type":"emoji"}`, want: "[emoji —"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := adf.Render(mustParse(t, `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[`+tc.node+`]}]}`), fakeStyler{}, 0)
			if !strings.Contains(got, tc.want) || strings.ContainsAny(got, "\x1b\a") {
				t.Fatalf("unsafe or missing native label: %q", got)
			}
		})
	}
}

func TestRenderMalformedReadableContainersKeepDescendants(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, node string }{
		{name: "layout parent attrs", node: `{"type":"layoutSection","attrs":{"localId":5},"content":[{"type":"layoutColumn","attrs":{"width":50},"content":[{"type":"paragraph","content":[{"type":"text","text":"retained"}]}]}]}`},
		{name: "task parent attrs", node: `{"type":"taskList","attrs":{"localId":5},"content":[{"type":"taskItem","attrs":{"state":"TODO"},"content":[{"type":"text","text":"retained"}]}]}`},
		{name: "decision parent attrs", node: `{"type":"decisionList","attrs":{"localId":5},"content":[{"type":"decisionItem","attrs":{"state":"DECIDED"},"content":[{"type":"text","text":"retained"}]}]}`},
		{name: "expand attrs", node: `{"type":"expand","attrs":{"title":5},"content":[{"type":"text","text":"retained"}]}`},
		{name: "layout attrs", node: `{"type":"layoutSection","content":[{"type":"layoutColumn","attrs":{"width":"bad"},"content":[{"type":"paragraph","content":[{"type":"text","text":"retained"}]}]}]}`},
		{name: "task state", node: `{"type":"taskList","content":[{"type":"taskItem","attrs":{"state":"FUTURE"},"content":[{"type":"text","text":"retained"}]}]}`},
		{name: "decision state", node: `{"type":"decisionList","content":[{"type":"decisionItem","content":[{"type":"text","text":"retained"}]}]}`},
		{name: "future block", node: `{"type":"expand","content":[{"type":"futureBlock","content":[{"type":"paragraph","content":[{"type":"text","text":"retained"}]}]}]}`},
		{name: "future text", node: `{"type":"expand","content":[{"type":"futureText","text":"retained"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := mustParse(t, `{"type":"doc","version":1,"content":[`+tc.node+`]}`)
			got := adf.Render(doc, fakeStyler{}, 0)
			if !strings.Contains(got, "retained") || !strings.Contains(got, "see --output=json") {
				t.Fatalf("malformed native container dropped identity or descendants: %q", got)
			}
		})
	}
}

func TestRenderReadableBlockTaskAndOpenDecisionState(t *testing.T) {
	t.Parallel()
	raw := `{"type":"doc","version":1,"content":[{"type":"taskList","attrs":{"localId":"list"},"content":[{"type":"blockTaskItem","attrs":{"localId":"item","state":"DONE"},"content":[{"type":"paragraph","content":[{"type":"text","text":"completed","marks":[{"type":"strong"}]},{"type":"hardBreak"},{"type":"text","text":"code","marks":[{"type":"code"}]}]}]}]},{"type":"decisionList","attrs":{"localId":"choices"},"content":[{"type":"decisionItem","attrs":{"localId":"choice","state":"FUTURE"},"content":[{"type":"text","text":"open state"}]}]}]}`
	if err := adf.Validate([]byte(raw)); err != nil {
		t.Fatal("invalid independent block fixture", err)
	}
	doc := mustParse(t, raw)
	got := adf.Render(doc, fakeStyler{}, 0)
	for _, want := range []string{"[x] **completed**", "`code`", "Decision (FUTURE)", "open state"} {
		if !strings.Contains(got, want) {
			t.Errorf("native state/marks lost %q: %q", want, got)
		}
	}
	after, err := doc.Marshal()
	if err != nil || string(after) != raw {
		t.Fatal("native readable state rewrote bytes")
	}
}

func TestRenderReadableLayoutWidthPresence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, attrs, want string }{
		{name: "explicit zero", attrs: `{"width":0}`, want: "Column (0%)"},
		{name: "missing", attrs: `{}`, want: "[layoutColumn —"},
		{name: "null", attrs: `{"width":null}`, want: "[layoutColumn —"},
		{name: "negative", attrs: `{"width":-1}`, want: "[layoutColumn —"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"type":"doc","version":1,"content":[{"type":"layoutColumn","attrs":` + tc.attrs + `,"content":[{"type":"paragraph","content":[{"type":"text","text":"retained"}]}]}]}`
			got := adf.Render(mustParse(t, raw), fakeStyler{}, 0)
			if !strings.Contains(got, tc.want) || !strings.Contains(got, "retained") {
				t.Fatal("layout width presence/content lost", got)
			}
		})
	}
}

func TestRenderReadableTaskWideGraphemesFitNarrowTerminal(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"TODO", "DONE"} {
		for _, prefixed := range []bool{false, true} {
			for _, width := range []int{5, 6, 7, 8} {
				if prefixed && width < 7 {
					continue
				}
				t.Run(state+"/"+strconv.Itoa(width)+"/prefixed="+strconv.FormatBool(prefixed), func(t *testing.T) {
					raw := `{"type":"doc","version":1,"content":[{"type":"taskList","attrs":{"localId":"list"},"content":[{"type":"taskItem","attrs":{"localId":"item","state":"` + state + `"},"content":[{"type":"text","text":"東京🙂"}]}]}]}`
					if prefixed {
						raw = `{"type":"doc","version":1,"content":[{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph"},` + strings.TrimSuffix(strings.TrimPrefix(raw, `{"type":"doc","version":1,"content":[`), `]}`) + `]}]}]}`
					}
					if err := adf.Validate([]byte(raw)); err != nil {
						t.Fatal("invalid independent wide-task fixture", err)
					}
					got := adf.Render(mustParse(t, raw), fakeStyler{}, width)
					if !strings.Contains(strings.Join(strings.Fields(got), ""), "東京🙂") {
						t.Fatal("narrow task lost content", got)
					}
					for _, line := range strings.Split(got, "\n") {
						if ansi.StringWidth(line) > width {
							t.Errorf("width %d overflow: %q", width, line)
						}
					}
				})
			}
		}
	}
}
