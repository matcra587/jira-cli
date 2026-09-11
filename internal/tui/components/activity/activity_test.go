package activity

import (
	"errors"
	"testing"
)

func TestStartReturnsMonotonicIDsFromOne(t *testing.T) {
	r := New()
	for i, want := range []uint64{1, 2, 3} {
		if got := r.Start("op"); got != want {
			t.Errorf("start #%d: got id %d, want %d", i, got, want)
		}
	}
}

func TestStartZeroValueRegistry(t *testing.T) {
	// A zero-value Registry (New not called) still starts ids at 1.
	var r Registry
	if got := r.Start("op"); got != 1 {
		t.Errorf("got id %d, want 1", got)
	}
}

func TestFinish(t *testing.T) {
	r := New()
	id := r.Start("creating issue in PROJ…")
	r.Finish(id, "PROJ-88 created", "PROJ-88")

	e, ok := r.Recent()
	if !ok {
		t.Fatal("recent returned false after finish")
	}
	if e.ID != id {
		t.Errorf("id: got %d, want %d", e.ID, id)
	}
	if e.Status != StatusDone {
		t.Errorf("status: got %d, want StatusDone", e.Status)
	}
	if e.Done != "PROJ-88 created" {
		t.Errorf("done: got %q", e.Done)
	}
	if e.IssueKey != "PROJ-88" {
		t.Errorf("issueKey: got %q", e.IssueKey)
	}
	if e.Pending != "creating issue in PROJ…" {
		t.Errorf("pending text should be preserved, got %q", e.Pending)
	}
}

func TestFinishResolvesInPlaceSameID(t *testing.T) {
	r := New()
	id1 := r.Start("first")
	id2 := r.Start("second")
	r.Finish(id1, "first done", "")

	log := r.Log()
	if len(log) != 2 {
		t.Fatalf("log length: got %d, want 2", len(log))
	}
	// Newest-first: id2 then id1; id1 resolved in place, ids unchanged.
	if log[0].ID != id2 || log[1].ID != id1 {
		t.Errorf("ids after finish: got [%d %d], want [%d %d]", log[0].ID, log[1].ID, id2, id1)
	}
	if log[1].Status != StatusDone {
		t.Errorf("id1 status: got %d, want StatusDone", log[1].Status)
	}
	if log[0].Status != StatusPending {
		t.Errorf("id2 status: got %d, want StatusPending", log[0].Status)
	}
}

func TestFail(t *testing.T) {
	r := New()
	id := r.Start("deleting PROJ-9")
	wantErr := errors.New("boom")
	r.Fail(id, wantErr)

	e, _ := r.Recent()
	if e.Status != StatusFailed {
		t.Errorf("status: got %d, want StatusFailed", e.Status)
	}
	if !errors.Is(e.Err, wantErr) {
		t.Errorf("err: got %v, want %v", e.Err, wantErr)
	}
}

func TestUnknownIDIsNoOp(t *testing.T) {
	r := New()
	id := r.Start("op")
	// Neither of these should panic or alter the recorded entry.
	r.Finish(999, "nope", "NOPE")
	r.Fail(777, errors.New("nope"))

	e, _ := r.Recent()
	if e.ID != id || e.Status != StatusPending {
		t.Errorf("entry mutated by unknown-id calls: %+v", e)
	}
	if e.Done != "" || e.IssueKey != "" || e.Err != nil {
		t.Errorf("entry fields written by unknown-id calls: %+v", e)
	}
}

func TestInFlightCountsOnlyPending(t *testing.T) {
	r := New()
	a := r.Start("a")
	r.Start("b")
	c := r.Start("c")
	if got := r.InFlight(); got != 3 {
		t.Fatalf("all pending: got %d, want 3", got)
	}
	r.Finish(a, "a done", "")
	r.Fail(c, errors.New("c failed"))
	if got := r.InFlight(); got != 1 {
		t.Errorf("after one done and one failed: got %d, want 1", got)
	}
}

func TestRecent(t *testing.T) {
	r := New()
	if _, ok := r.Recent(); ok {
		t.Fatal("recent on empty registry returned true")
	}
	r.Start("first")
	r.Start("second")
	e, ok := r.Recent()
	if !ok {
		t.Fatal("recent returned false with entries present")
	}
	if e.Pending != "second" {
		t.Errorf("recent should be newest (highest id), got %q", e.Pending)
	}
}

func TestLogNewestFirst(t *testing.T) {
	r := New()
	r.Start("first")
	r.Start("second")
	r.Start("third")
	log := r.Log()
	want := []string{"third", "second", "first"}
	if len(log) != len(want) {
		t.Fatalf("log length: got %d, want %d", len(log), len(want))
	}
	for i, w := range want {
		if log[i].Pending != w {
			t.Errorf("log[%d]: got %q, want %q", i, log[i].Pending, w)
		}
	}
}

func TestLogCappedAtMaxLog(t *testing.T) {
	r := New()
	const extra = 5
	total := maxLog + extra
	var lastID uint64
	for range total {
		lastID = r.Start("op")
	}

	log := r.Log()
	if len(log) != maxLog {
		t.Fatalf("log length: got %d, want %d", len(log), maxLog)
	}
	// The newest survive: log[0] is the very last Start, and the oldest
	// retained id is total-maxLog+1 (the first `extra` were dropped).
	if log[0].ID != lastID {
		t.Errorf("newest id: got %d, want %d", log[0].ID, lastID)
	}
	wantOldest := uint64(total - maxLog + 1)
	if got := log[len(log)-1].ID; got != wantOldest {
		t.Errorf("oldest retained id: got %d, want %d", got, wantOldest)
	}
}

func TestReturnedEntriesAreCopies(t *testing.T) {
	r := New()
	id := r.Start("op")

	// Mutating an Entry returned by Recent must not touch registry state.
	e, _ := r.Recent()
	e.Pending = "tampered"
	e.Status = StatusFailed
	if e.Pending != "tampered" || e.Status != StatusFailed {
		t.Fatal("a returned Entry should be a freely-mutable copy")
	}

	// Mutating an Entry returned by Log must not either.
	log := r.Log()
	log[0].Done = "tampered"
	if log[0].Done != "tampered" {
		t.Fatal("a returned log slice element should be a freely-mutable copy")
	}

	fresh, _ := r.Recent()
	if fresh.Pending != "op" || fresh.Status != StatusPending || fresh.Done != "" {
		t.Errorf("registry state changed through returned copy: %+v", fresh)
	}
	if fresh.ID != id {
		t.Errorf("id changed: got %d, want %d", fresh.ID, id)
	}
}
