package adf

import (
	"fmt"

	"github.com/gechr/x/ansi"
)

// StatusStyler is an optional semantic extension of Styler. Both arguments
// are sanitized before delivery; the implementation owns terminal color policy.
type StatusStyler interface {
	Status(text, color string) string
}

func (w *walker) status(label, hue string) string {
	label, hue = w.styler.Text(label), w.styler.Text(hue)
	if s, ok := w.styler.(StatusStyler); ok {
		return s.Status(label, hue)
	}
	return "[" + label + "]"
}

func (w *walker) renderStatus(n *Node) string {
	view, err := AsStatus(n)
	if err != nil || view.Attrs.Text == "" {
		return w.placeholder(n)
	}
	return w.status(view.Attrs.Text, view.Attrs.Color)
}

func (w *walker) renderPanel(n *Node, prefix string) {
	view, err := AsPanel(n)
	if err != nil {
		w.renderPlaceholder(n, prefix)
		w.blocks(n.Content(), prefix)
		return
	}
	label, hue := panelLabel(view.Attrs.PanelType)
	if view.Attrs.PanelType == "custom" && view.Attrs.PanelColor != "" {
		hue = view.Attrs.PanelColor
	}
	// Very narrow terminals keep the content, without spending its width on a frame.
	if w.width > 0 && w.width-ansi.StringWidth(prefix) < 4 {
		w.writeBlock(prefix, prefix, []string{w.status(label, hue)})
		w.blocks(n.Content(), prefix)
		return
	}
	w.writeBlock(prefix+"┌─ ", prefix+"│  ", []string{w.status(label, hue)})
	w.blocks(n.Content(), prefix+"│ ")
	w.writeLine(prefix, "└─")
}

func panelLabel(kind string) (string, string) {
	switch kind {
	case "info":
		return "Info", "blue"
	case "note":
		return "Note", "purple"
	case "tip":
		return "Tip", "green"
	case "warning":
		return "Warning", "yellow"
	case "error":
		return "Error", "red"
	case "success":
		return "Success", "green"
	case "custom":
		return "Custom", "neutral"
	default:
		return fmt.Sprintf("Panel (%s)", kind), "neutral"
	}
}

// PlaceholderStyler supplies recovery guidance appropriate to the caller's
// output contract. The node type is sanitized before reaching this extension.
type PlaceholderStyler interface{ UnsupportedNode(kind string) string }
