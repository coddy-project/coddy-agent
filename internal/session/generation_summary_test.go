package session

import (
	"reflect"
	"testing"
	"time"
)

func TestGenerationSummaryReplacesContinuedTurnAndPersists(t *testing.T) {
	store := &FileStore{Root: t.TempDir()}
	const id = "sess_continued_summary"
	dir, err := store.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	state := &State{ID: id, CWD: t.TempDir(), SessionDir: dir, Mode: ModeAgent}
	start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	state.AppendGenerationSummary(start, 1, 100, 50)
	before := state.GetUILog()
	state.AppendUILogNotice(2, "Follow-up accepted")
	state.AppendGenerationSummary(start, 2, 300, 75)
	state.AppendGenerationSummary(start, 2, 300, 75)
	state.AppendGenerationSummary(start.Add(time.Minute), 3, 200, 20)

	rows := state.GetUILog()
	if len(rows) != 3 {
		t.Fatalf("continued turn summaries were appended instead of replaced: %+v", rows)
	}
	if rows[0].Message != "Follow-up accepted" || rows[1].UserTurnIndex != 2 ||
		rows[1].Generation == nil || *rows[1].Generation != (GenerationSummary{OutputTokens: 300, TokensPerSecond: 75}) ||
		rows[2].Generation == nil || rows[2].Generation.TokensPerSecond != 20 {
		t.Fatalf("summary lost its latest value or transcript position: %+v", rows)
	}
	if before[0].Generation.OutputTokens != 100 {
		t.Fatal("updating the summary mutated an earlier UI-log snapshot")
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.UILog, rows) {
		t.Fatalf("saved summary changed on reload: %+v, want %+v", snapshot.UILog, rows)
	}
}
