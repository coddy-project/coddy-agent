package serve

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
)

// wakeOffer is one surface that offered to run woken turns.
type wakeOffer struct {
	seq     uint64
	rank    agent.WakeRank
	surface agent.WakeSurface
}

// wakeSurfaces are the surfaces of the process that can run a woken turn,
// guarded by their own lock and withdrawn by the offer, like the approvers.
type wakeSurfaces struct {
	mu     sync.RWMutex
	offers map[uint64]wakeOffer
	seq    uint64
}

// AddWakeSurface implements agent.WakeSurfaces: a surface that came up offers
// to run the turns finished background tasks start, and withdraws the offer
// when it stops. An offer for the same reason the detached permission prompts
// are one: the surfaces come up, and are restarted, on their own schedule,
// while the waker is the process's from the start.
func (r *Runtime) AddWakeSurface(surface agent.WakeSurface, rank agent.WakeRank) (withdraw func()) {
	if surface == nil {
		return func() {}
	}
	r.wakes.mu.Lock()
	r.wakes.seq++
	id := r.wakes.seq
	if r.wakes.offers == nil {
		r.wakes.offers = make(map[uint64]wakeOffer)
	}
	r.wakes.offers[id] = wakeOffer{seq: id, rank: rank, surface: surface}
	r.wakes.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.wakes.mu.Lock()
			delete(r.wakes.offers, id)
			r.wakes.mu.Unlock()
		})
	}
}

var _ agent.WakeSurfaces = (*Runtime)(nil)

// attachWaker subscribes the process waker to the task pool. One process has
// one: whichever surfaces are enabled, a task started with a wake wakes
// the agent, and run_command and spawn_agent can promise it (Pool.CanWake).
func (r *Runtime) attachWaker() {
	agent.NewBackgroundWaker(r.Log, r.runBackgroundWake).Attach(bgtask.Default())
}

// runBackgroundWake runs one woken turn where it belongs.
//
// The surface that owns the conversation comes first - a chat bound to the
// session, whose person reads the answer there - then a surface that can host
// any session where watchers follow it (the HTTP server). With neither up, the
// turn still runs, through the manager, because the model was promised it: the
// transcript keeps the outcome for whoever opens the session next. Its
// permission prompts get the manager's default answer, the operator's standing
// one - refused unless the permission mode is bypass - since no surface is
// there to show them.
func (r *Runtime) runBackgroundWake(ctx context.Context, wake agent.Wake) error {
	r.wakes.mu.RLock()
	offers := make([]wakeOffer, 0, len(r.wakes.offers))
	for _, o := range r.wakes.offers {
		offers = append(offers, o)
	}
	r.wakes.mu.RUnlock()
	sort.Slice(offers, func(i, j int) bool {
		if offers[i].rank != offers[j].rank {
			return offers[i].rank < offers[j].rank
		}
		return offers[i].seq < offers[j].seq
	})
	for _, o := range offers {
		if handled, err := o.surface.RunBackgroundWake(ctx, wake); handled {
			return err
		}
	}

	if r.Mgr == nil || bgtask.Default().Draining() {
		return nil
	}
	sessionID := strings.TrimSpace(wake.SessionID)
	if r.Mgr.SessionByID(sessionID) == nil {
		if _, err := r.Mgr.HandleSessionLoad(ctx, acp.SessionLoadParams{SessionID: sessionID}); err != nil {
			return err
		}
	}
	sender, release := r.MirrorTurn(sessionID, &defaultSender{live: r.Cfg})
	defer release()
	_, err := r.Mgr.HandleSessionPromptWithSender(ctx, wake.PromptParams(), sender, wake.RunOpts())
	return err
}
