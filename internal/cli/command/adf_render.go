package command

import (
	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/output"
)

// RenderADF shares the human document renderer across local review, issue
// descriptions and comments. Machine serializers retain their native contract.
func RenderADF(v *output.View, raw []byte, recovery string) string {
	return renderADF(raw, v.Output().Width(), adfStyler{view: v, recovery: recovery})
}

// RenderADFWidth renders a screen's document without creating a writer. Links
// remain readable text; all Jira-authored spans use the ordinary value sinks.
func RenderADFWidth(raw []byte, width int, recovery string) string {
	return renderADF(raw, width, adfStyler{recovery: recovery})
}

func renderADF(raw []byte, width int, styler adfStyler) string {
	doc, err := adf.ParseDocument(raw)
	if err != nil {
		return cli.Text("[unable to render — " + styler.recovery + "]")
	}
	return adf.Render(doc, styler, width)
}

type adfStyler struct {
	view     *output.View
	recovery string
}

func (s adfStyler) Text(str string) string     { return cli.Text(str) }
func (s adfStyler) Strong(str string) string   { return cli.MarkupStrong(str) }
func (s adfStyler) Emphasis(str string) string { return cli.MarkupEmphasis(str) }
func (s adfStyler) Code(str string) string     { return cli.MarkupCode(str) }
func (s adfStyler) Link(url, text string) string {
	if s.view != nil {
		return s.view.Output().Hyperlink(url, text)
	}
	return cli.Text(text) + " (" + cli.Text(url) + ")"
}
func (s adfStyler) Status(text, hue string) string { return cli.ADFStatus(text, hue) }

func (s adfStyler) UnsupportedNode(kind string) string {
	return cli.Text("[" + kind + " — " + s.recovery + "]")
}
