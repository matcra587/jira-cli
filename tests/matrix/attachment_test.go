//go:build flagmatrix

package matrix

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The `jira issue attachment` group (add, list, download, delete). The
// harness lives in list_test.go. Every case keeps its files inside the
// run's own t.TempDir — the download verb is the only one in this CLI
// that writes to disk at all, and its cases assert exactly where.

const (
	mx3AttachmentMetaPath    = "/rest/api/3/attachment/10042"
	mx3AttachmentContentPath = "/rest/api/3/attachment/content/10042"
	mx3AttachmentBytes       = "hello bytes"
)

// mx3Upload is one multipart upload the attachment stub accepted.
type mx3Upload struct {
	Key       string
	Filenames []string
	Content   string
}

// mx3AttachmentAddHandler answers the upload endpoint, parsing each
// multipart body so the cases can assert which file landed on which key.
func mx3AttachmentAddHandler(t *testing.T, mu *sync.Mutex, uploads *[]mx3Upload) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := strings.CutPrefix(r.URL.Path, "/rest/api/3/issue/")
		key, hasSuffix := strings.CutSuffix(key, "/attachments")
		if !ok || !hasSuffix || r.Method != http.MethodPost {
			http.Error(w, `{"errorMessages":["unexpected request"]}`, http.StatusBadRequest)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart upload: %v", err)
			http.Error(w, `{"errorMessages":["bad multipart"]}`, http.StatusBadRequest)
			return
		}
		up := mx3Upload{Key: key}
		for _, header := range r.MultipartForm.File["file"] {
			up.Filenames = append(up.Filenames, header.Filename)
			f, err := header.Open()
			if err != nil {
				t.Errorf("open multipart part: %v", err)
				continue
			}
			buf := make([]byte, header.Size)
			n, _ := f.Read(buf) // a short read surfaces as a content mismatch
			f.Close()           //nolint:errcheck // read-only part
			up.Content = string(buf[:n])
		}
		mu.Lock()
		*uploads = append(*uploads, up)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[{"id":"9001","filename":"note.txt","size":5,"mimeType":"text/plain","created":"2026-08-01T00:00:00.000+0000","author":{"displayName":"A Person"}}]`)
	})
}

// --file flips the positional grammar: with it EVERY positional is an
// issue key, and the named file is what uploads — to each key.
func TestAttachmentAddFileFlagSetsGrammarAndUploads(t *testing.T) {
	var mu sync.Mutex
	var uploads []mx3Upload
	baseURL := mx3Stub(t, mx3AttachmentAddHandler(t, &mu, &uploads))
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "note.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("seed upload file: %v", err)
	}
	// Without --file, KEY-124 would be read as a PATH (and fail to open);
	// with it, both positionals are keys and note.txt uploads to each.
	res := mx3Run(t, home, baseURL, "issue", "attachment", "add", "KEY-123", "KEY-124",
		"--file", "note.txt", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("attachment add --file: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	mu.Lock()
	got := append([]mx3Upload(nil), uploads...)
	mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("uploads = %d, want 2 (one per key)", len(got))
	}
	keys := map[string]bool{}
	for _, up := range got {
		keys[up.Key] = true
		if len(up.Filenames) != 1 || up.Filenames[0] != "note.txt" {
			t.Fatalf("upload to %s carried filenames %v, want [note.txt]", up.Key, up.Filenames)
		}
		if up.Content != "hello" {
			t.Fatalf("upload to %s carried content %q, want the seeded file's bytes", up.Key, up.Content)
		}
	}
	if !keys["KEY-123"] || !keys["KEY-124"] {
		t.Fatalf("uploaded keys = %v, want KEY-123 and KEY-124", keys)
	}
	Prove(t, "jira issue attachment add", "file",
		"--file made BOTH positionals issue keys: the stub saw note.txt's bytes POSTed to KEY-123 and KEY-124 (without the flag the second positional is a path)")
}

// A bare upload preview checks local metadata without HTTP requests.
func TestAttachmentAddDryRunChecksMetadataAndSendsNothing(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "empty.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	res := mx3Run(t, home, baseURL, "issue", "attachment", "add", "KEY-123",
		"empty.txt", "--dry-run", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("attachment add --dry-run: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	mx3RequireNoRequests(t, rec)
	data := mx3Data(t, res)
	details, ok := data["file_details"].([]any)
	if data["dry_run"] != true || !ok || len(details) != 1 {
		t.Fatalf("preview lost metadata: %v", data)
	}
	file, ok := details[0].(map[string]any)
	if !ok || file["path"] != "empty.txt" || file["size"] != float64(0) || file["mime_inferred"] != "text/plain" {
		t.Fatalf("preview lost explicit empty size, authored path or inferred MIME: %v", file)
	}
	missing := mx3Run(t, home, baseURL, "issue", "attachment", "add", "KEY-123",
		"does-not-exist.log", "--dry-run", "--output=json")
	if missing.Exit != 3 {
		t.Fatalf("missing preview input was accepted: exit=%d", missing.Exit)
	}
	mx3RequireNoRequests(t, rec)
	Prove(t, "jira issue attachment add", "dry-run",
		"the preview reports explicit empty-file metadata, refuses a missing path, and sends zero HTTP requests")
}

func TestAttachmentAddParallelismRefusedOutOfRange(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "attachment", "add", "KEY-123",
		"--file", "note.txt", "--parallelism", "0", "--output=json")
	mx3RequireRefusal(t, res, "flag_value_invalid", "parallelism")
	mx3RequireNoRequests(t, rec)
	Prove(t, "jira issue attachment add", "parallelism",
		"--parallelism 0 refused by code flag_value_invalid on field parallelism, before any request")
}

func TestAttachmentListParallelismRefusedOutOfRange(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "attachment", "list", "KEY-123",
		"--parallelism", "99", "--output=json")
	mx3RequireRefusal(t, res, "flag_value_invalid", "parallelism")
	mx3RequireNoRequests(t, rec)
	Prove(t, "jira issue attachment list", "parallelism",
		"--parallelism 99 refused by code flag_value_invalid on field parallelism, before any request")
}

// mx3AttachmentDeleteHandler answers the delete endpoint, recording.
func mx3AttachmentDeleteHandler(rec *mx3Recorder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		if req.Method == http.MethodDelete && req.Path == mx3AttachmentMetaPath {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, `{"errorMessages":["unexpected request"]}`, http.StatusBadRequest)
	})
}

func TestAttachmentDeleteDryRunContactsNothing(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "attachment", "delete", "10042", "--dry-run", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("attachment delete --dry-run: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	mx3RequireNoRequests(t, rec)
	data := mx3Data(t, res)
	if data["dry_run"] != true || data["attachment_id"] != "10042" {
		t.Fatalf("data = %v, want dry_run=true and attachment_id=10042", data)
	}
	Prove(t, "jira issue attachment delete", "dry-run",
		"stub saw zero requests (an ignored flag would have confirmed and DELETEd) and the envelope published dry_run=true")
}

// --force is the non-interactive confirmation: without it this
// destructive command refuses (there is no terminal here to ask at),
// with it the DELETE goes out.
func TestAttachmentDeleteForceGatesTheDelete(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3AttachmentDeleteHandler(rec))
	home := t.TempDir()

	refused := mx3Run(t, home, baseURL, "issue", "attachment", "delete", "10042", "--output=json")
	mx3RequireRefusal(t, refused, "flag_required", "force")
	mx3RequireNoRequests(t, rec)

	forced := mx3Run(t, home, baseURL, "issue", "attachment", "delete", "10042", "--force", "--output=json")
	if forced.Exit != 0 {
		t.Fatalf("attachment delete --force: exit %d\nstderr: %s", forced.Exit, forced.Stderr)
	}
	deletes := rec.byPath(mx3AttachmentMetaPath)
	if len(deletes) != 1 || deletes[0].Method != http.MethodDelete {
		t.Fatalf("deletes = %v, want exactly one DELETE %s", deletes, mx3AttachmentMetaPath)
	}
	Prove(t, "jira issue attachment delete", "force",
		"without it the non-interactive run refused by code flag_required on field force with zero requests; with it the stub saw the DELETE")
}

// --yes is --force's hidden alias: the same DELETE goes out.
func TestAttachmentDeleteHiddenYesAlias(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3AttachmentDeleteHandler(rec))
	res := mx3Run(t, t.TempDir(), baseURL, "issue", "attachment", "delete", "10042", "--yes", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("attachment delete --yes: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	deletes := rec.byPath(mx3AttachmentMetaPath)
	if len(deletes) != 1 || deletes[0].Method != http.MethodDelete {
		t.Fatalf("deletes = %v, want exactly one DELETE %s", deletes, mx3AttachmentMetaPath)
	}
	Prove(t, "jira issue attachment delete", "yes",
		"hidden --yes skipped the non-interactive refusal exactly as --force does: the stub saw the DELETE")
}

// mx3DownloadHandler answers the metadata and content endpoints.
func mx3DownloadHandler(rec *mx3Recorder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := rec.record(r)
		switch {
		case req.Method == http.MethodGet && req.Path == mx3AttachmentMetaPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"id":10042,"filename":"report.pdf","size":11,"mimeType":"application/pdf","created":"2026-08-01T00:00:00.000+0000","author":{"displayName":"A Person"}}`)
		case req.Method == http.MethodGet && req.Path == mx3AttachmentContentPath:
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = fmt.Fprint(w, mx3AttachmentBytes)
		default:
			http.Error(w, `{"errorMessages":["unexpected request"]}`, http.StatusBadRequest)
		}
	})
}

// --to lands the file in the named directory, under the server's own
// (reduced) filename.
func TestAttachmentDownloadToLandsInNamedDirectory(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3DownloadHandler(rec))
	home := t.TempDir()
	dest := filepath.Join(home, "out")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}
	res := mx3Run(t, home, baseURL, "issue", "attachment", "download", "10042",
		"--to", dest, "--output=json")
	if res.Exit != 0 {
		t.Fatalf("attachment download --to: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	got, err := os.ReadFile(filepath.Join(dest, "report.pdf"))
	if err != nil {
		t.Fatalf("the file did not land in --to's directory: %v", err)
	}
	if string(got) != mx3AttachmentBytes {
		t.Fatalf("downloaded bytes = %q, want %q", got, mx3AttachmentBytes)
	}
	Prove(t, "jira issue attachment download", "to",
		"the stub's bytes landed at <--to dir>/report.pdf, the server-resolved name inside the caller's named directory")
}

// --as picks the local name — and a caller-supplied name carrying a path
// separator is REFUSED rather than reduced, because the caller typed it.
func TestAttachmentDownloadAsPicksNameAndRefusesPaths(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3DownloadHandler(rec))
	home := t.TempDir()
	dest := filepath.Join(home, "out")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}
	res := mx3Run(t, home, baseURL, "issue", "attachment", "download", "10042",
		"--to", dest, "--as", "picked.bin", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("attachment download --as: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	if _, err := os.Stat(filepath.Join(dest, "picked.bin")); err != nil {
		t.Fatalf("the file did not land under --as's name: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "report.pdf")); err == nil {
		t.Fatalf("the server's own name was written despite --as")
	}

	refusedRec := &mx3Recorder{}
	refusedURL := mx3Stub(t, mx3RecordOnly(refusedRec))
	refused := mx3Run(t, home, refusedURL, "issue", "attachment", "download", "10042",
		"--to", dest, "--as", "sub/evil.bin", "--output=json")
	mx3RequireRefusal(t, refused, "flag_value_invalid", "as")
	mx3RequireNoRequests(t, refusedRec)

	Prove(t, "jira issue attachment download", "as",
		"the bytes landed under picked.bin instead of the server's report.pdf, and --as sub/evil.bin was refused by code flag_value_invalid before any request")
}

// --overwrite is the explicit opt-in to replacing an existing file:
// without it the collision fails and the old bytes survive; with it the
// file is replaced and the envelope says so.
func TestAttachmentDownloadOverwriteGatesReplacement(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3DownloadHandler(rec))
	home := t.TempDir()
	dest := filepath.Join(home, "out")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}
	target := filepath.Join(dest, "report.pdf")
	if err := os.WriteFile(target, []byte("old bytes"), 0o600); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}

	collided := mx3Run(t, home, baseURL, "issue", "attachment", "download", "10042",
		"--to", dest, "--output=json")
	if collided.Exit == 0 {
		t.Fatalf("download over an existing file succeeded without --overwrite\nstdout: %s", collided.Stdout)
	}
	mx3RequireRefusal(t, collided, "local_io_failed", "")
	if got, err := os.ReadFile(target); err != nil || string(got) != "old bytes" {
		t.Fatalf("existing file after refused download = %q, %v; want the old bytes untouched", got, err)
	}

	replaced := mx3Run(t, home, baseURL, "issue", "attachment", "download", "10042",
		"--to", dest, "--overwrite", "--output=json")
	if replaced.Exit != 0 {
		t.Fatalf("attachment download --overwrite: exit %d\nstderr: %s", replaced.Exit, replaced.Stderr)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != mx3AttachmentBytes {
		t.Fatalf("file after --overwrite = %q, %v; want the stub's bytes", got, err)
	}
	if data := mx3Data(t, replaced); data["overwrote"] != true {
		t.Fatalf("data.overwrote = %v, want true", data["overwrote"])
	}
	Prove(t, "jira issue attachment download", "overwrite",
		"without it the collision failed by code local_io_failed and the old bytes survived; with it the file held the stub's bytes and data.overwrote=true")
}

// A bare --dry-run reads nothing and writes nothing: zero requests, an
// unresolved filename, and an untouched directory.
func TestAttachmentDownloadDryRunWritesNothing(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3RecordOnly(rec))
	home := t.TempDir()
	dest := filepath.Join(home, "out")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}
	res := mx3Run(t, home, baseURL, "issue", "attachment", "download", "10042",
		"--to", dest, "--dry-run", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("attachment download --dry-run: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	mx3RequireNoRequests(t, rec)
	data := mx3Data(t, res)
	if data["dry_run"] != true || data["filename_resolved"] != false {
		t.Fatalf("data = %v, want dry_run=true and filename_resolved=false", data)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("the preview wrote %d files into --to's directory", len(entries))
	}
	Prove(t, "jira issue attachment download", "dry-run",
		"stub saw zero requests, the directory stayed empty, and the envelope admitted filename_resolved=false")
}

// --validate-remote lets the preview read the METADATA — never the
// content — and report the exact path, still writing nothing.
func TestAttachmentDownloadValidateRemoteResolvesName(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3DownloadHandler(rec))
	home := t.TempDir()
	dest := filepath.Join(home, "out")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}
	res := mx3Run(t, home, baseURL, "issue", "attachment", "download", "10042",
		"--to", dest, "--dry-run", "--validate-remote", "--output=json")
	if res.Exit != 0 {
		t.Fatalf("download --dry-run --validate-remote: exit %d\nstderr: %s", res.Exit, res.Stderr)
	}
	if got := rec.byPath(mx3AttachmentMetaPath); len(got) != 1 {
		t.Fatalf("metadata reads = %d, want 1", len(got))
	}
	if got := rec.byPath(mx3AttachmentContentPath); len(got) != 0 {
		t.Fatalf("content reads = %d, want 0 — the remote preview must not stream the bytes", len(got))
	}
	data := mx3Data(t, res)
	if data["filename_resolved"] != true || data["filename"] != "report.pdf" {
		t.Fatalf("data = %v, want filename_resolved=true with filename report.pdf", data)
	}
	if entries, err := os.ReadDir(dest); err != nil || len(entries) != 0 {
		t.Fatalf("the remote preview wrote files (%v, %v)", entries, err)
	}
	Prove(t, "jira issue attachment download", "validate-remote",
		"the preview made exactly one metadata GET and zero content GETs, published filename_resolved=true with the server's report.pdf, and wrote nothing")
}

func TestAttachmentDownloadByteLimit(t *testing.T) {
	rec := &mx3Recorder{}
	baseURL := mx3Stub(t, mx3DownloadHandler(rec))
	home := t.TempDir()
	dest := filepath.Join(home, "out")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	args := []string{"issue", "attachment", "download", "10042", "--to", dest, "--output=json"}
	refused := mx3Run(t, home, baseURL, append(args, "--max-download-bytes=1")...)
	mx3RequireRefusal(t, refused, "attachment_download_limit_exceeded", "")
	entries, err := os.ReadDir(dest)
	if err != nil || len(entries) != 0 {
		t.Fatal("cap retained partial download", err)
	}
	accepted := mx3Run(t, home, baseURL, append(args, fmt.Sprintf("--max-download-bytes=%d", len(mx3AttachmentBytes)))...)
	got, err := os.ReadFile(filepath.Join(dest, "report.pdf"))
	if accepted.Exit != 0 || err != nil || string(got) != mx3AttachmentBytes {
		t.Fatalf("exact limit rejected: exit=%d err=%v", accepted.Exit, err)
	}
	Prove(t, "jira issue attachment download", "max-download-bytes", "a one-byte cap refused the stream without a partial file; the exact byte count saved all original bytes")
}
