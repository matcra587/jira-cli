package adf_test

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// This file pins the low-coverage branches of attrsMatch, contentMatches,
// and marksMatch in classify.go — the "does a raw node match this
// definition" logic that ClassifyIn/Matches build on. Each case names the
// spec it exercises so a schema regen that changes that definition's shape
// makes the failure obvious.

func TestMatchesAttrsEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		spec string
		in   string
		want bool
	}{
		{
			name: "attrs present but not a JSON object",
			spec: "panel_node",
			in:   `{"type":"panel","attrs":"weird","content":[]}`,
			want: false,
		},
		{
			name: "no Attrs spec declared and attrs key present on a closed definition",
			spec: "mediaGroup_node",
			in:   `{"type":"mediaGroup","attrs":{"foo":"bar"},"content":[{"type":"media","attrs":{"type":"file","id":"1","collection":"c"}}]}`,
			want: false,
		},
		{
			name: "no Attrs spec declared and no attrs key present",
			spec: "mediaGroup_node",
			in:   `{"type":"mediaGroup","content":[{"type":"media","attrs":{"type":"file","id":"1","collection":"c"}}]}`,
			want: true,
		},
		{
			name: "unknown attr key rejected on a closed attrs definition",
			spec: "panel_node",
			in:   `{"type":"panel","attrs":{"panelType":"info","bogus":"x"},"content":[]}`,
			want: false,
		},
		{
			name: "required attr key missing",
			spec: "panel_node",
			in:   `{"type":"panel","attrs":{},"content":[]}`,
			want: false,
		},
		{
			name: "unknown attr key tolerated on an open attrs definition",
			// mediaSingle_caption_node's NodeSpec.Open is true, but that
			// governs undeclared *content*, not attrs — its AttrsSpec.Open
			// is false, so an unknown attr key is still rejected. Pins the
			// two Open flags as independent, not a shared "anything goes".
			spec: "mediaSingle_caption_node",
			in:   `{"type":"mediaSingle","attrs":{"layout":"center","bogus":"x"},"content":[{"type":"media","attrs":{"type":"file","id":"1","collection":"c"}},{"type":"caption","content":[]}]}`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, ok := adf.NodeSpecs[tt.spec]
			if !ok {
				t.Fatalf("no such spec %s", tt.spec)
			}
			n := parseNode(t, tt.in)
			if got := spec.Matches(n); got != tt.want {
				t.Errorf("%s.Matches(%s) = %v, want %v", tt.spec, tt.in, got, tt.want)
			}
		})
	}
}

// TestMatchesAttrsOpenSpecAcceptsUnknownKeys pins the AttrsSpec.Open == true
// branch directly: as of this writing no generated definition in the
// vendored schema sets it (every real Open:true definition in nodes_gen.go
// is at the NodeSpec level, gating content, not attrs — see the case above),
// so this branch is currently unreachable through any real spec. A
// synthetic spec exercises it so a future refactor of attrsMatch can't
// silently break it unnoticed. Flagged for Matt: dead code by the current
// schema, not by a missing test.
func TestMatchesAttrsOpenSpecAcceptsUnknownKeys(t *testing.T) {
	spec := &adf.NodeSpec{
		Name:  "synthetic_open_attrs",
		Type:  "synthetic",
		Attrs: &adf.AttrsSpec{Open: true},
	}
	n := parseNode(t, `{"type":"synthetic","attrs":{"anything":1}}`)
	if !spec.Matches(n) {
		t.Errorf("open-attrs synthetic spec rejected an unknown attr key")
	}
}

func TestMatchesMarksEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		spec string
		in   string
		want bool
	}{
		{
			name: "no Marks spec declared and marks present on a closed definition",
			spec: "mediaGroup_node",
			in:   `{"type":"mediaGroup","marks":[{"type":"strong"}],"content":[{"type":"media","attrs":{"type":"file","id":"1","collection":"c"}}]}`,
			want: false,
		},
		{
			name: "Max:0 forbids marks even though a Marks spec is declared",
			spec: "codeBlock_node",
			in:   `{"type":"codeBlock","marks":[{"type":"strong"}]}`,
			want: false,
		},
		{
			name: "Max:-1 with Allowed nil admits any mark type up to no bound",
			spec: "bodiedExtension_node",
			in:   `{"type":"bodiedExtension","attrs":{"extensionKey":"k","extensionType":"t"},"marks":[{"type":"strong"},{"type":"em"}],"content":[{"type":"paragraph","content":[]}]}`,
			want: true,
		},
		{
			name: "mark type outside the Allowed set rejected",
			spec: "code_inline_node",
			in:   `{"type":"text","text":"x","marks":[{"type":"strong"}]}`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, ok := adf.NodeSpecs[tt.spec]
			if !ok {
				t.Fatalf("no such spec %s", tt.spec)
			}
			n := parseNode(t, tt.in)
			if got := spec.Matches(n); got != tt.want {
				t.Errorf("%s.Matches(%s) = %v, want %v", tt.spec, tt.in, got, tt.want)
			}
		})
	}
}

func TestMatchesContentEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		spec string
		in   string
		want bool
	}{
		{
			name: "no Content spec declared and content present on a closed definition",
			spec: "media_node",
			in:   `{"type":"media","attrs":{"type":"file","id":"1","collection":"c"},"content":[{"type":"text","text":"x"}]}`,
			want: false,
		},
		{
			name: "MinItems violated by empty content",
			spec: "blockquote_node",
			in:   `{"type":"blockquote","content":[]}`,
			want: false,
		},
		{
			name: "MinItems satisfied at exactly the boundary",
			spec: "blockquote_node",
			in:   `{"type":"blockquote","content":[{"type":"paragraph","content":[]}]}`,
			want: true,
		},
		{
			name: "MaxItems violated by one too many children",
			spec: "layoutSection_full_node",
			in: `{"type":"layoutSection","content":[` +
				layoutColumnJSON + `,` + layoutColumnJSON + `,` + layoutColumnJSON + `,` + layoutColumnJSON + `]}`,
			want: false,
		},
		{
			name: "MaxItems satisfied at exactly the boundary",
			spec: "layoutSection_full_node",
			in: `{"type":"layoutSection","content":[` +
				layoutColumnJSON + `,` + layoutColumnJSON + `,` + layoutColumnJSON + `]}`,
			want: true,
		},
		{
			name: "disallowed child type rejected even though min/max are satisfied",
			spec: "blockquote_node",
			in:   `{"type":"blockquote","content":[{"type":"panel","attrs":{"panelType":"info"},"content":[]}]}`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, ok := adf.NodeSpecs[tt.spec]
			if !ok {
				t.Fatalf("no such spec %s", tt.spec)
			}
			n := parseNode(t, tt.in)
			if got := spec.Matches(n); got != tt.want {
				t.Errorf("%s.Matches(%s) = %v, want %v", tt.spec, tt.in, got, tt.want)
			}
		})
	}
}

const layoutColumnJSON = `{"type":"layoutColumn","attrs":{"width":50},"content":[{"type":"paragraph","content":[]}]}`
