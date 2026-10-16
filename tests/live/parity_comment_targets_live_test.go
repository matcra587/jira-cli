//go:build live

package live

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Only two exact spellings of one owned parent are admitted. The existing
// Markdown recipe still owns body, role, selected binary and input binding.
func parityCommentTargetArgs(lane string, recipe parityCommentFlagRecipe, role, spelling string) ([]string, error) {
	if recipe.Operation != "add" || recipe.ID != "" || recipe.Source != "inline" {
		return nil, fmt.Errorf("comment target controls require the fixed inline add recipe")
	}
	base, _, err := parityCommentFlagArgs(lane, recipe, role)
	if err != nil {
		return nil, err
	}
	args := slices.Clone(base[:3])
	switch spelling {
	case "repeated":
		args = append(args, recipe.Key, recipe.Key)
	case "csv":
		args = append(args, recipe.Key+","+recipe.Key)
	default:
		return nil, fmt.Errorf("comment target spelling is not registered")
	}
	return append(args, base[4:]...), nil
}

func (p *parityRun) validateCommentTargetMutation(lane parityLane, recipe parityCommentFlagRecipe, spelling string, args []string) error {
	expected, err := parityCommentTargetArgs(lane.Name, recipe, p.owner.commentRole, spelling)
	if err != nil || !slices.Equal(expected, args) {
		return fmt.Errorf("comment target argv differs from its exact owned recipe")
	}
	base, _, err := parityCommentFlagArgs(lane.Name, recipe, p.owner.commentRole)
	if err != nil {
		return err
	}
	return p.validateCommentFlagMutation(lane, recipe, base)
}

func TestParityCommentTargetMutationAdmission(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "admitted-fixture")
	bytes := []byte("independent comment target admission bytes")
	if err := os.WriteFile(binary, bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	lane := parityLane{Name: "after", Binary: binary, Digest: fmt.Sprintf("%x", sha256.Sum256(bytes))}
	r := &liveRun{verified: true, dir: dir, commentRole: "Developers", manifest: liveManifest{Issues: []ownedIssue{{Key: "PAIR-1"}}}}
	p := &parityRun{owner: r, evidence: parityEvidence{Lanes: []parityLane{lane}}}
	recipe := parityCommentFlagRecipe{Key: "PAIR-1", Operation: "add", Source: "inline", Native: r.parityCommentInput(t, "PAIR-1", "", []byte(parityAuthoringDocument))}
	for _, spelling := range []string{"repeated", "csv"} {
		t.Run(spelling, func(t *testing.T) {
			args, err := parityCommentTargetArgs(lane.Name, recipe, r.commentRole, spelling)
			if err != nil || p.validateCommentTargetMutation(lane, recipe, spelling, args) != nil {
				t.Fatal("registered single-parent duplicate spelling was refused")
			}
			mixed := slices.Clone(args)
			if spelling == "repeated" {
				mixed[4] = "PAIR-2"
			} else {
				mixed[3] = "PAIR-1,PAIR-2"
			}
			for _, tc := range []struct {
				name string
				args []string
			}{
				{"mixed-parent", mixed},
				{"extra-parent", append(slices.Clone(args), "PAIR-2")},
				{"extra-selector", append(slices.Clone(args), "--jql=project=PAIR")},
				{"different-markdown", append(slices.Clone(args), "--markdown=foreign")},
				{"preview", append(slices.Clone(args), "--dry-run")},
				{"missing-target", slices.Delete(slices.Clone(args), 3, 4)},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if p.validateCommentTargetMutation(lane, recipe, spelling, tc.args) == nil {
						t.Fatal("unauthored target argv entered mutation admission")
					}
				})
			}
			for _, tc := range []struct {
				name   string
				change func(*parityCommentFlagRecipe)
			}{
				{"foreign-parent", func(v *parityCommentFlagRecipe) { v.Key = "PAIR-2" }},
				{"edit-resource", func(v *parityCommentFlagRecipe) { v.Operation, v.ID = "edit", "501" }},
				{"file-input", func(v *parityCommentFlagRecipe) { v.Source = "file" }},
				{"unregistered-native", func(v *parityCommentFlagRecipe) { v.Native = filepath.Join(dir, "other.json") }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					changed := recipe
					tc.change(&changed)
					actual, err := parityCommentTargetArgs(lane.Name, changed, r.commentRole, spelling)
					if err == nil && p.validateCommentTargetMutation(lane, changed, spelling, actual) == nil {
						t.Fatal("unowned or different operation entered target admission")
					}
				})
			}
			changed := lane
			changed.Digest = "unadmitted"
			if p.validateCommentTargetMutation(changed, recipe, spelling, args) == nil || p.validateCommentTargetMutation(lane, recipe, "unknown", args) == nil {
				t.Fatal("unadmitted binary or target spelling entered mutation")
			}
		})
	}
}

func TestLiveParityCommentDuplicateTargets(t *testing.T) {
	p := newParityRun(t)
	r := p.owner
	r.commentRole = currentCommentRole(t, r)
	for _, lane := range p.evidence.Lanes {
		if !t.Run(lane.Name, func(t *testing.T) {
			key := p.create(t, lane)
			original := p.native(t, key)
			ids, bodies := []string{}, map[string][]byte{}
			p.requireCommentInventory(t, key, ids, bodies)
			for _, spelling := range []string{"repeated", "csv"} {
				if !t.Run(spelling, func(t *testing.T) {
					body := []byte(parityAuthoringDocument)
					recipe := parityCommentFlagRecipe{Key: key, Operation: "add", Source: "inline", Native: r.parityCommentInput(t, key, "", body)}
					args, err := parityCommentTargetArgs(lane.Name, recipe, r.commentRole, spelling)
					if err != nil || p.validateCommentTargetMutation(lane, recipe, spelling, args) != nil {
						t.Fatal("owned duplicate target recipe failed initial admission")
					}
					index := len(p.evidence.Comments)
					p.evidence.Comments = append(p.evidence.Comments, parityCommentAttempt{Lane: lane.Name, Key: key, Digest: fmt.Sprintf("%x", sha256.Sum256(body)), Operation: "add", Source: "targets-" + spelling})
					p.save(t)
					if err := p.validateCommentTargetMutation(lane, recipe, spelling, args); err != nil {
						t.Fatal("owned duplicate target recipe changed before entry")
					}
					frame, stdout, exit := p.invoke(t, lane, nil, args...)
					id := parityCommentFlagIdentity(stdout, lane.Name, key)
					p.evidence.Comments[index].ID = id
					p.save(t)
					if id == "" || slices.Contains(ids, id) {
						t.Fatal("comment target control has no unique returned resource; retain intent and never replay")
					}
					read := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key+"/comment/"+id)
					if !parityCreateFlagsIdentity(read.Response.JSON, map[string]string{"id": id}) {
						t.Fatal("comment target returned resource is not independently bound to its parent")
					}
					r.registerComment(t, key, id)
					p.evidence.Comments[index].Bound = true
					p.save(t)
					ids = append(ids, id)
					bodies[id] = body
					stored := rawOwnedComment(t, r, key, id)
					if !nativeMetadataMatches(stored.Body, body, nil) || stored.Visibility == nil || stored.Visibility.Type != "role" || stored.Visibility.Value != r.commentRole {
						t.Fatal("comment target lost native Markdown or verified visibility")
					}
					p.requireCommentInventory(t, key, ids, bodies)
					if exit != 0 || !frame.OK {
						t.Fatal("known comment effect retained failed command outcome; never replay")
					}
					parityAuthoringCleanData(t, stdout, lane.Name, "issue.comment.add")
					reply := parityCommentAddReply(stdout, lane.Name, false, "markdown")
					if !reply.Valid || reply.ID != id || reply.Key != key || !nativeMetadataMatches(reply.Body, body, nil) || !nativeMetadataMatches(reply.StoredBody, body, nil) || lane.Name == "after" && !parityCommentVisibility(reply.Visibility, "replace", r.commentRole) {
						t.Fatal("comment target receipt lost its single native acknowledgement")
					}
					if err := p.validateCommentTargetMutation(lane, recipe, spelling, args); err != nil {
						t.Fatal("comment target control changed frozen input")
					}
					p.check(t, lane, "I007.duplicate-targets."+spelling+".native")
				}) {
					return
				}
			}
			current := p.native(t, key)
			if current.ID != original.ID || !nativeMetadataMatches(current.Fields, original.Fields, nil) {
				t.Fatal("duplicate comment targets changed unrelated issue fields")
			}
		}) {
			return
		}
	}
}
