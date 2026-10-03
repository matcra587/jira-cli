package jira

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/matcra587/jira-cli/internal/core"
)

func TestCloneSourcePreservesOriginalReadFailures(t *testing.T) {
	t.Parallel()
	for name, prefix := range map[string]string{
		"initial": "", "trailing": `{"key":"KEY-1","fields":{"summary":"Source"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cause := errors.New("native full issue stream failed")
			issue, err := decodeCloneIssue(io.MultiReader(strings.NewReader(prefix), commentFailureReader{cause: cause}))
			if issue != nil || !errors.Is(err, cause) || !errors.Is(err, core.ErrIssueCloneSourceInvalid) {
				t.Fatal("clone source hid its reader failure or published a partial observation")
			}
		})
	}
}
