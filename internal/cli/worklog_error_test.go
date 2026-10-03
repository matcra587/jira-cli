package cli

import (
	"fmt"
	"strings"
	"testing"

	adapterjira "github.com/matcra587/jira-cli/internal/adapters/jira"
)

func TestWorklogResponseErrorUsesServerExitAndInspectionHint(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("created receipt: %w", adapterjira.ErrWorklogResponseInvalid)
	class := MapError(err)
	if class.Code != CodeWorklogResponseInvalid || class.Exit != ExitServer || ExitCode(err) != ExitServer || !strings.Contains(class.Hint, "before retrying") || class.Message != err.Error() {
		t.Fatalf("worklog protocol failure misclassified: %+v", class)
	}
}
