package config

import (
	"context"
	"errors"
	"testing"

	"github.com/matcra587/jira-cli/internal/cli"
)

func TestConfigLocalIOPreservesContextCauses(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		err := localIO("read config", cause)
		if !errors.Is(err, cause) {
			t.Fatalf("local config operation erased its context cause: %v", err)
		}
		want := cli.CodeCanceled
		if errors.Is(cause, context.DeadlineExceeded) {
			want = "timeout"
		}
		if classified := cli.MapError(err); classified.Code != want {
			t.Fatalf("local config cancellation classified as storage failure: %+v", classified)
		}
	}
}
