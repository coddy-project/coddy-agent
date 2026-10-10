package config

import (
	"fmt"
	"strings"
)

// SessionsDefaultMinFreeMB is the free space, in MB, below which Coddy warns
// that the volume holding the sessions is running out (sessions.min_free_mb
// when the file leaves it out). A session is rewritten whole on every save, so
// a long transcript needs room for a second copy of itself while it is
// replaced; 512 MB is far above that for any session Coddy keeps and still
// small enough not to warn on a machine with a healthy disk.
const SessionsDefaultMinFreeMB = 512

// Sessions is the YAML sessions section (key sessions).
type Sessions struct {
	// Dir is the filesystem root for persisted session bundles. Empty means
	// <Paths.Home>/sessions or ~/.coddy/sessions when Home is unset.
	Dir string `yaml:"dir"`
	// MinFreeMB is the free space, in MB, on the volume holding the sessions
	// folder (and on the one holding the home folder, when that is another)
	// below which the web UI shows a low-space banner, GET /coddy/info reports
	// the state "low" and --dry-run warns. A nil pointer means the default
	// (SessionsDefaultMinFreeMB); an explicit 0 turns the warning off.
	MinFreeMB *int `yaml:"min_free_mb"`
}

// MinFreeBytes returns sessions.min_free_mb in bytes with the default applied;
// 0 means the warning is off.
func (s *Sessions) MinFreeBytes() uint64 {
	mb := SessionsDefaultMinFreeMB
	if s.MinFreeMB != nil {
		mb = max(*s.MinFreeMB, 0)
	}
	// The bound keeps the shift below from wrapping to a smaller number, and
	// so a huge threshold from reading as a small one; no volume has 2^43 MB.
	return min(uint64(mb), 1<<43) << 20
}

// Validate trims Dir in place and refuses a negative threshold.
func (s *Sessions) Validate() error {
	s.Dir = strings.TrimSpace(s.Dir)
	if s.MinFreeMB != nil && *s.MinFreeMB < 0 {
		return fmt.Errorf("sessions.min_free_mb: must be >= 0")
	}
	return nil
}
