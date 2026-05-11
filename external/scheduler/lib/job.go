//go:build scheduler

package scheduler

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
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
func NextScheduledUTC(sched cron.Schedule, lastFiredSlot time.Time) time.Time {
	t := lastFiredSlot
	if t.IsZero() {
		t = CronEpoch()
	}
	return sched.Next(t).UTC()
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
