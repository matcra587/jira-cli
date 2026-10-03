package issue

import (
	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/output"
)

// bodyStyler implements adf.Styler over the shared human-output sinks
// (internal/cli/sinks.go) plus a View's own hyperlink support — the
// injection point that lets internal/adf's render walker stay pure (it
// must not import internal/cli or internal/output; see adf/doc.go and
// adf/render.go) while still drawing the same color, sanitization and
// OSC 8 link behavior every human surface in this package uses.
// clog.Output.Hyperlink already degrades to plain text when color is
// off, so this type does not need its own non-TTY branch for Link.
//
// Shared by every command that renders a Jira ADF document — issue
// view's description (renderDescription) and issue comment list's
// comment bodies (renderCommentList) — so a second renderer never has
// to reinvent this wiring.
type bodyStyler struct {
	view *output.View
}

func (s bodyStyler) Text(str string) string       { return cli.Text(str) }
func (s bodyStyler) Strong(str string) string     { return cli.MarkupStrong(str) }
func (s bodyStyler) Emphasis(str string) string   { return cli.MarkupEmphasis(str) }
func (s bodyStyler) Code(str string) string       { return cli.MarkupCode(str) }
func (s bodyStyler) Link(url, text string) string { return s.view.Output().Hyperlink(url, text) }

// renderBody parses raw as an ADF document and renders it against v's
// width through bodyStyler, returning the rendered text.
//
// A parse failure (malformed ADF Jira should never send, but the wire is
// not this package's to trust blindly) does not panic and does not drop
// the caller's content silently: it returns one placeholder line, the
// same posture adf.Render itself takes for a node type it cannot draw.
func renderBody(v *output.View, raw []byte) string {
	doc, err := adf.ParseDocument(raw)
	if err != nil {
		return cli.Text("[unable to render — see --output=json]")
	}
	return adf.Render(doc, bodyStyler{view: v}, v.Output().Width())
}
