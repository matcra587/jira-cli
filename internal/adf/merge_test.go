package adf_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
)

func mergeDocument(t *testing.T, labels ...string) *adf.Document {
	t.Helper()
	doc, warnings, err := adf.FromMarkdownLossy(strings.Join(labels, "\n\n"))
	if err != nil || len(warnings) != 0 {
		t.Fatalf("invalid authored merge fixture: %v, %+v", err, warnings)
	}
	return doc
}

func mergeMarkdown(t *testing.T, session *adf.EditSession, order []int, replacements map[int]string) string {
	t.Helper()
	var result strings.Builder
	for _, i := range order {
		chunk := editChunk(t, session, i)
		if replacement, exists := replacements[i]; exists {
			start := strings.Index(chunk, "-->\n") + len("-->\n")
			end := strings.Index(chunk, "<!-- jira-adf:end")
			chunk = chunk[:start] + "\n" + replacement + "\n\n" + chunk[end:]
		}
		result.WriteString(chunk)
	}
	return result.String()
}

func requireMergedStructure(t *testing.T, session *adf.EditSession, markdown string, current, expected *adf.Document) *adf.Document {
	t.Helper()
	currentBytes := editBytes(t, current)
	merged, err := session.Merge(markdown, current)
	if err != nil {
		t.Fatal(err)
	}
	equal, err := adf.StructuralEqual(editBytes(t, expected), editBytes(t, merged))
	if err != nil || !equal {
		t.Fatalf("wrong three-way result: %s; expected %s; %v", editBytes(t, merged), editBytes(t, expected), err)
	}
	if !bytes.Equal(currentBytes, editBytes(t, current)) {
		t.Fatal("merging mutated the current document")
	}
	return merged
}

func TestMergeCombinesDisjointStructuralChanges(t *testing.T) {
	for _, tc := range []struct {
		name         string
		base         []string
		current      []string
		order        []int
		replacements map[int]string
		expected     []string
	}{
		{name: "local text only", base: []string{"alpha", "beta"}, current: []string{"alpha", "beta"}, order: []int{0, 1}, replacements: map[int]string{0: "local alpha"}, expected: []string{"local alpha", "beta"}},
		{name: "disjoint text", base: []string{"alpha", "beta"}, current: []string{"alpha", "remote beta"}, order: []int{0, 1}, replacements: map[int]string{0: "local alpha"}, expected: []string{"local alpha", "remote beta"}},
		{name: "structural replacement and remote text", base: []string{"alpha", "beta", "gamma"}, current: []string{"alpha", "beta", "remote gamma"}, order: []int{0, 1, 2}, replacements: map[int]string{0: "# heading\n\nnew paragraph"}, expected: []string{"# heading", "new paragraph", "beta", "remote gamma"}},
		{name: "disjoint deletes", base: []string{"alpha", "beta", "gamma", "delta"}, current: []string{"alpha", "beta", "gamma"}, order: []int{1, 2, 3}, expected: []string{"beta", "gamma"}},
		{name: "disjoint reorder", base: []string{"alpha", "beta", "gamma", "delta"}, current: []string{"alpha", "beta", "delta", "gamma"}, order: []int{1, 0, 2, 3}, expected: []string{"beta", "alpha", "delta", "gamma"}},
		{name: "remote move and local value", base: []string{"alpha", "beta", "gamma"}, current: []string{"beta", "alpha", "gamma"}, order: []int{0, 1, 2}, replacements: map[int]string{0: "local alpha"}, expected: []string{"beta", "local alpha", "gamma"}},
		{name: "remote move with no local edit", base: []string{"alpha", "beta"}, current: []string{"beta", "alpha"}, order: []int{0, 1}, expected: []string{"beta", "alpha"}},
		{name: "same text change", base: []string{"alpha", "beta"}, current: []string{"changed", "beta"}, order: []int{0, 1}, replacements: map[int]string{0: "changed"}, expected: []string{"changed", "beta"}},
		{name: "same reorder", base: []string{"alpha", "beta", "gamma"}, current: []string{"gamma", "alpha", "beta"}, order: []int{2, 0, 1}, expected: []string{"gamma", "alpha", "beta"}},
		{name: "local deletion remote insertion elsewhere", base: []string{"alpha", "beta", "gamma"}, current: []string{"remote intro", "alpha", "beta", "gamma"}, order: []int{0, 2}, expected: []string{"remote intro", "alpha", "gamma"}},
		{name: "local replacement remote insertion", base: []string{"alpha", "beta"}, current: []string{"alpha", "remote insertion", "beta"}, order: []int{0, 1}, replacements: map[int]string{0: "# heading\n\nexpanded"}, expected: []string{"# heading", "expanded", "remote insertion", "beta"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := mergeDocument(t, tc.base...)
			session, err := adf.PrepareEdit(base)
			if err != nil {
				t.Fatal(err)
			}
			markdown := mergeMarkdown(t, session, tc.order, tc.replacements)
			requireMergedStructure(t, session, markdown, mergeDocument(t, tc.current...), mergeDocument(t, tc.expected...))
		})
	}
}

func TestMergeConcurrentInsertionsRemainDistinctAndOrdered(t *testing.T) {
	for _, name := range []string{"same gap", "identical same gap", "beginning", "end", "empty document", "identical empty document"} {
		t.Run(name, func(t *testing.T) {
			base := mergeDocument(t, "alpha", "beta")
			if name == "empty document" || name == "identical empty document" {
				base = mergeDocument(t)
			}
			session, err := adf.PrepareEdit(base)
			if err != nil {
				t.Fatal(err)
			}
			var markdown string
			var current, expected *adf.Document
			switch name {
			case "same gap":
				markdown = editChunk(t, session, 0) + "local one\n\nlocal two\n\n" + editChunk(t, session, 1)
				current = mergeDocument(t, "alpha", "remote one", "remote two", "beta")
				expected = mergeDocument(t, "alpha", "remote one", "remote two", "local one", "local two", "beta")
			case "identical same gap":
				markdown = editChunk(t, session, 0) + "same insertion\n\n" + editChunk(t, session, 1)
				current = mergeDocument(t, "alpha", "same insertion", "beta")
				expected = mergeDocument(t, "alpha", "same insertion", "same insertion", "beta")
			case "beginning":
				markdown = "local intro\n\n" + session.Markdown()
				current = mergeDocument(t, "remote intro", "alpha", "beta")
				expected = mergeDocument(t, "remote intro", "local intro", "alpha", "beta")
			case "end":
				markdown = session.Markdown() + "local end\n"
				current = mergeDocument(t, "alpha", "beta", "remote end")
				expected = mergeDocument(t, "alpha", "beta", "remote end", "local end")
			case "empty document":
				markdown = "local content"
				current = mergeDocument(t, "remote content")
				expected = mergeDocument(t, "remote content", "local content")
			case "identical empty document":
				markdown = "same insertion"
				current = mergeDocument(t, "same insertion")
				expected = mergeDocument(t, "same insertion", "same insertion")
			}
			requireMergedStructure(t, session, markdown, current, expected)
		})
	}
}

func TestMergeRejectsConflictingEditsWithoutCandidate(t *testing.T) {
	for _, tc := range []struct {
		name         string
		current      []string
		order        []int
		replacements map[int]string
		insertion    string
		kind         adf.EditErrorKind
	}{
		{name: "edit edit", current: []string{"remote alpha", "beta", "gamma"}, order: []int{0, 1, 2}, replacements: map[int]string{0: "local alpha"}, kind: adf.EditMergeConflict},
		{name: "local edit remote delete", current: []string{"beta", "gamma"}, order: []int{0, 1, 2}, replacements: map[int]string{0: "local alpha"}, kind: adf.EditMergeConflict},
		{name: "local delete remote edit", current: []string{"remote alpha", "beta", "gamma"}, order: []int{1, 2}, kind: adf.EditAmbiguousIdentity},
		{name: "local move remote delete", current: []string{"alpha", "gamma"}, order: []int{1, 0, 2}, kind: adf.EditMergeConflict},
		{name: "local delete remote move", current: []string{"beta", "alpha", "gamma"}, order: []int{0, 2}, kind: adf.EditMergeConflict},
		{name: "cyclic moves", current: []string{"alpha", "gamma", "beta"}, order: []int{1, 0, 2}, kind: adf.EditMergeConflict},
		{name: "insertion crossed by remote move", current: []string{"beta", "alpha", "gamma"}, order: []int{0, 1, 2}, insertion: "local insertion", kind: adf.EditMergeConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := adf.PrepareEdit(mergeDocument(t, "alpha", "beta", "gamma"))
			if err != nil {
				t.Fatal(err)
			}
			markdown := mergeMarkdown(t, session, tc.order, tc.replacements)
			if tc.insertion != "" {
				markdown = editChunk(t, session, 0) + tc.insertion + "\n\n" + editChunk(t, session, 1) + editChunk(t, session, 2)
			}
			candidate, err := session.Merge(markdown, mergeDocument(t, tc.current...))
			var editErr *adf.EditError
			if candidate != nil || !errors.As(err, &editErr) || editErr.Kind != tc.kind {
				t.Fatalf("conflict produced a candidate or wrong error: %v", err)
			}
			if name := tc.name; (name == "edit edit" || name == "local edit remote delete") && editErr.Region != session.Regions()[0].ID {
				t.Fatal("conflict did not identify the authored source region")
			}
		})
	}
}

func TestMergePreservesCurrentOpaqueStateAndRootMetadata(t *testing.T) {
	_, session := prepareEdit(t, editFixture)
	remoteSource := strings.Replace(editFixture, `"rich content"`, `"remote rich content"`, 1)
	remoteSource = strings.Replace(remoteSource, `"id":9007199254740993`, `"id":9007199254740995`, 1)
	remoteSource = strings.Replace(remoteSource, `"panelType":"warning"`, `"panelType":"info"`, 1)
	current, err := adf.ParseDocument([]byte(remoteSource))
	if err != nil {
		t.Fatal(err)
	}
	markdown := mergeMarkdown(t, session, []int{0, 1, 2}, map[int]string{0: "# edited heading"})
	merged, err := session.Merge(markdown, current)
	if err != nil {
		t.Fatal(err)
	}
	if content := merged.Content(); len(content) != 3 || content[0].Type() != "heading" || content[1].Type() != "panel" {
		t.Fatalf("wrong merged structure: %s", editBytes(t, merged))
	}
	want, err := current.Content()[1].MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	got, err := merged.Content()[1].MarshalJSON()
	if err != nil || !bytes.Equal(want, got) {
		t.Fatalf("current rich subtree bytes were overwritten: %s, %v", got, err)
	}
	future, ok := merged.Field("future")
	if !ok || string(future) != `{"id":9007199254740995}` {
		t.Fatalf("current root metadata lost precision or was overwritten: %s", future)
	}
	if !bytes.Equal(editBytes(t, current), []byte(remoteSource)) {
		t.Fatal("merge mutated current native input")
	}
}

func TestMergePreservesInsertedFutureNodesWithoutSchemaValidation(t *testing.T) {
	_, session := prepareEdit(t, editFixture)
	future := `{"type":"futureRemote","attrs":{"id":9007199254740997},"future":{"markup":"<>&\u2028"}}`
	remoteSource := strings.Replace(editFixture, `"content":[`, `"content":[`+future+`,`, 1)
	current, err := adf.ParseDocument([]byte(remoteSource))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := session.Merge(mergeMarkdown(t, session, []int{0, 1, 2}, map[int]string{2: "edited after"}), current)
	if err != nil {
		t.Fatal(err)
	}
	if content := merged.Content(); len(content) != 4 || content[0].Type() != "futureRemote" || content[3].Content()[0].Text() != "edited after" {
		t.Fatalf("future insertion or local change lost: %s", editBytes(t, merged))
	}
	got, err := merged.Content()[0].MarshalJSON()
	if err != nil || string(got) != future {
		t.Fatalf("future native bytes changed: %s, %v", got, err)
	}
}

func TestMergeRepeatedSubtreesNeedProvenCorrespondence(t *testing.T) {
	base := mergeDocument(t, "same", "same", "other")
	session, err := adf.PrepareEdit(base)
	if err != nil {
		t.Fatal(err)
	}
	markdown := mergeMarkdown(t, session, []int{0, 1, 2}, map[int]string{0: "first changed"})
	requireMergedStructure(t, session, markdown, base, mergeDocument(t, "first changed", "same", "other"))
	for _, labels := range [][]string{
		{"same", "same", "other", "remote insertion"},
		{"same", "other", "remote insertion"},
	} {
		current := mergeDocument(t, labels...)
		candidate, err := session.Merge(markdown, current)
		var editErr *adf.EditError
		if candidate != nil || !errors.As(err, &editErr) || editErr.Kind != adf.EditAmbiguousIdentity {
			t.Fatalf("partial matching guessed repeated region identity: %v", err)
		}
	}
}

func TestMergeCurrentRawSpellingWinsForUnchangedRegions(t *testing.T) {
	_, session := prepareEdit(t, editFixture)
	remoteSource := strings.Replace(editFixture, `"type":"panel","attrs":`, `"attrs":`, 1)
	remoteSource = strings.Replace(remoteSource, `"panelType":"warning","localId":"keep"}`, `"panelType":"warning","localId":"keep"},"type":"panel"`, 1)
	remoteSource = strings.Replace(remoteSource, `"<>&\u2028"`, `"\u003c>&\u2028"`, 1)
	remoteSource = strings.Replace(remoteSource, `"version":1,"type":"doc"`, `"type":"doc","version":1`, 1)
	current, err := adf.ParseDocument([]byte(remoteSource))
	if err != nil {
		t.Fatal(err)
	}
	merged, err := session.Merge(mergeMarkdown(t, session, []int{0, 1, 2}, map[int]string{0: "edited"}), current)
	if err != nil {
		t.Fatal(err)
	}
	want, err := current.Content()[1].MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	got, err := merged.Content()[1].MarshalJSON()
	if err != nil || !bytes.Equal(want, got) {
		t.Fatalf("current raw spelling was replaced by base bytes: %s, %v", got, err)
	}
	if !strings.HasPrefix(string(editBytes(t, merged)), `{"type":"doc","version":1,`) {
		t.Fatal("current root field order was replaced")
	}
}

func TestMergeRejectsAmbiguousCurrentNativeIdentities(t *testing.T) {
	_, session := prepareEdit(t, editFixture)
	for _, inserted := range []string{
		`{"type":"future","attrs":{"localId":"keep"}},`,
		`{"type":"futureOne","attrs":{"localId":"new"}},{"type":"futureTwo","attrs":{"localId":"new"}},`,
	} {
		currentSource := strings.Replace(editFixture, `"content":[`, `"content":[`+inserted, 1)
		current, err := adf.ParseDocument([]byte(currentSource))
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := session.Merge(mergeMarkdown(t, session, []int{0, 1, 2}, map[int]string{0: "edited"}), current)
		var editErr *adf.EditError
		if candidate != nil || !errors.As(err, &editErr) || editErr.Kind != adf.EditAmbiguousIdentity {
			t.Fatalf("duplicate native identities were accepted: %v", err)
		}
	}
}

func TestMergeRejectsAmbiguousSourceNativeIdentities(t *testing.T) {
	base := strings.Replace(editFixture, `"content":[`, `"content":[{"type":"future","attrs":{"localId":"keep"}},`, 1)
	_, session := prepareEdit(t, base)
	current, err := adf.ParseDocument([]byte(strings.Replace(editFixture, `"rich content"`, `"remote content"`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := session.Merge(session.Markdown(), current)
	var editErr *adf.EditError
	if candidate != nil || !errors.As(err, &editErr) || editErr.Kind != adf.EditAmbiguousIdentity {
		t.Fatalf("duplicate source native identities were accepted: %v", err)
	}
}

func TestMergeRejectsInvalidCurrentAndInvalidLocalInput(t *testing.T) {
	_, session := prepareEdit(t, editFixture)
	for name, source := range map[string]string{
		"future version": `{"type":"doc","version":2,"content":[]}`,
		"duplicate root": `{"type":"doc","version":1,"version":1,"content":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			current, err := adf.ParseDocument([]byte(source))
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := session.Merge(session.Markdown(), current)
			var editErr *adf.EditError
			if candidate != nil || !errors.As(err, &editErr) || editErr.Kind != adf.EditInvalidSource {
				t.Fatalf("invalid current snapshot accepted: %v", err)
			}
		})
	}
	if candidate, err := session.Merge(session.Markdown(), nil); candidate != nil || err == nil {
		t.Fatal("nil current snapshot accepted")
	}
	current, err := adf.ParseDocument([]byte(editFixture))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := session.Merge("", current)
	var editErr *adf.EditError
	if candidate != nil || !errors.As(err, &editErr) || editErr.Kind != adf.EditProtectedRegion {
		t.Fatalf("merge bypassed local protected-region checks: %v", err)
	}
}

func TestMergeSnapshotsAndResultsAreIndependent(t *testing.T) {
	_, session := prepareEdit(t, editFixture)
	current, err := adf.ParseDocument([]byte(editFixture))
	if err != nil {
		t.Fatal(err)
	}
	first, err := session.Merge(session.Markdown(), current)
	if err != nil {
		t.Fatal(err)
	}
	first.Content()[0].Content()[0].SetText("mutated result")
	if string(editBytes(t, current)) != editFixture {
		t.Fatal("no-op result aliases current input")
	}
	second, err := session.Merge(session.Markdown(), current)
	if err != nil || string(editBytes(t, second)) != editFixture {
		t.Fatalf("result mutation corrupted session snapshot: %v", err)
	}
}

func TestMergeChangedResultsNeverAliasCurrentOrSession(t *testing.T) {
	_, session := prepareEdit(t, editFixture)
	remoteSource := strings.Replace(editFixture, `"rich content"`, `"remote content"`, 1)
	current, err := adf.ParseDocument([]byte(remoteSource))
	if err != nil {
		t.Fatal(err)
	}
	markdown := mergeMarkdown(t, session, []int{0, 1, 2}, map[int]string{0: "# local heading"})
	first, err := session.Merge(markdown, current)
	if err != nil {
		t.Fatal(err)
	}
	first.Content()[0].Content()[0].SetText("mutated local result")
	first.Content()[1].Content()[0].Content()[0].SetText("mutated current result")
	if string(editBytes(t, current)) != remoteSource {
		t.Fatal("merged result aliases current input")
	}
	second, err := session.Merge(markdown, current)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(editBytes(t, second)), "mutated") {
		t.Fatal("merged result aliases the source session")
	}
}

func TestMergeKeepsCurrentRawBytesAfterIdentityChecks(t *testing.T) {
	base := mergeDocument(t, "alpha")
	session, err := adf.PrepareEdit(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`{"future":{"id":9007199254740995,"text":"\u003cremote\u003e"},"version":1,"type":"doc"}`,
		`{"future":{"id":9007199254740995,"text":"\u003cremote\u003e"},"content":null,"version":1,"type":"doc"}`,
	} {
		current, err := adf.ParseDocument([]byte(source))
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := session.Merge(session.Markdown(), current)
		if err != nil {
			t.Fatal(err)
		}
		if string(editBytes(t, candidate)) != source {
			t.Fatalf("semantic no-op changed current raw document: %s", editBytes(t, candidate))
		}
	}
}

func TestMergeNeverDropsAnUnmatchedProtectedRegion(t *testing.T) {
	_, session := prepareEdit(t, editFixture)
	current := mergeDocument(t, "before", "after")
	markdown := mergeMarkdown(t, session, []int{0, 1, 2}, map[int]string{0: "edited before"})
	candidate, err := session.Merge(markdown, current)
	var editErr *adf.EditError
	if candidate != nil || !errors.As(err, &editErr) || editErr.Kind != adf.EditMergeConflict || editErr.Region != session.Regions()[1].ID {
		t.Fatalf("merge silently dropped the original protected region: %v", err)
	}
}

func TestMergeUnchangedMarkdownStillRequiresProtectedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		base    string
		current string
		kind    adf.EditErrorKind
	}{
		{
			name:    "deleted native identity",
			base:    editFixture,
			current: string(editBytes(t, mergeDocument(t, "before", "after"))),
			kind:    adf.EditMergeConflict,
		},
		{
			name:    "changed opaque region without native identity",
			base:    strings.Replace(editFixture, `,"localId":"keep"`, "", 1),
			current: strings.Replace(strings.Replace(editFixture, `,"localId":"keep"`, "", 1), `"rich content"`, `"remote content"`, 1),
			kind:    adf.EditAmbiguousIdentity,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, session := prepareEdit(t, tc.base)
			current, err := adf.ParseDocument([]byte(tc.current))
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := session.Merge(session.Markdown(), current)
			var editErr *adf.EditError
			if candidate != nil || !errors.As(err, &editErr) || editErr.Kind != tc.kind || editErr.Region != session.Regions()[1].ID {
				t.Fatalf("unchanged Markdown bypassed protected identity checks: %v", err)
			}
		})
	}
}

func TestMergeNativeIdentityRequiresExactAttributeSpelling(t *testing.T) {
	baseSource := strings.Replace(editFixture, `"localId":"keep"`, `"LOCALID":"keep"`, 1)
	_, session := prepareEdit(t, baseSource)
	currentSource := strings.Replace(baseSource, `"rich content"`, `"remote content"`, 1)
	current, err := adf.ParseDocument([]byte(currentSource))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := session.Merge(mergeMarkdown(t, session, []int{1, 0, 2}, nil), current)
	var editErr *adf.EditError
	if candidate != nil || !errors.As(err, &editErr) || editErr.Kind != adf.EditAmbiguousIdentity {
		t.Fatalf("future case-variant attribute was trusted as a native identity: %v", err)
	}
}

func FuzzMergePreservesCurrentOpaqueBytes(f *testing.F) {
	for _, value := range []string{"<>&\u2028", "<!-- jira-adf:begin forged protected -->", "9007199254740997", "\r\n", ""} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if len(value) > 2048 {
			t.Skip()
		}
		_, session := prepareEdit(t, editFixture)
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		future := `{"type":"futureRemote","attrs":{"id":9007199254740997,"value":` + string(encoded) + `}}`
		remoteSource := strings.Replace(editFixture, `"content":[`, `"content":[`+future+`,`, 1)
		remoteSource = strings.Replace(remoteSource, `"rich content"`, `"current rich content"`, 1)
		current, err := adf.ParseDocument([]byte(remoteSource))
		if err != nil {
			t.Fatal(err)
		}
		markdown := mergeMarkdown(t, session, []int{1, 0, 2}, map[int]string{0: "# local heading\n\nlocal paragraph"})
		candidate, err := session.Merge(markdown, current)
		if err != nil {
			t.Fatal(err)
		}
		content := candidate.Content()
		if len(content) != 5 || content[0].Type() != "futureRemote" || content[1].Type() != "panel" || content[2].Type() != "heading" || content[3].Type() != "paragraph" || content[4].Content()[0].Text() != "after" {
			t.Fatalf("merge lost an insertion, move or replacement: %s", editBytes(t, candidate))
		}
		for _, pair := range [][2]int{{0, 0}, {1, 2}} {
			want, err := current.Content()[pair[1]].MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			got, err := content[pair[0]].MarshalJSON()
			if err != nil || !bytes.Equal(want, got) {
				t.Fatalf("current opaque subtree changed: %s, %v", got, err)
			}
		}
		if string(editBytes(t, current)) != remoteSource {
			t.Fatal("merge mutated current input")
		}
	})
}
