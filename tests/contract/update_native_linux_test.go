//go:build linux

package contract

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	xos "github.com/gechr/x/os"
	"golang.org/x/sys/unix"

	"github.com/matcra587/jira-cli/internal/envelope"
)

// nativeReleaseFixture serves the production api.github.com URL through an
// owned HTTPS CONNECT proxy. The CLI still checks the actual host, certificate,
// anonymous request policy and asset IDs; no production endpoint override exists.
type nativeReleaseFixture struct {
	env          []string
	mu           sync.Mutex
	requests     map[string]int
	targets      map[string]int
	metadata     []byte
	listMetadata []byte
	archive      []byte
	checksum     []byte
	status       int
	onLatest     func(context.Context)
}

func nativeReleaseServer(t *testing.T, candidate []byte) *nativeReleaseFixture {
	t.Helper()
	var packed bytes.Buffer
	compressed := gzip.NewWriter(&packed)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: "jira", Mode: 0o755, Size: int64(len(candidate))}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(candidate); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	name := "jira_1.2.3_linux_" + runtime.GOARCH + ".tar.gz"
	fixture := &nativeReleaseFixture{
		requests: make(map[string]int), targets: make(map[string]int), archive: packed.Bytes(), status: http.StatusOK,
		checksum: fmt.Appendf(nil, "%x  %s\n", sha256.Sum256(packed.Bytes()), name),
	}
	fixture.metadata = fmt.Appendf(nil, `{"id":100,"tag_name":"v1.2.3","draft":false,"prerelease":false,"published_at":"2026-01-01T00:00:00Z","assets":[{"id":10,"name":%q,"state":"uploaded","size":%d},{"id":11,"name":"checksums.txt","state":"uploaded","size":%d}]}`, name, len(fixture.archive), len(fixture.checksum))
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Owned release fixture"},
		DNSNames: []string{"api.github.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	certPath := filepath.Join(t.TempDir(), "release-ca.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		fixture.requests[request.URL.Path]++
		fixture.targets[request.URL.RequestURI()]++
		if request.Method != http.MethodGet || request.Host != "api.github.com" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			t.Errorf("release request violated fixed anonymous GET policy: %s %s", request.Method, request.Host)
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		var body []byte
		switch request.URL.Path {
		case "/repos/matcra587/jira-cli/releases":
			if fixture.listMetadata == nil {
				t.Errorf("release command used an unselected release-list endpoint")
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(fixture.status)
			body = fixture.listMetadata
		case "/repos/matcra587/jira-cli/releases/latest":
			if fixture.onLatest != nil {
				fixture.onLatest(request.Context())
				if request.Context().Err() != nil {
					return
				}
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(fixture.status)
			body = fixture.metadata
		case "/repos/matcra587/jira-cli/releases/assets/10":
			body = fixture.archive
		case "/repos/matcra587/jira-cli/releases/assets/11":
			body = fixture.checksum
		default:
			t.Errorf("release command used unexpected endpoint %s", request.URL.Path)
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if _, err := writer.Write(body); err != nil {
			t.Errorf("write owned release response: %v", err)
		}
	}))
	backend.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	backend.StartTLS()
	t.Cleanup(backend.Close)
	proxy := ownedReleaseProxy(t, backend.Listener.Addr().String())
	fixture.env = []string{
		"HTTPS_PROXY=" + proxy, "HTTP_PROXY=" + proxy, "NO_PROXY=", "SSL_CERT_FILE=" + certPath,
		"SSL_CERT_DIR=" + t.TempDir(), "JIRA_KEYRING=false", "JIRA_EMAIL=", "JIRA_API_TOKEN=", "AGENT=1", "JIRA_NO_UPDATE_CHECK=1",
	}
	return fixture
}

// ownedReleaseProxy never dials the requested host. It accepts only the fixed
// release API CONNECT target, relays to the owned backend and joins both copy
// directions. Cleanup explicitly closes hijacked connections before joining.
func ownedReleaseProxy(t *testing.T, backend string) string {
	t.Helper()
	var mu sync.Mutex
	var active sync.WaitGroup
	connections := make(map[net.Conn]struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		active.Add(1)
		defer active.Done()
		if request.Method != http.MethodConnect || request.Host != "api.github.com:443" || request.Header.Get("Proxy-Authorization") != "" {
			t.Errorf("unexpected owned proxy request: %s %s", request.Method, request.Host)
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		upstream, err := net.DialTimeout("tcp", backend, time.Second)
		if err != nil {
			t.Errorf("dial owned backend: %v", err)
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		defer upstream.Close() //nolint:errcheck // closing an owned relay
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			t.Error("owned proxy cannot hijack HTTP/1 connection")
			return
		}
		client, buffered, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack owned proxy: %v", err)
			return
		}
		defer client.Close() //nolint:errcheck // closing an owned relay
		mu.Lock()
		connections[client] = struct{}{}
		connections[upstream] = struct{}{}
		mu.Unlock()
		defer func() {
			mu.Lock()
			delete(connections, client)
			delete(connections, upstream)
			mu.Unlock()
		}()
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := buffered.Flush(); err != nil {
			return
		}
		deadline := time.Now().Add(30 * time.Second)
		if err := client.SetDeadline(deadline); err != nil {
			t.Errorf("bound owned client: %v", err)
			return
		}
		if err := upstream.SetDeadline(deadline); err != nil {
			t.Errorf("bound owned upstream: %v", err)
			return
		}
		done := make(chan struct{}, 2)
		go func() { _, _ = io.Copy(upstream, buffered); done <- struct{}{} }()
		go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
		<-done
		_ = client.Close()
		_ = upstream.Close()
		<-done
	}))
	t.Cleanup(func() {
		proxy.Close()
		mu.Lock()
		for connection := range connections {
			_ = connection.Close()
		}
		mu.Unlock()
		active.Wait()
	})
	return proxy.URL
}

func nativeUpdateBinaries(t *testing.T) (original string, candidate []byte) {
	t.Helper()
	original, replacement := os.Getenv("JIRA_NATIVE_UPDATE_ORIGINAL"), os.Getenv("JIRA_NATIVE_UPDATE_CANDIDATE")
	if original == "" || replacement == "" {
		t.Skip("requires externally built archive-stamped original and candidate CLI binaries")
	}
	data, err := os.ReadFile(replacement)
	if err != nil {
		t.Fatal(err)
	}
	return original, data
}

func nativeUpdateFrame(t *testing.T, stdout string, exit int, code string) envelope.UpdateOutput {
	t.Helper()
	var frame struct {
		OK   bool                  `json:"ok"`
		Data envelope.UpdateOutput `json:"data"`
		Meta struct {
			Command  string `json:"command"`
			ExitCode *int   `json:"exit_code"`
		} `json:"meta"`
		Errors []struct{ Code string } `json:"errors"`
	}
	if err := json.Unmarshal([]byte(stdout), &frame); err != nil {
		t.Fatalf("decode native update receipt: %v; stdout=%s", err, stdout)
	}
	if frame.Meta.Command != "update" || frame.OK != (exit == 0) {
		t.Fatalf("native update frame lost command/outcome: %+v", frame)
	}
	if exit == 0 {
		if len(frame.Errors) != 0 || frame.Meta.ExitCode != nil {
			t.Fatalf("successful native frame retained errors: %+v", frame)
		}
	} else if frame.Meta.ExitCode == nil || *frame.Meta.ExitCode != exit || len(frame.Errors) != 1 || frame.Errors[0].Code != code {
		t.Fatalf("native failure lost typed code %s/exit%d: %+v", code, exit, frame)
	}
	return frame.Data
}

func nativeVersion(t *testing.T, bin, dir string, env []string) string {
	t.Helper()
	stdout, stderr, exit := runExecutableIOInDirStarted(t, bin, dir, env, nil, nil, nil, "version", "--output=json")
	var frame struct {
		OK   bool                   `json:"ok"`
		Data envelope.VersionOutput `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &frame); err != nil || exit != 0 || stderr != "" || !frame.OK {
		t.Fatalf("independent native version failed: exit%d err=%v stderr=%s", exit, err, stderr)
	}
	return frame.Data.Version
}

func TestNativeArchiveUpdateCommand(t *testing.T) {
	original, candidate := nativeUpdateBinaries(t)
	for _, mode := range []string{"success", "preview", "current", "integrity", "unavailable", "changed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "jira")
			if err := xos.CopyFile(original, bin); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(bin, 0o700); err != nil { //nolint:gosec // owned native executable fixture
				t.Fatal(err)
			}
			before, err := os.ReadFile(bin)
			if err != nil {
				t.Fatal(err)
			}
			fixture := nativeReleaseServer(t, candidate)
			current := nativeVersion(t, bin, root, fixture.env)
			if current != "v1.0.0" {
				t.Fatalf("original fixture version = %q, want v1.0.0", current)
			}
			args := []string{"up", "--force", "--output=json"}
			wantExit, code := 0, ""
			preserved := before
			processReady := make(chan *os.Process, 1)
			fixture.mu.Lock()
			switch mode {
			case "preview":
				args = append(args, "--dry-run")
			case "current":
				fixture.metadata = bytes.ReplaceAll(fixture.metadata, []byte(`"tag_name":"v1.2.3"`), []byte(`"tag_name":"v1.0.0"`))
			case "integrity":
				fixture.checksum = bytes.Replace(fixture.checksum, fixture.checksum[:64], bytes.Repeat([]byte("0"), 64), 1)
				wantExit, code = 5, "update_integrity"
			case "unavailable":
				fixture.status, fixture.metadata = http.StatusNotFound, []byte(`{"message":"No published release"}`)
				wantExit, code = 2, "update_unavailable"
			case "changed":
				preserved = append(bytes.Clone(before), []byte("owned intervening replacement")...)
				fixture.onLatest = func(context.Context) {
					if err := xos.AtomicWrite(bin, preserved, 0o700); err != nil {
						t.Errorf("publish intervening owned binary: %v", err)
					}
				}
				wantExit, code = 3, "update_changed"
			case "canceled":
				fixture.onLatest = func(ctx context.Context) {
					var process *os.Process
					select {
					case process = <-processReady:
					case <-ctx.Done():
						return
					case <-time.After(5 * time.Second):
						t.Error("CLI process handle was not published before signaling")
						return
					}
					if err := process.Signal(os.Interrupt); err != nil {
						t.Errorf("signal actual update command: %v", err)
						return
					}
					select {
					case <-ctx.Done():
					case <-time.After(5 * time.Second):
						t.Error("canceled CLI did not close its owned release request")
					}
				}
				wantExit, code = 6, "canceled"
			}
			fixture.mu.Unlock()
			downloadDir := t.TempDir()
			env := append(slices.Clone(fixture.env), "TMPDIR="+downloadDir)
			stdout, stderr, exit := runExecutableIOInDirStarted(t, bin, root, env, nil, nil, func(started *os.Process) { processReady <- started }, args...)
			if exit != wantExit || (exit == 0 && stderr != "") {
				t.Fatalf("native %s update exit=%d want%d stderr=%s stdout=%s", mode, exit, wantExit, stderr, stdout)
			}
			out := nativeUpdateFrame(t, stdout, exit, code)
			if out.Channel != "github-archive" || out.Current != current || out.Managed || out.Brew != nil || out.Uncertain || out.DryRun != (mode == "preview") {
				t.Fatalf("native archive facts: %+v", out)
			}
			if mode == "unavailable" || mode == "canceled" {
				if out.Latest != "" || out.UpdateAvailable != nil {
					t.Fatalf("unavailable release fabricated comparison: %+v", out)
				}
			} else if out.UpdateAvailable == nil || *out.UpdateAvailable != (mode != "current") {
				t.Fatalf("native comparison changed: %+v", out)
			}
			installed, err := os.ReadFile(bin)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "success":
				if !out.Attempted || !out.Updated || out.Publication == nil || !out.Publication.Published || !out.Publication.Durable || out.Publication.Uncertain || out.Publication.Bytes != int64(len(candidate)) || out.Publication.SHA256 != fmt.Sprintf("%x", sha256.Sum256(candidate)) || !bytes.Equal(installed, candidate) || nativeVersion(t, bin, root, env) != "v1.2.3" {
					t.Fatalf("native publication/installed bytes/version differ: %+v", out)
				}
			case "changed":
				if !out.Attempted || out.Updated || out.Publication == nil || out.Publication.Published || !bytes.Equal(installed, preserved) {
					t.Fatalf("intervening replacement was not preserved: %+v", out)
				}
			default:
				if out.Attempted || out.Updated || out.Publication != nil || !bytes.Equal(installed, preserved) || nativeVersion(t, bin, root, env) != current {
					t.Fatalf("refused/preview update changed installation: %+v", out)
				}
			}
			fixture.mu.Lock()
			latestReads := fixture.requests["/repos/matcra587/jira-cli/releases/latest"]
			archiveReads := fixture.requests["/repos/matcra587/jira-cli/releases/assets/10"]
			checksumReads := fixture.requests["/repos/matcra587/jira-cli/releases/assets/11"]
			fixture.mu.Unlock()
			if latestReads != 1 || (mode == "success" || mode == "changed" || mode == "integrity") && (archiveReads != 1 || checksumReads != 1) || (mode == "preview" || mode == "current" || mode == "unavailable" || mode == "canceled") && (archiveReads != 0 || checksumReads != 0) {
				t.Fatalf("release was replayed or preview fetched assets: latest=%d archive=%d checksum=%d", latestReads, archiveReads, checksumReads)
			}
			entries, err := os.ReadDir(downloadDir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("candidate cleanup left files: %v err=%v", entries, err)
			}
			t.Logf("verified native command %s: current=%s latest=%s attempted=%t updated=%t", mode, current, out.Latest, out.Attempted, out.Updated)
		})
	}
}

func TestNativeHomebrewUpdateCommand(t *testing.T) {
	original, candidate := nativeUpdateBinaries(t)
	helper := os.Getenv("JIRA_NATIVE_UPDATE_BREW_HELPER")
	if helper == "" {
		t.Skip("requires the externally compiled selfupdate test helper executable")
	}
	for _, mode := range []string{"success", "failure", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			oldKeg := filepath.Join(root, "Cellar", "jira", "1.0.0")
			for _, dir := range []string{filepath.Join(oldKeg, "bin"), filepath.Join(root, "bin"), filepath.Join(root, "opt")} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			bin, shadow := filepath.Join(oldKeg, "bin", "jira"), filepath.Join(root, "bin", "jira")
			for _, pair := range [][2]string{{original, bin}, {original, shadow}, {helper, filepath.Join(root, "bin", "brew")}} {
				if err := xos.CopyFile(pair[0], pair[1]); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(pair[1], 0o700); err != nil { //nolint:gosec // owned native executables
					t.Fatal(err)
				}
			}
			if err := os.Symlink(oldKeg, filepath.Join(root, "opt", "jira")); err != nil {
				t.Fatal(err)
			}
			fixture := nativeReleaseServer(t, candidate)
			helperMode := mode
			if mode == "canceled" {
				helperMode = "deadline"
			}
			env := append(slices.Clone(fixture.env), "PATH="+filepath.Join(root, "bin"), "JIRA_OWNED_BREW_HELPER=1", "JIRA_OWNED_BREW_ROOT="+root,
				"JIRA_OWNED_BREW_CANDIDATE="+os.Getenv("JIRA_NATIVE_UPDATE_CANDIDATE"), "JIRA_OWNED_BREW_OUTCOME="+helperMode)
			before, err := os.ReadFile(bin)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			signalResult := make(chan error, 1)
			onStart := func(process *os.Process) {
				if mode != "canceled" {
					return
				}
				go func() {
					ticker := time.NewTicker(10 * time.Millisecond)
					defer ticker.Stop()
					deadline := time.NewTimer(5 * time.Second)
					defer deadline.Stop()
					for {
						select {
						case <-ctx.Done():
							signalResult <- ctx.Err()
							return
						case <-deadline.C:
							signalResult <- fmt.Errorf("owned keg was not published before signaling")
							return
						case <-ticker.C:
							installed, err := filepath.EvalSymlinks(filepath.Join(root, "opt", "jira"))
							if err == nil && filepath.Base(installed) == "1.2.3" {
								signalResult <- process.Signal(os.Interrupt)
								return
							}
						}
					}
				}()
			}
			stdout, stderr, exit := runExecutableIOInDirStarted(t, bin, root, env, nil, nil, onStart, "update", "--force", "--output=json")
			cancel()
			code, wantExit := "", 0
			if mode == "failure" {
				code, wantExit = "update_failed", 5
				if !strings.Contains(stderr, "owned failure after publication") {
					t.Fatal("public command dropped installer failure after publication")
				}
			}
			if mode == "canceled" {
				if err := <-signalResult; err != nil {
					t.Fatal(err)
				}
				code, wantExit = "canceled", 6
			}
			if exit != wantExit {
				t.Fatalf("native brew %s exit%d want%d stderr=%s", mode, exit, wantExit, stderr)
			}
			out := nativeUpdateFrame(t, stdout, exit, code)
			if out.Channel != "brew" || !out.Attempted || !out.Updated || out.Uncertain || out.Publication != nil || out.Brew == nil || out.Brew.After == nil || out.Brew.Shadow != shadow || out.Brew.Before.Executable != bin || out.Brew.Before.Version != "v1.0.0" || out.Brew.After.Version != "v1.2.3" || out.Brew.After.SHA256 != fmt.Sprintf("%x", sha256.Sum256(candidate)) {
				t.Fatalf("public brew receipt lost observed failure/publication: %+v", out)
			}
			installed := filepath.Join(root, "opt", "jira", "bin", "jira")
			if nativeVersion(t, installed, root, env) != "v1.2.3" {
				t.Fatal("new active keg version differs from receipt")
			}
			for _, retained := range []string{bin, shadow} {
				data, err := os.ReadFile(retained)
				if err != nil || !bytes.Equal(data, before) || nativeVersion(t, retained, root, env) != "v1.0.0" {
					t.Fatalf("old keg or shadow changed: %v", err)
				}
			}
			t.Logf("verified native command brew %s: attempted=%t updated=%t installed=%s", mode, out.Attempted, out.Updated, out.Brew.After.Version)
		})
	}
}

func TestNativeUpdateNotificationTerminalPolicy(t *testing.T) {
	original, _ := nativeUpdateBinaries(t)
	for _, tc := range []struct {
		name, mode string
		extra      []string
		agent      bool
		hint       bool
	}{
		{name: "human", mode: "human", hint: true},
		{name: "json", mode: "json"},
		{name: "compact", mode: "compact"},
		{name: "headless human", mode: "human", extra: []string{"--no-input"}},
		{name: "agent human", mode: "human", agent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			cache := filepath.Join(root, "cache")
			stamps := filepath.Join(cache, "jira", "last-update")
			if err := os.MkdirAll(stamps, 0o700); err != nil {
				t.Fatal(err)
			}
			const cached = `{"version":1,"track":"stable","latest":"v1.2.3"}`
			if err := os.WriteFile(filepath.Join(stamps, "check-stable"), []byte(cached), 0o600); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet || request.URL.Path != "/rest/api/3/myself" {
					t.Errorf("unexpected notification workflow request: %s %s", request.Method, request.URL.Path)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, `{"accountId":"owned","displayName":"Owned fixture"}`)
			}))
			t.Cleanup(server.Close)
			env := append(editReceiptEnv(server.URL), "XDG_CACHE_HOME="+cache, "AGENT=0", "JIRA_NO_UPDATE_CHECK=")
			if tc.agent {
				env = append(env, "AGENT=1")
			}
			args := append([]string{"api", "get", "/rest/api/3/myself", "--output=" + tc.mode, "--color=never"}, tc.extra...)
			master, slave := editorPTY(t)
			stdout, _, exit := runExecutableIOInDirStarted(t, original, root, env, nil, slave, nil, args...)
			if err := slave.Close(); err != nil {
				t.Fatal(err)
			}
			diagnostics, err := io.ReadAll(master)
			if err != nil && !errors.Is(err, unix.EIO) {
				t.Fatal(err)
			}
			if exit != 0 || strings.Contains(string(diagnostics), "is outdated") != tc.hint {
				t.Fatalf("terminal notification policy: exit%d hint=%t stderr=%q", exit, tc.hint, diagnostics)
			}
			if tc.hint && !strings.Contains(string(diagnostics), "jira update") {
				t.Fatal("notification recommended a missing update command")
			}
			if bytes.ContainsAny(diagnostics, "\x1b\a") {
				t.Fatal("notification ignored --color=never or leaked terminal controls")
			}
			if tc.mode != "human" {
				apiData(t, stdout, tc.mode, true, "api.get")
			}
			_, err = os.Stat(filepath.Join(stamps, "notify-stable"))
			if tc.hint && err != nil || !tc.hint && !os.IsNotExist(err) {
				t.Fatalf("notification stamp disagrees with printed hint: %v", err)
			}
			t.Logf("verified terminal notification %s: printed=%t", tc.name, tc.hint)
		})
	}
}
