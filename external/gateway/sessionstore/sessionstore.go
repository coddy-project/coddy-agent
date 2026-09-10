//go:build gateway || gateway.telegram

// Package sessionstore maps stable messenger chat/user keys to Coddy session IDs.
// Each unique (gateway, chatID, userID, isolation) combination yields a single session ID
// that is replaced when the user sends /clear.
//
// When a save path is supplied via NewPersisted, the map is written atomically to disk on
// every mutation so the bot can resume existing conversations after a restart.
package sessionstore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
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

// newID generates a random session ID with the gw_ prefix.
func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("gateway sessionstore: crypto/rand unavailable: " + err.Error())
	}
	return "gw_" + strings.ToLower(hex.EncodeToString(b))
}
