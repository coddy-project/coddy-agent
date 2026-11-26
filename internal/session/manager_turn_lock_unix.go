//go:build unix

package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const turnLockFileName = ".coddy-turn.lock"

type turnLockMeta struct {
	PID     int    `json:"pid"`
	Started string `json:"started"`
}

func (m *Manager) acquirePromptTurnLock(sessionID string, st *State) (unlock func(), err error) {
	dir := strings.TrimSpace(st.GetPersistedSessionDir())
	if dir == "" {
		return m.acquireStubTurnLock(sessionID)
	}
	return acquireTurnLockFlock(dir)
}

// TurnLockHeld reports whether another holder has an exclusive flock on the turn lock file.
func TurnLockHeld(sessionDir string) bool {
	dir := strings.TrimSpace(sessionDir)
	if dir == "" {
		return false
	}
	path := filepath.Join(dir, turnLockFileName)
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return true
		}
		return false
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

func acquireTurnLockFlock(sessionDir string) (unlock func(), err error) {
	path := filepath.Join(sessionDir, turnLockFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("turn lock open: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrSessionTurnBusy
		}
		return nil, fmt.Errorf("turn lock flock: %w", err)
	}
	meta := turnLockMeta{PID: os.Getpid(), Started: time.Now().UTC().Format(time.RFC3339)}
	b, err := json.Marshal(meta)
	if err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, err
	}
	if err := f.Truncate(0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, err
	}
	if _, err := f.WriteAt(b, 0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func (m *Manager) acquireDeleteTurnLock(id, dir string) (func(), error) {
	return acquireTurnLockFlock(dir)
}
