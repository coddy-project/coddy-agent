package agent

// The agent's side of a model a remote Coddy shares (provider type coddy).
//
// The provider owns the wait for a free stream slot of the remote and the
// resend after a stale_revision answer (internal/llm/coddy.go); this file hands
// it what only the agent and the session know - the wait budget, the way to
// refresh the capability cache entry, and for each request the level and the
// revision of one record of that entry - and shows the wait to the user. The
// first-token timer is not armed for such a row (llmTransport.firstTokenGuard):
// the remote applies its own stall guard from the start of the provider call
// and the client keeps the byte-level guard of its HTTP client.
// Design record: docs/plans/remote-model-provider.md, 4.1b and 4.3, and
// docs/plans/remote-model-provider-phase2.md, 3.3 (the one record of a request)
// and 4.6 (the countdown and its end).

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
// for a free stream slot, and the refresh the provider calls when the remote
// says the revision a request names is stale. The refresh reads the listing
// again through the session manager and updates its cache; it writes no key of
// the configuration. The revision itself is not set here: it and the level
// belong to one request and are set together by coddyRequestView, which the
// callers that build a request (getProvider, withChildFallbacks) run after the
// level is known.
func (a *Agent) coddyProviderInput(in *llm.ProviderInput, rm *config.ResolvedLLM) {
	in.BusyWait = a.cfg.EffectiveBusyWait(rm.ProviderName)
	listing, ok := a.state.(capabilityListing)
	if !ok {
		return
	}
	cfg, providerName, alias := a.cfg, rm.ProviderName, rm.Model
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

// coddyRequestView sets the two things one request of a coddy row carries from
// the remote's listing, the level it asks for and the revision it is checked
// against, from ONE read of the listing record: a refresh that lands between
// the reads of the two cannot make a request that names the new revision carry
// a level the new record does not offer (the split read of level and revision
// is what the decided model of p2-d1-config-source refutes).
//
// effort is the session's level as EffectiveReasoning resolved it. When the
// row writes neither reasoning_levels nor allow_reasoning_off, its levels and
// its off switch are inherited from the listing, so a level the record no longer
// offers falls back to the record's default (llm.FallbackReasoningEffort). When
// the row writes either key the choices are the operator's own and the level is
// sent as it is: the remote answers invalid_option for one it does not offer,
// and no refresh fixes a key someone wrote (plan 3.8, item 2). With no record
// (the listing never answered, dropped the alias, or the session has no
// manager) the level goes as it is and no revision is named.
func (a *Agent) coddyRequestView(in *llm.ProviderInput, rm *config.ResolvedLLM, effort string) {
	in.ReasoningEffort = effort
	listing, ok := a.state.(capabilityListing)
	if !ok {
		return
	}
	record, ok := listing.ProviderModelEntry(a.cfg, rm.ProviderName, rm.Model)
	if !ok {
		return
	}
	in.ExpectedRevision = record.Revision
	if a.coddyRowInheritsChoices(rm) {
		in.ReasoningEffort = llm.FallbackReasoningEffort(effort, &record)
	}
}

// coddyRowInheritsChoices reports whether the models[] row behind rm leaves
// both the reasoning levels and the off switch to the remote's listing: it
// writes neither reasoning_levels nor allow_reasoning_off (an explicit false and
// an explicit empty list are written keys).
func (a *Agent) coddyRowInheritsChoices(rm *config.ResolvedLLM) bool {
	ent := a.cfg.FindModelEntry(rm.ProviderName + "/" + rm.Model)
	return ent != nil && ent.ReasoningLevels == nil && ent.AllowReasoningOff == nil
}

// busyWaitNotice shows the wait of a call for a free stream slot as a
// provider_usage update that says resuming, the update a limit wait sends
// (limit_wait.go): Blocked with the time the wait ends at the latest, re-sent
// every limitWaitHeartbeat so the countdown stays in view. The blocker
// remote_busy tells a surface it is not a usage limit, and marks the update as
// belonging to the countdown of this call and no snapshot. When the wait is over
// - the remote admitted the call, it failed, the turn was cancelled - clear
// sends the end of the countdown: the same blocker with Resuming absent and
// Blocked false, stamped by the clock at the moment of sending (strictly after
// every earlier stamp of the process, see stampGenerator). It carries no
// usage data and no Unsupported (a surface reads that as "drop this row's
// usage"), and the agent reads no usage cache to make it. Both name the alias
// the row shares the model under (acp.ProviderUsageUpdate.Model), so one
// alias's countdown never shows or ends another's (plan 4.6).
type busyWaitNotice struct {
	a            *Agent
	sessionID    string
	providerName string
	providerType string
	// model is the alias: rm.Model of the resolved row, the part of
	// llmTransport.model (the provider/alias selector) after the provider name.
	model     string
	heartbeat time.Duration

	mu       sync.Mutex
	lastSent time.Time
	shown    bool
}

// stampGenerator hands out the stamps (FetchedAt and ObservedAt) of the busy
// countdown and of its end. A stamp is RFC 3339 at one second of resolution and
// a surface compares it strictly against the tombstone the previous end left, so
// two updates in one wall second would let a new countdown lose to an old end
// (a quick answer, an instant tool or a busy remote puts them there). The
// generator therefore never repeats or steps back: a stamp is the clock's second
// or, when that is not after the previous stamp, one second after it. It runs
// ahead of the clock only as long as stamps come faster than one a second.
type stampGenerator struct {
	mu   sync.Mutex
	last time.Time
}

// next returns the stamp for an update made at now, RFC 3339 in UTC.
func (g *stampGenerator) next(now time.Time) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	stamp := now.UTC().Truncate(time.Second)
	if !stamp.After(g.last) {
		stamp = g.last.Add(time.Second)
	}
	g.last = stamp
	return stamp.Format(time.RFC3339)
}

// busyStamps is the process-wide generator: every countdown and end of every
// session draws from it, so no two of them share a stamp.
var busyStamps stampGenerator

// nextBusyStamp is the stamp of the busy update made at now.
func nextBusyStamp(now time.Time) string { return busyStamps.next(now) }

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
	// The selector is provider/alias and the alias may hold slashes itself;
	// SplitModelRef cuts at the first one, as ResolveLLM does for rm.Model.
	_, alias, _ := config.SplitModelRef(transport.model)
	return &busyWaitNotice{
		a: a, sessionID: sessionID, heartbeat: heartbeat,
		providerName: transport.providerName, providerType: transport.providerType,
		model: alias,
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
	stamp := nextBusyStamp(now)
	_ = n.a.server.SendSessionUpdate(n.sessionID, acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage,
		Provider:      n.providerName,
		ProviderType:  n.providerType,
		Model:         n.model,
		ObservedAt:    stamp,
		FetchedAt:     stamp,
		Blocked:       true,
		Blockers:      []string{busyBlocker},
		RetryAt:       now.Add(left).UTC().Format(time.RFC3339),
		RetryInSec:    int(math.Ceil(left.Seconds())),
		Resuming:      true,
	})
}

// clear ends the countdown: when the remote admits the call, and again once the
// call has returned, whatever it returned (an error and a cancel included). It
// is idempotent, nil-safe and does nothing when no wait is on screen. The end
// is stamped by the clock now (through the stamp generator, so it is strictly
// newer than every countdown before it), never by a cache's fetchedAt.
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
	stamp := nextBusyStamp(time.Now())
	_ = n.a.server.SendSessionUpdate(n.sessionID, acp.ProviderUsageUpdate{
		SessionUpdate: acp.UpdateTypeProviderUsage,
		Provider:      n.providerName,
		ProviderType:  n.providerType,
		Model:         n.model,
		ObservedAt:    stamp,
		FetchedAt:     stamp,
		Blockers:      []string{busyBlocker},
	})
}
