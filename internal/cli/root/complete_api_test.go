package root

import (
	"context"
	"strings"
	"testing"
)

func TestAPIEndpointCompletionIsMethodBoundAndCancelable(t *testing.T) {
	t.Parallel()
	get := completionKinds["apiendpoint_get"](t.Context(), "bash", nil)
	post := completionKinds["apiendpoint_post"](t.Context(), "bash", nil)
	hasPath := func(values []string, path string) bool {
		for _, value := range values {
			candidate, _, _ := strings.Cut(value, "\t")
			if candidate == path {
				return true
			}
		}
		return false
	}
	if !hasPath(get, "/rest/api/3/myself") || hasPath(post, "/rest/api/3/myself") || !hasPath(get, "/rest/agile/1.0/board") {
		t.Fatal("completion lost method selection or Agile coverage")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if values := completionKinds["apiendpoint_get"](ctx, "bash", nil); len(values) != 0 {
		t.Fatal("completion ignored cancellation")
	}
}
