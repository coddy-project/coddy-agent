package canvas

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestReportsKeepTheNewestAndBoundTheConsole(t *testing.T) {
	r := NewReports()
	entries := make([]ConsoleEntry, 30)
	for i := range entries {
		entries[i] = ConsoleEntry{Level: "error", Message: strings.Repeat("x", 5000)}
	}
	entries[29].Message = "last"
	r.Put(Report{SessionID: "s", FrameID: "f", Stamp: "a", Loaded: true, Console: entries})
	got, ok := r.Get("s", "f")
	if !ok || !got.Loaded || got.Stamp != "a" || got.At.IsZero() {
		t.Fatalf("report %+v %v", got, ok)
	}
	if len(got.Console) != MaxConsoleEntries {
		t.Fatalf("%d console entries kept, want %d", len(got.Console), MaxConsoleEntries)
	}
	if got.Console[len(got.Console)-1].Message != "last" {
		t.Fatal("the newest console entries were not the ones kept")
	}
	for _, e := range got.Console[:len(got.Console)-1] {
		if len(e.Message) > MaxConsoleMessage {
			t.Fatalf("message of %d bytes kept", len(e.Message))
		}
	}
	if _, ok := r.Get("s", "other"); ok {
		t.Fatal("a report for another frame")
	}
	r.Forget("s", "f")
	if _, ok := r.Get("s", "f"); ok {
		t.Fatal("a forgotten report is still there")
	}
}

func TestReportsWaitForTheCurrentStamp(t *testing.T) {
	r := NewReports()
	r.Put(Report{SessionID: "s", FrameID: "f", Stamp: "old", Loaded: true})
	done := make(chan Report, 1)
	go func() {
		rep, _ := r.Wait(context.Background(), "s", "f", "new", 2*time.Second)
		done <- rep
	}()
	time.Sleep(50 * time.Millisecond)
	r.Put(Report{SessionID: "s", FrameID: "f", Stamp: "new", Loaded: true, Title: "fresh"})
	select {
	case rep := <-done:
		if rep.Stamp != "new" || rep.Title != "fresh" {
			t.Fatalf("waited report %+v", rep)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not return on the fresh report")
	}
	// A wait that times out returns what there is.
	rep, fresh := r.Wait(context.Background(), "s", "f", "newer", 50*time.Millisecond)
	if fresh || rep.Stamp != "new" {
		t.Fatalf("timed out wait: %+v fresh=%v", rep, fresh)
	}
}

func TestReportsAreBoundedOverall(t *testing.T) {
	r := NewReports()
	for i := 0; i < MaxReports+50; i++ {
		r.Put(Report{SessionID: "s", FrameID: strings.Repeat("f", 1) + string(rune('a'+i%26)) + strings.Repeat("x", i), Stamp: "a"})
	}
	if n := r.Len(); n > MaxReports {
		t.Fatalf("%d reports kept, want at most %d", n, MaxReports)
	}
}

func TestAClosedReportSaysSo(t *testing.T) {
	r := NewReports()
	r.Put(Report{SessionID: "s", FrameID: "f", Stamp: "a", Loaded: true})
	r.Put(Report{SessionID: "s", FrameID: "f", Stamp: "a", Closed: true})
	got, ok := r.Get("s", "f")
	if !ok || !got.Closed || !got.Loaded {
		t.Fatalf("closed report %+v", got)
	}
}

func TestAWaitThatTimesOutLeavesNoWaiterBehind(t *testing.T) {
	r := NewReports()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 50; i++ {
		r.Wait(context.Background(), "s", "f", "stamp", time.Millisecond)
		r.Wait(ctx, "s", "f", "stamp", time.Hour)
	}
	r.mu.Lock()
	n := len(r.waiters)
	r.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d waiters left after waits that ended without a report", n)
	}
}
