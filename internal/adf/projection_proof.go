package adf

// projectionBlockEqual proves the complete native block, including field
// presence and child structure, through the actual Markdown converter.
func projectionBlockEqual(n *Node, markdown string) bool {
	converted, warnings, err := FromMarkdownLossy(markdown)
	if err != nil || len(warnings) != 0 {
		return false
	}
	candidate, err := converted.Marshal()
	if err != nil {
		return false
	}
	block := NewDocument()
	block.SetContent([]Node{*n})
	source, err := block.Marshal()
	if err != nil {
		return false
	}
	equal, err := StructuralEqual(source, candidate)
	return err == nil && equal
}
