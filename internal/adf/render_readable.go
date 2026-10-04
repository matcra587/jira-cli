package adf

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gechr/x/ansi"
)

func (w *walker) renderMention(n *Node) string {
	v, err := AsMention(n)
	if err != nil {
		return w.placeholder(n)
	}
	if label := w.styler.Text(v.Attrs.Text); label != "" {
		return label
	}
	if id := w.styler.Text(v.Attrs.ID); id != "" {
		return "@" + id
	}
	return w.placeholder(n)
}

func (w *walker) renderEmoji(n *Node) string {
	v, err := AsEmoji(n)
	if err != nil {
		return w.placeholder(n)
	}
	if text := w.styler.Text(v.Attrs.Text); text != "" {
		return text
	}
	if name := w.styler.Text(v.Attrs.ShortName); name != "" {
		return name
	}
	return w.placeholder(n)
}

func (w *walker) renderExpand(n *Node, prefix string) {
	var title string
	var err error
	if NodeType(n.Type()) == NodeTypeNestedExpand {
		var v *NestedExpand
		v, err = AsNestedExpand(n)
		if err == nil {
			title = v.Attrs.Title
		}
	} else {
		var v *Expand
		v, err = AsExpand(n)
		if err == nil {
			title = v.Attrs.Title
		}
	}
	if err != nil {
		w.renderPlaceholder(n, prefix)
	} else {
		title = w.styler.Text(title)
		if title == "" {
			title = "Expand"
		}
		w.writeBlock(prefix, prefix, []string{title})
	}
	w.renderReadableChildren(n.Content(), prefix)
}

// Terminal layouts are sequential labeled columns. They retain authored order
// and width metadata without shrinking or dropping a column to fit the screen.
func (w *walker) renderLayoutSection(n *Node, prefix string) {
	if _, err := AsLayoutSection(n); err != nil {
		w.renderPlaceholder(n, prefix)
	} else {
		w.writeBlock(prefix, prefix, []string{"Layout"})
	}
	children := n.Content()
	for i := range children {
		if NodeType(children[i].Type()) == NodeTypeLayoutColumn {
			w.renderColumn(&children[i], prefix, "Column "+strconv.Itoa(i+1))
		} else {
			w.renderReadableChildren(children[i:i+1], prefix)
		}
	}
}

func (w *walker) renderLayoutColumn(n *Node, prefix string) {
	w.renderColumn(n, prefix, "Column")
}

func (w *walker) renderColumn(n *Node, prefix, label string) {
	v, err := AsLayoutColumn(n)
	// The generated required Width is a value. Check presence separately so a
	// missing/null attr is not mislabeled as the valid authored width zero.
	var presence struct {
		Width *float64 `json:"width"`
	}
	if err != nil || json.Unmarshal(n.Attrs(), &presence) != nil || presence.Width == nil || v.Attrs.Width < 0 || v.Attrs.Width > 100 {
		w.renderPlaceholder(n, prefix)
	} else {
		label += " (" + strconv.FormatFloat(v.Attrs.Width, 'f', -1, 64) + "%"
		if valign := w.styler.Text(v.Attrs.Valign); valign != "" {
			label += ", " + valign
		}
		w.writeBlock(prefix, prefix, []string{label + ")"})
	}
	w.renderReadableChildren(n.Content(), prefix)
}

func (w *walker) renderReadableList(n *Node, prefix string) {
	var err error
	if NodeType(n.Type()) == NodeTypeTaskList {
		_, err = AsTaskList(n)
	} else {
		_, err = AsDecisionList(n)
	}
	if err != nil {
		w.renderPlaceholder(n, prefix)
	}
	if len(n.Content()) == 0 {
		if err == nil {
			w.renderPlaceholder(n, prefix)
		}
		return
	}
	w.renderReadableChildren(n.Content(), prefix)
}

func (w *walker) renderTaskItem(n *Node, prefix string) {
	var state string
	var err error
	if NodeType(n.Type()) == NodeTypeBlockTaskItem {
		var v *BlockTaskItem
		v, err = AsBlockTaskItem(n)
		if err == nil {
			state = v.Attrs.State
		}
	} else {
		var v *TaskItem
		v, err = AsTaskItem(n)
		if err == nil {
			state = v.Attrs.State
		}
	}
	marker := "[ ] "
	if state == "DONE" {
		marker = "[x] "
	} else if err != nil || state != "TODO" {
		w.renderPlaceholder(n, prefix)
		w.renderReadableChildren(n.Content(), prefix)
		return
	}
	cont := prefix + strings.Repeat(" ", ansi.StringWidth(marker))
	sub := &walker{styler: w.styler, width: w.width}
	sub.renderReadableChildren(n.Content(), cont)
	separate := w.width > 0 && w.width <= ansi.StringWidth(cont)
	if w.width > 0 {
		// Wrapping preserves indivisible graphemes. A marker can leave less
		// room than a wide grapheme needs, so reclaim its indent for the body.
		for _, line := range strings.Split(sub.buf.String(), "\n") {
			if ansi.StringWidth(line) > w.width {
				separate = true
				break
			}
		}
	}
	if separate {
		w.writeBlock(prefix, prefix, []string{strings.TrimSpace(marker)})
		w.renderReadableChildren(n.Content(), prefix)
		return
	}
	w.buf.WriteString(overlayMarker(sub.buf.String(), cont, prefix+marker))
}

func (w *walker) renderDecisionItem(n *Node, prefix string) {
	v, err := AsDecisionItem(n)
	state := ""
	if err == nil {
		state = w.styler.Text(v.Attrs.State)
	}
	if state == "" {
		w.renderPlaceholder(n, prefix)
	} else {
		// The stable schema leaves decision state open; expose future state
		// text rather than inventing completion semantics for an unknown value.
		w.writeBlock(prefix, prefix, []string{"Decision (" + state + ")"})
	}
	w.renderReadableChildren(n.Content(), prefix)
}

// A known container may carry malformed children or newly deployed nodes.
// Retain their identity and descendant text instead of silently losing the body.
// Consecutive inline leaves share one run, preserving their original spacing.
func (w *walker) renderReadableChildren(nodes []Node, prefix string) {
	for i := 0; i < len(nodes); {
		if i > 0 {
			w.buf.WriteByte('\n')
		}
		if readableInline(NodeType(nodes[i].Type())) {
			end := i + 1
			for end < len(nodes) && readableInline(NodeType(nodes[end].Type())) {
				end++
			}
			w.writeBlock(prefix, prefix, w.inlineSegments(nodes[i:end]))
			i = end
			continue
		}
		n := &nodes[i]
		w.block(n, prefix)
		if _, known := blockDispatch[NodeType(n.Type())]; !known {
			if n.Text() != "" {
				w.writeBlock(prefix, prefix, []string{w.styler.Text(n.Text())})
			}
			w.renderReadableChildren(n.Content(), prefix)
		}
		i++
	}
}

func readableInline(kind NodeType) bool {
	switch kind {
	case NodeTypeText, NodeTypeHardBreak, NodeTypeStatus, NodeTypeMention, NodeTypeEmoji:
		return true
	default:
		return false
	}
}
