//go:build live

package live

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

type ownedAttachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	Deleted  bool   `json:"deleted"`
}

// Attachment deletion takes a site-wide resource ID. Bind it to an active
// attachment recorded on this run's issue; uploads accept only registered
// authored bytes and a single explicit owned issue, without selectors.
func (r *liveRun) checkAttachmentMutation(keys, args []string) error {
	if !r.verified || len(keys) != 1 || len(args) < 4 || args[0] != "issue" || args[1] != "attachment" {
		return errors.New("attachment mutation requires one verified owned issue")
	}
	issue := r.ownedLiveIssue(keys[0])
	if issue == nil || !liveKey.MatchString(keys[0]) {
		return errors.New("attachment mutation targets an inactive or foreign issue")
	}
	start := 4
	switch args[2] {
	case "add":
		if args[3] != issue.Key {
			return errors.New("attachment upload differs from its ownership declaration")
		}
		seen := map[string]bool{}
		for start < len(args) && !strings.HasPrefix(args[start], "--") {
			path := args[start]
			digest, registered := r.attachmentInputs[path]
			if !registered || seen[path] {
				return errors.New("attachment upload requires distinct registered inputs")
			}
			seen[path] = true
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				return errors.New("attachment input is no longer a regular authored file")
			}
			data, err := os.ReadFile(path)
			if err != nil || sha256.Sum256(data) != digest {
				return errors.New("attachment bytes changed after registration")
			}
			start++
		}
		if len(seen) == 0 {
			return errors.New("attachment upload requires authored files")
		}
	case "delete":
		if r.ownedLiveAttachment(issue, args[3]) == nil {
			return errors.New("attachment deletion requires an active resource on this owned issue")
		}
	default:
		return errors.New("unsupported owned attachment mutation")
	}
	seen := map[string]bool{}
	for _, arg := range args[start:] {
		if seen[arg] || arg != "--dry-run" && (args[2] != "delete" || arg != "--force") {
			return errors.New("attachment mutation contains a repeated or unauthored option")
		}
		seen[arg] = true
	}
	return nil
}

func (r *liveRun) ownedLiveAttachment(issue *ownedIssue, id string) *ownedAttachment {
	for i := range issue.Attachments {
		attachment := &issue.Attachments[i]
		if attachment.ID == id && !attachment.Deleted {
			return attachment
		}
	}
	return nil
}

func (r *liveRun) attachmentInput(t *testing.T, name string, data []byte) string {
	t.Helper()
	if filepath.Base(name) != name || name == "." || name == ".." {
		t.Fatal("attachment fixture must have a plain authored name")
	}
	path := filepath.Join(r.dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if r.attachmentInputs == nil {
		r.attachmentInputs = make(map[string][sha256.Size]byte)
	}
	r.attachmentInputs[path] = sha256.Sum256(data)
	return path
}

func (r *liveRun) registerAttachment(t *testing.T, key string, attachment envelope.AttachmentView) {
	t.Helper()
	issue := r.ownedLiveIssue(key)
	id, err := strconv.ParseUint(attachment.ID, 10, 64)
	if issue == nil || err != nil || id == 0 || !attachment.Size.Set || attachment.Size.Null || attachment.Size.Value < 0 || attachment.Filename == "" {
		t.Fatal("attachment receipt lacks an active owned issue and numeric resource identity")
	}
	for _, owned := range r.manifest.Issues {
		if slices.ContainsFunc(owned.Attachments, func(a ownedAttachment) bool { return a.ID == attachment.ID }) {
			t.Fatal("attachment upload returned an already registered identity")
		}
	}
	issue.Attachments = append(issue.Attachments, ownedAttachment{ID: attachment.ID, Filename: attachment.Filename, Size: attachment.Size.Value})
	r.save(t)
}

func liveAttachments(t *testing.T, r *liveRun, key string) []envelope.AttachmentView {
	t.Helper()
	frame, raw, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "attachment", "list", key)
	var out struct {
		Data envelope.IssueAttachmentListOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(raw, &out) != nil || len(out.Data.Results) != 1 || !out.Data.Results[0].OK || out.Data.Results[0].Issue.Key != key || out.Data.Results[0].Data == nil || out.Data.Results[0].Data.Attachments == nil {
		t.Fatal("attachment listing lost its owned key or complete resource array")
	}
	return out.Data.Results[0].Data.Attachments
}

func rawOwnedAttachments(t *testing.T, r *liveRun, key string) []ownedAttachment {
	t.Helper()
	if r.ownedLiveIssue(key) == nil {
		t.Fatal("attachment read requires an active owned issue")
	}
	read := rawRead(t, r, "api", "GET", "/rest/api/3/issue/"+key, "--query=fields=attachment,summary")
	var issue struct {
		Key    string `json:"key"`
		Fields *struct {
			Attachments []ownedAttachment `json:"attachment"`
		} `json:"fields"`
	}
	if json.Unmarshal(read.Response.JSON, &issue) != nil || issue.Key != key || issue.Fields == nil || issue.Fields.Attachments == nil {
		t.Fatal("raw attachment read lost its owned issue identity or fields")
	}
	return issue.Fields.Attachments
}

func TestLiveAttachments(t *testing.T) {
	r := newLiveRun(t)
	key := r.create(t, "attachment lifecycle")
	if len(liveAttachments(t, r, key)) != 0 || len(rawOwnedAttachments(t, r, key)) != 0 {
		t.Fatal("new owned fixture already has attachments")
	}
	content := bytes.Repeat([]byte{0, 0xff, '<', '&', '>', '\n'}, 8192)
	text := []byte("Owned attachment acceptance <&> \u2028\n")
	paths := []string{r.attachmentInput(t, "binary.bin", content), r.attachmentInput(t, "note.txt", text), r.attachmentInput(t, "empty.bin", nil)}
	details := []envelope.AttachmentFileInput{
		{Path: paths[0], Size: int64(len(content)), MIMEInferred: "application/octet-stream"},
		{Path: paths[1], Size: int64(len(text)), MIMEInferred: "text/plain"},
		{Path: paths[2], Size: 0, MIMEInferred: "application/octet-stream"},
	}
	args := append([]string{"issue", "attachment", "add", key}, paths...)
	for _, preview := range []bool{true, false} {
		call := slices.Clone(args)
		extra := []string{"JIRA_READ_ONLY=true"}
		if preview {
			call = append(call, "--dry-run")
			extra = nil
		}
		if err := r.checkMutation([]string{key}, call); err != nil {
			t.Fatal(err)
		}
		frame, raw, exit := r.invoke(t, t.Context(), extra, call...)
		if preview {
			var out struct {
				Data envelope.IssueAttachmentAddOutput `json:"data"`
			}
			if exit != 0 || !frame.OK || json.Unmarshal(raw, &out) != nil || !out.Data.DryRun || !slices.Equal(out.Data.Files, paths) || len(out.Data.Results) != 1 || out.Data.Results[0].Issue.Key != key || !out.Data.Results[0].OK || out.Data.Results[0].Data == nil || len(out.Data.Results[0].Data.Attachments) != 0 {
				t.Fatal("upload preview invented resources or lost authored targets/files")
			}
			if !slices.Equal(out.Data.FileDetails, details) {
				t.Fatal("upload preview lost authored paths, sizes or inferred MIME")
			}
		} else if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "read_only_blocked" {
			t.Fatal("attachment upload bypassed read-only policy")
		}
		if len(rawOwnedAttachments(t, r, key)) != 0 {
			t.Fatal("upload preview/refusal changed Jira")
		}
	}
	if err := r.checkMutation([]string{key}, args); err != nil {
		t.Fatal(err)
	}
	frame, raw, exit := r.invoke(t, t.Context(), nil, args...)
	var upload struct {
		Data envelope.IssueAttachmentAddOutput `json:"data"`
	}
	if exit != 0 || !frame.OK || json.Unmarshal(raw, &upload) != nil || upload.Data.DryRun || len(upload.Data.Results) != 1 || upload.Data.Results[0].Issue.Key != key || !upload.Data.Results[0].OK || upload.Data.Results[0].Data == nil || len(upload.Data.Results[0].Data.Attachments) != 3 {
		t.Fatal("single upload did not return three owned resources; inspect receipts, never replay it")
	}
	if !slices.Equal(upload.Data.FileDetails, details) {
		t.Fatal("upload lost independently authored input metadata")
	}
	stored := rawOwnedAttachments(t, r, key)
	listed := liveAttachments(t, r, key)
	if len(stored) != 3 || len(listed) != 3 {
		t.Fatal("single upload stored duplicate or missing attachments")
	}
	want := map[string][]byte{"binary.bin": content, "note.txt": text, "empty.bin": nil}
	seenNames := map[string]bool{}
	for _, attachment := range upload.Data.Results[0].Data.Attachments {
		data, ok := want[attachment.Filename]
		if !ok || seenNames[attachment.Filename] || attachment.Size != jira.NullableOf(int64(len(data))) || !slices.ContainsFunc(stored, func(a ownedAttachment) bool {
			return a.ID == attachment.ID && a.Filename == attachment.Filename && jira.NullableOf(a.Size) == attachment.Size
		}) || !slices.ContainsFunc(listed, func(a envelope.AttachmentView) bool {
			return a.ID == attachment.ID && a.Filename == attachment.Filename && a.Size == attachment.Size
		}) {
			t.Fatal("upload receipt differs from independently stored/listed metadata")
		}
		seenNames[attachment.Filename] = true
		r.registerAttachment(t, key, attachment)
	}
	dir := filepath.Join(r.dir, "downloads")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, attachment := range upload.Data.Results[0].Data.Attachments {
		for _, remote := range []bool{false, true} {
			call := []string{"issue", "attachment", "download", attachment.ID, "--to", dir, "--dry-run"}
			if remote {
				call = append(call, "--validate-remote")
			}
			frame, raw, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, call...)
			var out struct {
				Data envelope.IssueAttachmentDownloadOutput `json:"data"`
			}
			if exit != 0 || !frame.OK || json.Unmarshal(raw, &out) != nil || !out.Data.DryRun || out.Data.AttachmentID != attachment.ID || out.Data.FilenameResolved != remote || out.Data.BytesWritten != nil || out.Data.Overwrote != nil || remote && (out.Data.Path != filepath.Join(dir, attachment.Filename) || out.Data.Filename != attachment.Filename) {
				t.Fatal("download preview fabricated publication or lost resolved metadata")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatal("download preview wrote files")
			}
		}
		frame, raw, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "attachment", "download", attachment.ID, "--to", dir)
		var out struct {
			Data envelope.IssueAttachmentDownloadOutput `json:"data"`
		}
		path := filepath.Join(dir, attachment.Filename)
		data, err := os.ReadFile(path)
		if exit != 0 || !frame.OK || json.Unmarshal(raw, &out) != nil || err != nil || !bytes.Equal(data, want[attachment.Filename]) || out.Data.BytesWritten == nil || jira.NullableOf(*out.Data.BytesWritten) != attachment.Size || out.Data.Path != path || out.Data.Overwrote == nil || *out.Data.Overwrote {
			t.Fatal("live download changed exact bytes or its publication receipt")
		}
		frame, _, exit = r.invoke(t, t.Context(), nil, "issue", "attachment", "download", attachment.ID, "--to", dir)
		if exit != 3 || frame.OK || len(frame.Errors) != 1 || frame.Errors[0].Code != "local_io_failed" {
			t.Fatal("download collision did not refuse")
		}
		data, err = os.ReadFile(path)
		if err != nil || !bytes.Equal(data, want[attachment.Filename]) {
			t.Fatal("download collision changed the existing bytes")
		}
		if err := os.WriteFile(path, []byte("replace only when complete"), 0o600); err != nil {
			t.Fatal(err)
		}
		frame, raw, exit = r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "issue", "attachment", "download", attachment.ID, "--to", dir, "--overwrite")
		data, err = os.ReadFile(path)
		out.Data = envelope.IssueAttachmentDownloadOutput{}
		if exit != 0 || !frame.OK || json.Unmarshal(raw, &out) != nil || err != nil || !bytes.Equal(data, want[attachment.Filename]) || out.Data.BytesWritten == nil || jira.NullableOf(*out.Data.BytesWritten) != attachment.Size || out.Data.Path != path || out.Data.Overwrote == nil || !*out.Data.Overwrote {
			t.Fatal("live overwrite lost bytes or receipt")
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	for _, attachment := range upload.Data.Results[0].Data.Attachments {
		args := []string{"issue", "attachment", "delete", attachment.ID}
		for _, scenario := range []string{"preview", "confirmation", "read-only"} {
			call := slices.Clone(args)
			extra := []string(nil)
			if scenario == "preview" {
				call = append(call, "--dry-run")
			}
			if scenario == "read-only" {
				call = append(call, "--force")
				extra = []string{"JIRA_READ_ONLY=true"}
			}
			if err := r.checkMutation([]string{key}, call); err != nil {
				t.Fatal(err)
			}
			frame, _, exit := r.invoke(t, t.Context(), extra, call...)
			if scenario == "preview" {
				if exit != 0 || !frame.OK || !frame.Data.DryRun {
					t.Fatal("attachment delete preview failed")
				}
			} else if exit != 3 || frame.OK || len(frame.Errors) != 1 || scenario == "read-only" && frame.Errors[0].Code != "read_only_blocked" || scenario == "confirmation" && frame.Errors[0].Code != "flag_required" {
				t.Fatal("attachment deletion escaped refusal")
			}
			if !slices.ContainsFunc(rawOwnedAttachments(t, r, key), func(a ownedAttachment) bool { return a.ID == attachment.ID }) {
				t.Fatal("delete preview/refusal removed the attachment")
			}
		}
		deleteArgs := append(args, "--force")
		if err := r.checkMutation([]string{key}, deleteArgs); err != nil {
			t.Fatal(err)
		}
		frame, raw, exit := r.invoke(t, t.Context(), nil, deleteArgs...)
		var deleted struct {
			Data envelope.IssueAttachmentDeleteOutput `json:"data"`
		}
		if exit != 0 || !frame.OK || json.Unmarshal(raw, &deleted) != nil || deleted.Data.DryRun || deleted.Data.AttachmentID != attachment.ID {
			t.Fatal("attachment deletion lost its exact acknowledgement; inspect before retrying")
		}
		missing, _, exit := r.invoke(t, t.Context(), []string{"JIRA_READ_ONLY=true"}, "api", "GET", "/rest/api/3/attachment/"+attachment.ID)
		if exit != 2 || missing.OK || len(missing.Errors) != 1 || missing.Errors[0].Code != "jira_not_found" {
			t.Fatal("attachment deletion was not independently confirmed")
		}
		r.ownedLiveAttachment(r.ownedLiveIssue(key), attachment.ID).Deleted = true
		r.save(t)
		remaining := rawOwnedAttachments(t, r, key)
		listed := liveAttachments(t, r, key)
		want := 0
		for _, sibling := range r.ownedLiveIssue(key).Attachments {
			if sibling.Deleted {
				continue
			}
			want++
			if !slices.ContainsFunc(remaining, func(a ownedAttachment) bool {
				return a.ID == sibling.ID && a.Filename == sibling.Filename && a.Size == sibling.Size
			}) || !slices.ContainsFunc(listed, func(a envelope.AttachmentView) bool {
				return a.ID == sibling.ID && a.Filename == sibling.Filename && a.Size == jira.NullableOf(sibling.Size)
			}) {
				t.Fatal("attachment deletion changed a surviving sibling")
			}
		}
		if len(remaining) != want || len(listed) != want {
			t.Fatal("attachment deletion left the wrong native or domain resource count")
		}
	}
	if len(liveAttachments(t, r, key)) != 0 || len(rawOwnedAttachments(t, r, key)) != 0 {
		t.Fatal("deleted attachments remained on the owned fixture")
	}
}
