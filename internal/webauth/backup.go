package webauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
)

// BackupState is the persistable representation of a set; it never holds codes.
type BackupState struct {
	Generation string   `json:"generation" yaml:"generation"`
	Hashes     []string `json:"hashes" yaml:"hashes"`
}

// MaxBackupCodes bounds the number of codes generated in one set.
const MaxBackupCodes = 64

// GenerateBackupCodes returns a fresh set and the only plaintext copy of its codes.
func GenerateBackupCodes(count int) (BackupState, []string, error) {
	if count <= 0 || count > MaxBackupCodes {
		return BackupState{}, nil, fmt.Errorf("backup code count must be between 1 and %d", MaxBackupCodes)
	}
	var generation [16]byte
	if _, err := rand.Read(generation[:]); err != nil {
		return BackupState{}, nil, fmt.Errorf("generate backup generation: %w", err)
	}
	state := BackupState{Generation: hex.EncodeToString(generation[:]), Hashes: make([]string, 0, count)}
	codes := make([]string, 0, count)
	for len(codes) < count {
		var raw [8]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return BackupState{}, nil, fmt.Errorf("generate backup code: %w", err)
		}
		text := strings.ToUpper(hex.EncodeToString(raw[:]))
		code := text[:8] + "-" + text[8:]
		hash := backupHash(code)
		duplicate := false
		for _, old := range state.Hashes {
			if subtle.ConstantTimeCompare([]byte(old), []byte(hash)) == 1 {
				duplicate = true
			}
		}
		if duplicate {
			continue
		}
		codes = append(codes, code)
		state.Hashes = append(state.Hashes, hash)
	}
	return state, codes, nil
}

func backupHash(code string) string {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.Join(strings.Fields(code), ""), "-", ""))
	if len(normalized) == 16 {
		normalized = normalized[:8] + "-" + normalized[8:]
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// BackupStore serializes consumption within one process. Persist snapshots under
// the caller's transaction lock before granting authentication.
type BackupStore struct {
	mu    sync.Mutex
	state BackupState
}

func NewBackupStore(state BackupState) *BackupStore {
	state.Hashes = append([]string(nil), state.Hashes...)
	return &BackupStore{state: state}
}

// Consume checks all stored hashes and removes a match atomically.
func (s *BackupStore) Consume(code string) bool {
	hash := backupHash(code)
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i, candidate := range s.state.Hashes {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(hash)) == 1 {
			index = i
		}
	}
	if index < 0 {
		return false
	}
	s.state.Hashes = append(s.state.Hashes[:index], s.state.Hashes[index+1:]...)
	return true
}

// Snapshot copies the remaining hashes and the unchanged generation.
func (s *BackupStore) Snapshot() BackupState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return BackupState{Generation: s.state.Generation, Hashes: append([]string(nil), s.state.Hashes...)}
}
