//go:build linux

package contract

import (
	"testing"

	"github.com/matcra587/jira-cli/tests/internal/releaseacceptance"
)

func TestReleaseNotesNativeTerminalPagingAndHeadlessPolicy(t *testing.T) {
	t.Parallel()
	releaseacceptance.VerifyPaging(t, jiraBinary(t))
}
