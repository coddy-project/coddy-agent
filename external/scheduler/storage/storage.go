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
	"sort"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

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

// DueFireSlotUTC returns the cron instant the daemon should treat as due at wall-clock now.
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
			if _, ok := seen[ap]; ok {
				continue
			}
			seen[ap] = struct{}{}
			out = append(out, ap)
		}
	}
	sort.Strings(out)
	return out, nil
}

// LockPath returns the lock file path for a job .md path.
func LockPath(jobMDPath string) string {
	base := strings.TrimSuffix(filepath.Base(jobMDPath), ".md")
	return filepath.Join(filepath.Dir(jobMDPath), base+".lock")
}

// StatePath returns the state file path for a job .md path.
func StatePath(jobMDPath string) string {
	base := strings.TrimSuffix(filepath.Base(jobMDPath), ".md")
	return filepath.Join(filepath.Dir(jobMDPath), base+".state")
}

type jobDiskState struct {
	LastScheduledUTC string `json:"last_scheduled_utc"`
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
	if strings.TrimSpace(st.LastScheduledUTC) == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(st.LastScheduledUTC))
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// WriteJobState persists the last executed cron slot (UTC).
// It writes through a temp file in the same directory and renames into place so
// concurrent daemon ticks never read a truncated JSON file as an empty checkpoint.
func WriteJobState(path string, lastScheduled time.Time) error {
	st := jobDiskState{LastScheduledUTC: lastScheduled.UTC().Format(time.RFC3339)}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
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
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
