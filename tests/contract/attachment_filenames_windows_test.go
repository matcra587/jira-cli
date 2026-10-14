//go:build windows && nativefilesystem

package contract

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
	"github.com/matcra587/jira-cli/internal/envelope"
)

func TestNativePublicWindowsAttachmentFilenames(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human"} {
		for _, extended := range []bool{true, false} {
			for _, filename := range []string{"report.bin", "report.bin.", "report.bin "} {
				t.Run(mode+"/extended="+strconv.FormatBool(extended)+"/"+strconv.Quote(filename), func(t *testing.T) {
					nativeDirectory := `\\?\` + t.TempDir()
					directory := nativeDirectory
					if !extended {
						directory = strings.TrimPrefix(directory, `\\?\`)
					}
					t.Cleanup(func() {
						if err := os.RemoveAll(nativeDirectory); err != nil {
							t.Error(err)
						}
					})
					const sibling = "ordinary sibling must remain unchanged\n"
					if filename != "report.bin" {
						if err := os.WriteFile(filepath.Join(directory, "report.bin"), []byte(sibling), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					path := filepath.Join(nativeDirectory, filename)
					wantPath := filepath.Join(directory, filename)
					if filename != "report.bin" && !extended {
						wantPath = `\\?\` + wantPath
					}
					content := bytes.Repeat([]byte{0, 0xff, '<', '&', '>', '\n'}, 1024)
					const previous = "authored bytes before overwrite\n"
					var requests atomic.Int32
					var incomplete atomic.Bool
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests.Add(1)
						user, password, ok := r.BasicAuth()
						if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/attachment/content/10042" || r.URL.RawQuery != "" || !ok || user != "reader@example.test" || password != "synthetic-token" {
							t.Error("download changed its request or credential binding")
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						w.Header().Set("Content-Type", "application/octet-stream")
						w.Header().Set("Content-Length", strconv.Itoa(len(content)))
						if incomplete.Load() {
							_, _ = w.Write(content[:11])
							return
						}
						_, _ = w.Write(content)
					}))
					t.Cleanup(server.Close)
					env := binaryDraftEnv(t, server.URL)
					invoke := func(preview, overwrite bool, wantExit int) {
						t.Helper()
						args := []string{"issue", "attachment", "download", "10042", "--to", directory, "--as=" + filename, "--output=" + mode, "--no-input"}
						if preview {
							args = append(args, "--dry-run")
						}
						if overwrite {
							args = append(args, "--overwrite")
						}
						stdout, stderr, exit := runBinaryEnv(t, env, args...)
						if exit != wantExit || (exit == 0 && stderr != "") {
							t.Fatalf("filename invocation failed: preview=%v overwrite=%v exit=%d want=%d stdout=%q stderr=%q", preview, overwrite, exit, wantExit, stdout, stderr)
						}
						if wantExit != 0 {
							return
						}
						if mode == "human" {
							if !strings.Contains(stdout, wantPath) {
								t.Fatal("download lost its literal human path", stdout)
							}
							return
						}
						var got envelope.IssueAttachmentDownloadOutput
						decodeWindowsJunctionOutput(t, mode, stdout, "issue.attachment.download", &got)
						if got.Directory != directory || got.Path != wantPath || got.Filename != filename || !got.FilenameResolved || got.DryRun != preview || (!preview && (got.BytesWritten == nil || *got.BytesWritten != int64(len(content)) || got.Overwrote == nil || *got.Overwrote != overwrite)) {
							t.Fatalf("download changed filename identity or receipt: %+v", got)
						}
					}
					check := func(want []byte, count int32) {
						t.Helper()
						data, err := os.ReadFile(path)
						entries, directoryErr := os.ReadDir(directory)
						wantEntries := 1
						if filename != "report.bin" {
							wantEntries++
							ordinary, ordinaryErr := os.ReadFile(filepath.Join(directory, "report.bin"))
							if ordinaryErr != nil || string(ordinary) != sibling {
								t.Fatalf("download changed ordinary sibling: %q %v", ordinary, ordinaryErr)
							}
						}
						if err != nil || !bytes.Equal(data, want) || directoryErr != nil || len(entries) != wantEntries || requests.Load() != count {
							t.Fatalf("download selected wrong entry, left staging or changed bytes: read=%v entries=%v directory=%v requests=%d want=%d", err, entries, directoryErr, requests.Load(), count)
						}
					}
					invoke(true, false, 0)
					if _, err := os.Lstat(path); !os.IsNotExist(err) || requests.Load() != 0 {
						t.Fatalf("preview changed selected literal entry or contacted Jira: %v requests=%d", err, requests.Load())
					}
					incomplete.Store(true)
					invoke(false, false, cli.ExitValidation)
					if _, err := os.Lstat(path); !os.IsNotExist(err) || requests.Load() != 1 {
						t.Fatalf("incomplete create retained a partial literal entry: %v requests=%d", err, requests.Load())
					}
					incomplete.Store(false)
					invoke(false, false, 0)
					check(content, 2)
					invoke(true, false, cli.ExitValidation)
					check(content, 2)
					if err := os.WriteFile(path, []byte(previous), 0o600); err != nil {
						t.Fatal(err)
					}
					incomplete.Store(true)
					invoke(false, true, cli.ExitValidation)
					check([]byte(previous), 3)
					incomplete.Store(false)
					invoke(false, true, 0)
					check(content, 4)
				})
			}
		}
	}
}

func TestNativePublicWindowsAttachmentLiteralOverwriteDoesNotReplaceSibling(t *testing.T) {
	for _, filename := range []string{"report.bin.", "report.bin "} {
		t.Run(strconv.Quote(filename), func(t *testing.T) {
			directory := `\\?\` + t.TempDir()
			ordinary := filepath.Join(directory, "report.bin")
			literal := filepath.Join(directory, filename)
			const sibling = "ordinary sibling\n"
			const original = "literal file before overwrite\n"
			const replacement = "downloaded replacement\n"
			for path, value := range map[string]string{ordinary: sibling, literal: original} {
				if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/attachment/content/10042" {
					t.Error("unexpected attachment request")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_, _ = w.Write([]byte(replacement))
			}))
			t.Cleanup(server.Close)
			stdout, stderr, exit := runBinaryEnv(t, binaryDraftEnv(t, server.URL), "issue", "attachment", "download", "10042", "--to", directory, "--as="+filename, "--overwrite", "--output=json", "--no-input")
			if exit != 0 || stderr != "" {
				t.Fatalf("overwrite invocation failed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			untouched, ordinaryErr := os.ReadFile(ordinary)
			selected, literalErr := os.ReadFile(literal)
			if ordinaryErr != nil || string(untouched) != sibling || literalErr != nil || string(selected) != replacement || requests.Load() != 1 {
				t.Fatalf("overwrite replaced the ordinary sibling instead of its literal filename: ordinary=%q error=%v literal=%q error=%v requests=%d", untouched, ordinaryErr, selected, literalErr, requests.Load())
			}
		})
	}
}

func TestNativePublicWindowsAttachmentMetadataFilenames(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human"} {
		for _, filename := range []string{"remote.bin.", "remote.bin "} {
			t.Run(mode+"/"+strconv.Quote(filename), func(t *testing.T) {
				directory := t.TempDir()
				nativeDirectory := `\\?\` + directory
				t.Cleanup(func() {
					if err := os.RemoveAll(nativeDirectory); err != nil {
						t.Error(err)
					}
				})
				const sibling = "ordinary remote sibling\n"
				if err := os.WriteFile(filepath.Join(directory, "remote.bin"), []byte(sibling), 0o600); err != nil {
					t.Fatal(err)
				}
				const contents = "native attachment payload\n"
				var metadata, content, total atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					total.Add(1)
					user, password, ok := r.BasicAuth()
					if r.Method != http.MethodGet || r.URL.RawQuery != "" || !ok || user != "reader@example.test" || password != "synthetic-token" {
						t.Error("metadata download changed its request or credential binding")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					switch r.URL.Path {
					case "/rest/api/3/attachment/10042":
						metadata.Add(1)
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write([]byte(`{"id":10042,"filename":` + strconv.Quote(filename) + `,"size":26}`))
					case "/rest/api/3/attachment/content/10042":
						content.Add(1)
						_, _ = w.Write([]byte(contents))
					default:
						t.Error("metadata download made an unexpected request")
						w.WriteHeader(http.StatusBadRequest)
					}
				}))
				t.Cleanup(server.Close)
				env := binaryDraftEnv(t, server.URL)
				path := filepath.Join(nativeDirectory, filename)
				for _, preview := range []bool{true, false} {
					args := []string{"issue", "attachment", "download", "10042", "--to", directory, "--output=" + mode, "--no-input"}
					if preview {
						args = append(args, "--dry-run", "--validate-remote")
					}
					stdout, stderr, exit := runBinaryEnv(t, env, args...)
					if exit != 0 || stderr != "" {
						t.Fatalf("metadata filename invocation failed: preview=%v exit=%d stdout=%q stderr=%q", preview, exit, stdout, stderr)
					}
					if mode == "human" {
						if !strings.Contains(stdout, path) {
							t.Fatal("metadata download lost literal human destination", stdout)
						}
					} else {
						var got envelope.IssueAttachmentDownloadOutput
						decodeWindowsJunctionOutput(t, mode, stdout, "issue.attachment.download", &got)
						if got.Directory != directory || got.Path != path || got.Filename != filename || !got.FilenameResolved || got.DryRun != preview {
							t.Fatalf("metadata download changed literal destination: %+v", got)
						}
					}
					if preview {
						if _, err := os.Lstat(path); !os.IsNotExist(err) || metadata.Load() != 1 || content.Load() != 0 || total.Load() != 1 {
							t.Fatalf("remote preview wrote or downloaded content: %v metadata=%d content=%d total=%d", err, metadata.Load(), content.Load(), total.Load())
						}
					}
				}
				data, err := os.ReadFile(path)
				ordinary, ordinaryErr := os.ReadFile(filepath.Join(directory, "remote.bin"))
				if err != nil || string(data) != contents || ordinaryErr != nil || string(ordinary) != sibling || metadata.Load() != 2 || content.Load() != 1 || total.Load() != 3 {
					t.Fatalf("metadata download changed sibling or requested unexpected resources: read=%v ordinary=%q error=%v metadata=%d content=%d total=%d", err, ordinary, ordinaryErr, metadata.Load(), content.Load(), total.Load())
				}
			})
		}
	}
}
