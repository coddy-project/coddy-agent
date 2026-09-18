package session

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// BackgroundWakeUpdate is the session update that stands for a woken turn's
// first message on the wire: the same tasks the persisted marker names, in the
// shape every other Coddy session update has.
func BackgroundWakeUpdate(wake *llm.BackgroundWake) acp.BackgroundWakeUpdate {
	u := acp.BackgroundWakeUpdate{SessionUpdate: acp.UpdateTypeBackgroundWake, Tasks: []acp.BackgroundWakeTask{}}
	if wake == nil {
		return u
	}
	for _, t := range wake.Tasks {
		u.Tasks = append(u.Tasks, acp.BackgroundWakeTask{
			ID:         t.ID,
			Kind:       t.Kind,
			Label:      t.Label,
			Agent:      t.Agent,
			Status:     t.Status,
			ExitCode:   t.ExitCode,
			DurationMs: t.DurationMs,
			Error:      t.Error,
		})
	}
	return u
}

// BackgroundWakeNote is the one-glance text of a wake, for a surface that can
// only show text - a chat, an editor that renders no Coddy update: "Woken by a
// finished background task: bg_3 make test, failed, exit 2, 1m 30s", one line
// per task when several ended together.
func BackgroundWakeNote(u acp.BackgroundWakeUpdate) string {
	if len(u.Tasks) == 1 {
		return "Woken by a finished background task: " + wakeTaskLine(u.Tasks[0])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Woken by %d finished background tasks:", len(u.Tasks))
	for _, t := range u.Tasks {
		b.WriteString("\n- ")
		b.WriteString(wakeTaskLine(t))
	}
	return b.String()
}

// wakeTaskLine names one task and says how it ended.
func wakeTaskLine(t acp.BackgroundWakeTask) string {
	parts := append([]string{strings.TrimSpace(t.ID + " " + strings.TrimSpace(t.Label))}, wakeOutcome(t)...)
	return strings.Join(parts, ", ")
}

// wakeOutcome is how one task of a wake ended, in the order a person reads it:
// the status in words, the exit code of a command and how long it ran -
// "failed", "exit 2", "1m 30s". An agent run has no process behind it, so the
// pool's exit code for it is left out.
func wakeOutcome(t acp.BackgroundWakeTask) []string {
	parts := []string{strings.ReplaceAll(strings.TrimSpace(t.Status), "_", " ")}
	if t.ExitCode != nil && t.Kind != "agent" {
		parts = append(parts, "exit "+strconv.Itoa(*t.ExitCode))
	}
	return append(parts, wakeDuration(t.DurationMs))
}

// wakeDuration renders how long a task ran the way a person says it: 45s,
// 1m 30s, 1h 5m.
func wakeDuration(ms int64) string {
	secs := (ms + 500) / 1000
	switch {
	case secs < 60:
		return strconv.FormatInt(secs, 10) + "s"
	case secs < 3600:
		m, s := secs/60, secs%60
		if s == 0 {
			return strconv.FormatInt(m, 10) + "m"
		}
		return strconv.FormatInt(m, 10) + "m " + strconv.FormatInt(s, 10) + "s"
	default:
		h, m := secs/3600, (secs%3600)/60
		if m == 0 {
			return strconv.FormatInt(h, 10) + "h"
		}
		return strconv.FormatInt(h, 10) + "h " + strconv.FormatInt(m, 10) + "m"
	}
}
