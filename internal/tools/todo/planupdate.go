package todo

import (
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func sendPlanUpdate(env *tooling.Env, entries []acp.PlanEntry) {
	if env.Sender == nil {
		return
	}
	_ = env.Sender.SendSessionUpdate(env.SessionID, acp.PlanUpdate{
		SessionUpdate: acp.UpdateTypePlan,
		Entries:       entries,
	})
}
