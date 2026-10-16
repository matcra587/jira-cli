//go:build live

package live

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type parityDescriptionScope struct {
	Site, Account, API, ID, Key, Updated string
	Native                               []byte
}

// This owned subprocess records any unexpected editor entry. Preview and
// headless-refusal acceptance require its marker to remain absent.
func TestLiveDescriptionEditorObserver(t *testing.T) {
	if os.Getenv("JIRA_PARITY_EDITOR_OBSERVER") != "1" {
		return
	}
	marker := os.Getenv("JIRA_PARITY_EDITOR_MARKER")
	if marker == "" {
		t.Fatal("editor observer lacks its owned marker")
	}
	body, err := json.Marshal(os.Args[1:])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func parityDescriptionEditorNotRun(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatal("description preparation unexpectedly launched its selected editor")
	}
}

func parityDescriptionEditorControl(t *testing.T, argv []string, directory string) {
	t.Helper()
	marker := filepath.Join(directory, "editor-control.json")
	filename := filepath.Join(directory, "literal filename '$;&.md")
	args := append(append([]string{}, argv[1:]...), filename)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], args...)
	cmd.Dir = directory
	home := t.TempDir()
	temporary := filepath.Join(home, "tmp")
	if err := os.Mkdir(temporary, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd.Env = []string{"JIRA_PARITY_EDITOR_OBSERVER=1", "JIRA_PARITY_EDITOR_MARKER=" + marker, "HOME=" + home, "USERPROFILE=" + home, "APPDATA=" + home, "LOCALAPPDATA=" + home, "XDG_CONFIG_HOME=" + home, "XDG_CACHE_HOME=" + home, "XDG_DATA_HOME=" + home, "XDG_STATE_HOME=" + home, "TMP=" + temporary, "TEMP=" + temporary, "TMPDIR=" + temporary}
	for _, name := range []string{"PATH", "PATHEXT", "SystemRoot", "WINDIR"} {
		if value, exists := os.LookupEnv(name); exists {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	if err := cmd.Run(); err != nil {
		t.Fatal("owned editor control did not execute and join: ", err)
	}
	body, err := os.ReadFile(marker)
	want, _ := json.Marshal(args)
	if err != nil || !parityAuthoringJSON(body, want) {
		t.Fatal("owned editor control did not record exact literal argv")
	}
}

func parityStoredDescription(t *testing.T, r *liveRun, key string, authored []byte) parityDescriptionScope {
	t.Helper()
	owned := r.ownedLiveIssue(key)
	if !r.verified || owned == nil || owned.ID == "" {
		t.Fatal("description oracle requires an owned immutable ID")
	}
	read := rawRead(t, r, "api", "get", "/rest/api/3/issue/"+owned.ID, "--query=fields=description,updated")
	body, err := base64.StdEncoding.DecodeString(read.Response.BodyBase64)
	var root, fields map[string]json.RawMessage
	var id, nativeKey, updated string
	if err != nil || !jsontext.Value(body).IsValid() || json.Unmarshal(body, &root) != nil || json.Unmarshal(root["id"], &id) != nil || id != owned.ID || json.Unmarshal(root["key"], &nativeKey) != nil || nativeKey != key || json.Unmarshal(root["fields"], &fields) != nil || json.Unmarshal(fields["updated"], &updated) != nil || updated == "" || !parityAuthoringJSON(fields["description"], authored) {
		t.Fatal("native description oracle lost exact owned identity, timestamp or authored document")
	}
	return parityDescriptionScope{Site: r.manifest.BaseURL, Account: r.account, API: r.manifest.BaseURL, ID: id, Key: key, Updated: updated, Native: bytes.Clone(fields["description"])}
}

func parityDescriptionSnapshot(raw []byte, member string, scope parityDescriptionScope) bool {
	var data map[string]json.RawMessage
	return jsontext.Value(raw).IsValid() && json.Unmarshal(raw, &data) == nil && parityDescriptionSnapshotObject(data[member], scope)
}

func parityDescriptionSnapshotObject(raw []byte, scope parityDescriptionScope) bool {
	if scope.ID == "" || scope.Key == "" || scope.Site == "" || scope.Account == "" || scope.API == "" || scope.Updated == "" || !jsontext.Value(scope.Native).IsValid() {
		return false
	}
	want, _ := json.Marshal(map[string]any{"version": 1, "site": scope.Site, "account": scope.Account, "api_base_url": scope.API, "issue_id": scope.ID, "issue_key": scope.Key, "description": json.RawMessage(scope.Native), "description_bytes": scope.Native, "updated": scope.Updated})
	return parityAuthoringJSON(raw, want)
}

func parityDescriptionDraftState(raw []byte, directory string, created bool) bool {
	var data map[string]json.RawMessage
	if !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &data) != nil {
		return false
	}
	want := parityDescriptionDraftPlan(directory)
	want["ready"] = true
	want["durability"] = "not_checked"
	if created {
		for _, member := range []string{"directory_created", "source_published", "markdown_published"} {
			want[member] = true
		}
		want["member_attempted"], want["durability"] = "manifest.json", "synced"
		if runtime.GOOS == "windows" {
			want["durability"] = "platform_unconfirmed"
		}
	}
	expected, _ := json.Marshal(want)
	return parityAuthoringJSON(data["draft"], expected)
}

func parityDescriptionDraftPlan(directory string) map[string]any {
	return map[string]any{"directory": directory, "source_file": filepath.Join(directory, "source.json"), "markdown_file": filepath.Join(directory, "description.md"), "manifest_file": filepath.Join(directory, "manifest.json"), "directory_created": false, "source_published": false, "markdown_published": false, "ready": false, "durability": "unconfirmed", "editor_attempted": false, "editor_succeeded": false}
}

func parityDescriptionDraftPreview(raw []byte, directory, key string) bool {
	want, _ := json.Marshal(map[string]any{"dry_run": true, "draft": parityDescriptionDraftPlan(directory), "requested_issue": key})
	return parityAuthoringJSON(raw, want)
}

func parityDescriptionDraftUnchanged(raw, native []byte) bool {
	var data map[string]json.RawMessage
	return jsontext.Value(raw).IsValid() && json.Unmarshal(raw, &data) == nil && parityAuthoringJSON(data["description"], native) && bytes.Equal(data["changed"], []byte("false")) && bytes.Equal(data["dry_run"], []byte("false"))
}

func parityDescriptionResumePreview(raw []byte, argv []string) bool {
	var data map[string]json.RawMessage
	if !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &data) != nil {
		return false
	}
	delete(data, "draft")
	delete(data, "source")
	got, _ := json.Marshal(data)
	want, _ := json.Marshal(map[string]any{"dry_run": true, "editor": argv})
	return parityAuthoringJSON(got, want)
}

func parityDescriptionCandidate(raw, description []byte, refreshed, changed bool) bool {
	var data map[string]json.RawMessage
	if !jsontext.Value(raw).IsValid() || !jsontext.Value(description).IsValid() || json.Unmarshal(raw, &data) != nil || !parityAuthoringJSON(data["description"], description) {
		return false
	}
	wantRefresh, _ := json.Marshal(refreshed)
	wantChanged, _ := json.Marshal(changed)
	if !bytes.Equal(data["refreshed"], wantRefresh) || !bytes.Equal(data["changed"], wantChanged) {
		return false
	}
	_, current := data["current"]
	if current != refreshed {
		return false
	}
	if current {
		var snapshot map[string]json.RawMessage
		if json.Unmarshal(data["current"], &snapshot) != nil || snapshot == nil {
			return false
		}
	}
	return true
}

func parityDescriptionConflict(raw []byte, region string) bool {
	var root map[string]json.RawMessage
	if !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &root) != nil || region == "" {
		return false
	}
	want, _ := json.Marshal([]any{map[string]any{"code": "validation_failed", "message": fmt.Sprintf("adf: edit merge_conflict region %q: region was edited or moved locally but removed or replaced remotely", region), "hint": "Check the values you passed and try again."}})
	return parityAuthoringJSON(root["errors"], want)
}

func parityDescriptionTerminalRefusal(raw []byte) bool {
	var root map[string]json.RawMessage
	if !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &root) != nil || !bytes.Equal(root["data"], []byte("null")) {
		return false
	}
	want := []byte(`[{"code":"terminal_required","message":"an editor requires interactive terminal input; inspect the saved draft or use --dry-run","hint":"Run it from a terminal, or use ` + "`--dry-run`" + ` to see the file and editor without opening anything."}]`)
	return parityAuthoringJSON(root["errors"], want)
}

func parityDescriptionPrepared(raw []byte, scope parityDescriptionScope) bool {
	var data map[string]json.RawMessage
	if !jsontext.Value(raw).IsValid() || json.Unmarshal(raw, &data) != nil || !parityDescriptionSnapshot(raw, "snapshot", scope) {
		return false
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(scope.Native))
	markdown := fmt.Sprintf("<!-- jira-adf:begin %s-0 editable -->\n\nsource alpha\n\n<!-- jira-adf:end %s-0 -->\n\n<!-- jira-adf:begin %s-1 protected -->\n\n> **note**\n>\n> protected native panel\n\n<!-- jira-adf:end %s-1 -->\n\n", digest, digest, digest, digest)
	want, _ := json.Marshal(map[string]any{"issue": map[string]any{"key": scope.Key, "id": scope.ID}, "snapshot": json.RawMessage(data["snapshot"]), "markdown": markdown, "regions": []any{map[string]any{"id": digest + "-0", "node_type": "paragraph", "protected": false}, map[string]any{"id": digest + "-1", "node_type": "panel", "protected": true}}})
	return parityAuthoringJSON(raw, want)
}

func parityDescriptionFiles(t *testing.T, directory string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 3 {
		t.Fatal("draft must retain exactly its three owned regular members")
	}
	result := make(map[string][]byte)
	for _, name := range []string{"source.json", "description.md", "manifest.json"} {
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatal("draft member is missing or redirected")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		result[name] = body
	}
	return result
}

func parityDescriptionManifest(raw, source, markdown []byte) bool {
	want, _ := json.Marshal(map[string]any{"version": 1, "source_sha256": fmt.Sprintf("%x", sha256.Sum256(source)), "source_bytes": len(source), "initial_markdown_sha256": fmt.Sprintf("%x", sha256.Sum256(markdown))})
	return parityAuthoringJSON(raw, want)
}

func parityDescriptionFilesUnchanged(t *testing.T, directory string, expected map[string][]byte) {
	t.Helper()
	actual := parityDescriptionFiles(t, directory)
	for name, body := range expected {
		if !bytes.Equal(actual[name], body) {
			t.Fatal("local description command changed saved draft member: " + name)
		}
	}
}

func TestParityDescriptionOraclesRequireNativeFacts(t *testing.T) {
	t.Parallel()
	scope := parityDescriptionScope{Site: "https://example.invalid", Account: "reader@example.invalid", API: "https://example.invalid", ID: "101", Key: "PAIR-1", Updated: "2026-10-16T00:00:00.000+0000", Native: []byte(`{"type":"doc","version":1,"content":[]}`)}
	valid := `{"version":1,"site":"https://example.invalid","account":"reader@example.invalid","api_base_url":"https://example.invalid","issue_id":"101","issue_key":"PAIR-1","description":{"type":"doc","version":1,"content":[]},"description_bytes":"eyJ0eXBlIjoiZG9jIiwidmVyc2lvbiI6MSwiY29udGVudCI6W119","updated":"2026-10-16T00:00:00.000+0000"}`
	if !parityDescriptionSnapshotObject([]byte(valid), scope) {
		t.Fatal("independent native snapshot refused")
	}
	for name, invalid := range map[string]string{"foreign_id": strings.ReplaceAll(valid, `"101"`, `"102"`), "wrong_account": strings.ReplaceAll(valid, "reader@", "foreign@"), "wrong_route": strings.ReplaceAll(valid, `"api_base_url":"https://example.invalid"`, `"api_base_url":"https://foreign.invalid"`), "missing_timestamp": strings.ReplaceAll(valid, `,"updated":"2026-10-16T00:00:00.000+0000"`, ""), "bad_bytes": strings.ReplaceAll(valid, "eyJ0", "e30=eyJ0"), "wrong_case": strings.ReplaceAll(valid, "issue_id", "Issue_ID"), "null": "null"} {
		t.Run(name, func(t *testing.T) {
			if parityDescriptionSnapshotObject([]byte(invalid), scope) {
				t.Fatal("snapshot admitted missing, foreign or normalized native facts")
			}
		})
	}
	candidate := `{"description":{"type":"doc","version":1,"content":[]},"refreshed":false,"changed":true}`
	if !parityDescriptionCandidate([]byte(candidate), scope.Native, false, true) {
		t.Fatal("complete native candidate refused")
	}
	for name, invalid := range map[string]string{"sparse": `{"description":{"type":"doc","version":1,"content":[]}}`, "missing_changed": strings.ReplaceAll(candidate, `,"changed":true`, ""), "false_refresh": strings.ReplaceAll(candidate, `"refreshed":false`, `"refreshed":true`), "wrong_document": strings.ReplaceAll(candidate, `"content":[]`, `"content":[{}]`)} {
		t.Run(name, func(t *testing.T) {
			if parityDescriptionCandidate([]byte(invalid), scope.Native, false, true) {
				t.Fatal("candidate admitted missing facts or wrong complete document")
			}
		})
	}
}

func TestParityDescriptionManifestRequiresExactPreimages(t *testing.T) {
	t.Parallel()
	source, markdown := []byte("source bytes\n"), []byte("anchored Markdown\n")
	valid := `{"version":1,"source_sha256":"8482cb75bd3c9abc864397324f7ba6cdbdb4920fe384ef7ee34b6ab4996ae8b2","source_bytes":13,"initial_markdown_sha256":"c952f7c816eb3a2996d83f7858445336406ecf378f2b5866300298484852a3b0"}`
	if !parityDescriptionManifest([]byte(valid), source, markdown) {
		t.Fatal("independently pinned manifest refused")
	}
	for name, invalid := range map[string]string{"wrong_length": strings.ReplaceAll(valid, ":13", ":12"), "wrong_source": strings.ReplaceAll(valid, "8482cb75bd3c9abc864397324f7ba6cdbdb4920fe384ef7ee34b6ab4996ae8b2", "bad"), "wrong_markdown": strings.ReplaceAll(valid, "c952f7c816eb3a2996d83f7858445336406ecf378f2b5866300298484852a3b0", "bad"), "missing_hash": strings.ReplaceAll(valid, `,"initial_markdown_sha256":"c952f7c816eb3a2996d83f7858445336406ecf378f2b5866300298484852a3b0"`, ""), "extra": strings.ReplaceAll(valid, `"version":1`, `"version":1,"extra":true`), "duplicate": strings.ReplaceAll(valid, `"version":1`, `"version":1,"version":1`)} {
		t.Run(name, func(t *testing.T) {
			if parityDescriptionManifest([]byte(invalid), source, markdown) {
				t.Fatal("manifest admitted corrupt or incomplete preimages")
			}
		})
	}
}

func TestParityDescriptionTerminalRefusalRequiresExactReason(t *testing.T) {
	t.Parallel()
	valid := "{\"data\":null,\"errors\":[{\"code\":\"terminal_required\",\"message\":\"an editor requires interactive terminal input; inspect the saved draft or use --dry-run\",\"hint\":\"Run it from a terminal, or use `--dry-run` to see the file and editor without opening anything.\"}]}"
	if !parityDescriptionTerminalRefusal([]byte(valid)) {
		t.Fatal("complete terminal refusal refused")
	}
	for name, invalid := range map[string]string{"wrong_reason": strings.ReplaceAll(valid, "an editor requires", "an unknown command requires"), "wrong_hint": strings.ReplaceAll(valid, "Run it from a terminal", "Retry the write"), "wrong_code": strings.ReplaceAll(valid, "terminal_required", "validation_failed"), "retained_data": strings.ReplaceAll(valid, `"data":null`, `"data":{}`), "missing_data": strings.ReplaceAll(valid, `"data":null,`, "")} {
		t.Run(name, func(t *testing.T) {
			if parityDescriptionTerminalRefusal([]byte(invalid)) {
				t.Fatal("terminal refusal admitted unrelated or incomplete reason")
			}
		})
	}
}

func TestParityDescriptionOperationMapping(t *testing.T) {
	t.Parallel()
	for _, child := range []string{"prepare", "draft", "inspect", "resume"} {
		if parityOperation([]string{"issue", "edit", child}) != "issue.edit."+child {
			t.Fatal("description child operation lost its exact attribution")
		}
	}
	if parityOperation([]string{"issue", "edit", "PAIR-1"}) != "issue.edit" {
		t.Fatal("ordinary edit key became a child operation")
	}
}
