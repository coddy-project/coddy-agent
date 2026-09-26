//go:build http

package httpserver

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// servedConfigsKept bounds how many configurations GET /coddy/config remembers. A
// document older than that - a form left open across more swaps of the live
// configuration - is measured against the live one, as every save used to be.
const servedConfigsKept = 32

// servedConfigs remembers the configurations GET /coddy/config handed out, each
// under the revision the document carries (config.ConfigJSON.Revision). A PUT
// compares what a client sends back with what that client read, rather than with
// whatever is live when the PUT arrives: another save, the agent's config_commit or
// a hand edit may have replaced it in between, and a value the client never touched
// must not put back what it was shown.
type servedConfigs struct {
	mu sync.Mutex
	// epoch tells this process's revisions from the ones a client may still hold
	// from before a restart, which name nothing here.
	epoch string
	seq   uint64
	byRev map[string]*config.Config
	revOf map[*config.Config]string
	order []string
}

func newServedConfigs() *servedConfigs {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return &servedConfigs{
		epoch: hex.EncodeToString(b[:]),
		byRev: map[string]*config.Config{},
		revOf: map[*config.Config]string{},
	}
}

// revision returns the revision of c, remembering c under it. Every swap installs
// a new configuration, so one live configuration keeps one revision for as long as
// it is live.
func (s *servedConfigs) revision(c *config.Config) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rev, ok := s.revOf[c]; ok {
		return rev
	}
	s.seq++
	rev := s.epoch + "-" + strconv.FormatUint(s.seq, 10)
	s.byRev[rev] = c
	s.revOf[c] = rev
	s.order = append(s.order, rev)
	for len(s.order) > servedConfigsKept {
		old := s.order[0]
		s.order = s.order[1:]
		delete(s.revOf, s.byRev[old])
		delete(s.byRev, old)
	}
	return rev
}

// lookup returns the configuration served under rev, nil when rev names none this
// process remembers.
func (s *servedConfigs) lookup(rev string) *config.Config {
	if rev == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byRev[rev]
}
