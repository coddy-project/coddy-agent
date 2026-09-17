package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// ErrSchedulerSessionReadOnly is returned for any turn aimed at the session of
// a scheduler job. That session is the parent every run of the job is a child
// of and nothing else: it has no transcript of its own to continue.
var ErrSchedulerSessionReadOnly = errors.New("scheduler job sessions are read-only")

// SchedulerJobSessionSpec describes the job session the scheduler asks the
// manager for.
type SchedulerJobSessionSpec struct {
	// ID is the session id the job's sidecar records; minted by the scheduler
	// before the first run so the pointer exists before the bundle does.
	ID string
	// JobID is the scheduler job (the file basename under scheduler.dir).
	JobID string
	// CWD is the job's resolved working directory.
	CWD string
	// Title is pinned as the session title; the job id when empty.
	Title string
}

// readOnlyRefusal is the manager's one answer for a session no surface may
// start a turn on: a child transcript names the parent to prompt instead, a
// job session names the job it belongs to. Nil for an ordinary session.
func readOnlyRefusal(st *State, sessionID string) error {
	if st == nil {
		return nil
	}
	if st.IsSubagentRun() {
		return fmt.Errorf("%w: %s belongs to %s", ErrSubagentReadOnly, sessionID, subagentParentOf(st))
	}
	if st.IsSchedulerJob() {
		return fmt.Errorf("%w: %s is the session of scheduler job %q", ErrSchedulerSessionReadOnly, sessionID, st.GetSchedulerJobID())
	}
	return nil
}

// schedulerRunMetaFromSnapshot reads a run's origin back off its bundle, or
// nil for a child a model spawned.
func schedulerRunMetaFromSnapshot(meta SessionMeta) *SchedulerRunMeta {
	jobID := strings.TrimSpace(meta.SchedulerJobID)
	if jobID == "" {
		return nil
	}
	out := &SchedulerRunMeta{JobID: jobID, Trigger: strings.TrimSpace(meta.SchedulerTrigger)}
	if raw := strings.TrimSpace(meta.SchedulerFireSlot); raw != "" {
		if slot, err := time.Parse(time.RFC3339, raw); err == nil {
			out.FireSlot = slot.UTC()
		}
	}
	return out
}

// EnsureSchedulerJobSession returns the live session of a scheduler job: the
// live entry when there is one, the bundle loaded when one is on disk, a fresh
// bundle otherwise. A job session is built with nothing a turn would need -
// no skills, no rules, no MCP clients, no SessionStart hooks - because no turn
// ever runs on it; it exists so the job's runs have a parent whose bundle
// holds their transcripts and task records, and so the Tasks panel and the
// sessions API find them where they find every other child.
func (m *Manager) EnsureSchedulerJobSession(ctx context.Context, spec SchedulerJobSessionSpec) (*State, error) {
	if m.store == nil {
		return nil, fmt.Errorf("scheduler job sessions need session persistence")
	}
	id := strings.TrimSpace(spec.ID)
	if err := ValidateFolderSessionID(id); err != nil {
		return nil, err
	}
	jobID := strings.TrimSpace(spec.JobID)
	if jobID == "" {
		return nil, fmt.Errorf("scheduler job session needs a job id")
	}
	if existing := m.getSession(id); existing != nil {
		if !existing.IsSchedulerJob() {
			return nil, fmt.Errorf("session %s is live and is not the session of a scheduler job", id)
		}
		return existing, nil
	}
	if m.store.HasPersistedSnapshot(id) {
		if _, err := m.loadSessionFromDisk(ctx, acp.SessionLoadParams{SessionID: id}, true); err != nil {
			return nil, err
		}
		st := m.getSession(id)
		if st == nil {
			return nil, fmt.Errorf("session load incomplete: %s", id)
		}
		if !st.IsSchedulerJob() {
			m.ForgetLiveSession(id)
			return nil, fmt.Errorf("session %s exists and is not the session of a scheduler job", id)
		}
		return st, nil
	}

	cwd, err := EffectiveSessionCWD(spec.CWD, m.defaultCWD)
	if err != nil {
		return nil, fmt.Errorf("scheduler job session cwd: %w", err)
	}
	sessionDir, err := m.store.EnsureLayout(id)
	if err != nil {
		return nil, fmt.Errorf("scheduler job session layout: %w", err)
	}
	state := &State{
		ID:             id,
		CWD:            cwd,
		Mode:           ModeAgent,
		SessionDir:     sessionDir,
		contextWindows: m,
	}
	state.SetSchedulerJobWithoutPersist(jobID)
	title := strings.TrimSpace(spec.Title)
	if title == "" {
		title = jobID
	}
	state.SetTitlePinnedWithoutPersist(title)
	state.SetPersistHook(m.makePersist(state))

	m.mu.Lock()
	if occupied, ok := m.sessions[id]; ok {
		m.mu.Unlock()
		if occupied.IsSchedulerJob() {
			return occupied, nil
		}
		return nil, fmt.Errorf("session %s is live and is not the session of a scheduler job", id)
	}
	m.sessions[id] = state
	m.mu.Unlock()

	if err := m.store.Save(state); err != nil {
		m.ForgetLiveSession(id)
		return nil, fmt.Errorf("initial scheduler job session save: %w", err)
	}
	m.log.Info("scheduler job session created", "id", id, "job", jobID, "cwd", cwd)
	return state, nil
}
