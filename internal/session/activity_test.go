package session

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestRecordActivityOutcomeTransitionsAndPersistsAfterCounters(t *testing.T) {
	st := &State{ID: "sess_activity_outcome", CWD: "/tmp", Mode: ModeAgent}
	var persisted [][3]uint64
	st.SetPersistHook(func() {
		persisted = append(persisted, [3]uint64{st.GetActivitySeq(), st.GetReadActivitySeq(), st.GetLastErrorSeq()})
	})

	st.RecordActivityOutcome(ActivityOutcomeFailure)
	if got := [3]uint64{st.GetActivitySeq(), st.GetReadActivitySeq(), st.GetLastErrorSeq()}; got != [3]uint64{1, 0, 1} {
		t.Fatalf("failure counters = %v, want [1 0 1]", got)
	}
	st.RecordActivityOutcome(ActivityOutcomeCanceled)
	if got := [3]uint64{st.GetActivitySeq(), st.GetReadActivitySeq(), st.GetLastErrorSeq()}; got != [3]uint64{2, 0, 1} {
		t.Fatalf("cancellation counters = %v, want [2 0 1]", got)
	}
	st.RecordActivityOutcome(ActivityOutcomeSuccess)
	if got := [3]uint64{st.GetActivitySeq(), st.GetReadActivitySeq(), st.GetLastErrorSeq()}; got != [3]uint64{3, 0, 0} {
		t.Fatalf("success counters = %v, want [3 0 0]", got)
	}
	want := [][3]uint64{{1, 0, 1}, {2, 0, 1}, {3, 0, 0}}
	if len(persisted) != len(want) {
		t.Fatalf("persisted %v, want %v", persisted, want)
	}
	for i := range want {
		if persisted[i] != want[i] {
			t.Fatalf("persisted[%d] = %v, want %v", i, persisted[i], want[i])
		}
	}
}

func TestActivityLastErrorSeqSurvivesSaveRestoreAndReadPatch(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	id := "sess_activity_persist"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	st := &State{ID: id, CWD: "/tmp", Mode: ModeAgent, SessionDir: dir}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hi"})
	st.RestoreActivityFromSnapshot(4, 1, 4)
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}

	snap, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.ActivitySeq != 4 || snap.Meta.ReadActivitySeq != 1 || snap.Meta.LastErrorSeq != 4 {
		t.Fatalf("saved activity metadata = %+v", snap.Meta)
	}
	fresh := &State{ID: id, CWD: "/tmp", Mode: ModeAgent, SessionDir: dir}
	fresh.RestoreActivityFromSnapshot(snap.Meta.ActivitySeq, snap.Meta.ReadActivitySeq, snap.Meta.LastErrorSeq)
	if fresh.GetLastErrorSeq() != 4 {
		t.Fatalf("restored lastErrorSeq = %d, want 4", fresh.GetLastErrorSeq())
	}

	fresh.MarkActivityReadSynced()
	if err := fs.PatchSessionMetaActivitySync(fresh); err != nil {
		t.Fatal(err)
	}
	snap, err = fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.ReadActivitySeq != 4 || snap.Meta.LastErrorSeq != 4 {
		t.Fatalf("read patch changed activity metadata = %+v", snap.Meta)
	}
}

func TestPatchSessionMetaActivitySyncMergesCurrentDiskCounters(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	id := "sess_activity_stale_patch"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	current := &State{ID: id, CWD: "/tmp", Mode: ModeAgent, SessionDir: dir}
	current.RestoreActivityFromSnapshot(7, 2, 7)
	if err := fs.Save(current); err != nil {
		t.Fatal(err)
	}
	snapBefore, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}

	stale := &State{ID: id, CWD: "/tmp", Mode: ModeAgent, SessionDir: dir}
	stale.RestoreActivityFromSnapshot(3, 1, 3)
	if err := fs.PatchSessionMetaActivitySync(stale); err != nil {
		t.Fatal(err)
	}

	snapAfter, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snapAfter.Meta.ActivitySeq != 7 || snapAfter.Meta.ReadActivitySeq != 7 || snapAfter.Meta.LastErrorSeq != 7 {
		t.Fatalf("stale activity patch changed current counters: %+v", snapAfter.Meta)
	}
	if snapAfter.Meta.UpdatedAt != snapBefore.Meta.UpdatedAt {
		t.Fatalf("stale activity patch changed updatedAt: before %q after %q", snapBefore.Meta.UpdatedAt, snapAfter.Meta.UpdatedAt)
	}
}

func TestSaveDoesNotRollBackNewerDiskActivityCounters(t *testing.T) {
	root := t.TempDir()
	newerStore := &FileStore{Root: root}
	staleStore := &FileStore{Root: root}
	id := "sess_activity_stale_save"
	dir, err := newerStore.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}

	newer := &State{ID: id, CWD: "/tmp", Mode: ModeAgent, SessionDir: dir}
	newer.AddMessage(llm.Message{Role: llm.RoleUser, Content: "newer"})
	newer.RestoreActivityFromSnapshot(8, 6, 8)
	if err := newerStore.Save(newer); err != nil {
		t.Fatal(err)
	}
	snapBefore, err := newerStore.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}

	stale := &State{ID: id, CWD: "/tmp", Mode: ModeAgent, SessionDir: dir}
	stale.RestoreActivityFromSnapshot(3, 1, 3)
	stale.AddMessage(llm.Message{Role: llm.RoleUser, Content: "newer"})
	if err := staleStore.Save(stale); err != nil {
		t.Fatal(err)
	}
	snapAfter, err := newerStore.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snapAfter.Meta.ActivitySeq != 8 || snapAfter.Meta.ReadActivitySeq != 6 || snapAfter.Meta.LastErrorSeq != 8 {
		t.Fatalf("stale save changed newer activity counters: %+v", snapAfter.Meta)
	}
	if snapAfter.Meta.UpdatedAt != snapBefore.Meta.UpdatedAt {
		t.Fatalf("stale save changed updatedAt: before %q after %q", snapBefore.Meta.UpdatedAt, snapAfter.Meta.UpdatedAt)
	}
}

func TestRestoreActivityFromSnapshotDoesNotRollBackLiveCounters(t *testing.T) {
	st := &State{ID: "sess_activity_live", CWD: "/tmp", Mode: ModeAgent}
	st.RestoreActivityFromSnapshot(9, 7, 9)
	st.RestoreActivityFromSnapshot(4, 2, 4)

	if got := [3]uint64{st.GetActivitySeq(), st.GetReadActivitySeq(), st.GetLastErrorSeq()}; got != [3]uint64{9, 7, 9} {
		t.Fatalf("stale snapshot rolled back live counters: %v", got)
	}
}
