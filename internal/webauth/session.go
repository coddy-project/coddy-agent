package webauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"sort"
	"sync"
	"time"
)

// DefaultSessionTTL is how long a browser stays signed in when the
// configuration names no session_ttl_hours.
const DefaultSessionTTL = 30 * 24 * time.Hour

// maxSessions bounds the store. A single-operator server never needs many, and
// a cap means a script hammering the form cannot grow the process without end.
// The oldest entries go first, so the browsers actually in use survive.
const maxSessions = 512

// sessionTokenBytes is the entropy of a session token (256 bits, URL-safe).
const sessionTokenBytes = 32

// Session is one signed-in browser.
type Session struct {
	// User is the account the browser signed in as.
	User string
	// Credential is the fingerprint of the account that opened this session.
	// A rotated password produces a different fingerprint, which is what makes
	// every session it opened stop working without anything to invalidate.
	Credential string
	// IssuedAt and ExpiresAt bound the session's life on the server.
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// SessionStore keeps the live browser sessions of one server, in memory.
//
// They are deliberately not written to disk: the file would be a second copy of
// a credential, and a process that restarts is rare enough that signing in
// again is a fair price. A configuration reload does not restart the process,
// so saving settings from the browser never signs anybody out.
type SessionStore struct {
	// Now is the clock, overridable in tests. Nil means time.Now.
	Now func() time.Time

	mu       sync.Mutex
	sessions map[string]Session
}

// NewSessionStore returns an empty store.
func NewSessionStore() *SessionStore {
	return &SessionStore{sessions: make(map[string]Session)}
}

func (s *SessionStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Issue opens a session for user and returns its opaque token.
//
// credential is the fingerprint of the account behind it (CredentialFingerprint);
// ttl of zero or less falls back to DefaultSessionTTL, because a session the
// server keeps forever is not a session.
func (s *SessionStore) Issue(user, credential string, ttl time.Duration) (string, Session, error) {
	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", Session{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	now := s.now()
	sess := Session{User: user, Credential: credential, IssuedAt: now, ExpiresAt: now.Add(ttl)}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]Session)
	}
	s.sweepLocked(now)
	s.evictOldestLocked(maxSessions - 1)
	s.sessions[token] = sess
	return token, sess, nil
}

// Lookup returns the session behind token when it is live and was opened by the
// account credential describes. Anything else - unknown token, expired session,
// rotated password - is a miss, and the caller answers 401 to all of them alike.
func (s *SessionStore) Lookup(token, credential string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return Session{}, false
	}
	if !now.Before(sess.ExpiresAt) {
		delete(s.sessions, token)
		return Session{}, false
	}
	if subtle.ConstantTimeCompare([]byte(sess.Credential), []byte(credential)) != 1 {
		// The account moved under this session. Drop it here rather than leave
		// a row that can never match again.
		delete(s.sessions, token)
		return Session{}, false
	}
	return sess, true
}

// Revoke ends one session. Signing out is exactly this, and it is idempotent so
// a second click on a stale page is not an error.
func (s *SessionStore) Revoke(token string) {
	if token == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// RevokeAll ends every session, for a password change made in this process.
func (s *SessionStore) RevokeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = make(map[string]Session)
}

// Len reports how many sessions are held, expired ones swept first. Tests and
// the sign-in handler's bookkeeping use it; nothing else needs to know.
func (s *SessionStore) Len() int {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	return len(s.sessions)
}

// sweepLocked drops every session that has run out. Called on the paths that
// already hold the lock, so the map cannot grow unbounded with dead rows.
func (s *SessionStore) sweepLocked(now time.Time) {
	for tok, sess := range s.sessions {
		if !now.Before(sess.ExpiresAt) {
			delete(s.sessions, tok)
		}
	}
}

// evictOldestLocked trims the store down to keep entries, oldest first.
func (s *SessionStore) evictOldestLocked(keep int) {
	if keep < 0 {
		keep = 0
	}
	if len(s.sessions) <= keep {
		return
	}
	type row struct {
		token  string
		issued time.Time
	}
	rows := make([]row, 0, len(s.sessions))
	for tok, sess := range s.sessions {
		rows = append(rows, row{token: tok, issued: sess.IssuedAt})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].issued.Equal(rows[j].issued) {
			return rows[i].token < rows[j].token
		}
		return rows[i].issued.Before(rows[j].issued)
	})
	for i := 0; i < len(rows)-keep; i++ {
		delete(s.sessions, rows[i].token)
	}
}

// CredentialFingerprint identifies an account by what it is, not by what it is
// called: the user name and the password hash behind it. Sessions carry it, so
// rotating the password - or switching the account from the file to the
// environment - ends the sessions the old one opened, with no invalidation
// hook anywhere and no plaintext kept in memory.
func CredentialFingerprint(user, hash string) string {
	sum := sha256.Sum256([]byte(user + "\x00" + hash))
	return hex.EncodeToString(sum[:8])
}
