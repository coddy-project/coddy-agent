package session

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestFileStoreRoundTripUILog(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	id := "sess_ulog"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}

	st := &State{
		ID:         id,
		CWD:        "/tmp/unit",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	st.AppendUILogError(1, "context exceeded")

	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.UILog) != 1 {
		t.Fatalf("ui log len=%d", len(snap.UILog))
	}
	if snap.UILog[0].Message != "context exceeded" || snap.UILog[0].UserTurnIndex != 1 {
		t.Fatalf("entry %+v", snap.UILog[0])
	}
}

func TestFileStoreRoundTripMessages(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	id := "sess_unit"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}

	st := &State{
		ID:         id,
		CWD:        "/tmp/unit",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hi"})
	st.AddMessage(llm.Message{
		Role:                llm.RoleAssistant,
		Content:             "hello",
		Reasoning:           "step one",
		ReasoningDurationMs: 42,
	})

	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}

	snap, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 2 {
		t.Fatalf("messages roundtrip len=%d", len(snap.Messages))
	}
	if snap.Messages[1].Role != llm.RoleAssistant {
		t.Fatalf("second role %+v", snap.Messages[1].Role)
	}
	if snap.Messages[1].Reasoning != "step one" || snap.Messages[1].ReasoningDurationMs != 42 {
		t.Fatalf("reasoning roundtrip %+v", snap.Messages[1])
	}
}

func TestActiveTodoPersistence(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	id := "sess_td"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}

	st := &State{
		ID:         id,
		CWD:        "/tmp",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	st.SetPlanWithoutPersist([]acp.PlanEntry{
		{Content: "a", Status: "pending"},
		{Content: "b", Status: "completed"},
	})

	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap, err := fs.ReadSnapshot(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Plan) != 2 {
		t.Fatalf("plan len=%d", len(snap.Plan))
	}
}

func TestListSnapshotsSkipsMarkedSchedulerRunsByMeta(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	visibleID := "conv_normal"
	dirV, err := fs.EnsureLayout(visibleID)
	if err != nil {
		t.Fatal(err)
	}
	v := &State{ID: visibleID, CWD: "/tmp", Mode: ModeAgent, SessionDir: dirV}
	v.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hi"})
	if err := fs.Save(v); err != nil {
		t.Fatal(err)
	}

	metaOnlyID := "custom_no_prefix"
	dirM, err := fs.EnsureLayout(metaOnlyID)
	if err != nil {
		t.Fatal(err)
	}
	m := &State{ID: metaOnlyID, CWD: "/tmp", Mode: ModeAgent, SessionDir: dirM}
	m.SetSchedulerRunMeta("job_a", time.Now().UTC().Format(time.RFC3339))
	m.AddMessage(llm.Message{Role: llm.RoleUser, Content: "cron"})
	if err := fs.Save(m); err != nil {
		t.Fatal(err)
	}

	rowsDefault, err := fs.ListSnapshots("", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsDefault) != 1 || rowsDefault[0].SessionID != visibleID {
		t.Fatalf("composer list: %+v", rowsDefault)
	}
	all, err := fs.ListSnapshots("", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 sessions with include scheduler, got %d", len(all))
	}
}

func TestListSnapshotsSkipsSchedulerSessions(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	// Normal persisted session.
	normalID := "sess_normal"
	normalDir, err := fs.EnsureLayout(normalID)
	if err != nil {
		t.Fatal(err)
	}
	normal := &State{
		ID:         normalID,
		CWD:        "/tmp/unit",
		Mode:       ModeAgent,
		SessionDir: normalDir,
	}
	normal.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})
	if err := fs.Save(normal); err != nil {
		t.Fatal(err)
	}

	// Scheduler-like session id prefix.
	schedID := "sched_deadbeef"
	schedDir, err := fs.EnsureLayout(schedID)
	if err != nil {
		t.Fatal(err)
	}
	sched := &State{
		ID:         schedID,
		CWD:        "/tmp/unit",
		Mode:       ModeAgent,
		SessionDir: schedDir,
	}
	sched.AddMessage(llm.Message{Role: llm.RoleUser, Content: "ignore me"})
	if err := fs.Save(sched); err != nil {
		t.Fatal(err)
	}

	rows, err := fs.ListSnapshots("", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 visible snapshot, got %d", len(rows))
	}
	if rows[0].SessionID != normalID {
		t.Fatalf("expected %s, got %s", normalID, rows[0].SessionID)
	}
}

func TestFilterSnapshotListForSearchMatchesFirstUserNotTitle(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	makeSess := func(id, title, firstUserContent string, prefixRoles []llm.Message) error {
		dir, err := fs.EnsureLayout(id)
		if err != nil {
			return err
		}
		st := &State{
			ID:         id,
			CWD:        "/tmp",
			Mode:       ModeAgent,
			SessionDir: dir,
		}
		st.SetTitlePinned(title)
		msgs := append(append([]llm.Message{}, prefixRoles...), llm.Message{Role: llm.RoleUser, Content: firstUserContent})
		for _, m := range msgs {
			st.AddMessage(m)
		}
		return fs.Save(st)
	}

	if err := makeSess("sess_a", "Alpha topic", "unique zebra finder", nil); err != nil {
		t.Fatal(err)
	}
	if err := makeSess("sess_b", "Other", "nothing special", nil); err != nil {
		t.Fatal(err)
	}

	rows, err := fs.ListSnapshots("", false)
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := fs.FilterSnapshotListForSearch(rows, "zebra")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].SessionID != "sess_a" {
		t.Fatalf("want sess_a only, got %+v", filtered)
	}
}

func TestFilterSnapshotListAssistantPrefixNoFirstUserSkippedForMessageMatch(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	id := "sess_assist_only"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	st := &State{
		ID:         id,
		CWD:        "/tmp",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	st.SetTitlePinned("gamma title plain")
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "hidden needle in assistant"})
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}

	rows, err := fs.ListSnapshots("", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"needle", "hidden"} {
		filtered, err := fs.FilterSnapshotListForSearch(rows, needle)
		if err != nil {
			t.Fatal(err)
		}
		if len(filtered) != 0 {
			t.Fatalf("q=%q expected no rows (no user message); got %+v", needle, filtered)
		}
	}
	filteredGamma, err := fs.FilterSnapshotListForSearch(rows, "gamma")
	if err != nil {
		t.Fatal(err)
	}
	if len(filteredGamma) != 1 || filteredGamma[0].SessionID != id {
		t.Fatalf("title match gamma: %+v", filteredGamma)
	}
}

func TestFilterSnapshotFirstUserAfterSystemIgnoredForSecondUser(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	id := "sess_order"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	st := &State{
		ID:         id,
		CWD:        "/tmp",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	st.SetTitlePinned("")
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "first hello"})
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "second unique xyzzy"})
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	rows, err := fs.ListSnapshots("", false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fs.FilterSnapshotListForSearch(rows, "xyzzy")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("second user must not match, got %+v", got)
	}
	got2, err := fs.FilterSnapshotListForSearch(rows, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 1 {
		t.Fatalf("first user hello: %+v", got2)
	}
}

func TestSavePreservesUpdatedAtWhenMessagesAndActivitySeqUnchanged(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	id := "sess_preserve_ut"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	st := &State{
		ID:         id,
		CWD:        "/tmp",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hi"})
	st.RestoreActivityFromSnapshot(1, 0)
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap1, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	ut1 := snap1.Meta.UpdatedAt
	if ut1 == "" {
		t.Fatal("expected updatedAt after first save")
	}
	if snap1.Meta.ActivitySeq != 1 || snap1.Meta.ReadActivitySeq != 0 {
		t.Fatalf("meta %+v", snap1.Meta)
	}
	st.MarkActivityReadSynced()
	if err := fs.PatchSessionMetaActivitySync(st); err != nil {
		t.Fatal(err)
	}
	snap2, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap2.Meta.ReadActivitySeq != 1 {
		t.Fatalf("read seq %+v", snap2.Meta)
	}
	if snap2.Meta.UpdatedAt != ut1 {
		t.Fatalf("mark read only: updatedAt changed from %q to %q", ut1, snap2.Meta.UpdatedAt)
	}
	time.Sleep(1200 * time.Millisecond)
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "bye"})
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap3, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap3.Meta.UpdatedAt == ut1 {
		t.Fatal("expected updatedAt to change after new user message")
	}
}

func TestConcurrentPatchSessionMetaActivitySync(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	id := "sess_concur_meta"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	st := &State{
		ID:         id,
		CWD:        "/tmp",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hi"})
	st.RestoreActivityFromSnapshot(10, 0)
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(k int) {
			defer wg.Done()
			stLocal := &State{
				ID:         id,
				CWD:        "/tmp",
				Mode:       ModeAgent,
				SessionDir: dir,
			}
			stLocal.RestoreActivityFromSnapshot(uint64(10+k), uint64(k))
			if err := fs.PatchSessionMetaActivitySync(stLocal); err != nil {
				errCh <- fmt.Errorf("k=%d: %w", k, err)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	snap, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.ActivitySeq < 10 {
		t.Fatalf("activitySeq=%d", snap.Meta.ActivitySeq)
	}
}
