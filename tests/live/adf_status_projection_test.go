//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestLiveADFStatusRoundTrip(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "native-status-roundtrip")
	// Native expectations are independent of the converter. The source covers
	// URI adjacency, named/hex colors, IDs, styles and explicit empty values.
	const original = `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"https://example.test/"},{"type":"status","attrs":{"text":"READY &amp; 雪","color":"#12abEF","localId":"first-id","style":"bold"}},{"type":"text","text":"after"}]},{"type":"paragraph","content":[{"type":"status","attrs":{"text":"OTHER","color":"green","localId":"second-id","style":""}}]},{"type":"paragraph","content":[{"type":"status","attrs":{"text":"EMPTY","color":"blue","localId":"","style":""}}]},{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"status","attrs":{"text":"PANEL","color":"purple","localId":"panel-id","style":"bold"}}]}]}]}`
	if err := adf.Validate([]byte(original)); err != nil {
		t.Fatal("invalid independent native status fixture", err)
	}
	storeDescription := func(body []byte) {
		endpoint, path := r.rawDescriptionInput(t, key, body)
		updated, frame, exit := rawMutation(t, r, key, nil, "api", "put", endpoint, "--input", path, "--force")
		requireRawSuccess(t, updated, frame, exit, 204)
	}
	readDescription := func(expected []byte) []byte {
		native := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=description")
		var issue struct {
			Fields struct{ Description json.RawMessage }
		}
		if err := json.Unmarshal(native.Response.JSON, &issue); err != nil {
			t.Fatal(err)
		}
		equal, err := adf.StructuralEqual(expected, issue.Fields.Description)
		if err != nil || !equal {
			t.Fatal("stored native status differs from independent expectation", err)
		}
		assertStoredPreconditionBody(t, r, key, "", "description", expected)
		return issue.Fields.Description
	}
	storeDescription([]byte(original))
	stored := readDescription([]byte(original))
	_, rendered, exit := r.invoke(t, t.Context(), nil, "adf", "render", "--input", r.nativeBody(t, stored))
	var projection struct{ Data envelope.ADFRenderOutput }
	if exit != 0 || json.Unmarshal(rendered, &projection) != nil || projection.Data.Lossy || len(projection.Data.LossyConstructs) != 0 {
		t.Fatal("stored native status lacks a proved binary inverse")
	}
	markdownPath := filepath.Join(r.dir, "status-projection.md")
	if err := os.WriteFile(markdownPath, []byte(projection.Data.Markdown), 0o600); err != nil {
		t.Fatal(err)
	}
	_, candidate, exit := r.invoke(t, t.Context(), nil, "adf", "convert", "--input", markdownPath)
	var converted struct{ Data envelope.ADFConvertOutput }
	if exit != 0 || json.Unmarshal(candidate, &converted) != nil || converted.Data.Lossy || len(converted.Data.Diagnostics) != 0 || adf.Validate(converted.Data.Document) != nil {
		t.Fatal("strict binary status conversion failed")
	}
	equal, err := adf.StructuralEqual([]byte(original), converted.Data.Document)
	if err != nil || !equal {
		t.Fatal("binary status round trip lost native fields", err)
	}
	storeDescription(converted.Data.Document)
	readDescription([]byte(original))
	doc, err := adf.ParseDocument(stored)
	if err != nil {
		t.Fatal(err)
	}
	session, err := adf.PrepareEdit(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, region := range session.Regions() {
		if region.Protected {
			t.Fatal("stored representable status remained protected")
		}
	}
	unchanged, err := session.Apply(session.Markdown())
	if err != nil {
		t.Fatal(err)
	}
	unchangedBytes, err := unchanged.Marshal()
	if err != nil || !bytes.Equal(stored, unchangedBytes) {
		t.Fatal("unchanged stored status bytes changed", err)
	}
	localMarkdown := strings.Replace(session.Markdown(), "READY", "LOCAL", 1)
	local, err := session.Apply(localMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	localBytes, err := local.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	localExpected := strings.Replace(original, "READY", "LOCAL", 1)
	equal, err = adf.StructuralEqual([]byte(localExpected), localBytes)
	if err != nil || !equal {
		t.Fatal("local status edit lost native fields", err)
	}
	storeDescription(localBytes)
	readDescription([]byte(localExpected))

	// Serial owned writes exercise disjoint merge/storage. They do not prove
	// server-side atomic preconditions or a delivered issue editor.
	remoteExpected := strings.Replace(original, `"OTHER"`, `"REMOTE"`, 1)
	storeDescription([]byte(remoteExpected))
	remote, err := adf.ParseDocument(readDescription([]byte(remoteExpected)))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := session.Merge(localMarkdown, remote)
	if err != nil {
		t.Fatal("disjoint stored status merge refused", err)
	}
	mergedBytes, err := merged.Marshal()
	if err != nil || adf.Validate(mergedBytes) != nil {
		t.Fatal("merged status candidate is invalid", err)
	}
	mergedExpected := strings.Replace(remoteExpected, "READY", "LOCAL", 1)
	equal, err = adf.StructuralEqual([]byte(mergedExpected), mergedBytes)
	if err != nil || !equal {
		t.Fatal("status merge lost independent native fields", err)
	}
	storeDescription(mergedBytes)
	readDescription([]byte(mergedExpected))
	conflicting, err := adf.ParseDocument([]byte(strings.Replace(original, "READY", "CONFLICT", 1)))
	if err != nil {
		t.Fatal(err)
	}
	refused, err := session.Merge(localMarkdown, conflicting)
	var conflict *adf.EditError
	if refused != nil || !errors.As(err, &conflict) || conflict.Kind != adf.EditMergeConflict {
		t.Fatal("concurrent status label edits lost local conflict refusal", err)
	}
	posted := r.mutate(t, []string{key}, "issue", "comment", "add", key, "--json-input", r.nativeBody(t, mergedBytes))
	id := postedADFCommentID(t, posted)
	r.registerComment(t, key, id)
	comment := rawOwnedComment(t, r, key, id)
	equal, err = adf.StructuralEqual([]byte(mergedExpected), comment.Body)
	if err != nil || !equal {
		t.Fatal("native status comment storage changed", err)
	}
	assertStoredPreconditionBody(t, r, key, id, "comment", []byte(mergedExpected))
}
