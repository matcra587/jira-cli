//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

const parityCommentFlagsEdited = `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Edited café","marks":[{"type":"strong"}]}]}]}`

type parityCommentFlagRecipe struct {
	Key, ID, Operation, Source, Native, Markdown string
}

func parityCommentFlagContent(operation string) (string, []byte) {
	switch operation {
	case "add":
		return "**Owned café**", []byte(parityAuthoringDocument)
	case "edit":
		return "**Edited café**", []byte(parityCommentFlagsEdited)
	}
	return "", nil
}

// The actual Markdown argv is closed over two fixed independent documents.
// Translate only that exact recipe into the existing native admission owner;
// never widen the general live harness to arbitrary Markdown or file reads.
func parityCommentFlagArgs(lane string, recipe parityCommentFlagRecipe, role string) ([]string, []string, error) {
	markup, body := parityCommentFlagContent(recipe.Operation)
	if body == nil || role == "" || (lane != "before" && lane != "after") || (recipe.Source != "inline" && recipe.Source != "file") || recipe.Operation == "add" && recipe.ID != "" || recipe.Operation == "edit" && recipe.ID == "" {
		return nil, nil, fmt.Errorf("comment flags require an exact authored operation, source and role")
	}
	base := []string{"issue", "comment", recipe.Operation, recipe.Key}
	if recipe.Operation == "edit" {
		base = append(base, recipe.ID)
	}
	actual := slices.Clone(base)
	if recipe.Source == "inline" {
		actual = append(actual, "--markdown", markup)
	} else {
		actual = append(actual, "--markdown-file", recipe.Markdown)
	}
	canonical := append(slices.Clone(base), "--json-input", recipe.Native)
	actual = append(actual, "--visibility-role", role)
	canonical = append(canonical, "--visibility-role", role)
	if lane == "after" && recipe.Operation == "edit" {
		actual, canonical = append(actual, "--notify=false"), append(canonical, "--notify=false")
	}
	return actual, canonical, nil
}

func (p *parityRun) validateCommentFlagMutation(lane parityLane, recipe parityCommentFlagRecipe, args []string) error {
	expected, canonical, err := parityCommentFlagArgs(lane.Name, recipe, p.owner.commentRole)
	if err != nil || !slices.Equal(args, expected) {
		return fmt.Errorf("comment flags differ from their exact owned recipe")
	}
	// This checks the selected digest, active issue/comment pair, registered
	// native input and verified role before either input is opened below.
	if err := p.validateCommentMutation(lane, recipe.Key, canonical); err != nil {
		return err
	}
	markup, body := parityCommentFlagContent(recipe.Operation)
	native, err := os.ReadFile(recipe.Native)
	if err != nil || !parityAuthoringJSON(native, body) {
		return fmt.Errorf("comment flags native recipe differs from its authored document")
	}
	if recipe.Source == "inline" {
		if recipe.Markdown != "" {
			return fmt.Errorf("inline comment flags cannot carry an unused file")
		}
		return nil
	}
	if recipe.Markdown != filepath.Join(p.owner.dir, filepath.Base(recipe.Markdown)) {
		return fmt.Errorf("comment flags file must be an exact direct child of its owned directory")
	}
	info, err := os.Lstat(recipe.Markdown)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("comment flags require an owned regular Markdown file")
	}
	text, err := os.ReadFile(recipe.Markdown)
	if err != nil || string(text) != markup {
		return fmt.Errorf("comment flags file differs from its exact authored bytes")
	}
	return nil
}

func parityCommentFlagIdentity(stdout []byte, lane, key string) string {
	if key == "" || !jsontext.Value(stdout).IsValid() {
		return ""
	}
	var frame, data, entry, comment map[string]json.RawMessage
	if json.Unmarshal(stdout, &frame) != nil || json.Unmarshal(frame["data"], &data) != nil || data == nil {
		return ""
	}
	switch lane {
	case "before":
		if !parityWorklogReceiptTarget(data["issue"], key) || json.Unmarshal(data["comment"], &comment) != nil {
			return ""
		}
	case "after":
		var rows []map[string]json.RawMessage
		if json.Unmarshal(data["results"], &rows) != nil || len(rows) != 1 || !parityWorklogReceiptTarget(rows[0]["issue"], key) || json.Unmarshal(rows[0]["data"], &entry) != nil || json.Unmarshal(entry["comment"], &comment) != nil {
			return ""
		}
	default:
		return ""
	}
	var id string
	if json.Unmarshal(comment["id"], &id) != nil {
		return ""
	}
	numeric, err := strconv.ParseUint(id, 10, 64)
	if err != nil || numeric == 0 || strconv.FormatUint(numeric, 10) != id {
		return ""
	}
	return id
}

func TestParityCommentFlagIdentityRequiresExactTarget(t *testing.T) {
	for _, tc := range []struct{ lane, raw string }{
		{"before", `{"data":{"issue":{"key":"PAIR-1"},"comment":{"id":"501"}}}`},
		{"after", `{"data":{"results":[{"issue":{"key":"PAIR-1"},"data":{"comment":{"id":"501"}}}]}}`},
	} {
		t.Run(tc.lane, func(t *testing.T) {
			if parityCommentFlagIdentity([]byte(tc.raw), tc.lane, "PAIR-1") != "501" {
				t.Fatal("independent comment identity was refused")
			}
			for _, change := range []struct{ name, old, value string }{
				{"foreign-target", `"key":"PAIR-1"`, `"key":"PAIR-2"`},
				{"wrong-case-key", `"key":`, `"Key":`},
				{"wrong-case-id", `"id":`, `"ID":`},
				{"duplicate-id", `"id":"501"`, `"id":"502","id":"501"`},
				{"padded-id", `"id":"501"`, `"id":"0501"`},
				{"null-id", `"id":"501"`, `"id":null`},
			} {
				t.Run(change.name, func(t *testing.T) {
					changed := strings.Replace(tc.raw, change.old, change.value, 1)
					if changed == tc.raw || parityCommentFlagIdentity([]byte(changed), tc.lane, "PAIR-1") != "" {
						t.Fatal("unbound comment identity earned mutation or cleanup ownership")
					}
				})
			}
			aliased := strings.Replace(tc.raw, `"id":"501"`, `"id":"501","ID":"777"`, 1)
			if parityCommentFlagIdentity([]byte(aliased), tc.lane, "PAIR-1") != "501" {
				t.Fatal("mixed-case alias replaced the exact retained native identity")
			}
		})
	}
}

func TestParityCommentFlagMutationAdmission(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "admitted-fixture")
	bytes := []byte("independent admission bytes")
	if err := os.WriteFile(binary, bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	lane := parityLane{Name: "after", Binary: binary, Digest: fmt.Sprintf("%x", sha256.Sum256(bytes))}
	r := &liveRun{verified: true, dir: dir, commentRole: "Developers", manifest: liveManifest{Issues: []ownedIssue{{Key: "PAIR-1", Comments: []ownedComment{{ID: "501"}}}}}}
	p := &parityRun{owner: r, evidence: parityEvidence{Lanes: []parityLane{lane}}}
	for _, operation := range []string{"add", "edit"} {
		for _, source := range []string{"inline", "file"} {
			t.Run(operation+"/"+source, func(t *testing.T) {
				markup, body := parityCommentFlagContent(operation)
				id := ""
				if operation == "edit" {
					id = "501"
				}
				recipe := parityCommentFlagRecipe{Key: "PAIR-1", ID: id, Operation: operation, Source: source, Native: r.parityCommentInput(t, "PAIR-1", id, body)}
				if source == "file" {
					recipe.Markdown = filepath.Join(dir, operation+".md")
					if err := os.WriteFile(recipe.Markdown, []byte(markup), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				args, _, err := parityCommentFlagArgs(lane.Name, recipe, r.commentRole)
				if err != nil || p.validateCommentFlagMutation(lane, recipe, args) != nil {
					t.Fatal("complete fixed comment flags recipe was refused")
				}
				otherNative := r.parityCommentInput(t, recipe.Key, recipe.ID, []byte(parityDescription))
				for _, change := range []struct {
					name   string
					mutate func(*parityCommentFlagRecipe)
				}{
					{"foreign-key", func(v *parityCommentFlagRecipe) { v.Key = "PAIR-9" }},
					{"unsupported-operation", func(v *parityCommentFlagRecipe) { v.Operation = "delete" }},
					{"unsupported-source", func(v *parityCommentFlagRecipe) { v.Source = "unregistered" }},
					{"unregistered-native", func(v *parityCommentFlagRecipe) { v.Native = filepath.Join(dir, "unregistered.json") }},
					{"different-native-document", func(v *parityCommentFlagRecipe) { v.Native = otherNative }},
					{"wrong-resource", func(v *parityCommentFlagRecipe) { v.ID = "502" }},
					{"outside-markdown", func(v *parityCommentFlagRecipe) { v.Markdown = filepath.Join(filepath.Dir(dir), "unowned.md") }},
				} {
					t.Run(change.name, func(t *testing.T) {
						changed := recipe
						change.mutate(&changed)
						changedArgs, _, err := parityCommentFlagArgs(lane.Name, changed, r.commentRole)
						if err == nil && p.validateCommentFlagMutation(lane, changed, changedArgs) == nil {
							t.Fatal("unregistered or mismatched comment recipe entered mutation")
						}
					})
				}
				t.Run("unadmitted-lane", func(t *testing.T) {
					changed := lane
					changed.Digest = "unadmitted"
					if p.validateCommentFlagMutation(changed, recipe, args) == nil {
						t.Fatal("unadmitted selected binary entered comment mutation")
					}
				})
				for _, suffix := range []string{"PAIR-2", "--jql=project=PAIR", "--dry-run", "--markdown=foreign", "--notify=true"} {
					t.Run("extra/"+suffix, func(t *testing.T) {
						if p.validateCommentFlagMutation(lane, recipe, append(slices.Clone(args), suffix)) == nil {
							t.Fatal("unauthored comment flags entered mutation admission")
						}
					})
				}
				if source == "file" {
					t.Run("ancestor-symlink-escape", func(t *testing.T) {
						outside := t.TempDir()
						if err := os.WriteFile(filepath.Join(outside, "comment.md"), []byte(markup), 0o600); err != nil {
							t.Fatal(err)
						}
						link := filepath.Join(dir, operation+"-linked")
						if err := os.Symlink(outside, link); err != nil {
							if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
								t.Skip("native Windows symlink privilege unavailable: ", err)
							}
							t.Fatal(err)
						}
						changed := recipe
						changed.Markdown = filepath.Join(link, "comment.md")
						changedArgs, _, err := parityCommentFlagArgs(lane.Name, changed, r.commentRole)
						if err != nil || p.validateCommentFlagMutation(lane, changed, changedArgs) == nil {
							t.Fatal("an ancestor symlink escaped the owned Markdown file boundary")
						}
					})
					if err := os.WriteFile(recipe.Markdown, []byte("foreign"), 0o600); err != nil {
						t.Fatal(err)
					}
					if p.validateCommentFlagMutation(lane, recipe, args) == nil {
						t.Fatal("changed Markdown bytes entered mutation")
					}
					if err := os.WriteFile(recipe.Markdown, []byte(markup), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(recipe.Native, []byte(parityDescription), 0o600); err != nil {
					t.Fatal(err)
				}
				if p.validateCommentFlagMutation(lane, recipe, args) == nil {
					t.Fatal("changed native registration entered mutation")
				}
			})
		}
	}
}

func TestParityCommentFlagReceiptRoutes(t *testing.T) {
	const add = `{"data":{"dry_run":true,"body":` + parityAuthoringDocument + `,"body_route":"markdown","results":[{"issue":{"key":"PAIR-1"},"ok":true,"data":{}}]}}`
	if !parityCommentAddReply([]byte(add), "after", true, "markdown").Valid || parityCommentAddReply([]byte(add), "after", true).Valid || parityCommentAddReply([]byte(add), "unknown", true, "markdown").Valid || parityCommentAddReply([]byte(add), "after", true, "markdown", "adf").Valid {
		t.Fatal("comment add receipt did not require its exact declared lane and body route")
	}
	const edit = `{"data":{"issue":{"key":"PAIR-1"},"comment_id":"501","dry_run":true,"body":` + parityCommentFlagsEdited + `,"body_route":"markdown","visibility_cleared":false,"notify":false}}`
	want := parityCommentReceiptOptions{Route: "markdown", Notify: false}
	check := func(raw string, option parityCommentReceiptOptions) bool {
		return parityCommentEditReceipt([]byte(raw), "after", "PAIR-1", "501", []byte(parityCommentFlagsEdited), "keep", "", true, option)
	}
	if !check(edit, want) || check(edit, parityCommentReceiptOptions{Route: "adf", Notify: false}) || check(edit, parityCommentReceiptOptions{Route: "markdown", Notify: true}) {
		t.Fatal("comment edit receipt did not bind its route and notification request")
	}
	for _, tc := range []struct{ name, old, value string }{
		{"wrong-route", `"body_route":"markdown"`, `"body_route":"adf"`},
		{"wrong-notify", `"notify":false`, `"notify":true`},
		{"wrong-body", "Edited café", "Owned café"},
		{"wrong-target", "PAIR-1", "PAIR-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(edit, tc.old, tc.value, 1)
			if changed == edit || check(changed, want) {
				t.Fatal("unrelated Markdown edit receipt earned acceptance")
			}
		})
	}
}

func TestLiveParityCommentMarkdownFlags(t *testing.T) {
	p := newParityRun(t)
	r := p.owner
	r.commentRole = currentCommentRole(t, r)
	for _, lane := range p.evidence.Lanes {
		if !t.Run(lane.Name, func(t *testing.T) {
			key := p.create(t, lane)
			original := p.native(t, key)
			ids, bodies := []string{}, map[string][]byte{}
			p.requireCommentInventory(t, key, ids, bodies)
			for _, source := range []string{"inline", "file"} {
				if !t.Run(source, func(t *testing.T) {
					var id string
					for _, operation := range []string{"add", "edit"} {
						markup, body := parityCommentFlagContent(operation)
						recipe := parityCommentFlagRecipe{Key: key, ID: id, Operation: operation, Source: source, Native: r.parityCommentInput(t, key, id, body)}
						if source == "file" {
							recipe.Markdown = filepath.Join(r.dir, "comment-"+lane.Name+"-"+operation+" spaces 古い '$;&.md")
							if err := os.WriteFile(recipe.Markdown, []byte(markup), 0o600); err != nil {
								t.Fatal(err)
							}
						}
						args, _, err := parityCommentFlagArgs(lane.Name, recipe, r.commentRole)
						if err != nil || p.validateCommentFlagMutation(lane, recipe, args) != nil {
							t.Fatal("fixed comment Markdown recipe failed immediate admission")
						}
						index := len(p.evidence.Comments)
						p.evidence.Comments = append(p.evidence.Comments, parityCommentAttempt{Lane: lane.Name, Key: key, ID: id, Digest: fmt.Sprintf("%x", sha256.Sum256(body)), Operation: operation, Source: source})
						p.save(t)
						// Re-admit after publishing intent, immediately before entry.
						if err := p.validateCommentFlagMutation(lane, recipe, args); err != nil {
							t.Fatal(err)
						}
						frame, stdout, exit := p.invoke(t, lane, nil, args...)
						if operation == "add" {
							id = parityCommentFlagIdentity(stdout, lane.Name, key)
							p.evidence.Comments[index].ID = id
							p.save(t)
							if id == "" || slices.Contains(ids, id) {
								t.Fatal("comment add returned no fresh bound identity; retain intent and never replay")
							}
							read := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key+"/comment/"+id)
							if !parityCreateFlagsIdentity(read.Response.JSON, map[string]string{"id": id}) {
								t.Fatal("comment add identity lacks an exact independent parent-bound native record")
							}
							// Register known ownership before checking requested behavior.
							r.registerComment(t, key, id)
							p.evidence.Comments[index].Bound = true
							p.save(t)
							ids = append(ids, id)
						}
						stored := rawOwnedComment(t, r, key, id)
						if !nativeMetadataMatches(stored.Body, body, nil) || stored.Visibility == nil || stored.Visibility.Type != "role" || stored.Visibility.Value != r.commentRole {
							t.Fatal("owned Markdown comment lost its requested body or verified role")
						}
						p.evidence.Comments[index].Bound = true
						p.save(t)
						bodies[id] = body
						p.requireCommentInventory(t, key, ids, bodies)
						if exit != 0 || !frame.OK {
							t.Fatal("native comment effect retained a failed command outcome; never replay")
						}
						parityAuthoringCleanData(t, stdout, lane.Name, "issue.comment."+operation)
						if operation == "add" {
							reply := parityCommentAddReply(stdout, lane.Name, false, "markdown")
							if !reply.Valid || reply.ID != id || reply.Key != key || !nativeMetadataMatches(reply.Body, body, nil) || !nativeMetadataMatches(reply.StoredBody, body, nil) || lane.Name == "after" && !parityCommentVisibility(reply.Visibility, "replace", r.commentRole) {
								t.Fatal("Markdown comment add lost its candidate or native acknowledgement")
							}
						} else if !parityCommentEditReceipt(stdout, lane.Name, key, id, body, "replace", r.commentRole, false, parityCommentReceiptOptions{Route: "markdown", Notify: lane.Name == "before"}) {
							t.Fatal("Markdown comment edit lost its candidate, role or notification receipt")
						}
						if err := p.validateCommentFlagMutation(lane, recipe, args); err != nil {
							t.Fatal("comment operation changed its frozen input: ", err)
						}
						prefix := "I007"
						if operation == "edit" {
							prefix = "I009"
						}
						p.check(t, lane, prefix+".markdown-flags."+source+".native")
					}
				}) {
					return
				}
			}
			current := p.native(t, key)
			if current.ID != original.ID || !nativeMetadataMatches(current.Fields, original.Fields, nil) {
				t.Fatal("comment flag workflow changed unrelated owned issue fields")
			}
		}) {
			return
		}
	}
}
