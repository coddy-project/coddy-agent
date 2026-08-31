package session

import (
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
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

func TestWriteToolCallPlanSnapshotPersistsFinalTodoState(t *testing.T) {
	t.Parallel()
	sd := t.TempDir()
	entries := []acp.PlanEntry{
		{Content: "Inspect tool cards", Status: "completed"},
		{Content: "Render todo preview", Status: "in_progress"},
	}
	if err := MarkToolCallFinished(sd, "todo-1", "coddy_todo_item_update", "todo", "completed"); err != nil {
		t.Fatalf("MarkToolCallFinished: %v", err)
	}
	if err := WriteToolCallPlanSnapshot(sd, "todo-1", entries); err != nil {
		t.Fatalf("WriteToolCallPlanSnapshot: %v", err)
	}

	meta, err := ReadToolCallMeta(sd, "todo-1")
	if err != nil {
		t.Fatalf("ReadToolCallMeta: %v", err)
	}
	if meta.Status != "completed" || meta.Name != "coddy_todo_item_update" {
		t.Fatalf("tool metadata was overwritten: %+v", meta)
	}
	if len(meta.PlanSnapshot) != len(entries) || meta.PlanSnapshot[1].Status != "in_progress" {
		t.Fatalf("PlanSnapshot = %+v, want %+v", meta.PlanSnapshot, entries)
	}
}
