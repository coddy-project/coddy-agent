//go:build cli

package cli

// Operator gates: the permission and question modals a worker blocks on.
//
// A background subagent asks whenever it needs to - between turns, or while a
// turn's own prompt is already on screen - so a gate that arrives while another
// is open waits its turn instead of replacing it, which would leave the first
// asker blocked with nothing left to answer. A gate whose asker gave up (its
// turn was cancelled, the subagent was stopped, another surface answered first)
// is taken down, or skipped if it never reached the screen.

import (
	"context"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
)

// pendingGate is a permission or question modal waiting for the one on screen.
type pendingGate struct {
	ctx  context.Context
	open func()
}

// gateWithdrawn is an internal loop message: the asker behind a gate gave up.
type gateWithdrawn struct{}

func isGateModal(c tui.Component) bool {
	switch c.(type) {
	case *permissionModal, *questionModal:
		return true
	}
	return false
}

// showGate opens a gate now, or queues it behind the gate already on screen. A
// selector is replaced as before: a blocked asker outranks a menu the operator
// can open again.
func (a *App) showGate(ctx context.Context, open func()) {
	if isGateModal(a.modal) {
		a.gates = append(a.gates, pendingGate{ctx: ctx, open: open})
		return
	}
	a.openGate(ctx, open)
}

func (a *App) openGate(ctx context.Context, open func()) {
	a.gateCtx = ctx
	if ctx != nil {
		a.gateStop = context.AfterFunc(ctx, func() {
			select {
			case a.updatesCh <- updateMsg{update: gateWithdrawn{}}:
			case <-a.closed:
			}
		})
	}
	open()
}

// releaseGate forgets the gate on screen; closeModal calls it for every modal.
func (a *App) releaseGate() {
	if a.gateStop != nil {
		a.gateStop()
		a.gateStop = nil
	}
	a.gateCtx = nil
}

// openNextGate shows the oldest queued gate whose asker is still waiting.
func (a *App) openNextGate() {
	if isGateModal(a.modal) {
		return
	}
	for len(a.gates) > 0 {
		g := a.gates[0]
		a.gates = a.gates[1:]
		if g.ctx != nil && g.ctx.Err() != nil {
			continue
		}
		a.openGate(g.ctx, g.open)
		return
	}
}

// dropAbandonedGate takes the gate on screen down once its asker has given up.
// The asker already returned through its context, so nobody reads an answer.
func (a *App) dropAbandonedGate() {
	if !isGateModal(a.modal) || a.gateCtx == nil || a.gateCtx.Err() == nil {
		return
	}
	a.closeModal()
}

// RequestDetachedPermission implements agent.DetachedPermissionBroker for the
// console: a background subagent whose parent turn has ended asks through the
// same modal as every other prompt, with its name in the title. The console
// stays open between turns, so while it runs there is somebody to ask; the
// child's own stamped mode still decides, so a child narrowed to bypass is not
// asked at all.
func (a *App) RequestDetachedPermission(ctx context.Context, req agent.DetachedPermissionRequest) (*acp.PermissionResult, error) {
	return a.Sender().RequestPermission(ctx, req.Params)
}

var _ agent.DetachedPermissionBroker = (*App)(nil)
