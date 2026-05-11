//go:build scheduler

// Package schedtools registers coddy_scheduler_* tools when scheduler is enabled.
package schedtools

import (
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// RegisterTools registers scheduler maintenance tools (requires cfg.Scheduler enabled).
func RegisterTools(reg func(*tooling.Tool), cfg *config.Config) {
	if cfg == nil || !cfg.SchedulerEffectiveEnabled() {
		return
	}
	reg(jobsListTool(cfg))
	reg(jobGetTool(cfg))
	reg(jobPauseTool(cfg))
	reg(jobResumeTool(cfg))
	reg(jobCreateTool(cfg))
	reg(jobReplaceTool(cfg))
	reg(jobPatchTool(cfg))
	reg(jobDeleteTool(cfg))
	reg(jobRunTool(cfg))
	reg(jobCancelTool(cfg))
	reg(jobRunsTool(cfg))
}
