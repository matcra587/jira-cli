//go:build flagmatrix

package matrix

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransitionListParallelismControlsReadsAndRefusesInvalidWidth(t *testing.T) {
	t.Parallel()
	var active, peak atomic.Int32
	recorder := &mx3Recorder{}
	url := mx3Stub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder.record(r)
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/transitions") || r.URL.Query().Get("expand") != "transitions.fields" {
			t.Error("discovery changed method, scope or native expansion")
		}
		current := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); current > old; old = peak.Load() {
			if peak.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte(`{"transitions":[]}`))
	}))
	res := mx3Run(t, t.TempDir(), url, "issue", "transition", "list", "KEY-1", "KEY-2", "KEY-3", "KEY-4", "--parallelism=1", "--output=json")
	if res.Exit != 0 || peak.Load() != 1 || len(recorder.byPath("/rest/api/3/issue/KEY-1/transitions")) != 1 || len(recorder.byPath("/rest/api/3/issue/KEY-4/transitions")) != 1 {
		t.Fatal("--parallelism=1 did not serialize successful native discovery")
	}
	noRequests := &mx3Recorder{}
	url = mx3Stub(t, mx3RecordOnly(noRequests))
	res = mx3Run(t, t.TempDir(), url, "issue", "transition", "list", "KEY-1", "--parallelism=99", "--output=json")
	mx3RequireRefusal(t, res, "flag_value_invalid", "parallelism")
	mx3RequireNoRequests(t, noRequests)
	Prove(t, "jira issue transition list", "parallelism", "four native reads ran with peak concurrency one; an out-of-range width was refused before any request")
}
