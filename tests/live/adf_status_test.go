//go:build live

package live

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

// Controlled intervening writes exercise the three-way primitive against
// actual stored snapshots. Raw PUT is not a conditional write, so this does
// not establish optimistic concurrency for an issue editor.
func TestLiveADFThreeWayMerge(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "three-way-adf-merge")
	const panelID = "11111111-1111-4111-8111-111111111111"
	original := []byte(fmt.Sprintf(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"alpha"}]},{"type":"panel","attrs":{"panelType":"warning","localId":%q},"content":[{"type":"paragraph","content":[{"type":"text","text":"rich content"}]}]},{"type":"paragraph","content":[{"type":"text","text":"beta"}]}]}`, panelID))
	posted := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--json-input", r.nativeBody(t, original))
	id := postedADFCommentID(t, posted)
	r.registerComment(t, key, id)
	initial := r.success(t, "issue", "comment", "list", key, "--all")
	if err := verifyStoredADFComments(map[string]json.RawMessage{id: original}, initial); err != nil {
		t.Fatal(err)
	}
	source, err := adf.ParseDocument(initial.Data.Comments[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	session, err := adf.PrepareEdit(source)
	if err != nil {
		t.Fatal(err)
	}
	regions := session.Regions()
	if len(regions) != 3 || regions[0].Protected || !regions[1].Protected || regions[2].Protected {
		t.Fatal("stored native source lost its editable and protected region identities")
	}
	markdown := session.Markdown()
	panelStart := strings.Index(markdown, "<!-- jira-adf:begin "+regions[1].ID)
	betaStart := strings.Index(markdown, "<!-- jira-adf:begin "+regions[2].ID)
	if panelStart < 0 || betaStart <= panelStart {
		t.Fatal("export lost its source region identities")
	}
	alpha := strings.Replace(markdown[:panelStart], "\nalpha\n", "\n# edited heading\n\nlocal paragraph\n", 1)
	local := markdown[panelStart:betaStart] + alpha + markdown[betaStart:]
	remoteBody := []byte(strings.NewReplacer(
		`"warning"`, `"info"`,
		`"rich content"`, `"remote rich content"`,
		`"beta"`, `"remote beta"`,
	).Replace(string(original)))
	endpoint, path := r.rawCommentInput(t, key, id, remoteBody)
	updated, frame, exit := rawMutation(t, r, key, nil, "api", "put", endpoint, "--input", path, "--force")
	requireRawSuccess(t, updated, frame, exit, 200)
	current := r.success(t, "issue", "comment", "list", key, "--all")
	if err := verifyStoredADFComments(map[string]json.RawMessage{id: remoteBody}, current); err != nil {
		t.Fatal(err)
	}
	currentDoc, err := adf.ParseDocument(current.Data.Comments[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	conflicting := strings.Replace(local, "\nbeta\n", "\nlocal beta\n", 1)
	candidate, err := session.Merge(conflicting, currentDoc)
	var conflict *adf.EditError
	if candidate != nil || !errors.As(err, &conflict) || conflict.Kind != adf.EditMergeConflict {
		t.Fatal("concurrent paragraph replacement did not refuse a conflicting local edit")
	}
	merged, err := session.Merge(local, currentDoc)
	if err != nil {
		t.Fatal(err)
	}
	data, err := merged.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// Independent expected structure pins both actors' results, rather than
	// merely comparing Jira's response to whatever the merge happened to emit.
	expected := []byte(fmt.Sprintf(`{"version":1,"type":"doc","content":[{"type":"panel","attrs":{"panelType":"info","localId":%q},"content":[{"type":"paragraph","content":[{"type":"text","text":"remote rich content"}]}]},{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"edited heading"}]},{"type":"paragraph","content":[{"type":"text","text":"local paragraph"}]},{"type":"paragraph","content":[{"type":"text","text":"remote beta"}]}]}`, panelID))
	equal, err := adf.StructuralEqual(expected, data)
	if err != nil || !equal {
		t.Fatal("merge did not retain the current rich content and disjoint paragraph edit alongside local structural changes")
	}
	endpoint, path = r.rawCommentInput(t, key, id, data)
	written, frame, exit := rawMutation(t, r, key, nil, "api", "put", endpoint, "--input", path, "--force")
	requireRawSuccess(t, written, frame, exit, 200)
	final := r.success(t, "issue", "comment", "list", key, "--all")
	if err := verifyStoredADFComments(map[string]json.RawMessage{id: expected}, final); err != nil {
		t.Fatal(err)
	}
}

// This probes local editing primitives with native Jira storage. It does not
// claim acceptance of an issue editor or optimistic write concurrency.
func TestLiveADFAnchoredEdits(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "anchored-adf-edit")
	original := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"before"}]},{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"preserved rich content"}]}]},{"type":"paragraph","content":[{"type":"text","text":"after"}]}]}`)
	r.mutate(t, []string{key}, "issue", "comment", "add", key, "--json-input", r.nativeBody(t, original))
	stored := r.success(t, "issue", "comment", "list", key, "--all")
	if len(stored.Data.Comments) != 1 {
		t.Fatal("source comment was not stored exactly once")
	}
	source, err := adf.ParseDocument(stored.Data.Comments[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	session, err := adf.PrepareEdit(source)
	if err != nil {
		t.Fatal(err)
	}
	regions := session.Regions()
	if len(regions) != 3 || regions[0].Protected || !regions[1].Protected || regions[2].Protected {
		t.Fatal("stored source does not expose the expected editable and protected regions")
	}
	markdown := session.Markdown()
	panelStart := strings.Index(markdown, "<!-- jira-adf:begin "+regions[1].ID)
	lastStart := strings.Index(markdown, "<!-- jira-adf:begin "+regions[2].ID)
	if panelStart < 0 || lastStart <= panelStart {
		t.Fatal("source export lost its region identities")
	}
	// Drop the first editable region, move the last before the protected panel,
	// replace it with a heading/link and insert a paragraph outside the anchors.
	last := strings.Replace(markdown[lastStart:], "\nafter\n", "\n# edited heading\n\n[documentation](https://example.test/guide \"important title\")\n", 1)
	edited := "inserted paragraph\n\n" + last + markdown[panelStart:lastStart]
	candidate, err := session.Apply(edited)
	if err != nil {
		t.Fatal(err)
	}
	want, err := candidate.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	posted := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--json-input", r.nativeBody(t, want))
	// Exercise the actual rebuilt Markdown converter independently of the
	// in-process edit engine, then inspect server storage for its title attr.
	binaryPosted := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--markdown", `[binary link](https://example.test/guide "binary title")`)
	final := r.success(t, "issue", "comment", "list", key, "--all")
	expected := map[string]json.RawMessage{
		stored.Data.Comments[0].ID:          stored.Data.Comments[0].Body,
		postedADFCommentID(t, posted):       want,
		postedADFCommentID(t, binaryPosted): []byte(liveBinaryTitleADF),
	}
	if len(expected) != 3 {
		t.Fatal("posted comments did not have distinct identities")
	}
	if err := verifyStoredADFComments(expected, final); err != nil {
		t.Fatal(err)
	}
}

// Pin the actual title attribute independently of the converter under test.
const liveBinaryTitleADF = `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"binary link","marks":[{"type":"link","attrs":{"href":"https://example.test/guide","title":"binary title"}}]}]}]}`

func postedADFCommentID(t *testing.T, frame liveFrame) string {
	t.Helper()
	if len(frame.Data.Results) != 1 || !frame.Data.Results[0].OK {
		t.Fatal("comment post did not report exactly one successful target")
	}
	var result struct {
		Comment struct {
			ID string `json:"id"`
		} `json:"comment"`
	}
	if err := json.Unmarshal(frame.Data.Results[0].Data, &result); err != nil || result.Comment.ID == "" {
		t.Fatal("posted comment identity is missing")
	}
	return result.Comment.ID
}

func verifyStoredADFComments(expected map[string]json.RawMessage, frame liveFrame) error {
	if len(frame.Data.Comments) != len(expected) {
		return fmt.Errorf("stored comment count differs from authored identities")
	}
	seen := make(map[string]bool)
	for _, comment := range frame.Data.Comments {
		want, exists := expected[comment.ID]
		if !exists || seen[comment.ID] {
			return fmt.Errorf("stored comments contain a foreign or repeated identity")
		}
		equal, err := adf.StructuralEqual(want, comment.Body)
		if err != nil || !equal {
			return fmt.Errorf("stored comment differs from its complete authored structure")
		}
		seen[comment.ID] = true
	}
	return nil
}

func TestLiveADFEditStorageGuard(t *testing.T) {
	source := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"source"}]}]}`
	candidate := `{"type":"doc","version":1,"content":[{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"candidate"}]}]}`
	expected := map[string]json.RawMessage{"source": []byte(source), "candidate": []byte(candidate), "binary": []byte(liveBinaryTitleADF)}
	complete := `[{"id":"source","body":` + source + `},{"id":"candidate","body":` + candidate + `},{"id":"binary","body":` + liveBinaryTitleADF + `}]`
	for name, comments := range map[string]string{
		"missing comment":         `[{"id":"source","body":` + source + `},{"id":"binary","body":` + liveBinaryTitleADF + `}]`,
		"complete":                complete,
		"missing source":          `[{"id":"candidate","body":` + candidate + `},{"id":"candidate","body":` + candidate + `},{"id":"binary","body":` + liveBinaryTitleADF + `}]`,
		"foreign identity":        strings.Replace(complete, `"id":"source"`, `"id":"foreign"`, 1),
		"title only in text":      strings.Replace(complete, `"text":"binary link"`, `"text":"binary title"`, 1),
		"missing title attribute": strings.Replace(complete, `,"title":"binary title"`, ``, 1),
		"different href":          strings.Replace(complete, "https://example.test/guide", "https://wrong.example.test", 1),
	} {
		t.Run(name, func(t *testing.T) {
			var frame liveFrame
			if err := json.Unmarshal([]byte(`{"data":{"comments":`+comments+`}}`), &frame); err != nil {
				t.Fatal(err)
			}
			err := verifyStoredADFComments(expected, frame)
			if (err == nil) != (name == "complete") {
				t.Fatalf("storage guard accepted invalid facts or refused valid facts: %v", err)
			}
		})
	}
}

func TestLiveADFStatusColors(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "native-status-colors")
	ids := make(map[string]string)
	for _, color := range []string{"neutral", "#a1B2c3"} {
		document := []byte(fmt.Sprintf(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"status","attrs":{"text":%q,"color":%q}}]}]}`, r.manifest.RunID, color))
		path := r.nativeBody(t, document)
		posted := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--json-input", path)
		if len(posted.Data.Results) != 1 || !posted.Data.Results[0].OK {
			t.Fatal("native ADF comment did not report its owned target")
		}
		var result struct {
			Comment struct {
				ID   string          `json:"id"`
				Body json.RawMessage `json:"body"`
			} `json:"comment"`
		}
		if err := json.Unmarshal(posted.Data.Results[0].Data, &result); err != nil {
			t.Fatal(err)
		}
		if result.Comment.ID == "" || statusColor(t, result.Comment.Body) != color {
			t.Fatal("Jira did not return the authored status color and comment identity")
		}
		ids[result.Comment.ID] = color
	}
	stored := r.success(t, "issue", "comment", "list", key, "--all")
	if len(stored.Data.Comments) != len(ids) {
		t.Fatal("owned native comments were not stored exactly once")
	}
	for _, comment := range stored.Data.Comments {
		color, found := ids[comment.ID]
		if !found || statusColor(t, comment.Body) != color {
			t.Fatal("stored comment lost its authored status color")
		}
	}
}

func statusColor(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var doc struct {
		Content []struct {
			Content []struct {
				Type  string `json:"type"`
				Attrs struct {
					Color string `json:"color"`
				} `json:"attrs"`
			} `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Content) != 1 || len(doc.Content[0].Content) != 1 || doc.Content[0].Content[0].Type != "status" {
		t.Fatal("stored native body is not the expected status document")
	}
	return doc.Content[0].Content[0].Attrs.Color
}
