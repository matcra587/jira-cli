package jira

import (
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // Keep the IANA DST fixture available on native Windows.
)

func TestWorklogStartedNormalizesAcceptedForms(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("UTC-4", -4*int(time.Hour/time.Second))
	now := time.Date(2026, 6, 26, 15, 30, 45, 123456789, loc)
	for _, tc := range []struct{ input, want string }{
		{"2026-06-26T10:00:00Z", "2026-06-26T10:00:00.000+0000"},
		{"2026-06-26T10:00:00+02:00", "2026-06-26T10:00:00.000+0200"},
		{"2026-06-26T10:00:00.123456Z", "2026-06-26T10:00:00.123+0000"},
		{"2026-06-26T10:00:00.000-0400", "2026-06-26T10:00:00.000-0400"},
		{"2026-06-26T10:00:00+0000", "2026-06-26T10:00:00.000+0000"},
		{"2026-06-26T10:00:00", "2026-06-26T10:00:00.000-0400"},
		{"2026-06-26T10:00", "2026-06-26T10:00:00.000-0400"},
		{"2026-06-26 10:00:00", "2026-06-26T10:00:00.000-0400"},
		{"2026-06-26 10:00", "2026-06-26T10:00:00.000-0400"},
		{"2026-06-26", "2026-06-26T00:00:00.000-0400"},
		{"now", "2026-06-26T15:30:45.123-0400"},
		{"Yesterday", "2026-06-25T15:30:45.123-0400"},
		{"2H AGO", "2026-06-26T13:30:45.123-0400"},
		{"1d2h ago", "2026-06-25T13:30:45.123-0400"},
		{"  2026-06-26T10:00  ", "2026-06-26T10:00:00.000-0400"},
		{"0001-01-01T00:00:00Z", "0001-01-01T00:00:00.000+0000"},
		{"0 ago", "2026-06-26T15:30:45.123-0400"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got, err := ParseWorklogStarted(tc.input, now, loc)
			if err != nil || got != tc.want {
				t.Fatalf("started = %q %v; want %q", got, err, tc.want)
			}
			if _, err := time.Parse(worklogStartedLayout, got); err != nil || strings.HasSuffix(got, "Z") {
				t.Fatalf("invalid Jira timestamp: %q %v", got, err)
			}
		})
	}
	got, err := ParseWorklogStarted("now", now, nil)
	if err != nil || got != now.In(time.Local).Format(worklogStartedLayout) {
		t.Fatalf("nil location: %q %v", got, err)
	}
}

func TestWorklogStartedRejectsMalformedAndOverflow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 26, 15, 30, 45, 0, time.UTC)
	for _, input := range []string{"", "tomorrow", "last tuesday morning", "2h", "-2h ago", "2fortnights ago", "2026-02-30T10:00:00", "10:00:00", "999999999999999h ago", "0000-01-01", "2026-06-26T10:00:00+25:00"} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			if got, err := ParseWorklogStarted(input, now, time.UTC); err == nil || got != "" {
				t.Fatalf("invalid started accepted: %q %v", got, err)
			}
		})
	}
	if got, err := ParseWorklogStarted("1d ago", time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC); err == nil || got != "" {
		t.Fatalf("year underflow accepted: %q %v", got, err)
	}
}

func TestWorklogStartedCalendarDayAcrossDST(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, loc)
	yesterday, err := ParseWorklogStarted("yesterday", now, loc)
	if err != nil || yesterday != "2026-03-07T12:00:00.000-0500" {
		t.Fatalf("calendar day changed: %q %v", yesterday, err)
	}
	elapsed, err := ParseWorklogStarted("1d ago", now, loc)
	if err != nil || elapsed != "2026-03-07T11:00:00.000-0500" {
		t.Fatalf("elapsed day changed: %q %v", elapsed, err)
	}
}
