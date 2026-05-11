//go:build scheduler

// Package storage holds filesystem and serialization helpers for flat *.md scheduler jobs.
package storage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

// ScheduleMinimumInterval returns the wall-clock gap between two consecutive cron fires from a
// fixed anchor (robfig's Next twice). Used to enforce at least that much time between process
// spawn times so a long run that crosses into the next cron minute does not immediately start
// another execution ("not more than once per minute" for * * * * * in practice).
func ScheduleMinimumInterval(sched cron.Schedule) time.Duration {
	t0 := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := sched.Next(t0).UTC()
	t2 := sched.Next(t1).UTC()
	d := t2.Sub(t1)
	if d <= 0 {
		return 0
	}
	return d
}

var standardParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ParseCronUTC parses a 5-field crontab expression interpreted in UTC.
func ParseCronUTC(spec string) (cron.Schedule, error) {
	return standardParser.Parse(strings.TrimSpace(spec))
}

// CronEpoch is the anchor used before any recorded fire time.
func CronEpoch() time.Time {
	return time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
}

// NextScheduledUTC returns the first scheduled instant strictly after lastFiredSlot (RFC cron semantics).
// When lastFiredSlot is zero, the anchor is CronEpoch (historical tests and callers that need that contract).
func NextScheduledUTC(sched cron.Schedule, lastFiredSlot time.Time) time.Time {
	t := lastFiredSlot
	if t.IsZero() {
		t = CronEpoch()
	}
	return sched.Next(t).UTC()
}

// staleCheckpointYear treats .state checkpoints before this as missing (epoch-era bug or corrupt file).
const staleCheckpointYear = 1980

// TruncateUTCToMinute returns t in UTC with seconds and sub-second fields zeroed.
func TruncateUTCToMinute(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), u.Hour(), u.Minute(), 0, 0, time.UTC)
}

// CronMinuteMatchesUTC reports whether the five-field schedule fires at the given UTC minute start
// (second and nanosecond must be zero; vixie-style minute grid).
func CronMinuteMatchesUTC(sched cron.Schedule, minuteStart time.Time) bool {
	t := TruncateUTCToMinute(minuteStart)
	n := sched.Next(t.Add(-time.Nanosecond)).UTC()
	return n.Equal(t)
}

// CronJobEligibleForMinute reports whether the daemon should start a run for evalMinute (UTC minute start),
// given last checkpoint from .state. Stale or zero last is treated as no durable prior run.
func CronJobEligibleForMinute(sched cron.Schedule, lastFromState, evalMinute time.Time) bool {
	eval := TruncateUTCToMinute(evalMinute)
	last := lastFromState.UTC()
	if !last.IsZero() && last.Year() >= staleCheckpointYear {
		if !last.Before(eval) {
			return false
		}
	}
	return CronMinuteMatchesUTC(sched, eval)
}

// DueFireSlotUTC is kept for tests and legacy callers. The daemon uses CronJobEligibleForMinute on a
// UTC minute tick instead of sub-minute polling. It returns the cron instant the daemon would treat as due
// at wall-clock now when driven by sub-minute polls.
// With no durable checkpoint (zero last, or stale pre-1980 timestamps from old epoch anchoring), it behaves
// like vixie crontab for a new line: the next fire follows the schedule from real time, not from Unix epoch.
// With a normal last checkpoint, it is the first scheduled instant strictly after that last fire.
func DueFireSlotUTC(sched cron.Schedule, lastFiredSlot time.Time, now time.Time) time.Time {
	now = now.UTC()
	last := lastFiredSlot.UTC()
	if !last.IsZero() && last.Year() >= staleCheckpointYear {
		return sched.Next(last).UTC()
	}
	// Anchor just before the current minute so robfig's strictly-after Next still lands on the current
	// minute boundary when the tick falls later in the same minute (poll interval can be > 1s).
	anchor := now.Truncate(time.Minute).Add(-time.Second)
	return sched.Next(anchor).UTC()
}

// NextScheduledDisplayUTC returns the next cron instant strictly after max(lastFiredSlot, now) for UI lists.
// When lastFiredSlot is zero (never recorded), anchors at now instead of CronEpoch so clients do not show 1970.
// Instants are on the UTC minute grid (five-field crontab), matching the daemon's CronJobEligibleForMinute model.
func NextScheduledDisplayUTC(sched cron.Schedule, lastFiredSlot time.Time, now time.Time) time.Time {
	now = now.UTC()
	anchor := lastFiredSlot.UTC()
	if anchor.IsZero() {
		return sched.Next(now).UTC()
	}
	if anchor.Before(now) {
		anchor = now
	}
	return sched.Next(anchor).UTC()
}

// JobFrontmatter is YAML metadata for a scheduler job file (skills-style description field).
type JobFrontmatter struct {
	Description string `yaml:"description"`
	Schedule    string `yaml:"schedule"`
	Paused      bool   `yaml:"paused"`
	CWD         string `yaml:"cwd"`
	Model       string `yaml:"model"`
	Mode        string `yaml:"mode"`
}

// ParseJobFile reads a markdown job file and returns frontmatter, instruction body, or error.
func ParseJobFile(path string) (*JobFrontmatter, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	body, fm := splitFrontmatter(data)
	if fm == nil {
		return nil, "", fmt.Errorf("missing YAML frontmatter")
	}
	if strings.TrimSpace(fm.Schedule) == "" {
		return fm, strings.TrimSpace(body), fmt.Errorf("schedule is required in frontmatter")
	}
	return fm, strings.TrimSpace(body), nil
}

func splitFrontmatter(data []byte) (string, *JobFrontmatter) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) < 3 || lines[0] != "---" {
		return string(data), nil
	}
	endIdx := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			endIdx = i
			break
		}
	}
	if endIdx < 0 {
		return string(data), nil
	}
	fmContent := strings.Join(lines[1:endIdx], "\n")
	body := strings.Join(lines[endIdx+1:], "\n")
	var fm JobFrontmatter
	if err := yaml.Unmarshal([]byte(fmContent), &fm); err != nil {
		return body, nil
	}
	return body, &fm
}

// FormatJobMarkdown serializes frontmatter plus optional markdown instruction body for a *.md scheduler job file.
func FormatJobMarkdown(fm *JobFrontmatter, body string) ([]byte, error) {
	if fm == nil {
		return nil, fmt.Errorf("nil job frontmatter")
	}
	head, err := yaml.Marshal(fm)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(head)
	if len(head) > 0 && head[len(head)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("---\n")
	body = strings.TrimRight(body, "\n")
	if strings.TrimSpace(body) != "" {
		b.WriteString(body)
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

// ParseJobFromBytes validates frontmatter for programmatic writes.
func ParseJobFromBytes(data []byte) (*JobFrontmatter, error) {
	body, fm := splitFrontmatter(data)
	if fm == nil {
		return nil, fmt.Errorf("missing YAML frontmatter")
	}
	if strings.TrimSpace(fm.Schedule) == "" {
		return nil, fmt.Errorf("schedule is required")
	}
	if _, err := ParseCronUTC(fm.Schedule); err != nil {
		return nil, err
	}
	_ = body
	return fm, nil
}

// ListFlatJobMarkdownFiles returns *.md job files immediately under each scheduler root (non-recursive, no subfolders).
func ListFlatJobMarkdownFiles(roots []string) ([]string, error) {
	var out []string
	seen := map[string]struct{}{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		de, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, ent := range de {
			if ent.IsDir() {
				continue
			}
			name := ent.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if !strings.HasSuffix(strings.ToLower(name), ".md") {
				continue
			}
			path := filepath.Join(root, name)
			ap, err := filepath.Abs(path)
			if err != nil {
				continue
			}
			canonical := CanonicalSchedulerJobPath(ap)
			if canonical == "" {
				canonical = ap
			}
			if _, ok := seen[canonical]; ok {
				continue
			}
			seen[canonical] = struct{}{}
			out = append(out, canonical)
		}
	}
	sort.Strings(out)
	return out, nil
}

// CanonicalSchedulerJobPath returns an absolute path for a *.md job file. When possible,
// symbolic link segments are resolved so scans via symlinked scheduler dirs share .state,
// lock files, and in-process dedupe keys with the resolved location.
func CanonicalSchedulerJobPath(jobMDPath string) string {
	p := strings.TrimSpace(jobMDPath)
	if p == "" {
		return ""
	}
	ap, err := filepath.Abs(p)
	if err != nil {
		ap = filepath.Clean(p)
	}
	if sym, err := filepath.EvalSymlinks(ap); err == nil {
		return sym
	}
	return ap
}

// LockPath returns the lock file path for a job .md path.
func LockPath(jobMDPath string) string {
	base := strings.TrimSuffix(filepath.Base(jobMDPath), ".md")
	return filepath.Join(filepath.Dir(jobMDPath), base+".lock")
}

// ReadSchedulerLockFireSlotUTC reads the first line of a job .lock file as an RFC3339 instant in UTC.
// The daemon writes the committed cron fire slot there so ticks between lock creation and .state
// rename still see that this fire is already in progress.
// It returns (zero, false) when the file is missing, empty, or the first line is not valid RFC3339.
func ReadSchedulerLockFireSlotUTC(lockPath string) (time.Time, bool) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return time.Time{}, false
	}
	first := strings.Split(string(data), "\n")[0]
	line := strings.TrimSpace(first)
	if line == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, line)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// StatePath returns the state file path for a job .md path.
func StatePath(jobMDPath string) string {
	base := strings.TrimSuffix(filepath.Base(jobMDPath), ".md")
	return filepath.Join(filepath.Dir(jobMDPath), base+".state")
}

type jobDiskState struct {
	LastScheduledUTC string `json:"last_scheduled_utc,omitempty"`
}

func parseRFC3339Field(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// ReadJobState reads last scheduled fire time from a .state file.
func ReadJobState(path string) (time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	var st jobDiskState
	if err := json.Unmarshal(data, &st); err != nil {
		return time.Time{}, err
	}
	if t, ok := parseRFC3339Field(st.LastScheduledUTC); ok {
		return t, nil
	}
	return time.Time{}, nil
}

func marshalJobDiskState(st jobDiskState) ([]byte, error) {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func persistJobDiskState(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	_ = f.Chmod(0o644)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return err
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			cleanup()
			return err
		}
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// WriteJobState persists the last executed cron slot (UTC).
// It writes through a temp file in the same directory and renames into place so
// concurrent daemon ticks never read a truncated JSON file as an empty checkpoint.
// On Unix, rename replaces an existing destination atomically. On Windows the prior
// file is removed first because os.Rename cannot replace an existing path there.
func WriteJobState(path string, lastScheduled time.Time) error {
	st := jobDiskState{LastScheduledUTC: lastScheduled.UTC().Format(time.RFC3339)}
	data, err := marshalJobDiskState(st)
	if err != nil {
		return err
	}
	return persistJobDiskState(path, data)
}
