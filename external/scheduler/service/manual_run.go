//go:build scheduler

package schedservice

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// LaunchManualJob executes one asynchronous manual scheduler job (wired from package daemon via init).
var LaunchManualJob func(ctx context.Context, cfg *config.Config, log *slog.Logger, processCWD, absJobMD string, fm *storage.JobFrontmatter, instruction string) error

// TriggerJobRun starts an asynchronous scheduler run without advancing cron last-fire state.
// HTTP and schedtools call this path (not the daemon tick).
func (o *Service) TriggerJobRun(jobID string) error {
	if err := o.requireEnabled(); err != nil {
		return err
	}
	abs, err := o.jobAbsPath(jobID)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return ErrJobNotFound
		}
		return err
	}
	if lockOrTracked(abs) {
		return ErrJobBusy
	}
	fm, body, err := storage.ParseJobFile(abs)
	if err != nil {
		return err
	}
	if fm.Paused {
		return ErrJobPaused
	}
	if LaunchManualJob == nil {
		return ErrLauncherNotConfigured
	}
	logRef := o.slog()
	proc := strings.TrimSpace(o.ProcessCWD)
	fmCopy := *fm
	go func(f storage.JobFrontmatter, instruction string) {
		ff := f
		_ = LaunchManualJob(context.Background(), o.Cfg, logRef, proc, abs, &ff, instruction)
	}(fmCopy, body)
	return nil
}
