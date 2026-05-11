//go:build scheduler

package schedservice

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/scheduler/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// Service centralizes scheduler job CRUD, run tracking, and HTTP or tool adapters.
type Service struct {
	Cfg        *config.Config
	Log        *slog.Logger
	ProcessCWD string
}

func NewService(cfg *config.Config, log *slog.Logger, processCWD string) *Service {
	return &Service{Cfg: cfg, Log: log, ProcessCWD: processCWD}
}

func (o *Service) slog() *slog.Logger {
	if o == nil || o.Log == nil {
		return slog.Default()
	}
	return o.Log
}

func (o *Service) requireEnabled() error {
	if o == nil || o.Cfg == nil || !o.Cfg.SchedulerEffectiveEnabled() {
		return ErrSchedulerDisabled
	}
	return nil
}

func jobIDFromMDPath(abs string) string {
	return strings.TrimSuffix(filepath.Base(abs), ".md")
}

func (o *Service) jobAbsPath(jobID string) (string, error) {
	if err := ValidateJobID(jobID); err != nil {
		return "", err
	}
	roots := o.Cfg.SchedulerScanRoots()
	if len(roots) == 0 || strings.TrimSpace(roots[0]) == "" {
		return "", fmt.Errorf("scheduler.dir is empty")
	}
	return filepath.Join(filepath.Clean(roots[0]), jobID+".md"), nil
}

func lockOrTracked(abs string) bool {
	if _, err := os.Stat(storage.LockPath(abs)); err == nil {
		return true
	}
	return IsTrackedJob(abs)
}
