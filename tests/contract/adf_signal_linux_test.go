//go:build linux

package contract

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestInputSIGINTInterruptsInheritedPipeAndRestoresFlags(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		arguments []string
	}{
		{"adf convert", []string{"adf", "convert", "--input=-"}},
		{"adf render", []string{"adf", "render", "--input=-"}},
		{"raw payload", []string{"api", "post", "/rest/api/3/search/jql", "--force", "--input=-"}},
		{"issue payload", []string{"issue", "create", "--json-input=-", "--dry-run"}},
		{"issue description", []string{"issue", "edit", "TEST-1", "--markdown-file=-", "--dry-run"}},
		{"comment body", []string{"issue", "comment", "add", "TEST-1", "--markdown-file=-", "--dry-run"}},
		{"clone overrides", []string{"issue", "clone", "TEST-1", "--json-input=-", "--dry-run"}},
		{"link payload", []string{"issue", "link", "create", "TEST-1", "--to=TEST-2", "--type=Blocks", "--json-input=-", "--dry-run"}},
		{"file selector", []string{"issue", "delete", "--from-file=-", "--dry-run"}},
		{"alias import", []string{"alias", "import", "-", "--dry-run"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reader.Close(); _ = writer.Close() }()
			// Exporting Fd recreates inherited blocking stdin. The child shares
			// this open-file description, so its flag restoration is observable
			// here even after it exits. No fake Reader can prove this behavior.
			readerDescriptor, writerDescriptor := reader.Fd(), writer.Fd()
			before, err := unix.FcntlInt(readerDescriptor, unix.F_GETFL, 0)
			if err != nil || before&unix.O_NONBLOCK != 0 {
				t.Fatal("fixture did not establish inherited blocking input")
			}
			if _, err := writer.WriteString("partial input remains open"); err != nil {
				t.Fatal(err)
			}
			var emergencyKill *time.Timer
			defer func() {
				if emergencyKill != nil {
					emergencyKill.Stop()
				}
			}()
			hook := func(process *os.Process) {
				deadline := time.Now().Add(3 * time.Second)
				for {
					pending, err := unix.IoctlGetInt(int(writerDescriptor), unix.TIOCINQ)
					if err != nil {
						t.Fatal(err)
					}
					if pending == 0 {
						break // the actual child has consumed the queued bytes
					}
					if time.Now().After(deadline) {
						t.Fatal("child did not consume its input before cancellation")
					}
					time.Sleep(time.Millisecond)
				}
				if err := process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
				emergencyKill = time.AfterFunc(2*time.Second, func() { _ = process.Kill() })
			}
			arguments := append(tc.arguments, "--output=json")
			stdout, _, exit := runBinaryReaderStarted(t, []string{"JIRA_KEYRING=false"}, reader, hook, arguments...)
			var f frame
			var failure struct{ Code string }
			if exit != 6 || json.Unmarshal([]byte(stdout), &f) != nil || f.OK || len(f.Errors) != 1 || json.Unmarshal(f.Errors[0], &failure) != nil || failure.Code != "canceled" {
				t.Fatalf("SIGINT did not interrupt inherited input while its writer remained open: exit=%d stdout=%q", exit, stdout)
			}
			after, err := unix.FcntlInt(readerDescriptor, unix.F_GETFL, 0)
			if err != nil || after != before {
				t.Fatal("input cancellation changed the borrowed descriptor's flags")
			}
		})
	}
}
