//go:build linux

package contract

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenIssueLinuxProcessReceivesOnlyValidatedURL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	opener := filepath.Join(dir, "browser helper")
	observed := filepath.Join(dir, "observed-argv")
	// The helper records argv without evaluating it. It substitutes a browser
	// process and proves handoff only; no GUI or native page rendering is tested.
	if err := os.WriteFile(opener, []byte("#!/bin/sh\nprintf '%s\\000' \"$@\" > \"$OPEN_TEST_OBSERVED.partial\"\nmv -- \"$OPEN_TEST_OBSERVED.partial\" \"$OPEN_TEST_OBSERVED\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{"JIRA_BASE_URL=https://example.invalid/jira", "JIRA_API_TOKEN=", "JIRA_EMAIL=", "JIRA_KEYRING=true", "DBUS_SESSION_BUS_ADDRESS=/dev/null", "BROWSER=" + opener, "OPEN_TEST_OBSERVED=" + observed}
	for _, mode := range []string{"json", "compact", "agent"} {
		args := []string{"open", "KEY-1", "--no-input"}
		selectedEnv := append([]string{}, env...)
		if mode == "agent" {
			selectedEnv = append(selectedEnv, "AGENT=1")
		} else {
			args = append(args, "--output="+mode)
		}
		stdout, stderr, exit := runBinaryEnv(t, selectedEnv, args...)
		if exit != 0 || stderr != "" || !strings.Contains(stdout, `"opened":false`) {
			t.Fatalf("machine route fabricated handoff: exit=%d out=%q err=%q", exit, stdout, stderr)
		}
		if _, err := os.Stat(observed); !os.IsNotExist(err) {
			t.Fatal("machine route started the real browser helper")
		}
	}
	stdout, stderr, exit := runBinaryEnv(t, env, "open", "KEY-007", "--output=human", "--no-input")
	if exit != 0 || stderr != "" || !strings.Contains(stdout, "https://example.invalid/jira/browse/KEY-7") {
		t.Fatalf("human handoff failed: exit=%d out=%q err=%q", exit, stdout, stderr)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := os.ReadFile(observed)
		if err == nil {
			if !bytes.Equal(got, []byte("https://example.invalid/jira/browse/KEY-7\x00")) {
				t.Fatalf("browser helper received extra or changed arguments: %q", got)
			}
			break
		}
		if !os.IsNotExist(err) || time.Now().After(deadline) {
			t.Fatalf("browser helper produced no argv receipt: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
