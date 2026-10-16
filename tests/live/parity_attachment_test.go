//go:build live

package live

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
)

type parityAttachmentAttempt struct {
	Lane        string   `json:"lane"`
	Key         string   `json:"key"`
	Digests     []string `json:"input_sha256"`
	ReturnedIDs []string `json:"returned_ids,omitempty"`
	NativeIDs   []string `json:"observed_ids,omitempty"`
	Bound       bool     `json:"bound"`
}

type parityAttachmentNative struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	MIME     string `json:"mimeType"`
	Created  string `json:"created"`
	Content  string `json:"content"`
	Author   struct {
		AccountID   string `json:"accountId"`
		DisplayName string `json:"displayName"`
	} `json:"author"`
}

func parityAttachmentProjection(native []parityAttachmentNative, lane string) []any {
	rows := make([]any, 0, len(native))
	for _, file := range native {
		row := map[string]any{"id": file.ID, "filename": file.Filename, "size": file.Size, "mime_type": file.MIME, "created": file.Created, "author": map[string]any{"account_id": file.Author.AccountID, "display_name": file.Author.DisplayName}}
		if lane == "after" {
			row["content"] = file.Content
		}
		rows = append(rows, row)
	}
	return rows
}

func parityAttachmentUploadOrder(native []parityAttachmentNative, names []string) ([]parityAttachmentNative, bool) {
	if len(native) != len(names) {
		return nil, false
	}
	byName := make(map[string]parityAttachmentNative, len(native))
	for _, file := range native {
		if _, exists := byName[file.Filename]; exists {
			return nil, false
		}
		byName[file.Filename] = file
	}
	ordered := make([]parityAttachmentNative, 0, len(names))
	for _, name := range names {
		file, exists := byName[name]
		if !exists {
			return nil, false
		}
		ordered = append(ordered, file)
		delete(byName, name)
	}
	return ordered, len(byName) == 0
}

func parityAttachmentListData(data []byte, lane string, issue liveIssue, native []parityAttachmentNative) bool {
	var expected map[string]any
	switch lane {
	case "before":
		expected = map[string]any{"issue": map[string]any{"key": issue.Key}, "attachments": parityAttachmentProjection(native, lane)}
	case "after":
		if issue.ID == "" || issue.Key == "" || issue.Self == "" {
			return false
		}
		expected = map[string]any{"results": []any{map[string]any{"issue": map[string]any{"id": issue.ID, "key": issue.Key, "self": issue.Self}, "ok": true, "data": map[string]any{"attachments": parityAttachmentProjection(native, lane)}}}}
	default:
		return false
	}
	raw, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, raw)
}

func parityAttachmentListPagination(stdout []byte, lane string, total int) bool {
	if !jsontext.Value(stdout).IsValid() {
		return false
	}
	var frame map[string]json.RawMessage
	var meta map[string]json.RawMessage
	if json.Unmarshal(stdout, &frame) != nil || json.Unmarshal(frame["meta"], &meta) != nil || meta == nil {
		return false
	}
	switch lane {
	case "before":
		return parityAuthoringJSON(meta["pagination"], fmt.Appendf(nil, `{"startAt":0,"maxResults":50,"total":%d,"isLast":true}`, total))
	case "after":
		_, present := meta["pagination"]
		return !present
	default:
		return false
	}
}

func parityAttachmentAddData(data []byte, lane, key string, paths []string, sizes []int64, mimes []string, preview bool, native []parityAttachmentNative) bool {
	if len(paths) != len(sizes) || len(paths) != len(mimes) {
		return false
	}
	details := make([]any, 0, len(paths))
	for i, path := range paths {
		details = append(details, map[string]any{"path": path, "size": sizes[i], "mime_inferred": mimes[i]})
	}
	var expected map[string]any
	switch lane {
	case "before":
		expected = map[string]any{"issue": map[string]any{"key": key}, "files": details, "dry_run": preview}
		if !preview {
			expected["attachments"] = parityAttachmentProjection(native, lane)
		}
	case "after":
		entry := map[string]any{}
		if !preview {
			entry["attachments"] = parityAttachmentProjection(native, lane)
		}
		expected = map[string]any{"dry_run": preview, "files": paths, "file_details": details, "results": []any{map[string]any{"issue": map[string]any{"key": key}, "ok": true, "data": entry}}}
	default:
		return false
	}
	raw, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, raw)
}

func parityAttachmentRefusalData(data []byte, lane, key string, paths []string, sizes []int64, mimes []string) bool {
	if lane == "before" {
		return parityAuthoringJSON(data, []byte(`null`))
	}
	if lane != "after" || len(paths) != len(sizes) || len(paths) != len(mimes) {
		return false
	}
	details := make([]any, 0, len(paths))
	for i, path := range paths {
		details = append(details, map[string]any{"path": path, "size": sizes[i], "mime_inferred": mimes[i]})
	}
	expected := map[string]any{"dry_run": false, "files": paths, "file_details": details, "results": []any{map[string]any{"issue": map[string]any{"key": key}, "ok": false, "error": map[string]any{"code": "read_only_blocked", "message": "read-only mode: write blocked: POST /rest/api/3/issue/" + key + "/attachments", "hint": "Read-only mode is on, so writes are refused before they reach Jira — clear it to allow this command to mutate."}}}}
	raw, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, raw)
}

func parityAttachmentDeleteData(data []byte, lane, key, id string, preview bool) bool {
	expected := map[string]any{"dry_run": preview, "attachment_id": id}
	switch lane {
	case "before":
		expected["issue"] = map[string]any{"key": key}
		if !preview {
			expected["deleted"] = true
		}
	case "after":
	default:
		return false
	}
	raw, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, raw)
}

func parityAttachmentDownloadData(data []byte, lane, key, id, directory, name string, preview, overwrite bool, size int64) bool {
	path := filepath.Join(directory, name)
	var expected map[string]any
	switch lane {
	case "before":
		expected = map[string]any{"issue": map[string]any{"key": key}, "attachment_id": id, "mode": "output", "target": path, "dry_run": preview}
		if !preview {
			expected["written_to"] = path
			expected["bytes"] = size
		}
	case "after":
		expected = map[string]any{"attachment_id": id, "directory": directory, "filename_resolved": true, "filename": name, "path": path, "dry_run": preview}
		if !preview {
			expected["bytes_written"] = size
			expected["overwrote"] = overwrite
		}
	default:
		return false
	}
	raw, err := json.Marshal(expected)
	return err == nil && parityAuthoringJSON(data, raw)
}

func (p *parityRun) validateAttachmentMutation(lane parityLane, key string, args []string) error {
	if !slices.Contains(p.evidence.Lanes, lane) || p.owner.ownedLiveIssue(key) == nil {
		return fmt.Errorf("attachment mutation requires an admitted lane and active owned parent")
	}
	if err := parityFingerprint(lane.Binary, lane.Digest); err != nil {
		return err
	}
	if lane.Name == "before" {
		if len(args) < 4 || args[0] != "issue" || args[1] != "attachment" {
			return fmt.Errorf("legacy attachment mutation requires exact grammar")
		}
		switch args[2] {
		case "add":
		case "delete":
			if len(args) < 5 || args[3] != key {
				return fmt.Errorf("legacy attachment deletion requires the exact owned key and ID")
			}
			args = append(append([]string{}, args[:3]...), args[4:]...)
		default:
			return fmt.Errorf("unsupported legacy attachment mutation")
		}
	} else if lane.Name != "after" {
		return fmt.Errorf("unsupported attachment lane")
	}
	return p.owner.checkAttachmentMutation([]string{key}, args)
}

func (p *parityRun) invokeAttachment(t *testing.T, lane parityLane, key string, extra []string, args ...string) (liveFrame, []byte, int) {
	t.Helper()
	if err := p.validateAttachmentMutation(lane, key, args); err != nil {
		t.Fatal(err)
	}
	return p.invoke(t, lane, extra, args...)
}

func parityAttachmentInventory(raw []byte, id, key string) ([]parityAttachmentNative, bool) {
	if !jsontext.Value(raw).IsValid() || id == "" || key == "" {
		return nil, false
	}
	var issue struct {
		ID, Key string
		Fields  map[string]json.RawMessage
	}
	if json.Unmarshal(raw, &issue) != nil || issue.ID != id || issue.Key != key {
		return nil, false
	}
	var rows []json.RawMessage
	if json.Unmarshal(issue.Fields["attachment"], &rows) != nil || rows == nil {
		return nil, false
	}
	native := make([]parityAttachmentNative, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		var size struct {
			Size *int64 `json:"size"`
		}
		var file parityAttachmentNative
		if json.Unmarshal(row, &size) != nil || size.Size == nil || json.Unmarshal(row, &file) != nil {
			return nil, false
		}
		numeric, err := strconv.ParseUint(file.ID, 10, 64)
		if err != nil || numeric == 0 || seen[file.ID] || file.Filename == "" || file.Size < 0 || file.Content == "" || file.Author.AccountID == "" || file.Created == "" {
			return nil, false
		}
		seen[file.ID] = true
		native = append(native, file)
	}
	return native, true
}

func (p *parityRun) nativeAttachments(t *testing.T, key string) []parityAttachmentNative {
	t.Helper()
	issue := p.owner.ownedLiveIssue(key)
	if issue == nil {
		t.Fatal("native attachment read requires an active owned parent")
	}
	read := rawRead(t, p.owner, "api", "get", "/rest/api/3/issue/"+key, "--query=fields=attachment")
	native, ok := parityAttachmentInventory(read.Response.JSON, issue.ID, key)
	if !ok {
		t.Fatal("native attachment inventory lacks complete explicit metadata or owned identity")
	}
	return native
}

func parityAttachmentDirectory(before, after map[string]parityLocalFile, name string, content []byte, changed bool) bool {
	if !changed {
		return reflect.DeepEqual(before, after)
	}
	target, exists := after[name]
	if !exists || !target.Mode.IsRegular() || target.Hash != sha256.Sum256(content) {
		return false
	}
	expected := maps.Clone(before)
	expected[name] = target
	return reflect.DeepEqual(expected, after)
}

func TestLiveParityAttachments(t *testing.T) {
	p := newParityRun(t)
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			key := p.create(t, lane)
			original := p.native(t, key)
			content := [][]byte{bytes.Repeat([]byte{0, 0xff, '<', '&', '>', '\n'}, 8192), []byte("Owned attachment <&> café\n"), {}}
			names := []string{"binary-" + lane.Name + ".bin", "note-" + lane.Name + ".txt", "empty-" + lane.Name + ".bin"}
			mimes := []string{"application/octet-stream", "text/plain", "application/octet-stream"}
			var paths []string
			var sizes []int64
			var digests []string
			for i, name := range names {
				paths = append(paths, p.owner.attachmentInput(t, name, content[i]))
				sizes = append(sizes, int64(len(content[i])))
				digests = append(digests, fmt.Sprintf("%x", sha256.Sum256(content[i])))
			}
			args := append([]string{"issue", "attachment", "add", key}, paths...)
			if len(p.nativeAttachments(t, key)) != 0 {
				t.Fatal("new owned issue already carries attachments")
			}
			frame, stdout, exit := p.invokeAttachment(t, lane, key, nil, append(slices.Clone(args), "--dry-run")...)
			parityAuthoringData(t, stdout, lane.Name, "issue.attachment.add", exit)
			if exit != 0 || !frame.OK || !parityAttachmentAddData(parityOriginalFrameData(stdout), lane.Name, key, paths, sizes, mimes, true, nil) {
				t.Fatal("attachment preview lost input metadata or invented resources")
			}
			if len(p.nativeAttachments(t, key)) != 0 {
				t.Fatal("upload preview changed native inventory")
			}
			p.check(t, lane, "I001.attachment.preview")
			frame, stdout, exit = p.invokeAttachment(t, lane, key, []string{"JIRA_READ_ONLY=true"}, args...)
			if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != parityCommentReadOnlyCode(lane.Name) || !parityAttachmentRefusalData(parityOriginalFrameData(stdout), lane.Name, key, paths, sizes, mimes) {
				t.Fatal("attachment upload refusal lost retained input or invented resources")
			}
			if len(p.nativeAttachments(t, key)) != 0 {
				t.Fatal("upload read-only refusal changed native inventory")
			}
			p.check(t, lane, "I001.attachment.read_only")

			attempt := len(p.evidence.Attachments)
			p.evidence.Attachments = append(p.evidence.Attachments, parityAttachmentAttempt{Lane: lane.Name, Key: key, Digests: digests})
			p.save(t)
			frame, stdout, exit = p.invokeAttachment(t, lane, key, nil, args...)
			var receipt struct {
				Data struct {
					Attachments []struct{ ID string }
					Results     []struct {
						Data *struct{ Attachments []struct{ ID string } }
					}
				}
			}
			if json.Unmarshal(stdout, &receipt) == nil {
				if lane.Name == "before" {
					for _, file := range receipt.Data.Attachments {
						p.evidence.Attachments[attempt].ReturnedIDs = append(p.evidence.Attachments[attempt].ReturnedIDs, file.ID)
					}
				} else if len(receipt.Data.Results) == 1 && receipt.Data.Results[0].Data != nil {
					for _, file := range receipt.Data.Results[0].Data.Attachments {
						p.evidence.Attachments[attempt].ReturnedIDs = append(p.evidence.Attachments[attempt].ReturnedIDs, file.ID)
					}
				}
			}
			p.save(t)
			stored := p.nativeAttachments(t, key)
			// Persist the complete observation before any reconciliation can
			// refuse. Unmatched resources remain observations, never write grants.
			for _, file := range stored {
				p.evidence.Attachments[attempt].NativeIDs = append(p.evidence.Attachments[attempt].NativeIDs, file.ID)
			}
			p.save(t)
			if len(stored) != len(names) {
				t.Fatal("upload has uncertain or duplicate native effects; never replay")
			}
			seen := map[string]bool{}
			for _, file := range stored {
				i := slices.Index(names, file.Filename)
				if i < 0 || seen[file.Filename] || file.Size != sizes[i] {
					t.Fatal("native upload does not uniquely match authored files; never replay")
				}
				seen[file.Filename] = true
				p.owner.registerAttachment(t, key, envelope.AttachmentView{ID: file.ID, Filename: file.Filename, Size: file.Size})
			}
			p.evidence.Attachments[attempt].Bound = true
			p.save(t)
			// Multipart replies retain authored part order; the independent issue
			// inventory may use a different ordering of the same native resources.
			ordered, complete := parityAttachmentUploadOrder(stored, names)
			if exit != 0 || !frame.OK || !complete || !parityAttachmentAddData(parityOriginalFrameData(stdout), lane.Name, key, paths, sizes, mimes, false, ordered) {
				t.Fatal("upload receipt differs from independent native resources; owned cleanup retained")
			}
			returned := slices.Clone(p.evidence.Attachments[attempt].ReturnedIDs)
			nativeIDs := slices.Clone(p.evidence.Attachments[attempt].NativeIDs)
			slices.Sort(returned)
			slices.Sort(nativeIDs)
			if !slices.Equal(returned, nativeIDs) {
				t.Fatal("upload returned foreign, repeated or missing native identities")
			}
			for _, file := range stored {
				i := slices.Index(names, file.Filename)
				frame, raw, code := p.invoke(t, p.evidence.Lanes[1], []string{"JIRA_READ_ONLY=true"}, "api", "get", "/rest/api/3/attachment/content/"+file.ID, "--query=redirect=false")
				var response struct{ Data envelope.APIOutput }
				if code != 0 || !frame.OK || json.Unmarshal(raw, &response) != nil || response.Data.Response == nil || response.Data.Response.StatusCode != 200 || response.Data.Response.Truncated || response.Data.Response.Attempts != 1 || response.Data.Response.BodyBase64 != base64.StdEncoding.EncodeToString(content[i]) {
					t.Fatal("independent native attachment bytes differ from the authored preimage")
				}
			}
			p.check(t, lane, "I001.attachment.native_upload_bytes")
			listArgs := []string{"issue", "attachment", "list", key}
			if lane.Name == "before" {
				listArgs = append(listArgs, "--all")
			}
			frame, stdout, exit = p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, listArgs...)
			if exit != 0 || !frame.OK || !parityAttachmentListData(parityOriginalFrameData(stdout), lane.Name, original, stored) || !parityAttachmentListPagination(stdout, lane.Name, len(stored)) {
				t.Fatal("populated attachment list differs from complete native identity and metadata")
			}
			p.check(t, lane, "I004.attachment.native_populated")

			directory := filepath.Join(p.owner.dir, "download-"+lane.Name)
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			canonicalDirectory, err := liveInvocationDirectory(p.owner.dir, directory)
			if err != nil {
				t.Fatal(err)
			}
			directory = canonicalDirectory
			for _, file := range stored {
				i := slices.Index(names, file.Filename)
				path := filepath.Join(directory, file.Filename)
				download := []string{"issue", "attachment", "download", file.ID, "--to", directory}
				preview := append(slices.Clone(download), "--as", file.Filename, "--dry-run")
				if lane.Name == "before" {
					download = []string{"issue", "attachment", "download", key, file.ID, "--to", path}
					preview = append(slices.Clone(download), "--dry-run")
				}
				beforeDirectory := parityLocalTree(t, directory)
				frame, stdout, exit = p.invokeAt(t, lane, directory, []string{"JIRA_READ_ONLY=true"}, preview...)
				if exit != 0 || !frame.OK || !parityAttachmentDownloadData(parityOriginalFrameData(stdout), lane.Name, key, file.ID, directory, file.Filename, true, false, file.Size) {
					t.Fatal("mapped download preview lost its exact local destination")
				}
				if !parityAttachmentDirectory(beforeDirectory, parityLocalTree(t, directory), file.Filename, nil, false) {
					t.Fatal("download preview published a file")
				}
				p.check(t, lane, "I003.attachment.preview_"+strconv.Itoa(i))
				beforeDirectory = parityLocalTree(t, directory)
				frame, stdout, exit = p.invokeAt(t, lane, directory, []string{"JIRA_READ_ONLY=true"}, download...)
				data, err := os.ReadFile(path)
				if exit != 0 || !frame.OK || err != nil || !bytes.Equal(data, content[i]) || !parityAttachmentDirectory(beforeDirectory, parityLocalTree(t, directory), file.Filename, content[i], true) || !parityAttachmentDownloadData(parityOriginalFrameData(stdout), lane.Name, key, file.ID, directory, file.Filename, false, false, file.Size) {
					t.Fatal("mapped live download changed exact bytes or publication facts")
				}
				p.check(t, lane, "I003.attachment.native_download_"+strconv.Itoa(i))
				if err := os.WriteFile(path, []byte("owned overwrite preimage"), 0o600); err != nil {
					t.Fatal(err)
				}
				beforeDirectory = parityLocalTree(t, directory)
				frame, stdout, exit = p.invokeAt(t, lane, directory, []string{"JIRA_READ_ONLY=true"}, download...)
				code := "local_io_failed"
				if lane.Name == "before" {
					code = "validation_failed"
				}
				data, err = os.ReadFile(path)
				if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != code || err != nil || string(data) != "owned overwrite preimage" || !parityAttachmentDirectory(beforeDirectory, parityLocalTree(t, directory), file.Filename, nil, false) || !parityAuthoringJSON(parityOriginalFrameData(stdout), []byte(`null`)) {
					t.Fatal("download collision changed its owned preimage or invented publication")
				}
				p.check(t, lane, "I003.attachment.collision_"+strconv.Itoa(i))
				overwrite := "--overwrite"
				if lane.Name == "before" {
					overwrite = "--force"
				}
				beforeDirectory = parityLocalTree(t, directory)
				frame, stdout, exit = p.invokeAt(t, lane, directory, []string{"JIRA_READ_ONLY=true"}, append(slices.Clone(download), overwrite)...)
				data, err = os.ReadFile(path)
				if exit != 0 || !frame.OK || err != nil || !bytes.Equal(data, content[i]) || !parityAttachmentDirectory(beforeDirectory, parityLocalTree(t, directory), file.Filename, content[i], true) || !parityAttachmentDownloadData(parityOriginalFrameData(stdout), lane.Name, key, file.ID, directory, file.Filename, false, true, file.Size) {
					t.Fatal("mapped overwrite changed native bytes or publication facts")
				}
				p.check(t, lane, "I003.attachment.overwrite_"+strconv.Itoa(i))
			}

			for i, file := range stored {
				args := []string{"issue", "attachment", "delete", file.ID}
				if lane.Name == "before" {
					args = []string{"issue", "attachment", "delete", key, file.ID}
				}
				frame, stdout, exit = p.invokeAttachment(t, lane, key, nil, append(slices.Clone(args), "--dry-run")...)
				if exit != 0 || !frame.OK || !parityAttachmentDeleteData(parityOriginalFrameData(stdout), lane.Name, key, file.ID, true) {
					t.Fatal("attachment deletion preview lost its exact ID")
				}
				p.check(t, lane, "I002.attachment.preview_"+strconv.Itoa(i))
				for _, scenario := range []string{"confirmation", "read-only"} {
					call := slices.Clone(args)
					var extra []string
					if scenario == "read-only" {
						call = append(call, "--force")
						extra = []string{"JIRA_READ_ONLY=true"}
					}
					frame, stdout, exit = p.invokeAttachment(t, lane, key, extra, call...)
					code := "flag_required"
					if scenario == "read-only" {
						code = parityCommentReadOnlyCode(lane.Name)
					} else if lane.Name == "before" {
						code = "validation_failed"
					}
					if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != code || !parityAuthoringJSON(parityOriginalFrameData(stdout), []byte(`null`)) || !slices.Equal(p.nativeAttachments(t, key), stored[i:]) {
						t.Fatal("attachment delete refusal changed native inventory or invented an effect")
					}
					p.check(t, lane, "I002.attachment."+scenario+"_"+strconv.Itoa(i))
				}
				frame, stdout, exit = p.invokeAttachment(t, lane, key, nil, append(slices.Clone(args), "--force")...)
				if exit != 0 || !frame.OK || !parityAttachmentDeleteData(parityOriginalFrameData(stdout), lane.Name, key, file.ID, false) {
					t.Fatal("attachment deletion has no exact acknowledgement; never replay")
				}
				missing, _, code := p.invoke(t, p.evidence.Lanes[1], []string{"JIRA_READ_ONLY=true"}, "api", "get", "/rest/api/3/attachment/"+file.ID)
				if code != 2 || missing.OK || len(missing.Errors) != 1 || missing.Errors[0].Code != "jira_not_found" {
					t.Fatal("attachment deletion lacks independent native absence")
				}
				p.owner.ownedLiveAttachment(p.owner.ownedLiveIssue(key), file.ID).Deleted = true
				p.owner.save(t)
				remaining := p.nativeAttachments(t, key)
				if len(remaining) != len(stored)-i-1 {
					t.Fatal("attachment deletion removed an unexpected sibling set")
				}
				for _, sibling := range stored[i+1:] {
					if !slices.Contains(remaining, sibling) {
						t.Fatal("attachment deletion changed a surviving sibling")
					}
				}
				p.check(t, lane, "I002.attachment.native_delete_"+strconv.Itoa(i))
			}
			frame, stdout, exit = p.invoke(t, lane, []string{"JIRA_READ_ONLY=true"}, listArgs...)
			if exit != 0 || !frame.OK || !parityAttachmentListData(parityOriginalFrameData(stdout), lane.Name, original, []parityAttachmentNative{}) || !parityAttachmentListPagination(stdout, lane.Name, 0) {
				t.Fatal("post-deletion list lost explicit empty native inventory")
			}
			p.check(t, lane, "I004.attachment.native_empty_after_delete")
			if !nativeMetadataMatches(p.native(t, key).Fields, original.Fields, nil) {
				t.Fatal("attachment lifecycle changed unrelated owned fields")
			}
			for i, path := range paths {
				data, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(data, content[i]) {
					t.Fatal("attachment lifecycle changed an authored input")
				}
			}
		})
	}
}

func TestParityAttachmentLiteralReceipts(t *testing.T) {
	raw, err := os.ReadFile("testdata/parity-attachment-oracles.json")
	var fixtures []struct {
		Name, Lane, Operation string
		Preview, Overwrite    bool
		Size                  int64
		Data                  json.RawMessage
	}
	if err != nil || json.Unmarshal(raw, &fixtures) != nil || len(fixtures) != 20 {
		t.Fatal("independent attachment receipt fixtures unavailable")
	}
	native := parityAttachmentNative{ID: "41", Filename: "owned.bin", Size: 3, MIME: "application/octet-stream", Created: "2026-01-01T00:00:00.000+0000", Content: "https://jira.invalid/content/41"}
	native.Author.AccountID = "fixture-account"
	native.Author.DisplayName = "Fixture"
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			check := func(data []byte) bool {
				switch fixture.Operation {
				case "list":
					return parityAttachmentListData(data, fixture.Lane, liveIssue{ID: "1", Key: "PAIR-1", Self: "https://jira.invalid/rest/api/3/issue/1"}, []parityAttachmentNative{native})
				case "upload":
					return parityAttachmentAddData(data, fixture.Lane, "PAIR-1", []string{"owned.bin"}, []int64{3}, []string{"application/octet-stream"}, fixture.Preview, []parityAttachmentNative{native})
				case "refused":
					return parityAttachmentRefusalData(data, fixture.Lane, "PAIR-1", []string{"owned.bin"}, []int64{3}, []string{"application/octet-stream"})
				case "delete":
					return parityAttachmentDeleteData(data, fixture.Lane, "PAIR-1", "41", fixture.Preview)
				case "download":
					return parityAttachmentDownloadData(data, fixture.Lane, "PAIR-1", "41", ".", "owned.bin", fixture.Preview, fixture.Overwrite, fixture.Size)
				default:
					t.Fatal("undeclared attachment fixture operation")
					return false
				}
			}
			if !check(fixture.Data) {
				t.Fatal("independent literal attachment receipt rejected")
			}
			if fixture.Operation == "refused" && fixture.Lane == "before" {
				if check([]byte(`{}`)) {
					t.Fatal("legacy refusal invented retained data")
				}
				return
			}
			for _, mutation := range []string{"extra-effect", "missing-preview", "null-preview", "foreign-id", "foreign-target", "missing-zero-bytes"} {
				var data map[string]json.RawMessage
				if json.Unmarshal(fixture.Data, &data) != nil {
					t.Fatal("fixture is not an object")
				}
				switch mutation {
				case "extra-effect":
					data["invented_effect"] = json.RawMessage(`true`)
				case "missing-preview":
					if _, exists := data["dry_run"]; !exists {
						continue
					}
					delete(data, "dry_run")
				case "null-preview":
					if _, exists := data["dry_run"]; !exists {
						continue
					}
					data["dry_run"] = json.RawMessage(`null`)
				case "foreign-id":
					if !bytes.Contains(fixture.Data, []byte(`"41"`)) {
						continue
					}
				case "foreign-target":
					if !bytes.Contains(fixture.Data, []byte("PAIR-1")) {
						continue
					}
				case "missing-zero-bytes":
					if fixture.Operation != "download" || fixture.Preview || fixture.Size != 0 {
						continue
					}
					delete(data, "bytes")
					delete(data, "bytes_written")
				}
				changed, err := json.Marshal(data)
				if err != nil {
					t.Fatal(err)
				}
				if mutation == "foreign-id" {
					changed = bytes.ReplaceAll(changed, []byte(`"41"`), []byte(`"42"`))
				}
				if mutation == "foreign-target" {
					changed = bytes.ReplaceAll(changed, []byte("PAIR-1"), []byte("PAIR-2"))
				}
				t.Run(mutation, func(t *testing.T) {
					if !json.Valid(changed) {
						t.Fatal("mutation did not remain complete JSON")
					}
					if check(changed) {
						t.Fatal("attachment receipt guard admitted sparse, foreign or invented effects")
					}
				})
			}
		})
	}
}

func TestParityActualBinaryAttachmentLocal(t *testing.T) {
	evidence := parityLocalEvidence{Checks: make(map[string][]string)}
	for _, lane := range []struct{ name, binary, digest string }{{"before", os.Getenv("JIRA_PARITY_BEFORE_BINARY"), os.Getenv("JIRA_PARITY_BEFORE_SHA256")}, {"after", os.Getenv("JIRA_LIVE_BINARY"), os.Getenv("JIRA_PARITY_AFTER_SHA256")}} {
		if lane.binary == "" {
			t.Skip("requires both selected actual binaries")
		}
		for _, mode := range []string{"json", "compact"} {
			for _, scenario := range []string{"upload-positional", "upload-files", "upload-readonly", "delete-preview", "delete-confirmation", "delete-readonly", "download-preview", "download-collision", "download-unresolved"} {
				t.Run(lane.name+"/"+mode+"/"+scenario, func(t *testing.T) {
					var calls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusForbidden) }))
					defer server.Close()
					home := t.TempDir()
					config := parityCachePublicConfig(t, home, lane.name, server.URL)
					paths := []string{filepath.Join(home, "report,final.bin"), filepath.Join(home, "note.txt"), filepath.Join(home, "empty.bin")}
					values := [][]byte{{0, 0xff, 1}, []byte("Owned <&> café\n"), {}}
					sizes := []int64{int64(len(values[0])), int64(len(values[1])), 0}
					mimes := []string{"application/octet-stream", "text/plain", "application/octet-stream"}
					for i, path := range paths {
						if err := os.WriteFile(path, values[i], 0o600); err != nil {
							t.Fatal(err)
						}
					}
					var args []string
					expectedExit, code, id := 0, "", "I001"
					extra := []string{"JIRA_API_TOKEN=", "JIRA_TOKEN_FIXTURE=", "JIRA_KEYRING=false"}
					switch scenario {
					case "upload-positional", "upload-files", "upload-readonly":
						args = append([]string{"issue", "attachment", "add", "PAIR-1"}, paths...)
						if scenario == "upload-files" {
							args = []string{"issue", "attachment", "add", "PAIR-1"}
							for _, path := range paths {
								value := path
								if lane.name == "after" {
									value = `"` + strings.ReplaceAll(path, `"`, `""`) + `"`
								}
								args = append(args, "--file", value)
							}
						}
						if scenario == "upload-readonly" {
							expectedExit = 3
							code = parityCommentReadOnlyCode(lane.name)
							extra = []string{"JIRA_READ_ONLY=true"}
						} else {
							args = append(args, "--dry-run")
						}
					case "delete-preview", "delete-confirmation", "delete-readonly":
						id = "I002"
						args = []string{"issue", "attachment", "delete", "41"}
						if lane.name == "before" {
							args = []string{"issue", "attachment", "delete", "PAIR-1", "41"}
						}
						if scenario == "delete-preview" {
							args = append(args, "--dry-run")
						} else {
							expectedExit = 3
							code = "flag_required"
							if lane.name == "before" {
								code = "validation_failed"
							}
							if scenario == "delete-readonly" {
								args = append(args, "--force")
								extra = []string{"JIRA_READ_ONLY=true"}
								code = parityCommentReadOnlyCode(lane.name)
							}
						}
					case "download-preview", "download-collision", "download-unresolved":
						id = "I003"
						args = []string{"issue", "attachment", "download", "41"}
						if lane.name == "before" {
							args = []string{"issue", "attachment", "download", "PAIR-1", "41"}
						}
						if scenario != "download-unresolved" {
							target := home
							if lane.name == "before" {
								target = filepath.Join(home, "preview.bin")
							}
							args = append(args, "--to", target)
							if lane.name == "after" {
								args = append(args, "--as", "preview.bin")
							}
						}
						args = append(args, "--dry-run")
						if scenario == "download-collision" {
							if err := os.WriteFile(filepath.Join(home, "preview.bin"), []byte("owned collision preimage"), 0o600); err != nil {
								t.Fatal(err)
							}
							expectedExit = 3
							code = "local_io_failed"
							if lane.name == "before" {
								code = "validation_failed"
							}
						}
					}
					before := parityLocalTree(t, home)
					if err := parityFingerprint(lane.binary, lane.digest); err != nil {
						t.Fatal(err)
					}
					result := parityFixtureProcessAt(t, lane.name, lane.binary, server.URL, home, parityFixtureInput{Config: config, Output: mode}, extra, args...)
					evidence.Invocations = append(evidence.Invocations, parityLocalInvocation{Lane: lane.name, Digest: lane.digest, Argv: result.Argv, Exit: result.Exit, OutputBytes: result.Stdout, StderrBytes: result.Stderr})
					if result.Exit != expectedExit || expectedExit == 0 && len(result.Stderr) != 0 || calls.Load() != 0 || !reflect.DeepEqual(before, parityLocalTree(t, home)) {
						t.Fatalf("attachment local entry/effects changed: exit%d want%d HTTP%d stdout%s stderr%s", result.Exit, expectedExit, calls.Load(), result.Stdout, result.Stderr)
					}
					raw := result.Stdout
					if mode == "json" || expectedExit != 0 {
						parityAuthoringData(t, result.Stdout, lane.name, parityExpectedOperation(lane.name, args, expectedExit), expectedExit)
						raw = parityOriginalFrameData(result.Stdout)
					}
					if expectedExit != 0 {
						var frame struct {
							Errors   []struct{ Code string }
							Warnings json.RawMessage
						}
						if json.Unmarshal(result.Stdout, &frame) != nil || len(frame.Errors) != 1 || frame.Errors[0].Code != code || !parityAuthoringJSON(frame.Warnings, []byte(`[]`)) {
							t.Fatal("attachment refusal lost exact classification")
						}
						if scenario == "upload-readonly" {
							if !parityAttachmentRefusalData(raw, lane.name, "PAIR-1", paths, sizes, mimes) {
								t.Fatalf("upload refusal differs from its independent retained input: %s", raw)
							}
						} else if !parityAuthoringJSON(raw, []byte(`null`)) {
							t.Fatalf("unacknowledged attachment refusal invented data: %s", raw)
						}
					} else {
						switch scenario {
						case "upload-positional", "upload-files":
							if !parityAttachmentAddData(raw, lane.name, "PAIR-1", paths, sizes, mimes, true, nil) {
								t.Fatalf("upload preview changed literal file metadata: %s", raw)
							}
						case "delete-preview":
							if !parityAttachmentDeleteData(raw, lane.name, "PAIR-1", "41", true) {
								t.Fatalf("delete preview invented an effect: %s", raw)
							}
						case "download-preview":
							if !parityAttachmentDownloadData(raw, lane.name, "PAIR-1", "41", home, "preview.bin", true, false, 0) {
								t.Fatalf("download preview changed the mapped destination: %s", raw)
							}
						case "download-unresolved":
							expected := map[string]any{"attachment_id": "41", "directory": home, "filename_resolved": false, "dry_run": true}
							if lane.name == "before" {
								expected = map[string]any{"issue": map[string]any{"key": "PAIR-1"}, "attachment_id": "41", "mode": "current-dir", "target": "", "dry_run": true}
							}
							want, err := json.Marshal(expected)
							if err != nil || !parityAuthoringJSON(raw, want) {
								t.Fatalf("unresolved download preview invented a name or file: %s", raw)
							}
							if mode == "json" {
								var frame struct {
									Warnings []struct {
										Type  string
										Lossy *bool
									}
								}
								if json.Unmarshal(result.Stdout, &frame) != nil || lane.name == "after" && (len(frame.Warnings) != 1 || frame.Warnings[0].Type != "filename_unresolved" || frame.Warnings[0].Lossy == nil || *frame.Warnings[0].Lossy) || lane.name == "before" && len(frame.Warnings) != 0 {
									t.Fatal("unresolved preview lost its explicit provenance")
								}
							}
						}
					}
					if err := parityFingerprint(lane.binary, lane.digest); err != nil {
						t.Fatal(err)
					}
					check := id + ".attachment.local_" + scenario + "." + mode
					evidence.Checks[check] = append(evidence.Checks[check], lane.name)
				})
			}
		}
	}
	saveParityLocal(t, "attachment", evidence)
}

func TestParityAttachmentMutationAdmission(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "binary")
	input := filepath.Join(dir, "owned.bin")
	const marker = "independent attachment binary admission fixture"
	if err := os.WriteFile(binary, []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := []byte{0, 0xff, 1}
	if err := os.WriteFile(input, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	before := parityLane{Name: "before", Binary: binary, Digest: fmt.Sprintf("%x", sha256.Sum256([]byte(marker)))}
	after := before
	after.Name = "after"
	p := &parityRun{owner: &liveRun{verified: true, manifest: liveManifest{Issues: []ownedIssue{{Key: "PAIR-1", ID: "1", Attachments: []ownedAttachment{{ID: "41", Filename: "owned.bin", Size: 3}}}, {Key: "PAIR-2", ID: "2"}}}, attachmentInputs: map[string][sha256.Size]byte{input: sha256.Sum256(payload)}}, evidence: parityEvidence{Lanes: []parityLane{before, after}}}
	for _, lane := range p.evidence.Lanes {
		t.Run(lane.Name, func(t *testing.T) {
			upload := []string{"issue", "attachment", "add", "PAIR-1", input}
			deletion := []string{"issue", "attachment", "delete", "41", "--force"}
			if lane.Name == "before" {
				deletion = []string{"issue", "attachment", "delete", "PAIR-1", "41", "--force"}
			}
			for _, args := range [][]string{upload, append(slices.Clone(upload), "--dry-run"), deletion} {
				if err := p.validateAttachmentMutation(lane, "PAIR-1", args); err != nil {
					t.Fatal(err)
				}
			}
			for _, tc := range []struct {
				name, key string
				args      []string
			}{
				{"foreign-parent", "PAIR-3", upload},
				{"other-owned-parent", "PAIR-2", deletion},
				{"foreign-resource", "PAIR-1", func() []string {
					a := slices.Clone(deletion)
					for i, v := range a {
						if v == "41" {
							a[i] = "42"
						}
					}
					return a
				}()},
				{"missing-input", "PAIR-1", []string{"issue", "attachment", "add", "PAIR-1", input + ".missing"}},
				{"stdin-input", "PAIR-1", []string{"issue", "attachment", "add", "PAIR-1", "-"}},
				{"extra-key", "PAIR-1", []string{"issue", "attachment", "add", "PAIR-1", "PAIR-2", input}},
				{"repeated-input", "PAIR-1", append(slices.Clone(upload), input)},
				{"selector", "PAIR-1", append(slices.Clone(upload), "--jql=project=PAIR")},
				{"unknown-option", "PAIR-1", append(slices.Clone(deletion), "--cascade")},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if p.validateAttachmentMutation(lane, tc.key, tc.args) == nil {
						t.Fatal("attachment admission accepted foreign identity or unregistered bytes")
					}
				})
			}
			t.Run("changed-input", func(t *testing.T) {
				if err := os.WriteFile(input, []byte("changed"), 0o600); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := os.WriteFile(input, payload, 0o600); err != nil {
						t.Error(err)
					}
				}()
				if p.validateAttachmentMutation(lane, "PAIR-1", upload) == nil {
					t.Fatal("attachment admission reused an earlier input digest")
				}
			})
		})
	}
	t.Run("changed-binary", func(t *testing.T) {
		if err := os.WriteFile(binary, []byte("different executable"), 0o600); err != nil {
			t.Fatal(err)
		}
		if p.validateAttachmentMutation(after, "PAIR-1", []string{"issue", "attachment", "delete", "41", "--force"}) == nil {
			t.Fatal("attachment admission accepted a changed selected executable")
		}
	})
}

func TestParityAttachmentInventoryGuards(t *testing.T) {
	const row = `{"id":"41","filename":"empty.bin","size":0,"mimeType":"application/octet-stream","created":"2026-01-01T00:00:00.000+0000","content":"https://jira.invalid/content/41","author":{"accountId":"fixture-account","displayName":"Fixture"}}`
	const complete = `{"id":"1","key":"PAIR-1","fields":{"attachment":[` + row + `]}}`
	if native, ok := parityAttachmentInventory([]byte(complete), "1", "PAIR-1"); !ok || len(native) != 1 || native[0].Size != 0 {
		t.Fatal("explicit native empty-file metadata refused")
	}
	for name, replacement := range map[string][2]string{
		"missing-size":      {`"size":0,`, ``},
		"null-size":         {`"size":0`, `"size":null`},
		"negative-size":     {`"size":0`, `"size":-1`},
		"duplicate-size":    {`"size":0`, `"size":7,"size":0`},
		"foreign-parent":    {`"key":"PAIR-1"`, `"key":"PAIR-2"`},
		"foreign-parent-id": {`"id":"1"`, `"id":"2"`},
		"missing-array":     {`"attachment":[` + row + `]`, ``},
		"null-array":        {`"attachment":[` + row + `]`, `"attachment":null`},
		"repeated-id":       {`"attachment":[` + row + `]`, `"attachment":[` + row + `,` + row + `]`},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := parityAttachmentInventory([]byte(strings.Replace(complete, replacement[0], replacement[1], 1)), "1", "PAIR-1"); ok {
				t.Fatal("native inventory guard accepted absent, ambiguous or foreign metadata")
			}
		})
	}
}

func TestParityAttachmentDirectoryGuards(t *testing.T) {
	target := parityLocalFile{Mode: 0o600, Hash: sha256.Sum256([]byte("owned"))}
	sibling := parityLocalFile{Mode: 0o600, Hash: sha256.Sum256([]byte("sibling"))}
	before := map[string]parityLocalFile{"sibling.bin": sibling}
	after := maps.Clone(before)
	after["target.bin"] = target
	if !parityAttachmentDirectory(before, after, "target.bin", []byte("owned"), true) || !parityAttachmentDirectory(before, before, "target.bin", nil, false) {
		t.Fatal("exact owned directory publication/no-op refused")
	}
	for _, scenario := range []string{"extra-file", "changed-sibling", "changed-sibling-mode", "missing-target", "wrong-bytes", "target-directory", "changed-preview"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := maps.Clone(after)
			changed := true
			switch scenario {
			case "extra-file":
				candidate["unrequested.bin"] = target
			case "changed-sibling":
				candidate["sibling.bin"] = target
			case "changed-sibling-mode":
				file := candidate["sibling.bin"]
				file.Mode = 0o400
				candidate["sibling.bin"] = file
			case "missing-target":
				delete(candidate, "target.bin")
			case "wrong-bytes":
				candidate["target.bin"] = sibling
			case "target-directory":
				file := target
				file.Mode = os.ModeDir | 0o700
				candidate["target.bin"] = file
			case "changed-preview":
				changed = false
			}
			if parityAttachmentDirectory(before, candidate, "target.bin", []byte("owned"), changed) {
				t.Fatal("download directory guard admitted collateral changes or wrong publication")
			}
		})
	}
}

func TestParityAttachmentListPagination(t *testing.T) {
	for _, total := range []int{0, 3} {
		valid := fmt.Appendf(nil, `{"meta":{"pagination":{"startAt":0,"maxResults":50,"total":%d,"isLast":true}}}`, total)
		if !parityAttachmentListPagination(valid, "before", total) || !parityAttachmentListPagination([]byte(`{"meta":{}}`), "after", total) {
			t.Fatal("native attachment pagination difference refused")
		}
	}
	for _, tc := range []struct{ name, lane, raw string }{
		{"wrong-total", "before", `{"meta":{"pagination":{"startAt":0,"maxResults":50,"total":2,"isLast":true}}}`},
		{"missing-total", "before", `{"meta":{"pagination":{"startAt":0,"maxResults":50,"isLast":true}}}`},
		{"null-total", "before", `{"meta":{"pagination":{"startAt":0,"maxResults":50,"total":null,"isLast":true}}}`},
		{"incomplete", "before", `{"meta":{"pagination":{"startAt":0,"maxResults":50,"total":3,"isLast":false}}}`},
		{"invented-cursor", "before", `{"meta":{"pagination":{"startAt":0,"maxResults":50,"total":3,"isLast":true,"nextCursor":"foreign"}}}`},
		{"missing-pagination", "before", `{"meta":{}}`},
		{"foreign-current-pagination", "after", `{"meta":{"pagination":{"total":3}}}`},
		{"null-current-pagination", "after", `{"meta":{"pagination":null}}`},
		{"duplicate-pagination", "before", `{"meta":{"pagination":null,"pagination":{"startAt":0,"maxResults":50,"total":3,"isLast":true}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if parityAttachmentListPagination([]byte(tc.raw), tc.lane, 3) {
				t.Fatal("attachment pagination admitted missing, foreign or misleading completeness")
			}
		})
	}
}

func TestParityAttachmentUploadOrder(t *testing.T) {
	first := parityAttachmentNative{ID: "41", Filename: "z.bin", Size: 3}
	second := parityAttachmentNative{ID: "42", Filename: "a.bin", Size: 0}
	ordered, ok := parityAttachmentUploadOrder([]parityAttachmentNative{second, first}, []string{"z.bin", "a.bin"})
	if !ok || !slices.Equal(ordered, []parityAttachmentNative{first, second}) {
		t.Fatal("independent inventory was not bound to authored multipart order")
	}
	for name, tc := range map[string]struct {
		native []parityAttachmentNative
		names  []string
	}{
		"missing":                 {[]parityAttachmentNative{first}, []string{"z.bin", "a.bin"}},
		"foreign":                 {[]parityAttachmentNative{first, second}, []string{"z.bin", "other.bin"}},
		"duplicate-native-name":   {[]parityAttachmentNative{first, first}, []string{"z.bin", "a.bin"}},
		"duplicate-authored-name": {[]parityAttachmentNative{first, second}, []string{"z.bin", "z.bin"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := parityAttachmentUploadOrder(tc.native, tc.names); ok {
				t.Fatal("upload order accepted incomplete or ambiguous binding")
			}
		})
	}
}
