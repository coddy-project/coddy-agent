package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// SchedulerConfig controls the optional cron-driven scheduler
type SchedulerConfig struct {
	Enabled bool `yaml:"enable"`

	// MaxQueue caps concurrent scheduled sub-agent runs (default 10). When saturated, pending jobs are skipped until a slot frees.
	MaxQueue int `yaml:"max_queue"`

	// Timeout limits one scheduled agent run (LLM + tools), e.g. "30m".
	Timeout string `yaml:"timeout"`

	// ProjectTrust decides what a job found in <workspace>/.coddy/scheduler
	// may do: ask (default: listed, run only once approved for that exact
	// content), allow (run like the operator's own jobs), or deny (never run).
	// Same vocabulary as hooks.project_trust and mcp.project_trust.
	ProjectTrust string `yaml:"project_trust"`

	// RetainSessions keeps at most N completed scheduler-run session dirs per job_id under sessions.dir (default 5 when unset or 0).
	RetainSessions int `yaml:"retain_sessions"`
}

// SchedulerEffectiveEnabled reports whether the scheduler daemon and tools are active for this process.
func (c *Config) SchedulerEffectiveEnabled() bool {
	return c != nil && c.Scheduler.Enabled
}

// SchedulerUserDirName is the folder of the user jobs inside the coddy home.
const SchedulerUserDirName = "scheduler"

// SchedulerProjectDir is the folder of a workspace's project jobs, relative to
// the workspace root.
var SchedulerProjectDir = filepath.Join(".coddy", "scheduler")

// SchedulerUserDir is where the user jobs live: ${CODDY_HOME}/scheduler. The
// location is fixed; a process with no home (tests) falls back to
// <cwd>/.scheduler.
func (c *Config) SchedulerUserDir() string {
	if c == nil {
		return ""
	}
	return SchedulerUserDirFor(c.Paths)
}

// SchedulerUserDirFor is SchedulerUserDir for a set of paths.
func SchedulerUserDirFor(p Paths) string {
	if h := strings.TrimSpace(p.Home); h != "" {
		return filepath.Join(filepath.Clean(h), SchedulerUserDirName)
	}
	if cwd := strings.TrimSpace(p.CWD); cwd != "" {
		return filepath.Join(filepath.Clean(cwd), ".scheduler")
	}
	return ""
}

// Normalize trims the scheduler settings.
func (s *SchedulerConfig) Normalize(_ Paths) {
	s.Timeout = strings.TrimSpace(s.Timeout)
}

// ApplyDefaults fills scheduler defaults after Normalize.
func (s *SchedulerConfig) ApplyDefaults(_ Paths) {
	if s.MaxQueue <= 0 {
		s.MaxQueue = 10
	}
	if s.Timeout == "" {
		s.Timeout = "30m"
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

// ResolvedProjectTrust returns ProjectTrust with the safe default of ask.
func (s SchedulerConfig) ResolvedProjectTrust() string {
	switch v := strings.ToLower(strings.TrimSpace(s.ProjectTrust)); v {
	case ProjectTrustAllow, ProjectTrustDeny:
		return v
	default:
		return ProjectTrustAsk
	}
}

// Validate checks scheduler settings: project_trust always, the rest when the
// scheduler is enabled.
func (s *SchedulerConfig) Validate(cfg *Config) error {
	switch v := strings.ToLower(strings.TrimSpace(s.ProjectTrust)); v {
	case "":
		s.ProjectTrust = ""
	case ProjectTrustAsk, ProjectTrustAllow, ProjectTrustDeny:
		s.ProjectTrust = v
	default:
		return fmt.Errorf("scheduler.project_trust: must be one of %q, %q, %q (got %q)",
			ProjectTrustAsk, ProjectTrustAllow, ProjectTrustDeny, s.ProjectTrust)
	}
	if cfg == nil || !cfg.SchedulerEffectiveEnabled() {
		return nil
	}
	if _, err := time.ParseDuration(s.Timeout); err != nil {
		return fmt.Errorf("scheduler.timeout: %w", err)
	}
	if cfg.SchedulerUserDir() == "" {
		return fmt.Errorf("scheduler: no coddy home to keep the jobs in")
	}
	return nil
}
