package adf

import (
	"encoding/json"
	"fmt"

	xslices "github.com/gechr/x/slices"
)

// Merge reconciles the source snapshot, anchored Markdown edits and a current
// native document. Regions remain atomic even when a local replacement authors
// several blocks. Current root metadata and untouched current subtrees retain
// their raw bytes; authored replacements were validated by the edit boundary.
//
// Native localId values and unique structural matches establish current region
// identity. Array offsets are used only when the entire ordered content is
// structurally unchanged. Ambiguous identity, edit/delete, move/delete and
// cyclic ordering conflicts return no candidate. Protected regions require a
// proven current identity even when the Markdown is unchanged. Current blocks
// without source identities remain distinct insertions; simultaneous insertions
// in the same gap retain current order before local order. This does not make a Jira write
// atomic: a remote precondition is still required to prevent a stale overwrite.
func (s *EditSession) Merge(markdown string, current *Document) (*Document, error) {
	draft, err := s.parseEdit(markdown)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, &EditError{Kind: EditInvalidSource, Reason: "current document is nil"}
	}
	data, err := current.Marshal()
	if err != nil {
		return nil, fmt.Errorf("adf: snapshot current document: %w", err)
	}
	if _, err := canonicalStructure(data); err != nil {
		return nil, &EditError{Kind: EditInvalidSource, Reason: err.Error()}
	}
	remote, err := ParseDocument(data)
	if err != nil {
		return nil, &EditError{Kind: EditInvalidSource, Reason: err.Error()}
	}
	version, err := remote.Version()
	if err != nil || version != 1 {
		return nil, &EditError{Kind: EditInvalidSource, Reason: "merging requires current document version 1"}
	}
	localStructure, err := editNodesStructure(draft.document.Content())
	if err != nil {
		return nil, err
	}
	remoteStructure, err := editNodesStructure(remote.Content())
	if err != nil {
		return nil, err
	}
	// Equal replacements of existing regions can coalesce. Independent
	// insertions retain both identities even when their text is identical.
	if localStructure == remoteStructure && !xslices.AnyFunc(draft.blocks, func(block editBlock) bool { return block.region < 0 }) {
		return remote, nil
	}
	base, err := ParseDocument(s.source)
	if err != nil {
		return nil, fmt.Errorf("adf: restore merge base: %w", err)
	}
	baseStructure, err := editNodesStructure(base.Content())
	if err != nil {
		return nil, err
	}
	baseFacts, err := editNodeFacts(base.Content())
	if err != nil {
		return nil, err
	}
	if baseStructure == remoteStructure {
		// Whole-sequence equivalence proves this positional correspondence; a
		// partial or reordered sequence must use identities instead.
		content := make([]Node, 0)
		for _, block := range draft.blocks {
			value, err := editNodesStructure(block.nodes)
			if err != nil {
				return nil, err
			}
			if block.region >= 0 && value == baseFacts[block.region].value {
				content = append(content, remote.Content()[block.region])
				continue
			}
			content = append(content, block.nodes...)
		}
		remote.SetContent(content)
		return remote, nil
	}
	remoteFacts, err := editNodeFacts(remote.Content())
	if err != nil {
		return nil, err
	}
	baseToRemote, remoteToBase, err := matchEditRegions(baseFacts, remoteFacts)
	if err != nil {
		return nil, err
	}
	localPositions := make(map[int]int)
	for i, block := range draft.blocks {
		if block.region >= 0 {
			localPositions[block.region] = i
		}
	}
	var blocks []mergeBlock
	for i, original := range baseFacts {
		localPosition, localExists := localPositions[i]
		remotePosition, remoteExists := baseToRemote[i]
		if !localExists {
			if remoteExists {
				if remoteFacts[remotePosition].value != original.value || editRegionMoved(i, baseToRemote) {
					return nil, s.mergeConflict(i, "region was removed locally but changed or moved remotely")
				}
				continue
			}
			// Without a native identity, an unmatched current block could be
			// this region edited and moved, rather than a separate insertion.
			if original.id == "" && len(remoteToBase) != len(remoteFacts) {
				return nil, &EditError{Kind: EditAmbiguousIdentity, Region: s.regions[i].ID, Reason: "cannot distinguish a remote replacement from an insertion for the deleted region"}
			}
			continue
		}
		local := draft.blocks[localPosition]
		localValue, err := editNodesStructure(local.nodes)
		if err != nil {
			return nil, err
		}
		if !remoteExists {
			if s.regions[i].Protected {
				if original.id == "" {
					return nil, &EditError{Kind: EditAmbiguousIdentity, Region: s.regions[i].ID, Reason: "protected current region has no proven source identity"}
				}
				return nil, s.mergeConflict(i, "protected region was removed remotely")
			}
			if localValue != original.value || editRegionMoved(i, localPositions) {
				return nil, s.mergeConflict(i, "region was edited or moved locally but removed or replaced remotely")
			}
			continue
		}
		selected := []Node{remote.Content()[remotePosition]}
		if localValue != original.value {
			if remoteFacts[remotePosition].value != original.value && localValue != remoteFacts[remotePosition].value {
				return nil, s.mergeConflict(i, "region was changed differently on both sides")
			}
			if localValue != remoteFacts[remotePosition].value {
				selected = local.nodes
			}
		}
		blocks = append(blocks, mergeBlock{region: i, nodes: selected, local: localPosition, remote: remotePosition})
	}
	for i, block := range draft.blocks {
		if block.region < 0 {
			blocks = append(blocks, mergeBlock{region: -1, nodes: block.nodes, local: i, remote: -1})
		}
	}
	for i, node := range remote.Content() {
		if _, matched := remoteToBase[i]; !matched {
			blocks = append(blocks, mergeBlock{region: -1, nodes: []Node{node}, local: -1, remote: i})
		}
	}
	content, err := mergeBlockOrder(blocks)
	if err != nil {
		return nil, err
	}
	mergedStructure, err := editNodesStructure(content)
	if err != nil {
		return nil, err
	}
	if mergedStructure == remoteStructure {
		return remote, nil
	}
	remote.SetContent(content)
	return remote, nil
}

func (s *EditSession) mergeConflict(region int, reason string) error {
	return &EditError{Kind: EditMergeConflict, Region: s.regions[region].ID, Reason: reason}
}

type editNodeFact struct {
	id    string
	value string
}

func editNodeFacts(nodes []Node) ([]editNodeFact, error) {
	facts := make([]editNodeFact, len(nodes))
	for i, node := range nodes {
		value, err := editNodesStructure([]Node{node})
		if err != nil {
			return nil, err
		}
		// Future or non-string attrs remain preserved structural data, but
		// do not establish a native identity at this matching boundary. A map
		// requires exact key spelling; struct decoding accepts case variants.
		id := ""
		if raw := node.Attrs(); len(raw) > 0 {
			var attrs map[string]json.RawMessage
			if err := json.Unmarshal(raw, &attrs); err == nil {
				if localID, exists := attrs["localId"]; exists {
					var value string
					if err := json.Unmarshal(localID, &value); err == nil {
						id = value
					}
				}
			}
		}
		facts[i] = editNodeFact{id: id, value: value}
	}
	return facts, nil
}

func editNodesStructure(nodes []Node) (string, error) {
	doc := NewDocument()
	doc.SetContent(nodes)
	data, err := doc.Marshal()
	if err != nil {
		return "", fmt.Errorf("adf: compare merge content: %w", err)
	}
	canonical, err := canonicalStructure(data)
	if err != nil {
		return "", fmt.Errorf("adf: compare merge content: %w", err)
	}
	return string(canonical), nil
}

func nativeEditIdentities(facts []editNodeFact) (map[string]int, error) {
	identities := make(map[string]int)
	for i, fact := range facts {
		if fact.id == "" {
			continue
		}
		if _, exists := identities[fact.id]; exists {
			return nil, &EditError{Kind: EditAmbiguousIdentity, Reason: "document contains repeated native localId values"}
		}
		identities[fact.id] = i
	}
	return identities, nil
}

func matchEditRegions(base, remote []editNodeFact) (map[int]int, map[int]int, error) {
	baseIDs, err := nativeEditIdentities(base)
	if err != nil {
		return nil, nil, err
	}
	if _, err := nativeEditIdentities(remote); err != nil {
		return nil, nil, err
	}
	baseValues := make(map[string][]int)
	remoteValues := make(map[string]int)
	for i, fact := range base {
		baseValues[fact.value] = append(baseValues[fact.value], i)
	}
	for _, fact := range remote {
		remoteValues[fact.value]++
	}
	baseToRemote, remoteToBase := make(map[int]int), make(map[int]int)
	for i, fact := range remote {
		index, matched := baseIDs[fact.id]
		if !matched {
			candidates := baseValues[fact.value]
			if len(candidates) == 0 {
				continue
			}
			if len(candidates) != 1 || remoteValues[fact.value] != 1 {
				return nil, nil, &EditError{Kind: EditAmbiguousIdentity, Reason: "repeated subtrees do not establish a unique current region"}
			}
			index = candidates[0]
		}
		if _, duplicate := baseToRemote[index]; duplicate {
			return nil, nil, &EditError{Kind: EditAmbiguousIdentity, Reason: "multiple current blocks match the same source region"}
		}
		baseToRemote[index], remoteToBase[i] = i, index
	}
	return baseToRemote, remoteToBase, nil
}

func editRegionMoved(region int, positions map[int]int) bool {
	position := positions[region]
	for other, otherPosition := range positions {
		if (region < other) != (position < otherPosition) {
			return true
		}
	}
	return false
}

type mergeBlock struct {
	region int
	nodes  []Node
	local  int
	remote int
}

// Relative order is itself a three-way value. An unchanged side does not
// veto the other side's move. Insertions retain each relation observable on
// their own side; incompatible combinations form a cycle and fail loudly.
func editBlockBefore(a, b mergeBlock) bool {
	if a.region >= 0 && b.region >= 0 {
		base := a.region < b.region
		local, remote := a.local < b.local, a.remote < b.remote
		if local != base {
			return local
		}
		return remote
	}
	if a.local >= 0 && b.local >= 0 {
		return a.local < b.local
	}
	if a.remote >= 0 && b.remote >= 0 {
		return a.remote < b.remote
	}
	return false
}

func editBlockPriority(a, b mergeBlock) bool {
	if a.remote >= 0 && b.remote >= 0 {
		return a.remote < b.remote
	}
	if a.remote >= 0 || b.remote >= 0 {
		return a.remote >= 0
	}
	return a.local < b.local
}

// Kahn's ordering computes pair constraints on demand rather than retaining
// a quadratic adjacency matrix. Selection is deterministic, including when
// distinct current and local insertions share the same gap.
func mergeBlockOrder(blocks []mergeBlock) ([]Node, error) {
	indegree := make([]int, len(blocks))
	for i, block := range blocks {
		for j, other := range blocks {
			if i != j && editBlockBefore(other, block) {
				indegree[i]++
			}
		}
	}
	emitted := make([]bool, len(blocks))
	content := make([]Node, 0)
	for range blocks {
		next := -1
		for i, block := range blocks {
			if !emitted[i] && indegree[i] == 0 && (next < 0 || editBlockPriority(block, blocks[next])) {
				next = i
			}
		}
		if next < 0 {
			return nil, &EditError{Kind: EditMergeConflict, Reason: "concurrent structural edits require incompatible block order"}
		}
		content = append(content, blocks[next].nodes...)
		emitted[next] = true
		for i, block := range blocks {
			if !emitted[i] && editBlockBefore(blocks[next], block) {
				indegree[i]--
			}
		}
	}
	return content, nil
}
