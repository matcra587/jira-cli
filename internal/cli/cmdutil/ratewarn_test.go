package cmdutil

import (
	"strings"
	"sync"
	"testing"

	"github.com/matcra587/jira-cli/internal/jira"
	"github.com/spf13/cobra"
)

// cmdWithRateSink returns a command whose context carries a fresh rate-warn
// sink, mirroring what PersistentPreRunE installs in production.
func cmdWithRateSink(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "root"}
	cmd.SetContext(WithRateWarnSink(t.Context()))
	return cmd
}

func TestRecordRateNearLimitTripsSinkOnlyWhenNear(t *testing.T) {
	cmd := cmdWithRateSink(t)
	ctx := cmd.Context()

	// A response that is not near the limit records nothing.
	RecordRateNearLimit(ctx, jira.Rate{NearLimit: false, Remaining: 500})
	if w := collectedRateWarnings(cmd); len(w) != 0 {
		t.Fatalf("a non-near response must not warn, got %+v", w)
	}

	// A near-limit response trips the sink once; a second near-limit response
	// (a storm) must not add a second warning.
	RecordRateNearLimit(ctx, jira.Rate{NearLimit: true, Reason: "jira-burst-based"})
	RecordRateNearLimit(ctx, jira.Rate{NearLimit: true, Reason: "jira-burst-based"})
	w := collectedRateWarnings(cmd)
	if len(w) != 1 {
		t.Fatalf("near-limit storm must dedup to one warning, got %d", len(w))
	}
	if w[0].Type != "rate_limit_near" {
		t.Fatalf("warning type = %q, want rate_limit_near", w[0].Type)
	}
	if !strings.Contains(w[0].Message, "jira-burst-based") {
		t.Fatalf("warning should name the reason, got %q", w[0].Message)
	}
}

func TestRecordRateNearLimitConcurrentTripDedupsToOne(t *testing.T) {
	// Under -p fan-out, many request goroutines trip the same sink at once.
	// Run with -race: this fails if trip()/collected() drop their mutex.
	cmd := cmdWithRateSink(t)
	ctx := cmd.Context()
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			RecordRateNearLimit(ctx, jira.Rate{NearLimit: true, Reason: "jira-burst-based"})
		})
	}
	wg.Wait()
	if w := collectedRateWarnings(cmd); len(w) != 1 {
		t.Fatalf("concurrent near-limit trips must dedup to one warning, got %d", len(w))
	}
}

func TestRecordRateNearLimitNoSinkIsNoop(t *testing.T) {
	// No sink installed: RecordRateNearLimit must not panic, and a command
	// without a sink yields no warnings.
	cmd := &cobra.Command{Use: "root"}
	cmd.SetContext(t.Context())
	RecordRateNearLimit(cmd.Context(), jira.Rate{NearLimit: true})
	if w := collectedRateWarnings(cmd); w != nil {
		t.Fatalf("no sink should yield no warnings, got %+v", w)
	}
}

func TestCollectedCommandWarningsFoldsRateNotice(t *testing.T) {
	cmd := cmdWithRateSink(t)
	RecordRateNearLimit(cmd.Context(), jira.Rate{NearLimit: true})
	all := collectedCommandWarnings(cmd)
	if len(all) != 1 || all[0].Type != "rate_limit_near" {
		t.Fatalf("combined collector should carry the rate notice, got %+v", all)
	}
}
