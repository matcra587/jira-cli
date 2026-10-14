//go:build flagmatrix

package matrix

import (
	"testing"

	"github.com/matcra587/jira-cli/tests/internal/releaseacceptance"
)

func TestReleaseNotesNoPagerControlsActualTerminalPublication(t *testing.T) {
	t.Parallel()
	releaseacceptance.VerifyPaging(t, mx4Binary(t))
	Prove(t, "jira release-notes", "no-pager", "native Windows consoles prove exact configured-pager stdin, complete no-pager display, headless policy, builtin navigation and restored modes after Control-C/Control-Break")
}

func TestReleaseNotesWindowsProcess(t *testing.T) {
	releaseacceptance.RunWindowsProcess(t)
}
