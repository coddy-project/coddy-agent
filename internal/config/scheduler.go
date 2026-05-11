package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// SchedulerConfig controls the optional cron-driven scheduler
type SchedulerConfig struct {
	Enabled bool `yaml:"enabled"`

	// Dir is the filesystem root containing *.md job definitions. Empty defaults to ${CODDY_HOME}/scheduler.
	Dir string `yaml:"dir"`

	// MaxQueue caps concurrent scheduled sub-agent runs (default 10). When saturated, pending jobs are skipped until a slot frees.
	MaxQueue int `yaml:"max_queue"`

	// Timeout limits one scheduled agent run (LLM + tools), e.g. "30m".
	Timeout string `yaml:"timeout"`

	// RetainSessions keeps at most N completed scheduler-run session dirs per job_id under sessions.dir (default 5 when unset or 0).
	RetainSessions int `yaml:"retain_sessions"`
}

// SchedulerEffectiveEnabled reports whether the scheduler daemon and tools are active for this process.
func (c *Config) SchedulerEffectiveEnabled() bool {
	return c != nil && c.Scheduler.Enabled
}

// SchedulerScanRoots returns normalized job scan directories (currently a single Dir after defaults).
func (c *Config) SchedulerScanRoots() []string {
	if c == nil {
		return nil
	}
	d := strings.TrimSpace(c.Scheduler.Dir)
	if d == "" {
		return nil
	}
	return []string{filepath.Clean(d)}
}

// Normalize trims scheduler paths using CODDY_HOME expansion.
func (s *SchedulerConfig) Normalize(p Paths) {
	s.Dir = strings.TrimSpace(s.Dir)
	if s.Dir != "" {
		s.Dir = filepath.Clean(ExpandCODDYHomeOnly(s.Dir, p))
	}
	s.Timeout = strings.TrimSpace(s.Timeout)
}

// ApplyDefaults fills scheduler defaults after Normalize.
func (s *SchedulerConfig) ApplyDefaults(p Paths) {
	if s.MaxQueue <= 0 {
		s.MaxQueue = 10
	}
	if s.Timeout == "" {
		s.Timeout = "30m"
	}
	if s.Dir == "" {
		if p.Home != "" {
			s.Dir = filepath.Join(p.Home, "scheduler")
		} else {
			s.Dir = filepath.Join(p.CWD, ".scheduler")
		}
	}
	if s.RetainSessions <= 0 {
		s.RetainSessions = 5
	}
}

// SchedulerRetainSessionsEffective returns retained completed run dirs per job (after defaults).
func (c *Config) SchedulerRetainSessionsEffective() int {
	if c == nil {
		return 5
	}
	n := c.Scheduler.RetainSessions
	if n <= 0 {
		return 5
	}
	return n
}

// Validate checks scheduler settings when enabled (effective).
func (s *SchedulerConfig) Validate(cfg *Config) error {
	if cfg == nil || !cfg.SchedulerEffectiveEnabled() {
		return nil
	}
	if _, err := time.ParseDuration(s.Timeout); err != nil {
		return fmt.Errorf("scheduler.timeout: %w", err)
	}
	if strings.TrimSpace(s.Dir) == "" {
		return fmt.Errorf("scheduler.dir resolved empty")
	}
	return nil
}
