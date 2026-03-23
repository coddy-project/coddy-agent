package tui

import (
	"os"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestSessionStoreSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSessionStore(dir)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	sess := &StoredSession{
		ID:        "sess_test001",
		CWD:       "/tmp/project",
		Mode:      "agent",
		CreatedAt: time.Now(),
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "hello"},
			{Role: llm.RoleAssistant, Content: "hi there"},
		},
	}

	if err := store.Save(sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(sess.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.ID != sess.ID {
		t.Errorf("ID mismatch: got %q, want %q", loaded.ID, sess.ID)
	}
	if loaded.CWD != sess.CWD {
		t.Errorf("CWD mismatch: got %q, want %q", loaded.CWD, sess.CWD)
	}
	if loaded.Mode != sess.Mode {
		t.Errorf("Mode mismatch: got %q, want %q", loaded.Mode, sess.Mode)
	}
	if len(loaded.Messages) != 2 {
		t.Errorf("Messages count: got %d, want 2", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "hello" {
		t.Errorf("Message[0] content: got %q", loaded.Messages[0].Content)
	}
	if loaded.UpdatedAt.IsZero() {
		t.Error("UpdatedAt must be set after Save")
	}
}

func TestSessionStoreLoadNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSessionStore(dir)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	_, err = store.Load("nonexistent_id")
	if err == nil {
		t.Fatal("expected error loading non-existent session")
	}
}

func TestSessionStoreList(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSessionStore(dir)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	ids, err := store.List()
	if err != nil {
		t.Fatalf("List (empty): %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(ids))
	}

	for _, id := range []string{"sess_aaa", "sess_bbb", "sess_ccc"} {
		if err := store.Save(&StoredSession{ID: id, CWD: "/tmp", Mode: "agent"}); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}

	ids, err = store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(ids) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(ids))
	}
}

func TestSessionStoreDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSessionStore(dir)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	sess := &StoredSession{ID: "sess_del", CWD: "/tmp", Mode: "agent"}
	if err := store.Save(sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := store.Delete(sess.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := os.Stat(store.sessionPath(sess.ID)); !os.IsNotExist(err) {
		t.Error("file should not exist after Delete")
	}

	// Deleting again should not return an error.
	if err := store.Delete(sess.ID); err != nil {
		t.Fatalf("Delete (idempotent): %v", err)
	}
}

func TestSessionStoreCreatesDir(t *testing.T) {
	dir := t.TempDir()
	nested := dir + "/a/b/c"

	store, err := NewSessionStore(nested)
	if err != nil {
		t.Fatalf("NewSessionStore with nested dir: %v", err)
	}

	if err := store.Save(&StoredSession{ID: "sess_x", CWD: "/", Mode: "agent"}); err != nil {
		t.Fatalf("Save in nested dir: %v", err)
	}
}
