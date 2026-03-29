package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// StoredSession is the persistent representation of a TUI session.
type StoredSession struct {
	ID        string        `json:"id"`
	CWD       string        `json:"cwd"`
	Mode      string        `json:"mode"`
	Messages  []llm.Message `json:"messages"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
}

// SessionStore manages saving and loading TUI sessions to disk.
type SessionStore struct {
	dir string
}

// NewSessionStore creates a store that persists sessions under dir.
func NewSessionStore(dir string) (*SessionStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create session dir %s: %w", dir, err)
	}
	return &SessionStore{dir: dir}, nil
}

// DefaultSessionDir returns the default session storage directory.
func DefaultSessionDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".coddy-agent", "sessions")
	}
	return filepath.Join(home, ".local", "share", "coddy-agent", "sessions")
}

// Save persists a session to disk.
func (s *SessionStore) Save(sess *StoredSession) error {
	sess.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	path := s.sessionPath(sess.ID)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write session %s: %w", path, err)
	}
	return nil
}

// Load reads a session from disk by ID.
func (s *SessionStore) Load(id string) (*StoredSession, error) {
	path := s.sessionPath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session %q not found", id)
		}
		return nil, fmt.Errorf("read session %s: %w", path, err)
	}
	var sess StoredSession
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("parse session %s: %w", path, err)
	}
	return &sess, nil
}

// Delete removes a session file from disk.
func (s *SessionStore) Delete(id string) error {
	path := s.sessionPath(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete session %s: %w", path, err)
	}
	return nil
}

// List returns all stored session IDs, sorted newest first.
func (s *SessionStore) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			ids = append(ids, e.Name()[:len(e.Name())-5])
		}
	}
	return ids, nil
}

func (s *SessionStore) sessionPath(id string) string {
	return filepath.Join(s.dir, id+".json")
}
