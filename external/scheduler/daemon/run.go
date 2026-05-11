//go:build scheduler

package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/service"
	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/skills"
)

func randomSchedulerSessionID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "sched_err"
	}
	return "sched_" + hex.EncodeToString(b)
}

func resolveJobCWD(processCWD string, fm *storage.JobFrontmatter) (string, error) {
	base := strings.TrimSpace(processCWD)
	if base == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		base = wd
	}
	raw := strings.TrimSpace(fm.CWD)
	if raw == "" {
		return filepath.Clean(base), nil
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	return filepath.Clean(filepath.Join(base, raw)), nil
}

func parseSessionMode(s string) session.Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "agent":
		return session.ModeAgent
	case "plan":
		return session.ModePlan
	default:
		return session.ModeAgent
	}
}

func jobIDFromMDPath(abs string) string {
	return strings.TrimSuffix(filepath.Base(abs), ".md")
}

// RunJobFile executes one scheduler job (cron tick or manual). When updateLastScheduledState is true, fireSlot updates the .state checkpoint
// as soon as the run is committed (after initial session persist) so daemon poll ticks do not treat the same cron slot as still due while
// the agent turn is in progress or if the final checkpoint write at shutdown were to fail.
func RunJobFile(ctx context.Context, cfg *config.Config, log *slog.Logger, processCWD, jobPath string, fireSlot time.Time, updateLastScheduledState bool, fm *storage.JobFrontmatter, instruction string) error {
	if fm != nil && fm.Paused {
		return nil
	}
	absJob, err := filepath.Abs(jobPath)
	if err != nil {
		absJob = filepath.Clean(jobPath)
	} else {
		absJob = filepath.Clean(absJob)
	}
	jobID := jobIDFromMDPath(absJob)
	lock := storage.LockPath(absJob)
	stPath := storage.StatePath(absJob)

	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	_, _ = f.WriteString(time.Now().UTC().Format(time.RFC3339) + "\n")
	_ = f.Close()
	defer func() { _ = os.Remove(lock) }()

	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	schedservice.RegisterTrackedRun(absJob, cancel)
	defer schedservice.UnregisterTrackedRun(absJob)

	sessRoot := cfg.ResolvedSessionsRoot()
	fs := &session.FileStore{Root: sessRoot}
	sid := randomSchedulerSessionID()
	dir, err := fs.EnsureLayout(sid)
	if err != nil {
		log.Error("scheduler_run_session_layout", "job_id", jobID, "error", err)
		return err
	}
	started := time.Now().UTC().Format(time.RFC3339)

	jobCWD, err := resolveJobCWD(processCWD, fm)
	if err != nil {
		log.Error("scheduler_run_cwd", "job_id", jobID, "error", err)
		return err
	}

	loader := skills.NewLoader(cfg.Skills.Dirs)
	skillList, loadErr := loader.LoadAll(jobCWD, cfg.Paths.Home)
	if loadErr != nil {
		log.Warn("scheduler_run_skills", "job_id", jobID, "error", loadErr)
		skillList = nil
	}

	st := &session.State{
		ID:              sid,
		CWD:             jobCWD,
		Mode:            parseSessionMode(fm.Mode),
		SelectedModelID: strings.TrimSpace(fm.Model),
		Skills:          skillList,
		SessionDir:      dir,
	}
	st.SetSchedulerRunMeta(jobID, started)
	if saveErr := fs.Save(st); saveErr != nil {
		log.Error("scheduler_run_persist_meta", "job_id", jobID, "session_id", sid, "error", saveErr)
		return saveErr
	}

	if updateLastScheduledState {
		if werr := storage.WriteJobState(stPath, fireSlot); werr != nil {
			log.Warn("scheduler_run_state_write", "job_id", jobID, "path", stPath, "error", werr)
			return werr
		}
	}

	log.Info("scheduler_run_spawn", "job_id", jobID, "session_id", sid)

	timeout, err := time.ParseDuration(cfg.Scheduler.Timeout)
	if err != nil {
		timeout = 30 * time.Minute
	}
	runCtx, timeoutCancel := context.WithTimeout(jobCtx, timeout)
	defer timeoutCancel()

	var snd autoAllowSender
	text, stopReason, runErr := agent.RunScheduledTurn(runCtx, cfg, st, log, snd, instruction)
	_ = text

	status := "completed"
	switch {
	case errors.Is(runErr, context.Canceled), errors.Is(runCtx.Err(), context.Canceled):
		status = "cancelled"
	case runCtx.Err() != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded):
		status = "failed"
	case runErr != nil:
		status = "failed"
	case stopReason == string(acp.StopReasonCancelled):
		status = "cancelled"
	}

	ended := time.Now().UTC().Format(time.RFC3339)
	st.FinishSchedulerRun(ended, status)
	if saveErr := fs.Save(st); saveErr != nil {
		log.Error("scheduler_run_persist_final", "job_id", jobID, "session_id", sid, "error", saveErr)
	} else {
		if perr := schedservice.PruneSchedulerRunSessions(fs, jobID, cfg.SchedulerRetainSessionsEffective()); perr != nil {
			log.Warn("scheduler_run_prune", "job_id", jobID, "error", perr)
		}
	}

	switch status {
	case "completed":
		log.Info("scheduler_run_finish", "job_id", jobID, "session_id", sid, "status", status)
	case "cancelled":
		log.Info("scheduler_run_finish", "job_id", jobID, "session_id", sid, "status", status)
	default:
		errStr := ""
		if runErr != nil {
			errStr = runErr.Error()
			if len(errStr) > 200 {
				errStr = errStr[:200] + "..."
			}
		}
		log.Info("scheduler_run_finish", "job_id", jobID, "session_id", sid, "status", status, "error", errStr)
	}

	return runErr
}

// autoAllowSender drops streamed updates and allows every permission request (unattended scheduler runs).
type autoAllowSender struct{}

func (autoAllowSender) SendSessionUpdate(string, interface{}) error { return nil }

func (autoAllowSender) RequestPermission(_ context.Context, _ acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}
