package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	xos "github.com/gechr/x/os"
)

type releaseFixture struct {
	source   *GitHubSource
	mu       sync.Mutex
	metadata []byte
	status   int
	assets   map[string][]byte
	requests map[string]int
	redirect string
	root     string
}

type mappedReleaseTransport struct {
	base   http.RoundTripper
	server *url.URL
}

func (m mappedReleaseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copyRequest := request.Clone(request.Context())
	copyURL := *request.URL
	copyURL.Scheme, copyURL.Host = m.server.Scheme, m.server.Host
	copyRequest.URL = &copyURL
	copyRequest.Host = request.URL.Host
	return m.base.RoundTrip(copyRequest)
}

func newReleaseFixture(t *testing.T, goos, arch string, archive []byte) *releaseFixture {
	t.Helper()
	f := &releaseFixture{status: http.StatusOK, assets: make(map[string][]byte), requests: make(map[string]int), root: t.TempDir()}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, f.root)
	}
	t.Setenv("GITHUB_TOKEN", "unused-test-token")
	t.Setenv("GH_TOKEN", "unused-test-token")
	name, err := (Release{Version: "v1.2.3"}).ArchiveName(goos, arch)
	if err != nil {
		t.Fatal(err)
	}
	f.assets["10"] = archive
	f.assets["11"] = fmt.Appendf(nil, "%x  %s\n", sha256.Sum256(archive), name)
	f.metadata = fmt.Appendf(nil, `{"id":100,"tag_name":"v1.2.3","draft":false,"prerelease":false,"published_at":"2026-01-01T00:00:00Z","assets":[{"id":10,"name":%q,"state":"uploaded","size":%d},{"id":11,"name":"checksums.txt","state":"uploaded","size":%d}]}`, name, len(archive), len(f.assets["11"]))
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Error("anonymous release request carried authentication")
		}
		if request.Method != http.MethodGet {
			t.Errorf("release request method = %s", request.Method)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests[request.URL.Path]++
		if strings.HasSuffix(request.URL.Path, "/releases/latest") {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(f.status)
			if _, err := writer.Write(f.metadata); err != nil {
				t.Errorf("write release metadata: %v", err)
			}
			return
		}
		if f.redirect != "" && strings.Contains(request.URL.Path, "/releases/assets/") {
			writer.Header().Set("Location", f.redirect)
			writer.WriteHeader(http.StatusFound)
			return
		}
		id := filepath.Base(request.URL.Path)
		body, ok := f.assets[id]
		if !ok {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/octet-stream")
		if _, err := writer.Write(body); err != nil {
			t.Errorf("write release asset: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	location, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	f.source, err = NewGitHubSource(&http.Client{Transport: mappedReleaseTransport{base: server.Client().Transport, server: location}})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *releaseFixture) changeMetadata(t *testing.T, edit func(map[string]any)) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var metadata map[string]any
	if err := json.Unmarshal(f.metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	edit(metadata)
	var err error
	f.metadata, err = json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
}

func (f *releaseFixture) assertRequestCount(t *testing.T, suffix string, want int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for path, requests := range f.requests {
		if strings.HasSuffix(path, suffix) {
			count += requests
		}
	}
	if count != want {
		t.Fatalf("requests ending %q = %d, want %d", suffix, count, want)
	}
}

func (f *releaseFixture) assertPrivateCleanup(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(f.root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("private download cleanup: entries=%v err=%v", entries, err)
	}
}

type archiveMember struct {
	name     string
	body     string
	mode     os.FileMode
	typeFlag byte
}

func tarFixture(t *testing.T, members ...archiveMember) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(gz)
	for _, member := range members {
		typeFlag := member.typeFlag
		if typeFlag == 0 {
			typeFlag = tar.TypeReg
		}
		header := &tar.Header{Name: member.name, Mode: 0o755, Typeflag: typeFlag, Size: int64(len(member.body))}
		if typeFlag != tar.TypeReg {
			header.Size = 0
			header.Linkname = "victim"
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size != 0 {
			if _, err := io.WriteString(writer, member.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := errors.Join(writer.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func zipFixture(t *testing.T, members ...archiveMember) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, member := range members {
		header := &zip.FileHeader{Name: member.name, Method: zip.Store}
		mode := member.mode
		if mode == 0 {
			mode = 0o755
		}
		header.SetMode(mode)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(file, member.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestFrozenReleaseDownload(t *testing.T) {
	for _, goos := range []string{xos.PlatformLinux, xos.PlatformDarwin, xos.PlatformWindows} {
		t.Run(goos, func(t *testing.T) {
			const executable = "new executable bytes"
			targetDirectory := t.TempDir()
			archive := tarFixture(t, archiveMember{name: "jira", body: executable}, archiveMember{name: "LICENSE", body: "license"})
			if goos == xos.PlatformWindows {
				archive = zipFixture(t, archiveMember{name: "jira.exe", body: executable}, archiveMember{name: "LICENSE", body: "license"})
			}
			f := newReleaseFixture(t, goos, xos.ArchARM64, archive)
			release, err := f.source.Latest(t.Context())
			if err != nil || release.ID != 100 || release.Version != "v1.2.3" {
				t.Fatalf("latest = %+v, %v", release, err)
			}
			f.changeMetadata(t, func(metadata map[string]any) { metadata["id"] = 200; metadata["tag_name"] = "v9.9.9" })
			candidate, err := f.source.Download(t.Context(), release, goos, xos.ArchARM64)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := candidate.File.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(candidate.File)
			if err != nil || string(data) != executable || candidate.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) || candidate.ArchiveSHA256 != fmt.Sprintf("%x", sha256.Sum256(archive)) {
				t.Fatalf("candidate content/hash = %q/%s, %v", data, candidate.SHA256, err)
			}
			info, err := candidate.File.Stat()
			if err != nil || (!xos.IsWindows() && info.Mode().Perm() != 0o600) {
				t.Fatalf("candidate private mode: %v, %v", info, err)
			}
			// Publishing to a renamed executable must still consume the packaged jira.
			targetPath := filepath.Join(targetDirectory, "renamed-jira")
			writeExecutable(t, targetPath, "old executable bytes", 0o755)
			target, err := OpenTarget(t.Context(), targetPath)
			if err != nil {
				t.Fatal(err)
			}
			publication, publishErr := target.Publish(t.Context(), candidate.File, candidate.SHA256)
			if err := errors.Join(publishErr, target.Close()); err != nil || !publication.Published {
				t.Fatalf("publication = %+v, %v", publication, err)
			}
			assertExecutable(t, targetPath, executable)
			if err := candidate.Close(); err != nil {
				t.Fatal(err)
			}
			f.assertRequestCount(t, "/releases/latest", 1)
			f.assertRequestCount(t, "/assets/10", 1)
			f.assertRequestCount(t, "/assets/11", 1)
			// targetPath lives outside the download root.
			f.assertPrivateCleanup(t)
		})
	}
}

func TestLatestReleaseRefusal(t *testing.T) {
	tests := []struct {
		name   string
		edit   func(map[string]any)
		body   string
		status int
		want   error
	}{
		{name: "missing", status: http.StatusNotFound, want: ErrReleaseUnavailable},
		{name: "unexpected_success", status: http.StatusCreated, want: ErrReleaseInvalid},
		{name: "null", body: "null", want: ErrReleaseInvalid},
		{name: "multiple_documents", body: "{} {}", want: ErrReleaseInvalid},
		{name: "oversized", edit: func(m map[string]any) { m["body"] = strings.Repeat("x", int(maxReleaseMetadataBytes)) }, want: ErrReleaseInvalid},
		{name: "missing_identity", edit: func(m map[string]any) { delete(m, "id") }, want: ErrReleaseInvalid},
		{name: "missing_draft", edit: func(m map[string]any) { delete(m, "draft") }, want: ErrReleaseInvalid},
		{name: "draft", edit: func(m map[string]any) { m["draft"] = true }, want: ErrReleaseInvalid},
		{name: "prerelease", edit: func(m map[string]any) { m["prerelease"] = true }, want: ErrReleaseInvalid},
		{name: "missing_publication", edit: func(m map[string]any) { delete(m, "published_at") }, want: ErrReleaseInvalid},
		{name: "invalid_tag", edit: func(m map[string]any) { m["tag_name"] = "unversioned" }, want: ErrReleaseInvalid},
		{name: "development_tag", edit: func(m map[string]any) { m["tag_name"] = "v1.2.3-2-gabc" }, want: ErrReleaseInvalid},
		{name: "prerelease_tag", edit: func(m map[string]any) { m["tag_name"] = "v1.2.3-rc1" }, want: ErrReleaseInvalid},
		{name: "gamma_tag", edit: func(m map[string]any) { m["tag_name"] = "v1.2.3-gamma" }, want: ErrReleaseInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newReleaseFixture(t, xos.PlatformLinux, xos.ArchAMD64, []byte("unused"))
			if test.edit != nil {
				f.changeMetadata(t, test.edit)
			}
			f.mu.Lock()
			if test.name == "multiple_documents" {
				f.metadata = append(f.metadata, []byte(" {}")...)
			} else if test.body != "" {
				f.metadata = []byte(test.body)
			}
			if test.status != 0 {
				f.status = test.status
			}
			f.mu.Unlock()
			release, err := f.source.Latest(t.Context())
			if !errors.Is(err, test.want) || release.ID != 0 || release.Version != "" {
				t.Fatalf("latest refusal = %+v, %v; want %v", release, err, test.want)
			}
			f.assertPrivateCleanup(t)
		})
	}
}

func TestReleaseVersionOrdering(t *testing.T) {
	for _, test := range []struct {
		current, latest string
		want            bool
		invalid         bool
	}{
		{current: "v1.2.2", latest: "1.2.3", want: true, invalid: false},
		{current: "v1.2.3", latest: "1.2.3", want: false, invalid: false},
		{current: "1.2.4", latest: "v1.2.3", want: false, invalid: false},
		{current: "v1.2.3-2-gabc-dev", latest: "v1.2.3", want: false, invalid: false},
		{current: "v1.2.3-2-gabc-dev", latest: "v1.2.4", want: true, invalid: false},
		{current: "1.2.3-rc2", latest: "1.2.3-rc10", want: true, invalid: false},
		{current: "dev", latest: "1.2.3", want: false, invalid: true},
		{current: "1.2.3", latest: "dev", want: false, invalid: true},
		{current: "", latest: "1.2.3", want: false, invalid: true},
		{current: "1.2.3", latest: "1.2.4\n", want: false, invalid: true},
	} {
		t.Run(test.current+"_"+test.latest, func(t *testing.T) {
			newer, err := Newer(test.current, test.latest)
			if newer != test.want || (err != nil) != test.invalid {
				t.Fatalf("newer = %t, %v", newer, err)
			}
		})
	}
}

func TestReleaseDownloadRefusesIntegrity(t *testing.T) {
	archive := tarFixture(t, archiveMember{name: "jira", body: "new executable"})
	f := newReleaseFixture(t, xos.PlatformLinux, xos.ArchAMD64, archive)
	f.mu.Lock()
	f.assets["11"] = bytes.Replace(f.assets["11"], f.assets["11"][:64], bytes.Repeat([]byte("0"), 64), 1)
	f.mu.Unlock()
	release, err := f.source.Latest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := f.source.Download(t.Context(), release, xos.PlatformLinux, xos.ArchAMD64)
	if candidate != nil || !errors.Is(err, ErrIntegrity) {
		t.Fatalf("candidate=%v err=%v", candidate, err)
	}
	f.assertPrivateCleanup(t)
}

func TestChecksumManifestRefusal(t *testing.T) {
	const name = "jira_1.2.3_linux_amd64.tar.gz"
	valid := strings.Repeat("a", 64) + "  " + name + "\n"
	for _, test := range []struct {
		name, manifest string
		valid          bool
	}{
		{name: "valid", manifest: valid, valid: true},
		{name: "binary_marker", manifest: strings.Replace(valid, "  ", " *", 1), valid: true},
		{name: "crlf", manifest: strings.ReplaceAll(valid, "\n", "\r\n"), valid: true},
		{name: "missing", manifest: "", valid: false},
		{name: "duplicate", manifest: valid + valid, valid: false},
		{name: "invalid_hex", manifest: "x" + valid[1:], valid: false},
		{name: "wrong_name", manifest: strings.Replace(valid, name, name+".sig", 1), valid: false},
		{name: "short_hash", manifest: valid[1:], valid: false},
		{name: "malformed_other_row", manifest: valid + "oops\n", valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			checksum, err := checksumFor([]byte(test.manifest), name)
			if (err == nil) != test.valid || (test.valid && checksum != strings.Repeat("a", 64)) || (!test.valid && !errors.Is(err, ErrIntegrity)) {
				t.Fatalf("checksum = %q, %v", checksum, err)
			}
		})
	}
}

func TestReleaseAssetMetadataRefusal(t *testing.T) {
	for _, name := range []string{"missing_archive", "missing_checksum", "duplicate_archive", "duplicate_checksum", "duplicate_id", "zero_id", "zero_size", "oversized", "not_uploaded"} {
		t.Run(name, func(t *testing.T) {
			f := newReleaseFixture(t, xos.PlatformLinux, xos.ArchAMD64, []byte("unused"))
			f.changeMetadata(t, func(metadata map[string]any) {
				assets := metadata["assets"].([]any)
				archive := assets[0].(map[string]any)
				switch name {
				case "missing_archive":
					assets = assets[1:]
				case "missing_checksum":
					assets = assets[:1]
				case "duplicate_archive":
					assets = append(assets, assets[0])
				case "duplicate_checksum":
					assets = append(assets, assets[1])
				case "duplicate_id":
					assets[1].(map[string]any)["id"] = archive["id"]
				case "zero_id":
					archive["id"] = 0
				case "zero_size":
					archive["size"] = 0
				case "oversized":
					archive["size"] = maxArchiveBytes + 1
				case "not_uploaded":
					archive["state"] = "open"
				}
				metadata["assets"] = assets
			})
			release, err := f.source.Latest(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := f.source.Download(t.Context(), release, xos.PlatformLinux, xos.ArchAMD64)
			if candidate != nil || !errors.Is(err, ErrReleaseInvalid) {
				t.Fatalf("candidate=%v err=%v", candidate, err)
			}
			f.assertRequestCount(t, "/assets/10", 0)
			f.assertRequestCount(t, "/assets/11", 0)
			f.assertPrivateCleanup(t)
		})
	}
}

func TestReleaseDownloadSizeMismatch(t *testing.T) {
	for _, id := range []string{"10", "11"} {
		for _, change := range []string{"truncated", "extra"} {
			t.Run(id+"_"+change, func(t *testing.T) {
				f := newReleaseFixture(t, xos.PlatformLinux, xos.ArchAMD64, tarFixture(t, archiveMember{name: "jira", body: "new executable"}))
				release, err := f.source.Latest(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				if change == "truncated" {
					f.assets[id] = f.assets[id][:len(f.assets[id])-1]
				} else {
					f.assets[id] = append(f.assets[id], 'x')
				}
				f.mu.Unlock()
				candidate, err := f.source.Download(t.Context(), release, xos.PlatformLinux, xos.ArchAMD64)
				if candidate != nil || !errors.Is(err, ErrReleaseInvalid) {
					t.Fatalf("candidate=%v err=%v", candidate, err)
				}
				f.assertPrivateCleanup(t)
			})
		}
	}
}

func TestReleaseAssetRedirects(t *testing.T) {
	for _, location := range []string{"https://release-assets.githubusercontent.com/download/11", "http://release-assets.githubusercontent.com/download/11", "https://evil.example/download/11", "https://api.github.com@evil.example/11", "https://api.github.com:443/11"} {
		t.Run(location, func(t *testing.T) {
			f := newReleaseFixture(t, xos.PlatformLinux, xos.ArchAMD64, tarFixture(t, archiveMember{name: "jira", body: "new"}))
			release, err := f.source.Latest(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.redirect = location
			f.mu.Unlock()
			var body bytes.Buffer
			err = f.source.download(t.Context(), release.assets[1], &body, maxChecksumBytes)
			valid := location == "https://release-assets.githubusercontent.com/download/11"
			if (err == nil) != valid {
				t.Fatalf("redirect accepted=%t err=%v", valid, err)
			}
			f.assertRequestCount(t, "/download/11", map[bool]int{true: 1, false: 0}[valid])
		})
	}
}

func TestReleaseArchivePlatforms(t *testing.T) {
	for _, test := range []struct{ goos, arch, want string }{
		{goos: xos.PlatformLinux, arch: xos.ArchAMD64, want: "jira_1.2.3_linux_amd64.tar.gz"},
		{goos: xos.PlatformLinux, arch: xos.ArchARM64, want: "jira_1.2.3_linux_arm64.tar.gz"},
		{goos: xos.PlatformDarwin, arch: xos.ArchARM64, want: "jira_1.2.3_darwin_arm64.tar.gz"},
		{goos: xos.PlatformWindows, arch: xos.ArchAMD64, want: "jira_1.2.3_windows_amd64.zip"},
		{goos: xos.PlatformWindows, arch: xos.ArchARM64, want: "jira_1.2.3_windows_arm64.zip"},
		{goos: xos.PlatformDarwin, arch: xos.ArchAMD64, want: ""},
		{goos: xos.PlatformLinux, arch: "386", want: ""},
		{goos: xos.PlatformFreeBSD, arch: xos.ArchAMD64, want: ""},
	} {
		t.Run(test.goos+"_"+test.arch, func(t *testing.T) {
			name, err := (Release{Version: "v1.2.3"}).ArchiveName(test.goos, test.arch)
			if name != test.want || (err != nil) != (test.want == "") {
				t.Fatalf("archive=%q err=%v", name, err)
			}
		})
	}
}

func TestLatestReleaseCancellation(t *testing.T) {
	f := newReleaseFixture(t, xos.PlatformLinux, xos.ArchAMD64, []byte("unused"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	release, err := f.source.Latest(ctx)
	if !errors.Is(err, context.Canceled) || release.ID != 0 {
		t.Fatalf("canceled latest = %+v, %v", release, err)
	}
	f.assertRequestCount(t, "/releases/latest", 0)
}
