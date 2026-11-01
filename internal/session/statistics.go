package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/statistics"
)

func (m *Manager) Statistics() *statistics.Store {
	return statistics.New(m.activeCfg().Paths.Home)
}

// StartStatisticsImport takes a process-wide lease and returns immediately.
// Stable ids and the first live turn's start make retries safe while new
// conversations continue running. It reads every surface's bundles.
func (m *Manager) StartStatisticsImport() error {
	s := m.Statistics()
	if m.store == nil {
		return fmt.Errorf("session storage is unavailable")
	}
	if _, err := s.Settings(); err != nil {
		return err
	}
	release, acquired, err := platform.TryLockFile(filepath.Join(s.Dir, ".import.lock"))
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}
	if err := s.UpdateSettings(func(st *statistics.Settings) error { st.Import = statistics.ImportStatus{State: "running"}; return nil }); err != nil {
		release()
		return err
	}
	go func() {
		defer release()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		status := statistics.ImportStatus{State: "running"}
		defer func() {
			if status.Error == "" {
				status.State = "complete"
			} else {
				status.State = "error"
			}
			if err := s.UpdateSettings(func(st *statistics.Settings) error { st.Import = status; return nil }); err != nil {
				m.log.Warn("statistics import status could not be saved", "error", err)
			}
		}()
		rows, err := m.store.ListSnapshotsWith(ListOptions{IncludeSchedulerRuns: true, IncludeSubagents: true, IncludePrintRuns: true, IncludeEmpty: true, Archived: ArchiveAll})
		if err != nil {
			status.Error = err.Error()
			return
		}
		status.Total = len(rows)
		importBefore := time.Now().UTC()
		live, err := s.Events()
		if err != nil {
			status.Error = err.Error()
			return
		}
		firstLive := map[string]time.Time{}
		for _, e := range live {
			if e.Imported {
				continue
			}
			at := e.StartedAt
			if at.IsZero() {
				at = e.At
			}
			if before, ok := firstLive[e.Session]; !ok || at.Before(before) {
				firstLive[e.Session] = at
			}
		}
		for _, row := range rows {
			if ctx.Err() != nil {
				status.Error = ctx.Err().Error()
				return
			}
			before := importBefore
			if at, ok := firstLive[row.SessionID]; ok && at.Before(before) {
				before = at
			}
			if err := m.importStatisticsSession(s, row.SessionID, before); err != nil {
				status.Failed++
				m.log.Warn("statistics could not import a chat", "session", row.SessionID, "error", err)
			}
			status.Done++
			if err := s.UpdateSettings(func(st *statistics.Settings) error { st.Import = status; return nil }); err != nil {
				status.Error = err.Error()
				return
			}
		}
	}()
	return nil
}

func (m *Manager) StatisticsImportStatus() (statistics.Settings, error) {
	s := m.Statistics()
	st, err := s.Settings()
	if err != nil || st.Import.State != "running" {
		return st, err
	}
	release, acquired, err := platform.TryLockFile(filepath.Join(s.Dir, ".import.lock"))
	if err != nil {
		return st, err
	}
	if acquired {
		defer release()
		st.Import.State = "interrupted"
		err = s.UpdateSettings(func(v *statistics.Settings) error {
			if v.Import.State == "running" {
				v.Import.State = "interrupted"
			}
			return nil
		})
	}
	return st, err
}

func (m *Manager) importStatisticsSession(s *statistics.Store, id string, before time.Time) error {
	snap, err := m.store.ReadSnapshot(id)
	if err != nil {
		return err
	}
	base := statistics.Event{Session: id, Project: snap.Meta.CWD, Source: snap.Meta.Origin, Imported: true}
	if base.Source == "" {
		base.Source = "local"
	}
	if snap.Meta.SubagentRun {
		base.Source = "subagent"
	}
	if snap.Meta.SchedulerRun {
		base.Source = "scheduler"
	}
	for i, msg := range snap.Messages {
		if msg.Role != llm.RoleUser {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, msg.CreatedAt)
		if err != nil || (!before.IsZero() && !at.Before(before)) {
			continue
		}
		e := base
		e.Kind = "turn"
		if msg.FollowUp {
			e.Kind = "request"
		}
		e.ID = fmt.Sprintf("import:%s:turn:%s:%d", id, msg.CreatedAt, i)
		e.At = at
		e.Requests = 1
		if msg.BackgroundWake != nil || (msg.GoalTurn != nil && msg.GoalTurn.Kind != acp.GoalTurnKickoff) {
			e.Source = "background"
		}
		if e.Source == "subagent" || e.Source == "memory" || e.Source == "scheduler" || e.Source == "background" {
			e.Requests = 0
		}
		// Only the assistant message itself identifies the model that actually
		// answered; current session settings cannot recover an old selection.
		for j := i + 1; j < len(snap.Messages) && snap.Messages[j].Role != llm.RoleUser; j++ {
			if v := snap.Messages[j].Model; v != "" {
				e.Provider, e.Model, _ = strings.Cut(v, "/")
				break
			}
		}
		if err := s.Append(e); err != nil {
			return err
		}
	}
	// Old stats.json contains only the most recently saved invocation, not
	// a lifetime total. Import that known sample once at its recorded time.
	st, err := ReadSessionStats(snap.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	at, err := time.Parse(time.RFC3339Nano, st.UpdatedAt)
	if err != nil || (!before.IsZero() && !at.Before(before)) {
		return nil
	}
	if st.TokenUsageTotal.InputTokens == 0 && st.TokenUsageTotal.OutputTokens == 0 {
		return nil
	}
	e := base
	e.Kind = "sample"
	e.ID = "import:" + id + ":usage"
	e.At = at
	e.Input = statistics.Int(int64(st.TokenUsageTotal.InputTokens))
	e.Output = statistics.Int(int64(st.TokenUsageTotal.OutputTokens))
	return s.Append(e)
}

// StatisticsSkillCount is an inventory, not an estimate of historical use.
func (m *Manager) StatisticsSkillCount(cwd string) int {
	loader := m.skillsLoad
	loaded, err := loader.LoadAll(cwd, m.activeCfg().Paths.Home)
	if err != nil {
		return 0
	}
	return len(loaded)
}
