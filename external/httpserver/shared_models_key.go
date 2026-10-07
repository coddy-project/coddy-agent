//go:build http

package httpserver

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// sharedModelsKeyFile is the per-install secret that keys the revision of a
// shared row and the tag of the model a reasoning signature belongs to. It
// lives in the agent home, is created on first use with mode 0600 and is read
// again after a restart, so a revision and an envelope are the same before and
// after one while nothing changed. It is never served and never logged.
const sharedModelsKeyFile = "shared-models.key"

// sharedModelsKeyBytes is the size of the key: 32 random bytes, stored as hex.
const sharedModelsKeyBytes = 32

// sharedModelsKey returns the key of this install, creating the file on first
// use. A key that cannot be persisted (no agent home, a read-only folder) is
// kept in memory for the life of the process and reported once: revisions and
// envelopes then stay valid until a restart, after which the clients refresh
// their view once and drop the signatures of earlier calls, which is safe.
func (s *Server) sharedModelsKey() []byte {
	s.sharedKeyMu.Lock()
	defer s.sharedKeyMu.Unlock()
	if len(s.sharedKey) > 0 {
		return s.sharedKey
	}
	home := ""
	if c := s.activeCfg(); c != nil {
		home = strings.TrimSpace(c.Paths.Home)
	}
	key, err := loadOrCreateSharedKey(home)
	if err != nil {
		s.log.Warn("the shared-model key could not be stored, using one for this process only: revisions and signature envelopes will not survive a restart",
			"file", sharedModelsKeyFile, "error", err)
		key = make([]byte, sharedModelsKeyBytes)
		_, _ = rand.Read(key)
	}
	s.sharedKey = key
	return key
}

// loadOrCreateSharedKey reads <home>/shared-models.key, or writes a new one.
func loadOrCreateSharedKey(home string) ([]byte, error) {
	if home == "" {
		return nil, errors.New("no agent home")
	}
	path := filepath.Join(home, sharedModelsKeyFile)
	if key, err := readSharedKey(path); err == nil {
		return key, nil
	}
	// No file, or a damaged one that is replaced below. The key is written to a
	// private temporary file first and only then given its name, so the file at
	// path is never seen empty or half written: of two processes starting
	// together on one home, one publishes its key and the other reads that one.
	key := make([]byte, sharedModelsKeyBytes)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(home, sharedModelsKeyFile+".*")
	if err != nil {
		return nil, err
	}
	// After a successful publish the temporary name is a second link to the
	// file, or gone; either way removing it is right.
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, werr := tmp.WriteString(hex.EncodeToString(key) + "\n")
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		return nil, errors.Join(werr, cerr)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return nil, err
	}
	// A hard link fails when the name exists, which a rename would not: the
	// loser of the race reads the winner's file.
	err = os.Link(tmp.Name(), path)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	if existing, rerr := readSharedKey(path); rerr == nil {
		return existing, nil
	}
	// The file that is there is damaged: replace it atomically, nobody can hold a
	// revision or a signature made with a key that cannot be read.
	if err := os.Rename(tmp.Name(), path); err != nil {
		return nil, err
	}
	return key, nil
}

// readSharedKey reads and checks a stored key.
func readSharedKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != sharedModelsKeyBytes {
		return nil, fmt.Errorf("%s is not a key written by Coddy", sharedModelsKeyFile)
	}
	return key, nil
}
