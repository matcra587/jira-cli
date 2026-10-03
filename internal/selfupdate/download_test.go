package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	xos "github.com/gechr/x/os"
)

func TestReleaseDownloadArchiveRefusal(t *testing.T) {
	validTar := tarFixture(t, archiveMember{name: "jira", body: "executable"})
	badTrailer := bytes.Clone(validTar)
	badTrailer[len(badTrailer)-8] ^= 1
	validZIP := zipFixture(t, archiveMember{name: "jira.exe", body: "executable"})
	badZIPCRC := bytes.Clone(validZIP)
	position := bytes.Index(badZIPCRC, []byte("executable"))
	if position < 0 {
		t.Fatal("ZIP fixture lacks stored executable bytes")
	}
	badZIPCRC[position] ^= 1
	for _, test := range []struct {
		name, goos string
		archive    []byte
	}{
		{name: "tar_missing", goos: xos.PlatformLinux, archive: tarFixture(t, archiveMember{name: "LICENSE", body: "license"})},
		{name: "tar_duplicate", goos: xos.PlatformLinux, archive: tarFixture(t, archiveMember{name: "jira", body: "one"}, archiveMember{name: "jira", body: "two"})},
		{name: "tar_symlink", goos: xos.PlatformLinux, archive: tarFixture(t, archiveMember{name: "jira", typeFlag: tar.TypeSymlink})},
		{name: "tar_hardlink", goos: xos.PlatformLinux, archive: tarFixture(t, archiveMember{name: "jira", typeFlag: tar.TypeLink})},
		{name: "tar_empty", goos: xos.PlatformLinux, archive: tarFixture(t, archiveMember{name: "jira"})},
		{name: "tar_nested", goos: xos.PlatformLinux, archive: tarFixture(t, archiveMember{name: "nested/jira", body: "exe"})},
		{name: "tar_path_escape", goos: xos.PlatformLinux, archive: tarFixture(t, archiveMember{name: "../jira", body: "exe"})},
		{name: "tar_gzip_trailer", goos: xos.PlatformLinux, archive: badTrailer},
		{name: "tar_truncated", goos: xos.PlatformLinux, archive: validTar[:len(validTar)-5]},
		{name: "not_gzip", goos: xos.PlatformLinux, archive: []byte("not an archive")},
		{name: "zip_missing", goos: xos.PlatformWindows, archive: zipFixture(t, archiveMember{name: "LICENSE", body: "license"})},
		{name: "zip_duplicate", goos: xos.PlatformWindows, archive: zipFixture(t, archiveMember{name: "jira.exe", body: "one"}, archiveMember{name: "jira.exe", body: "two"})},
		{name: "zip_symlink", goos: xos.PlatformWindows, archive: zipFixture(t, archiveMember{name: "jira.exe", body: "victim", mode: 0o777 | os.ModeSymlink})},
		{name: "zip_empty", goos: xos.PlatformWindows, archive: zipFixture(t, archiveMember{name: "jira.exe"})},
		{name: "zip_nested", goos: xos.PlatformWindows, archive: zipFixture(t, archiveMember{name: "nested/jira.exe", body: "exe"})},
		{name: "zip_crc", goos: xos.PlatformWindows, archive: badZIPCRC},
		{name: "zip_truncated", goos: xos.PlatformWindows, archive: validZIP[:len(validZIP)-5]},
		{name: "not_zip", goos: xos.PlatformWindows, archive: []byte("not an archive")},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newReleaseFixture(t, test.goos, xos.ArchARM64, test.archive)
			release, err := f.source.Latest(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := f.source.Download(t.Context(), release, test.goos, xos.ArchARM64)
			if err == nil || candidate != nil {
				t.Fatalf("invalid archive candidate=%v err=%v", candidate, err)
			}
			f.assertPrivateCleanup(t)
		})
	}
}

type responseBodyTransport struct {
	base   http.RoundTripper
	suffix string
	body   func(io.ReadCloser) io.ReadCloser
}

func (b responseBodyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := b.base.RoundTrip(request)
	if err == nil && strings.HasSuffix(request.URL.Path, b.suffix) {
		response.Body = b.body(response.Body)
	}
	return response, err
}

type observedBody struct {
	io.ReadCloser
	read  func([]byte) (int, error)
	close func() error
}

func (b observedBody) Read(data []byte) (int, error) {
	if b.read != nil {
		return b.read(data)
	}
	return b.ReadCloser.Read(data)
}

func (b observedBody) Close() error {
	return errors.Join(b.ReadCloser.Close(), b.close())
}

func TestReleaseDownloadCancellation(t *testing.T) {
	for _, phase := range []string{"before_download", "after_checksum", "during_archive", "after_archive"} {
		t.Run(phase, func(t *testing.T) {
			f := newReleaseFixture(t, xos.PlatformLinux, xos.ArchAMD64, tarFixture(t, archiveMember{name: "jira", body: "new executable"}))
			release, err := f.source.Latest(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			closed := false
			if phase == "before_download" {
				cancel()
			} else {
				suffix := "/assets/10"
				if phase == "after_checksum" {
					suffix = "/assets/11"
				}
				f.source, err = NewGitHubSource(&http.Client{Transport: responseBodyTransport{
					base: f.source.assets.Transport, suffix: suffix, body: func(body io.ReadCloser) io.ReadCloser {
						observed := observedBody{ReadCloser: body, close: func() error { closed = true; cancel(); return nil }}
						if phase == "during_archive" {
							observed.read = func(data []byte) (int, error) {
								count, err := body.Read(data[:min(len(data), 1)])
								cancel()
								return count, err
							}
						}
						return observed
					},
				}})
				if err != nil {
					t.Fatal(err)
				}
			}
			candidate, err := f.source.Download(ctx, release, xos.PlatformLinux, xos.ArchAMD64)
			if candidate != nil || !errors.Is(err, context.Canceled) || (phase != "before_download" && !closed) {
				t.Fatalf("canceled download candidate=%v err=%v closed=%t", candidate, err, closed)
			}
			if phase == "before_download" || phase == "after_checksum" {
				f.assertRequestCount(t, "/assets/10", 0)
			}
			f.assertPrivateCleanup(t)
		})
	}
}

func TestReleaseDownloadCloseFailure(t *testing.T) {
	for _, suffix := range []string{"/assets/10", "/assets/11"} {
		t.Run(suffix, func(t *testing.T) {
			f := newReleaseFixture(t, xos.PlatformLinux, xos.ArchAMD64, tarFixture(t, archiveMember{name: "jira", body: "exe"}))
			release, err := f.source.Latest(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			closeFailure := errors.New("injected response close failure")
			f.source, err = NewGitHubSource(&http.Client{Transport: responseBodyTransport{
				base: f.source.assets.Transport, suffix: suffix, body: func(body io.ReadCloser) io.ReadCloser {
					return observedBody{ReadCloser: body, close: func() error { return closeFailure }}
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := f.source.Download(t.Context(), release, xos.PlatformLinux, xos.ArchAMD64)
			if candidate != nil || !errors.Is(err, closeFailure) {
				t.Fatalf("close failure candidate=%v err=%v", candidate, err)
			}
			f.assertPrivateCleanup(t)
		})
	}
}

func TestArchiveMemberCountBound(t *testing.T) {
	members := make([]archiveMember, maxArchiveEntries)
	for index := range members {
		members[index] = archiveMember{name: fmt.Sprintf("extra-%d", index), body: "x"}
	}
	members[0] = archiveMember{name: "jira", body: "executable"}
	var out bytes.Buffer
	if err := extractTar(t.Context(), bytes.NewReader(tarFixture(t, members...)), &out); err != nil {
		t.Fatalf("tar exact member bound: %v", err)
	}
	members = append(members, archiveMember{name: "extra-final", body: "x"})
	err := extractTar(t.Context(), bytes.NewReader(tarFixture(t, members...)), &out)
	if !errors.Is(err, ErrReleaseInvalid) {
		t.Fatalf("tar entry bound: %v", err)
	}
	members[0].name = "jira.exe"
	archive := zipFixture(t, members...)
	err = extractZIP(t.Context(), bytes.NewReader(archive), int64(len(archive)), &out)
	if !errors.Is(err, ErrReleaseInvalid) {
		t.Fatalf("ZIP entry bound: %v", err)
	}
	archive = zipFixture(t, members[:maxArchiveEntries]...)
	if err := extractZIP(t.Context(), bytes.NewReader(archive), int64(len(archive)), &out); err != nil {
		t.Fatalf("ZIP exact member bound: %v", err)
	}
}

func TestZIPExpansionBound(t *testing.T) {
	archive := zipFixture(t, archiveMember{name: "padding", body: "x"}, archiveMember{name: "jira.exe", body: "executable"})
	index := bytes.Index(archive, []byte{'P', 'K', 1, 2})
	if index < 0 {
		t.Fatal("ZIP fixture has no central directory")
	}
	binary.LittleEndian.PutUint32(archive[index+24:index+28], uint32(maxExpandedArchiveBytes+1))
	err := extractZIP(t.Context(), bytes.NewReader(archive), int64(len(archive)), io.Discard)
	if !errors.Is(err, ErrReleaseInvalid) {
		t.Fatalf("ZIP expansion bound: %v", err)
	}
}

func TestReleaseStreamBound(t *testing.T) {
	f := newReleaseFixture(t, xos.PlatformLinux, xos.ArchAMD64, []byte("unused"))
	release, err := f.source.Latest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.assets["11"] = bytes.Repeat([]byte("x"), 100)
	f.mu.Unlock()
	asset := release.assets[1]
	asset.size = 32
	var out bytes.Buffer
	err = f.source.download(t.Context(), asset, &out, 32)
	if !errors.Is(err, ErrReleaseInvalid) || out.Len() != 33 {
		t.Fatalf("stream bound: bytes=%d err=%v", out.Len(), err)
	}
}

func TestReleaseMetadataCloseFailure(t *testing.T) {
	f := newReleaseFixture(t, xos.PlatformLinux, xos.ArchAMD64, []byte("unused"))
	closeFailure := errors.New("injected metadata close failure")
	source, err := NewGitHubSource(&http.Client{Transport: responseBodyTransport{
		base: f.source.assets.Transport.(releaseTransport).base, suffix: "/releases/latest", body: func(body io.ReadCloser) io.ReadCloser {
			return observedBody{ReadCloser: body, close: func() error { return closeFailure }}
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	release, err := source.Latest(t.Context())
	if !errors.Is(err, closeFailure) || release.ID != 0 {
		t.Fatalf("metadata close failure: %+v, %v", release, err)
	}
}

func TestReleaseErrorResponseClosure(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f := newReleaseFixture(t, xos.PlatformLinux, xos.ArchAMD64, []byte("unused"))
			release, err := f.source.Latest(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			suffix := "/releases/latest"
			f.mu.Lock()
			if status == http.StatusFound {
				f.redirect = "https://release-assets.githubusercontent.com/download/11"
				suffix = "/assets/11"
			} else {
				f.status = status
			}
			f.mu.Unlock()
			closed := false
			source, err := NewGitHubSource(&http.Client{Transport: responseBodyTransport{
				base: f.source.assets.Transport.(releaseTransport).base, suffix: suffix, body: func(body io.ReadCloser) io.ReadCloser {
					return observedBody{ReadCloser: body, close: func() error { closed = true; return nil }}
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if status == http.StatusFound {
				var out bytes.Buffer
				err = source.download(t.Context(), release.assets[1], &out, maxChecksumBytes)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				_, err = source.Latest(t.Context())
				if err == nil {
					t.Fatal("native error status accepted")
				}
			}
			if !closed {
				t.Fatal("original error/redirect response body was not closed")
			}
		})
	}
}

func TestArchiveExecutableSizeBound(t *testing.T) {
	for _, size := range []int64{0, MaxExecutableBytes + 1} {
		var out bytes.Buffer
		err := copyExecutable(t.Context(), &out, strings.NewReader("small"), size)
		if !errors.Is(err, ErrReleaseInvalid) || out.Len() != 0 {
			t.Fatalf("executable size %d: %v", size, err)
		}
	}
}

type zeroReader struct{}

func (zeroReader) Read(data []byte) (int, error) { clear(data); return len(data), nil }

func TestTarExpansionBound(t *testing.T) {
	// A small compressed fixture expands past the actual production bound. This
	// verifies the reader bound, rather than merely checking advertised lengths.
	var buffer bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buffer, gzip.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(gz)
	if err := writer.WriteHeader(&tar.Header{Name: "jira", Mode: 0o755, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(writer, "x"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteHeader(&tar.Header{Name: "padding", Size: maxExpandedArchiveBytes + 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(writer, zeroReader{}, maxExpandedArchiveBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(writer.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	err = extractTar(t.Context(), bytes.NewReader(buffer.Bytes()), io.Discard)
	if !errors.Is(err, ErrReleaseInvalid) {
		t.Fatalf("tar expansion bound: %v", err)
	}
}
