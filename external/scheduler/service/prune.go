//go:build scheduler

package schedservice

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type schedRunEntry struct {
	id      string
	dir     string
	endedAt string
}

// PruneSchedulerRunSessions removes oldest completed scheduler-run session directories for jobID, keeping retain most recent by schedulerEndedAt.
func PruneSchedulerRunSessions(fs *session.FileStore, jobID string, retain int) error {
	if fs == nil || fs.Root == "" || strings.TrimSpace(jobID) == "" || retain < 0 {
		return nil
	}
	if retain == 0 {
		retain = 5
	}
	de, err := os.ReadDir(fs.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var terminal []schedRunEntry
	for _, ent := range de {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		id := ent.Name()
		snap, err := fs.ReadSnapshot(id)
		if err != nil {
			continue
		}
		meta := snap.Meta
		if !meta.SchedulerRun || strings.TrimSpace(meta.SchedulerJobID) != strings.TrimSpace(jobID) {
			continue
		}
		if strings.TrimSpace(meta.SchedulerEndedAt) == "" {
			continue
		}
		status := strings.TrimSpace(meta.SchedulerStopStatus)
		if status == "running" {
			continue
		}
		terminal = append(terminal, schedRunEntry{id: id, dir: snap.Dir, endedAt: meta.SchedulerEndedAt})
	}
	if len(terminal) <= retain {
		return nil
	}
	sort.Slice(terminal, func(i, j int) bool {
		if terminal[i].endedAt == terminal[j].endedAt {
			return terminal[i].id < terminal[j].id
		}
		return terminal[i].endedAt < terminal[j].endedAt
	})
	remove := len(terminal) - retain
	for i := 0; i < remove; i++ {
		dir := terminal[i].dir
		if strings.TrimSpace(dir) == "" {
			dir = filepath.Join(fs.Root, terminal[i].id)
		}
		_ = os.RemoveAll(dir)
	}
	return nil
}
