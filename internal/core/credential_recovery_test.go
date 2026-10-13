package core_test

import (
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestCredentialBackupIDsStayInTheirDedicatedNamespace(t *testing.T) {
	first, second := core.NewCredentialBackupID(), core.NewCredentialBackupID()
	if first == second || !core.ValidCredentialBackupID(first) || !core.ValidCredentialBackupID(second) {
		t.Fatal("backup identity was reused or invalid")
	}
	for _, name := range []string{"", "default", "host.invalid/default", "jira-cli-recovery-default", "jira-cli-recovery-00000000-0000-0000-0000-000000000000"} {
		if core.ValidCredentialBackupID(name) {
			t.Fatal("ordinary credential slot became a backup namespace")
		}
	}
}
