package session

import "testing"

func TestGoalStatePersistsWithSession(t *testing.T) {
	fs := &FileStore{Root: t.TempDir()}
	dir, err := fs.EnsureLayout("sess_goal")
	if err != nil {
		t.Fatal(err)
	}
	st := &State{ID: "sess_goal", CWD: t.TempDir(), Mode: ModeAgent, SessionDir: dir}
	st.SetGoal(GoalState{Text: "ship the fix", Status: GoalActive})
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap, err := fs.ReadSnapshot(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.Goal.Text != "ship the fix" || snap.Meta.Goal.Status != GoalActive {
		t.Fatalf("persisted goal = %+v", snap.Meta.Goal)
	}
}
