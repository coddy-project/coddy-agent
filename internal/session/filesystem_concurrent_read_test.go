package session

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// A bundle is read while its own turn keeps persisting: the session list and the
// activity endpoint both do it on every poll. On Windows an atomic write holds the
// destination open, so an unguarded read fails with a sharing violation instead of
// returning the previous or the next version of the file.
func TestReadSnapshotSurvivesConcurrentSaves(t *testing.T) {
	store := &FileStore{Root: filepath.Join(t.TempDir(), "sessions")}
	sid := "sess_concurrent_read"
	sd, err := store.EnsureLayout(sid)
	if err != nil {
		t.Fatal(err)
	}
	st := &State{
		ID:         sid,
		CWD:        t.TempDir(),
		Mode:       ModeAgent,
		SessionDir: sd,
		Messages:   []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
	}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}

	const rounds = 200
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "turn output"})
			if err := store.Save(st); err != nil {
				t.Errorf("save %d: %v", i, err)
				return
			}
		}
	}()

	for i := 0; i < rounds; i++ {
		if _, err := store.ReadSnapshot(sid); err != nil {
			t.Fatalf("read %d while the session was being written: %v", i, err)
		}
	}
	wg.Wait()
}
