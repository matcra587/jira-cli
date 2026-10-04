package adf

import (
	"encoding/json"
	"maps"

	"github.com/gechr/x/set"
)

// panel uses the converter's top-level GitHub alert dialect. The complete
// native subtree must survive conversion before its projection is loss-free;
// readable alert syntax alone cannot carry IDs, extra attrs or unknown state.
func (p *markdownProjection) panel(n *Node, attrs map[string]json.RawMessage, topLevel bool) string {
	children := markdownProjection{losses: set.New[string]()}
	body := children.blocks(n.Content(), false)
	marker := ""
	if topLevel {
		if panel, err := AsPanel(n); err == nil {
			switch panel.Attrs.PanelType {
			case "info":
				marker = "NOTE"
			case "tip":
				marker = "TIP"
			case "note":
				marker = "IMPORTANT"
			case "warning":
				marker = "WARNING"
			case "error":
				marker = "CAUTION"
			}
		}
	}
	if marker != "" {
		markdown := quoteMarkdown("[!" + marker + "]\n\n" + body)
		if projectionBlockEqual(n, markdown) {
			return markdown
		}
	}
	p.losses.Add(n.Type())
	maps.Copy(p.losses, children.losses)
	return quoteMarkdown("**" + escapeMarkdown(projectionAttr(attrs, "panelType")) + "**\n\n" + body)
}
