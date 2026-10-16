//go:build live

package live

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/adf"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// liveDescriptionReader exercises the shared merge owner through the approved
// binary/keyring harness. Its only capability is a read of one owned immutable
// issue ID. This bridge is protocol evidence; the production prepare command and
// adapter are exercised separately below and in the binary contract suite.
type liveDescriptionReader struct {
	t          *testing.T
	r          *liveRun
	key, id    string
	apiBaseURL string
	reads      int
}

func (c *liveDescriptionReader) BaseURL() string { return c.r.manifest.BaseURL }
func (c *liveDescriptionReader) Account() string { return c.r.account }
func (c *liveDescriptionReader) JiraScope() core.JiraScope {
	return core.JiraScope{Site: c.r.manifest.BaseURL, Account: c.r.account, APIBaseURL: c.apiBaseURL}
}

func (c *liveDescriptionReader) GetIssue(ctx context.Context, id string, fields []string, expand ...string) (core.IssueRead, error) {
	if err := ctx.Err(); err != nil {
		return core.IssueRead{}, err
	}
	if c.r == nil || !c.r.verified || c.r.ownedLiveIssue(c.key) == nil || id != c.id || !slices.Equal(fields, []string{"description", "updated"}) || len(expand) != 0 {
		return core.IssueRead{}, errors.New("description read is outside the owned immutable-ID scope")
	}
	c.reads++
	frame, stdout, exit := c.r.invoke(c.t, ctx, []string{"JIRA_READ_ONLY=true"}, "api", "get", "/rest/api/3/issue/"+id, "--query=fields=description,updated")
	var decoded struct {
		Data envelope.APIOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &decoded) != nil || decoded.Data.Response == nil || decoded.Data.Response.StatusCode != 200 || decoded.Data.Response.Truncated {
		return core.IssueRead{}, errors.New("description read lacks a complete HTTP 200 receipt")
	}
	var issue jira.IssueBean
	if err := json.Unmarshal(decoded.Data.Response.JSON, &issue); err != nil {
		return core.IssueRead{}, err
	}
	return core.IssueRead{Issue: &issue, Retry: core.RetryInfo{Attempts: decoded.Data.Response.Attempts}}, nil
}

func TestLiveBoundDescriptionPrepareAndFreshMerge(t *testing.T) {
	original := []byte(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"source alpha"}]},{"type":"panel","attrs":{"panelType":"note","localId":"11111111-1111-4111-8111-111111111111"},"content":[{"type":"paragraph","content":[{"type":"text","text":"protected native panel"}]}]}]}`)
	fixture, err := adf.ParseDocument(original)
	if err != nil {
		t.Fatal(err)
	}
	fixtureSession, err := adf.PrepareEdit(fixture)
	if err != nil {
		t.Fatal(err)
	}
	regions := fixtureSession.Regions()
	if len(regions) != 2 || regions[0].Protected || !regions[1].Protected {
		t.Fatal("live fixture does not distinguish editable text and native protected panel state")
	}
	p := newParityRun(t)
	r := p.owner
	lane := p.evidence.Lanes[1]
	key := p.create(t, lane)
	editorBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	editorArgv := []string{editorBinary, "-test.run=^TestLiveDescriptionEditorObserver$"}
	parityDescriptionEditorControl(t, editorArgv, r.dir)
	editorMarker := filepath.Join(r.dir, "editor-entered.json")
	editorEnvironment := []string{"JIRA_READ_ONLY=true", "JIRA_EDITOR=" + strconv.Quote(editorBinary) + " " + editorArgv[1], "JIRA_PARITY_EDITOR_OBSERVER=1", "JIRA_PARITY_EDITOR_MARKER=" + editorMarker}
	localEnvironment := append(append([]string{}, editorEnvironment...), "JIRA_KEYRING=false", "JIRA_API_TOKEN=", "JIRA_API_TOKEN_ENV=", "JIRA_EMAIL=", "JIRA_BASE_URL=https://unreachable.example.invalid")
	store := func(document []byte) {
		t.Helper()
		endpoint, path := r.rawDescriptionInput(t, key, document)
		out, frame, exit := rawMutation(t, r, key, nil, "api", "put", endpoint, "--input", path, "--force")
		requireRawSuccess(t, out, frame, exit, 204)
		assertStoredPreconditionBody(t, r, key, "", "description", document)
	}
	store(original)
	scope := parityStoredDescription(t, r, key, original)
	frame, stdout, exit := p.invoke(t, lane, editorEnvironment, "issue", "edit", "prepare", key)
	var prepared struct {
		Data envelope.IssueEditPrepareOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &prepared) != nil {
		t.Fatal("actual read-only preparation failed")
	}
	out := prepared.Data
	if out.Issue.Key != key || out.Issue.ID == "" || out.Snapshot.IssueID != out.Issue.ID || out.Snapshot.Site != r.manifest.BaseURL || out.Snapshot.Account != r.account || !out.Snapshot.Updated.Set || out.Snapshot.Updated.Null {
		t.Fatal("production preparation lost native identity, scope or timestamp")
	}
	equal, err := adf.StructuralEqual(original, out.Snapshot.Description)
	if err != nil || !equal {
		t.Fatal("production snapshot differs from independently stored source", err)
	}
	session, err := core.OpenIssueDescriptionSession(out.Snapshot)
	if err != nil || session.Markdown() != out.Markdown {
		t.Fatal("production artifact could not reconstruct identical anchors", err)
	}
	untouched, err := session.Apply(out.Markdown)
	if err != nil || !bytes.Equal(untouched, out.Snapshot.DescriptionBytes) {
		t.Fatal("real native snapshot lost bytes on untouched edit", err)
	}
	if !parityDescriptionPrepared(parityOriginalFrameData(stdout), scope) {
		t.Fatal("prepared original JSON lacks independently bound native snapshot")
	}
	assertStoredPreconditionBody(t, r, key, "", "description", original)
	protected := false
	editable := false
	for _, region := range out.Regions {
		protected = protected || region.Protected
		editable = editable || !region.Protected
	}
	if !protected || !editable {
		t.Fatal("actual snapshot did not distinguish editable text and protected panel")
	}
	parityDescriptionEditorNotRun(t, editorMarker)
	p.check(t, lane, "N001.description.prepare.native")
	directory := filepath.Join(r.dir, "description-draft")
	previewDirectory := filepath.Join(r.dir, "description-preview")
	frame, stdout, exit = p.invoke(t, lane, localEnvironment, "issue", "edit", "draft", key, "--directory", previewDirectory, "--dry-run")
	if exit != 0 || !frame.OK || !parityDescriptionDraftPreview(parityOriginalFrameData(stdout), previewDirectory, key) {
		t.Fatal("local draft preview lost its exact no-publication plan")
	}
	if _, err := os.Lstat(previewDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("draft preview published editing artifacts")
	}
	assertStoredPreconditionBody(t, r, key, "", "description", original)
	parityDescriptionEditorNotRun(t, editorMarker)
	p.check(t, lane, "N002.description.draft.local_preview")
	frame, stdout, exit = p.invoke(t, lane, editorEnvironment, "issue", "edit", "draft", key, "--directory", directory)
	var drafted struct {
		Data envelope.IssueEditDraftOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &drafted) != nil || !drafted.Data.Draft.Ready || drafted.Data.Source == nil || drafted.Data.Source.IssueID != out.Snapshot.IssueID {
		t.Fatal("actual live Jira draft did not publish the bound native backup")
	}
	if !parityDescriptionSnapshot(parityOriginalFrameData(stdout), "source", scope) || !parityDescriptionDraftState(parityOriginalFrameData(stdout), directory, true) {
		t.Fatal("published draft lacks native scope or explicit publication facts")
	}
	assertStoredPreconditionBody(t, r, key, "", "description", original)
	sourcePath, markdownPath := filepath.Join(directory, "source.json"), filepath.Join(directory, "description.md")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	markdown, err := os.ReadFile(markdownPath)
	if err != nil || !bytes.Contains(markdown, []byte("source alpha")) {
		t.Fatal("actual live Jira draft lost editable source", err)
	}
	if !parityDescriptionSnapshotObject(source, scope) {
		t.Fatal("saved native source lost its independent binding")
	}
	initialTree := parityDescriptionFiles(t, directory)
	if !parityDescriptionManifest(initialTree["manifest.json"], source, markdown) || !bytes.Equal(markdown, []byte(out.Markdown)) || !parityDescriptionDraftUnchanged(parityOriginalFrameData(stdout), scope.Native) {
		t.Fatal("published draft or manifest lost independently bound source, text or unchanged candidate")
	}
	parityDescriptionEditorNotRun(t, editorMarker)
	p.check(t, lane, "N002.description.draft.native_source")
	frame, stdout, exit = p.invoke(t, lane, localEnvironment, "issue", "edit", "resume", "--directory", directory, "--dry-run")
	if exit != 0 || !frame.OK || !parityDescriptionDraftState(parityOriginalFrameData(stdout), directory, false) || !parityDescriptionResumePreview(parityOriginalFrameData(stdout), editorArgv) || !parityDescriptionSnapshot(parityOriginalFrameData(stdout), "source", scope) {
		t.Fatal("resume preview lost editor selection or claimed an editor launch")
	}
	assertStoredPreconditionBody(t, r, key, "", "description", original)
	parityDescriptionFilesUnchanged(t, directory, initialTree)
	parityDescriptionEditorNotRun(t, editorMarker)
	p.check(t, lane, "N004.description.resume.local_preview")
	frame, stdout, exit = p.invoke(t, lane, localEnvironment, "issue", "edit", "resume", "--directory", directory)
	if exit != 3 || frame.OK || !parityDescriptionTerminalRefusal(stdout) {
		t.Fatal("headless resume did not refuse before launching the editor")
	}
	assertStoredPreconditionBody(t, r, key, "", "description", original)
	parityDescriptionFilesUnchanged(t, directory, initialTree)
	parityDescriptionEditorNotRun(t, editorMarker)
	p.check(t, lane, "N004.description.resume.terminal_refusal")
	localMarkdown := bytes.Replace(markdown, []byte("source alpha"), []byte("authored alpha"), 1)
	if err := os.WriteFile(markdownPath, localMarkdown, 0o600); err != nil {
		t.Fatal(err)
	}
	localTree := parityDescriptionFiles(t, directory)
	frame, stdout, exit = p.invoke(t, lane, localEnvironment, "issue", "edit", "inspect", "--directory", directory)
	var inspected struct {
		Data envelope.IssueEditInspectOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &inspected) != nil || inspected.Data.Refreshed || inspected.Data.Source == nil || !bytes.Contains(inspected.Data.Description, []byte("authored alpha")) || !bytes.Contains(inspected.Data.Description, []byte("protected native panel")) {
		t.Fatal("actual live Jira artifact cannot be inspected without credentials")
	}
	wantLocal := bytes.Replace(original, []byte("source alpha"), []byte("authored alpha"), 1)
	if !parityDescriptionCandidate(parityOriginalFrameData(stdout), wantLocal, false, true) || !parityDescriptionSnapshot(parityOriginalFrameData(stdout), "source", scope) || !parityDescriptionDraftState(parityOriginalFrameData(stdout), directory, false) {
		t.Fatal("local inspection differs from complete independently authored candidate")
	}
	assertStoredPreconditionBody(t, r, key, "", "description", original)
	parityDescriptionFilesUnchanged(t, directory, localTree)
	parityDescriptionEditorNotRun(t, editorMarker)
	p.check(t, lane, "N003.description.inspect.local_candidate")
	currentBytes := []byte(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"source alpha"}]},{"type":"panel","attrs":{"panelType":"note","localId":"11111111-1111-4111-8111-111111111111"},"content":[{"type":"paragraph","content":[{"type":"text","text":"protected native panel"}]}]},{"type":"paragraph","content":[{"type":"text","text":"remote appended"}]}]}`)
	store(currentBytes)
	currentScope := parityStoredDescription(t, r, key, currentBytes)
	inspected.Data = envelope.IssueEditInspectOutput{}
	frame, stdout, exit = p.invoke(t, lane, editorEnvironment, "issue", "edit", "inspect", "--directory", directory, "--refresh")
	if exit != 0 || !frame.OK || json.Unmarshal(stdout, &inspected) != nil || !inspected.Data.Refreshed || inspected.Data.Current == nil || inspected.Data.Current.IssueID != out.Snapshot.IssueID || !bytes.Contains(inspected.Data.Description, []byte("authored alpha")) || !bytes.Contains(inspected.Data.Description, []byte("remote appended")) || !bytes.Contains(inspected.Data.Description, []byte("protected native panel")) {
		t.Fatal("production draft refresh lost local/current/protected live Jira state")
	}
	wantMerged := bytes.Replace(currentBytes, []byte("source alpha"), []byte("authored alpha"), 1)
	if !parityDescriptionCandidate(parityOriginalFrameData(stdout), wantMerged, true, true) || !parityDescriptionSnapshot(parityOriginalFrameData(stdout), "source", scope) || !parityDescriptionSnapshot(parityOriginalFrameData(stdout), "current", currentScope) || !parityDescriptionDraftState(parityOriginalFrameData(stdout), directory, false) {
		t.Fatal("fresh inspection differs from complete independently authored merge")
	}
	assertStoredPreconditionBody(t, r, key, "", "description", currentBytes)
	parityDescriptionFilesUnchanged(t, directory, localTree)
	parityDescriptionEditorNotRun(t, editorMarker)
	p.check(t, lane, "N003.description.inspect.fresh_merge")
	reader := &liveDescriptionReader{t: t, r: r, key: key, id: out.Snapshot.IssueID, apiBaseURL: out.Snapshot.APIBaseURL}
	local := strings.Replace(out.Markdown, "source alpha", "authored alpha", 1)
	merged, err := session.Merge(t.Context(), reader, local)
	if err != nil || !merged.Changed || reader.reads != 1 || !bytes.Contains(merged.Description, []byte("authored alpha")) || !bytes.Contains(merged.Description, []byte("remote appended")) || !bytes.Contains(merged.Description, []byte("protected native panel")) {
		t.Fatal("fresh merge lost local/current/protected content", err)
	}
	assertStoredPreconditionBody(t, r, key, "", "description", currentBytes)
	conflict := bytes.Replace(currentBytes, []byte("source alpha"), []byte("remote alpha"), 1)
	store(conflict)
	inspected.Data = envelope.IssueEditInspectOutput{}
	frame, stdout, exit = p.invoke(t, lane, editorEnvironment, "issue", "edit", "inspect", "--directory", directory, "--refresh")
	if exit != 3 || frame.OK || json.Unmarshal(stdout, &inspected) != nil || len(inspected.Data.Description) != 0 || inspected.Data.Current != nil || inspected.Data.Changed != nil || inspected.Data.Source == nil {
		t.Fatal("production draft refresh fabricated a candidate on live Jira conflict")
	}
	if !parityDescriptionConflict(stdout, out.Regions[0].ID) || !parityDescriptionSnapshot(parityOriginalFrameData(stdout), "source", scope) || !parityDescriptionDraftState(parityOriginalFrameData(stdout), directory, false) {
		t.Fatal("conflicting refresh lost its exact refusal reason")
	}
	assertStoredPreconditionBody(t, r, key, "", "description", conflict)
	parityDescriptionFilesUnchanged(t, directory, localTree)
	parityDescriptionEditorNotRun(t, editorMarker)
	p.check(t, lane, "N003.description.inspect.fresh_conflict")
	backup, err := os.ReadFile(sourcePath)
	saved, savedErr := os.ReadFile(markdownPath)
	if err != nil || savedErr != nil || !bytes.Equal(backup, source) || !bytes.Equal(saved, localMarkdown) {
		t.Fatal("actual live Jira reconciliation lost native backup or saved work", err, savedErr)
	}
	refused, err := session.Merge(t.Context(), reader, local)
	if err == nil || refused.Description != nil {
		t.Fatal("conflicting remote description became publishable")
	}
	assertStoredPreconditionBody(t, r, key, "", "description", conflict)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before := reader.reads
	if _, err := session.Merge(ctx, reader, local); !errors.Is(err, context.Canceled) || reader.reads != before {
		t.Fatal("canceled merge entered another Jira read", err)
	}
}

func TestLiveDescriptionReaderRefusesUnownedOrDifferentID(t *testing.T) {
	t.Parallel()
	reader := &liveDescriptionReader{id: "123456"}
	for _, id := range []string{"654321", "123456"} {
		if _, err := reader.GetIssue(t.Context(), id, []string{"description", "updated"}); err == nil || reader.reads != 0 {
			t.Fatal("unverified/unowned description scope entered binary")
		}
	}
}
