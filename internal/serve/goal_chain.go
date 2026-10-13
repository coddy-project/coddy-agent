package serve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// errShuttingDown refuses a chained turn once the process stops.
var errShuttingDown = errors.New("the server is shutting down")

// runGoalChainTurn is the process's session.GoalChainStarter: it runs the
// first turn of a session a goal chain opened (internal/session/goal_chain.go).
//
// Nobody opened that session, so the turn runs the way a background wake does
// when no surface claims it: through the manager, mirrored into the HTTP
// relay so a browser that opens the session follows it live. The turn lock
// comes first, as in the HTTP server's own wake path: a relay registered
// before it would evict the watchers of a turn just admitted there. A busy
// session answers session.ErrSessionTurnBusy, which the chain tries again.
// Its prompts get the operator's standing answer (defaultSender): allowed
// when the session's permission mode allows the call, refused otherwise.
func (r *Runtime) runGoalChainTurn(ctx context.Context, params acp.SessionPromptParams, opts *session.PromptRunOpts) error {
	if r.Mgr == nil || bgtask.Default().Draining() {
		return errShuttingDown
	}
	sessionID := strings.TrimSpace(params.SessionID)
	st := r.Mgr.SessionByID(sessionID)
	if st == nil {
		return fmt.Errorf("%w: %s", session.ErrSessionGone, sessionID)
	}
	unlock, err := r.Mgr.AcquireComposerTurnLock(sessionID, st)
	if err != nil {
		return err
	}
	defer unlock()
	sender, release := r.MirrorTurn(sessionID, &defaultSender{live: r.Cfg})
	defer release()
	if opts == nil {
		opts = &session.PromptRunOpts{}
	}
	opts.SkipTurnLock = true
	_, err = r.Mgr.HandleSessionPromptWithSender(ctx, params, sender, opts)
	return err
}
