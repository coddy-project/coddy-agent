package session

import (
	"testing"

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

	rows, err := fs.ListSnapshots("")
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

	rows, err := fs.ListSnapshots("")
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

	rows, err := fs.ListSnapshots("")
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
	rows, err := fs.ListSnapshots("")
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
