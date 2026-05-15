package session

import (
	"testing"
	"time"
)

func TestMarkToolCallFinishedPreservesStartedAt(t *testing.T) {
	t.Parallel()
	sd := t.TempDir()
	id := "call_test_1"
	if err := MarkToolCallStarted(sd, id, "grep", "tool", "in_progress"); err != nil {
		t.Fatalf("MarkToolCallStarted: %v", err)
	}
	before, err := ReadToolCallMeta(sd, id)
	if err != nil {
		t.Fatalf("ReadToolCallMeta after start: %v", err)
	}
	if before.StartedAt == "" {
		t.Fatal("expected StartedAt after MarkToolCallStarted")
	}
	time.Sleep(2 * time.Millisecond)
	if err := MarkToolCallFinished(sd, id, "grep", "tool", "completed"); err != nil {
		t.Fatalf("MarkToolCallFinished: %v", err)
	}
	after, err := ReadToolCallMeta(sd, id)
	if err != nil {
		t.Fatalf("ReadToolCallMeta after finish: %v", err)
	}
	if after.StartedAt != before.StartedAt {
		t.Fatalf("StartedAt changed: before %q after %q", before.StartedAt, after.StartedAt)
	}
	if after.FinishedAt == "" {
		t.Fatal("expected FinishedAt after MarkToolCallFinished")
	}
	st0, err0 := time.Parse(time.RFC3339, after.StartedAt)
	st1, err1 := time.Parse(time.RFC3339, after.FinishedAt)
	if err0 != nil || err1 != nil {
		t.Fatalf("parse RFC3339: started %v finished %v", err0, err1)
	}
	if !st1.After(st0) && !st1.Equal(st0) {
		t.Fatalf("FinishedAt should be >= StartedAt: %v %v", st0, st1)
	}
}
