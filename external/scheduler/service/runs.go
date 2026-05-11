//go:build scheduler

package schedservice

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// orphanLockCancelMinAge avoids racing a brand-new lock created milliseconds ago on the same tick.
const orphanLockCancelMinAge = 800 * time.Millisecond

// tryRemoveOrphanSchedulerLock drops basename.lock when no tracked in-process run exists
// and the lock is not brand new. Used so POST …/cancel clears zombie locks after crashes.
func tryRemoveOrphanSchedulerLock(abs string) bool {
	if IsTrackedJob(abs) {
		return false
	}
	lp := storage.LockPath(abs)
	fi, err := os.Stat(lp)
	if err != nil {
		return false
	}
	if time.Since(fi.ModTime()) < orphanLockCancelMinAge {
		return false
	}
	return os.Remove(lp) == nil
}

// CancelJobRun asks the active run for this job path to cancel.
func (o *Service) CancelJobRun(jobID string) (cancelled bool, err error) {
	if err := o.requireEnabled(); err != nil {
		return false, err
	}
	abs, err := o.jobAbsPath(jobID)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return false, ErrJobNotFound
		}
		return false, err
	}
	if CancelTrackedRun(abs) {
		return true, nil
	}
	if tryRemoveOrphanSchedulerLock(abs) {
		return true, nil
	}
	return false, nil
}

// ListJobRuns returns persisted scheduler run sessions for jobID from sessions.dir.
func (o *Service) ListJobRuns(jobID string, limit int) ([]SchedulerRunEntry, error) {
	if err := o.requireEnabled(); err != nil {
		return nil, err
	}
	absJob, err := o.jobAbsPath(jobID)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(absJob); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrJobNotFound
		}
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	fs := &session.FileStore{Root: o.Cfg.ResolvedSessionsRoot()}
	if fs.Root == "" {
		return nil, fmt.Errorf("sessions root empty")
	}
	de, err := os.ReadDir(fs.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return []SchedulerRunEntry{}, nil
		}
		return nil, err
	}
	jobID = strings.TrimSpace(jobID)
	var entries []SchedulerRunEntry
	for _, ent := range de {
		if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		snap, err := fs.ReadSnapshot(ent.Name())
		if err != nil {
			continue
		}
		m := snap.Meta
		if !m.SchedulerRun || strings.TrimSpace(m.SchedulerJobID) != jobID {
			continue
		}
		entries = append(entries, SchedulerRunEntry{
			SessionID: m.ID,
			StartedAt: strings.TrimSpace(m.SchedulerStartedAt),
			EndedAt:   strings.TrimSpace(m.SchedulerEndedAt),
			Status:    strings.TrimSpace(m.SchedulerStopStatus),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].StartedAt > entries[j].StartedAt
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}
