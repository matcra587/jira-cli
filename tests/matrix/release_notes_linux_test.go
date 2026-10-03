//go:build flagmatrix && linux

package matrix

import (
	"testing"

	"github.com/matcra587/jira-cli/tests/internal/releaseacceptance"
)

func TestReleaseNotesNoPagerControlsActualTerminalPublication(t *testing.T) {
	t.Parallel()
	releaseacceptance.VerifyPaging(t, mx4Binary(t))
	Prove(t, "jira release-notes", "no-pager", "actual terminals prove default paging receives exact full Markdown; --no-pager streams the same bytes without launching the configured pager, alongside headless and built-in lifecycle guards")
}
