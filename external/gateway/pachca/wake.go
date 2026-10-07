//go:build gateway || gateway.pachca

package pachca

// Woken turns in a chat: a background task the chat's agent started with
// notify_on_finish wakes the session when it ends, and the turn runs here,
// through the chat's own sender, so the answer lands where the work was asked
// for. A direct conversation is addressed through the person, since a Pachca
// direct chat id is not the person's id.

import (
	"context"
	"errors"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/access"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// RunBackgroundWake implements agent.WakeSurface.
func (b *Bot) RunBackgroundWake(ctx context.Context, wake agent.Wake) (bool, error) {
	c := b.connectedClient()
	if c == nil {
		return false, nil
	}
	key, ok := b.store.KeyFor(strings.TrimSpace(wake.SessionID))
	if !ok {
		return false, nil
	}
	target, ok := targetForKey(key)
	if !ok {
		return false, nil
	}
	if !b.beginTurn() {
		return false, nil
	}
	defer b.inFlight.Done()
	return true, b.runWokenTurn(ctx, c, target, wake)
}

func (b *Bot) runWokenTurn(ctx context.Context, c *Client, target Target, wake agent.Wake) error {
	ctx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	st, err := b.runner.EnsureHTTPSession(ctx, wake.SessionID, b.cwd)
	if err != nil {
		b.log.Warn("pachca: woken turn: ensure session", "err", err, "session", wake.SessionID)
		return err
	}
	sender := b.newSender(ctx, c, target, 0)
	mirrored, releaseMirror := session.Mirror(b.mirror, st.GetID(), sender)
	defer releaseMirror()

	b.log.Debug("pachca: woken turn", "session", st.GetID(), "target", target.EntityID, "tasks", len(wake.Tasks))
	opts := wake.RunOpts()
	opts.SurfaceSystemPrompt = surfaceSystemPrompt()
	// Nobody typed this turn: it runs with the rights of whoever the
	// conversation belongs to, and a shared group's belongs to everybody.
	if key, ok := b.store.KeyFor(st.GetID()); !ok || !access.KeyIsAdmin(key, b.cfg) {
		opts.Restriction = access.NonAdminTurn()
		sender.refuseApprovals = true
	}
	result, err := b.runner.HandleSessionPromptWithSender(ctx, wake.PromptParams(), mirrored, opts)
	if errors.Is(err, session.ErrSessionTurnBusy) {
		// The person's own turn is still running: the waker asks again.
		return err
	}
	sender.Flush()
	if err != nil {
		b.log.Warn("pachca: woken turn failed", "err", err, "session", st.GetID())
		b.reply(ctx, c, target, 0, "Agent error: "+err.Error())
		return err
	}
	stopReason := ""
	if result != nil {
		stopReason = string(result.StopReason)
	}
	b.log.Debug("pachca: woken turn done", "session", st.GetID(), "stop_reason", stopReason)
	return nil
}

var _ agent.WakeSurface = (*Bot)(nil)
