//go:build gateway || gateway.telegram

// Package sessionstore maps stable messenger chat/user keys to Coddy session IDs.
// Each unique (gateway, chatID, userID, isolation) combination yields a single session ID
// that is replaced when the user sends /clear, or bound to an existing one by /resume.
//
// When a save path is supplied via NewPersisted, the map is written atomically to disk on
// every mutation so the bot can resume existing conversations after a restart.
package sessionstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// Store maps session keys to Coddy session IDs.
type Store struct {
	mu       sync.Mutex
	data     map[string]string
	savePath string // empty = in-memory only
}

// New creates an in-memory store with no disk persistence.
func New() *Store {
	return &Store{data: make(map[string]string)}
}

// NewPersisted creates a Store backed by savePath.
// Any existing data is loaded immediately; a missing file is treated as an empty store.
func NewPersisted(savePath string) *Store {
	savePath = strings.TrimSpace(savePath)
	s := &Store{data: make(map[string]string), savePath: savePath}
	if savePath != "" {
		_ = os.MkdirAll(filepath.Dir(savePath), 0o755)
		_ = s.load() // missing file → fresh store, no error
	}
	return s
}

// SessionKey returns the string key for the given gateway/chat/user context.
// Private chats always use individual isolation regardless of the configured mode.
func SessionKey(gateway string, chatID, userID int64, mode config.IsolationMode, isGroup bool) string {
	if !isGroup {
		return fmt.Sprintf("%s:user:%d", gateway, userID)
	}
	switch mode {
	case config.IsolationShared:
		return fmt.Sprintf("%s:chat:%d", gateway, chatID)
	case config.IsolationAdmin:
		return fmt.Sprintf("%s:chat:%d:admin", gateway, chatID)
	default: // IsolationIndividual
		return fmt.Sprintf("%s:chat:%d:user:%d", gateway, chatID, userID)
	}
}

// Get returns the Coddy session ID for key, creating a new random one when absent.
func (s *Store) Get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.data[key]; ok {
		return id
	}
	id := newID()
	s.data[key] = id
	s.saveUnlocked()
	return id
}

// Peek returns the Coddy session ID mapped to key, or "" when the key has none.
// Unlike Get it never mints one, so a caller that only reports what exists -
// a log line, a status listing - leaves the store unchanged.
func (s *Store) Peek(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[key]
}

// Reset replaces the session ID for key with a fresh one and returns it.
// Used by the /clear command.
func (s *Store) Reset(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := newID()
	s.data[key] = id
	s.saveUnlocked()
	return id
}

// KeyFor returns the key that maps to sessionID. A background subagent asks
// about its parent session, not about a chat, and this is how the bot finds the
// conversation that session belongs to - after a restart too, since the map is
// persisted.
func (s *Store) KeyFor(sessionID string) (string, bool) {
	if sessionID == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, id := range s.data {
		if id == sessionID {
			return key, true
		}
	}
	return "", false
}

// ChatID returns the chat a session key addresses. A private conversation is
// keyed by the user, and Telegram gives a private chat the id of that user.
func ChatID(key string) (int64, bool) {
	parts := strings.Split(key, ":")
	if len(parts) < 3 {
		return 0, false
	}
	switch parts[1] {
	case "user", "chat":
	default:
		return 0, false
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// Bind maps key to a session that already exists - the /resume command, where
// a chat continues a session it did not start or left with /clear - and
// persists the mapping like every other mutation. An empty id is ignored:
// nothing may map a chat to no session, because Get would then answer ""
// instead of minting one.
func (s *Store) Bind(key, id string) {
	id = strings.TrimSpace(id)
	if key == "" || id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data[key] == id {
		return
	}
	s.data[key] = id
	s.saveUnlocked()
}

// KnownIDs returns all session IDs currently held in the store.
// Used on startup to pre-populate the "already seen" set so restarting the bot
// does not re-inject one-time initialization messages into existing sessions.
func (s *Store) KnownIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.data))
	for _, id := range s.data {
		ids = append(ids, id)
	}
	return ids
}

// load reads the persisted map from disk. Must be called before any concurrent access.
func (s *Store) load() error {
	data, err := os.ReadFile(s.savePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, &s.data)
}

// saveUnlocked writes the map to disk atomically. Must be called with mu held.
func (s *Store) saveUnlocked() {
	if s.savePath == "" {
		return
	}
	data, err := json.Marshal(s.data)
	if err != nil {
		return
	}
	dir := filepath.Dir(s.savePath)
	base := filepath.Base(s.savePath)
	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmpPath, s.savePath)
}

// newID generates a session ID. A conversation held in a messenger is an
// ordinary Coddy session - the same id shape a console run or a browser tab
// gets - so nothing downstream can tell where the person was sitting.
func newID() string {
	return session.NewSessionID()
}
