package core

import "testing"

func TestRecentTouchOrdersMostRecentFirstAndDedupes(t *testing.T) {
	r := NewRecentList()
	r.Touch("JCT-1", "first")
	r.Touch("JCT-2", "second")
	r.Touch("JCT-1", "first again") // revisit moves to front, updates summary
	got := r.List()
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (deduped)", len(got))
	}
	if got[0].Key != "JCT-1" || got[0].Summary != "first again" {
		t.Errorf("front = %+v, want refreshed JCT-1", got[0])
	}
	if got[1].Key != "JCT-2" {
		t.Errorf("second = %+v", got[1])
	}
}

func TestRecentListCapsLength(t *testing.T) {
	r := NewRecentList()
	for i := range recentCap + 10 {
		r.Touch("JCT-"+string(rune('A'+i%26))+string(rune('a'+i/26)), "s")
	}
	if got := len(r.List()); got > recentCap {
		t.Errorf("history grew past cap: %d > %d", got, recentCap)
	}
}

func TestRecentEmptySummaryKeepsRecordedOne(t *testing.T) {
	r := NewRecentList()
	r.Touch("JCT-1", "real summary")
	r.Touch("JCT-1", "") // stub revisit (jumplist foreign-key path)
	if got := r.List()[0].Summary; got != "real summary" {
		t.Errorf("summary after stub touch = %q, want kept", got)
	}
}

func TestRecentIgnoresEmptyKey(t *testing.T) {
	r := NewRecentList()
	r.Touch("", "nope")
	if len(r.List()) != 0 {
		t.Error("empty key recorded")
	}
}
