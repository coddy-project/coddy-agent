package agent

import (
	"context"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/memory"
)

func (a *Agent) runMemoryRecall(ctx context.Context, userText string) {
	if !a.cfg.Memory.Enabled {
		return
	}
	mr := strings.TrimSpace(a.cfg.Memory.Model)
	if mr == "" {
		mr = a.state.EffectiveModelID(a.cfg)
	}
	block, err := memory.RunRecall(ctx, a.log, a.cfg, a.state.GetCWD(), userText, mr)
	if err != nil {
		a.log.Warn("memory recall", "error", err)
		return
	}
	if strings.TrimSpace(block) != "" {
		a.state.SetMemoryCopilotBlock(block)
	}
}

func (a *Agent) runMemoryPersist(ctx context.Context, userText, assistant string) {
	if !a.cfg.Memory.Enabled {
		return
	}
	mr := strings.TrimSpace(a.cfg.Memory.Model)
	if mr == "" {
		mr = a.state.EffectiveModelID(a.cfg)
	}
	if err := memory.RunPersist(ctx, a.log, a.cfg, a.state.GetCWD(), mr, userText, assistant); err != nil {
		a.log.Warn("memory persist", "error", err)
	}
}
