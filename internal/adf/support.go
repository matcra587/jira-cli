package adf

import (
	"slices"

	xmaps "github.com/gechr/x/maps"
	"github.com/gechr/x/set"
)

// SupportCapabilities describes CLI operations, not Jira field acceptance.
// Author means Markdown can emit this definition; Render means a dedicated
// Markdown projection exists, including approximations. Edit is reserved for
// an actual lossless editing workflow, not parse/marshal preservation.
type SupportCapabilities struct {
	Author   bool `json:"author"`
	Render   bool `json:"render"`
	Preserve bool `json:"preserve"`
	Validate bool `json:"validate"`
	Submit   bool `json:"submit"`
	Edit     bool `json:"edit"`
}

// SupportEntry keeps distinct schema contexts visible. Bounds are structural
// summaries; full-document Validate owns enum values, recursion and context.
type SupportEntry struct {
	Kind               string              `json:"kind"`
	Name               string              `json:"name"`
	Definition         string              `json:"definition"`
	Variant            string              `json:"variant"`
	Capabilities       SupportCapabilities `json:"capabilities"`
	TerminalRender     bool                `json:"terminal_render"`
	MarkdownProjection string              `json:"markdown_projection"`
	NodeBounds         *NodeSpec           `json:"node_bounds,omitempty"`
	MarkBounds         *MarkSpec           `json:"mark_bounds,omitempty"`
}

// These emit sets are pinned against executed conversion of the complete
// authoring corpus. Native authoring reaches the entire schema separately.
var markdownAuthorNodes = set.New(
	NodeTypeDoc, NodeTypeParagraph, NodeTypeText, NodeTypeHeading,
	NodeTypeBulletList, NodeTypeOrderedList, NodeTypeListItem,
	NodeTypeCodeBlock, NodeTypeBlockquote, NodeTypeHardBreak, NodeTypeRule,
	NodeTypeTable, NodeTypeTableRow, NodeTypeTableCell, NodeTypeTableHeader,
	NodeTypeTaskList, NodeTypeTaskItem,
)

var markdownAuthorMarks = set.New(MarkTypeStrong, MarkTypeEm, MarkTypeStrike, MarkTypeCode, MarkTypeLink)

// SupportMatrix derives its universe from the generated pinned catalog. Every
// call owns its nested slices/pointers so consumers cannot mutate that catalog.
func SupportMatrix() []SupportEntry {
	rows := make([]SupportEntry, 0, len(NodeSpecs)+len(MarkSpecs))
	for definition, spec := range xmaps.Sorted(NodeSpecs) {
		projection := nodeProjectionSupport(spec.Type)
		rows = append(rows, SupportEntry{
			Kind: "node", Name: string(spec.Type), Definition: definition, Variant: spec.Variant,
			Capabilities: SupportCapabilities{
				Author: markdownAuthorNodes.Contains(spec.Type), Render: projection != "placeholder",
				Preserve: true, Validate: true, Submit: true,
			},
			TerminalRender: terminalNodeSupport(spec.Type), MarkdownProjection: projection,
			NodeBounds: cloneNodeSpec(spec),
		})
	}
	for definition, spec := range xmaps.Sorted(MarkSpecs) {
		render := markdownAuthorMarks.Contains(spec.Type)
		projection := "dropped"
		if render {
			projection = "formatting"
		}
		rows = append(rows, SupportEntry{
			Kind: "mark", Name: string(spec.Type), Definition: definition,
			Capabilities:       SupportCapabilities{Author: render, Render: render, Preserve: true, Validate: true, Submit: true},
			TerminalRender:     spec.Type == MarkTypeStrong || spec.Type == MarkTypeEm || spec.Type == MarkTypeCode || spec.Type == MarkTypeLink,
			MarkdownProjection: projection,
			MarkBounds:         &MarkSpec{Name: spec.Name, Type: spec.Type, Attrs: cloneAttrsSpec(spec.Attrs)},
		})
	}
	return rows
}

func nodeProjectionSupport(kind NodeType) string {
	switch kind {
	case NodeTypeDoc, NodeTypeParagraph, NodeTypeText, NodeTypeHeading,
		NodeTypeBulletList, NodeTypeOrderedList, NodeTypeListItem,
		NodeTypeCodeBlock, NodeTypeBlockquote, NodeTypeHardBreak, NodeTypeRule:
		return "structural"
	case NodeTypePanel, NodeTypeMention, NodeTypeEmoji, NodeTypeStatus, NodeTypeInlineCard,
		NodeTypeTaskList, NodeTypeTaskItem, NodeTypeBlockTaskItem, NodeTypeDecisionList, NodeTypeDecisionItem,
		NodeTypeTable, NodeTypeTableRow, NodeTypeTableCell, NodeTypeTableHeader,
		NodeTypeMedia, NodeTypeMediaGroup, NodeTypeMediaSingle:
		return "approximate"
	default:
		return "placeholder"
	}
}

func terminalNodeSupport(kind NodeType) bool {
	if _, ok := blockDispatch[kind]; ok {
		return true
	}
	// The root, list items and inline nodes are walked by their containing
	// document/list/paragraph rather than the block dispatcher.
	return kind == NodeTypeDoc || kind == NodeTypeListItem || kind == NodeTypeText || kind == NodeTypeHardBreak
}

func cloneAttrsSpec(spec *AttrsSpec) *AttrsSpec {
	if spec == nil {
		return nil
	}
	return &AttrsSpec{Required: slices.Clone(spec.Required), Allowed: slices.Clone(spec.Allowed), Open: spec.Open}
}

func cloneNodeSpec(spec *NodeSpec) *NodeSpec {
	cloned := *spec
	cloned.Required = slices.Clone(spec.Required)
	cloned.Attrs = cloneAttrsSpec(spec.Attrs)
	if spec.Marks != nil {
		cloned.Marks = &MarksSpec{Max: spec.Marks.Max, Allowed: slices.Clone(spec.Marks.Allowed)}
	}
	if spec.Content != nil {
		cloned.Content = &ContentSpec{MinItems: spec.Content.MinItems, MaxItems: spec.Content.MaxItems, Refs: slices.Clone(spec.Content.Refs)}
	}
	return &cloned
}
