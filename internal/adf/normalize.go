package adf

import "fmt"

// Normalize repairs structurally-invalid but losslessly-fixable constructs that
// Jira's ADF validator rejects outright — an opaque INVALID_INPUT on the whole
// document — even though they carry no renderable content. It edits d in place
// and returns one non-lossy Warning per class of repair, and is meant to run
// just before Validate so the submitted document is both valid and minimal.
// Every repair is provably lossless: it changes nothing the user can see, only
// "rejected by Jira" into "accepted", so it runs in every mode.
//
// It mutates rather than returning a copy because the substrate's byte
// preservation lives in the node itself: a "copy" would either alias the
// nodes anyway or force a parse round-trip that discards the raw fields the
// contract rests on. Untouched subtrees keep their original bytes.
//
// Repairs:
//
//   - Empty text nodes. A text node's `text` must be a non-empty string; an
//     empty (or absent) one is rejected by Jira yet renders as nothing. These
//     are a common artifact of document generators that emit blank table
//     cells. Removing them is lossless — the node carried no content — and a
//     parent left with no inline children (e.g. an empty paragraph in a blank
//     cell) is itself valid ADF.
func Normalize(d *Document) []Warning {
	cleaned, removed := pruneEmptyText(d.Content())
	if removed == 0 {
		return nil
	}
	d.SetContent(cleaned)
	return []Warning{{
		Type:    "adf_normalized",
		Message: fmt.Sprintf("removed %d empty text node(s): a text node must carry non-empty text or Jira rejects the document", removed),
		Lossy:   false,
	}}
}

// pruneEmptyText returns nodes with empty text nodes removed, recursing into
// every node's content. A text node is "empty" when its text is the empty
// string — which also covers a node that omitted the `text` field entirely,
// since the substrate decodes the absence to "". Surviving nodes are edited
// in place only when a descendant was actually removed, so an untouched
// subtree still re-emits its original bytes.
func pruneEmptyText(nodes []Node) ([]Node, int) {
	if len(nodes) == 0 {
		return nodes, 0
	}
	out := make([]Node, 0, len(nodes))
	removed := 0
	for i := range nodes {
		n := &nodes[i]
		if n.Type() == string(NodeTypeText) && n.Text() == "" {
			removed++
			continue
		}
		if children := n.Content(); len(children) > 0 {
			cleaned, c := pruneEmptyText(children)
			if c > 0 {
				n.SetContent(cleaned)
				removed += c
			}
		}
		out = append(out, *n)
	}
	return out, removed
}
