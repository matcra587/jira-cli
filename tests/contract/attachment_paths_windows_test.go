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
	"github.com/matcra587/jira-cli/internal/testfixture"
)

func TestNativePublicWindowsAttachmentDirectories(t *testing.T) {
	for _, mode := range []string{"json", "compact", "human"} {
		for _, selection := range []string{"junction", "extended", "dot", "space"} {
			t.Run(mode+"/"+selection, func(t *testing.T) {
				parent := t.TempDir()
				directory := filepath.Join(parent, "downloads 古い ~$")
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				selected, actual := directory, directory
				verify := func() {}
				switch selection {
				case "junction":
					link := filepath.Join(parent, "selected 'é")
					before := testfixture.Junction(t, link, directory)
					selected = link
					verify = func() { testfixture.SameJunction(t, link, directory, before) }
				case "extended":
					selected = `\\?\` + directory
				case "dot", "space":
					suffix := "."
					if selection == "space" {
						suffix = " "
					}
					selected = `\\?\` + directory + suffix
					actual = selected
					if err := os.Mkdir(actual, 0o700); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := os.RemoveAll(actual); err != nil {
							t.Error(err)
						}
					})
					const decoy = "ordinary sibling must remain untouched\n"
					marker := filepath.Join(directory, "report.bin")
					if err := os.WriteFile(marker, []byte(decoy), 0o600); err != nil {
						t.Fatal(err)
					}
					verify = func() {
						contents, err := os.ReadFile(marker)
						entries, readErr := os.ReadDir(directory)
						if err != nil || string(contents) != decoy || readErr != nil || len(entries) != 1 {
							t.Fatalf("download changed ordinary sibling: content=%q error=%v entries=%v read=%v", contents, err, entries, readErr)
						}
					}
				}
				content := bytes.Repeat([]byte{0, 0xff, '<', '&', '>', '\n'}, 1024)
				const replaced = "authored bytes to replace\n"
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					request := requests.Add(1)
					user, password, authenticated := r.BasicAuth()
					if r.Method != http.MethodGet || r.URL.Path != "/rest/api/3/attachment/content/10042" || r.URL.RawQuery != "" || !authenticated || user != "reader@example.test" || password != "synthetic-token" {
						t.Error("download made an unexpected request or changed its credential binding")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if request == 2 {
						data, err := os.ReadFile(filepath.Join(actual, "report.bin"))
						if err != nil || string(data) != replaced {
							t.Error("overwrite changed its target before receiving complete bytes")
						}
					}
					w.Header().Set("Content-Type", "application/octet-stream")
					w.Header().Set("Content-Length", strconv.Itoa(len(content)))
					_, _ = w.Write(content)
				}))
				t.Cleanup(server.Close)
				env := binaryDraftEnv(t, server.URL)
				path := filepath.Join(selected, "report.bin")
				invoke := func(preview, overwrite bool, wantExit int) {
					t.Helper()
					args := []string{"issue", "attachment", "download", "10042", "--to", selected, "--as=report.bin", "--output=" + mode, "--no-input"}
					if preview {
						args = append(args, "--dry-run")
					}
					if overwrite {
						args = append(args, "--overwrite")
					}
					stdout, stderr, exit := runBinaryEnv(t, env, args...)
					if exit != wantExit || (exit == 0 && stderr != "") {
						t.Fatalf("attachment invocation failed: preview=%v overwrite=%v exit=%d want=%d stdout=%q stderr=%q", preview, overwrite, exit, wantExit, stdout, stderr)
					}
					if wantExit != 0 {
						if !strings.Contains(stdout+stderr, "already exists") || !strings.Contains(stdout+stderr, "--overwrite") {
							t.Fatalf("collision lost its destination refusal: stdout=%q stderr=%q", stdout, stderr)
						}
						return
					}
					if mode == "human" {
						if !strings.Contains(stdout, path) {
							t.Fatalf("download lost selected human destination: %q", stdout)
						}
					} else {
						var got envelope.IssueAttachmentDownloadOutput
						decodeWindowsJunctionOutput(t, mode, stdout, "issue.attachment.download", &got)
						if got.Directory != selected || got.Path != path || got.Filename != "report.bin" || !got.FilenameResolved || got.DryRun != preview || (!preview && (got.BytesWritten == nil || *got.BytesWritten != int64(len(content)) || got.Overwrote == nil || *got.Overwrote != overwrite)) {
							t.Fatalf("download lost literal paths or publication receipt: %+v", got)
						}
					}
				}
				invoke(true, false, 0)
				if entries, err := os.ReadDir(actual); err != nil || len(entries) != 0 || requests.Load() != 0 {
					t.Fatalf("local download preview published or requested bytes: entries=%v error=%v requests=%d", entries, err, requests.Load())
				}
				invoke(false, false, 0)
				if data, err := os.ReadFile(filepath.Join(actual, "report.bin")); err != nil || !bytes.Equal(data, content) || requests.Load() != 1 {
					t.Fatalf("download bytes landed outside selected directory: error=%v requests=%d", err, requests.Load())
				}
				invoke(true, false, cli.ExitValidation)
				if requests.Load() != 1 {
					t.Fatal("collision preview contacted Jira")
				}
				if err := os.WriteFile(filepath.Join(actual, "report.bin"), []byte(replaced), 0o600); err != nil {
					t.Fatal(err)
				}
				invoke(false, true, 0)
				entries, err := os.ReadDir(actual)
				data, readErr := os.ReadFile(filepath.Join(actual, "report.bin"))
				if err != nil || len(entries) != 1 || entries[0].Name() != "report.bin" || readErr != nil || !bytes.Equal(data, content) || requests.Load() != 2 {
					t.Fatalf("overwrite left partial/staging files or changed bytes: entries=%v error=%v read=%v requests=%d", entries, err, readErr, requests.Load())
				}
				verify()
			})
		}
	}
}
