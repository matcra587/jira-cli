package adf

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	xmaps "github.com/gechr/x/maps"
)

// ErrUnknownNodeType reports a node type the generated catalog has no view
// for. Callers fall back to the substrate — the node is preserved, just not
// typed.
var ErrUnknownNodeType = errors.New("adf: unknown node type")

// ErrUnknownMarkType is ErrUnknownNodeType for marks.
var ErrUnknownMarkType = errors.New("adf: unknown mark type")

// TypedNode is the discriminated union over the generated node views:
// Typed returns one, and a type switch over the concrete view types
// (*Paragraph, *Panel, …) recovers the specific node. Sealed — only
// generated views implement it, so the switch's case set is the complete
// catalog.
type TypedNode interface {
	// Node returns the underlying substrate node.
	Node() *Node
	// NodeType returns the view's runtime type.
	NodeType() NodeType

	typedNode()
}

// TypedMark is TypedNode for marks.
type TypedMark interface {
	// Mark returns the underlying substrate mark.
	Mark() *Mark
	// MarkType returns the view's runtime type.
	MarkType() MarkType

	typedMark()
}

// Matches reports whether n structurally satisfies this definition:
// required keys present, marks within the allowed set and count, attr keys
// within bounds, children of permitted types. It checks one level deep —
// Validate is the authority for full-tree conformance; Matches exists to
// discriminate variants and validate context cheaply.
func (s *NodeSpec) Matches(n *Node) bool {
	if n.Type() != string(s.Type) {
		return false
	}
	for _, key := range s.Required {
		if !n.hasField(key) {
			return false
		}
	}
	if !s.marksMatch(n) || !s.attrsMatch(n) {
		return false
	}
	return s.contentMatches(n)
}

func (s *NodeSpec) marksMatch(n *Node) bool {
	marks := n.Marks()
	if len(marks) == 0 {
		return true
	}
	if s.Marks == nil {
		// Marks undeclared on a closed definition means marks forbidden.
		return s.Open
	}
	if s.Marks.Max >= 0 && len(marks) > s.Marks.Max {
		return false
	}
	if s.Marks.Allowed == nil {
		return true
	}
	for i := range marks {
		if !slices.Contains(s.Marks.Allowed, MarkType(marks[i].Type())) {
			return false
		}
	}
	return true
}

func (s *NodeSpec) attrsMatch(n *Node) bool {
	raw := n.rawAttrs()
	if raw == nil {
		// Absence is legal unless "attrs" is required, checked via Required.
		return true
	}
	if s.Attrs == nil {
		return s.Open
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return false
	}
	for _, req := range s.Attrs.Required {
		if _, ok := keys[req]; !ok {
			return false
		}
	}
	if s.Attrs.Open {
		return true
	}
	for key := range keys {
		if !slices.Contains(s.Attrs.Allowed, key) {
			return false
		}
	}
	return true
}

func (s *NodeSpec) contentMatches(n *Node) bool {
	content := n.Content()
	if len(content) == 0 {
		// An empty or absent content array can only violate minItems.
		return s.Content == nil || s.Content.MinItems <= 0
	}
	if s.Content == nil {
		return s.Open
	}
	if len(content) < s.Content.MinItems {
		return false
	}
	if s.Content.MaxItems >= 0 && len(content) > s.Content.MaxItems {
		return false
	}
	// Definition-level, not type-level: a context referencing
	// paragraph_with_no_marks_node must reject a marked paragraph, and
	// collapsing refs to runtime types would let it through because both
	// spell "paragraph".
	allowed := expandRefs(s.Content.Refs)
	for i := range content {
		if !childSatisfiesAny(&content[i], allowed) {
			return false
		}
	}
	return true
}

// childSatisfiesAny reports whether the child matches at least one of the
// named definitions. An unknown definition name cannot judge anything, so
// it is skipped rather than treated as a rejection.
func childSatisfiesAny(child *Node, definitions []string) bool {
	known := false
	for _, name := range definitions {
		spec, ok := NodeSpecs[name]
		if !ok {
			continue
		}
		known = true
		if spec.Matches(child) {
			return true
		}
	}
	// No definition in the list is one this build knows: fall back to the
	// substrate's posture — preserve, do not reject what we cannot judge.
	return !known
}

// ClassifyIn returns the definitions among the named content group that n
// satisfies. An empty result means n is not valid in that context; more
// than one match is normal (an unmarked paragraph satisfies every
// paragraph variant), and callers wanting a single answer take the first —
// the generated order follows the schema's own anyOf order.
func ClassifyIn(n *Node, group string) []*NodeSpec {
	var matched []*NodeSpec
	for _, name := range expandRefs([]string{group}) {
		spec, ok := NodeSpecs[name]
		if !ok {
			continue
		}
		if spec.Matches(n) {
			matched = append(matched, spec)
		}
	}
	return matched
}

// Variants returns every definition for a runtime type, primary first.
func Variants(t NodeType) []*NodeSpec {
	var specs []*NodeSpec
	for _, spec := range xmaps.Sorted(NodeSpecs) {
		if spec.Type == t {
			specs = append(specs, spec)
		}
	}
	// The primary definition's empty Variant sorts before every label.
	// Stable, so equal Variants keep the deterministic key order the
	// sorted iteration above just established.
	slices.SortStableFunc(specs, func(a, b *NodeSpec) int {
		return strings.Compare(a.Variant, b.Variant)
	})
	return specs
}

// expandRefs resolves a mixed list of definition and group names to
// concrete definition names, depth-first, preserving the schema's order.
func expandRefs(refs []string) []string {
	var out []string
	seen := map[string]bool{}
	var walk func([]string)
	walk = func(names []string) {
		for _, name := range names {
			if seen[name] {
				continue
			}
			seen[name] = true
			if members, ok := Groups[name]; ok {
				walk(members)
				continue
			}
			out = append(out, name)
		}
	}
	walk(refs)
	return out
}
