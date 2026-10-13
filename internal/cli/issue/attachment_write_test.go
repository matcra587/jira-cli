package issue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/cli/command"
	"github.com/matcra587/jira-cli/internal/core"
	"github.com/matcra587/jira-cli/internal/envelope"
	"github.com/matcra587/jira-cli/internal/jira"
)

// The keyed mechanics are pinned once against `issue link list` and the
// keyed write semantics once against `issue comment add`. The path
// sandbox is pinned in attachment_path_test.go, which is where the
// hostile names live. What is pinned HERE is what these three commands
// decide:
//
//   - the positional grammar, which --file decides rather than a
//     heuristic;
//   - that upload previews inspect metadata without contents or a client;
//   - that a download resolves its filename from the site and reports
//     honestly when it has not;
//   - that a failed download leaves no partial file behind.

// fakeAttachmentClient is a core.JiraClient double for the whole
// attachment write group.
type fakeAttachmentClient struct {
	// Every seam this fake does not serve fails by name.
	unusedJiraClient
	// content is the body GetAttachmentContent streams; filename is what
	// GetAttachment reports. failures maps a key or id to an error.
	content  string
	filename string
	failures map[string]error
	// contentErr fails the copy mid-stream, which is how the
	// partial-file cleanup is exercised.
	contentErr error

	mu          sync.Mutex
	uploads     []attachmentUpload
	deleted     []string
	metaGets    int
	contentGets int
}

type attachmentUpload struct {
	key   string
	paths []string
}

func (f *fakeAttachmentClient) AddAttachment(_ context.Context, key string, paths []string) (core.AttachmentWrite, error) {
	f.mu.Lock()
	f.uploads = append(f.uploads, attachmentUpload{key: key, paths: paths})
	f.mu.Unlock()
	if err, ok := f.failures[key]; ok {
		return core.AttachmentWrite{}, err
	}
	created := make([]jira.Attachment, 0, len(paths))
	for i, p := range paths {
		created = append(created, jira.Attachment{
			ID:       jira.NullableOf(string(rune('a'+i)) + "-" + key),
			Filename: jira.NullableOf(filepath.Base(p)),
			Size:     jira.NullableOf(int64(11)),
		})
	}
	return core.AttachmentWrite{Attachments: created}, nil
}

func (f *fakeAttachmentClient) GetAttachment(_ context.Context, id string) (core.AttachmentRead, error) {
	f.mu.Lock()
	f.metaGets++
	f.mu.Unlock()
	if err, ok := f.failures[id]; ok {
		return core.AttachmentRead{}, err
	}
	return core.AttachmentRead{Attachment: &jira.AttachmentMetadata{
		Filename: jira.NullableOf(f.filename),
		Size:     jira.NullableOf(int64(len(f.content))),
	}}, nil
}

func (f *fakeAttachmentClient) GetAttachmentContent(_ context.Context, id string) (core.AttachmentContent, error) {
	f.mu.Lock()
	f.contentGets++
	f.mu.Unlock()
	if err, ok := f.failures["content:"+id]; ok {
		return core.AttachmentContent{}, err
	}
	var body io.Reader = strings.NewReader(f.content)
	if f.contentErr != nil {
		// A reader that yields some bytes and then fails, which is the
		// only way to reach the partial-file cleanup.
		body = io.MultiReader(strings.NewReader(f.content), errReader{f.contentErr})
	}
	return core.AttachmentContent{Body: io.NopCloser(body), Size: int64(len(f.content))}, nil
}

func (f *fakeAttachmentClient) DeleteAttachment(_ context.Context, id string) (core.AttachmentDeleteWrite, error) {
	f.mu.Lock()
	f.deleted = append(f.deleted, id)
	f.mu.Unlock()
	if err, ok := f.failures[id]; ok {
		return core.AttachmentDeleteWrite{}, err
	}
	return core.AttachmentDeleteWrite{StatusCode: http.StatusNoContent}, nil
}

// errReader fails on first read, for the mid-stream failure above.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func attachmentDeps(fake *fakeAttachmentClient) Deps {
	return Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) { return fake, nil }}
}

// --- attachment add ---

// TestSplitKeysAndFilesIsDecidedByTheFlag is this command's own grammar
// decision. The two forms have to mean different things, and neither may
// depend on guessing whether a word looks like a key or exists on disk.
func TestSplitKeysAndFilesIsDecidedByTheFlag(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		fileFlags []string
		wantKeys  []string
		wantPaths []string
		wantErr   bool
	}{
		{
			name: "no flag: first positional is the key, rest are paths",
			args: []string{"KEY-123", "a.log", "b.log"},
			// The ergonomic single-issue form.
			wantKeys: []string{"KEY-123"}, wantPaths: []string{"a.log", "b.log"},
		},
		{
			name: "with flag: every positional is a key",
			args: []string{"KEY-123", "KEY-124"}, fileFlags: []string{"a.log"},
			wantKeys: []string{"KEY-123", "KEY-124"}, wantPaths: []string{"a.log"},
		},
		{
			name: "with flag and several files",
			args: []string{"KEY-123"}, fileFlags: []string{"a.log", "b.log"},
			wantKeys: []string{"KEY-123"}, wantPaths: []string{"a.log", "b.log"},
		},
		{
			// A key that happens to look like a path is still a key when
			// --file is given, which is the property the flag buys.
			name: "a path-looking positional is a key under the flag",
			args: []string{"KEY-123", "b.log"}, fileFlags: []string{"a.log"},
			wantKeys: []string{"KEY-123", "b.log"}, wantPaths: []string{"a.log"},
		},
		{name: "one positional and no file", args: []string{"KEY-123"}, wantErr: true},
		{name: "flag but no positionals", fileFlags: []string{"a.log"}, wantErr: true},
		{name: "nothing at all", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys, paths, err := splitKeysAndFiles(tt.args, tt.fileFlags)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("splitKeysAndFiles(%v, %v) = (%v, %v), want a refusal", tt.args, tt.fileFlags, keys, paths)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitKeysAndFiles: %v", err)
			}
			if strings.Join(keys, ",") != strings.Join(tt.wantKeys, ",") {
				t.Errorf("keys = %v, want %v", keys, tt.wantKeys)
			}
			if strings.Join(paths, ",") != strings.Join(tt.wantPaths, ",") {
				t.Errorf("paths = %v, want %v", paths, tt.wantPaths)
			}
		})
	}
}

// TestAttachmentAddPreviewChecksMetadataWithoutClient preserves local input
// facts without claiming created resources or constructing a Jira client.
func TestAttachmentAddPreviewChecksMetadataWithoutClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	asked := false
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a dry run must not build a client")
	}}
	out, err := runAttachmentAdd(t.Context(), deps, invocation(true, "KEY-123", path), nil, 4)
	if err != nil {
		t.Fatalf("runAttachmentAdd: %v", err)
	}
	if asked || !out.DryRun {
		t.Fatal("preview built a client or lost its preview state")
	}
	if len(out.Files) != 1 || out.Files[0] != path || len(out.FileDetails) != 1 || out.FileDetails[0].Path != path || out.FileDetails[0].Size != 0 || out.FileDetails[0].MIMEInferred != "text/plain" {
		t.Fatalf("preview lost authored metadata: %+v", out)
	}
	if len(out.Results) != 1 || !out.Results[0].OK || out.Results[0].Data == nil || len(out.Results[0].Data.Attachments) != 0 {
		t.Fatalf("preview invented created resources or lost its target: %+v", out.Results)
	}
}

// TestAttachmentAddSendsEveryFileToEveryKey pins the fan-out shape: one
// set of paths, every key.
func TestAttachmentAddSendsEveryFileToEveryKey(t *testing.T) {
	fake := &fakeAttachmentClient{}
	paths := []string{filepath.Join(t.TempDir(), "a.log"), filepath.Join(t.TempDir(), "b.log")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("authored"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runAttachmentAdd(t.Context(), attachmentDeps(fake), invocation(false, "KEY-1", "KEY-2"), paths, 4)
	if err != nil {
		t.Fatalf("runAttachmentAdd: %v", err)
	}
	if len(fake.uploads) != 2 {
		t.Fatalf("uploaded %d times, want one per key", len(fake.uploads))
	}
	for _, u := range fake.uploads {
		if strings.Join(u.paths, ",") != strings.Join(paths, ",") {
			t.Errorf("%s got paths %v, want both files", u.key, u.paths)
		}
	}
	for i, r := range out.Results {
		if !r.OK {
			t.Fatalf("results[%d] failed", i)
		}
		if len(r.Data.Attachments) != 2 {
			t.Errorf("results[%d] carries %d attachments, want one per file — the ids are why", i, len(r.Data.Attachments))
		}
	}
}

// TestAttachmentAddPartialFailureIsAPartialResult: the keyed WRITE rule,
// and it matters most here — Jira de-duplicates nothing, so re-running a
// key that succeeded attaches a second copy under the same name.
func TestAttachmentAddPartialFailureIsAPartialResult(t *testing.T) {
	keys := []string{"KEY-1", "KEY-404", "KEY-3"}
	fake := &fakeAttachmentClient{failures: map[string]error{
		"KEY-404": &adapterjira.APIError{StatusCode: http.StatusNotFound, Messages: []string{"Issue does not exist"}},
	}}

	path := filepath.Join(t.TempDir(), "a.log")
	if err := os.WriteFile(path, []byte("authored"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runAttachmentAdd(t.Context(), attachmentDeps(fake), invocation(false, keys...), []string{path}, 4)
	if err != nil {
		t.Fatalf("a partly-successful upload must not fail the command: %v", err)
	}
	for i, want := range keys {
		if out.Results[i].Issue.Key != want {
			t.Fatalf("results[%d].issue.key = %q, want %q (argv order)", i, out.Results[i].Issue.Key, want)
		}
	}
	if bad := out.Results[1]; bad.OK || bad.Err == nil || bad.Err.Code != "jira_not_found" {
		t.Errorf("failed key = %+v, want a jira_not_found entry carrying no data", bad)
	}
}

type lateAttachmentReceiptClient struct{ unusedJiraClient }

func (lateAttachmentReceiptClient) AddAttachment(ctx context.Context, _ string, _ []string) (core.AttachmentWrite, error) {
	<-ctx.Done()
	return core.AttachmentWrite{Attachments: []jira.Attachment{{ID: jira.NullableOf("10042"), Filename: jira.NullableOf("authored.txt"), Size: jira.NullableOf(int64(8))}}}, nil
}

func TestAttachmentFanOutDeadlineRetainsLateJoinedReceipt(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	results, err := attachmentAddFanOut(ctx, lateAttachmentReceiptClient{}, []string{"KEY-1"}, []string{"authored.txt"}, 1)
	if !errors.Is(err, context.DeadlineExceeded) || len(results) != 1 || results[0].Issue.Key != "KEY-1" || !results[0].OK || results[0].Data == nil || len(results[0].Data.Attachments) != 1 || results[0].Data.Attachments[0].ID != "10042" {
		t.Fatalf("deadline discarded a late joined upload receipt: err=%v results=%+v", err, results)
	}
}

func TestAttachmentTotalFailureRetainsOriginalTransportError(t *testing.T) {
	t.Parallel()
	cause := &adapterjira.APIError{StatusCode: http.StatusTooManyRequests, RetryAfterSeconds: 7, Retry: core.RetryInfo{Attempts: 1, Waited: time.Millisecond}}
	client := &fakeAttachmentClient{failures: map[string]error{"KEY-1": cause}}
	results, err := attachmentAddFanOut(t.Context(), client, []string{"KEY-1"}, []string{"authored.txt"}, 1)
	original, ok := errors.AsType[*adapterjira.APIError](err)
	if !ok || original != cause || len(results) != 1 || results[0].OK || results[0].Err == nil || results[0].Err.Code != "jira_rate_limited" {
		t.Fatal("retaining a failed upload rebuilt or discarded its original transport error")
	}
	mapped := cli.MapError(err)
	if mapped.Exit != cli.ExitRateLimit || mapped.RetryAfterSeconds != 7 || mapped.Attempts != 1 || mapped.Waited != time.Millisecond {
		t.Fatal("retained upload changed rate-limit metadata")
	}
}

// --- attachment download ---

func downloadOpts(dir string) downloadOptions {
	return downloadOptions{to: dir}
}

// TestDownloadBarePreviewResolvesNothing: the filename lives on the
// site, and a bare preview must not contact it — so it reports the
// directory, says the filename is unresolved, and writes nothing.
func TestDownloadBarePreviewResolvesNothing(t *testing.T) {
	dir := t.TempDir()
	asked := false
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		asked = true
		return nil, errors.New("a bare dry run must not build a client")
	}}

	out, err := runAttachmentDownload(t.Context(), deps, invocation(true, "10042"), downloadOpts(dir))
	if err != nil {
		t.Fatalf("runAttachmentDownload: %v", err)
	}
	if asked {
		t.Error("the preview asked for a Jira client")
	}
	if out.FilenameResolved || out.Filename != "" || out.Path != "" {
		t.Errorf("preview resolved a filename it never looked up: %+v", out)
	}
	if out.Directory != dir {
		t.Errorf("directory = %q, want the resolved --to %q", out.Directory, dir)
	}
	assertEmptyDir(t, dir)
}

// TestDownloadRemotePreviewResolvesButWritesNothing pins the
// --validate-remote split: it answers the question only the site can
// answer, and still writes nothing.
func TestDownloadRemotePreviewResolvesButWritesNothing(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeAttachmentClient{filename: "report.pdf", content: "hello world"}
	opts := downloadOpts(dir)
	opts.validateRemote = true

	out, err := runAttachmentDownload(t.Context(), attachmentDeps(fake), invocation(true, "10042"), opts)
	if err != nil {
		t.Fatalf("runAttachmentDownload: %v", err)
	}
	if !out.FilenameResolved || out.Filename != "report.pdf" {
		t.Errorf("filename = %q resolved=%v, want the metadata's own name", out.Filename, out.FilenameResolved)
	}
	if out.Path != filepath.Join(dir, "report.pdf") {
		t.Errorf("path = %q, want the joined destination", out.Path)
	}
	if out.BytesWritten != nil {
		t.Errorf("bytes_written = %v on a preview", out.BytesWritten)
	}
	assertEmptyDir(t, dir)
}

// TestDownloadWithAsNeedsNoLookup: the caller supplied the name, so even
// a bare preview knows the exact path — resolving it locally is what
// makes `--as x --dry-run` report a real answer instead of a warning
// about a lookup it does not need.
func TestDownloadWithAsNeedsNoLookup(t *testing.T) {
	dir := t.TempDir()
	asked := false
	deps := Deps{JiraClient: func(ctx context.Context) (core.JiraClient, error) {
		asked = true
		return nil, errors.New("--as needs no site read")
	}}
	opts := downloadOpts(dir)
	opts.as = "mine.pdf"

	out, err := runAttachmentDownload(t.Context(), deps, invocation(true, "10042"), opts)
	if err != nil {
		t.Fatalf("runAttachmentDownload: %v", err)
	}
	if asked {
		t.Error("a preview with --as still contacted the site")
	}
	if !out.FilenameResolved || out.Filename != "mine.pdf" {
		t.Errorf("filename = %q resolved=%v, want the caller's own name", out.Filename, out.FilenameResolved)
	}
}

// TestDownloadWritesTheFile is the happy path, and it checks the bytes
// rather than only the envelope: a command that reported a path without
// writing to it would pass a weaker test.
func TestDownloadWritesTheFile(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeAttachmentClient{filename: "report.pdf", content: "hello world"}

	out, err := runAttachmentDownload(t.Context(), attachmentDeps(fake), invocation(false, "10042"), downloadOpts(dir))
	if err != nil {
		t.Fatalf("runAttachmentDownload: %v", err)
	}
	if out.BytesWritten == nil || *out.BytesWritten != int64(len("hello world")) {
		t.Errorf("bytes_written = %v, want %d", out.BytesWritten, len("hello world"))
	}
	if out.Overwrote == nil || *out.Overwrote {
		t.Errorf("overwrote = %v, want false — nothing was there", out.Overwrote)
	}
	got, err := os.ReadFile(filepath.Join(dir, "report.pdf"))
	if err != nil {
		t.Fatalf("read what was written: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("wrote %q, want the streamed content", got)
	}
}

// TestDownloadRefusesAHostileServerName is the end-to-end version of the
// sandbox test: a hostile name reaching the COMMAND must stop it, and
// must not leave anything behind.
func TestDownloadRefusesAHostileServerName(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeAttachmentClient{filename: "..", content: "payload"}

	_, err := runAttachmentDownload(t.Context(), attachmentDeps(fake), invocation(false, "10042"), downloadOpts(dir))
	if err == nil {
		t.Fatal("a server filename that does not name a file must stop the download")
	}
	if code := cli.MapError(err).Code; code != cli.CodePathInvalid {
		t.Errorf("code = %q, want %q", code, cli.CodePathInvalid)
	}
	assertEmptyDir(t, dir)
}

// TestDownloadReducesATraversalName: a name the CLI CAN reduce is
// written under the reduced name, inside the destination — not refused,
// because failing a download over a filename the caller had no say in
// would be worse, and not written outside, which is the whole point.
func TestDownloadReducesATraversalName(t *testing.T) {
	outside := t.TempDir()
	dir := filepath.Join(outside, "downloads")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	fake := &fakeAttachmentClient{filename: "../../.ssh/authorized_keys", content: "payload"}

	out, err := runAttachmentDownload(t.Context(), attachmentDeps(fake), invocation(false, "10042"), downloadOpts(dir))
	if err != nil {
		t.Fatalf("runAttachmentDownload: %v", err)
	}
	if out.Filename != "authorized_keys" {
		t.Errorf("filename = %q, want the reduced base name", out.Filename)
	}
	if filepath.Dir(out.Path) != dir {
		t.Errorf("wrote to %q, outside the destination %q", out.Path, dir)
	}
	entries, _ := os.ReadDir(outside)
	for _, e := range entries {
		if e.Name() != "downloads" {
			t.Errorf("%q escaped the destination directory", e.Name())
		}
	}
}

// TestDownloadRefusesToClobberAndOverwrites pins both halves of the
// collision rule in one test, because they are one decision.
func TestDownloadRefusesToClobberAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(existing, []byte("mine"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fake := &fakeAttachmentClient{filename: "report.pdf", content: "theirs"}

	_, err := runAttachmentDownload(t.Context(), attachmentDeps(fake), invocation(false, "10042"), downloadOpts(dir))
	if err == nil {
		t.Fatal("an existing file must not be replaced without --overwrite")
	}
	if got, _ := os.ReadFile(existing); string(got) != "mine" {
		t.Errorf("the existing file was modified: %q", got)
	}

	opts := downloadOpts(dir)
	opts.overwrite = true
	out, err := runAttachmentDownload(t.Context(), attachmentDeps(fake), invocation(false, "10042"), opts)
	if err != nil {
		t.Fatalf("--overwrite must replace it: %v", err)
	}
	if out.Overwrote == nil || !*out.Overwrote {
		t.Errorf("overwrote = %v, want true — a file was replaced", out.Overwrote)
	}
	if got, _ := os.ReadFile(existing); string(got) != "theirs" {
		t.Errorf("file = %q, want the downloaded content", got)
	}
}

// TestDownloadRemovesAPartialFile: a truncated download left on disk
// under the right name is worse than no file, because the next reader
// cannot tell it apart from a complete one.
func TestDownloadRemovesAPartialFile(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeAttachmentClient{
		filename:   "report.pdf",
		content:    "partial",
		contentErr: errors.New("connection reset"),
	}

	_, err := runAttachmentDownload(t.Context(), attachmentDeps(fake), invocation(false, "10042"), downloadOpts(dir))
	if err == nil {
		t.Fatal("a failed copy must fail the command")
	}
	assertEmptyDir(t, dir)
}

func TestDownloadFailurePreservesExistingOverwriteTarget(t *testing.T) {
	for _, failure := range []string{"request", "stream"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "report.pdf")
			if err := os.WriteFile(path, []byte("authored contents"), 0o640); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			fake := &fakeAttachmentClient{filename: "report.pdf", content: "incomplete"}
			if failure == "request" {
				fake.failures = map[string]error{"content:10042": errors.New("content unavailable")}
			} else {
				fake.contentErr = errors.New("connection reset")
			}
			opts := downloadOpts(dir)
			opts.overwrite = true
			if _, err := runAttachmentDownload(t.Context(), attachmentDeps(fake), invocation(false, "10042"), opts); err == nil {
				t.Fatal("failed download reported success")
			}
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != "authored contents" {
				t.Fatalf("failed overwrite destroyed the existing file: %v", err)
			}
			after, err := os.Stat(path)
			if err != nil || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) {
				t.Fatal("failed overwrite changed existing metadata")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 || entries[0].Name() != "report.pdf" {
				t.Fatal("failed download left staging files")
			}
		})
	}
}

func TestDownloadStreamCancellationPreservesCauseAndExistingTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(path, []byte("authored contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeAttachmentClient{filename: "report.pdf", content: "incomplete", contentErr: context.Canceled}
	opts := downloadOpts(dir)
	opts.overwrite = true
	_, err := runAttachmentDownload(t.Context(), attachmentDeps(fake), invocation(false, "10042"), opts)
	if !errors.Is(err, context.Canceled) || cli.MapError(err).Code != cli.CodeCanceled {
		t.Fatalf("stream cancellation was classified as disk failure: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "authored contents" {
		t.Fatal("canceled overwrite changed the original file")
	}
}

type downloadEOFHookReader struct {
	io.Reader
	hook func()
	err  error
}

func (r downloadEOFHookReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		r.hook()
		if r.err != nil {
			return n, r.err
		}
	}
	return n, err
}

type downloadContentHookClient struct {
	core.JiraClient
	body io.ReadCloser
}

func (c downloadContentHookClient) GetAttachmentContent(context.Context, string) (core.AttachmentContent, error) {
	return core.AttachmentContent{Body: c.body}, nil
}

func TestDownloadCancellationBeforePublicationPreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.bin")
	if err := os.WriteFile(path, []byte("original contents"), 0o640); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := downloadContentHookClient{body: io.NopCloser(downloadEOFHookReader{Reader: strings.NewReader("complete download"), hook: cancel})}
	_, _, err := writeAttachment(ctx, client, "10042", dir, "report.bin", true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled complete stream was published: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original contents" {
		t.Fatal("cancellation before publication replaced the original")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "report.bin" {
		t.Fatal("cancellation left staging files")
	}
}

func TestDownloadOverwriteSymlinkStaysInsideDestination(t *testing.T) {
	for _, outside := range []bool{false, true} {
		t.Run(fmt.Sprint(outside), func(t *testing.T) {
			dir := t.TempDir()
			targetDir := dir
			if outside {
				targetDir = t.TempDir()
			}
			target := filepath.Join(targetDir, "authored.bin")
			if err := os.WriteFile(target, []byte("authored contents"), 0o640); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "report.bin")
			linkTarget := target
			if !outside {
				linkTarget = filepath.Base(target)
			}
			if err := os.Symlink(linkTarget, path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			fake := &fakeAttachmentClient{content: "download contents"}
			_, replaced, err := writeAttachment(t.Context(), fake, "10042", dir, "report.bin", true)
			if outside {
				if err == nil || fake.contentGets != 0 {
					t.Fatal("overwrite followed an outside link or requested content before refusal")
				}
			} else {
				info, statErr := os.Lstat(path)
				data, readErr := os.ReadFile(path)
				if err != nil || !replaced || statErr != nil || !info.Mode().IsRegular() || readErr != nil || string(data) != "download contents" {
					t.Fatal("complete overwrite did not replace the inside link's entry")
				}
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "authored contents" {
				t.Fatal("overwrite changed the link's target")
			}
		})
	}
}

func TestDownloadFailureCleanupRetainsOriginalDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "downloads")
	held := filepath.Join(parent, "original-directory")
	replacement := filepath.Join(parent, "replacement")
	for _, path := range []string{dir, replacement} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	foreign := filepath.Join(replacement, "report.bin")
	if err := os.WriteFile(foreign, []byte("another writer's contents"), 0o640); err != nil {
		t.Fatal(err)
	}
	// Swapping the directory is deterministic fault injection, not a timing race.
	hook := func() {
		if err := os.Rename(dir, held); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(replacement, dir); err != nil {
			t.Fatal(err)
		}
	}
	client := downloadContentHookClient{body: io.NopCloser(downloadEOFHookReader{Reader: strings.NewReader("partial bytes"), hook: hook, err: errors.New("stream reset")})}
	if _, _, err := writeAttachment(t.Context(), client, "10042", dir, "report.bin", false); err == nil {
		t.Fatal("failed stream reported success")
	}
	data, err := os.ReadFile(foreign)
	if err != nil || string(data) != "another writer's contents" {
		t.Fatal("failed download removed a file in a replaced directory")
	}
	if _, err := os.Lstat(filepath.Join(held, "report.bin")); !os.IsNotExist(err) {
		t.Fatal("failed download left its original partial file")
	}
}

// --- attachment delete ---

// TestAttachmentDeleteUsesTheSharedConfirmation pins that the second
// destructive command behaves exactly like the first: refuses without a
// terminal, names --force, and deletes nothing.
func TestAttachmentDeleteUsesTheSharedConfirmation(t *testing.T) {
	fake := &fakeAttachmentClient{}
	confirm := &recordingConfirm{answer: true}
	deps := attachmentDeps(fake)
	deps.Confirm = confirm.ask

	_, err := runAttachmentDelete(t.Context(), deps, invocation(false, "10042"), false)
	if err == nil {
		t.Fatal("a non-interactive delete without --force must be refused")
	}
	var e *cli.InputError
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not a *cli.InputError", err)
	}
	if e.Code != cli.CodeFlagRequired || e.Field != command.FlagForce {
		t.Errorf("code/field = %q/%q, want %q/%q", e.Code, e.Field, cli.CodeFlagRequired, command.FlagForce)
	}
	if confirm.asked != 0 {
		t.Error("a non-interactive run drew a dialog")
	}
	if len(fake.deleted) != 0 {
		t.Errorf("the refused run deleted %v", fake.deleted)
	}
}

func TestAttachmentDeleteAsksAtATerminal(t *testing.T) {
	fake := &fakeAttachmentClient{}
	confirm := &recordingConfirm{answer: true}
	deps := attachmentDeps(fake)
	deps.Confirm = confirm.ask

	if _, err := runAttachmentDelete(t.Context(), deps, interactiveInvocation(false, "10042"), false); err != nil {
		t.Fatalf("runAttachmentDelete: %v", err)
	}
	if confirm.asked != 1 {
		t.Fatalf("dialog drawn %d times, want once", confirm.asked)
	}
	if !strings.Contains(confirm.last.Question, "10042") {
		t.Errorf("question %q does not name the attachment", confirm.last.Question)
	}
	if !strings.Contains(confirm.last.Caveat, "Download it first") {
		t.Errorf("caveat %q does not mention the only undo there is", confirm.last.Caveat)
	}
	if len(fake.deleted) != 1 || fake.deleted[0] != "10042" {
		t.Errorf("deleted %v, want [10042]", fake.deleted)
	}
}

func TestAttachmentDeleteAbortsOnNo(t *testing.T) {
	fake := &fakeAttachmentClient{}
	confirm := &recordingConfirm{answer: false}
	deps := attachmentDeps(fake)
	deps.Confirm = confirm.ask

	_, err := runAttachmentDelete(t.Context(), deps, interactiveInvocation(false, "10042"), false)
	if err == nil {
		t.Fatal("answering no must not report success")
	}
	if code := cli.MapError(err).Code; code != cli.CodeCanceled {
		t.Errorf("code = %q, want %q", code, cli.CodeCanceled)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("the declined run deleted %v", fake.deleted)
	}
}

func TestAttachmentDeleteForceSkipsThePrompt(t *testing.T) {
	fake := &fakeAttachmentClient{}
	confirm := &recordingConfirm{answer: false}
	deps := attachmentDeps(fake)
	deps.Confirm = confirm.ask

	if _, err := runAttachmentDelete(t.Context(), deps, invocation(false, "10042"), true); err != nil {
		t.Fatalf("runAttachmentDelete: %v", err)
	}
	if confirm.asked != 0 {
		t.Error("--force still drew the dialog")
	}
	if len(fake.deleted) != 1 {
		t.Errorf("deleted %v, want the attachment gone", fake.deleted)
	}
}

func TestAttachmentDeletePreviewNeitherAsksNorDeletes(t *testing.T) {
	fake := &fakeAttachmentClient{}
	confirm := &recordingConfirm{answer: false}
	deps := attachmentDeps(fake)
	deps.Confirm = confirm.ask

	out, err := runAttachmentDelete(t.Context(), deps, interactiveInvocation(true, "10042"), false)
	if err != nil {
		t.Fatalf("runAttachmentDelete: %v", err)
	}
	if confirm.asked != 0 {
		t.Error("the preview asked for confirmation; it destroys nothing")
	}
	if !out.DryRun || out.AttachmentID != "10042" {
		t.Errorf("preview = %+v", out)
	}
	if len(fake.deleted) != 0 {
		t.Errorf("the preview deleted %v", fake.deleted)
	}
}

// --- metadata and registration ---

// TestAttachmentWriteMetadata covers all three at once, and the three
// differ in every flag that matters: add writes Jira and is keyed;
// download writes only the LOCAL filesystem and is not; delete writes
// Jira and names no issue.
func TestAttachmentWriteMetadata(t *testing.T) {
	tests := []struct {
		op       string
		cmd      *cobra.Command
		class    command.Class
		keyed    bool
		identity bool
	}{
		{"issue.attachment.add", newAttachmentAdd(Deps{}), command.ClassMutation, true, true},
		{"issue.attachment.download", newAttachmentDownload(Deps{}), command.ClassLocalState, false, false},
		{"issue.attachment.delete", newAttachmentDelete(Deps{}), command.ClassMutation, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.op, func(t *testing.T) {
			metas, ok := command.MetaFor(tt.cmd)
			if !ok || len(metas) == 0 {
				t.Fatalf("no metadata recorded for %s", tt.op)
			}
			m := metas[0]
			if m.Op != tt.op {
				t.Errorf("op = %q, want %q", m.Op, tt.op)
			}
			if m.Class() != tt.class {
				t.Errorf("class = %v, want %v", m.Class(), tt.class)
			}
			if m.Keyed != tt.keyed {
				t.Errorf("Keyed = %v, want %v", m.Keyed, tt.keyed)
			}
			if m.Identity != tt.identity {
				t.Errorf("Identity = %v, want %v", m.Identity, tt.identity)
			}
			if tt.cmd.Flags().Lookup(cli.FlagDryRun) == nil {
				t.Error("--dry-run is not registered; every mutation carries it")
			}
		})
	}
}

// TestDownloadIsNotAJiraWrite is the classification worth pinning on its
// own: this command sends nothing to Jira, so a WritesJira declaration
// would put it in the read-only mode's blocking path for a request it
// never makes.
func TestDownloadIsNotAJiraWrite(t *testing.T) {
	metas, _ := command.MetaFor(newAttachmentDownload(Deps{}))
	if metas[0].WritesJira {
		t.Error("WritesJira is true; this command only ever GETs — the mutation is to the local filesystem")
	}
	if !metas[0].MutatesLocalState {
		t.Error("MutatesLocalState is false; this command writes a file, which is what earns it a --dry-run")
	}
}

func TestAttachmentWriteFlags(t *testing.T) {
	add := newAttachmentAdd(Deps{}).Flags()
	if add.Lookup(flagFile) == nil || add.Lookup(command.FlagParallelism) == nil {
		t.Error("attachment add is missing --file or --parallelism")
	}
	if add.Lookup(command.FlagForce) != nil {
		t.Error("attachment add registers --force; uploading destroys nothing")
	}

	down := newAttachmentDownload(Deps{}).Flags()
	for _, want := range []string{flagTo, flagAs, flagOverwrite, command.FlagValidateRemote} {
		if down.Lookup(want) == nil {
			t.Errorf("attachment download is missing --%s", want)
		}
	}
	if down.Lookup(command.FlagForce) != nil {
		t.Error("attachment download registers --force; the collision case has its own flag")
	}

	del := newAttachmentDelete(Deps{}).Flags()
	if del.Lookup(command.FlagForce) == nil {
		t.Error("attachment delete is missing --force")
	}
	if alias := del.Lookup(command.FlagYes); alias == nil || !alias.Hidden {
		t.Error("attachment delete is missing the hidden --yes alias")
	}
}

func TestAttachmentWriteRegistrations(t *testing.T) {
	for op, want := range map[string]string{
		"issue.attachment.add":      "envelope.IssueAttachmentAddOutput",
		"issue.attachment.download": "envelope.IssueAttachmentDownloadOutput",
		"issue.attachment.delete":   "envelope.IssueAttachmentDeleteOutput",
	} {
		reg, ok := envelope.Registered(op)
		if !ok {
			t.Errorf("%s has no envelope registration", op)
			continue
		}
		if got := fmt.Sprintf("%T", reg.Sample); got != want {
			t.Errorf("%s registered %s, want %s", op, got, want)
		}
	}
}

// assertEmptyDir fails when anything was written into dir.
func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("%v was written; this path must write nothing", names)
	}
}
