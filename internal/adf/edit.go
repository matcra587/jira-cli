package adf

import (
	"crypto/sha256"
	"fmt"
	"strings"

	xslices "github.com/gechr/x/slices"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// EditError describes rejected editing input without requiring callers to
// interpret error text. No partial candidate is returned on failure.
type EditError struct {
	Kind   EditErrorKind
	Region string
	Reason string
}

// EditErrorKind identifies the boundary that refused an edit.
type EditErrorKind string

const (
	EditInvalidSource     EditErrorKind = "invalid_source"
	EditInvalidAnchor     EditErrorKind = "invalid_anchor"
	EditProtectedRegion   EditErrorKind = "protected_region"
	EditLossyContent      EditErrorKind = "lossy_content"
	EditMergeConflict     EditErrorKind = "merge_conflict"
	EditAmbiguousIdentity EditErrorKind = "ambiguous_identity"
)

func (e *EditError) Error() string {
	if e.Region != "" {
		return fmt.Sprintf("adf: edit %s region %q: %s", e.Kind, e.Region, e.Reason)
	}
	return fmt.Sprintf("adf: edit %s: %s", e.Kind, e.Reason)
}

// EditRegion identifies one anchored top-level subtree. Protected regions
// retain all native state and can be moved, but cannot be edited or removed.
type EditRegion struct {
	ID        string
	NodeType  string
	Protected bool
}

type editRegion struct {
	EditRegion
	markdown string
}

// EditSession owns an immutable snapshot and its anchored Markdown. Apply
// supports insert/delete/reorder against that snapshot; Merge reconciles a
// current document without establishing a remote write precondition. Each call
// returns independent nodes, so editing a result cannot corrupt the session.
type EditSession struct {
	source   []byte
	markdown string
	regions  []editRegion
}

// PrepareEdit preserves every source subtree. A block is editable only when
// projection and conversion are loss-free and structurally equal to its source.
// Checking structure also catches empty attrs/marks and future state that a
// projection's loss inventory might overlook. Unknown server nodes pass through
// without validation; only newly authored Markdown is schema-validated by Apply.
func PrepareEdit(doc *Document) (*EditSession, error) {
	if doc == nil {
		return nil, &EditError{Kind: EditInvalidSource, Reason: "document is nil"}
	}
	source, err := doc.Marshal()
	if err != nil {
		return nil, fmt.Errorf("adf: snapshot editing source: %w", err)
	}
	if _, err := canonicalStructure(source); err != nil {
		return nil, &EditError{Kind: EditInvalidSource, Reason: err.Error()}
	}
	snapshot, err := ParseDocument(source)
	if err != nil {
		return nil, &EditError{Kind: EditInvalidSource, Reason: err.Error()}
	}
	version, err := snapshot.Version()
	if err != nil || version != 1 {
		return nil, &EditError{Kind: EditInvalidSource, Reason: "editing requires document version 1"}
	}
	digest := sha256.Sum256(source)
	session := &EditSession{source: source}
	var markdown strings.Builder
	for i, node := range snapshot.Content() {
		block := NewDocument()
		block.SetContent([]Node{node})
		projection := ToMarkdownLossy(block)
		region := editRegion{
			EditRegion: EditRegion{
				ID:        fmt.Sprintf("%x-%d", digest, i),
				NodeType:  node.Type(),
				Protected: true,
			},
			markdown: strings.Trim(strings.ReplaceAll(projection.Markdown, "\r\n", "\n"), "\n"),
		}
		if len(projection.LossyConstructs) == 0 {
			converted, warnings, err := FromMarkdownLossy(region.markdown)
			if err != nil {
				return nil, fmt.Errorf("adf: check editable region: %w", err)
			}
			if len(warnings) == 0 {
				convertedBytes, err := converted.Marshal()
				if err != nil {
					return nil, fmt.Errorf("adf: compare editable region: %w", err)
				}
				originalBytes, err := block.Marshal()
				if err != nil {
					return nil, fmt.Errorf("adf: compare source region: %w", err)
				}
				equal, err := StructuralEqual(originalBytes, convertedBytes)
				if err != nil {
					return nil, err
				}
				region.Protected = !equal
			}
		}
		mode := "editable"
		if region.Protected {
			mode = "protected"
		}
		fmt.Fprintf(&markdown, "<!-- jira-adf:begin %s %s -->\n\n%s\n\n<!-- jira-adf:end %s -->\n\n", region.ID, mode, region.markdown, region.ID)
		session.regions = append(session.regions, region)
	}
	session.markdown = markdown.String()
	return session, nil
}

// Markdown returns the editable projection. Keep anchor comments on their own
// top-level lines. Markdown between regions inserts new blocks; removing both
// comments deletes an editable region. Protected content must remain unchanged.
func (s *EditSession) Markdown() string { return s.markdown }

// Regions returns independent metadata, never mutable source nodes.
func (s *EditSession) Regions() []EditRegion {
	return xslices.Map(s.regions, func(region editRegion) EditRegion { return region.EditRegion })
}

// Apply builds a candidate against the immutable source. Unchanged subtrees
// retain their raw bytes, including unknown attrs/marks/nodes and large IDs.
// Every protected region must occur exactly once. This local operation does
// not imply that Jira still holds the source snapshot.
func (s *EditSession) Apply(markdown string) (*Document, error) {
	draft, err := s.parseEdit(markdown)
	if err != nil {
		return nil, err
	}
	return draft.document, nil
}

// A block keeps its original region identity even when its replacement spans
// several authored nodes. Flattening to a document before merging would lose
// that identity and turn structural replacements into guessed array offsets.
type editBlock struct {
	region int // -1 identifies authored content outside source anchors.
	nodes  []Node
}

type editDraft struct {
	document *Document
	blocks   []editBlock
}

func (s *EditSession) parseEdit(markdown string) (*editDraft, error) {
	source, err := ParseDocument(s.source)
	if err != nil {
		return nil, fmt.Errorf("adf: restore editing snapshot: %w", err)
	}
	// CRLF is an editor transport convention, not an ADF text mutation. The
	// original raw snapshot wins when normalized editor text is unchanged.
	markdown = strings.ReplaceAll(markdown, "\r\n", "\n")
	draft := &editDraft{document: source}
	if markdown == s.markdown {
		for i, node := range source.Content() {
			draft.blocks = append(draft.blocks, editBlock{region: i, nodes: []Node{node}})
		}
		return draft, nil
	}
	markers, references, err := editMarkers([]byte(markdown))
	if err != nil {
		return nil, err
	}
	regions := make(map[string]int, len(s.regions))
	for i, region := range s.regions {
		regions[region.ID] = i
	}
	seen := make(map[string]bool, len(s.regions))
	content := make([]Node, 0)
	position := 0
	for i := 0; i < len(markers); i += 2 {
		open := markers[i]
		index, exists := regions[open.id]
		if !open.begin || !exists || seen[open.id] {
			return nil, &EditError{Kind: EditInvalidAnchor, Region: open.id, Reason: "unknown, duplicated, or unexpected opening anchor"}
		}
		if i+1 == len(markers) || markers[i+1].begin || markers[i+1].id != open.id {
			return nil, &EditError{Kind: EditInvalidAnchor, Region: open.id, Reason: "missing or mismatched closing anchor"}
		}
		close := markers[i+1]
		region := s.regions[index]
		if open.protected != region.Protected {
			return nil, &EditError{Kind: EditInvalidAnchor, Region: open.id, Reason: "anchor protection metadata changed"}
		}
		inserted, err := authoredEditContent(markdown[position:open.start], references)
		if err != nil {
			return nil, err
		}
		content = append(content, inserted...)
		draft.blocks = append(draft.blocks, xslices.Map(inserted, func(node Node) editBlock {
			return editBlock{region: -1, nodes: []Node{node}}
		})...)
		body := strings.Trim(markdown[open.end:close.start], "\n")
		var replacement []Node
		if body == region.markdown {
			replacement = []Node{source.Content()[index]}
		} else {
			if region.Protected {
				return nil, &EditError{Kind: EditProtectedRegion, Region: open.id, Reason: "protected projection was changed"}
			}
			replacement, err = authoredEditContent(body, references)
			if err != nil {
				return nil, err
			}
		}
		content = append(content, replacement...)
		if len(replacement) != 0 {
			draft.blocks = append(draft.blocks, editBlock{region: index, nodes: replacement})
		}
		seen[open.id] = true
		position = close.end
	}
	for _, region := range s.regions {
		if region.Protected && !seen[region.ID] {
			return nil, &EditError{Kind: EditProtectedRegion, Region: region.ID, Reason: "protected region is missing"}
		}
	}
	inserted, err := authoredEditContent(markdown[position:], references)
	if err != nil {
		return nil, err
	}
	content = append(content, inserted...)
	draft.blocks = append(draft.blocks, xslices.Map(inserted, func(node Node) editBlock {
		return editBlock{region: -1, nodes: []Node{node}}
	})...)
	source.SetContent(content)
	return draft, nil
}

func authoredEditContent(markdown string, references []parser.Reference) ([]Node, error) {
	doc, warnings, err := fromMarkdownWithReferences(markdown, references)
	if err != nil {
		return nil, fmt.Errorf("adf: convert edited Markdown: %w", err)
	}
	// Reshaping warnings also require refusal: strict editing cannot silently
	// normalize wiki markup, move nested tables, or drop unsupported HTML.
	if len(warnings) != 0 {
		return nil, &EditError{Kind: EditLossyContent, Reason: warnings[0].Message}
	}
	if err := doc.Validate(); err != nil {
		return nil, &EditError{Kind: EditLossyContent, Reason: err.Error()}
	}
	return doc.Content(), nil
}

type editMarker struct {
	id        string
	begin     bool
	protected bool
	start     int
	end       int
}

// Goldmark determines block context. Looking for comment text with a line
// regexp would treat anchors inside fenced/indented code as editor metadata.
// Only complete single-line top-level HTML comments can be anchors.
func editMarkers(source []byte) ([]editMarker, []parser.Reference, error) {
	pc := parser.NewContext()
	root := markdownParser.Parse(text.NewReader(source), parser.WithContext(pc))
	var markers []editMarker
	for child := root.FirstChild(); child != nil; child = child.NextSibling() {
		block, ok := child.(*ast.HTMLBlock)
		if !ok || block.HTMLBlockType != ast.HTMLBlockType2 {
			continue
		}
		value := string(block.Lines().Value(source))
		if block.HasClosure() {
			value += string(block.ClosureLine.Value(source))
		}
		if !strings.HasPrefix(strings.TrimSpace(value), "<!-- jira-adf:") {
			continue
		}
		if block.Lines().Len() != 1 || block.HasClosure() {
			return nil, nil, &EditError{Kind: EditInvalidAnchor, Reason: "anchor must be a complete single-line comment"}
		}
		line := block.Lines().At(0)
		parts := strings.Fields(value)
		marker := editMarker{start: line.Start, end: line.Stop}
		switch {
		case len(parts) == 5 && parts[0] == "<!--" && parts[1] == "jira-adf:begin" && parts[4] == "-->" && (parts[3] == "editable" || parts[3] == "protected"):
			marker.id, marker.begin, marker.protected = parts[2], true, parts[3] == "protected"
		case len(parts) == 4 && parts[0] == "<!--" && parts[1] == "jira-adf:end" && parts[3] == "-->":
			marker.id = parts[2]
		default:
			return nil, nil, &EditError{Kind: EditInvalidAnchor, Reason: "malformed editing anchor"}
		}
		markers = append(markers, marker)
	}
	return markers, pc.References(), nil
}
