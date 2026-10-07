package agent

// The agent's side of a model a remote Coddy shares (provider type coddy).
//
// The provider owns the wait for a free stream slot of the remote and the
// resend after a stale_revision answer (internal/llm/coddy.go); this file hands
// it what only the agent and the session know - the wait budget, the revision
// of the capability cache entry and the way to refresh it - and shows the
// wait to the user. The first-token timer is not armed for such a row
// (llmTransport.firstTokenGuard): the remote applies its own stall guard from
// the start of the provider call and the client keeps the byte-level guard of
// its HTTP client.
// Design record: docs/plans/remote-model-provider.md, 4.1b and 4.3.

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// coddyProviderType is the provider type of a model a remote Coddy shares.
const coddyProviderType = "coddy"

// busyBlocker names the wait for a free stream slot in the blockers of its
// countdown, so a surface that shows the first blocker names the remote's
// slot limit and not a usage limit of an account.
const busyBlocker = "remote_busy"

// capabilityListing is what a session state offers a coddy row: the listing
// record the session manager cached for the model, and a refresh of it after
// the remote answered stale_revision. *session.State implements it; a state no
// manager built does not, and the row then sends no expected_revision and
// cannot refresh.
type capabilityListing interface {
	ProviderModelEntry(cfg *config.Config, providerName, apiModel string) (llm.ModelEntry, bool)
	RefreshProviderModelEntry(ctx context.Context, cfg *config.Config, providerName, apiModel, staleRevision string) (*llm.ModelEntry, error)
}

// coddyProviderInput fills what only a coddy row takes: the budget of the wait
// for a free stream slot, the revision of the listing record the request is
// checked against, and the refresh the provider calls when the remote says
// that revision is stale. The refresh reads the listing again through the
// session manager and updates its cache; it writes no key of the
// configuration.
func (a *Agent) coddyProviderInput(in *llm.ProviderInput, rm *config.ResolvedLLM) {
	in.BusyWait = a.cfg.EffectiveBusyWait(rm.ProviderName)
	listing, ok := a.state.(capabilityListing)
	if !ok {
		return
	}
	cfg, providerName, alias := a.cfg, rm.ProviderName, rm.Model
	if e, ok := listing.ProviderModelEntry(cfg, providerName, alias); ok {
		in.ExpectedRevision = e.Revision
	}
	in.RefreshCapabilities = func(ctx context.Context) (*llm.ModelEntry, error) {
		// The revision the cache holds now is the view the refresh must move
		// past: a read that still shows it describes the same row.
		stale := ""
		if e, ok := listing.ProviderModelEntry(cfg, providerName, alias); ok {
			stale = e.Revision
		}
		return listing.RefreshProviderModelEntry(ctx, cfg, providerName, alias, stale)
	}
}

// busyWaitNotice shows the wait of a call for a free stream slot as a
// provider_usage update that says resuming, the update a limit wait sends
// (limit_wait.go): Blocked with the time the wait ends at the latest, re-sent
// every limitWaitHeartbeat so the countdown stays in view. The blocker
// remote_busy tells a surface it is not a usage limit. Nothing replaces the
// update when the wait ends - a coddy row has no usage read at the end of a turn
// - so clear sends the answer a row without usage gives, which takes the line
// down, as soon as the provider reports the admission, from the first chunk of
// the stream at the latest, and when the call returns.
type busyWaitNotice struct {
	a            *Agent
	sessionID    string
	providerName string
	providerType string
	heartbeat    time.Duration

	mu       sync.Mutex
	lastSent time.Time
	shown    bool
}

// newBusyWaitNotice returns the notice of a call on transport, nil for any
// other provider type: nothing is shown and nothing changes for them.
func (a *Agent) newBusyWaitNotice(sessionID string, transport llmTransport) *busyWaitNotice {
	if transport.providerType != coddyProviderType {
		return nil
	}
	heartbeat := a.limitWaitHeartbeat
	if heartbeat <= 0 {
		heartbeat = limitWaitHeartbeat
	}
	return &busyWaitNotice{
		a: a, sessionID: sessionID, heartbeat: heartbeat,
		providerName: transport.providerName, providerType: transport.providerType,
	}
}

// report is the callback of llm.WithBusyWaitNotify: it runs before each sleep
// of the wait and must not block. The report that says the remote admitted the
// call ends the countdown on the spot - the stream that follows can run for
// minutes, and a banner that still says the call waits for a free slot would
// be wrong for all of them.
func (n *busyWaitNotice) report(st llm.BusyWaitStatus) {
	if n == nil {
		return
	}
	if st.Admitted {
		n.clear()
		return
	}
	now := time.Now()
	n.mu.Lock()
	if n.shown && now.Sub(n.lastSent) < n.heartbeat {
		n.mu.Unlock()
		return
	}
	n.shown, n.lastSent = true, now
	n.mu.Unlock()

	left := max(st.Remaining, 0)
	_ = n.a.server.SendSessionUpdate(n.sessionID, acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage,
		Provider:      n.providerName,
		ProviderType:  n.providerType,
		ObservedAt:    now.UTC().Format(time.RFC3339),
		FetchedAt:     now.UTC().Format(time.RFC3339),
		Blocked:       true,
		Blockers:      []string{busyBlocker},
		RetryAt:       now.Add(left).UTC().Format(time.RFC3339),
		RetryInSec:    int(math.Ceil(left.Seconds())),
		Resuming:      true,
	})
}

// clear takes the countdown down: when the remote admits the call, and again
// once the call has returned, whatever it returned. It is idempotent, nil-safe
// and does nothing when no wait is on screen.
func (n *busyWaitNotice) clear() {
	if n == nil {
		return
	}
	n.mu.Lock()
	shown := n.shown
	n.shown = false
	n.mu.Unlock()
	if !shown {
		return
	}
	_ = n.a.server.SendSessionUpdate(n.sessionID, acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage,
		Provider:      n.providerName,
		ProviderType:  n.providerType,
		Unsupported:   true,
	})
}
