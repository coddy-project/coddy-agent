package session

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

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
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "hello"})

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
