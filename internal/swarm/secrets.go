package swarm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileSecretStore keeps lease secrets under the coddy home so a node that
// restarts re-claims its own name immediately, instead of being locked out of
// it until the relay's lease expires.
//
// The file holds credentials, so it is written with owner-only permissions.
type FileSecretStore struct {
	path string

	mu     sync.Mutex
	loaded bool
	data   map[string]string
}

// NewFileSecretStore returns a store backed by <home>/swarm-leases.json.
func NewFileSecretStore(home string) *FileSecretStore {
	return &FileSecretStore{path: filepath.Join(home, "swarm-leases.json")}
}

func leaseKey(relayURL, name string) string { return relayURL + "\x00" + name }

// Load returns the stored secret for one relay and name.
func (s *FileSecretStore) Load(relayURL, name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return "", false
	}
	secret, ok := s.data[leaseKey(relayURL, name)]
	return secret, ok && secret != ""
}

// Save records the secret, replacing any previous one.
func (s *FileSecretStore) Save(relayURL, name, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return err
	}
	s.data[leaseKey(relayURL, name)] = secret
	return s.saveLocked()
}

func (s *FileSecretStore) loadLocked() error {
	if s.loaded {
		return nil
	}
	s.data = map[string]string{}
	s.loaded = true
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", s.path, err)
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		// A corrupt file is not worth failing a startup over: the node simply
		// re-claims its name the slow way.
		s.data = map[string]string{}
	}
	return nil
}

func (s *FileSecretStore) saveLocked() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// MemorySecretStore is the in-process equivalent, for tests and for a node that
// deliberately keeps nothing on disk.
type MemorySecretStore struct {
	mu   sync.Mutex
	data map[string]string
}

// Load returns the stored secret.
func (m *MemorySecretStore) Load(relayURL, name string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	secret, ok := m.data[leaseKey(relayURL, name)]
	return secret, ok
}

// Save records the secret.
func (m *MemorySecretStore) Save(relayURL, name, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string]string{}
	}
	m.data[leaseKey(relayURL, name)] = secret
	return nil
}
