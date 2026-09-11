package adf

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/matcra587/jira-cli/internal/cli/adfmode"
)

// ValidateDoc validates a parsed ADF Document according to the given mode.
//
// Root shape (type="doc", version=1) is always enforced regardless of mode —
// a structurally invalid root cannot be sent to Jira in any mode.
//
// Validation is registry-backed: the rules in schema_rules.go encode the
// pinned ADF JSON schema (@atlaskit/adf-schema 56.0.15). In ModeStrict (the
// mutation-submit default) every rule violation is a fatal error naming the
// offending field path. In ModeBestEffort every violation is a non-fatal
// Warning and the document is forwarded as-is.
//
// Rules enforced:
//   - root shape and root-child node types
//   - unknown node / mark types
//   - required attrs, attr types, attr enums, numeric ranges
//   - content/nesting rules (inline-only nodes, child whitelists)
//   - marks-on-block-nodes are illegal (marks are inline-only)
//   - per-mark rules (link href, subsup type, color hex) and the
//     code-mark mutual-exclusion group
//
// Returns (warnings, nil) on success; (nil, err) on fatal validation failure.
func ValidateDoc(doc Document, mode adfmode.Mode) ([]Warning, error) {
	if doc.Type != "doc" {
		return nil, fmt.Errorf("ADF root type must be \"doc\", got %q", doc.Type)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("ADF version must be 1, got %d", doc.Version)
	}

	v := &validator{mode: mode, reg: Registry()}
	for i, node := range doc.Content {
		path := fmt.Sprintf("content/%d", i)
		// Document root permits only block-level node types. Unknown node
		// types are left to validateNode's unknown-node path.
		if node.Type != "" && knownNodeType(node.Type, v.reg) && !docRootNodeTypes[node.Type] {
			if err := v.fail(node.Type, "", path, fmt.Sprintf("node type %q is not permitted at the document root", node.Type)); err != nil {
				return nil, err
			}
		}
		if err := v.validateNode(node, path); err != nil {
			return nil, err
		}
	}
	return v.warnings, v.err
}

// validator carries mode + accumulated warnings through the recursive walk.
type validator struct {
	mode     adfmode.Mode
	reg      RegistryView
	warnings []Warning
	err      error
}

// fail records a violation. In strict mode it returns an error (callers
// abort the walk). In best-effort mode it records a Warning and returns
// nil so the walk continues.
func (v *validator) fail(nodeType, markType, path, msg string) error {
	full := fmt.Sprintf("ADF validation: %s at %s", msg, path)
	if v.mode == adfmode.ModeStrict {
		return fmt.Errorf("%s", full)
	}
	w := Warning{
		Type:     "adf_invalid_structure",
		Message:  full,
		Path:     path,
		NodeType: nodeType,
		MarkType: markType,
		Lossy:    false,
	}
	if markType != "" {
		w.Type = "adf_invalid_mark"
	}
	v.warnings = append(v.warnings, w)
	return nil
}

// knownNodeType reports whether a node type is recognized — either it has
// a registry Entry, a structural rule, or appears in the inline/root sets.
func knownNodeType(t string, reg RegistryView) bool {
	if _, ok := reg.Lookup(KindNode, t); ok {
		return true
	}
	if _, ok := nodeRules[t]; ok {
		return true
	}
	return inlineNodeTypes[t] || docRootNodeTypes[t]
}

// knownMarkType reports whether a mark type is recognized.
func knownMarkType(t string, reg RegistryView) bool {
	if _, ok := reg.Lookup(KindMark, t); ok {
		return true
	}
	_, ok := markRules[t]
	return ok
}

func (v *validator) validateNode(n Node, path string) error {
	nodePath := path + "/" + n.Type

	if !knownNodeType(n.Type, v.reg) {
		if v.mode == adfmode.ModeStrict {
			return fmt.Errorf("ADF validation: unsupported node type %q at %s", n.Type, path)
		}
		v.warnings = append(v.warnings, Warning{
			Type:     "unknown_adf_node",
			Message:  fmt.Sprintf("unsupported ADF node type %q will be forwarded opaquely", n.Type),
			Path:     path,
			NodeType: n.Type,
			Lossy:    false,
		})
		// Do not recurse into unknown nodes — they are opaque.
		return nil
	}

	// Block nodes may carry only the block-legal marks the schema
	// permits via the marks-constrained node variants (alignment,
	// indentation, border, breakout, fontSize). Every other mark is
	// inline-only and illegal on a block node.
	if !inlineNodeTypes[n.Type] {
		for _, m := range n.Marks {
			if blockLegalMarks[m.Type] {
				continue
			}
			if err := v.fail(n.Type, m.Type, nodePath, fmt.Sprintf("mark %q is not permitted on block node %q", m.Type, n.Type)); err != nil {
				return err
			}
		}
	}

	rule, hasRule := nodeRules[n.Type]
	if hasRule {
		if err := v.checkAttrs(n, rule, nodePath); err != nil {
			return err
		}
	}

	// Validate marks against the mark registry/rules.
	if err := v.validateMarks(n, nodePath); err != nil {
		return err
	}

	// Content / nesting rules.
	if hasRule {
		if err := v.checkContent(n, rule, nodePath); err != nil {
			return err
		}
	}

	// Recurse into children.
	for i, child := range n.Content {
		if err := v.validateNode(child, fmt.Sprintf("%s/content/%d", nodePath, i)); err != nil {
			return err
		}
	}
	return nil
}

// checkAttrs verifies required and optional attribute rules for a node.
func (v *validator) checkAttrs(n Node, rule nodeRule, nodePath string) error {
	for _, ar := range rule.requiredAttrs {
		raw, present := n.Attrs[ar.key]
		if !present {
			if err := v.fail(n.Type, "", nodePath, fmt.Sprintf("node %q is missing required attrs.%s", n.Type, ar.key)); err != nil {
				return err
			}
			continue
		}
		if err := v.checkAttrValue(n.Type, "", ar, raw, nodePath); err != nil {
			return err
		}
	}
	if err := v.checkAnyOfAttrs(n, rule, nodePath); err != nil {
		return err
	}
	for _, ar := range rule.optionalAttrs {
		raw, present := n.Attrs[ar.key]
		if !present {
			continue
		}
		if err := v.checkAttrValue(n.Type, "", ar, raw, nodePath); err != nil {
			return err
		}
	}
	return nil
}

// checkAnyOfAttrs enforces an attrs.anyOf rule: the node is valid if it
// satisfies every required attr of at least one branch. The node fails
// only if no branch matches — the reported error names the branch that
// got furthest (fewest missing keys) so the message is actionable.
func (v *validator) checkAnyOfAttrs(n Node, rule nodeRule, nodePath string) error {
	if len(rule.anyOfAttrs) == 0 {
		return nil
	}
	var bestMissing []string
	for _, branch := range rule.anyOfAttrs {
		var missing []string
		ok := true
		for _, ar := range branch {
			raw, present := n.Attrs[ar.key]
			if !present {
				missing = append(missing, ar.key)
				ok = false
				continue
			}
			// A present-but-wrong-typed/enum attr disqualifies the branch
			// without surfacing a per-attr error — another branch may match.
			if !attrValueValid(ar, raw) {
				ok = false
			}
		}
		if ok {
			return nil
		}
		if bestMissing == nil || (len(missing) > 0 && len(missing) < len(bestMissing)) {
			bestMissing = missing
		}
	}
	detail := "no attribute variant is satisfied"
	if len(bestMissing) > 0 {
		detail = "missing required attrs." + strings.Join(bestMissing, "/attrs.")
	}
	return v.fail(n.Type, "", nodePath, fmt.Sprintf("node %q %s", n.Type, detail))
}

// checkAttrValue verifies a single attribute value against its rule.
func (v *validator) checkAttrValue(nodeType, markType string, ar attrRule, raw any, path string) error {
	switch ar.typ {
	case attrString:
		s, ok := raw.(string)
		if !ok {
			return v.fail(nodeType, markType, path, fmt.Sprintf("attrs.%s must be a string", ar.key))
		}
		if ar.nonEmpty && s == "" {
			return v.fail(nodeType, markType, path, fmt.Sprintf("attrs.%s must not be empty", ar.key))
		}
		if len(ar.enum) > 0 && !inEnum(ar.enum, s) {
			return v.fail(nodeType, markType, path, fmt.Sprintf("attrs.%s value %q is not one of %s", ar.key, s, strings.Join(ar.enum, ", ")))
		}
		if ar.hexColor && !hexColorValid(s, ar.allowAlpha) {
			return v.fail(nodeType, markType, path, fmt.Sprintf("attrs.%s value %q is not a %s hex string", ar.key, s, hexColorShape(ar.allowAlpha)))
		}
	case attrNumber:
		f, ok := numericValue(raw)
		if !ok {
			return v.fail(nodeType, markType, path, fmt.Sprintf("attrs.%s must be a number", ar.key))
		}
		if ar.hasRange && (f < ar.min || f > ar.max) {
			return v.fail(nodeType, markType, path, fmt.Sprintf("attrs.%s value %v is out of range [%v, %v]", ar.key, f, ar.min, ar.max))
		}
	case attrBool:
		if _, ok := raw.(bool); !ok {
			return v.fail(nodeType, markType, path, fmt.Sprintf("attrs.%s must be a boolean", ar.key))
		}
	case attrAny:
		// No constraint.
	}
	return nil
}

// attrValueValid reports whether raw satisfies ar's type/enum/range/hex
// constraints without recording a failure — used by anyOf branch
// matching, where one failing branch must not abort the others.
func attrValueValid(ar attrRule, raw any) bool {
	switch ar.typ {
	case attrString:
		s, ok := raw.(string)
		if !ok {
			return false
		}
		if ar.nonEmpty && s == "" {
			return false
		}
		if len(ar.enum) > 0 && !inEnum(ar.enum, s) {
			return false
		}
		if ar.hexColor && !hexColorValid(s, ar.allowAlpha) {
			return false
		}
		return true
	case attrNumber:
		f, ok := numericValue(raw)
		if !ok {
			return false
		}
		if ar.hasRange && (f < ar.min || f > ar.max) {
			return false
		}
		return true
	case attrBool:
		_, ok := raw.(bool)
		return ok
	case attrAny:
		return true
	default:
		return true
	}
}

// validateMarks checks every mark on a node: known type, required attrs,
// hex-color patterns, and the code-mark mutual-exclusion group.
func (v *validator) validateMarks(n Node, nodePath string) error {
	hasCode := false
	for _, m := range n.Marks {
		if m.Type == "code" {
			hasCode = true
		}
	}
	for _, m := range n.Marks {
		if !knownMarkType(m.Type, v.reg) {
			if v.mode == adfmode.ModeStrict {
				return fmt.Errorf("ADF validation: unsupported mark type %q at %s", m.Type, nodePath)
			}
			v.warnings = append(v.warnings, Warning{
				Type:     "unknown_adf_mark",
				Message:  fmt.Sprintf("unsupported ADF mark type %q will be forwarded opaquely", m.Type),
				Path:     nodePath,
				NodeType: n.Type,
				MarkType: m.Type,
				Lossy:    false,
			})
			continue
		}
		// code is mutually exclusive with decorative text marks.
		if hasCode && codeExclusiveMarks[m.Type] {
			if err := v.fail(n.Type, m.Type, nodePath, fmt.Sprintf("mark %q cannot be combined with the code mark", m.Type)); err != nil {
				return err
			}
		}
		mr, ok := markRules[m.Type]
		if !ok {
			continue
		}
		// Position rule: a text-node-only mark (code) is illegal on any
		// other node type (mention, emoji, ...).
		if mr.textNodeOnly && n.Type != "text" {
			if err := v.fail(n.Type, m.Type, nodePath, fmt.Sprintf("mark %q is only permitted on text nodes, not %q", m.Type, n.Type)); err != nil {
				return err
			}
		}
		for _, ar := range mr.requiredAttrs {
			raw, present := m.Attrs[ar.key]
			if !present {
				if err := v.fail(n.Type, m.Type, nodePath, fmt.Sprintf("mark %q is missing required attrs.%s", m.Type, ar.key)); err != nil {
					return err
				}
				continue
			}
			if err := v.checkAttrValue(n.Type, m.Type, ar, raw, nodePath); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkContent verifies the content/nesting rule for a node.
func (v *validator) checkContent(n Node, rule nodeRule, nodePath string) error {
	// minItems: the schema requires at least this many child nodes. An
	// empty bulletList/panel/table/listItem is rejected by Jira.
	if rule.content.minItems > 0 && len(n.Content) < rule.content.minItems {
		if err := v.fail(n.Type, "", nodePath, fmt.Sprintf("node %q requires at least %d child node(s), has %d", n.Type, rule.content.minItems, len(n.Content))); err != nil {
			return err
		}
	}
	for i, child := range n.Content {
		childPath := fmt.Sprintf("%s/content/%d", nodePath, i)
		if rule.content.inlineOnly && child.Type != "" && !inlineNodeTypes[child.Type] {
			if err := v.fail(child.Type, "", childPath, fmt.Sprintf("block node %q is not permitted inside %q (inline content only)", child.Type, n.Type)); err != nil {
				return err
			}
		}
		if rule.content.allowed != nil && child.Type != "" && !rule.content.allowed[child.Type] {
			if err := v.fail(child.Type, "", childPath, fmt.Sprintf("node %q is not permitted inside %q", child.Type, n.Type)); err != nil {
				return err
			}
		}
		if rule.content.noMarks && len(child.Marks) > 0 {
			if err := v.fail(child.Type, "", childPath, fmt.Sprintf("content of %q must not carry marks", n.Type)); err != nil {
				return err
			}
		}
	}
	return nil
}

// numericValue extracts a float64 from a JSON-decoded number (json gives
// float64; typed callers may pass int).
func numericValue(raw any) (float64, bool) {
	switch x := raw.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	default:
		return 0, false
	}
}

func inEnum(set []string, v string) bool {
	return slices.Contains(set, v)
}

var (
	hexColorRe      = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	hexColorAlphaRe = regexp.MustCompile(`^#[0-9a-fA-F]{8}$|^#[0-9a-fA-F]{6}$`)
)

// hexColorValid reports whether s is a valid hex color. allowAlpha
// accepts the 8-hex (#RRGGBBAA) form in addition to 6-hex; without it
// only #RRGGBB is valid.
func hexColorValid(s string, allowAlpha bool) bool {
	if allowAlpha {
		return hexColorAlphaRe.MatchString(s)
	}
	return hexColorRe.MatchString(s)
}

// hexColorShape names the expected hex form for error messages.
func hexColorShape(allowAlpha bool) string {
	if allowAlpha {
		return "#RRGGBB or #RRGGBBAA"
	}
	return "#RRGGBB"
}

// Validate is the legacy mode-unaware validator (kept for backwards
// compatibility with existing callers). It uses strict mode.
//
// Deprecated: prefer ValidateDoc(doc, adfmode.ModeStrict).
func (d Document) Validate() error {
	_, err := ValidateDoc(d, adfmode.ModeStrict)
	return err
}
