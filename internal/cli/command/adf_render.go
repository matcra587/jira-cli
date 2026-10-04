package command

import (
	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/output"
)

// RenderADF shares the human document renderer across local review, issue
// descriptions and comments. Machine serializers retain their native contract.
func RenderADF(v *output.View, raw []byte, recovery string) string {
	doc, err := adf.ParseDocument(raw)
	if err != nil {
		return cli.Text("[unable to render — " + recovery + "]")
	}
	return adf.Render(doc, adfStyler{view: v, recovery: recovery}, v.Output().Width())
}

type adfStyler struct {
	view     *output.View
	recovery string
}

func (s adfStyler) Text(str string) string         { return cli.Text(str) }
func (s adfStyler) Strong(str string) string       { return cli.MarkupStrong(str) }
func (s adfStyler) Emphasis(str string) string     { return cli.MarkupEmphasis(str) }
func (s adfStyler) Code(str string) string         { return cli.MarkupCode(str) }
func (s adfStyler) Link(url, text string) string   { return s.view.Output().Hyperlink(url, text) }
func (s adfStyler) Status(text, hue string) string { return cli.ADFStatus(text, hue) }

func (s adfStyler) UnsupportedNode(kind string) string {
	return cli.Text("[" + kind + " — " + s.recovery + "]")
}
