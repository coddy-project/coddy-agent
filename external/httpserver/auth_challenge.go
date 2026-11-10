//go:build http

package httpserver

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/webauth"
)

// Challenges expire after three minutes, even if no verification is attempted.
const challengeTTL = 3 * time.Minute
const maxChallenges = 4096

type challengeEntry struct {
	fingerprint string
	expires     time.Time
}
type challengeStore struct {
	mu      sync.Mutex
	entries map[string]challengeEntry
}

func (c *challengeStore) issue(fingerprint string, now time.Time) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]challengeEntry)
	}
	for k, v := range c.entries {
		if !now.Before(v.expires) {
			delete(c.entries, k)
		}
	}
	if len(c.entries) >= maxChallenges {
		return "", errors.New("too many pending challenges")
	}
	c.entries[token] = challengeEntry{fingerprint, now.Add(challengeTTL)}
	return token, nil
}
func (c *challengeStore) take(token string, now time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[token]
	delete(c.entries, token)
	return entry.fingerprint, ok && now.Before(entry.expires)
}
func (s *Server) coddyAuthLoginVerifyPost(w http.ResponseWriter, r *http.Request) {
	if !isSameOriginRequest(r) {
		writeJSON(w, 403, map[string]interface{}{"error": "cross-site sign-in refused"})
		return
	}
	var req struct {
		Challenge string `json:"challenge"`
		Code      string `json:"code"`
	}
	if err := enrollmentBody(r, &req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"error": "invalid JSON body"})
		return
	}
	client := loginClientAddr(r)
	if d := s.loginThrottle.Penalize(client); d > 0 {
		select {
		case <-time.After(d):
		case <-r.Context().Done():
			return
		}
	}
	fp, ok := s.challenges.take(req.Challenge, s.authNow())
	pol := s.loginPolicyNow()
	if !ok || !pol.enabled || pol.broken || pol.account.totpSecret == "" || fp != pol.account.fingerprint() {
		writeJSON(w, 401, map[string]interface{}{"error": "invalid credentials"})
		return
	}
	valid := s.verifySecondFactor(pol.account, req.Code)
	if !valid {
		var err error
		valid, err = s.consumeLoginBackup(fp, req.Code)
		if err != nil {
			s.log.Error("backup code persistence failed", "error", err)
			writeJSON(w, 500, map[string]interface{}{"error": "could not update authentication"})
			return
		}
	}
	if !valid {
		writeJSON(w, 401, map[string]interface{}{"error": "invalid credentials"})
		return
	}
	// Recheck after verification so a rotation cannot yield a usable old session.
	pol = s.loginPolicyNow()
	if !pol.enabled || pol.broken || pol.account.fingerprint() != fp {
		writeJSON(w, 401, map[string]interface{}{"error": "invalid credentials"})
		return
	}
	s.loginThrottle.Reset(client)
	s.issueLoginSession(w, r, pol.account, client)
}

func (s *Server) consumeLoginBackup(fingerprint, code string) (bool, error) {
	consumed := false
	err := config.WithConfigFileLock(func() error {
		pol := s.loginPolicyNow()
		if !pol.enabled || pol.broken || pol.account.fingerprint() != fingerprint {
			return nil
		}
		c := s.activeCfg()
		if c == nil {
			return errors.New("configuration unavailable")
		}
		store := webauth.NewBackupStore(c.HTTPServer.Login.BackupCodes)
		if !store.Consume(code) {
			return nil
		}
		next := *c
		next.HTTPServer.Login.BackupCodes = store.Snapshot()
		doc, err := json.Marshal(config.ConfigToJSONDTO(&next))
		if err != nil {
			return err
		}
		if _, err = config.ParseConfigJSONPreservingSecrets(doc, next.Paths, &next); err != nil {
			return err
		}
		path := c.Paths.ConfigPath
		original, readErr := os.ReadFile(path)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		data, err := config.MarshalConfigYAMLForEdit(&next, c, c, path)
		if err != nil {
			return err
		}
		loaded, err := config.ParseConfigYAML(data, c.Paths)
		if err != nil {
			return err
		}
		if loaded.HTTPServer.Login.TOTPSecret != next.HTTPServer.Login.TOTPSecret || !reflect.DeepEqual(loaded.HTTPServer.Login.BackupCodes, next.HTTPServer.Login.BackupCodes) {
			return errors.New("rendered authentication differs from requested state")
		}
		if err = config.BackupCurrent(path); err != nil {
			return err
		}
		if err = config.AtomicWriteConfigYAML(path, data); err != nil {
			return err
		}
		if err = s.mgr.InstallConfig(loaded); err != nil {
			var rollbackErr error
			if errors.Is(readErr, os.ErrNotExist) {
				rollbackErr = os.Remove(path)
			} else {
				rollbackErr = config.AtomicWriteFile(path, original, 0600)
			}
			if rollbackErr != nil {
				s.log.Error("authentication config rollback failed", "error", rollbackErr)
			}
			return err
		}
		consumed = true
		return nil
	})
	return consumed, err
}
