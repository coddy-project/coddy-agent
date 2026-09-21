//go:build gateway || gateway.telegram

package sessionstore_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

func TestSessionKey_Private(t *testing.T) {
	k := sessionstore.SessionKey("tg", -1, 42, config.IsolationShared, false)
	want := "tg:user:42"
	if k != want {
		t.Fatalf("want %q got %q", want, k)
	}
}

func TestSessionKey_GroupShared(t *testing.T) {
	k := sessionstore.SessionKey("tg", -100, 42, config.IsolationShared, true)
	want := "tg:chat:-100"
	if k != want {
		t.Fatalf("want %q got %q", want, k)
	}
}

func TestSessionKey_GroupIndividual(t *testing.T) {
	k := sessionstore.SessionKey("tg", -100, 42, config.IsolationIndividual, true)
	want := "tg:chat:-100:user:42"
	if k != want {
		t.Fatalf("want %q got %q", want, k)
	}
}

func TestSessionKey_GroupAdmin(t *testing.T) {
	k := sessionstore.SessionKey("tg", -100, 42, config.IsolationAdmin, true)
	want := "tg:chat:-100:admin"
	if k != want {
		t.Fatalf("want %q got %q", want, k)
	}
}

// A background subagent asks about a session, not a chat: the store is how the
// bot finds the chat that conversation lives in, including after a restart.
func TestKeyForFindsTheChatOfASession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway_sessions.json")
	s := sessionstore.NewPersisted(path)
	group := sessionstore.SessionKey("tg", -100, 42, config.IsolationIndividual, true)
	id := s.Get(group)
	s.Get(sessionstore.SessionKey("tg", -1, 7, config.IsolationShared, false))

	reloaded := sessionstore.NewPersisted(path)
	key, ok := reloaded.KeyFor(id)
	if !ok || key != group {
		t.Fatalf("KeyFor(%q) = %q, %v; want %q", id, key, ok, group)
	}
	if _, ok := reloaded.KeyFor("sess_nobody"); ok {
		t.Fatal("a session no chat holds was found")
	}
	if _, ok := reloaded.KeyFor(""); ok {
		t.Fatal("an empty session id was found")
	}
}

func TestChatIDOfEveryKeyShape(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want int64
	}{
		{sessionstore.SessionKey("tg", -1, 42, config.IsolationShared, false), 42},
		{sessionstore.SessionKey("tg", -100, 42, config.IsolationShared, true), -100},
		{sessionstore.SessionKey("tg", -100, 42, config.IsolationAdmin, true), -100},
		{sessionstore.SessionKey("tg", -100, 42, config.IsolationIndividual, true), -100},
	} {
		got, ok := sessionstore.ChatID(tc.key)
		if !ok || got != tc.want {
			t.Fatalf("ChatID(%q) = %d, %v; want %d", tc.key, got, ok, tc.want)
		}
	}
	for _, bad := range []string{"", "tg", "tg:user:", "tg:room:5", "tg:chat:not-a-number"} {
		if _, ok := sessionstore.ChatID(bad); ok {
			t.Fatalf("ChatID(%q) accepted a key that names no chat", bad)
		}
	}
}

func TestStore_GetAndReset(t *testing.T) {
	s := sessionstore.New()
	id1 := s.Get("tg:user:1")
	if id1 == "" {
		t.Fatal("expected non-empty session ID")
	}
	if s.Get("tg:user:1") != id1 {
		t.Fatal("second Get should return same ID")
	}
	id2 := s.Reset("tg:user:1")
	if id2 == id1 {
		t.Fatal("Reset should produce a different ID")
	}
	if s.Get("tg:user:1") != id2 {
		t.Fatal("Get after Reset should return new ID")
	}
}

// Peek reports a mapping without creating one: a log line that names the
// session must not persist a new entry for a chat that only typed /help.
func TestPeekDoesNotMintAMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway_sessions.json")
	s := sessionstore.NewPersisted(path)

	if got := s.Peek("tg:user:1"); got != "" {
		t.Fatalf("Peek on an unknown key = %q, want empty", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Peek wrote the store file: %v", err)
	}

	id := s.Get("tg:user:1")
	if id == "" {
		t.Fatal("Get returned no id")
	}
	if got := s.Peek("tg:user:1"); got != id {
		t.Fatalf("Peek = %q, want the id Get minted (%q)", got, id)
	}
}

// Bind maps a chat to a session that already exists - what /resume does - and
// the mapping is on disk for the next process like one Get or Reset wrote.
func TestBindPersistsTheChosenID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway_sessions.json")
	s := sessionstore.NewPersisted(path)

	s.Bind("tg:user:1", "sess_aaaaaaaaaaaaaaaaaaaaaaaa")
	if got := s.Get("tg:user:1"); got != "sess_aaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("Get after Bind = %q", got)
	}
	again := sessionstore.NewPersisted(path)
	if got := again.Peek("tg:user:1"); got != "sess_aaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("a fresh store over the same file reads %q, Bind was not persisted", got)
	}

	// Nothing may map a chat to no session, and no chat is called "".
	s.Bind("tg:user:1", "  ")
	if got := s.Peek("tg:user:1"); got != "sess_aaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("Bind with an empty id changed the mapping to %q", got)
	}
	s.Bind("", "sess_bbbbbbbbbbbbbbbbbbbbbbbb")
	if got := s.Peek(""); got != "" {
		t.Fatalf("Bind with an empty key stored %q", got)
	}
}

// SetLastModel records the gateway's own last pick in the same file, survives
// a reload like every other entry, and stays invisible to the session-key
// readers (KeyFor, KnownIDs).
func TestLastModelPersistsAndStaysOutOfSessionLookups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway_sessions.json")
	s := sessionstore.NewPersisted(path)

	if got := s.LastModel(); got != "" {
		t.Fatalf("LastModel on a fresh store = %q, want empty", got)
	}
	s.SetLastModel("  ")
	if got := s.LastModel(); got != "" {
		t.Fatalf("SetLastModel with a blank id stored %q", got)
	}
	s.SetLastModel("rpa/qwen3.6-35b-a3b")
	if got := s.LastModel(); got != "rpa/qwen3.6-35b-a3b" {
		t.Fatalf("LastModel = %q", got)
	}

	id := s.Get("tg:user:1")
	if key, ok := s.KeyFor(id); !ok || key != "tg:user:1" {
		t.Fatalf("KeyFor(%q) = (%q, %v), the model entry must not answer session lookups", id, key, ok)
	}
	for _, known := range s.KnownIDs() {
		if known == "rpa/qwen3.6-35b-a3b" {
			t.Fatalf("KnownIDs lists the model entry %q as a session id", known)
		}
	}

	again := sessionstore.NewPersisted(path)
	if got := again.LastModel(); got != "rpa/qwen3.6-35b-a3b" {
		t.Fatalf("a fresh store over the same file reads LastModel %q", got)
	}
}

// A file written by a build that knew only the flat session map loads with
// the model memory simply empty - no migration needed.
func TestLastModelLoadsFromAFlatSessionsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway_sessions.json")
	if err := os.WriteFile(path, []byte(`{"tg:user:1":"sess_aaaaaaaaaaaaaaaaaaaaaaaa"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := sessionstore.NewPersisted(path)
	if got := s.LastModel(); got != "" {
		t.Fatalf("LastModel over a flat file = %q, want empty", got)
	}
	if got := s.Peek("tg:user:1"); got != "sess_aaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("flat file session binding lost: Peek = %q", got)
	}
}
