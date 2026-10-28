package issue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/core"
)

type downloadLimitBody struct {
	io.Reader
	read   int64
	closed bool
}

func (b *downloadLimitBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += int64(n)
	return n, err
}

func (b *downloadLimitBody) Close() error { b.closed = true; return nil }

type downloadLimitClient struct {
	unusedJiraClient
	body *downloadLimitBody
	size int64
}

func (c downloadLimitClient) GetAttachmentContent(context.Context, string) (core.AttachmentContent, error) {
	return core.AttachmentContent{Body: c.body, Size: c.size}, nil
}

type downloadZeroReader struct{}

func (downloadZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestDownloadDefaultLimitRejectsUnknownLength(t *testing.T) {
	const limit int64 = 100 << 20
	body := &downloadLimitBody{Reader: io.LimitReader(downloadZeroReader{}, limit+1)}
	client := downloadLimitClient{body: body}
	deps := Deps{JiraClient: func(context.Context) (core.JiraClient, error) { return client, nil }}
	dir := t.TempDir()
	opts := downloadOpts(dir)
	opts.as = "bounded.bin"
	_, err := runAttachmentDownload(t.Context(), deps, invocation(false, "10042"), opts)
	if err == nil {
		t.Fatal("unknown-length download exceeded the default 100 MiB bound without an error")
	}
	if !body.closed || body.read > limit+1 {
		t.Fatal("download did not close or read beyond its single-byte overflow probe")
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil || len(entries) != 0 {
		t.Fatal("limit refusal retained a partial destination", readErr)
	}
}

func TestDownloadLimitBoundariesAndOverwrite(t *testing.T) {
	for _, size := range []int64{-1, 0, 1, math.MaxInt64} {
		for _, count := range []int{0, 7, 8, 9, 64} {
			for _, overwrite := range []bool{false, true} {
				t.Run(fmt.Sprintf("size=%d/count=%d/overwrite=%v", size, count, overwrite), func(t *testing.T) {
					dir := t.TempDir()
					path := filepath.Join(dir, "bounded.bin")
					var before os.FileInfo
					if overwrite {
						if err := os.WriteFile(path, []byte("original"), 0o640); err != nil {
							t.Fatal(err)
						}
						var err error
						before, err = os.Stat(path)
						if err != nil {
							t.Fatal(err)
						}
					}
					payload := bytes.Repeat([]byte{0xff}, count)
					body := &downloadLimitBody{Reader: bytes.NewReader(payload)}
					client := downloadLimitClient{body: body, size: size}
					deps := Deps{JiraClient: func(context.Context) (core.JiraClient, error) { return client, nil }}
					opts := downloadOptions{to: dir, as: "bounded.bin", overwrite: overwrite, maxBytes: 8}
					out, err := runAttachmentDownload(t.Context(), deps, invocation(false, "10042"), opts)
					if !body.closed || body.read > 9 {
						t.Fatal("stream was not closed or read beyond overflow probe")
					}
					if count <= 8 {
						if err != nil || out.BytesWritten == nil || *out.BytesWritten != int64(count) {
							t.Fatalf("legitimate download rejected: %+v %v", out, err)
						}
						got, err := os.ReadFile(path)
						if err != nil || !bytes.Equal(got, payload) {
							t.Fatal("download changed bytes", err)
						}
						return
					}
					if err == nil || cli.MapError(err).Code != cli.CodeAttachmentDownloadLimit {
						t.Fatalf("overflow missing typed refusal: %v", err)
					}
					entries, err := os.ReadDir(dir)
					if err != nil {
						t.Fatal(err)
					}
					if !overwrite {
						if len(entries) != 0 {
							t.Fatal("overflow retained partial download")
						}
						return
					}
					got, err := os.ReadFile(path)
					if err != nil || string(got) != "original" || len(entries) != 1 {
						t.Fatal("overflow altered original or retained staging", err)
					}
					after, err := os.Stat(path)
					if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
						t.Fatal("overflow changed original identity or metadata", err)
					}
				})
			}
		}
	}
}

func TestDownloadLimitAllowsMaxInt64(t *testing.T) {
	body := &downloadLimitBody{Reader: bytes.NewReader([]byte("small"))}
	client := downloadLimitClient{body: body}
	deps := Deps{JiraClient: func(context.Context) (core.JiraClient, error) { return client, nil }}
	out, err := runAttachmentDownload(t.Context(), deps, invocation(false, "10042"), downloadOptions{to: t.TempDir(), as: "small.bin", maxBytes: math.MaxInt64})
	if err != nil || out.BytesWritten == nil || *out.BytesWritten != 5 {
		t.Fatalf("large positive limit overflowed: %+v %v", out, err)
	}
}

func TestDownloadLimitFlagValidation(t *testing.T) {
	for _, limit := range []string{"0", "-1", "9223372036854775808"} {
		t.Run(limit, func(t *testing.T) {
			cmd := newAttachmentDownload(Deps{JiraClient: func(context.Context) (core.JiraClient, error) {
				t.Fatal("invalid limit constructed client")
				return nil, nil
			}})
			cmd.SetArgs([]string{"10042", "--max-download-bytes=" + limit, "--dry-run"})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			if err := cmd.Execute(); err == nil {
				t.Fatal("invalid byte limit accepted")
			}
		})
	}
}

type downloadTruncatedReader struct{ sent bool }

func (r *downloadTruncatedReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.EOF
	}
	r.sent = true
	return copy(p, "12345678"), io.ErrUnexpectedEOF
}

func TestDownloadLimitPreservesErrorAtBoundary(t *testing.T) {
	body := &downloadLimitBody{Reader: &downloadTruncatedReader{}}
	client := downloadLimitClient{body: body}
	deps := Deps{JiraClient: func(context.Context) (core.JiraClient, error) { return client, nil }}
	dir := t.TempDir()
	_, err := runAttachmentDownload(t.Context(), deps, invocation(false, "10042"), downloadOptions{to: dir, as: "truncated.bin", maxBytes: 8})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("lost stream failure at exact limit: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("published truncated file", err)
	}
}
